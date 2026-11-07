package claudecfg

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestComputeManagedSet(t *testing.T) {
	composed := map[string]string{
		"ANTHROPIC_BASE_URL": "http://127.0.0.1:17890",
		"HTTPS_PROXY":        "http://proxy:8080",
	}
	configs := []map[string]any{
		{"permissions": map[string]any{"allow": []any{"Bash"}}, "env": map[string]any{}},
		{"statusLine": map[string]any{"type": "command"}, "env": map[string]any{}},
	}
	got := ComputeManagedSet(configs, composed)
	if len(got.StrongEnv) != 13 {
		t.Fatalf("strong env = %v", got.StrongEnv)
	}
	if !reflect.DeepEqual(got.StrongTopLevel, []string{"permissions", "statusLine"}) {
		t.Fatalf("strong top-level = %v", got.StrongTopLevel)
	}
	if !reflect.DeepEqual(got.WeakEnv, []string{"env.HTTPS_PROXY"}) {
		t.Fatalf("weak env = %v", got.WeakEnv)
	}
	if got.Source["permissions"] == "" || got.Source["env.HTTPS_PROXY"] == "" {
		t.Fatalf("source = %v", got.Source)
	}
}

// TestMergeManagedDocs 覆盖 spec §6.1.1 的托管层内部合并：标量后写覆盖、
// 数组并集去重、对象逐键合并、四个整值替换键与两个同名条目整值替换键。
func TestMergeManagedDocs(t *testing.T) {
	base := map[string]any{
		"model":             "sonnet",
		"permissions":       map[string]any{"defaultMode": "default", "allow": []any{"Bash(go test:*)"}},
		"env":               map[string]any{"A": "1", "B": "2"},
		"fallbackModel":     []any{"sonnet", "opus"},
		"availableModels":   []any{"sonnet"},
		"managedMcpServers": map[string]any{"fs": map[string]any{"command": "fs", "timeout": 30}},
	}
	drop1 := map[string]any{
		"model": "opus", // 标量后写覆盖
		"permissions": map[string]any{ // 对象逐键合并（未提及键保留）
			"allow": []any{"Read"}, // 数组并集去重
		},
		"env":             map[string]any{"B": "override", "C": "3"},
		"fallbackModel":   []any{"haiku"}, // 整值替换（不并集）
		"availableModels": []any{"opus", "sonnet"},
		"managedMcpServers": map[string]any{ // 同名 fs 整值替换、新增 srv
			"fs":  map[string]any{"command": "fs2"},
			"srv": map[string]any{"url": "http://srv"},
		},
	}
	got := MergeManagedDocs([]map[string]any{base, drop1})

	if got["model"] != "opus" {
		t.Fatalf("scalar later-wins failed: %v", got["model"])
	}
	perm := got["permissions"].(map[string]any)
	if perm["defaultMode"] != "default" {
		t.Fatalf("object key-wise must keep unmentioned keys: %v", perm)
	}
	if !reflect.DeepEqual(perm["allow"], []any{"Bash(go test:*)", "Read"}) {
		t.Fatalf("array union+dedupe failed: %v", perm["allow"])
	}
	env := got["env"].(map[string]any)
	if env["A"] != "1" || env["B"] != "override" || env["C"] != "3" {
		t.Fatalf("env key-wise merge failed: %v", env)
	}
	if !reflect.DeepEqual(got["fallbackModel"], []any{"haiku"}) {
		t.Fatalf("fallbackModel must whole-replace: %v", got["fallbackModel"])
	}
	if !reflect.DeepEqual(got["availableModels"], []any{"opus", "sonnet"}) {
		t.Fatalf("availableModels must whole-replace: %v", got["availableModels"])
	}
	mcp := got["managedMcpServers"].(map[string]any)
	fs := mcp["fs"].(map[string]any)
	if fs["command"] != "fs2" || len(fs) != 1 {
		t.Fatalf("same-name managedMcpServers entry must whole-replace: %v", fs)
	}
	if mcp["srv"] == nil {
		t.Fatalf("new managedMcpServers entry missing: %v", mcp)
	}
}

func TestMergeManagedDocsScalarTypeChangeAndNumbers(t *testing.T) {
	// 类型变化（数组→标量）后写整值覆盖；json.Number 原样保留。
	got := MergeManagedDocs([]map[string]any{
		{"cleanupPeriodDays": json.Number("30"), "tags": []any{"a"}},
		{"cleanupPeriodDays": json.Number("45"), "tags": "none"},
	})
	if got["cleanupPeriodDays"] != json.Number("45") {
		t.Fatalf("json.Number must be preserved: %#v", got["cleanupPeriodDays"])
	}
	if got["tags"] != "none" {
		t.Fatalf("type change must later-win whole value: %v", got["tags"])
	}
}

func TestMergeManagedDocsDoesNotMutateInputs(t *testing.T) {
	base := map[string]any{
		"env":                    map[string]any{"A": "1"},
		"managedMcpServers":      map[string]any{"fs": map[string]any{"command": "fs"}},
		"extraKnownMarketplaces": map[string]any{"m1": map[string]any{"source": "x"}},
	}
	drop := map[string]any{
		"env":                    map[string]any{"B": "2"},
		"managedMcpServers":      map[string]any{"fs": map[string]any{"command": "fs2"}},
		"extraKnownMarketplaces": map[string]any{"m1": map[string]any{"source": "y"}},
	}
	got := MergeManagedDocs([]map[string]any{base, drop})
	if got["env"].(map[string]any)["A"] != "1" {
		t.Fatalf("env merge lost base key: %v", got["env"])
	}
	if base["env"].(map[string]any)["B"] != nil {
		t.Fatal("merge must not mutate input docs (env)")
	}
	if base["managedMcpServers"].(map[string]any)["fs"].(map[string]any)["command"] != "fs" {
		t.Fatal("merge must not mutate input docs (managedMcpServers)")
	}
	if base["extraKnownMarketplaces"].(map[string]any)["m1"].(map[string]any)["source"] != "x" {
		t.Fatal("merge must not mutate input docs (extraKnownMarketplaces)")
	}
	if got["extraKnownMarketplaces"].(map[string]any)["m1"].(map[string]any)["source"] != "y" {
		t.Fatalf("same-name marketplace entry must whole-replace: %v", got["extraKnownMarketplaces"])
	}
}
