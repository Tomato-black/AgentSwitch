package presets

import (
	"testing"

	"AgentSwitch/internal/provider"
)

// TestAllPresetsValidateBaseURL 覆盖 spec §10：每个内置预设 base_url
// 可被 SSRF/私网校验接受（allowPrivate=false，预设必须是公网 https 端点）。
func TestAllPresetsValidateBaseURL(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("All() empty")
	}
	for _, p := range all {
		if err := provider.ValidateBaseURL(p.BaseURL, false); err != nil {
			t.Errorf("preset %s base_url %q: %v", p.ID, p.BaseURL, err)
		}
	}
}

// TestPresetIDsUnique 预设 ID 唯一（UI 网格与 ById 按 ID 索引）。
func TestPresetIDsUnique(t *testing.T) {
	seen := make(map[string]bool)
	for _, p := range All() {
		if seen[p.ID] {
			t.Errorf("duplicate preset id %q", p.ID)
		}
		seen[p.ID] = true
	}
}

// TestPresetFieldsComplete 覆盖 spec §10「每个内置预设字段完整」：六字段齐全，
// 协议只允许 anthropic/openai，且必含简报点名的 12 个起步 ID。
func TestPresetFieldsComplete(t *testing.T) {
	required := map[string]bool{
		"xiaomi": false, "qwen": false, "deepseek": false, "kimi": false,
		"zhipu": false, "siliconflow": false, "anthropic": false, "openai": false,
		"google": false, "groq": false, "together": false, "openrouter": false,
	}
	for _, p := range All() {
		if p.ID == "" || p.DisplayName == "" || p.BaseURL == "" || p.DocURL == "" {
			t.Errorf("preset %q has empty required field: %+v", p.ID, p)
		}
		switch p.Protocol {
		case "anthropic", "openai":
		default:
			t.Errorf("preset %s protocol %q want anthropic|openai", p.ID, p.Protocol)
		}
		if len(p.DefaultModels) == 0 {
			t.Errorf("preset %s has no default models", p.ID)
		}
		if _, ok := required[p.ID]; ok {
			required[p.ID] = true
		}
	}
	for id, present := range required {
		if !present {
			t.Errorf("required preset %q missing", id)
		}
	}
}

// TestFeaturedPresetsRequestURL：精选预设 zhipu/doubao/google 的 base_url 与
// provider.JoinAPIPath 拼出的完整请求 URL——供应商 API 前缀不得被插入 /v1
// （智谱 /api/paas/v4、豆包 /api/v3、Google /v1beta/openai）。
func TestFeaturedPresetsRequestURL(t *testing.T) {
	cases := []struct{ id, suffix, want string }{
		{"zhipu", "/chat/completions", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"doubao", "/chat/completions", "https://ark.cn-beijing.volces.com/api/v3/chat/completions"},
		{"google", "/chat/completions", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
	}
	for _, c := range cases {
		p, ok := ById(c.id)
		if !ok {
			t.Fatalf("preset %s missing", c.id)
		}
		got, err := provider.JoinAPIPath(p.BaseURL, c.suffix)
		if err != nil {
			t.Fatalf("JoinAPIPath(%s base=%q): %v", c.id, p.BaseURL, err)
		}
		if got != c.want {
			t.Errorf("preset %s request URL=%q want %q", c.id, got, c.want)
		}
	}
}

// TestVersionedPathPresetsRequestURL：命名空间前缀类预设（千问/Groq/OpenRouter）
// base_url 以 /v1 结尾，拼出完整 /v1/chat/completions。
func TestVersionedPathPresetsRequestURL(t *testing.T) {
	cases := []struct{ id, want string }{
		{"qwen", "https://dashscope.aliyuncs.com/compatible-mode/v1/chat/completions"},
		{"groq", "https://api.groq.com/openai/v1/chat/completions"},
		{"openrouter", "https://openrouter.ai/api/v1/chat/completions"},
	}
	for _, c := range cases {
		p, ok := ById(c.id)
		if !ok {
			t.Fatalf("preset %s missing", c.id)
		}
		got, err := provider.JoinAPIPath(p.BaseURL, "/chat/completions")
		if err != nil {
			t.Fatalf("JoinAPIPath(%s base=%q): %v", c.id, p.BaseURL, err)
		}
		if got != c.want {
			t.Errorf("preset %s request URL=%q want %q", c.id, got, c.want)
		}
	}
}

// TestPresetByID 按 ID 查找命中与未命中。
func TestPresetByID(t *testing.T) {
	p, ok := ById("anthropic")
	if !ok || p.ID != "anthropic" || p.Protocol != "anthropic" {
		t.Fatalf("ById(anthropic) = %+v, %v", p, ok)
	}
	if _, ok := ById("no-such-preset"); ok {
		t.Error("ById(no-such-preset) ok=true, want false")
	}
}
