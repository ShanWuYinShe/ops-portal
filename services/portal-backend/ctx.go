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

const agentCtxKey ctxKey = "agentID"

// contextWithAgentID 把 token 绑定的 portal_agent_id 塞进 context
// （requireAgentTokenV2 已校验；legacy 单 token 回退时传空串表示不绑定）。
func contextWithAgentID(r *http.Request, agentID string) context.Context {
	return context.WithValue(r.Context(), agentCtxKey, agentID)
}

// agentIDFromContext 取 token 绑定的 portal_agent_id；空串表示未绑定（legacy 回退）。
func agentIDFromContext(r *http.Request) string {
	if v, ok := r.Context().Value(agentCtxKey).(string); ok {
		return v
	}
	return ""
}
