package main

import (
	"net"
	"strings"
	"testing"
)

func TestIsPublicIP(t *testing.T) {
	cases := []struct {
		ip   string
		want bool
	}{
		{"1.2.3.4", true},
		{"2606:4700::1111", true},
		{"127.0.0.1", false},
		{"::1", false},
		{"10.0.0.5", false},
		{"172.16.1.1", false},
		{"192.168.1.1", false},
		{"169.254.1.1", false}, // link-local
		{"fe80::1", false},     // IPv6 link-local
		{"fc00::1", false},     // IPv6 ULA
		{"0.0.0.0", false},
		{"224.0.0.1", false}, // 组播
	}
	for _, c := range cases {
		if got := isPublicIP(net.ParseIP(c.ip)); got != c.want {
			t.Errorf("isPublicIP(%s) = %v, want %v", c.ip, got, c.want)
		}
	}
}

func TestValidateProbeURL(t *testing.T) {
	ok := []string{
		"https://demo.example.com",
		"https://demo.example.com/",
		"http://demo.example.com:8443/ops",
		"https://1.2.3.4:9000",
		"https://[2606:4700::1111]/",
	}
	for _, in := range ok {
		if _, err := validateProbeURL(in); err != nil {
			t.Errorf("validateProbeURL(%q) unexpected error: %v", in, err)
		}
	}

	bad := map[string]string{
		"":                                 "ops_url required",
		"ftp://demo.example.com":           "仅支持 http/https",
		"file:///etc/passwd":               "仅支持 http/https",
		"https://":                         "缺少 host",
		"https://user:pw@demo.example.com": "userinfo",
		"https://127.0.0.1":                "非公网",
		"https://10.1.2.3":                 "非公网",
		"https://192.168.5.5":              "非公网",
		"https://[::1]:8319":               "非公网",
		"http://169.254.169.254":           "非公网",
	}
	for in, wantSub := range bad {
		_, err := validateProbeURL(in)
		if err == nil {
			t.Errorf("validateProbeURL(%q) = nil error, want containing %q", in, wantSub)
			continue
		}
		if len(wantSub) > 0 && !strings.Contains(err.Error(), wantSub) {
			t.Errorf("validateProbeURL(%q) error = %q, want containing %q", in, err.Error(), wantSub)
		}
	}
}

// 逃生阀打开时私网目标放行（内网自托管联调场景）。
func TestValidateProbeURLAllowPrivate(t *testing.T) {
	t.Setenv("OPS_PROBE_ALLOW_PRIVATE", "1")
	if _, err := validateProbeURL("http://127.0.0.1:8319"); err != nil {
		t.Errorf("allow-private mode should accept loopback, got %v", err)
	}
}

// 解析失败的域名不拦（站点未上线先注册），dial 侧兜底。
func TestValidateProbeURLUnresolvedHost(t *testing.T) {
	if _, err := validateProbeURL("https://nonexistent.invalid"); err != nil {
		t.Errorf("unresolved host should be accepted at register time, got %v", err)
	}
}

func TestProbeDialControl(t *testing.T) {
	if err := probeDialControl("tcp", "127.0.0.1:8319", nil); err == nil {
		t.Error("loopback dial target should be refused")
	}
	if err := probeDialControl("tcp", "10.0.0.9:443", nil); err == nil {
		t.Error("private dial target should be refused")
	}
	if err := probeDialControl("tcp", "1.2.3.4:443", nil); err != nil {
		t.Errorf("public dial target should pass, got %v", err)
	}
	if err := probeDialControl("tcp", "not-an-address", nil); err == nil {
		t.Error("malformed address should be refused")
	}

	t.Setenv("OPS_PROBE_ALLOW_PRIVATE", "1")
	if err := probeDialControl("tcp", "127.0.0.1:8319", nil); err != nil {
		t.Errorf("allow-private mode should accept loopback, got %v", err)
	}
}
