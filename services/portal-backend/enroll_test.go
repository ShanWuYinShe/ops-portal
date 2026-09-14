package main

import (
	"testing"
	"time"
)

// enroll 令牌：签发 → 兑换成功 → 重放失败；串号失败；过期失败；
// enroll 令牌不能当正式 token 用。
func TestIssueConsumeEnroll(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)

	plain, tok, err := issueEnrollToken("agent-test-01", "tester")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if len(plain) < 8 || plain[:7] != "enroll_" {
		t.Fatalf("enroll prefix = %q", plain)
	}
	if tok.Kind != "enroll" || tok.ExpiresAt == "" {
		t.Fatalf("kind/expires = %+v", tok)
	}

	bound, err := consumeEnrollToken(plain, "agent-test-01")
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if bound != "agent-test-01" {
		t.Fatalf("bound = %q", bound)
	}

	// 重放：单次使用，再兑失败
	if _, err := consumeEnrollToken(plain, "agent-test-01"); err == nil {
		t.Fatal("replay should fail")
	}

	// 串号失败
	plain2, _, err := issueEnrollToken("agent-test-02", "tester")
	if err != nil {
		t.Fatalf("issue2: %v", err)
	}
	if _, err := consumeEnrollToken(plain2, "agent-test-OTHER"); err == nil {
		t.Fatal("mismatch agent should fail")
	}

	// enroll 令牌不能当正式 token 用
	if _, ok := validAgentToken(plain2); ok {
		t.Fatal("enroll token must not pass validAgentToken")
	}

	// 过期失败：回写一个已过期的 enroll 记录
	db, err := getDB()
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	if _, err := db.Exec(`UPDATE agent_tokens SET expires_at=? WHERE kind='enroll' AND used=0`,
		time.Now().Add(-time.Minute).UTC().Format(time.RFC3339)); err != nil {
		t.Fatalf("age: %v", err)
	}
	if _, err := consumeEnrollToken(plain2, "agent-test-02"); err == nil {
		t.Fatal("expired should fail")
	}
}
