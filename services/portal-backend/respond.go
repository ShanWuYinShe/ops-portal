package main

import (
	"encoding/json"
	"net/http"
)

// 统一 JSON 信封：成功 {"ok":true,...}，失败 {"ok":false,"error":...}。

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeOK(w http.ResponseWriter, v any) {
	if v == nil {
		v = map[string]any{}
	}
	m, ok := v.(map[string]any)
	if !ok {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "data": v})
		return
	}
	m["ok"] = true
	writeJSON(w, http.StatusOK, m)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": msg})
}
