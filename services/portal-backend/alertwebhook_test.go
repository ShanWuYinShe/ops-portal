package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeAlertSender 假渠道：把 Send 调用写进 channel，用于验证 emitAlert 的接线。
type fakeAlertSender struct{ ch chan string }

func (f *fakeAlertSender) Send(kind, subject, _ string) error {
	f.ch <- kind + "/" + subject
	return nil
}

// 未配置 ALERT_WEBHOOK_URL：不构造渠道（保持"只写审计与日志"的原行为）。
func TestAlertSenderUnconfigured(t *testing.T) {
	t.Setenv("ALERT_WEBHOOK_URL", "")
	if s := webhookAlertSenderFromEnv(); s != nil {
		t.Fatalf("sender = %#v, want nil（未配置不外发）", s)
	}
}

// 配置非法：返回 nil（不静默持有一个必然失败的目标）。
func TestAlertSenderInvalidURL(t *testing.T) {
	t.Setenv("OPS_PROBE_ALLOW_PRIVATE", "") // 私网目标按默认拒绝
	cases := map[string]string{
		"非 http(s)":  "ftp://example.com/hook",
		"带 userinfo": "http://user:pw@example.com/hook",
		"缺 host":     "https://",
		"私网目标":       "http://127.0.0.1:9/hook",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("ALERT_WEBHOOK_URL", raw)
			if s := webhookAlertSenderFromEnv(); s != nil {
				t.Fatalf("sender = %#v, want nil（目标 %q 非法）", s, raw)
			}
		})
	}
}

// 出站投递：方法/头/签名/payload 与 webhook 网关出站转发同构。
func TestAlertWebhookSenderDelivery(t *testing.T) {
	var (
		gotBody  []byte
		gotSig   string
		gotEvent string
		gotCT    string
		gotPath  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotSig = r.Header.Get("X-Webhook-Signature")
		gotEvent = r.Header.Get("X-Webhook-Event")
		gotCT = r.Header.Get("Content-Type")
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	t.Setenv("OPS_PROBE_ALLOW_PRIVATE", "1") // 放行环回：与拨测/webhook 转发共用同一逃生阀
	t.Setenv("ALERT_WEBHOOK_URL", srv.URL+"/hook")
	t.Setenv("ALERT_WEBHOOK_SECRET", "s3cret")
	s := webhookAlertSenderFromEnv()
	if s == nil {
		t.Fatal("sender = nil, want 构造成功")
	}
	if err := s.Send("probe_down", "hotspot", "站点拨测连续不可达：timeout"); err != nil {
		t.Fatalf("Send: %v", err)
	}
	if gotPath != "/hook" {
		t.Fatalf("path = %q, want /hook", gotPath)
	}
	if gotCT != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", gotCT)
	}
	if gotEvent != "probe_down" {
		t.Fatalf("X-Webhook-Event = %q, want probe_down", gotEvent)
	}
	mac := hmac.New(sha256.New, []byte("s3cret"))
	mac.Write(gotBody)
	if want := "sha256=" + hex.EncodeToString(mac.Sum(nil)); gotSig != want {
		t.Fatalf("X-Webhook-Signature = %q, want %q", gotSig, want)
	}
	var p alertWebhookPayload
	if err := json.Unmarshal(gotBody, &p); err != nil {
		t.Fatalf("payload 不是 JSON: %v (%s)", err, gotBody)
	}
	if p.Event != "probe_down" || p.Subject != "hotspot" || p.Detail == "" || p.Text == "" || p.Time == "" {
		t.Fatalf("payload = %+v, 字段不完整", p)
	}
}

// 非 2xx 视为失败（与 webhook 出站转发同判定），错误由 emitAlert 记日志。
func TestAlertWebhookSenderNon2xx(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	t.Setenv("OPS_PROBE_ALLOW_PRIVATE", "1")
	t.Setenv("ALERT_WEBHOOK_URL", srv.URL+"/hook")
	s := webhookAlertSenderFromEnv()
	if s == nil {
		t.Fatal("sender = nil, want 构造成功")
	}
	if err := s.Send("agent_offline", "agent-server-01", "心跳超时"); err == nil {
		t.Fatal("Send = nil, want 非 2xx 报错")
	}
}

// 接线：emitAlert 走配置好的渠道。发送是异步的，用 channel 等待而不是 sleep。
func TestEmitAlertInvokesSender(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	t.Setenv("ALERT_COOLDOWN", "30m")
	if _, err := getDB(); err != nil {
		t.Fatalf("getDB: %v", err)
	}
	fake := &fakeAlertSender{ch: make(chan string, 4)}
	alertState.Lock()
	alertState.lastSent = map[string]time.Time{}
	alertState.sender = fake
	alertState.Unlock()
	t.Cleanup(func() {
		alertState.Lock()
		alertState.sender = nil
		alertState.Unlock()
	})

	emitAlert(time.Now(), "agent_offline", "agent-server-01", "心跳超时")
	select {
	case got := <-fake.ch:
		if got != "agent_offline/agent-server-01" {
			t.Fatalf("渠道收到 %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("超时未调用通知渠道")
	}
}
