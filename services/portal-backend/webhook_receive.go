package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// webhook 网关数据层 + 入站接收端点（设计见 .workwork/plan/webhook-design.md）。
// 接收侧无证书鉴权（公网可达），安全全靠 per-endpoint secret 的 HMAC 校验；
// 管理侧走 requireRole。出站转发 worker 在 webhook_deliver.go（另一交付）。

// ---------- 数据模型 ----------

type webhookEndpoint struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	Secret    string `json:"secret,omitempty"` // 出站/入站共用的 HMAC 密钥；列表接口打码
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
	CreatedBy string `json:"created_by"`
	Enabled   bool   `json:"enabled"`
}

type webhookSource struct {
	ID          string `json:"id"`
	EndpointID  string `json:"endpoint_id"`
	Name        string `json:"name"`
	TargetURL   string `json:"target_url"`
	Secret      string `json:"secret,omitempty"`
	CreatedAt   string `json:"created_at"`
	Enabled     bool   `json:"enabled"`
	MaxAttempts int    `json:"max_attempts"`
	TimeoutMS   int    `json:"timeout_ms"`
}

type webhookDelivery struct {
	ID           string            `json:"id"`
	EndpointID   string            `json:"endpoint_id"`
	Slug         string            `json:"slug"`
	ReceivedAt   string            `json:"received_at"`
	RemoteIP     string            `json:"remote_ip"`
	Method       string            `json:"method"`
	ContentType  string            `json:"content_type"`
	Body         string            `json:"body,omitempty"`
	Status       string            `json:"status"`
	RejectReason string            `json:"reject_reason,omitempty"`
	EventType    string            `json:"event_type"`
	DedupKey     string            `json:"dedup_key"`
	Headers      map[string]string `json:"headers,omitempty"`
	BodyBytes    int               `json:"body_bytes"`
	SigOK        bool              `json:"sig_ok"`
}

type webhookAttempt struct {
	ID           string `json:"id"`
	DeliveryID   string `json:"delivery_id"`
	SourceID     string `json:"source_id"`
	Status       string `json:"status"`
	Error        string `json:"error,omitempty"`
	RequestBody  string `json:"request_body,omitempty"`
	ResponseBody string `json:"response_body,omitempty"`
	At           string `json:"at"`
	AttemptNo    int    `json:"attempt_no"`
	HTTPStatus   int    `json:"http_status"`
	DurationMS   int64  `json:"duration_ms"`
}

// 投递状态：pending（待转发）/ delivered（全部目标成功）/ failed（超过重试上限）
// / rejected（入站即拒：签名错、超限等）/ dedup（命中幂等去重，不重复转发）。
const (
	webhookStatusPending   = "pending"
	webhookStatusDelivered = "delivered"
	webhookStatusFailed    = "failed"
	webhookStatusRejected  = "rejected"
	webhookStatusDedup     = "dedup"
)

// webhookMaxBody 入站 body 上限 256KB：超限整条拒收（413），不落库。
const webhookMaxBody = 256 << 10

const webhookEndpointColumns = `id,name,slug,secret,enabled,note,created_at,created_by`
const webhookSourceColumns = `id,endpoint_id,name,target_url,secret,enabled,max_attempts,timeout_ms,created_at`
const webhookDeliveryColumns = `id,endpoint_id,slug,received_at,remote_ip,method,content_type,headers,body,body_bytes,sig_ok,status,reject_reason,event_type,dedup_key`
const webhookAttemptColumns = `id,delivery_id,source_id,attempt_no,status,http_status,error,duration_ms,request_body,response_body,at`

func scanWebhookEndpoint(row interface{ Scan(dest ...any) error }) (*webhookEndpoint, error) {
	e := &webhookEndpoint{}
	var enabled int
	if err := row.Scan(&e.ID, &e.Name, &e.Slug, &e.Secret, &enabled, &e.Note, &e.CreatedAt, &e.CreatedBy); err != nil {
		return nil, err
	}
	e.Enabled = enabled != 0
	return e, nil
}

