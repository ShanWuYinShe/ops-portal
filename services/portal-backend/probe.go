package main

import (
	"crypto/tls"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// 连通拨测：portal 以 service 客户端证书定期 GET {ops_url}/ops/v1/health，
// 结果进统一网站视图的 reachable 徽标（注册 ≠ 可达 Masalah solved）。
// service 证书尚未签发时整轮跳过（日志限流提示一次）。

type probeResult struct {
	Reachable bool
	CheckedAt time.Time
	LatencyMs int64
	Note      string
	// Health 为站点自报的健康档（ok|degraded|starting|down），
	// 仅 Reachable 时有效；空串 = 2xx 但响应体不可解析（按 ok 显示）。
	Health string
}

var probeStore = struct {
	sync.Mutex
	m map[string]probeResult
}{m: make(map[string]probeResult)}

func getProbe(name string) (probeResult, bool) {
	probeStore.Lock()
	defer probeStore.Unlock()
	r, ok := probeStore.m[name]
	return r, ok
}

func clearProbe(name string) {
	probeStore.Lock()
	defer probeStore.Unlock()
	delete(probeStore.m, name)
}

func probeInterval() time.Duration {
	d, err := time.ParseDuration(getEnv("OPS_PROBE_INTERVAL", "60s"))
	if err != nil || d < 5*time.Second {
		return 60 * time.Second
	}
	return d
}

// serviceHTTPClient 出示 service 证书的出网 client（被管网站放行清单钉死其 serial）。
func serviceHTTPClient() (*http.Client, error) {
	ensureServiceKeyPEM()
	cert, err := tls.LoadX509KeyPair(servicePemPath(), serviceKeyPath())
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Timeout: 10 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{cert}},
		},
	}, nil
}

// probeOne 拨测单个原生注册站点；ops_url 为空由调用方过滤。
// 2xx 即为可达，同时尽力解析响应体的 status/checks（见 ops-v1-spec.md），
// 解析失败不影响可达判定（Health 留空，按 ok 显示）。
func probeOne(entry siteEntry, cli *http.Client) probeResult {
	url := strings.TrimRight(strings.TrimSpace(entry.OpsURL), "/") + "/ops/v1/health"
	start := time.Now()
	resp, err := cli.Get(url)
	lat := time.Since(start).Milliseconds()
	if err != nil {
		return probeResult{CheckedAt: time.Now(), LatencyMs: lat, Note: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return probeResult{Reachable: false, CheckedAt: time.Now(), LatencyMs: lat, Note: resp.Status}
	}
	res := probeResult{Reachable: true, CheckedAt: time.Now(), LatencyMs: lat}
	body, err := io.ReadAll(http.MaxBytesReader(nil, resp.Body, 1<<20))
	if err != nil {
		return res
	}
	var h struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
		Checks []struct {
			Name   string `json:"name"`
			OK     bool   `json:"ok"`
			Detail string `json:"detail"`
		} `json:"checks"`
	}
	if json.Unmarshal(body, &h) != nil {
		return res
	}
	if !h.OK {
		res.Health = "down"
		res.Note = "site reports ok:false"
		return res
	}
	switch strings.ToLower(strings.TrimSpace(h.Status)) {
	case "", "ok":
		res.Health = "ok"
	case "degraded", "starting":
		res.Health = strings.ToLower(strings.TrimSpace(h.Status))
		var bad []string
		for _, c := range h.Checks {
			if !c.OK {
				if c.Detail != "" {
					bad = append(bad, c.Name+": "+c.Detail)
				} else {
					bad = append(bad, c.Name)
				}
			}
		}
		if len(bad) > 0 {
			res.Note = strings.Join(bad, "; ")
		}
	default:
		res.Health = "degraded"
		res.Note = "unknown status " + strings.TrimSpace(h.Status)
	}
	return res
}

var probeNoCertOnce sync.Once

func runProbeOnce() {
	var targets []siteEntry
	for _, e := range listSiteEntries() {
		if strings.TrimSpace(e.OpsURL) != "" {
			targets = append(targets, e)
		}
	}
	if len(targets) == 0 {
		return
	}
	cli, err := serviceHTTPClient()
	if err != nil {
		probeNoCertOnce.Do(func() {
			log.Printf("probe: skip (no service cert yet, issue one first): %v", err)
		})
		return
	}
	for _, e := range targets {
		res := probeOne(e, cli)
		probeStore.Lock()
		probeStore.m[e.Name] = res
		probeStore.Unlock()
		if !res.Reachable {
			log.Printf("probe: %s unreachable: %s", e.Name, res.Note)
		} else if res.Health != "" && res.Health != "ok" {
			log.Printf("probe: %s reachable but %s: %s", e.Name, res.Health, res.Note)
		}
	}
}

func startProbeLoop() {
	go func() {
		runProbeOnce()
		t := time.NewTicker(probeInterval())
		defer t.Stop()
		for range t.C {
			runProbeOnce()
		}
	}()
}
