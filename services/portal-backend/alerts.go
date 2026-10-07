package main

// 告警引擎：状态跃迁（agent 掉线/恢复、拨测不可达/恢复/降级）统一走
// emitAlert：写审计（action = "alert."+kind，随审计保留期清理）+ 通知渠道。
// 渠道由 senderFromEnv 按 env 构造（当前为出站 webhook，见 alertwebhook.go），
// 未配置时只记录不发送；接入 Telegram/SMTP 时实现 alertSender 并在此返回即可。

import (
	"log"
	"strings"
	"sync"
	"time"
)

// alertSender 通知渠道接口。Send 的错误只记日志，不影响引擎运转。
type alertSender interface {
	Send(kind, subject, detail string) error
}

// senderFromEnv 按环境变量构造通知渠道；未配置返回 nil（只记录）。
// 渠道接入点：在此新增分支返回对应实现（当前唯一实现见 alertwebhook.go）。
func senderFromEnv() alertSender { return webhookAlertSenderFromEnv() }

var alertState = struct {
	sync.Mutex
	lastSent map[string]time.Time // kind+"/"+subject -> 上次发出时刻
	sender   alertSender
	once     sync.Once
}{lastSent: map[string]time.Time{}}

func alertCooldown() time.Duration {
	d, err := time.ParseDuration(getEnv("ALERT_COOLDOWN", "30m"))
	if err != nil || d < time.Minute {
		return 30 * time.Minute
	}
	return d
}

// emitAlert 单条告警：冷却期内同一 kind+subject 不重复；全部进审计。
func emitAlert(now time.Time, kind, subject, detail string) {
	alertState.Lock()
	key := kind + "/" + subject
	if last, ok := alertState.lastSent[key]; ok && now.Sub(last) < alertCooldown() {
		alertState.Unlock()
		return
	}
	alertState.lastSent[key] = now
	if alertState.sender == nil {
		alertState.once.Do(func() { alertState.sender = senderFromEnv() })
	}
	sender := alertState.sender
	alertState.Unlock()

	audit("portal", "", "alert."+kind, subject, "ok", detail, "")
	// 异步发送：告警由看门狗（10s tick）与拨测循环（默认 60s）触发，出站渠道
	// 最慢可占满 alertWebhookTimeout；同步发送会拖住这两个循环，与「Send 的
	// 错误只记日志，不影响引擎运转」的契约不符。冷却窗口已限制并发量。
	if sender != nil {
		go func() {
			if err := sender.Send(kind, subject, detail); err != nil {
				log.Printf("alert: send %s/%s failed: %v", kind, subject, err)
			}
		}()
	}
}

// agentOfflineAfter 心跳多久无更新视为掉线（默认 60s ≈ 6 个心跳周期）。
func agentOfflineAfter() time.Duration {
	d, err := time.ParseDuration(getEnv("AGENT_OFFLINE_AFTER", "60s"))
	if err != nil || d < 10*time.Second {
		return 60 * time.Second
	}
	return d
}

// agentOnlineStates 纯函数：按 last_seen 新鲜度计算各 agent 在线态。
func agentOnlineStates(m map[string]agentState, now time.Time, offlineAfter time.Duration) map[string]bool {
	out := make(map[string]bool, len(m))
	for id, st := range m {
		out[id] = now.Sub(st.LastSeen) <= offlineAfter
	}
	return out
}

// startAgentWatchdog 周期扫描心跳新鲜度，online↔offline 跃迁时告警。
// backend 重启后第一轮只建基线（不补发历史跃迁，防重启风暴）。
func startAgentWatchdog() {
	go func() {
		offlineAfter := agentOfflineAfter()
		seen := map[string]bool{}
		first := true
		t := time.NewTicker(10 * time.Second)
		defer t.Stop()
		for range t.C {
			agentStore.Lock()
			now := agentOnlineStates(agentStore.m, time.Now(), offlineAfter)
			agentStore.Unlock()
			if first {
				seen, first = now, false
				continue
			}
			for id, on := range now {
				if seen[id] == on {
					continue
				}
				if on {
					emitAlert(time.Now(), "agent_online", id, "心跳恢复")
				} else {
					emitAlert(time.Now(), "agent_offline", id,
						"心跳超时（> "+offlineAfter.String()+"），该机所有网站失联")
				}
			}
			seen = now
		}
	}()
}

// probeAlertState 单个站点的告警态（与视图字段 probeResult 分离）。
type probeAlertState struct {
	Down   bool   // 连续失败达到阈值
	Fails  int    // 连续失败次数
	Health string // 上次可达时的健康档（"" = 从未可达/当前不可达）
}

// 连续失败多少次才算 down（拨测默认 60s 一轮，2 次 ≈ 2 分钟，滤单次抖动）。
const probeDownAfterFails = 2

// probeAlertDecision 由上一态与新一轮拨测结果得出要发的告警与下一态。
// first=true 表示本轮是 backend 启动后的基线轮，只记录不告警。
func probeAlertDecision(prev probeAlertState, res probeResult, first bool) (next probeAlertState, alerts []string) {
	next = prev
	if !res.Reachable {
		next.Fails = prev.Fails + 1
		if !prev.Down && next.Fails >= probeDownAfterFails {
			next.Down = true
			next.Health = ""
			if !first {
				alerts = append(alerts, "probe_down")
			}
		}
		return next, alerts
	}
	next.Fails = 0
	wasDown, wasHealth := prev.Down, prev.Health
	next.Health = res.Health
	if wasDown {
		next.Down = false
		if !first {
			alerts = append(alerts, "probe_up")
		}
		return next, alerts
	}
	degraded := res.Health != "" && res.Health != "ok"
	wasDegraded := wasHealth != "" && wasHealth != "ok"
	if degraded && !wasDegraded && !first {
		alerts = append(alerts, "probe_degraded")
	}
	if !degraded && wasDegraded && !first {
		alerts = append(alerts, "probe_recovered")
	}
	return next, alerts
}

var probeAlerts = struct {
	sync.Mutex
	m map[string]probeAlertState
}{m: map[string]probeAlertState{}}

// probeAlertCheck 在拨测循环里对单个站点做跃迁检测并发出告警。
func probeAlertCheck(name string, res probeResult, first bool) {
	probeAlerts.Lock()
	prev := probeAlerts.m[name]
	next, alerts := probeAlertDecision(prev, res, first)
	probeAlerts.m[name] = next
	probeAlerts.Unlock()
	for _, kind := range alerts {
		emitAlert(time.Now(), kind, name, probeAlertDetail(kind, res))
	}
}

func probeAlertDetail(kind string, res probeResult) string {
	switch kind {
	case "probe_down":
		return "站点拨测连续不可达：" + strings.TrimSpace(res.Note)
	case "probe_up":
		return "站点拨测恢复"
	case "probe_degraded":
		return "站点可达但健康档 " + res.Health + "：" + strings.TrimSpace(res.Note)
	case "probe_recovered":
		return "站点健康档恢复 ok"
	}
	return ""
}
