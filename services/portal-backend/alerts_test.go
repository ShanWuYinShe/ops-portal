package main

import (
	"testing"
	"time"
)

// emitAlert：同 kind+subject 在冷却期内不重复，冷却过后可再发；
// 渠道未配置（senderFromEnv=nil）时也落审计。
func TestEmitAlertCooldown(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	t.Setenv("ALERT_COOLDOWN", "30m")
	db, err := getDB()
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}

	alertState.lastSent = map[string]time.Time{}
	base := time.Now()
	emitAlert(base, "agent_offline", "agent-x", "第一次")
	emitAlert(base.Add(time.Minute), "agent_offline", "agent-x", "冷却内不重复")
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit WHERE action='alert.agent_offline'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("audit rows = %d, want 1（冷却期内不得重复）", n)
	}

	emitAlert(base.Add(time.Hour), "agent_offline", "agent-x", "冷却后可再发")
	if err := db.QueryRow(`SELECT COUNT(*) FROM audit WHERE action='alert.agent_offline'`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Fatalf("audit rows = %d, want 2（冷却后允许再发）", n)
	}
}

// 心跳新鲜度判定与看门狗的首次基线语义。
func TestAgentOnlineStates(t *testing.T) {
	now := time.Now()
	m := map[string]agentState{
		"fresh":   {LastSeen: now.Add(-5 * time.Second)},
		"stale":   {LastSeen: now.Add(-2 * time.Minute)},
		"unknown": {LastSeen: now.Add(-59 * time.Second)},
	}
	got := agentOnlineStates(m, now, 60*time.Second)
	if !got["fresh"] || got["stale"] || !got["unknown"] {
		t.Fatalf("online states = %v, want fresh/unknown 在线、stale 离线", got)
	}
}

func TestProbeAlertDecision(t *testing.T) {
	up := probeResult{Reachable: true, Health: "ok"}
	down1 := probeResult{Reachable: false, Note: "timeout"}
	down2 := probeResult{Reachable: false, Note: "timeout"}
	degraded := probeResult{Reachable: true, Health: "degraded", Note: "redis: timeout"}

	// 基线轮：任何状态都不发告警
	if _, alerts := probeAlertDecision(probeAlertState{}, up, true); len(alerts) != 0 {
		t.Fatalf("baseline up alerts = %v, want none", alerts)
	}

	// 干净态下失败一次不算 down（去抖）
	clean := probeAlertState{Health: "ok"}
	if _, alerts := probeAlertDecision(clean, down1, false); len(alerts) != 0 {
		t.Fatalf("single fail alerts = %v, want none", alerts)
	}
	// 连续第二次失败才告警
	st1, _ := probeAlertDecision(clean, down1, false)
	st2, alerts := probeAlertDecision(st1, down2, false)
	if len(alerts) != 1 || alerts[0] != "probe_down" {
		t.Fatalf("second fail alerts = %v, want [probe_down]", alerts)
	}

	// down → up：恢复告警
	_, alerts = probeAlertDecision(st2, up, false)
	if len(alerts) != 1 || alerts[0] != "probe_up" {
		t.Fatalf("recover alerts = %v, want [probe_up]", alerts)
	}

	// ok → degraded：降级告警；degraded → ok：恢复告警
	st3, _ := probeAlertDecision(probeAlertState{}, up, false)
	_, alerts = probeAlertDecision(st3, degraded, false)
	if len(alerts) != 1 || alerts[0] != "probe_degraded" {
		t.Fatalf("degraded alerts = %v, want [probe_degraded]", alerts)
	}
	_, alerts = probeAlertDecision(probeAlertState{Health: "degraded"}, up, false)
	if len(alerts) != 1 || alerts[0] != "probe_recovered" {
		t.Fatalf("recovered alerts = %v, want [probe_recovered]", alerts)
	}
}

func TestAgentOfflineAfterDefault(t *testing.T) {
	t.Setenv("AGENT_OFFLINE_AFTER", "")
	if d := agentOfflineAfter(); d != 60*time.Second {
		t.Fatalf("default offline after = %v, want 60s", d)
	}
	t.Setenv("AGENT_OFFLINE_AFTER", "bogus")
	if d := agentOfflineAfter(); d != 60*time.Second {
		t.Fatalf("bogus offline after = %v, want fallback 60s", d)
	}
}
