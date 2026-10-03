package main

// webhook 管理 API 与转发 worker 的单测：SSRF 校验红线、slug 校验、
// 退避函数、投递重放与统计。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// newWebhookTestDB 指向临时数据目录并初始化 schema，
// 同时放开受信代理与免证开关：httptest 的默认来源 192.0.2.1 不在默认可信 CIDR 内，
// 且 requireRole 走 dev 分支才会按 ops.admin 放行（与 auth_verify_test.go 同手法）。
func newWebhookTestDB(t *testing.T) {
	t.Helper()
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("TRUSTED_PROXY_CIDRS", "127.0.0.0/8,::1/128,192.0.2.0/24")
	t.Setenv("DEV_ALLOW_NO_CERT", "1")
	if _, err := getDB(); err != nil {
		t.Fatalf("getDB: %v", err)
	}
}

// ---------- 退避表 ----------

func TestBackoffForAttempt(t *testing.T) {
	cases := []struct {
		n    int
		want time.Duration
	}{
		{0, 5 * time.Second},
		{1, 5 * time.Second},
		{2, 30 * time.Second},
		{3, 2 * time.Minute},
		{4, 10 * time.Minute},
		{5, 30 * time.Minute},
		{9, 30 * time.Minute}, // 超出档位取最后一档
	}
	for _, c := range cases {
		if got := backoffForAttempt(c.n); got != c.want {
			t.Errorf("backoffForAttempt(%d) = %v, want %v", c.n, got, c.want)
		}
	}
}

// ---------- SSRF 红线（注册侧静态校验） ----------

func TestValidateWebhookTargetURL(t *testing.T) {
	ok := []string{
		"https://hooks.example.com/path",
		"http://1.2.3.4:8080/ingest",
	}
	for _, in := range ok {
		if _, err := validateWebhookTargetURL(in); err != nil {
			t.Errorf("validateWebhookTargetURL(%q) unexpected error: %v", in, err)
		}
	}

	bad := map[string]string{
		"":                                  "required",
		"ftp://hooks.example.com":           "http/https",
		"file:///etc/passwd":                "http/https",
		"https://":                          "缺少 host",
		"https://user:pw@hooks.example.com": "userinfo",
		"http://127.0.0.1:8319/hook":        "非公网",
		"http://10.1.2.3/hook":              "非公网",
		"http://192.168.1.9/hook":           "非公网",
		"http://169.254.169.254/latest":     "非公网", // 云元数据地址
		"http://[::1]:9000/hook":            "非公网",
	}
	for in, wantSub := range bad {
		_, err := validateWebhookTargetURL(in)
		if err == nil {
			t.Errorf("validateWebhookTargetURL(%q) = nil error, want containing %q", in, wantSub)
			continue
		}
		if !strings.Contains(err.Error(), wantSub) {
			t.Errorf("validateWebhookTargetURL(%q) error = %q, want containing %q", in, err.Error(), wantSub)
		}
	}
}

// 逃生阀打开时私网目标放行（内网自托管联调场景）。
func TestValidateWebhookTargetURLAllowPrivate(t *testing.T) {
	t.Setenv("OPS_PROBE_ALLOW_PRIVATE", "1")
	if _, err := validateWebhookTargetURL("http://127.0.0.1:9000/hook"); err != nil {
		t.Errorf("allow-private should accept loopback, got %v", err)
	}
}

// 建连侧防线：dial 校验按真实目标 IP 判定（防 DNS rebinding）。
func TestDialTargetControl(t *testing.T) {
	if err := dialTargetControl("tcp", "127.0.0.1:8319", nil); err == nil {
		t.Error("loopback dial target should be refused")
	}
	if err := dialTargetControl("tcp", "10.0.0.9:443", nil); err == nil {
		t.Error("private dial target should be refused")
	}
	if err := dialTargetControl("tcp", "1.2.3.4:443", nil); err != nil {
		t.Errorf("public dial target should pass, got %v", err)
	}
	if err := dialTargetControl("tcp", "not-an-address", nil); err == nil {
		t.Error("malformed address should be refused")
	}

	t.Setenv("OPS_PROBE_ALLOW_PRIVATE", "1")
	if err := dialTargetControl("tcp", "127.0.0.1:8319", nil); err != nil {
		t.Errorf("allow-private mode should accept loopback, got %v", err)
	}
}

// ---------- handler 级 ----------

