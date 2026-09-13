package main

import "crypto/subtle"

// constantTimeEqual 常量时间比较 token，防止时序侧信道。
func constantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
