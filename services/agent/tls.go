package main

import (
	"crypto/tls"
	"net/http"
	"os"
)

// agentClient 返回带机器客户端证书的 HTTP client（经 CF 出网的 agent 必须出示
// 机器证书；同宿主机 http:// 内网直连不受影响）。AGENT_CERT/AGENT_KEY 指向
// portal「机器证书包」落盘的 PEM 文件。
func agentClient() *http.Client {
	certFile, keyFile := os.Getenv("AGENT_CERT"), os.Getenv("AGENT_KEY")
	if certFile == "" || keyFile == "" {
		return http.DefaultClient
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return http.DefaultClient
	}
	return &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
	}}
}