// postJSON 发一个 JSON POST 到给定 mux，返回响应记录器。
func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// 端点 upsert：坏 slug / 空 name 必须 400，合法则落库且列表打码。
func TestWebhookEndpointsUpsertValidation(t *testing.T) {
	newWebhookTestDB(t)
	mux := newWebhookAdminMux()

	for _, bad := range []string{
		`{"name":"a","slug":"Bad Slug"}`,
		`{"name":"a","slug":""}`,
		`{"name":"a","slug":"../etc"}`,
		`{"name":"a","slug":"UPPER"}`,
		`{"name":"","slug":"ok-slug"}`,
	} {
		if rr := postJSON(t, mux, "/api/portal/webhooks/endpoints/upsert", bad); rr.Code != http.StatusBadRequest {
			t.Errorf("upsert %s = %d, want 400 (body=%s)", bad, rr.Code, rr.Body.String())
		}
	}

	rr := postJSON(t, mux, "/api/portal/webhooks/endpoints/upsert",
		// 假密钥不带 whsec_/sk_ 等真实厂商前缀，避免 gitleaks generic-api-key 误报（CI 红灯）
		`{"name":"GitHub","slug":"github-main","secret":"fake-test-abcd1234","enabled":true,"note":"n"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("valid upsert = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	list, err := listEndpoints()
	if err != nil || len(list) != 1 {
		t.Fatalf("listEndpoints = %v, %v; want 1 row", list, err)
	}
	if list[0].Slug != "github-main" || !list[0].Enabled {
		t.Fatalf("saved endpoint = %+v", list[0])
	}

	// 列表接口必须打码密钥，绝不能回明文。
	grr := httptest.NewRecorder()
	mux.ServeHTTP(grr, httptest.NewRequest(http.MethodGet, "/api/portal/webhooks/endpoints", nil))
	var got struct {
		OK        bool              `json:"ok"`
		Endpoints []webhookEndpoint `json:"endpoints"`
	}
	if err := json.Unmarshal(grr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if !got.OK || len(got.Endpoints) != 1 {
		t.Fatalf("list response = %s", grr.Body.String())
	}
	if got.Endpoints[0].Secret == "fake-test-abcd1234" {
		t.Fatal("list endpoint must not return the plaintext secret")
	}
	if !strings.Contains(got.Endpoints[0].Secret, "****") {
		t.Fatalf("secret should be masked, got %q", got.Endpoints[0].Secret)
	}
}

// 更新端点时不传 secret 必须保留原密钥（前端不回传密钥的常见场景）。
func TestWebhookEndpointUpsertKeepsSecretWhenOmitted(t *testing.T) {
	newWebhookTestDB(t)
	mux := newWebhookAdminMux()
	postJSON(t, mux, "/api/portal/webhooks/endpoints/upsert",
		`{"name":"a","slug":"keep-secret","secret":"whsec_keepme01","enabled":true}`)
	list, _ := listEndpoints()
	if len(list) != 1 {
		t.Fatalf("want 1 endpoint, got %d", len(list))
	}
	id := list[0].ID
	postJSON(t, mux, "/api/portal/webhooks/endpoints/upsert",
		fmt.Sprintf(`{"id":%q,"name":"a2","slug":"keep-secret","enabled":false}`, id))
	after := findEndpointByID(id)
	if after == nil {
		t.Fatal("endpoint disappeared")
	}
	if after.Secret != "whsec_keepme01" {
		t.Fatalf("secret must be preserved when omitted, got %q", after.Secret)
	}
	if after.Name != "a2" || after.Enabled {
		t.Fatalf("update not applied: %+v", after)
	}
}

// 规则 upsert：私网 target_url 必须被 SSRF 校验挡下（400）。
func TestWebhookSourceUpsertRejectsPrivateTarget(t *testing.T) {
	newWebhookTestDB(t)
	mux := newWebhookAdminMux()
	postJSON(t, mux, "/api/portal/webhooks/endpoints/upsert",
		`{"name":"ep","slug":"ep-one","secret":"whsec_x1","enabled":true}`)
	eps, _ := listEndpoints()
	if len(eps) != 1 {
		t.Fatalf("setup endpoint failed: %d", len(eps))
	}
	epID := eps[0].ID

	for _, tgt := range []string{
		"http://127.0.0.1:9000/hook",
		"http://10.0.0.7/hook",
		"http://169.254.169.254/latest/meta-data",
		"ftp://example.com/hook",
	} {
		body := sourceBody(epID, tgt)
		if rr := postJSON(t, mux, "/api/portal/webhooks/sources/upsert", body); rr.Code != http.StatusBadRequest {
			t.Errorf("private/unsupported target %q = %d, want 400 (body=%s)", tgt, rr.Code, rr.Body.String())
		}
	}
	// 私网目标不得落库
	if srcs, _ := listSources(epID); len(srcs) != 0 {
		t.Fatalf("rejected sources must not be stored, got %+v", srcs)
	}

	// 公网目标放行
	okBody := sourceBody(epID, "http://1.2.3.4:8080/hook")
	if rr := postJSON(t, mux, "/api/portal/webhooks/sources/upsert", okBody); rr.Code != http.StatusOK {
		t.Fatalf("public target = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
}

// 规则必须挂在存在的端点上。
func TestWebhookSourceUpsertRejectsUnknownEndpoint(t *testing.T) {
	newWebhookTestDB(t)
	mux := newWebhookAdminMux()
	rr := postJSON(t, mux, "/api/portal/webhooks/sources/upsert",
		`{"endpoint_id":"wh-ep-nope","name":"s","target_url":"http://1.2.3.4/h","enabled":true}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown endpoint = %d, want 400", rr.Code)
	}
}

