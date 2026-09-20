package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// 本机探活端点：给容器 healthcheck 用（busybox wget --spider，与 backend 的
// 探针同风格）。只绑 127.0.0.1，容器网络外不可达。
//
// 语义分工：agent 进程活性归 healthcheck 管（始终 200，只要 HTTP 栈还能
// 应答即进程未死）；portal 连通性归 portal 侧心跳超时告警管
// （AGENT_OFFLINE_AFTER，见 docs/portal-agent-protocol.md）。不因心跳失败
// 回 503，避免 portal 短暂重启时被编排层误判 agent 不健康。最近一次心跳
// 结果仅作为诊断信息随 body 暴露（docker inspect 可见）。

var (
	// 最近一次心跳尝试的 unix 秒；0 = 尚未尝试过。
	lastBeatAt atomic.Int64
	lastBeatOK atomic.Bool
)

// recordHeartbeat 在每个心跳周期结束时记录结果，供 /health 展示。
func recordHeartbeat(err error) {
	lastBeatAt.Store(time.Now().Unix())
	lastBeatOK.Store(err == nil)
}

func healthBody() string {
	at := lastBeatAt.Load()
	if at == 0 {
		return `{"ok":true,"last_heartbeat":"never"}`
	}
	return fmt.Sprintf(`{"ok":true,"last_heartbeat":{"at_unix":%d,"ok":%t}}`, at, lastBeatOK.Load())
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(healthBody()))
}

// startHealthServer 起 /health 探活端点；addr 取 PORTAL_AGENT_HEALTH_ADDR
// （默认 127.0.0.1:18080，设空串禁用）。监听失败只降级告警不退出：
// 端口被占不应打断心跳主循环（healthcheck 会显示 unhealthy 引人来看日志）。
func startHealthServer(addr string) {
	if addr == "" {
		return
	}
	srv, ln, err := newHealthServer(addr)
	if err != nil {
		log.Printf("portal-agent: health endpoint disabled (listen %s: %v)", addr, err)
		return
	}
	log.Printf("portal-agent: health endpoint on http://%s/health", addr)
	go func() {
		if serveErr := srv.Serve(ln); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Printf("portal-agent: health server stopped: %v", serveErr)
		}
	}()
}

// newHealthServer 组装 /health 的 server 与 listener，供 startHealthServer
// 与测试共用；addr 传 "127.0.0.1:0" 可拿随机端口。
func newHealthServer(addr string) (*http.Server, net.Listener, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", healthHandler)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	return srv, ln, nil
}
