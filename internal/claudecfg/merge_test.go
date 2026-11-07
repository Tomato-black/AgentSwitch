package claudecfg

import (
	"reflect"
	"testing"
)

func TestMergeLayerStrategies(t *testing.T) {
	layers := []Layer{
		{Kind: ScopeUser, File: "user.json", Doc: map[string]any{
			"model": "u",
			"env":   map[string]any{"A": "u", "B": "user"},
			"permissions": map[string]any{
				"allow": []any{"Bash", "Read"},
			},
			"hooks":                             map[string]any{"PreToolUse": []any{"u"}},
			"enabledPlugins":                    map[string]any{"p": true},
			"skillOverrides":                    map[string]any{"s": "off"},
			"fallbackModel":                     []any{"u"},
			"skipDangerousModePermissionPrompt": false,
		}},
		{Kind: ScopeProject, File: "project.json", Doc: map[string]any{
			"model": "p",
			"env":   map[string]any{"A": "p"},
			"permissions": map[string]any{
				"allow": []any{"Bash", "Edit"},
			},
			"hooks":                             map[string]any{"PreToolUse": []any{"p"}},
			"enabledPlugins":                    map[string]any{"p": false},
			"skillOverrides":                    map[string]any{"s": "on"},
			"fallbackModel":                     []any{"p"},
			"skipDangerousModePermissionPrompt": true,
		}},
		{Kind: ScopeLocal, File: "local.json", Doc: map[string]any{
			"model": "l",
			"env":   map[string]any{"A": "l"},
			"permissions": map[string]any{
				"allow": []any{"WebFetch"},
			},
			"statusLine": map[string]any{"type": "command", "command": "x"},
		}},
		{Kind: ScopeManaged, File: "managed.json", Doc: map[string]any{
			"model": "m",
			"permissions": map[string]any{
				"deny": []any{"Bash(rm:*)"},
			},
		}},
	}
	merged, entries := Merge(layers)
	if merged["model"] != "m" {
		t.Fatalf("model = %#v", merged["model"])
	}
	if got := merged["env"].(map[string]any)["A"]; got != "l" {
		t.Fatalf("env.A = %#v", got)
	}
	if got := merged["env"].(map[string]any)["B"]; got != "user" {
		t.Fatalf("env.B = %#v", got)
	}
	wantAllow := []any{"Bash", "Read", "Edit", "WebFetch"}
	if !reflect.DeepEqual(merged["permissions"].(map[string]any)["allow"], wantAllow) {
		t.Fatalf("allow = %#v", merged["permissions"].(map[string]any)["allow"])
	}
	if got := merged["enabledPlugins"].(map[string]any)["p"]; got != false {
		t.Fatalf("plugin p = %#v", got)
	}
	if got := merged["skillOverrides"].(map[string]any)["s"]; got != "on" {
		t.Fatalf("skill s = %#v", got)
	}
	if got := merged["fallbackModel"]; !reflect.DeepEqual(got, []any{"p"}) {
		t.Fatalf("fallback = %#v", got)
	}
	if got := merged["skipDangerousModePermissionPrompt"]; got != true {
		t.Fatalf("skipDangerous = %#v", got)
	}
	if entries["env.A"].Source != ScopeLocal {
		t.Fatalf("env.A source = %q", entries["env.A"].Source)
	}
	if entries["permissions.allow"].Kind != MergeUnion {
		t.Fatalf("allow kind = %q", entries["permissions.allow"].Kind)
	}
	if entries["skipDangerousModePermissionPrompt"].Kind != MergeOr {
		t.Fatalf("skip kind = %q", entries["skipDangerousModePermissionPrompt"].Kind)
	}
	if entries["enabledPlugins.p"].Source != ScopeProject {
		t.Fatalf("plugin source = %q", entries["enabledPlugins.p"].Source)
	}
}

func TestMergeProjectModeFallsBack(t *testing.T) {
	layers := []Layer{
		{Kind: ScopeUser, Doc: map[string]any{"permissions": map[string]any{"defaultMode": "default"}}},
		{Kind: ScopeProject, Doc: map[string]any{"permissions": map[string]any{"defaultMode": "auto"}}},
	}
	merged, entries := Merge(layers)
	if got := merged["permissions"].(map[string]any)["defaultMode"]; got != "default" {
		t.Fatalf("mode = %#v", got)
	}
	entry := entries["permissions.defaultMode"]
	if entry.Source != ScopeUser {
		t.Fatalf("source = %q", entry.Source)
	}
	if len(entry.Contributions) != 2 {
		t.Fatalf("contributions = %#v", entry.Contributions)
	}
	if entry.Contributions[1].Effective {
		t.Fatal("project auto should be ineffective")
	}
}

func TestMergeOrAllFalse(t *testing.T) {
	layers := []Layer{
		{Kind: ScopeUser, Doc: map[string]any{"skipDangerousModePermissionPrompt": false}},
		{Kind: ScopeLocal, Doc: map[string]any{"skipDangerousModePermissionPrompt": false}},
	}
	merged, entries := Merge(layers)
	if got := merged["skipDangerousModePermissionPrompt"]; got != false {
		t.Fatalf("value = %#v", got)
	}
	if entries["skipDangerousModePermissionPrompt"].Source != ScopeLocal {
		t.Fatalf("source = %q", entries["skipDangerousModePermissionPrompt"].Source)
	}
}