func scanWebhookSource(row interface{ Scan(dest ...any) error }) (*webhookSource, error) {
	s := &webhookSource{}
	var enabled int
	if err := row.Scan(&s.ID, &s.EndpointID, &s.Name, &s.TargetURL, &s.Secret, &enabled,
		&s.MaxAttempts, &s.TimeoutMS, &s.CreatedAt); err != nil {
		return nil, err
	}
	s.Enabled = enabled != 0
	return s, nil
}

func scanWebhookDelivery(row interface{ Scan(dest ...any) error }) (*webhookDelivery, error) {
	d := &webhookDelivery{}
	var headers string
	var sigOK int
	if err := row.Scan(&d.ID, &d.EndpointID, &d.Slug, &d.ReceivedAt, &d.RemoteIP, &d.Method,
		&d.ContentType, &headers, &d.Body, &d.BodyBytes, &sigOK, &d.Status,
		&d.RejectReason, &d.EventType, &d.DedupKey); err != nil {
		return nil, err
	}
	d.SigOK = sigOK != 0
	d.Headers = unmarshalHeaders(headers)
	return d, nil
}

func scanWebhookAttempt(row interface{ Scan(dest ...any) error }) (*webhookAttempt, error) {
	a := &webhookAttempt{}
	if err := row.Scan(&a.ID, &a.DeliveryID, &a.SourceID, &a.AttemptNo, &a.Status, &a.HTTPStatus,
		&a.Error, &a.DurationMS, &a.RequestBody, &a.ResponseBody, &a.At); err != nil {
		return nil, err
	}
	return a, nil
}

// marshalfebhookHeaders 落入 headers 列（JSON 文本）；空 map 也存 `{}` 便于解析。
func marshalWebhookHeaders(h map[string]string) string {
	if len(h) == 0 {
		return "{}"
	}
	b, err := json.Marshal(h)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func unmarshalHeaders(s string) map[string]string {
	out := map[string]string{}
	if s == "" {
		return out
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil || out == nil {
		return map[string]string{}
	}
	return out
}

// ---------- 加密辅助 ----------

// maskSecret 打码展示：保留首尾各 4 位，中间定长 8 个 *（不泄露原长）。
func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 8 {
		return "********"
	}
	return s[:4] + "********" + s[len(s)-4:]
}

// verifyWebhookSignature 校验入站签名（HMAC-SHA256）。
// 签名头兼容 X-Webhook-Signature 与 GitHub 的 X-Hub-Signature-256，格式
// "sha256=<hex>"（前缀缺失时按裸 hex 处理）。tsHeader 非空时参与签名，签名串为
// "<ts>." + body（时间戳防重放，容忍 300s 偏移）。任何一步不满足即拒。
func verifyWebhookSignature(secret string, body []byte, sigHeader, tsHeader string) bool {
	if secret == "" {
		return false
	}
	sig := strings.TrimSpace(sigHeader)
	if sig == "" {
		return false
	}
	sig = strings.TrimPrefix(sig, "sha256=")
	sig = strings.TrimSpace(sig)
	got, err := hex.DecodeString(strings.ToLower(sig))
	if err != nil || len(got) == 0 {
		return false
	}
	ts := strings.TrimSpace(tsHeader)
	payload := body
	if ts != "" {
		// 时间戳必须能解析且落在容忍窗口内，否则视为重放/时钟异常拒绝。
		n, err := strconv.ParseInt(ts, 10, 64)
		if err != nil {
			return false
		}
		skew := time.Since(time.Unix(n, 0))
		if skew < 0 {
			skew = -skew
		}
		if skew > webhookTimestampTolerance {
			return false
		}
		payload = append([]byte(ts+"."), body...)
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	want := mac.Sum(nil)
	return hmac.Equal(want, got)
}

// webhookTimestampTolerance 时间戳容忍偏移（设计约定 300s）。
const webhookTimestampTolerance = 300 * time.Second

// ---------- 端点 ----------

func listEndpoints() ([]webhookEndpoint, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT ` + webhookEndpointColumns + ` FROM webhook_endpoints ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []webhookEndpoint{}
	for rows.Next() {
		e, err := scanWebhookEndpoint(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *e)
	}
	return out, rows.Err()
}

// errWebhookNotFound 统一的「记录不存在」哨兵错误，handler 据此回 404。
var errWebhookNotFound = errors.New("not found")

func getEndpointBySlug(slug string) (*webhookEndpoint, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	e, err := scanWebhookEndpoint(db.QueryRow(
		`SELECT `+webhookEndpointColumns+` FROM webhook_endpoints WHERE slug=? LIMIT 1`, slug))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errWebhookNotFound
	}
	if err != nil {
		return nil, err
	}
	return e, nil
}

// upsertEndpoint 新建或更新端点。ID 为空时生成 "wh-ep-"+randHex(4) 并落 created_at；
// slug 全局唯一（冲突由 UNIQUE 约束报错，交由 handler 转 400）。
func upsertEndpoint(e webhookEndpoint) error {
	e.Name = strings.TrimSpace(e.Name)
	e.Slug = strings.TrimSpace(e.Slug)
	if e.Slug == "" {
		return errors.New("slug required")
	}
	db, err := getDB()
	if err != nil {
		return err
	}
	if e.ID == "" {
		e.ID = "wh-ep-" + randHex(4)
		e.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		_, err = db.Exec(`INSERT INTO webhook_endpoints (`+webhookEndpointColumns+`)
			VALUES (?,?,?,?,?,?,?,?)`,
			e.ID, e.Name, e.Slug, e.Secret, boolInt(e.Enabled), e.Note, e.CreatedAt, e.CreatedBy)
		return err
	}
	// 更新：created_at/created_by 保持首次落库值不变（审计需要）。
	res, err := db.Exec(`UPDATE webhook_endpoints
		SET name=?, slug=?, secret=?, enabled=?, note=? WHERE id=?`,
		e.Name, e.Slug, e.Secret, boolInt(e.Enabled), e.Note, e.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("not found")
	}
	return nil
}

// removeEndpoint 删除端点并级联删其 sources（deliveries 保留作历史排障记录）。
func removeEndpoint(id string) error {
	db, err := getDB()
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM webhook_sources WHERE endpoint_id=?`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`DELETE FROM webhook_endpoints WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("not found")
	}
	return tx.Commit()
}

