package main

import (
	"crypto/tls"
	"log"
	"net/http"
	"os"
	"sync"
)

// agentClient 返回带机器客户端证书的 HTTP client（经 CF 出网的 agent 必须出示
// 机器证书；同宿主机 http:// 内网直连不受影响）。PORTAL_AGENT_CERT/PORTAL_AGENT_KEY
// 指向 portal「机器证书包」落盘的 PEM 文件。
// client 进程内缓存一次：证书文件运行期不变，缓存让 TLS 连接池跨心跳复用，
// 加载失败也只告警一次（否则每个心跳周期刷一条）。
var agentClientState struct {
	once sync.Once
	cli  *http.Client
}

func agentClient() *http.Client {
	agentClientState.once.Do(func() { agentClientState.cli = buildAgentClient() })
	return agentClientState.cli
}

func buildAgentClient() *http.Client {
	certFile, keyFile := os.Getenv("PORTAL_AGENT_CERT"), os.Getenv("PORTAL_AGENT_KEY")
	if certFile == "" || keyFile == "" {
		return http.DefaultClient
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		// 配了证书却加载失败不能静默降级：无证请求会被边缘 mTLS 拒掉，
		// 表现成"心跳全挂但看不出原因"。
		log.Printf("portal-agent: load client cert %s/%s failed: %v (requests will go out WITHOUT client cert and be rejected by the edge)", certFile, keyFile, err)
		return http.DefaultClient
	}
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
	}}
}
