package main

import (
	"net/http"
	"strings"
	"sync"
)

// 用户与证书绑定管理（admin）：给 users 表一个管理面，
// 不再靠 SSH 上服务器手改。用户凭绑定的证书登录（见 currentUser）。

func getUserByCertID(certID string) (portalUser, bool) {
	db, err := getDB()
	if err != nil {
		return portalUser{}, false
	}
	u := portalUser{}
	err = db.QueryRow(`SELECT username, role, cert_id FROM users WHERE cert_id=? LIMIT 1`, certID).
		Scan(&u.Username, &u.Role, &u.CertID)
	if err != nil {
		return portalUser{}, false
	}
	return u, true
}

func getUserRole(username string) (string, bool) {
	db, err := getDB()
	if err != nil {
		return "", false
	}
	var role string
	if err := db.QueryRow(`SELECT role FROM users WHERE username=?`, username).Scan(&role); err != nil {
		return "", false
	}
	return role, true
}

func getUserCertID(username string) string {
	db, err := getDB()
	if err != nil {
		return ""
	}
	var id string
	if err := db.QueryRow(`SELECT cert_id FROM users WHERE username=?`, username).Scan(&id); err != nil {
		return ""
	}
	return id
}

func upsertUser(username, role, certID string) {
	db, err := getDB()
	if err != nil {
		return
	}
	_, _ = db.Exec(`INSERT INTO users (username, role, cert_id) VALUES (?,?,?)
		ON CONFLICT(username) DO UPDATE SET role=excluded.role, cert_id=excluded.cert_id`,
		username, role, certID)
}

func deleteUser(username string) {
	db, err := getDB()
	if err != nil {
		return
	}
	_, _ = db.Exec(`DELETE FROM users WHERE username=?`, username)
}

func validRole(role string) bool {
	return role == "ops.admin" || role == "ops.operator" || role == "ops.viewer"
}

// GET /api/portal/users（admin）—— 用户列表（含绑定证书 label，方便认人）。
func apiUsersList(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	db, err := getDB()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	rows, err := db.Query(`SELECT u.username, u.role, u.cert_id, COALESCE(c.label,'')
		FROM users u LEFT JOIN certs c ON c.id = u.cert_id ORDER BY u.username`)
	if err != nil {
		writeInternalError(w, err)
		return
	}
	defer rows.Close()
	type userView struct {
		portalUser
		CertLabel string `json:"cert_label"`
	}
	out := []userView{}
	for rows.Next() {
		var v userView
		if err := rows.Scan(&v.Username, &v.Role, &v.CertID, &v.CertLabel); err != nil {
			writeInternalError(w, err)
			return
		}
		out = append(out, v)
	}
	writeOK(w, map[string]any{"users": out})
}

// userMu 串行化用户写操作：「是否最后一个 admin」的判定与写入必须原子，
// 否则并发降权/删除最后两个 admin 时可能双双通过判定，锁死管理面。
var userMu sync.Mutex

// POST /api/portal/users/upsert（admin）—— 新增或改绑用户。
func apiUsersUpsert(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Username string `json:"username"`
		Role     string `json:"role"`
		CertID   string `json:"cert_id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	username := strings.TrimSpace(body.Username)
	role := strings.TrimSpace(body.Role)
	certID := strings.TrimSpace(body.CertID)
	if username == "" || !validRole(role) || certID == "" {
		writeError(w, http.StatusBadRequest, "username, role (ops.admin/operator/viewer) and cert_id required")
		return
	}
	u := actorOf(r)
	c, err := getCertByID(certID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "unknown cert_id")
		return
	}
	// 机器身份证书（portal-agent/service/site）不可作登录绑定：它们是
	// 机器/网站的身份凭证，绑人会让持钥机器获得人的角色（与 rename 同样保护）。
	if c.Kind != "human" {
		writeError(w, http.StatusBadRequest,
			"只能绑定人用（human）证书；portal-agent/service/site 证书是机器或网站身份，不可作登录绑定")
		return
	}
	// 一证一用户：同一证书绑给两个人会让 currentUser 命中随机身份。
	if prev, ok := getUserByCertID(certID); ok && prev.Username != username {
		writeError(w, http.StatusBadRequest, "该证书已绑定到用户 "+prev.Username)
		return
	}
	userMu.Lock()
	defer userMu.Unlock()
	prevRole, _ := getUserRole(username)
	prevCert := getUserCertID(username)
	upsertUser(username, role, certID)
	// 禁止把最后一个 admin 降权：防锁死管理面（回滚本次写入）。
	if prevRole == "ops.admin" && role != "ops.admin" && !anotherAdmin(username) {
		upsertUser(username, prevRole, prevCert)
		audit(u.Username, "", "users.upsert", username, "fail", "refuse demoting last admin", clientIP(r))
		writeError(w, http.StatusBadRequest, "refuse demoting last admin")
		return
	}
	audit(u.Username, "", "users.upsert", username, "ok", role, clientIP(r))
	writeOK(w, map[string]any{"username": username, "role": role})
}

// POST /api/portal/users/remove（admin）—— 删除用户。
func apiUsersRemove(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodPost) {
		return
	}
	var body struct {
		Username string `json:"username"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" {
		writeError(w, http.StatusBadRequest, "username required")
		return
	}
	u := actorOf(r)
	if username == u.Username {
		writeError(w, http.StatusBadRequest, "refuse removing self")
		return
	}
	userMu.Lock()
	defer userMu.Unlock()
	removedRole, ok := getUserRole(username)
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if removedRole == "ops.admin" && !anotherAdmin(username) {
		audit(u.Username, "", "users.remove", username, "fail", "refuse removing last admin", clientIP(r))
		writeError(w, http.StatusBadRequest, "refuse removing last admin")
		return
	}
	deleteUser(username)
	audit(u.Username, "", "users.remove", username, "ok", "", clientIP(r))
	writeOK(w, map[string]any{"username": username, "removed": true})
}

// anotherAdmin 除外用户之外是否还有 admin。
func anotherAdmin(except string) bool {
	db, err := getDB()
	if err != nil {
		return false
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM users WHERE role='ops.admin' AND username <> ?`, except).Scan(&n); err != nil {
		return false
	}
	return n > 0
}
