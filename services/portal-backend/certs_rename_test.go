package main

import (
	"strings"
	"testing"
)

// 证书改名：human 可改；portal-agent kind 数据层直接拒绝
// （label 即 portal_agent_id 绑定键）；改名不影响按 serial 的门禁查表。
func TestRenameCert(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	db, err := getDB()
	if err != nil {
		t.Fatalf("getDB: %v", err)
	}

	human, err := addCert(certRecord{ID: "c-h1", Label: "liyou-mbpro", Kind: "human", Serial: "AA11"})
	if err != nil {
		t.Fatalf("add human: %v", err)
	}
	if _, err := addCert(certRecord{ID: "c-a1", Label: "agent-x", Kind: "portal-agent", Serial: "BB22"}); err != nil {
		t.Fatalf("add agent: %v", err)
	}

	// human 改名成功，中文名可用
	got, err := renameCert(human.ID, "李尤的 MacBook Pro")
	if err != nil || got.Label != "李尤的 MacBook Pro" {
		t.Fatalf("rename = %+v, %v", got, err)
	}

	// 门禁按 serial 查表不受改名影响
	if findActiveCert("AA11", "") == nil {
		t.Fatal("findActiveCert by serial must still work after rename")
	}

	// portal-agent 拒绝：label 是机器身份绑定键
	if _, err := renameCert("c-a1", "new-name"); err == nil || !strings.Contains(err.Error(), "不可改名") {
		t.Fatalf("agent rename err = %v, want 不可改名", err)
	}
	if c, _ := scanCert(db.QueryRow(`SELECT ` + certColumns + ` FROM certs WHERE id='c-a1'`)); c.Label != "agent-x" {
		t.Fatalf("agent label changed unexpectedly: %s", c.Label)
	}

	// 不存在的 id
	if _, err := renameCert("not-exist", "x"); err == nil {
		t.Fatal("renaming missing cert must fail")
	}
}
