package main

// readAuditTail 读审计最新 N 条（新→旧）。

func readAuditTail(n int) []auditEntry {
	db, err := getDB()
	if err != nil {
		return []auditEntry{}
	}
	if n <= 0 || n > 1000 {
		n = 200
	}
	rows, err := db.Query(`SELECT at,actor,cert,action,object,result,note,remote_ip
		FROM audit ORDER BY id DESC LIMIT ?`, n)
	if err != nil {
		return []auditEntry{}
	}
	defer rows.Close()
	out := []auditEntry{}
	for rows.Next() {
		e := auditEntry{}
		if err := rows.Scan(&e.At, &e.Actor, &e.CertDesc, &e.Action, &e.Object, &e.Result, &e.Note, &e.IP); err != nil {
			return []auditEntry{}
		}
		out = append(out, e)
	}
	return out
}
