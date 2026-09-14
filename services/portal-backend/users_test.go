package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func adminReq(t *testing.T, method, body string) (*httptest.ResponseRecorder, *http.Request) {
	t.Helper()
	req := httptest.NewRequest(method, "/", strings.NewReader(body))
	req.RemoteAddr = "192.0.2.9:1234"
	req = req.WithContext(contextWithUser(req, portalUser{Username: "tester", Role: "ops.admin"}))
	return httptest.NewRecorder(), req
}

func seedCert(t *testing.T, id, label string) {
	t.Helper()
	if _, err := addCert(certRecord{ID: id, Label: label, Kind: "human", Serial: "S-" + id, Status: "active"}); err != nil {
		t.Fatalf("addCert: %v", err)
	}
}

// 用户增改删 + 最后 admin 保护 + 不能删自己。
func TestUsersUpsertRemove(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	seedCert(t, "cert-a", "liyou-mbpro")
	seedCert(t, "cert-b", "liyou-phone")

	call := func(h http.HandlerFunc, body string) int {
		rec, req := adminReq(t, http.MethodPost, body)
		h(rec, req)
		return rec.Code
	}

	if code := call(apiUsersUpsert, `{"username":"liyou","role":"ops.admin","cert_id":"cert-a"}`); code != 200 {
		t.Fatalf("upsert = %d", code)
	}
	if code := call(apiUsersUpsert, `{"username":"op","role":"ops.operator","cert_id":"cert-b"}`); code != 200 {
		t.Fatalf("upsert2 = %d", code)
	}
	if code := call(apiUsersUpsert, `{"username":"x","role":"superuser","cert_id":"cert-b"}`); code != 400 {
		t.Fatalf("bad role = %d, want 400", code)
	}
	if code := call(apiUsersUpsert, `{"username":"y","role":"ops.viewer","cert_id":"cert-missing"}`); code != 400 {
		t.Fatalf("bad cert = %d, want 400", code)
	}

	// 不能删自己
	if code := call(apiUsersRemove, `{"username":"tester"}`); code != 400 {
		t.Fatalf("remove self = %d, want 400", code)
	}
	// liyou 是唯一 admin（tester 不在用户表，upsert 的 admin 只有 liyou）：删 liyou 应被拒
	if code := call(apiUsersRemove, `{"username":"liyou"}`); code != 400 {
		t.Fatalf("remove last admin = %d, want 400", code)
	}
	// 加第二个 admin 后可删
	seedCert(t, "cert-c", "root-ca")
	if code := call(apiUsersUpsert, `{"username":"root","role":"ops.admin","cert_id":"cert-c"}`); code != 200 {
		t.Fatalf("upsert root = %d", code)
	}
	if code := call(apiUsersRemove, `{"username":"liyou"}`); code != 200 {
		t.Fatalf("remove = %d, want 200", code)
	}

	// 列表带证书 label
	rec2, req2 := adminReq(t, http.MethodGet, "")
	apiUsersList(rec2, req2)
	if rec2.Code != 200 || !strings.Contains(rec2.Body.String(), "liyou-phone") {
		t.Fatalf("list = %d %s", rec2.Code, rec2.Body.String())
	}
}

// 站点注销：删注册表 + 清探测。
func TestSitesUnregister(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	if err := upsertSiteEntry(siteEntry{Name: "demo", OpsURL: "http://x"}); err != nil {
		t.Fatalf("save: %v", err)
	}
	probeStore.Lock()
	probeStore.m["demo"] = probeResult{Reachable: true}
	probeStore.Unlock()

	rec, req := adminReq(t, http.MethodPost, `{"name":"demo"}`)
	apiSitesUnregister(rec, req)
	if rec.Code != 200 {
		t.Fatalf("unregister = %d %s", rec.Code, rec.Body.String())
	}
	if _, ok := getProbe("demo"); ok {
		t.Fatal("probe cache should be cleared")
	}
	if got := listSiteEntries(); len(got) != 0 {
		t.Fatalf("sites = %+v", got)
	}

	rec2, req2 := adminReq(t, http.MethodPost, `{"name":"demo"}`)
	apiSitesUnregister(rec2, req2)
	if rec2.Code != 404 {
		t.Fatalf("repeat = %d, want 404", rec2.Code)
	}
}
