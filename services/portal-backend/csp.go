package main

// SPA 文档的 Content-Security-Policy。
// SvelteKit 预渲染的每张 HTML 各带一段内联 bootstrap <script>（index 用绝对
// 路径、其余页用相对路径，内容互不相同），且脚本体含每次构建都变化的模块
// 哈希——无法在 nginx 里写死白名单哈希。故由后端在首次用到时扫描 UI 产物
// 全部 *.html 的内联脚本、按内容计算 sha256 并缓存（与哈希基线 CSP 等效，
// 构建升级自动跟随）。
// 解析失败回退为仅 'self'：内联脚本会被浏览器拦截且控制台可见，上线冒烟
// 即可暴露；绝不静默放宽为 unsafe-inline。

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

// uiDir 前端产物目录（Docker 构建时拷入，见 backend Dockerfile 与 main.go）。
const uiDir = "/app/ui"

// cspBase 的 %s 为内联脚本哈希白名单（形如 ` 'sha256-xxx' 'sha256-yyy'`）。
// style-src 需 unsafe-inline：Svelte 过渡与容器定位用内联 style 属性。
const cspBase = "default-src 'self'; script-src 'self'%s; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; connect-src 'self'; font-src 'self'; object-src 'none'; " +
	"base-uri 'self'; form-action 'self'; frame-ancestors 'none'"

var cspCache struct {
	once sync.Once
	val  string
}

// cspHeader 返回 CSP 头值（进程内缓存，UI 产物运行期不变）。
func cspHeader() string {
	cspCache.once.Do(func() { cspCache.val = buildCSP(uiDir) })
	return cspCache.val
}

func buildCSP(dir string) string {
	var hashes []string
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".html") {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			hashes = append(hashes, inlineScriptHashes(data)...)
		}
	}
	hashes = dedupeStrings(hashes)
	if len(hashes) == 0 {
		return fmt.Sprintf(cspBase, "")
	}
	return fmt.Sprintf(cspBase, " "+strings.Join(hashes, " "))
}

// inlineScriptRe 匹配无属性 <script> 块（SvelteKit 内联引导脚本均无属性）。
// 捕获标签间原文——CSP 哈希按浏览器看到的精确字节计算。
var inlineScriptRe = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

func inlineScriptHashes(html []byte) []string {
	var out []string
	for _, m := range inlineScriptRe.FindAllSubmatch(html, -1) {
		sum := sha256.Sum256(m[1])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return out
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
