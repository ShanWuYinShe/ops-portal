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
	fresh := time.Now().UTC().Format(time.RFC3339)
	s := &commandStore{Commands: []portalCommand{
		{ID: "cmd-old", AgentID: "a1", Action: "restart", Service: "nginx", Status: "queued", CreatedAt: old},
		{ID: "cmd-new", AgentID: "a1", Action: "restart", Service: "nginx", Status: "queued", CreatedAt: fresh},
	}}
	if err := saveCommands(s); err != nil {
		t.Fatalf("save: %v", err)
	}
	got := takeCommands("a1")
	if len(got) != 1 || got[0].ID != "cmd-new" {
		t.Fatalf("take = %+v", got)
	}
	after, _ := loadCommands()
	statusOf := map[string]string{}
	for _, c := range after.Commands {
		statusOf[c.ID] = c.Status
	}
	if statusOf["cmd-old"] != "expired" || statusOf["cmd-new"] != "acked" {
		t.Fatalf("status = %v", statusOf)
	}

	// 裁剪：505 条终结 + 2 条 queued，再入队一条后终结只剩 500
	var cmds []portalCommand
	for i := 0; i < 505; i++ {
		cmds = append(cmds, portalCommand{ID: "t", AgentID: "a", Action: "restart", Service: "s", Status: "done", CreatedAt: fresh})
	}
	cmds = append(cmds,
		portalCommand{ID: "q1", AgentID: "a", Action: "restart", Service: "s", Status: "queued", CreatedAt: fresh},
		portalCommand{ID: "q2", AgentID: "a", Action: "restart", Service: "s", Status: "acked", CreatedAt: fresh},
	)
	if err := saveCommands(&commandStore{Commands: cmds}); err != nil {
		t.Fatalf("save2: %v", err)
	}
	if _, err := enqueueCommand(portalCommand{AgentID: "a", Action: "restart", Service: "s"}); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	cur, _ := loadCommands()
	var terminal, queued int
	for _, c := range cur.Commands {
		if c.Status == "queued" || c.Status == "acked" {
			queued++
		} else {
			terminal++
		}
	}
	if terminal != maxTerminalCommands || queued != 3 {
		t.Fatalf("terminal=%d queued=%d", terminal, queued)
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
