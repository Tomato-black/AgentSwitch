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

// TestSwitchProtocolRules 覆盖 spec §5 协议联动五例：值替换、手改保护、
// 空值恢复、无目标协议清空、建议模型独立手改保护。
func TestSwitchProtocolRules(t *testing.T) {
	dual := presets.ChannelPreset{
		ID: "p",
		Protocols: map[string]presets.ProtocolEndpoint{
			"openai":    {BaseURL: "https://o.example/v1", DefaultModels: []string{"m-o"}},
			"anthropic": {BaseURL: "https://a.example", DefaultModels: []string{"m-a"}},
		},
	}
	// ① 双协议切 anthropic：值替换。
	f := channelForm{protocol: "openai", baseURL: "https://o.example/v1", suggestions: "m-o"}
	switchProtocol(&f, dual, "anthropic")
	if f.baseURL != "https://a.example" || f.suggestions != "m-a" || f.protocol != "anthropic" {
		t.Fatalf("① 切换替换失败: %+v", f)
	}
	// ② 手改保护：baseURL 已人为改为第三值 → 切协议不动。
	f = channelForm{protocol: "openai", baseURL: "https://mine.example", suggestions: "m-o"}
	switchProtocol(&f, dual, "anthropic")
	if f.baseURL != "https://mine.example" {
		t.Fatalf("② 手改保护失败: %q", f.baseURL)
	}
	// 但建议模型仍是预设值 → 应替换（两字段独立判定）。
	if f.suggestions != "m-a" {
		t.Fatalf("② suggestions 应独立替换: %q", f.suggestions)
	}
	// ③ 空值恢复：切到无预设协议清空后切回 openai → 恢复预设。
	f = channelForm{protocol: "anthropic", baseURL: "https://a.example", suggestions: "m-a"}
	switchProtocol(&f, dual, "anthropic") // 先处于 anthropic 预设值
	switchProtocol(&f, dual, "openai")
	if f.baseURL != "https://o.example/v1" || f.suggestions != "m-o" {
		t.Fatalf("③ 往返恢复失败: %+v", f)
	}
	// ④ 仅 openai 预置切 anthropic → 清空（值为预设值时）。
	onlyOpenAI := presets.ChannelPreset{ID: "q", Protocols: map[string]presets.ProtocolEndpoint{
		"openai": {BaseURL: "https://q.example/v1", DefaultModels: []string{"mq"}},
	}}
	f = channelForm{protocol: "openai", baseURL: "https://q.example/v1", suggestions: "mq"}
	switchProtocol(&f, onlyOpenAI, "anthropic")
	if f.baseURL != "" || f.suggestions != "" || f.protocol != "anthropic" {
		t.Fatalf("④ 无目标协议应清空: %+v", f)
	}
	// ④b 清空后切回 openai（空值=未修改）→ 恢复。
	switchProtocol(&f, onlyOpenAI, "openai")
	if f.baseURL != "https://q.example/v1" || f.suggestions != "mq" {
		t.Fatalf("④b 空值恢复失败: %+v", f)
	}
	// ⑤ suggestions 手改后切协议不被覆盖，baseURL（预设值）正常替换。
	f = channelForm{protocol: "openai", baseURL: "https://o.example/v1", suggestions: "my-model"}
	switchProtocol(&f, dual, "anthropic")
	if f.suggestions != "my-model" {
		t.Fatalf("⑤ suggestions 手改保护失败: %q", f.suggestions)
	}
	if f.baseURL != "https://a.example" {
		t.Fatalf("⑤ baseURL 应替换: %q", f.baseURL)
	}
}
