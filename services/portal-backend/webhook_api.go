package main

// webhook 管理 API（/api/portal/webhooks/*，全部经 requireRole 门禁）：
// 端点、转发规则、投递记录查询与重放、概览统计。
// 所有写操作落审计（action=webhook.*）；入参在 handler 侧静态校验，
// 其中 target_url 的 SSRF 校验是安全红线（见 validateWebhookTargetURL）。

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// webhookSlugRe slug 只允许小写字母/数字/连字符/下划线，1~64 字符。
var webhookSlugRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// validWebhookStatuses 投递状态白名单，过滤条件不接受任意串。
var validWebhookStatuses = map[string]bool{
	webhookStatusPending:   true,
	webhookStatusDelivered: true,
	webhookStatusFailed:    true,
	webhookStatusRejected:  true,
	webhookStatusDedup:     true,
}

// validateWebhookTargetURL 出站目标静态校验（SSRF 第一道防线，注册/更新时即拒）：
//   - 仅 http/https，拒绝 userinfo（防 http://user@host 混淆与凭据外泄）；
//   - host 解析后必须全部为公网地址，环回/私网/链路本地一律拒。
//
// 逃生阀沿用 OPS_PROBE_ALLOW_PRIVATE（纯内网自托管部署联调），与拨测共用语义，
// 不新造开关。静态校验只给操作者即时反馈，建连时刻仍由 dial control 兜底防 rebinding。
func validateWebhookTargetURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("target_url required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", errors.New("target_url 不可解析: " + err.Error())
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", errors.New("target_url 仅支持 http/https")
	}
	if u.Host == "" {
		return "", errors.New("target_url 缺少 host")
	}
	if u.User != nil {
		return "", errors.New("target_url 不允许携带 userinfo")
	}
	if err := rejectPrivateHost(u.Hostname()); err != nil {
		// rejectPrivateHost 文案是 ops_url 语境，统一改写为 target_url 便于定位。
		return "", errors.New(strings.ReplaceAll(err.Error(), "ops_url", "target_url"))
	}
	return raw, nil
}

// webhookEndpointPayload 端点 upsert 请求体。Secret 用指针区分「未传（保留原值）」
// 与「显式传空（清空）」，避免前端不回传密钥时把已有密钥抹掉。
type webhookEndpointPayload struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Slug    string  `json:"slug"`
	Secret  *string `json:"secret"`
	Enabled *bool   `json:"enabled"`
	Note    string  `json:"note"`
}

type webhookSourcePayload struct {
	ID          string  `json:"id"`
	EndpointID  string  `json:"endpoint_id"`
	Name        string  `json:"name"`
	TargetURL   string  `json:"target_url"`
	Secret      *string `json:"secret"`
	Enabled     *bool   `json:"enabled"`
	MaxAttempts int     `json:"max_attempts"`
	TimeoutMS   int     `json:"timeout_ms"`
}

// maskEndpointSecrets 列表返回打码密钥（列表接口不得回明文）。
func maskEndpointSecrets(list []webhookEndpoint) []webhookEndpoint {
	out := make([]webhookEndpoint, 0, len(list))
	for _, e := range list {
		e.Secret = maskSecret(e.Secret)
		out = append(out, e)
	}
	return out
}

func maskSourceSecrets(list []webhookSource) []webhookSource {
	out := make([]webhookSource, 0, len(list))
	for _, s := range list {
		s.Secret = maskSecret(s.Secret)
		out = append(out, s)
	}
	return out
}

// findEndpointByID 按 id 取端点。数据层只提供 by-slug 查询，端点数有限，
// 列表查找足够，且不必为一条查询去改 task-1 负责的数据层文件。
func findEndpointByID(id string) *webhookEndpoint {
	list, err := listEndpoints()
	if err != nil {
		return nil
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// findSourceByID 跨端点按 id 找规则（未传 secret 时保留原值用）。
func findSourceByID(id string) *webhookSource {
	list, err := listSources("")
	if err != nil {
		return nil
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i]
		}
	}
	return nil
}

