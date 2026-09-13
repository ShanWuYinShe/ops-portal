package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
)

// constantTimeEqual 常量时间比较 token，防止时序侧信道。
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// sha256Hex token 摘要（库里只存摘要，防泄露直接可用）。
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
