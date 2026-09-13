package main

import (
	"bufio"
	"encoding/json"
	"os"
)

// readAuditTail 读审计日志最后 N 行（新→旧）。

func readAuditTail(n int) []auditEntry {
	f, err := os.Open(auditPath())
	if err != nil {
		return []auditEntry{}
	}
	defer f.Close()
	var all []json.RawMessage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := append([]byte{}, sc.Bytes()...)
		var e auditEntry
		if json.Unmarshal(line, &e) == nil {
			all = append(all, line)
		}
	}
	if len(all) > n {
		all = all[len(all)-n:]
	}
	out := make([]auditEntry, 0, len(all))
	for i := len(all) - 1; i >= 0; i-- {
		var e auditEntry
		if json.Unmarshal(all[i], &e) == nil {
			out = append(out, e)
		}
	}
	return out
}
