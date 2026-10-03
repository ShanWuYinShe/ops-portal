package main

import (
	"database/sql"
	"testing"
	"time"
)

// countRows 统计表行数（测试辅助）。
func countRows(t *testing.T, table string) int {
	t.Helper()
	db, err := getDB()
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// 保留期清理：过期投递连同其 attempts 一起删，未过期的不动。
func TestPurgeOldWebhooks(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	if _, err := getDB(); err != nil {
		t.Fatalf("open db: %v", err)
	}

	old := time.Now().AddDate(0, 0, -60).UTC().Format(time.RFC3339)
	now := time.Now().UTC().Format(time.RFC3339)

	insertDlv := func(t *testing.T, id, at string) {
		t.Helper()
		db, err := getDB()
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		_, err = db.Exec(`INSERT INTO webhook_deliveries
			(id, endpoint_id, slug, received_at, method, content_type, headers, body, body_bytes,
			 sig_ok, status, reject_reason, event_type, dedup_key)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, "ep-x", "slug-x", at, "POST", "application/json", "{}", "b", 1, 1, "delivered", "", "", "")
		if err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
	}
	insertAtt := func(t *testing.T, dlvID, at string) {
		t.Helper()
		db, err := getDB()
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		_, err = db.Exec(`INSERT INTO webhook_attempts
			(delivery_id, source_id, attempt_no, status, http_status, error, duration_ms, request_body, response_body, at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`,
			dlvID, "src-x", 1, "failed", 500, "boom", 1, "req", "resp", at)
		if err != nil {
			t.Fatalf("insert attempt: %v", err)
		}
	}

	insertDlv(t, "wh-dlv-old", old)
	insertAtt(t, "wh-dlv-old", old)
	insertDlv(t, "wh-dlv-new", now)
	insertAtt(t, "wh-dlv-new", now)

	purgeOldWebhooks()

	if got := countRows(t, "webhook_deliveries"); got != 1 {
		t.Errorf("deliveries after purge = %d, want 1 (only the fresh one)", got)
	}
	if got := countRows(t, "webhook_attempts"); got != 1 {
		t.Errorf("attempts after purge = %d, want 1 (old cascade-gone, new kept)", got)
	}
	var kept string
	db, _ := getDB()
	if err := db.QueryRow("SELECT id FROM webhook_deliveries").Scan(&kept); err != nil {
		if err != sql.ErrNoRows {
			t.Fatalf("scan kept delivery: %v", err)
		}
	}
	if kept != "wh-dlv-new" {
		t.Errorf("kept delivery = %q, want wh-dlv-new", kept)
	}
}

// 非法/非正值回退默认 30。
func TestWebhookRetentionDaysFallback(t *testing.T) {
	if webhookRetentionDays() != 30 {
		t.Errorf("default retention = %d, want 30", webhookRetentionDays())
	}
}
