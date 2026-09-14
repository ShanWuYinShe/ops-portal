package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// 过期 queued 命令不再下发（标 expired）；终结命令裁剪到 500 条，queued 全留。
func TestCommandTTLAndPrune(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)

	old := time.Now().Add(-25 * time.Hour).UTC().Format(time.RFC3339)
	oldCmd, err := enqueueCommand(portalCommand{AgentID: "a1", Action: "restart", Service: "nginx"})
	if err != nil {
		t.Fatalf("enqueue old: %v", err)
	}
	newCmd, err := enqueueCommand(portalCommand{AgentID: "a1", Action: "restart", Service: "nginx"})
	if err != nil {
		t.Fatalf("enqueue new: %v", err)
	}
	db, err := getDB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	// 把第一条的人工调老 25 小时（ enqueue 统一写 now）
	if _, err := db.Exec(`UPDATE commands SET created_at=? WHERE id=?`, old, oldCmd.ID); err != nil {
		t.Fatalf("age: %v", err)
	}
	got := takeCommands("a1")
	if len(got) != 1 || got[0].ID != newCmd.ID {
		t.Fatalf("take = %+v", got)
	}
	statusOf := map[string]string{}
	for _, c := range listCommands("a1") {
		statusOf[c.ID] = c.Status
	}
	if statusOf[oldCmd.ID] != "expired" || statusOf[newCmd.ID] != "acked" {
		t.Fatalf("status = %v", statusOf)
	}

	// 裁剪：505 条终结 + 2 条 queued/acked，再入队一条后终结只剩 500
	for i := 0; i < 505; i++ {
		c, err := enqueueCommand(portalCommand{AgentID: "a", Action: "restart", Service: "s"})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		if _, err := db.Exec(`UPDATE commands SET status='done' WHERE id=?`, c.ID); err != nil {
			t.Fatalf("seed status: %v", err)
		}
	}
	if _, err := enqueueCommand(portalCommand{AgentID: "a", Action: "restart", Service: "s"}); err != nil {
		t.Fatalf("seed q1: %v", err)
	}
	if _, err := enqueueCommand(portalCommand{AgentID: "a", Action: "restart", Service: "s"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	var terminal, pending int
	if err := db.QueryRow(`SELECT COUNT(*) FROM commands WHERE status NOT IN ('queued','acked')`).Scan(&terminal); err != nil {
		t.Fatalf("count terminal: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM commands WHERE status IN ('queued','acked')`).Scan(&pending); err != nil {
		t.Fatalf("count pending: %v", err)
	}
	// pending：a1 的 acked + expired？expired 属终结侧；a1 留 acked(1) + a 侧 q1 与末条(2) = 3
	if terminal != maxTerminalCommands || pending != 3 {
		t.Fatalf("terminal=%d pending=%d", terminal, pending)
	}
}

// 拨测：2xx 可达、非 2xx/连不上不可达。
func TestProbeOne(t *testing.T) {
	okSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ops/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer okSrv.Close()
	badSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer badSrv.Close()

	if res := probeOne(siteEntry{Name: "a", OpsURL: okSrv.URL}, http.DefaultClient); !res.Reachable {
		t.Fatalf("ok server: %+v", res)
	}
	if res := probeOne(siteEntry{Name: "b", OpsURL: okSrv.URL + "/"}, http.DefaultClient); !res.Reachable {
		t.Fatalf("trailing slash: %+v", res)
	}
	if res := probeOne(siteEntry{Name: "c", OpsURL: badSrv.URL}, http.DefaultClient); res.Reachable {
		t.Fatalf("500 should be unreachable: %+v", res)
	}
	if res := probeOne(siteEntry{Name: "d", OpsURL: "http://127.0.0.1:1"}, http.DefaultClient); res.Reachable || res.Note == "" {
		t.Fatalf("refused should be unreachable with note: %+v", res)
	}
}