// rotateEndpointSecret 轮换端点密钥，返回新明文（只此一次可读）。
func rotateEndpointSecret(id string) (string, error) {
	db, err := getDB()
	if err != nil {
		return "", err
	}
	secret := "whsec_" + randHex(24)
	res, err := db.Exec(`UPDATE webhook_endpoints SET secret=? WHERE id=?`, secret, id)
	if err != nil {
		return "", err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", errors.New("not found")
	}
	return secret, nil
}

// ---------- 转发规则 ----------

func listSources(endpointID string) ([]webhookSource, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + webhookSourceColumns + ` FROM webhook_sources`
	var args []any
	if endpointID != "" {
		q += ` WHERE endpoint_id=?`
		args = append(args, endpointID)
	}
	q += ` ORDER BY created_at DESC`
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []webhookSource{}
	for rows.Next() {
		s, err := scanWebhookSource(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *s)
	}
	return out, rows.Err()
}

// upsertSource 新建或更新转发规则。MaxAttempts/TimeoutMS 缺省补 3/10000。
func upsertSource(s webhookSource) error {
	s.Name = strings.TrimSpace(s.Name)
	s.TargetURL = strings.TrimSpace(s.TargetURL)
	if s.EndpointID == "" {
		return errors.New("endpoint_id required")
	}
	if s.TargetURL == "" {
		return errors.New("target_url required")
	}
	if s.MaxAttempts <= 0 {
		s.MaxAttempts = 3
	}
	if s.TimeoutMS <= 0 {
		s.TimeoutMS = 10000
	}
	db, err := getDB()
	if err != nil {
		return err
	}
	if s.ID == "" {
		s.ID = "wh-src-" + randHex(4)
		s.CreatedAt = time.Now().UTC().Format(time.RFC3339)
		_, err = db.Exec(`INSERT INTO webhook_sources (`+webhookSourceColumns+`)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			s.ID, s.EndpointID, s.Name, s.TargetURL, s.Secret, boolInt(s.Enabled),
			s.MaxAttempts, s.TimeoutMS, s.CreatedAt)
		return err
	}
	res, err := db.Exec(`UPDATE webhook_sources
		SET name=?, target_url=?, secret=?, enabled=?, max_attempts=?, timeout_ms=? WHERE id=?`,
		s.Name, s.TargetURL, s.Secret, boolInt(s.Enabled), s.MaxAttempts, s.TimeoutMS, s.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("not found")
	}
	return nil
}

func removeSource(id string) error {
	db, err := getDB()
	if err != nil {
		return err
	}
	res, err := db.Exec(`DELETE FROM webhook_sources WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("not found")
	}
	return nil
}

// ---------- 投递记录 ----------

// listDeliveries 投递列表（不含 body 全文，UI 列表页不需要）。
func listDeliveries(limit int, status, endpointID string) ([]webhookDelivery, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	q := `SELECT ` + webhookDeliveryColumns + ` FROM webhook_deliveries`
	var where []string
	var args []any
	if status != "" {
		where = append(where, `status=?`)
		args = append(args, status)
	}
	if endpointID != "" {
		where = append(where, `endpoint_id=?`)
		args = append(args, endpointID)
	}
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY received_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []webhookDelivery{}
	for rows.Next() {
		d, err := scanWebhookDelivery(rows)
		if err != nil {
			return nil, err
		}
		// 列表不返回 body/headers 全文（详情接口才给），避免响应膨胀
		d.Body = ""
		d.Headers = nil
		out = append(out, *d)
	}
	return out, rows.Err()
}

func getDelivery(id string) (*webhookDelivery, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	d, err := scanWebhookDelivery(db.QueryRow(
		`SELECT `+webhookDeliveryColumns+` FROM webhook_deliveries WHERE id=? LIMIT 1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errWebhookNotFound
	}
	if err != nil {
		return nil, err
	}
	return d, nil
}

func listAttempts(deliveryID string) ([]webhookAttempt, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT `+webhookAttemptColumns+
		` FROM webhook_attempts WHERE delivery_id=? ORDER BY attempt_no ASC, id ASC`, deliveryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []webhookAttempt{}
	for rows.Next() {
		a, err := scanWebhookAttempt(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

// resetDeliveryForReplay 重放：状态归位 pending 并清空拒绝原因，转发 worker 会重新扫描。
func resetDeliveryForReplay(id string) error {
	db, err := getDB()
	if err != nil {
		return err
	}
	res, err := db.Exec(`UPDATE webhook_deliveries SET status=?, reject_reason='' WHERE id=?`,
		webhookStatusPending, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("not found")
	}
	return nil
}

// webhookStats 概览计数：各状态投递数 + 端点数 + 规则数。
func webhookStats() (map[string]int, error) {
	db, err := getDB()
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	one := func(q string) (int, error) {
		var n int
		err := db.QueryRow(q).Scan(&n)
		return n, err
	}
	if out["total"], err = one(`SELECT COUNT(*) FROM webhook_deliveries`); err != nil {
		return nil, err
	}
	for _, st := range []string{webhookStatusPending, webhookStatusDelivered, webhookStatusFailed, webhookStatusRejected} {
		n, err := one(`SELECT COUNT(*) FROM webhook_deliveries WHERE status='` + st + `'`)
		if err != nil {
			return nil, err
		}
		out[st] = n
	}
	if out["endpoints"], err = one(`SELECT COUNT(*) FROM webhook_endpoints`); err != nil {
		return nil, err
	}
	if out["sources"], err = one(`SELECT COUNT(*) FROM webhook_sources`); err != nil {
		return nil, err
	}
	return out, nil
}

// ---------- 入站接收端点 ----------

// webhookSlugFromPath 取 /api/hooks/{slug} 的 slug 段（已 URL 解码由 ServeMux 完成）。
func webhookSlugFromPath(r *http.Request) string {
	return strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/hooks/"), "/")
}

// apiWebhookReceive 入站接收：调用方须持登记证书过 requireCert（注册处见
// main.go；消费方仅限自建站点），端点可另配 per-endpoint secret 的 HMAC 签名。
// 流程：查端点 → 读体（256KB 上限）→ 验签 → 幂等去重 → 落库 → 202。
// 端点未配置 secret 时仅接受受信代理来源（requireCert 之外的第二道纵深，
// 防「持有效证书的调用方向未配密钥的端点灌数据」）。
func apiWebhookReceive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	slug := webhookSlugFromPath(r)
	if slug == "" {
		writeError(w, http.StatusNotFound, "endpoint not found")
		return
	}
	ep, err := getEndpointBySlug(slug)
	if errors.Is(err, errWebhookNotFound) {
		writeError(w, http.StatusNotFound, "endpoint not found")
		return
	}
	if err != nil {
		writeInternalError(w, err)
		return
	}
	if !ep.Enabled {
		writeError(w, http.StatusNotFound, "endpoint not found")
		return
	}

	body, readErr := readWebhookBody(w, r)
	if readErr != nil {
		// MaxBytesReader 超限：readErr 为 *http.MaxBytesError，统一按 413 拒（不落库）。
		writeError(w, http.StatusRequestEntityTooLarge, "payload too large")
		return
	}

	remoteIP := clientIP(r)
	sigHeader := r.Header.Get("X-Webhook-Signature")
	if sigHeader == "" {
		sigHeader = r.Header.Get("X-Hub-Signature-256") // GitHub 同格式兼容
	}
	tsHeader := r.Header.Get("X-Webhook-Timestamp")

	var sigOK bool
	if ep.Secret == "" {
		// 未配密钥：只认受信代理透传的来源，否则拒绝（避免任意公网请求灌入）。
		sigOK = isTrustedProxy(r)
		if !sigOK {
			rejectWebhookDelivery(w, r, ep, body, remoteIP, "endpoint has no secret and source is not a trusted proxy")
			return
		}
	} else if !verifyWebhookSignature(ep.Secret, body, sigHeader, tsHeader) {
		// 签名缺失/不匹配：落一条 rejected 记录（body 仍存原文，便于排障），回 401。
		rejectWebhookDelivery(w, r, ep, body, remoteIP, "signature mismatch")
		return
	} else {
		sigOK = true
	}

	dedupKey := strings.TrimSpace(r.Header.Get("X-Webhook-Delivery"))
	if dedupKey == "" {
		dedupKey = strings.TrimSpace(r.Header.Get("X-GitHub-Delivery"))
	}
	// 幂等：同 endpoint + dedup_key 已存在 → 直接 200 去重，不新建记录、不重复转发。
	if dedupKey != "" {
		if existing, err := findDeliveryByDedup(ep.ID, dedupKey); err != nil {
			writeInternalError(w, err)
			return
		} else if existing != "" {
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "delivery_id": existing, "accepted": true, "dedup": true,
			})
			return
		}
	}

	d := webhookDelivery{
		ID:          "wh-dlv-" + randHex(8),
		EndpointID:  ep.ID,
		Slug:        ep.Slug,
		ReceivedAt:  time.Now().UTC().Format(time.RFC3339),
		RemoteIP:    remoteIP,
		Method:      r.Method,
		ContentType: r.Header.Get("Content-Type"),
		Body:        string(body),
		Status:      webhookStatusPending,
		DedupKey:    dedupKey,
		Headers:     webhookCaptureHeaders(r),
		BodyBytes:   len(body),
		SigOK:       sigOK,
	}
	d.EventType = webhookEventType(d.Headers, body)

	if err := insertWebhookDelivery(d); err != nil {
		// 并发同 dedup_key：唯一冲突归为 dedup 命中，返回既有记录。
		if isUniqueViolation(err) && dedupKey != "" {
			existing, _ := findDeliveryByDedup(ep.ID, dedupKey)
			writeJSON(w, http.StatusOK, map[string]any{
				"ok": true, "delivery_id": existing, "accepted": true, "dedup": true,
			})
			return
		}
		writeInternalError(w, err)
		return
	}
	audit("webhook", "", "webhook.receive", d.ID, "ok",
		"slug="+ep.Slug+" bytes="+strconv.Itoa(d.BodyBytes), remoteIP)
	log.Printf("portal: webhook received slug=%s id=%s bytes=%d ip=%s", ep.Slug, d.ID, d.BodyBytes, remoteIP)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok": true, "delivery_id": d.ID, "accepted": true, "dedup": false,
	})
}

// readWebhookBody 读入站 body（256KB 上限）。超限返回错误，调用方回 413。
func readWebhookBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	r.Body = http.MaxBytesReader(w, r.Body, webhookMaxBody)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	return body, nil
}