// ---------- 端点 ----------

func apiWebhookEndpointsList(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	list, err := listEndpoints()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeOK(w, map[string]any{"endpoints": maskEndpointSecrets(list)})
}

func apiWebhookEndpointsUpsert(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var p webhookEndpointPayload
	if !decodeBody(w, r, &p) {
		return
	}
	u := actorOf(r)
	slug := strings.TrimSpace(p.Slug)
	if !webhookSlugRe.MatchString(slug) {
		audit(u.Username, "", "webhook.endpoint.upsert", p.ID, "fail", "bad slug", clientIP(r))
		writeError(w, http.StatusBadRequest, "slug 只允许 [a-z0-9_-]，长度 1~64")
		return
	}
	if strings.TrimSpace(p.Name) == "" {
		writeError(w, http.StatusBadRequest, "name required")
		return
	}
	e := webhookEndpoint{
		ID:      strings.TrimSpace(p.ID),
		Name:    strings.TrimSpace(p.Name),
		Slug:    slug,
		Note:    p.Note,
		Enabled: true,
	}
	if p.Enabled != nil {
		e.Enabled = *p.Enabled
	}
	if p.Secret != nil {
		e.Secret = strings.TrimSpace(*p.Secret)
	} else if e.ID != "" {
		// 未传 secret 视为不改动：取回原值，避免更新时把密钥抹掉。
		if old := findEndpointByID(e.ID); old != nil {
			e.Secret = old.Secret
		}
	}
	e.CreatedBy = u.Username
	if err := upsertEndpoint(e); err != nil {
		audit(u.Username, "", "webhook.endpoint.upsert", e.ID, "fail", err.Error(), clientIP(r))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	audit(u.Username, "", "webhook.endpoint.upsert", slug, "ok", e.Name, clientIP(r))
	writeOK(w, map[string]any{})
}

func apiWebhookEndpointsRemove(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	u := actorOf(r)
	if strings.TrimSpace(body.ID) == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	if err := removeEndpoint(body.ID); err != nil {
		audit(u.Username, "", "webhook.endpoint.remove", body.ID, "fail", err.Error(), clientIP(r))
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	audit(u.Username, "", "webhook.endpoint.remove", body.ID, "ok", "级联删除转发规则", clientIP(r))
	writeOK(w, map[string]any{})
}

// apiWebhookEndpointsRotate 轮换密钥：新明文只在本次响应里出现一次。
func apiWebhookEndpointsRotate(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	u := actorOf(r)
	secret, err := rotateEndpointSecret(strings.TrimSpace(body.ID))
	if err != nil {
		audit(u.Username, "", "webhook.endpoint.rotate-secret", body.ID, "fail", err.Error(), clientIP(r))
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	audit(u.Username, "", "webhook.endpoint.rotate-secret", body.ID, "ok", "", clientIP(r))
	writeOK(w, map[string]any{"secret": secret})
}

// ---------- 转发规则 ----------

func apiWebhookSourcesList(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	list, err := listSources(strings.TrimSpace(r.URL.Query().Get("endpoint_id")))
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeOK(w, map[string]any{"sources": maskSourceSecrets(list)})
}

func apiWebhookSourcesUpsert(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var p webhookSourcePayload
	if !decodeBody(w, r, &p) {
		return
	}
	u := actorOf(r)
	endpointID := strings.TrimSpace(p.EndpointID)
	if endpointID == "" {
		writeError(w, http.StatusBadRequest, "endpoint_id required")
		return
	}
	if findEndpointByID(endpointID) == nil {
		writeError(w, http.StatusBadRequest, "endpoint_id 不存在")
		return
	}
	// SSRF 红线：目标必须 http/https 且解析为公网地址（除非显式放开私网）。
	target, err := validateWebhookTargetURL(p.TargetURL)
	if err != nil {
		audit(u.Username, "", "webhook.source.upsert", p.ID, "fail", err.Error(), clientIP(r))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s := webhookSource{
		ID:          strings.TrimSpace(p.ID),
		EndpointID:  endpointID,
		Name:        strings.TrimSpace(p.Name),
		TargetURL:   target,
		Enabled:     true,
		MaxAttempts: p.MaxAttempts,
		TimeoutMS:   p.TimeoutMS,
	}
	if p.Enabled != nil {
		s.Enabled = *p.Enabled
	}
	if p.Secret != nil {
		s.Secret = strings.TrimSpace(*p.Secret)
	} else if s.ID != "" {
		// 未传 secret 视为不改动（出站签名密钥同端点处理）。
		if old := findSourceByID(s.ID); old != nil {
			s.Secret = old.Secret
		}
	}
	if err := upsertSource(s); err != nil {
		audit(u.Username, "", "webhook.source.upsert", s.ID, "fail", err.Error(), clientIP(r))
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	audit(u.Username, "", "webhook.source.upsert", s.Name, "ok", target, clientIP(r))
	writeOK(w, map[string]any{})
}

func apiWebhookSourcesRemove(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	u := actorOf(r)
	if strings.TrimSpace(body.ID) == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	if err := removeSource(body.ID); err != nil {
		audit(u.Username, "", "webhook.source.remove", body.ID, "fail", err.Error(), clientIP(r))
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	audit(u.Username, "", "webhook.source.remove", body.ID, "ok", "", clientIP(r))
	writeOK(w, map[string]any{})
}

// ---------- 投递记录 ----------

func apiWebhookDeliveriesList(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	qv := r.URL.Query()
	limit := 100
	if v := strings.TrimSpace(qv.Get("limit")); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit 必须是正整数")
			return
		}
		limit = n
	}
	status := strings.TrimSpace(qv.Get("status"))
	if status != "" && !validWebhookStatuses[status] {
		writeError(w, http.StatusBadRequest, "status 取值非法")
		return
	}
	list, err := listDeliveries(limit, status, strings.TrimSpace(qv.Get("endpoint_id")))
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeOK(w, map[string]any{"deliveries": list})
}

func apiWebhookDeliveriesGet(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	d, err := getDelivery(id)
	if errors.Is(err, errWebhookNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		writeInternalError(w, err)
		return
	}
	attempts, err := listAttempts(id)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeOK(w, map[string]any{"delivery": d, "attempts": attempts})
}

// apiWebhookDeliveriesReplay 重放：状态归位 pending，转发 worker 会重新拾取。
func apiWebhookDeliveriesReplay(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	u := actorOf(r)
	id := strings.TrimSpace(body.ID)
	if id == "" {
		writeError(w, http.StatusBadRequest, "id required")
		return
	}
	if err := resetDeliveryForReplay(id); err != nil {
		audit(u.Username, "", "webhook.delivery.replay", id, "fail", err.Error(), clientIP(r))
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	// 重放是「现在再试一次」的明确人工意图：必须清掉该投递的历史尝试记录。
	// 重试计数以 webhook_attempts 为准，不清空则 attempt_no 已达规则 max_attempts 时
	// worker 会立即判定终结，重放变成空操作（用户看不到任何效果）。
	// 清空后重放从第 1 次尝试重新计，审计里留有 replay 事件可追溯。
	if err := clearDeliveryAttempts(id); err != nil {
		audit(u.Username, "", "webhook.delivery.replay", id, "fail", err.Error(), clientIP(r))
		writeInternalError(w, err)
		return
	}
	audit(u.Username, "", "webhook.delivery.replay", id, "ok", "", clientIP(r))
	writeOK(w, map[string]any{})
}

func apiWebhookStats(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	st, err := webhookStats()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	writeOK(w, map[string]any{"stats": st})
}