// 重放：状态归位 pending 且清空拒绝原因。
func TestWebhookDeliveriesReplay(t *testing.T) {
	newWebhookTestDB(t)
	db, err := getDB()
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO webhook_deliveries
		(id,endpoint_id,slug,received_at,remote_ip,method,content_type,headers,body,body_bytes,sig_ok,status,reject_reason,event_type,dedup_key)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		"wh-del-1", "wh-ep-1", "ep", "2026-01-01T00:00:00Z", "1.1.1.1", "POST",
		"application/json", "{}", "{}", 2, 1, webhookStatusFailed, "boom", "push", ""); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	h := newWebhookAdminMux()
	if rr := postJSON(t, h, "/api/portal/webhooks/deliveries/replay", `{"id":"wh-del-1"}`); rr.Code != http.StatusOK {
		t.Fatalf("replay = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	d, err := getDelivery("wh-del-1")
	if err != nil {
		t.Fatalf("getDelivery: %v", err)
	}
	if d.Status != webhookStatusPending {
		t.Fatalf("status after replay = %q, want pending", d.Status)
	}
	if d.RejectReason != "" {
		t.Fatalf("reject_reason should be cleared, got %q", d.RejectReason)
	}

	// 不存在的 id → 404
	if rr := postJSON(t, h, "/api/portal/webhooks/deliveries/replay", `{"id":"nope"}`); rr.Code != http.StatusNotFound {
		t.Errorf("replay unknown id = %d, want 404", rr.Code)
	}
}

// 重放必须真正产生一次新尝试：历史尝试记录要清空，否则 attempt_no 已达
// max_attempts 时 worker 立即判定终结，重放沦为空操作（回归用例）。
func TestWebhookReplayActuallyRetries(t *testing.T) {
	newWebhookTestDB(t)
	t.Setenv("OPS_PROBE_ALLOW_PRIVATE", "1") // 放行环回，便于构造确定性的失败目标
	db, err := getDB()
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO webhook_endpoints (id,name,slug,secret,enabled,note,created_at,created_by)
		VALUES (?,?,?,?,?,?,?,?)`, "e1", "e", "e", "s", 1, "", "2026-01-01T00:00:00Z", "t"); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	// max_attempts=1：一次失败即终结，模拟「已耗尽重试」的终态投递。
	if _, err := db.Exec(`INSERT INTO webhook_sources
		(id,endpoint_id,name,target_url,secret,enabled,max_attempts,timeout_ms,created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`, "s1", "e1", "s", "http://127.0.0.1:1/h", "sec", 1, 1, 500, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO webhook_deliveries
		(id,endpoint_id,slug,received_at,remote_ip,method,content_type,headers,body,body_bytes,sig_ok,status,reject_reason,event_type,dedup_key)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, "d1", "e1", "e", "2026-01-01T00:00:00Z", "1.1.1.1",
		"POST", "application/json", "{}", "{}", 2, 1, webhookStatusPending, "", "push", ""); err != nil {
		t.Fatalf("seed delivery: %v", err)
	}

	dispatchPendingWebhooks(time.Now())
	var st string
	db.QueryRow(`SELECT status FROM webhook_deliveries WHERE id=?`, "d1").Scan(&st)
	if st != webhookStatusFailed {
		t.Fatalf("after dispatch status = %q, want failed (max_attempts=1)", st)
	}

	// 走 HTTP 接口重放（覆盖 handler 与清空尝试的联动）。
	h := newWebhookAdminMux()
	if rr := postJSON(t, h, "/api/portal/webhooks/deliveries/replay", `{"id":"d1"}`); rr.Code != http.StatusOK {
		t.Fatalf("replay = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	var leftovers int
	db.QueryRow(`SELECT COUNT(*) FROM webhook_attempts WHERE delivery_id=?`, "d1").Scan(&leftovers)
	if leftovers != 0 {
		t.Fatalf("attempts after replay = %d, want 0 (counter must reset)", leftovers)
	}

	// 再次 dispatch 必须产生新的一次尝试（这正是修复前失败的点）。
	dispatchPendingWebhooks(time.Now())
	var n int
	db.QueryRow(`SELECT COUNT(*) FROM webhook_attempts WHERE delivery_id=?`, "d1").Scan(&n)
	if n != 1 {
		t.Fatalf("attempts after re-dispatch = %d, want 1 (replay must actually retry)", n)
	}

	// 已终结的投递重放后若规则仍耗尽，允许再次失败，但必须真的尝试过。
	db.QueryRow(`SELECT status FROM webhook_deliveries WHERE id=?`, "d1").Scan(&st)
	t.Logf("final status=%s attempts=%d", st, n)
}

// 统计：各状态计数 + 端点数 + 规则数。
func TestWebhookStats(t *testing.T) {
	newWebhookTestDB(t)
	db, err := getDB()
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	seed := func(id, status string) {
		t.Helper()
		if _, err := db.Exec(`INSERT INTO webhook_deliveries
			(id,endpoint_id,slug,received_at,remote_ip,method,content_type,headers,body,body_bytes,sig_ok,status,reject_reason,event_type,dedup_key)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, "wh-ep-1", "ep", "2026-01-01T00:00:00Z", "1.1.1.1", "POST",
			"application/json", "{}", "{}", 2, 1, status, "", "push", ""); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}
	seed("d1", webhookStatusPending)
	seed("d2", webhookStatusDelivered)
	seed("d3", webhookStatusDelivered)
	seed("d4", webhookStatusFailed)
	seed("d5", webhookStatusRejected)

	if _, err := db.Exec(`INSERT INTO webhook_endpoints (id,name,slug,secret,enabled,note,created_at,created_by)
		VALUES (?,?,?,?,?,?,?,?)`,
		"wh-ep-1", "ep", "ep", "s", 1, "", "2026-01-01T00:00:00Z", "tester"); err != nil {
		t.Fatalf("seed endpoint: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO webhook_sources
		(id,endpoint_id,name,target_url,secret,enabled,max_attempts,timeout_ms,created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		"wh-src-1", "wh-ep-1", "s", "http://1.2.3.4/h", "s", 1, 3, 3000, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatalf("seed source: %v", err)
	}

	h := newWebhookAdminMux()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/portal/webhooks/stats", nil))
	var got struct {
		OK    bool           `json:"ok"`
		Stats map[string]int `json:"stats"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	want := map[string]int{
		"total": 5, "pending": 1, "delivered": 2, "failed": 1, "rejected": 1,
		"endpoints": 1, "sources": 1,
	}
	for k, v := range want {
		if got.Stats[k] != v {
			t.Errorf("stats[%s] = %d, want %d (full=%v)", k, got.Stats[k], v, got.Stats)
		}
	}
}

// 列表接口的 status 过滤只接受白名单值。
func TestWebhookDeliveriesListStatusFilter(t *testing.T) {
	newWebhookTestDB(t)
	h := newWebhookAdminMux()
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/portal/webhooks/deliveries?status=bogus", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("bogus status = %d, want 400", rr.Code)
	}
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, httptest.NewRequest(http.MethodGet, "/api/portal/webhooks/deliveries?status=pending&limit=10", nil))
	if rr2.Code != http.StatusOK {
		t.Fatalf("valid status = %d, want 200 (body=%s)", rr2.Code, rr2.Body.String())
	}
}

// sourceBody 构造规则 upsert 请求体（避免手写 JSON 拼接错误）。
func sourceBody(endpointID, targetURL string) string {
	return fmt.Sprintf(`{"endpoint_id":%q,"name":"s","target_url":%q,"enabled":true,"max_attempts":3,"timeout_ms":3000}`,
		endpointID, targetURL)
}

// newWebhookAdminMux 用 DEV_ALLOW_NO_CERT 提供 admin 身份，复用生产路由包装。
// 无证书时 requireRole 走 dev 分支放行（与现有 whoami 测试同一手法）。
func newWebhookAdminMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/portal/webhooks/endpoints", requireRole("ops.viewer", apiWebhookEndpointsList))
	mux.HandleFunc("POST /api/portal/webhooks/endpoints/upsert", requireRole("ops.admin", apiWebhookEndpointsUpsert))
	mux.HandleFunc("POST /api/portal/webhooks/endpoints/remove", requireRole("ops.admin", apiWebhookEndpointsRemove))
	mux.HandleFunc("GET /api/portal/webhooks/sources", requireRole("ops.viewer", apiWebhookSourcesList))
	mux.HandleFunc("POST /api/portal/webhooks/sources/upsert", requireRole("ops.admin", apiWebhookSourcesUpsert))
	mux.HandleFunc("GET /api/portal/webhooks/deliveries", requireRole("ops.viewer", apiWebhookDeliveriesList))
	mux.HandleFunc("POST /api/portal/webhooks/deliveries/replay", requireRole("ops.operator", apiWebhookDeliveriesReplay))
	mux.HandleFunc("GET /api/portal/webhooks/stats", requireRole("ops.viewer", apiWebhookStats))
	return mux
}