// webhookCaptureHeaders 抓取入站头（供 UI 详情展示与重放参考）。
// 过滤 hop-by-hop 与超大体积头，避免无谓膨胀。
func webhookCaptureHeaders(r *http.Request) map[string]string {
	skip := map[string]bool{
		"Connection": true, "Keep-Alive": true, "Transfer-Encoding": true,
		"Upgrade": true, "Proxy-Authorization": true, "Proxy-Connection": true,
	}
	out := map[string]string{}
	for k, v := range r.Header {
		if skip[http.CanonicalHeaderKey(k)] || len(v) == 0 {
			continue
		}
		val := strings.Join(v, ", ")
		if len(val) > 2048 {
			val = val[:2048]
		}
		out[http.CanonicalHeaderKey(k)] = val
	}
	return out
}

// webhookEventType 事件类型：优先 X-Webhook-Event / X-GitHub-Event 头，
// 其次从 JSON body 的 type/event 字段兜底（便于 UI 分组）。
func webhookEventType(headers map[string]string, body []byte) string {
	for _, k := range []string{"X-Webhook-Event", "X-GitHub-Event", "X-Event-Type", "X-Webhook-Topic"} {
		if v := strings.TrimSpace(headers[k]); v != "" {
			return v
		}
	}
	var probe struct {
		Type  string `json:"type"`
		Event string `json:"event"`
	}
	if json.Unmarshal(body, &probe) == nil {
		if probe.Type != "" {
			return probe.Type
		}
		return probe.Event
	}
	return ""
}

