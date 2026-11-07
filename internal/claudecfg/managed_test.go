package claudecfg

import (
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
