package bridge

import (
	"strings"
	"testing"
)

func TestParseCodexSettings(t *testing.T) {
	doc := map[string]any{
		"models": []any{"my-code", "test"},
		"codex": map[string]any{
			"subagent_model":            "test",
			"subagent_reasoning_effort": "high",
			"auto_review_model":         "my-code",
		},
	}
	got, err := parseCodexSettings(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 || got.Models[1] != "test" ||
		got.SubagentModel != "test" || got.AutoReviewModel != "my-code" {
		t.Fatalf("%+v", got)
	}
	if got.SubagentReasoningEffort != "high" {
		t.Fatalf("%+v", got)
	}
}

func TestParseCodexSettingsRejectsBadShapes(t *testing.T) {
	cases := []map[string]any{
		{"models": []any{"a", "a"}},
		{"models": []any{}},
		{"codex": map[string]any{"unknown": "x"}},
		{"codex": map[string]any{"subagent_reasoning_effort": "bogus"}},
	}
	for _, doc := range cases {
		if _, err := parseCodexSettings(doc); err == nil {
			t.Fatalf("应拒绝: %#v", doc)
		}
	}
}

func TestParseCodexSettingsRejectsNonStrings(t *testing.T) {
	cases := []map[string]any{
		{"models": "my-code"},
		{"models": []any{"my-code", 1}},
		{"models": []any{""}},
		{"codex": "x"},
		{"codex": map[string]any{"subagent_model": 1}},
		{"codex": map[string]any{"auto_review_model": false}},
	}
	for _, doc := range cases {
		if _, err := parseCodexSettings(doc); err == nil {
			t.Fatalf("应拒绝: %#v", doc)
		}
	}
}

func TestParseCodexSettingsAllowsLegacyEnvOnly(t *testing.T) {
	// 旧配置只有 env（或空文档）：models/codex 缺省视为空，保持可解析。
	for _, doc := range []map[string]any{{}, {"env": map[string]any{"A": "1"}}, {"models": nil, "codex": nil}} {
		got, err := parseCodexSettings(doc)
		if err != nil {
			t.Fatalf("%#v: %v", doc, err)
		}
		if len(got.Models) != 0 || got.SubagentModel != "" {
			t.Fatalf("%#v: %+v", doc, got)
		}
	}
}

func TestParseAccessDocCodexAllowsModelsAndCodex(t *testing.T) {
	doc, env, err := parseAccessDoc("codex", `{"env":{"A":"1"},"models":["my-code","test"],"codex":{"subagent_model":"test"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if env["A"] != "1" {
		t.Fatalf("env=%v", env)
	}
	if _, ok := doc["models"]; !ok {
		t.Fatalf("models 应保留: %v", doc)
	}
}

func TestParseAccessDocCodexRejectsOtherTopLevelKeys(t *testing.T) {
	_, _, err := parseAccessDoc("codex", `{"env":{},"mcpServers":{}}`)
	if err == nil || !strings.Contains(err.Error(), "Codex 配置仅支持 env、models、codex 键") {
		t.Fatalf("codex 顶层键应报错: %v", err)
	}
}

func TestParseAccessDocCodexRejectsBadModels(t *testing.T) {
	for _, raw := range []string{
		`{"models":[]}`,
		`{"models":["a","a"]}`,
		`{"models":[1]}`,
		`{"codex":{"bogus":"x"}}`,
	} {
		if _, _, err := parseAccessDoc("codex", raw); err == nil {
			t.Fatalf("应拒绝: %s", raw)
		}
	}
}

func TestCodexSettingsFromDocLenient(t *testing.T) {
	// 提取函数不报错：坏字段被忽略，好字段保留。
	got := codexSettingsFromDoc(map[string]any{
		"models": []any{"a", "b"},
		"codex":  map[string]any{"subagent_model": "b", "auto_review_model": 1},
	})
	if len(got.Models) != 2 || got.SubagentModel != "b" || got.AutoReviewModel != "" {
		t.Fatalf("%+v", got)
	}

	models, ok := codexModelsFromDoc(map[string]any{"models": []any{"a", "b"}})
	if !ok || len(models) != 2 || models[1] != "b" {
		t.Fatalf("models=%v ok=%v", models, ok)
	}
	if _, ok := codexModelsFromDoc(map[string]any{}); ok {
		t.Fatal("缺省 models 不应报告显式列表")
	}
	if _, ok := codexModelsFromDoc(map[string]any{"models": []any{1}}); ok {
		t.Fatal("非法 models 不应报告显式列表")
	}
}
