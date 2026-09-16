package main

import (
	"crypto/tls"
	"log"
	"net/http"
	"os"
)

// agentClient 返回带机器客户端证书的 HTTP client（经 CF 出网的 agent 必须出示
// 机器证书；同宿主机 http:// 内网直连不受影响）。PORTAL_AGENT_CERT/PORTAL_AGENT_KEY
// 指向 portal「机器证书包」落盘的 PEM 文件。
func agentClient() *http.Client {
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
