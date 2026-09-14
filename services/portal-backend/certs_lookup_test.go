package main

import "testing"

// 门禁查表的大小写语义 + 空串防护（回归锁：CF 透传大写，建证返回小写）。
func TestFindActiveCertSemantics(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)

	if _, err := addCert(certRecord{
		ID: "c1", Label: "liyou-mbpro", Kind: "human",
		Serial: "AaBbCc", Fingerprint: "DEADbeef", Status: "active",
	}); err != nil {
		t.Fatalf("addCert: %v", err)
	}
	if _, err := addCert(certRecord{
		ID: "c2", Label: "revoked-dev", Kind: "human",
		Serial: "11223344", Fingerprint: "55667788", Status: "revoked",
	}); err != nil {
		t.Fatalf("addCert: %v", err)
	}

	cases := []struct {
		name       string
		serial, fp string
		want       string // 期望命中的 cert id，"" = 不命中
	}{
		{"serial 原样", "AaBbCc", "", "c1"},
		{"serial 大写（CF 透传）", "AABBCC", "", "c1"},
		{"serial 小写", "aabbcc", "", "c1"},
		{"指纹原样", "", "DEADbeef", "c1"},
		{"指纹大写", "", "DEADBEEF", "c1"},
		{"指纹小写", "", "deadbeef", "c1"},
		{"已吊销不命中", "11223344", "", ""},
		{"已吊销指纹不命中", "", "55667788", ""},
		{"空串不误命中", "", "", ""},
		{"未知值不命中", "FFFFFFFF", "", ""},
		{"serial 空但指纹命中", "", "deadbeef", "c1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := findActiveCert(c.serial, c.fp)
			if c.want == "" {
				if got != nil {
					t.Fatalf("期望不命中，实际 %+v", got)
				}
				return
			}
			if got == nil || got.ID != c.want {
				t.Fatalf("期望 %s，实际 %+v", c.want, got)
			}
		})
	}
}
