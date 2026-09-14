package main

import (
	"log"
	"time"
)

// 审计日志（SQLite audit 表）：只追加不修改，保留期见 AUDIT_RETENTION_DAYS。

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

func audit(actor, certDesc, action, object, result, note, ip string) {
	db, err := getDB()
	if err != nil {
		log.Printf("audit: open db failed: %v", err)
		return
	}
	_, err = db.Exec(`INSERT INTO audit (at,actor,cert,action,object,result,note,remote_ip)
		VALUES (?,?,?,?,?,?,?,?)`,
		time.Now().UTC().Format(time.RFC3339), actor, certDesc, action, object, result, note, ip)
	if err != nil {
		log.Printf("audit: insert failed: %v", err)
	}
}
