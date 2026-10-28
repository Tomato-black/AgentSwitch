package bridge

import (
	"strings"
	"testing"

	"AgentSwitch/internal/storage"
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
	if err == nil || !strings.Contains(err.Error(), "Codex 配置仅支持 env、models、codex、model_meta 键") {
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

func TestParseCodexSettingsTrimsInputs(t *testing.T) {
	got, err := parseCodexSettings(map[string]any{
		"models": []any{" my-code ", "test\t"},
		"codex": map[string]any{
			"subagent_model":            "  test  ",
			"subagent_reasoning_effort": " high ",
			"auto_review_model":         "   ",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 || got.Models[0] != "my-code" || got.Models[1] != "test" {
		t.Fatalf("models 应去首尾空白: %v", got.Models)
	}
	if got.SubagentModel != "test" || got.SubagentReasoningEffort != "high" || got.AutoReviewModel != "" {
		t.Fatalf("codex 字段应去首尾空白: %+v", got)
	}
	// 去空白后重复仍须拒绝。
	if _, err := parseCodexSettings(map[string]any{"models": []any{"a", " a"}}); err == nil {
		t.Fatal("去空白后重复应报错")
	}
	// 纯空白思考等级视为空（Codex 默认），不触发白名单错误。
	if _, err := parseCodexSettings(map[string]any{
		"codex": map[string]any{"subagent_reasoning_effort": "   "},
	}); err != nil {
		t.Fatalf("纯空白思考等级应视为空: %v", err)
	}
}

func TestResolveCodexModelsFallbackAndValidation(t *testing.T) {
	a := tempApp(t)
	for _, lm := range []storage.LogicalModel{
		{ID: "lm1", Name: "my-code", Enabled: true},
		{ID: "lm2", Name: "test", Enabled: true},
		{ID: "lm3", Name: "off", Enabled: false},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	got, err := acc.resolveCodexModels("my-code", codexAccessSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "my-code" || got[1] != "test" {
		t.Fatalf("%v", got)
	}
	if _, err := acc.resolveCodexModels("my-code", codexAccessSettings{Models: []string{"test"}}); err == nil {
		t.Fatal("第一项不是绑定的默认模型应报错")
	}
	if _, err := acc.resolveCodexModels("my-code", codexAccessSettings{Models: []string{"my-code", "off"}}); err == nil {
		t.Fatal("停用模型应报错")
	}
}

func TestResolveCodexModelsEdgeCases(t *testing.T) {
	a := tempApp(t)
	for _, lm := range []storage.LogicalModel{
		{ID: "lm1", Name: "my-code", Enabled: true},
		{ID: "lm2", Name: "test", Enabled: true},
		{ID: "lm3", Name: "off", Enabled: false},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}

	// 显式列表去空白后保留用户顺序。
	got, err := acc.resolveCodexModels("my-code", codexAccessSettings{Models: []string{" my-code ", "test "}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "my-code" || got[1] != "test" {
		t.Fatalf("%v", got)
	}

	cases := []struct {
		name     string
		bound    string
		settings codexAccessSettings
		want     string
	}{
		{"去空白后重复", "my-code", codexAccessSettings{Models: []string{"my-code", " my-code "}}, "重复"},
		{"显式列表含未知模型", "my-code", codexAccessSettings{Models: []string{"my-code", "ghost"}}, "ghost"},
		{"显式列表含纯空白名", "my-code", codexAccessSettings{Models: []string{"my-code", "  "}}, "不能为空"},
		{"绑定的默认模型不存在", "ghost", codexAccessSettings{}, "改绑"},
		{"绑定的默认模型已停用", "off", codexAccessSettings{}, "已停用"},
	}
	for _, tc := range cases {
		_, err := acc.resolveCodexModels(tc.bound, tc.settings)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err=%v，应包含 %q", tc.name, err, tc.want)
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
