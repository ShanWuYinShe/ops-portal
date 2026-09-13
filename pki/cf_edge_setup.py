#!/usr/bin/env python3
"""ops.yinghuoren.com 边缘规则自动化（幂等，已实测可用）。

token 权限要求（cf_account_token 已满足 Transform 部分）：
  - Zone | Transform Rules | Edit  → 建透传规则（必需）
  - Zone | Firewall Services | Edit → 建 WAF 拦截规则（可选加固；
    cf_account_token 无此权限，WAF 部分失败不影响透传）

用法：
  CF_RULES_TOKEN=$(cat ~/cf_token_and_cert/cf_account_token) \
    https_proxy=http://127.0.0.1:10809 python3 pki/cf_edge_setup.py

做两件事：
  1. http_request_late_transform（Request Header Transform）：ops 域名逐请求
     注入 X-Cert-Verify/Serial/Subject/Fingerprint。
     注意 headers 是对象（header名 → {operation, expression}），不是数组。
  2. （可选）http_request_firewall_custom：无证/吊销证 Block + 发卡方 SKI pin。
  同名规则先删后建，重复执行安全。无 WAF 规则时无证请求打到源站，
  由源站本地证书表拒绝（403），安全性不降，仅少一层边缘拦截。
"""

import json
import os
import sys
import urllib.request

TOKEN = os.environ.get("CF_RULES_TOKEN", "")
ZONE = os.environ.get("CF_ZONE_ID", "3c681cb3e32fb42f0fb9ddd38dfa858f")
HOSTS = [h.strip() for h in os.environ.get(
    "OPS_DOMAINS", "ops.yinghuoren.com").split(",") if h.strip()]
ISSUER_SKI = os.environ.get("CF_ISSUER_SKI", "F275E69E4459913F80F4D9A36BA5AE9129C721C6")

TRANSFORM_DESC = "ops-cert-passthrough"
WAF_DESC = "ops-mtls-required"

# 官方格式：headers 为对象（rules/transform/request-header-modification）。
TRANSFORM_HEADERS = {
    "X-Cert-Verify": {"operation": "set", "expression": "cf.tls_client_auth.cert_verified"},
    "X-Cert-Serial": {"operation": "set", "expression": "cf.tls_client_auth.cert_serial"},
    "X-Cert-Subject": {"operation": "set", "expression": "cf.tls_client_auth.cert_subject_dn"},
    "X-Cert-Fingerprint": {"operation": "set", "expression": "cf.tls_client_auth.cert_fingerprint_sha256"},
}

_HOST_SET = " ".join(f'"{h}"' for h in HOSTS)
_HOST_EXPR = f"http.host in {{ {_HOST_SET} }}"

# 无证时 issuer_ski 为空串：SKI 条件必须用 ne 放在 or 链里，
# 否则 eq 恒假导致无证请求不匹配、规则失效（实测踩坑）。
# SKI 用大写（CF 官方示例格式）。
# /api/agent/* 豁免边缘 mTLS：agent 无设备证书、以独立 bearer token 鉴权
# （一次性 enroll 换证），不豁免会让外连 agent 在边缘被拦（实测踩坑）。
WAF_EXPRESSION = (
    f"({_HOST_EXPR} and "
    f'not starts_with(http.request.uri.path, "/api/agent/") and ('
    f"not cf.tls_client_auth.cert_verified "
    f"or cf.tls_client_auth.cert_revoked "
    f'or cf.tls_client_auth.cert_issuer_ski ne "{ISSUER_SKI}"))'
)


def api(method, path, body=None):
    url = f"https://api.cloudflare.com/client/v4{path}"
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method, headers={
        "Authorization": f"Bearer {TOKEN}",
        "Content-Type": "application/json",
    })
    try:
        with urllib.request.urlopen(req, timeout=30) as resp:
            return json.load(resp)
    except urllib.error.HTTPError as e:
        try:
            return json.load(e)
        except Exception:
            return {"success": False, "errors": [{"message": str(e)}]}


def ensure_rule(phase, description, rule, required=True):
    """entrypoint 幂等合并：删同名 → 追加 → PUT 全量。"""
    got = api("GET", f"/zones/{ZONE}/rulesets/phases/{phase}/entrypoint")
    if got.get("success"):
        rules = [r for r in got["result"].get("rules", [])
                 if r.get("description") != description]
    elif any(e.get("code") == "0956010" or "could not find entrypoint" in
             str(e.get("message", "")) for e in got.get("errors", [])):
        rules = []  # entrypoint 尚不存在，PUT 直接建
    else:
        level = "FAIL" if required else "SKIP(可选，无权限)"
        print(f"{level} {phase}: GET {got.get('errors')}")
        return not required

    rules.append(rule)
    put = api("PUT", f"/zones/{ZONE}/rulesets/phases/{phase}/entrypoint",
              {"rules": rules})
    if put.get("success"):
        ids = [r["id"] for r in put["result"].get("rules", [])]
        print(f"OK   {phase}: {description} (ruleset={put['result']['id']}, rules={ids})")
        return True
    level = "FAIL" if required else "SKIP(可选，无权限)"
    print(f"{level} {phase}: PUT {put.get('errors')}")
    return not required


def ensure_hostname_associations():
    """把 HOSTS 并入 zone 的 mTLS 主机关联（幂等，保留既有）。"""
    got = api("GET", f"/zones/{ZONE}/certificate_authorities/hostname_associations")
    current = (got.get("result") or {}).get("hostnames") or []
    merged = sorted(set(current) | set(HOSTS))
    if merged == sorted(current):
        print(f"OK   hostname_associations: {merged} (无变化)")
        return True
    put = api("PUT", f"/zones/{ZONE}/certificate_authorities/hostname_associations",
              {"hostnames": merged})
    if put.get("success"):
        print(f"OK   hostname_associations: {merged}")
        return True
    print(f"FAIL hostname_associations: {put.get('errors')}")
    return False


def main():
    if not TOKEN:
        sys.exit("need CF_RULES_TOKEN env")
    ensure_hostname_associations()
    ok = ensure_rule("http_request_late_transform", TRANSFORM_DESC, {
        "action": "rewrite",
        "expression": _HOST_EXPR,
        "description": TRANSFORM_DESC,
        "action_parameters": {"headers": TRANSFORM_HEADERS},
        "enabled": True,
    }, required=True)
    # WAF 边缘拦截是可选加固：token 无 Firewall Services 权限时跳过不影响安全
    #（无证/吊销证由源站证书表 403 拒绝）。
    ok &= ensure_rule("http_request_firewall_custom", WAF_DESC, {
        "action": "block",
        "expression": WAF_EXPRESSION,
        "description": WAF_DESC,
        "enabled": True,
    }, required=False)
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