// rejectWebhookDelivery 记一条 rejected 投递（body 仍落库，便于排障与重放判定）。
func rejectWebhookDelivery(w http.ResponseWriter, r *http.Request, ep *webhookEndpoint, body []byte, ip, reason string) {
	d := webhookDelivery{
		ID:           "wh-dlv-" + randHex(8),
		EndpointID:   ep.ID,
		Slug:         ep.Slug,
		ReceivedAt:   time.Now().UTC().Format(time.RFC3339),
		RemoteIP:     ip,
		Method:       r.Method,
		ContentType:  r.Header.Get("Content-Type"),
		Body:         string(body),
		Status:       webhookStatusRejected,
		RejectReason: reason,
		Headers:      webhookCaptureHeaders(r),
		BodyBytes:    len(body),
		SigOK:        false,
	}
	d.EventType = webhookEventType(d.Headers, body)
	if err := insertWebhookDelivery(d); err != nil {
		log.Printf("portal: webhook reject record failed slug=%s: %v", ep.Slug, err)
	}
	audit("webhook", "", "webhook.reject", d.ID, "fail", "slug="+ep.Slug+" reason="+reason, ip)
	writeError(w, http.StatusUnauthorized, "invalid signature")
}

func insertWebhookDelivery(d webhookDelivery) error {
	db, err := getDB()
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO webhook_deliveries (`+webhookDeliveryColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.EndpointID, d.Slug, d.ReceivedAt, d.RemoteIP, d.Method, d.ContentType,
		marshalWebhookHeaders(d.Headers), d.Body, d.BodyBytes, boolInt(d.SigOK),
		d.Status, d.RejectReason, d.EventType, d.DedupKey)
	return err
}

// findDeliveryByDedup 按 endpoint+dedup_key 查已有投递 id（幂等去重）。
func findDeliveryByDedup(endpointID, dedupKey string) (string, error) {
	db, err := getDB()
	if err != nil {
		return "", err
	}
	var id string
	err = db.QueryRow(`SELECT id FROM webhook_deliveries WHERE endpoint_id=? AND dedup_key=? LIMIT 1`,
		endpointID, dedupKey).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return id, nil
}
