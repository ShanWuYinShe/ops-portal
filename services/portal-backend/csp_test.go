package main

import (
	"crypto/sha256"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// 内联脚本哈希按标签间原文（含换行缩进）独立计算，与实现互不共用逻辑。
func TestInlineScriptHashes(t *testing.T) {
	body := "\n\t\t\t\tfoo();\n\t\t\t"
	html := []byte("<html><p>x</p><script>" + body + "</script><script>bar()</script></html>")
	got := inlineScriptHashes(html)
	if len(got) != 2 {
		t.Fatalf("hashes = %v, want 2", got)
	}
	sum := sha256.Sum256([]byte(body))
	want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
	if got[0] != want {
		t.Fatalf("hash = %s, want %s", got[0], want)
	}
	// 无属性限定：带属性的 <script src=...> 不参与哈希
	html2 := []byte(`<script src="a.js"></script><script>ok()</script>`)
	if h := inlineScriptHashes(html2); len(h) != 1 {
		t.Fatalf("attr script must be skipped: %v", h)
	}
}

// buildCSP 扫描目录全部 *.html 取哈希并集；无脚本/无文件回退为无哈希白名单。
func TestBuildCSP(t *testing.T) {
	dir := t.TempDir()
	write := func(name, html string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(html), 0600); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	write("index.html", "<script>alpha()</script>")
	write("dashboard.html", "<script>beta()</script><script>alpha()</script>")
	write("readme.txt", "<script>ignored()</script>")

	csp := buildCSP(dir)
	if n := strings.Count(csp, "'sha256-"); n != 2 {
		t.Fatalf("csp = %s, want exactly 2 unique hashes", csp)
	}
	if !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("csp missing frame-ancestors: %s", csp)
	}

	empty := buildCSP(t.TempDir())
	if strings.Contains(empty, "sha256") {
		t.Fatalf("empty dir csp = %s, want no hashes", empty)
	}
}
