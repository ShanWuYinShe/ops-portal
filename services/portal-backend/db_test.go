package main

import (
	"os"
	"path/filepath"
	"testing"
)

// 旧版 JSON 文件一次性导入：行数一致、原文件改名 .migrated。
func TestMigrateLegacyJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)

	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("certs.json", `{"certs":[
		{"id":"c1","label":"liyou-mbpro","kind":"human","serial":"AA11","status":"active","created_at":"2026-01-01T00:00:00Z"},
		{"id":"c2","label":"agent-x","kind":"agent","serial":"BB22","status":"revoked"}]}`)
	write("agent_tokens.json", `{"tokens":[
		{"id":"tok-1","agent_id":"agent-x","hash":"h1","created_by":"a","created_at":"2026-01-01T00:00:00Z"},
		{"id":"enr-1","agent_id":"agent-y","hash":"h2","created_by":"a","created_at":"2026-01-01T00:00:00Z","kind":"enroll","expires_at":"2027-01-01T00:00:00Z"}]}`)
	write("users.json", `{"users":[{"username":"liyou","role":"ops.admin","cert_id":"c1"}]}`)
	write("sites.json", `{"sites":[{"name":"demo","ops_url":"http://x","cert_serial":"AA11","registered_at":"2026-01-01T00:00:00Z"}]}`)
	write("commands.json", `{"commands":[
		{"id":"cmd-1","agent_id":"agent-x","action":"restart","service":"nginx","status":"done","created_at":"2026-01-01T00:00:00Z"}]}`)
	write("audit.jsonl", "{\"at\":\"2026-01-01T00:00:00Z\",\"actor\":\"a\",\"cert\":\"c\",\"action\":\"act\",\"object\":\"o\",\"result\":\"ok\",\"note\":\"n\",\"remote_ip\":\"1.1.1.1\"}\n")

	db, err := getDB()
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	counts := map[string]int{}
	for _, tbl := range []string{"certs", "agent_tokens", "users", "sites", "commands", "audit"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM `+tbl).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		counts[tbl] = n
	}
	want := map[string]int{"certs": 2, "agent_tokens": 2, "users": 1, "sites": 1, "commands": 1, "audit": 1}
	for tbl, n := range want {
		if counts[tbl] != n {
			t.Fatalf("%s = %d, want %d", tbl, counts[tbl], n)
		}
	}
	// 旧 kind 缺省归一为 agent；无 kind 的旧 token 可作正式 token
	if _, ok := validAgentToken("nope-never-issued"); ok {
		t.Fatal("bogus token must fail")
	}
	for _, f := range []string{"certs.json", "agent_tokens.json", "users.json", "sites.json", "commands.json", "audit.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, f+".migrated")); err != nil {
			t.Fatalf("%s should be renamed: %v", f, err)
		}
	}
	// 语义抽查：绑定关系与站点可读
	if u, ok := getUserByCertID("c1"); !ok || u.Username != "liyou" || u.Role != "ops.admin" {
		t.Fatalf("user = %+v %v", u, ok)
	}
	if got := listSiteEntries(); len(got) != 1 || got[0].OpsURL != "http://x" {
		t.Fatalf("sites = %+v", got)
	}
	if got := readAuditTail(10); len(got) != 1 || got[0].Action != "act" {
		t.Fatalf("audit = %+v", got)
	}
}
