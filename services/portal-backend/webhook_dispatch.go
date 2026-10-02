package main

// webhook 出站转发 worker：单 goroutine + ticker 扫 webhook_deliveries 中
// status=pending 的记录，按该端点的启用规则逐个 POST 转发。
// 重试退避由 attempt_no 决定（backoffForAttempt），超过规则 max_attempts 标 failed。
// 安全：目标地址在建连时刻再次校验（dialTargetControl），防注册后改 DNS 解析到内网
// （DNS rebinding）——这是 SSRF 的第二道防线，与注册侧静态校验缺一不可。

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// webhookAttemptStatus 出站尝试结果。
const (
	webhookAttemptOK      = "ok"
	webhookAttemptFailed  = "failed"
	webhookAttemptSkipped = "skipped"
)

// webhookResponseCap 落库的响应体上限（4KB）：排障够用，防大响应撑爆库。
const webhookResponseCap = 4 << 10

// backoffForAttempt 第 n 次（1-based）失败后的等待时长：5s / 30s / 2m / 10m / 30m。
// 超出档位一律取最后一档（30m），保证长尾重试不失控。
func backoffForAttempt(n int) time.Duration {
	switch {
	case n <= 1:
		return 5 * time.Second
	case n == 2:
		return 30 * time.Second
	case n == 3:
		return 2 * time.Minute
	case n == 4:
		return 10 * time.Minute
	default:
		return 30 * time.Minute
	}
}

// webhookDispatchInterval 扫描周期（WEBHOOK_DISPATCH_INTERVAL，默认 5s；非法或过小回退默认）。
func webhookDispatchInterval() time.Duration {
	d, err := time.ParseDuration(getEnv("WEBHOOK_DISPATCH_INTERVAL", "5s"))
	if err != nil || d < time.Second {
		return 5 * time.Second
	}
	return d
}

// webhookMaxAttemptsCap 规则重试上限的兜底（超范围归一），防配置成天文数字。
func normalizeMaxAttempts(n int) int {
	if n <= 0 {
		return 3
	}
	if n > 10 {
		return 10
	}
	return n
}

// dialTargetControl 出站建连校验：以真实连接目标 IP 判定，不信任域名语义。
// 与 ssrfguard.go 的 probeDialControl 同思路（逃生阀共用 OPS_PROBE_ALLOW_PRIVATE）。
func dialTargetControl(_, address string, _ syscall.RawConn) error {
	if probeAllowPrivate() {
		return nil
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("webhook: bad dial address %q", address)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("webhook: non-IP dial target %q", address)
	}
	if !isPublicIP(ip) {
		return fmt.Errorf("webhook: refused non-public dial target %s", ip)
	}
	return nil
}

// startWebhookDispatcher 启动后台转发循环（main 启动时调用一次）。
func startWebhookDispatcher() {
	go func() {
		t := time.NewTicker(webhookDispatchInterval())
		defer t.Stop()
		for range t.C {
			dispatchPendingWebhooks(time.Now())
		}
	}()
}

// pendingWebhookRow 扫描出的待转发记录（只需转发所需字段）。
type pendingWebhookRow struct {
	ID          string
	EndpointID  string
	Slug        string
	ContentType string
	Body        string
	EventType   string
	DedupKey    string
}

// dispatchPendingWebhooks 扫一轮待转发记录并逐条尝试。
// 每条记录独立处理，单条失败不影响其他记录（worker 不能因一条坏数据停摆）。
func dispatchPendingWebhooks(now time.Time) {
	db, err := getDB()
	if err != nil {
		log.Printf("webhook: dispatcher getDB failed: %v", err)
		return
	}
	rows, err := db.Query(`SELECT id,endpoint_id,slug,content_type,body,event_type,dedup_key
		FROM webhook_deliveries WHERE status=? ORDER BY received_at ASC LIMIT 50`, webhookStatusPending)
	if err != nil {
		log.Printf("webhook: dispatcher query failed: %v", err)
		return
	}
	var pending []pendingWebhookRow
	for rows.Next() {
		var p pendingWebhookRow
		if err := rows.Scan(&p.ID, &p.EndpointID, &p.Slug, &p.ContentType, &p.Body, &p.EventType, &p.DedupKey); err != nil {
			log.Printf("webhook: dispatcher scan failed: %v", err)
			continue
		}
		pending = append(pending, p)
	}
	rows.Close()
	for _, p := range pending {
		dispatchOneWebhook(db, p, now)
	}
}

