package main

import (
	"encoding/json"
	"log"
	"os"
	"sync"
	"time"
)

// 审计日志：JSONL append，只追加不修改。

type auditEntry struct {
	At       string `json:"at"`
	Actor    string `json:"actor"`     // 用户名（dev 无头为 dev-no-cert）
	CertDesc string `json:"cert"`      // 证书 serial 或 label
	Action   string `json:"action"`    // certs.issue / certs.revoke / ...
	Object   string `json:"object"`    // 作用对象 id
	Result   string `json:"result"`    // ok / fail
	Note     string `json:"note"`      // 备注
	IP       string `json:"remote_ip"` // 对端地址
}

var auditMu sync.Mutex

func auditPath() string {
	return dataDir() + "/audit.jsonl"
}

func audit(actor, certDesc, action, object, result, note, ip string) {
	e := auditEntry{
		At:       time.Now().UTC().Format(time.RFC3339),
		Actor:    actor,
		CertDesc: certDesc,
		Action:   action,
		Object:   object,
		Result:   result,
		Note:     note,
		IP:       ip,
	}
	data, err := json.Marshal(e)
	if err != nil {
		log.Printf("audit: marshal failed: %v", err)
		return
	}
	auditMu.Lock()
	defer auditMu.Unlock()
	f, err := os.OpenFile(auditPath(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		log.Printf("audit: open failed: %v", err)
		return
	}
	defer f.Close()
	data = append(data, '\n')
	if _, err := f.Write(data); err != nil {
		log.Printf("audit: write failed: %v", err)
	}
}
