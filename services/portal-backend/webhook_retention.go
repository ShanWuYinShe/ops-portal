package main

// webhook 投递数据保留：deliveries 存全量入站原文、attempts 存每次转发的
// 请求/响应体，是库里增长最快的表，必须像 audit 一样有自动裁剪，否则
// 长期运行的部署会无限膨胀（WEBHOOK_RETENTION_DAYS，默认 30 天——比审计的
// 180 天短：这里是排障用途的大体积原文，非合规留痕）。

import (
	"log"
	"strconv"
	"time"
)

// webhookRetentionDays 投递保留天数（默认 30；非法值回退默认）。
func webhookRetentionDays() int {
	n, err := strconv.Atoi(getEnv("WEBHOOK_RETENTION_DAYS", "30"))
	if err != nil || n <= 0 {
		return 30
	}
	return n
}

// purgeOldWebhooks 按保留期清理投递及其出站尝试（启动 + 备份前 + 每日循环）。
// attempts 没有外键级联，先删旧投递的 attempts 再删 deliveries，
// 两者放同一事务：半途失败时回滚，不留孤儿 attempts。
func purgeOldWebhooks() {
	db, err := getDB()
	if err != nil {
		return
	}
	days := webhookRetentionDays()
	cutoff := time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339)
	tx, err := db.Begin()
	if err != nil {
		log.Printf("db: purge webhooks begin failed: %v", err)
		return
	}
	delAttempts, err := tx.Exec(`DELETE FROM webhook_attempts WHERE delivery_id IN
		(SELECT id FROM webhook_deliveries WHERE received_at < ?)`, cutoff)
	if err != nil {
		_ = tx.Rollback()
		log.Printf("db: purge webhook attempts failed: %v", err)
		return
	}
	delDeliveries, err := tx.Exec(`DELETE FROM webhook_deliveries WHERE received_at < ?`, cutoff)
	if err != nil {
		_ = tx.Rollback()
		log.Printf("db: purge webhooks failed: %v", err)
		return
	}
	if err := tx.Commit(); err != nil {
		log.Printf("db: purge webhooks commit failed: %v", err)
		return
	}
	na, _ := delAttempts.RowsAffected()
	nd, _ := delDeliveries.RowsAffected()
	if na > 0 || nd > 0 {
		log.Printf("db: purged %d webhook deliveries (+%d attempts) older than %d days", nd, na, days)
	}
}
