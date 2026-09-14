package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseDeployDirs(t *testing.T) {
	got := parseDeployDirs("workwork:/srv/workwork,hotspot:/srv/hotspot")
	want := map[string]string{"workwork": "/srv/workwork", "hotspot": "/srv/hotspot"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	// 脏输入跳过：缺冒号、空名、空路径
	if got := parseDeployDirs("bad-entry,:/x,proj:,ok:/srv/ok"); !reflect.DeepEqual(got, map[string]string{"ok": "/srv/ok"}) {
		t.Fatalf("dirty input got %v", got)
	}
	if got := parseDeployDirs(""); len(got) != 0 {
		t.Fatalf("empty got %v", got)
	}
}

func TestValidServiceName(t *testing.T) {
	for _, s := range []string{"nginx", "backend-go", "front_end1", "a"} {
		if !validServiceName(s) {
			t.Fatalf("%q should be valid", s)
		}
	}
	for _, s := range []string{"", "has space", "semi;colon", "dot.name", "slash/x", strings.Repeat("a", 65)} {
		if validServiceName(s) {
			t.Fatalf("%q should be invalid", s)
		}
	}
}
