package main

// 门户自身告警的出站渠道：把告警引擎（agent 掉线/恢复、拨测不可达/恢复/降级）
// 产生的告警 POST 到 ALERT_WEBHOOK_URL。
//
// 与 webhook 网关（入站接收 + 按规则转发）是两条独立链路：这里发的是 portal
// 自己观测到的状态跃迁，不经过 webhook_deliveries，因此没有落库/重试/重放
// ——重复抑制由告警引擎的 ALERT_COOLDOWN 负责。
//
// 安全：复用 webhook 出站的同一套 SSRF 防线（validateWebhookTargetURL 静态
// 校验 + dialTargetControl 建连校验），逃生阀也共用 OPS_PROBE_ALLOW_PRIVATE
// （私网目标默认拒绝，纯内网自托管联调才放开）。
//
// 签名：配 ALERT_WEBHOOK_SECRET 时对 body 做 HMAC-SHA256，头格式与 webhook
// 网关出站转发一致（X-Webhook-Signature: sha256=<hex>），接收方可复用同一份校验。

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"time"
)

// alertWebhookTimeout 单次投递上限：告警是旁路，不能长时间占住调用方。
const alertWebhookTimeout = 10 * time.Second

// alertWebhookPayload 出站告警负载：结构化字段供自建接收器/分流规则使用，
// text 供只认自然语言的 IM 机器人（Telegram/飞书/钉钉）直接展示。
type alertWebhookPayload struct {
	Event   string `json:"event"`
	Subject string `json:"subject"`
	Detail  string `json:"detail"`
	Text    string `json:"text"`
	Time    string `json:"time"`
}

type alertWebhookSender struct {
	url    string
	secret string
	client *http.Client
}

func alertWebhookURL() string { return strings.TrimSpace(getEnv("ALERT_WEBHOOK_URL", "")) }

func alertWebhookSecret() string { return strings.TrimSpace(getEnv("ALERT_WEBHOOK_SECRET", "")) }

// webhookAlertSenderFromEnv 构造出站渠道：未配置 URL 返回 nil（只写审计与日志，
// 与该变量引入前的行为一致）；配置非法时高声记日志后同样返回 nil，
// 不静默持有一个必然失败的目标。
func webhookAlertSenderFromEnv() alertSender {
	raw := alertWebhookURL()
	if raw == "" {
		return nil
	}
	u, err := validateWebhookTargetURL(raw)
	if err != nil {
		log.Printf("alert: ALERT_WEBHOOK_URL 不可用，告警不外发（仍写审计与日志）: %v", err)
		return nil
	}
	return &alertWebhookSender{url: u, secret: alertWebhookSecret(), client: alertWebhookHTTPClient()}
}

func (s *alertWebhookSender) Send(kind, subject, detail string) error {
	body, err := json.Marshal(alertWebhookPayload{
		Event:   kind,
		Subject: subject,
		Detail:  detail,
		Text:    alertWebhookText(kind, subject, detail),
		Time:    time.Now().UTC().Format(time.RFC3339),
	})
	if err != nil {
		return err
	}
	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Webhook-Event", kind)
	if s.secret != "" {
		mac := hmac.New(sha256.New, []byte(s.secret))
		mac.Write(body)
		req.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("alert webhook %s: %s", s.url, resp.Status)
	}
	return nil
}

// alertWebhookText 拼一行人类可读文案（机器人只认 text 时用它）。
func alertWebhookText(kind, subject, detail string) string {
	msg := "[ops-portal] " + kind
	if subject != "" {
		msg += " " + subject
	}
	if detail != "" {
		msg += "：" + detail
	}
	return msg
}

// alertWebhookHTTPClient 出站 client：建连过 SSRF dial 校验，不跟随重定向
// （重定向目标会绕过静态校验，直接按失败处理），超时 alertWebhookTimeout。
func alertWebhookHTTPClient() *http.Client {
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: dialTargetControl}
	return &http.Client{
		Timeout: alertWebhookTimeout,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: alertWebhookTimeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("alert webhook: redirect not followed")
		},
	}
}
