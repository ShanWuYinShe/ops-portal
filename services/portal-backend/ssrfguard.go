package main

// ops_url 拨测的 SSRF 防线：portal 会向站点登记的 ops_url 主动发起带
// service 客户端证书的拨测请求，必须保证该请求打不到 portal 所在内网。
// 两层防线（缺一不可）：
//   1. 注册/登记时静态校验（validateProbeURL）：scheme 限 http/https、
//      拒绝 userinfo、host 解析后拒绝环回/私网/link-local 地址——给操作者
//      即时反馈；
//   2. 拨测建连时校验（probeDialControl，挂 net.Dialer.Control）：以真实
//      建连目标 IP 为准，DNS rebinding（解析公网、连接私网）与注册后
//      改 DNS 解析到内网都拦得住。
// OPS_PROBE_ALLOW_PRIVATE=1 显式放开私网目标，仅供纯内网自托管部署联调。

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"syscall"
)

func probeAllowPrivate() bool { return getEnv("OPS_PROBE_ALLOW_PRIVATE", "") != "" }

// validateProbeURL 注册侧校验。返回规范化 ops_url（去尾部斜杠，与
// probeOne 的拼接语义一致）。
func validateProbeURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("ops_url required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("ops_url 不可解析: %v", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("ops_url 仅支持 http/https")
	}
	if u.Host == "" {
		return "", fmt.Errorf("ops_url 缺少 host")
	}
	if u.User != nil {
		return "", fmt.Errorf("ops_url 不允许携带 userinfo")
	}
	if err := rejectPrivateHost(u.Hostname()); err != nil {
		return "", err
	}
	return strings.TrimRight(raw, "/"), nil
}

// rejectPrivateHost 解析 host 并拒绝解析到非公网地址。解析失败不拦（域名
// 未上线时允许先注册，拨测自然失败并显示原因；dial 侧拿到的必是 IP）。
func rejectPrivateHost(host string) error {
	if probeAllowPrivate() {
		return nil
	}
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if !isPublicIP(ip) {
			return fmt.Errorf("ops_url 指向非公网地址 %s（环回/私网/链路本地不可作为拨测目标）", ip)
		}
	}
	return nil
}

// isPublicIP 排除环回、私网（RFC1918/ULA）、链路本地、组播与未指定地址。
func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified())
}

// probeDialControl 拨测侧防线：连接建立前对目标 IP 字面量做同一判定，
// 不信任注册时/解析时的域名语义，rebinding 无法绕过。
func probeDialControl(_, address string, _ syscall.RawConn) error {
	if probeAllowPrivate() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("probe: bad address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("probe: non-IP dial target %q", address)
	}
	if !isPublicIP(ip) {
		return fmt.Errorf("probe: refused non-public dial target %s", ip)
	}
	return nil
}
