package main

import (
	"context"
	"net/http"
)

// 从请求 context 取当前用户（requireRole 已塞入）。

func userFromContext(r *http.Request) portalUser {
	if u, ok := r.Context().Value(userCtxKey).(portalUser); ok {
		return u
	}
	return portalUser{Username: "unknown", Role: "ops.viewer"}
}

func contextWithUser(r *http.Request, u portalUser) context.Context {
	return context.WithValue(r.Context(), userCtxKey, u)
}