// dispatchOneWebhook 转发单条投递到该端点的全部启用规则。
// 判定：全部规则成功 → delivered；任一本轮仍可重试 → 保持 pending（等下次 tick）；
// 全部规则都超过重试上限 → failed。
func dispatchOneWebhook(db *sql.DB, p pendingWebhookRow, now time.Time) {
	sources, err := listSources(p.EndpointID)
	if err != nil {
		log.Printf("webhook: list sources for %s failed: %v", p.EndpointID, err)
		return
	}
	var active []webhookSource
	for _, s := range sources {
		if s.Enabled {
			active = append(active, s)
		}
	}
	if len(active) == 0 {
		// 没有可转发目标：视为投递完成（消息已被接收并留存），不无限 pending。
		_, _ = db.Exec(`UPDATE webhook_deliveries SET status=?, reject_reason=? WHERE id=? AND status=?`,
			webhookStatusDelivered, "no enabled source", p.ID, webhookStatusPending)
		return
	}
	allDone := true    // 所有规则都已终结（成功或彻底失败）
	anyServed := false // 至少一条规则本轮成功
	for _, s := range active {
		// 该规则已做过的尝试次数决定本次是第几次尝试与是否需要等待退避。
		attemptNo, lastAt, err := lastWebhookAttempt(db, p.ID, s.ID)
		if err != nil {
			log.Printf("webhook: attempt lookup %s/%s failed: %v", p.ID, s.ID, err)
			allDone = false
			continue
		}
		maxAttempts := normalizeMaxAttempts(s.MaxAttempts)
		if attemptNo >= maxAttempts {
			// 已达上限：该规则终结（不再重试）。
			continue
		}
		if attemptNo > 0 && now.Sub(lastAt) < backoffForAttempt(attemptNo) {
			// 退避窗口未到，保持 pending，下轮再试。
			allDone = false
			continue
		}
		status, httpStatus, respBody, errMsg, dur := deliverWebhook(s, p)
		if err := insertWebhookAttempt(webhookAttempt{
			DeliveryID:   p.ID,
			SourceID:     s.ID,
			AttemptNo:    attemptNo + 1,
			Status:       status,
			HTTPStatus:   httpStatus,
			Error:        errMsg,
			DurationMS:   dur,
			RequestBody:  p.Body,
			ResponseBody: respBody,
			At:           now.UTC().Format(time.RFC3339),
		}); err != nil {
			log.Printf("webhook: record attempt %s/%s failed: %v", p.ID, s.ID, err)
		}
		if status == webhookAttemptOK {
			anyServed = true
			continue
		}
		if attemptNo+1 < maxAttempts {
			allDone = false // 还能再试
		}
	}
	switch {
	case anyServed:
		_, _ = db.Exec(`UPDATE webhook_deliveries SET status=? WHERE id=? AND status=?`,
			webhookStatusDelivered, p.ID, webhookStatusPending)
	case allDone:
		_, _ = db.Exec(`UPDATE webhook_deliveries SET status=?, reject_reason=? WHERE id=? AND status=?`,
			webhookStatusFailed, "超过最大重试次数", p.ID, webhookStatusPending)
	default:
		// 仍有规则在退避等待中，保持 pending。
	}
}

