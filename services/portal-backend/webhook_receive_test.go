package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// webhook 入站接收：签名校验、slug 查找、投递落库、幂等去重。

// signWebhook 按契约算法签名：有 ts 时签名串为 "<ts>." + body。
func signWebhook(secret, ts string, body []byte) string {
	payload := body
	if ts != "" {
		payload = append([]byte(ts+"."), body...)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyWebhookSignature(t *testing.T) {
	secret := "whsec_test"
	body := []byte(`{"action":"opened"}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	staleTS := strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)

	cases := []struct {
		name string
		sig  string
		ts   string
		want bool
	}{
		{"正确签名（无时间戳）", signWebhook(secret, "", body), "", true},
		{"正确签名（含时间戳）", signWebhook(secret, ts, body), ts, true},
		{"裸 hex 无 sha256= 前缀", strings.TrimPrefix(signWebhook(secret, "", body), "sha256="), "", true},
		{"签名错误", signWebhook("wrong-secret", "", body), "", false},
		{"缺签名头", "", "", false},
		{"签名头格式非法", "sha256=zzzz", "", false},
		{"签名头非 hex", "not-a-hex-string", "", false},
		{"body 被篡改", signWebhook(secret, "", []byte(`{"action":"closed"}`)), "", false},
		// 带 ts 的签名串是 "<ts>."+body，混用（签名含 ts 但请求不带 ts）必须失败
		{"签名含 ts 但请求不带 ts", signWebhook(secret, ts, body), "", false},
		// 时间戳超窗（防重放）
		{"时间戳过期", signWebhook(secret, staleTS, body), staleTS, false},
		{"时间戳非数字", signWebhook(secret, "abc", body), "abc", false},
		// 空 secret 是「未配置」，不是「密钥为空串」，一律拒
		{"空 secret", signWebhook("", "", body), "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := verifyWebhookSignature(secret, body, c.sig, c.ts); got != c.want {
				t.Fatalf("verify(%s) = %v, want %v", c.name, got, c.want)
			}
		})
	}
}

func TestMaskSecret(t *testing.T) {
	if got := maskSecret(""); got != "" {
		t.Fatalf("maskSecret(empty) = %q, want empty", got)
	}
	if got := maskSecret("short"); got != "********" {
		t.Fatalf("maskSecret(short) = %q, want all stars", got)
	}
	full := "whsec_0123456789abcdef"
	got := maskSecret(full)
	if strings.Contains(got, full[4:len(full)-4]) {
		t.Fatalf("maskSecret leaked middle: %q", got)
	}
	if !strings.HasPrefix(got, "whse") || !strings.HasSuffix(got, "cdef") {
		t.Fatalf("maskSecret = %q, want 4-char head/tail", got)
	}
}

// seedEndpoint 建一个启用端点，返回明文 secret 与端点 id。
func seedEndpoint(t *testing.T, slug, secret string) *webhookEndpoint {
	t.Helper()
	ep := webhookEndpoint{Name: "test", Slug: slug, Secret: secret, Enabled: true, CreatedBy: "tester"}
	if err := upsertEndpoint(ep); err != nil {
		t.Fatalf("upsertEndpoint: %v", err)
	}
	got, err := getEndpointBySlug(slug)
	if err != nil {
		t.Fatalf("getEndpointBySlug: %v", err)
	}
	return got
}

func TestEndpointUpsertLookupAndRemove(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	ep := seedEndpoint(t, "github", "whsec_a")
	if !strings.HasPrefix(ep.ID, "wh-ep-") {
		t.Fatalf("id = %q, want wh-ep- prefix", ep.ID)
	}
	if ep.CreatedAt == "" {
		t.Fatal("created_at should be set on insert")
	}

	// 更新：改名字与启用态，id/slug 不变
	ep.Name, ep.Enabled = "renamed", false
	if err := upsertEndpoint(*ep); err != nil {
		t.Fatalf("update endpoint: %v", err)
	}
	again, err := getEndpointBySlug("github")
	if err != nil {
		t.Fatalf("lookup after update: %v", err)
	}
	if again.Name != "renamed" || again.Enabled {
		t.Fatalf("update not applied: %+v", again)
	}
	if again.CreatedAt != ep.CreatedAt {
		t.Fatalf("created_at must be preserved: %q -> %q", ep.CreatedAt, again.CreatedAt)
	}

	// slug 唯一：另建同 slug 端点必须失败
	if err := upsertEndpoint(webhookEndpoint{Name: "dup", Slug: "github"}); err == nil {
		t.Fatal("duplicate slug must fail")
	}
	// slug 必填
	if err := upsertEndpoint(webhookEndpoint{Name: "no-slug"}); err == nil {
		t.Fatal("empty slug must fail")
	}

	// 级联删 sources
	if err := upsertSource(webhookSource{EndpointID: ep.ID, Name: "s1", TargetURL: "https://example.com/hook", Enabled: true}); err != nil {
		t.Fatalf("upsertSource: %v", err)
	}
	if got, err := listSources(ep.ID); err != nil || len(got) != 1 {
		t.Fatalf("sources = %+v, %v; want 1", got, err)
	}
	if err := removeEndpoint(ep.ID); err != nil {
		t.Fatalf("removeEndpoint: %v", err)
	}
	if got, _ := listSources(ep.ID); len(got) != 0 {
		t.Fatalf("sources should be cascaded, got %+v", got)
	}
	if _, err := getEndpointBySlug("github"); err == nil {
		t.Fatal("removed endpoint should not resolve")
	}
	if err := removeEndpoint(ep.ID); err == nil {
		t.Fatal("removing twice should fail")
	}
}

func TestRotateEndpointSecret(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	ep := seedEndpoint(t, "rotate", "whsec_old")
	plain, err := rotateEndpointSecret(ep.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if !strings.HasPrefix(plain, "whsec_") || plain == "whsec_old" {
		t.Fatalf("rotated secret = %q", plain)
	}
	got, _ := getEndpointBySlug("rotate")
	if got.Secret != plain {
		t.Fatalf("stored secret = %q, want rotated plaintext", got.Secret)
	}
	if _, err := rotateEndpointSecret("wh-ep-nope"); err == nil {
		t.Fatal("rotating unknown endpoint should fail")
	}
}

// receive 发一个入站请求到 handler，返回响应与已落库的投递记录。
func receive(t *testing.T, slug, secret, ts string, body []byte, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/hooks/"+slug, strings.NewReader(string(body)))
	req.RemoteAddr = "203.0.113.9:12345"
	if secret != "" {
		req.Header.Set("X-Webhook-Signature", signWebhook(secret, ts, body))
	}
	if ts != "" {
		req.Header.Set("X-Webhook-Timestamp", ts)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	apiWebhookReceive(rec, req)
	return rec
}

func decodeEnvelope(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatalf("response not JSON: %v (%s)", err, rec.Body.String())
	}
	return m
}

func TestWebhookReceiveStoresDelivery(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	secret := "whsec_recv"
	ep := seedEndpoint(t, "recv", secret)
	body := []byte(`{"type":"push","ref":"main"}`)

	rec := receive(t, "recv", secret, "", body, map[string]string{"X-Webhook-Event": "push"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 (%s)", rec.Code, rec.Body.String())
	}
	env := decodeEnvelope(t, rec)
	if env["ok"] != true || env["accepted"] != true || env["dedup"] != false {
		t.Fatalf("envelope = %+v", env)
	}
	id, _ := env["delivery_id"].(string)
	if id == "" {
		t.Fatalf("delivery_id missing: %+v", env)
	}

	d, err := getDelivery(id)
	if err != nil {
		t.Fatalf("getDelivery: %v", err)
	}
	if d.EndpointID != ep.ID || d.Slug != "recv" || d.Status != webhookStatusPending {
		t.Fatalf("delivery = %+v", d)
	}
	if d.Body != string(body) || d.BodyBytes != len(body) || !d.SigOK {
		t.Fatalf("body/sig not persisted: %+v", d)
	}
	if d.EventType != "push" {
		t.Fatalf("event_type = %q, want push", d.EventType)
	}
	if d.ContentType != "application/json" || d.Method != http.MethodPost {
		t.Fatalf("content_type/method = %q/%q", d.ContentType, d.Method)
	}
	if d.Headers["X-Webhook-Event"] != "push" {
		t.Fatalf("headers not persisted: %+v", d.Headers)
	}
	if d.ReceivedAt == "" || time.Since(mustParse(t, d.ReceivedAt)) > time.Minute {
		t.Fatalf("received_at = %q", d.ReceivedAt)
	}

	// 列表接口不含 body 全文
	list, err := listDeliveries(10, "", "")
	if err != nil || len(list) != 1 {
		t.Fatalf("listDeliveries = %+v, %v", list, err)
	}
	if list[0].Body != "" || list[0].Headers != nil {
		t.Fatalf("list should omit body/headers: %+v", list[0])
	}

	stats, err := webhookStats()
	if err != nil {
		t.Fatalf("webhookStats: %v", err)
	}
	if stats["total"] != 1 || stats["pending"] != 1 || stats["endpoints"] != 1 {
		t.Fatalf("stats = %+v", stats)
	}
}

func mustParse(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatalf("bad RFC3339 %q: %v", s, err)
	}
	return ts
}

func TestWebhookReceiveRejectsBadSignature(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	secret := "whsec_sig"
	ep := seedEndpoint(t, "sig", secret)
	body := []byte(`{"a":1}`)

	// 错误签名 → 401，且落一条 rejected 记录（保留原文便于排障）
	rec := receive(t, "sig", "wrong-secret", "", body, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature status = %d, want 401", rec.Code)
	}
	rejected, err := listDeliveries(10, webhookStatusRejected, ep.ID)
	if err != nil || len(rejected) != 1 {
		t.Fatalf("rejected deliveries = %+v, %v; want 1", rejected, err)
	}
	if rejected[0].SigOK || rejected[0].RejectReason == "" {
		t.Fatalf("rejected record = %+v", rejected[0])
	}

	// 缺签名头 → 401
	req := httptest.NewRequest(http.MethodPost, "/api/hooks/sig", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	apiWebhookReceive(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("missing signature status = %d, want 401", w.Code)
	}
	if s, _ := webhookStats(); s["pending"] != 0 {
		t.Fatalf("rejected deliveries must not be pending: %+v", s)
	}
}

func TestWebhookReceiveUnknownOrDisabledEndpoint(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	body := []byte(`{"a":1}`)

	// 不存在的 slug → 404
	if rec := receive(t, "nope", "whatever", "", body, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown slug status = %d, want 404", rec.Code)
	}

	// disabled 端点 → 404，且不落库
	ep := seedEndpoint(t, "off", "whsec_off")
	ep.Enabled = false
	if err := upsertEndpoint(*ep); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if rec := receive(t, "off", "whsec_off", "", body, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("disabled endpoint status = %d, want 404", rec.Code)
	}
	if list, _ := listDeliveries(10, "", ""); len(list) != 0 {
		t.Fatalf("disabled endpoint must not persist deliveries: %+v", list)
	}
}

func TestWebhookReceiveDedup(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	secret := "whsec_dedup"
	seedEndpoint(t, "dd", secret)
	body := []byte(`{"type":"push"}`)
	hdr := map[string]string{"X-Webhook-Delivery": "dlv-123"}

	first := receive(t, "dd", secret, "", body, hdr)
	if first.Code != http.StatusAccepted {
		t.Fatalf("first status = %d, want 202", first.Code)
	}
	e1 := decodeEnvelope(t, first)
	if e1["dedup"] != false {
		t.Fatalf("first delivery should not be dedup: %+v", e1)
	}

	// 重复 dedup_key → 200 dedup=true，且不重复建记录
	second := receive(t, "dd", secret, "", body, hdr)
	if second.Code != http.StatusOK {
		t.Fatalf("duplicate status = %d, want 200", second.Code)
	}
	e2 := decodeEnvelope(t, second)
	if e2["dedup"] != true || e2["accepted"] != true {
		t.Fatalf("duplicate envelope = %+v", e2)
	}
	if e2["delivery_id"] != e1["delivery_id"] {
		t.Fatalf("dedup must return the original id: %v vs %v", e2["delivery_id"], e1["delivery_id"])
	}
	if list, _ := listDeliveries(10, "", ""); len(list) != 1 {
		t.Fatalf("dedup must not create a second row: got %d", len(list))
	}

	// GitHub 头同样作为 dedup_key
	gh := map[string]string{"X-GitHub-Delivery": "gh-999"}
	if rec := receive(t, "dd", secret, "", body, gh); rec.Code != http.StatusAccepted {
		t.Fatalf("github delivery status = %d, want 202", rec.Code)
	}
	if rec := receive(t, "dd", secret, "", body, gh); decodeEnvelope(t, rec)["dedup"] != true {
		t.Fatalf("github duplicate should dedup: %s", rec.Body.String())
	}

	// 无 dedup_key 的投递不去重（每次都是新记录）
	for i := 0; i < 2; i++ {
		if rec := receive(t, "dd", secret, "", body, nil); rec.Code != http.StatusAccepted {
			t.Fatalf("no-dedup-key status = %d, want 202", rec.Code)
		}
	}
	if list, _ := listDeliveries(50, "", ""); len(list) != 4 {
		t.Fatalf("total deliveries = %d, want 4 (2 dedup+2 plain)", len(list))
	}
}

func TestWebhookReceiveBodyTooLarge(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	secret := "whsec_big"
	seedEndpoint(t, "big", secret)
	body := []byte(strings.Repeat("x", webhookMaxBody+1))
	if rec := receive(t, "big", secret, "", body, nil); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized status = %d, want 413", rec.Code)
	}
	if list, _ := listDeliveries(10, "", ""); len(list) != 0 {
		t.Fatalf("oversized body must not persist: %+v", list)
	}
	// 恰好在限额内应当接受
	ok := []byte(strings.Repeat("y", webhookMaxBody))
	if rec := receive(t, "big", secret, "", ok, nil); rec.Code != http.StatusAccepted {
		t.Fatalf("at-limit status = %d, want 202", rec.Code)
	}
}

func TestWebhookReplayResetsDelivery(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	secret := "whsec_replay"
	seedEndpoint(t, "rp", secret)
	rec := receive(t, "rp", secret, "", []byte(`{"a":1}`), nil)
	id, _ := decodeEnvelope(t, rec)["delivery_id"].(string)

	db, err := getDB()
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	if _, err := db.Exec(`UPDATE webhook_deliveries SET status='failed', reject_reason='gave up' WHERE id=?`, id); err != nil {
		t.Fatalf("mark failed: %v", err)
	}
	if err := resetDeliveryForReplay(id); err != nil {
		t.Fatalf("resetDeliveryForReplay: %v", err)
	}
	d, err := getDelivery(id)
	if err != nil {
		t.Fatalf("getDelivery: %v", err)
	}
	if d.Status != webhookStatusPending || d.RejectReason != "" {
		t.Fatalf("after replay = %q/%q, want pending/empty", d.Status, d.RejectReason)
	}
	if err := resetDeliveryForReplay("wh-dlv-nope"); err == nil {
		t.Fatal("replaying unknown delivery should fail")
	}
}

func TestWebhookAttemptsListing(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	ep := seedEndpoint(t, "at", "whsec_at")
	src := webhookSource{EndpointID: ep.ID, Name: "target", TargetURL: "https://example.com/x", Enabled: true}
	if err := upsertSource(src); err != nil {
		t.Fatalf("upsertSource: %v", err)
	}
	sources, err := listSources(ep.ID)
	if err != nil || len(sources) != 1 {
		t.Fatalf("sources = %+v, %v", sources, err)
	}
	if sources[0].MaxAttempts != 3 || sources[0].TimeoutMS != 10000 {
		t.Fatalf("defaults not applied: %+v", sources[0])
	}
	if !strings.HasPrefix(sources[0].ID, "wh-src-") {
		t.Fatalf("source id = %q", sources[0].ID)
	}

	rec := receive(t, "at", "whsec_at", "", []byte(`{"a":1}`), nil)
	id, _ := decodeEnvelope(t, rec)["delivery_id"].(string)
	db, err := getDB()
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	// 直接写库（插入函数归出站 worker 交付，本文件不依赖其符号，保持契约边界）
	// id 是 INTEGER PRIMARY KEY AUTOINCREMENT，交给 SQLite 自增（不显式赋值）
	if _, err := db.Exec(`INSERT INTO webhook_attempts
		(delivery_id,source_id,attempt_no,status,http_status,error,duration_ms,request_body,response_body,at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		id, sources[0].ID, 1, "failed", 500, "boom", 12, "{}", "err",
		time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
	got, err := listAttempts(id)
	if err != nil || len(got) != 1 {
		t.Fatalf("attempts = %+v, %v", got, err)
	}
	if got[0].HTTPStatus != 500 || got[0].AttemptNo != 1 || got[0].Error != "boom" {
		t.Fatalf("attempt = %+v", got[0])
	}
	if other, _ := listAttempts("wh-dlv-none"); len(other) != 0 {
		t.Fatalf("unknown delivery attempts = %+v", other)
	}
}

// 输入校验：target_url 与 endpoint_id 必填
func TestSourceValidation(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	if err := upsertSource(webhookSource{TargetURL: "https://x"}); err == nil {
		t.Fatal("missing endpoint_id must fail")
	}
	if err := upsertSource(webhookSource{EndpointID: "wh-ep-x"}); err == nil {
		t.Fatal("missing target_url must fail")
	}
}

// 并发同 dedup_key：唯一索引兜底，只能有一条记录，其余返回既有 id。
func TestWebhookDedupConcurrent(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	secret := "whsec_conc"
	seedEndpoint(t, "conc", secret)
	hdr := map[string]string{"X-Webhook-Delivery": "dlv-conc"}

	ids := make(chan string, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			rec := receive(t, "conc", secret, "", []byte(`{"a":1}`), hdr)
			if rec.Code != http.StatusOK && rec.Code != http.StatusAccepted {
				errs <- fmt.Errorf("unexpected status %d: %s", rec.Code, rec.Body.String())
				return
			}
			var m map[string]any
			_ = json.Unmarshal(rec.Body.Bytes(), &m)
			ids <- fmt.Sprint(m["delivery_id"])
		}()
	}
	first := ""
	for i := 0; i < 8; i++ {
		select {
		case err := <-errs:
			t.Fatal(err)
		case id := <-ids:
			if first == "" {
				first = id
			} else if id != first {
				t.Fatalf("concurrent dedup returned different ids: %q vs %q", id, first)
			}
		}
	}
	if list, _ := listDeliveries(50, "", ""); len(list) != 1 {
		t.Fatalf("concurrent dedup rows = %d, want 1", len(list))
	}
}

// 门禁：/api/hooks/{slug} 与其他业务路径一致套 requireCert（消费方全部自建
// 持证站点，无免证路径）。handler 层用例（上面各 Test）直接调 apiWebhookReceive，
// 不含此门禁；这里在 requireCert 包裹层验证无证/有证行为。
func TestWebhookReceiveRequiresCert(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("DEV_ALLOW_NO_CERT", "")
	seedCert(t, "cert-hook-gate", "hotspot-server")
	seedEndpoint(t, "gated", "whsec_gated")

	h := requireCert(apiWebhookReceive)
	body := "{\"type\":\"push\"}"
	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/hooks/gated", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:5555" // 受信代理
		req.Header.Set("Content-Type", "application/json")
		return req
	}

	// 无证书头：边缘 verified 为假 → 401（到不了 handler）。
	rec := httptest.NewRecorder()
	h(rec, newReq())
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no cert = %d, want 401 (%s)", rec.Code, rec.Body.String())
	}

	// 证书在表 + verified=true：过门禁，签名也匹配 → 202。
	req := newReq()
	req.Header.Set(certVerifyHeader, "true")
	req.Header.Set(certSerialHeader, "S-cert-hook-gate")
	req.Header.Set("X-Webhook-Signature", signWebhook("whsec_gated", "", []byte(body)))
	rec2 := httptest.NewRecorder()
	h(rec2, req)
	if rec2.Code != http.StatusAccepted {
		t.Fatalf("with valid cert = %d, want 202 (%s)", rec2.Code, rec2.Body.String())
	}
}
