package main

import (
	"net/http"
	"os"
	"strings"
	"sync"
)

// RBAC 三档：ops.admin > ops.operator > ops.viewer。
// Phase 2：用户表本地登记（用户名 + 角色 + 绑定证书 label），dev 无头默认 admin。

type portalUser struct {
	Username string `json:"username"`
	Role     string `json:"role"` // ops.admin / ops.operator / ops.viewer
	CertID   string `json:"cert_id"`
}

type userStore struct {
	Users []portalUser `json:"users"`
}

var usersMu sync.Mutex

func usersPath() string {
	return dataDir() + "/users.json"
}

func loadUsers() (*userStore, error) {
	s := &userStore{}
	if err := readJSON(usersPath(), s); err != nil {
		if os.IsNotExist(err) {
			return &userStore{}, nil
		}
		return nil, err
	}
	if s.Users == nil {
		s.Users = []portalUser{}
	}
	return s, nil
}

func roleLevel(role string) int {
	switch role {
	case "ops.admin":
		return 3
	case "ops.operator":
		return 2
	case "ops.viewer":
		return 1
	default:
		return 0
	}
}

type ctxKey string

const userCtxKey ctxKey = "portalUser"

// currentUser 从证书 label 解析用户：dev 无头 → dev-no-cert/admin；
// 有头 → 查证书库 label → 查用户表绑定，无绑定默认 viewer。
func currentUser(r *http.Request, id certIdentity) portalUser {
	if getEnv("DEV_ALLOW_NO_CERT", "") != "" && strings.TrimSpace(id.Verified) == "" {
		return portalUser{Username: "dev-no-cert", Role: "ops.admin"}
	}
	usersMu.Lock()
	defer usersMu.Unlock()
	s, err := loadUsers()
	if err != nil {
		return portalUser{Username: id.Subject, Role: "ops.viewer"}
	}
	// 按证书库 label 找绑定用户（锁顺序固定 usersMu → certsMu，全局仅此一处）
	if cert := findActiveCert(id.Serial, id.Fingerprint); cert != nil {
		for _, u := range s.Users {
			if u.CertID == cert.ID {
				return u
			}
		}
		return portalUser{Username: cert.Label, Role: "ops.viewer", CertID: cert.ID}
	}
	return portalUser{Username: id.Subject, Role: "ops.viewer"}
}

func withUser(r *http.Request, u portalUser) *http.Request {
	return r.WithContext(contextWithUser(r, u))
}

// requireRole 要求用户角色 ≥ 指定等级，并把用户塞进 context。
func requireRole(minRole string, next http.HandlerFunc) http.HandlerFunc {
	min := roleLevel(minRole)
	return requireCert(func(w http.ResponseWriter, r *http.Request) {
		id := certFromRequest(r)
		u := currentUser(r, id)
		if roleLevel(u.Role) < min {
			audit(u.Username, id.Serial, "authz.deny", r.URL.Path, "fail", "need "+minRole, r.RemoteAddr)
			writeError(w, http.StatusForbidden, "insufficient role")
			return
		}
		next(w, withUser(r, u))
	})
}