// lastWebhookAttempt 取该投递在该规则下最后一次尝试的（次数, 时间）。无记录返回 (0, zero)。
// 时间解析失败按「刚发生过」处理——宁可多等一轮退避，也不要打垮下游。
func lastWebhookAttempt(db *sql.DB, deliveryID, sourceID string) (int, time.Time, error) {
	var n int
	var at string
	err := db.QueryRow(`SELECT attempt_no, at FROM webhook_attempts
		WHERE delivery_id=? AND source_id=? ORDER BY attempt_no DESC, id DESC LIMIT 1`,
		deliveryID, sourceID).Scan(&n, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, time.Time{}, nil
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	ts, perr := time.Parse(time.RFC3339, at)
	if perr != nil {
		return n, time.Now(), nil
	}
	return n, ts, nil
}

// insertWebhookAttempt 落一次出站尝试。
// 注意 webhook_attempts.id 是 INTEGER PRIMARY KEY AUTOINCREMENT（自增），
// 插入时不给 id 赋值，由 SQLite 生成。
func insertWebhookAttempt(a webhookAttempt) error {
	db, err := getDB()
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO webhook_attempts
		(delivery_id,source_id,attempt_no,status,http_status,error,duration_ms,request_body,response_body,at)
		VALUES (?,?,?,?,?,?,?,?,?,?)`,
		a.DeliveryID, a.SourceID, a.AttemptNo, a.Status, a.HTTPStatus, a.Error,
		a.DurationMS, a.RequestBody, truncateForStore(a.ResponseBody, webhookResponseCap), a.At)
	return err
}

// clearDeliveryAttempts 清空某投递的全部出站尝试记录（人工重放时调用）。
func clearDeliveryAttempts(deliveryID string) error {
	db, err := getDB()
	if err != nil {
		return err
	}
	_, err = db.Exec(`DELETE FROM webhook_attempts WHERE delivery_id=?`, deliveryID)
	return err
}

// truncateForStore 截断超长文本（响应体落库上限）。
func truncateForStore(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...(truncated)"
}

// deliverWebhook 执行一次出站 POST。返回（尝试状态, HTTP 状态码, 响应体, 错误文本, 耗时 ms）。
// 非 2xx 视为失败（下游明确拒收，重试有意义）。
func deliverWebhook(s webhookSource, p pendingWebhookRow) (string, int, string, string, int64) {
	cli := webhookHTTPClient(s.TimeoutMS)
	body := []byte(p.Body)
	req, err := http.NewRequest(http.MethodPost, s.TargetURL, bytes.NewReader(body))
	if err != nil {
		return webhookAttemptFailed, 0, "", err.Error(), 0
	}
	ct := strings.TrimSpace(p.ContentType)
	if ct == "" {
		ct = "application/json"
	}
	req.Header.Set("Content-Type", ct)
	req.Header.Set("X-Webhook-Delivery", p.ID)
	if p.EventType != "" {
		req.Header.Set("X-Webhook-Event", p.EventType)
	}
	// 出站签名用 source.secret；规则未配置 secret 时明确不发送签名头
	//（下游据此判定匿名来源，portal 侧不伪造一个空密钥签名）。
	if s.Secret != "" {
		mac := hmac.New(sha256.New, []byte(s.Secret))
		mac.Write(body)
		req.Header.Set("X-Webhook-Signature", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	}
	start := time.Now()
	resp, err := cli.Do(req)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		return webhookAttemptFailed, 0, "", err.Error(), dur
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, webhookResponseCap))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return webhookAttemptFailed, resp.StatusCode, string(respBody), resp.Status, dur
	}
	return webhookAttemptOK, resp.StatusCode, string(respBody), "", dur
}

// webhookHTTPClient 出站 client：超时用规则 timeout_ms，建连过 SSRF dial 校验，
// 且不跟随重定向（重定向目标会绕过注册时的校验，直接当失败处理更安全）。
func webhookHTTPClient(timeoutMS int) *http.Client {
	if timeoutMS <= 0 {
		timeoutMS = 10000
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second, Control: dialTargetControl}
	return &http.Client{
		Timeout: time.Duration(timeoutMS) * time.Millisecond,
		Transport: &http.Transport{
			DialContext:           dialer.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: time.Duration(timeoutMS) * time.Millisecond,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("webhook: redirect not followed")
		},
	}
}
