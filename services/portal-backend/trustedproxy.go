package main

import (
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 受信代理检查：后端只认受信来源透传的 X-Cert-* 头。
// 思路与 workwork 的 TRUSTED_PROXY_CIDRS/HOSTS 一致：CIDR 静态匹配 +
// 主机名动态解析（短 TTL 缓存）。

var trustedProxyCache = struct {
	sync.Mutex
	nets    []*net.IPNet
	raw     string
	ips     []net.IP
	hosts   string
	expires time.Time
}{}

const trustedProxyHostTTL = 30 * time.Second

func parseTrustedProxies() ([]*net.IPNet, []net.IP) {
	trustedProxyCache.Lock()
	defer trustedProxyCache.Unlock()

	rawCIDRs := getEnv("TRUSTED_PROXY_CIDRS", "127.0.0.0/8,::1/128")
	rawHosts := getEnv("TRUSTED_PROXY_HOSTS", "portal-nginx")
	now := time.Now()
	if trustedProxyCache.raw == rawCIDRs && trustedProxyCache.hosts == rawHosts && now.Before(trustedProxyCache.expires) {
		return trustedProxyCache.nets, trustedProxyCache.ips
	}

	var nets []*net.IPNet
	for _, c := range strings.Split(rawCIDRs, ",") {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(c); err == nil {
			nets = append(nets, n)
		} else if ip := net.ParseIP(c); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		} else {
			log.Printf("trusted proxy: skip invalid CIDR/IP %q", c)
		}
	}

	var ips []net.IP
	for _, h := range strings.Split(rawHosts, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		addrs, err := net.LookupIP(h)
		if err != nil {
			log.Printf("trusted proxy: resolve %q failed: %v", h, err)
			continue
		}
		ips = append(ips, addrs...)
	}

	trustedProxyCache.nets = nets
	trustedProxyCache.raw = rawCIDRs
	trustedProxyCache.ips = ips
	trustedProxyCache.hosts = rawHosts
	trustedProxyCache.expires = now.Add(trustedProxyHostTTL)
	return nets, ips
}

// isTrustedProxy 判断连接对端是否为受信代理。
func isTrustedProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(strings.TrimSpace(host))
	if ip == nil {
		return false
	}
	nets, ips := parseTrustedProxies()
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	for _, a := range ips {
		if a.Equal(ip) {
			return true
		}
	}
	return false
}
