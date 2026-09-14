package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// whoami 的 verified 对外必须是布尔：边缘透传头原值是字符串，未发版期修正。
func TestWhoamiVerifiedBool(t *testing.T) {
	h := http.HandlerFunc(apiPortalWhoami)

	req := httptest.NewRequest("GET", "/api/portal/whoami", nil)
	req = req.WithContext(contextWithUser(req, portalUser{Username: "liyou", Role: "ops.admin"}))
	req.Header.Set(certVerifyHeader, "true")
	rec := httptest.NewRecorder()
	h(rec, req)
	body := rec.Body.String()
	if !contains(body, `"verified":true`) {
		t.Fatalf("with cert header, want verified:true, got %s", body)
	}

	req2 := httptest.NewRequest("GET", "/api/portal/whoami", nil)
	req2 = req2.WithContext(contextWithUser(req2, portalUser{Username: "liyou", Role: "ops.admin"}))
	rec2 := httptest.NewRecorder()
	h(rec2, req2)
	if !contains(rec2.Body.String(), `"verified":false`) {
		t.Fatalf("without cert header, want verified:false, got %s", rec2.Body.String())
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
