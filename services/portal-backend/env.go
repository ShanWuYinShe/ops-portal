package main

import (
	"os"
	"strings"
)

// getEnv 读取环境变量（去首尾空格），缺省返回 def。
func getEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}
