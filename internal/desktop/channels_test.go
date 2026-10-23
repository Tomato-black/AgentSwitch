// channels_test.go 是渠道页纯函数测试（Task 14）：预设回填、表单校验、
// 删除引用警告。渲染薄壳不测字符串细节（同 logicalmodels 的纯函数口径）。
package desktop

import (
	"strings"
	"testing"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/presets"
	"AgentSwitch/internal/telemetry"
)

func TestChannelStats(t *testing.T) {
	recs := []telemetry.RequestRecord{{Channel: "main", Status: 200}, {Channel: "main", Status: 500}, {Channel: "other", Status: 200}}
	ok, fail := channelStats(recs, "main")
	if ok != 1 || fail != 1 {
		t.Fatalf("%d %d", ok, fail)
	}
}

// TestPresetPreselectFillsForm 覆盖预设网格点选回填：双协议预置写入
// presetID/第一可用协议（openai 优先）/base_url/建议模型，且不冲掉用户已填的名称。
func TestPresetPreselectFillsForm(t *testing.T) {
	qwen := presets.ChannelPreset{
		ID: "qwen", DisplayName: "阿里千问",
		Protocols: map[string]presets.ProtocolEndpoint{
			"openai":     {BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", DefaultModels: []string{"qwen3.8-max", "qwen3.8-flash"}},
			"anthropic":  {BaseURL: "https://dashscope.aliyuncs.com/apps/anthropic", DefaultModels: []string{"qwen3.8-max"}},
		},
	}
	f := channelForm{name: "我的渠道"}
	applyPreset(&f, qwen)
	if f.presetID != "qwen" {
		t.Fatalf("presetID = %q, want qwen", f.presetID)
	}
	if f.protocol != "openai" {
		t.Fatalf("protocol = %q, want openai", f.protocol)
	}
	if f.baseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("baseURL = %q", f.baseURL)
	}
	if !strings.Contains(f.suggestions, "qwen3.8-max") {
		t.Fatalf("suggestions = %q, want qwen3.8-max", f.suggestions)
	}
	if f.name != "我的渠道" {
		t.Fatalf("applyPreset must not clobber name, got %q", f.name)
	}
}

// TestChannelFormValidate 覆盖缺 name/key/base_url 的错误信息；详情态
// （editID 非空）Key 留空=不改，不拦。
func TestChannelFormValidate(t *testing.T) {
	f := channelForm{}
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "名称") {
		t.Fatalf("missing name: %v", err)
	}
	f.name = "n"
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "Key") {
		t.Fatalf("missing key: %v", err)
	}
	f.apiKey = "sk-1"
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "Base URL") {
		t.Fatalf("missing base_url: %v", err)
	}
	f.baseURL = "https://api.example.com"
	if err := f.validate(); err != nil {
		t.Fatalf("complete form: %v", err)
	}
	f2 := channelForm{editID: "c1", name: "n", baseURL: "https://api.example.com"}
	if err := f2.validate(); err != nil {
		t.Fatalf("edit keep-key must pass: %v", err)
	}
}

// TestDeleteWarnsWhenReferenced 覆盖删除引用警告：referencedBy 列出
// 目标链引用该渠道的逻辑模型名，无引用返回空。
func TestDeleteWarnsWhenReferenced(t *testing.T) {
	models := []bridge.ModelView{
		{Name: "my-code", Targets: []bridge.TargetView{{ChannelID: "c1"}, {ChannelID: "c2"}}},
		{Name: "chat", Targets: []bridge.TargetView{{ChannelID: "c2"}}},
	}
	got := referencedBy(models, "c1")
	if len(got) != 1 || got[0] != "my-code" {
		t.Fatalf("referencedBy(c1) = %v, want [my-code]", got)
	}
	if got := referencedBy(models, "nope"); len(got) != 0 {
		t.Fatalf("referencedBy(nope) = %v, want empty", got)
	}
}

// TestApplyPresetDualProtocol 覆盖 spec §5 完整语义：第一可用协议回填
// （openai 优先；仅 anthropic 时落 anthropic）+ name 空时回填 DisplayName、
// 已填不覆盖。
func TestApplyPresetDualProtocol(t *testing.T) {
	dual := presets.ChannelPreset{
		ID: "qwen", DisplayName: "阿里千问",
		Protocols: map[string]presets.ProtocolEndpoint{
			"openai":    {BaseURL: "https://o.example/v1", DefaultModels: []string{"m1"}},
			"anthropic": {BaseURL: "https://a.example", DefaultModels: []string{"m2"}},
		},
	}
	anthropicOnly := presets.ChannelPreset{
		ID: "x", DisplayName: "仅Claude协议",
		Protocols: map[string]presets.ProtocolEndpoint{
			"anthropic": {BaseURL: "https://x.example", DefaultModels: []string{"m3"}},
		},
	}
	f := channelForm{}
	applyPreset(&f, dual)
	if f.protocol != "openai" || f.baseURL != "https://o.example/v1" || f.suggestions != "m1" {
		t.Fatalf("dual → %+v", f)
	}
	if f.name != "阿里千问" {
		t.Fatalf("空 name 应回填 DisplayName, got %q", f.name)
	}
	f2 := channelForm{}
	applyPreset(&f2, anthropicOnly)
	if f2.protocol != "anthropic" || f2.baseURL != "https://x.example" || f2.suggestions != "m3" {
		t.Fatalf("anthropicOnly → %+v", f2)
	}
	f3 := channelForm{name: "我的中转"}
	applyPreset(&f3, dual)
	if f3.name != "我的中转" {
		t.Fatalf("已填 name 不得覆盖, got %q", f3.name)
	}
}
