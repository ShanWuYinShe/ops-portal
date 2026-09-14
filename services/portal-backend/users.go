package main

import (
	"net/http"
	"strings"
)

// 用户与证书绑定管理（admin）：给 users.json 一个管理面，
// 不再靠 SSH 上服务器手改。用户凭绑定的证书登录（见 currentUser）。

func saveUsers(s *userStore) error { return atomicWriteJSON(usersPath(), s, 0600) }

func validRole(role string) bool {
	return role == "ops.admin" || role == "ops.operator" || role == "ops.viewer"
}

func certIDExists(id string) bool {
	s, err := loadCerts()
	if err != nil {
		return false
	}
	for _, c := range s.Certs {
		if c.ID == id {
			return true
		}
	}
	return false
}

// GET /api/portal/users（admin）—— 用户列表（含绑定证书 label，方便认人）。
func apiUsersList(w http.ResponseWriter, r *http.Request) {
	if !mustMethod(w, r, http.MethodGet) {
		return
	}
	usersMu.Lock()
	s, err := loadUsers()
	usersMu.Unlock()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	certs, _ := loadCerts()
	labelOf := map[string]string{}
	for _, c := range certs.Certs {
		labelOf[c.ID] = c.Label
	}
	type userView struct {
		portalUser
		CertLabel string `json:"cert_label"`
	}
	out := make([]userView, 0, len(s.Users))
	for _, u := range s.Users {
		out = append(out, userView{portalUser: u, CertLabel: labelOf[u.CertID]})
	}
	writeOK(w, map[string]any{"users": out})
}

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
	usersMu.Lock()
	defer usersMu.Unlock()
	// 锁顺序固定 usersMu → certsMu（与 currentUser 一致），防死锁。
	if !certIDExists(certID) {
		writeError(w, http.StatusBadRequest, "unknown cert_id")
		return
	}
	s, err := loadUsers()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	prevRole := ""
	found := false
	for i := range s.Users {
		if s.Users[i].Username == username {
			prevRole = s.Users[i].Role
			s.Users[i].Role, s.Users[i].CertID = role, certID
			found = true
		}
	}
	if !found {
		s.Users = append(s.Users, portalUser{Username: username, Role: role, CertID: certID})
	}
	// 禁止把自己（或最后一个 admin）降权：防锁死管理面。
	if prevRole == "ops.admin" && role != "ops.admin" && !anotherAdmin(s, username) {
		audit(u.Username, "", "users.upsert", username, "fail", "refuse demoting last admin", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "refuse demoting last admin")
		return
	}
	if err := saveUsers(s); err != nil {
		writeInternalError(w, err)
		return
	}
	audit(u.Username, "", "users.upsert", username, "ok", role, r.RemoteAddr)
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
	usersMu.Lock()
	defer usersMu.Unlock()
	s, err := loadUsers()
	if err != nil {
		writeInternalError(w, err)
		return
	}
	kept := s.Users[:0]
	removedRole := ""
	for _, x := range s.Users {
		if x.Username == username {
			removedRole = x.Role
			continue
		}
		kept = append(kept, x)
	}
	if removedRole == "" {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.Users = kept
	if removedRole == "ops.admin" && !anotherAdmin(s, "") {
		audit(u.Username, "", "users.remove", username, "fail", "refuse removing last admin", r.RemoteAddr)
		writeError(w, http.StatusBadRequest, "refuse removing last admin")
		return
	}
	if err := saveUsers(s); err != nil {
		writeInternalError(w, err)
		return
	}
	audit(u.Username, "", "users.remove", username, "ok", "", r.RemoteAddr)
	writeOK(w, map[string]any{"username": username, "removed": true})
}

// anotherAdmin 除外用户之外是否还有 admin。
func anotherAdmin(s *userStore, except string) bool {
	for _, x := range s.Users {
		if x.Username != except && x.Role == "ops.admin" {
			return true
		}
	}
	return false
}
