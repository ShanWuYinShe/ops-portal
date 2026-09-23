package main

import (
	"testing"
)

// 证书登记的输入校验：kind 枚举、hex 格式、active 重复登记拒绝。

func TestIsHexStr(t *testing.T) {
	cases := map[string]bool{
		"":         true, // 允许缺省（fingerprint 可空）
		"197FE164": true,
		"abc123":   true,
		"ABC123":   true,
		"0x12":     false, // x 非 hex
		"12 34":    false, // 空格
		"zz":       false,
	}
	for in, want := range cases {
		if got := isHexStr(in); got != want {
			t.Errorf("isHexStr(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestActiveCertDupe(t *testing.T) {
	t.Setenv("DATA_DIR", t.TempDir())
	if _, err := addCert(certRecord{ID: "c1", Label: "a", Kind: "human", Serial: "aa11", Status: "active"}); err != nil {
		t.Fatalf("addCert c1: %v", err)
	}
	if _, err := addCert(certRecord{ID: "c2", Label: "b", Kind: "human", Serial: "bb22", Fingerprint: "fp22", Status: "revoked"}); err != nil {
		t.Fatalf("addCert c2: %v", err)
	}

	// serial 大小写不敏感命中（与 findActiveCert 的 NOCASE 语义一致）
	dupe, err := activeCertDupe("AA11", "")
	if err != nil {
		t.Fatalf("activeCertDupe: %v", err)
	}
	if !dupe {
		t.Error("same serial (case-insensitive) should be detected as dupe")
	}

	// 已吊销证书的 serial/指纹不算冲突
	if dupe, _ = activeCertDupe("BB22", ""); dupe {
		t.Error("revoked cert serial should not count as dupe")
	}
	if dupe, _ = activeCertDupe("", "FP22"); dupe {
		t.Error("revoked cert fingerprint should not count as dupe")
	}

	// 全新 serial+指纹无冲突
	if dupe, _ = activeCertDupe("cc33", "fp33"); dupe {
		t.Error("fresh serial/fingerprint should not be dupe")
	}
}
