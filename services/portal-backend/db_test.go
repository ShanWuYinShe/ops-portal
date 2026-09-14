package main

import (
	"database/sql"
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
	// 旧版文件名与字段名是历史命名（agent_tokens.json / agent_id），迁移必须按原样解析。
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
	for _, tbl := range []string{"certs", "portal_agent_tokens", "users", "sites", "commands", "audit"} {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", tbl, err)
		}
		counts[tbl] = n
	}
	want := map[string]int{"certs": 2, "portal_agent_tokens": 2, "users": 1, "sites": 1, "commands": 1, "audit": 1}
	for tbl, n := range want {
		if counts[tbl] != n {
			t.Fatalf("%s = %d, want %d", tbl, counts[tbl], n)
		}
	}
	// 旧 kind 缺省归一为 portal-agent；无 kind 的旧 token 可作正式 token
	if _, ok := validAgentToken("nope-never-issued"); ok {
		t.Fatal("bogus token must fail")
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM certs WHERE id='c2'`).Scan(&kind); err != nil || kind != "portal-agent" {
		t.Fatalf("legacy machine cert kind = %q, %v; want portal-agent", kind, err)
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

// 旧库（表/列/kind 仍用历史命名）在启动时被幂等迁移到新命名。
func TestMigrateRenames(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)

	legacy, err := sql.Open("sqlite", filepath.Join(dir, "portal.db"))
	if err != nil {
		t.Fatalf("open legacy: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE agent_tokens (
		id TEXT PRIMARY KEY, agent_id TEXT, hash TEXT, created_by TEXT, created_at TEXT,
		revoked INTEGER, kind TEXT DEFAULT 'agent', expires_at TEXT, used INTEGER)`); err != nil {
		t.Fatalf("create agent_tokens: %v", err)
	}
	if _, err := legacy.Exec(`CREATE TABLE commands (
		id TEXT PRIMARY KEY, agent_id TEXT, action TEXT, status TEXT)`); err != nil {
		t.Fatalf("create commands: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO agent_tokens (id, agent_id, hash, kind) VALUES ('tok-1','agent-x','h1','agent')`); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if _, err := legacy.Exec(`INSERT INTO commands (id, agent_id, action, status) VALUES ('cmd-1','agent-x','restart','done')`); err != nil {
		t.Fatalf("seed command: %v", err)
	}
	legacy.Close()

	db, err := getDB()
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}
	cols := func(table string) []string {
		t.Helper()
		rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
		if err != nil {
			t.Fatalf("pragma %s: %v", table, err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var cid, notnull, pk int
			var name, ctype string
			var dflt any
			if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err == nil {
				out = append(out, name)
			}
		}
		return out
	}
	for _, table := range []string{"portal_agent_tokens", "commands"} {
		for _, c := range cols(table) {
			if c == "agent_id" {
				t.Fatalf("%s still has legacy column agent_id", table)
			}
		}
	}
	var kind string
	if err := db.QueryRow(`SELECT kind FROM portal_agent_tokens WHERE id='tok-1'`).Scan(&kind); err != nil || kind != "portal-agent" {
		t.Fatalf("token kind = %q, %v; want portal-agent", kind, err)
	}
	// 幂等：重开（再次触发迁移）不报错、数据完好
	if _, err := getDB(); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM portal_agent_tokens`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("rows after reopen = %d, %v", n, err)
	}
}
