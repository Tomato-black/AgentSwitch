// channels_test.go 是渠道页纯函数测试（Task 14）：预设回填、表单校验、
// 删除引用警告。渲染薄壳不测字符串细节（同 logicalmodels 的纯函数口径）。
package channels

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uitest"
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
			"openai":    {BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", DefaultModels: []string{"qwen3.8-max", "qwen3.8-flash"}},
			"anthropic": {BaseURL: "https://dashscope.aliyuncs.com/apps/anthropic", DefaultModels: []string{"qwen3.8-max"}},
		},
	}
	f := state.ChannelForm{Name: "我的渠道"}
	applyPreset(&f, qwen)
	if f.PresetID != "qwen" {
		t.Fatalf("presetID = %q, want qwen", f.PresetID)
	}
	if f.Protocol != "openai" {
		t.Fatalf("protocol = %q, want openai", f.Protocol)
	}
	if f.BaseURL != "https://dashscope.aliyuncs.com/compatible-mode/v1" {
		t.Fatalf("baseURL = %q", f.BaseURL)
	}
	if !strings.Contains(f.Suggestions, "qwen3.8-max") {
		t.Fatalf("suggestions = %q, want qwen3.8-max", f.Suggestions)
	}
	if f.Name != "我的渠道" {
		t.Fatalf("applyPreset must not clobber name, got %q", f.Name)
	}
}

// TestChannelFormValidate 覆盖缺 name/key/base_url 的错误信息；详情态
// （editID 非空）Key 留空=不改，不拦。
func TestChannelFormValidate(t *testing.T) {
	f := state.ChannelForm{}
	if err := validate(f); err == nil || !strings.Contains(err.Error(), "名称") {
		t.Fatalf("missing Name: %v", err)
	}
	f.Name = "n"
	if err := validate(f); err == nil || !strings.Contains(err.Error(), "Key") {
		t.Fatalf("missing key: %v", err)
	}
	f.APIKey = "sk-1"
	if err := validate(f); err == nil || !strings.Contains(err.Error(), "Base URL") {
		t.Fatalf("missing base_url: %v", err)
	}
	f.BaseURL = "https://api.example.com"
	if err := validate(f); err != nil {
		t.Fatalf("complete form: %v", err)
	}
	f2 := state.ChannelForm{EditID: "c1", Name: "n", BaseURL: "https://api.example.com"}
	if err := validate(f2); err != nil {
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
	f := state.ChannelForm{}
	applyPreset(&f, dual)
	if f.Protocol != "openai" || f.BaseURL != "https://o.example/v1" || f.Suggestions != "m1" {
		t.Fatalf("dual → %+v", f)
	}
	if f.Name != "阿里千问" {
		t.Fatalf("空 name 应回填 DisplayName, got %q", f.Name)
	}
	f2 := state.ChannelForm{}
	applyPreset(&f2, anthropicOnly)
	if f2.Protocol != "anthropic" || f2.BaseURL != "https://x.example" || f2.Suggestions != "m3" {
		t.Fatalf("anthropicOnly → %+v", f2)
	}
	f3 := state.ChannelForm{Name: "我的中转"}
	applyPreset(&f3, dual)
	if f3.Name != "我的中转" {
		t.Fatalf("已填 name 不得覆盖, got %q", f3.Name)
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
	f := state.ChannelForm{Protocol: "openai", BaseURL: "https://o.example/v1", Suggestions: "m-o"}
	switchProtocol(&f, dual, "anthropic")
	if f.BaseURL != "https://a.example" || f.Suggestions != "m-a" || f.Protocol != "anthropic" {
		t.Fatalf("① 切换替换失败: %+v", f)
	}
	// ② 手改保护：baseURL 已人为改为第三值 → 切协议不动。
	f = state.ChannelForm{Protocol: "openai", BaseURL: "https://mine.example", Suggestions: "m-o"}
	switchProtocol(&f, dual, "anthropic")
	if f.BaseURL != "https://mine.example" {
		t.Fatalf("② 手改保护失败: %q", f.BaseURL)
	}
	// 但建议模型仍是预设值 → 应替换（两字段独立判定）。
	if f.Suggestions != "m-a" {
		t.Fatalf("② suggestions 应独立替换: %q", f.Suggestions)
	}
	// ③ 空值恢复：切到无预设协议清空后切回 openai → 恢复预设。
	f = state.ChannelForm{Protocol: "anthropic", BaseURL: "https://a.example", Suggestions: "m-a"}
	switchProtocol(&f, dual, "anthropic") // 先处于 anthropic 预设值
	switchProtocol(&f, dual, "openai")
	if f.BaseURL != "https://o.example/v1" || f.Suggestions != "m-o" {
		t.Fatalf("③ 往返恢复失败: %+v", f)
	}
	// ④ 仅 openai 预置切 anthropic → 清空（值为预设值时）。
	onlyOpenAI := presets.ChannelPreset{ID: "q", Protocols: map[string]presets.ProtocolEndpoint{
		"openai": {BaseURL: "https://q.example/v1", DefaultModels: []string{"mq"}},
	}}
	f = state.ChannelForm{Protocol: "openai", BaseURL: "https://q.example/v1", Suggestions: "mq"}
	switchProtocol(&f, onlyOpenAI, "anthropic")
	if f.BaseURL != "" || f.Suggestions != "" || f.Protocol != "anthropic" {
		t.Fatalf("④ 无目标协议应清空: %+v", f)
	}
	// ④b 清空后切回 openai（空值=未修改）→ 恢复。
	switchProtocol(&f, onlyOpenAI, "openai")
	if f.BaseURL != "https://q.example/v1" || f.Suggestions != "mq" {
		t.Fatalf("④b 空值恢复失败: %+v", f)
	}
	// ⑤ suggestions 手改后切协议不被覆盖，baseURL（预设值）正常替换。
	f = state.ChannelForm{Protocol: "openai", BaseURL: "https://o.example/v1", Suggestions: "my-model"}
	switchProtocol(&f, dual, "anthropic")
	if f.Suggestions != "my-model" {
		t.Fatalf("⑤ suggestions 手改保护失败: %q", f.Suggestions)
	}
	if f.BaseURL != "https://a.example" {
		t.Fatalf("⑤ baseURL 应替换: %q", f.BaseURL)
	}
}

// TestDetailProtocolSwitched：详情页切协议联动——baseURL 与建议文本框
// （chSuggText）走 switchProtocol 的手改保护语义，联动结果写回 chSuggText。
// 2026-10-28：协议自创建后可编辑（偏离 2026-10-23 spec §5「创建后不可变」），
// 联动规则与新建页一致。
func TestDetailProtocolSwitched(t *testing.T) {
	dual := presets.ChannelPreset{
		ID: "dual",
		Protocols: map[string]presets.ProtocolEndpoint{
			"openai":    {BaseURL: "https://o.example/v1", DefaultModels: []string{"m-o"}},
			"anthropic": {BaseURL: "https://a.example", DefaultModels: []string{"m-a"}},
		},
	}
	// ① 未手改：baseURL 与建议文本都换到目标协议预设值，SuggText 同步写回。
	st := &state.State{Channels: state.ChannelState{
		Form:     state.ChannelForm{PresetID: "dual", Protocol: "anthropic", BaseURL: "https://o.example/v1"},
		SuggText: "m-o"}}
	detailProtocolSwitched(st, dual)
	if st.Channels.Form.BaseURL != "https://a.example" {
		t.Fatalf("① baseURL 应联动: %q", st.Channels.Form.BaseURL)
	}
	if st.Channels.SuggText != "m-a" {
		t.Fatalf("① 建议文本应联动写回: %q", st.Channels.SuggText)
	}
	// ② 建议文本手改：保留；baseURL 未手改：仍联动（两字段独立判定）。
	st = &state.State{Channels: state.ChannelState{
		Form:     state.ChannelForm{PresetID: "dual", Protocol: "anthropic", BaseURL: "https://o.example/v1"},
		SuggText: "my-model"}}
	detailProtocolSwitched(st, dual)
	if st.Channels.SuggText != "my-model" {
		t.Fatalf("② 手改建议文本应保留: %q", st.Channels.SuggText)
	}
	if st.Channels.Form.BaseURL != "https://a.example" {
		t.Fatalf("② baseURL 应联动: %q", st.Channels.Form.BaseURL)
	}
}

// TestSaveChannelDetailPersistsProtocol：详情页改协议后保存，协议落库。
func TestSaveChannelDetailPersistsProtocol(t *testing.T) {
	st, _ := uitest.New(t)
	v, err := st.Svc.Channels.Create("qwen", "proto", "https://api.example.com/v1", "openai", "sk-proto-99", true, false)
	if err != nil {
		t.Fatal(err)
	}
	st.Page = "channels"
	openDetail(st, v.ID)
	if st.Channels.Form.Protocol != "openai" {
		t.Fatalf("前置条件：回显协议应为 openai, %q", st.Channels.Form.Protocol)
	}
	st.Channels.Form.Protocol = "anthropic" // 模拟下拉切换
	saveDetail(st)
	d, err := st.Svc.Channels.Get(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Protocol != "anthropic" {
		t.Fatalf("保存后协议应为 anthropic, %q", d.Protocol)
	}
	if !strings.Contains(st.Channels.TestMsg, "已保存") {
		t.Fatalf("chTestMsg = %q", st.Channels.TestMsg)
	}
}

// --- 拉取模型（详情页 / 新建表单）---

// TestChannelDetailFetchModelsOverwritesText：详情页点「拉取模型」把建议
// 文本框覆盖为上游返回的 id 列表。
func TestChannelDetailFetchModelsOverwritesText(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"m-a"},{"id":"m-b"}]}`))
	}))
	defer up.Close()
	st, _ := uitest.New(t)
	v, err := st.Svc.Channels.Create("", "fetch", up.URL, "openai", "sk-drag-12345", true, true)
	if err != nil {
		t.Fatal(err)
	}
	st.Page = "channels"
	st.Channels.View = "detail"
	openDetail(st, v.ID)
	if st.Channels.SuggText != "" {
		t.Fatalf("前置条件：详情页建议文本应为空, %q", st.Channels.SuggText)
	}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 900, 700)
	if err := tt.Click("拉取模型"); err != nil {
		t.Fatalf("拉取模型按钮不可点: %v", err)
	}
	if st.Channels.SuggText != "m-a\nm-b" {
		t.Fatalf("chSuggText = %q, want m-a\\nm-b", st.Channels.SuggText)
	}
	if !strings.Contains(st.Channels.TestMsg, "2") {
		t.Fatalf("chTestMsg = %q", st.Channels.TestMsg)
	}
}

// TestChannelDetailFetchModelsErrorKeepsText：无 Key 渠道点「拉取模型」→
// 报错，且不覆盖已有文本。
func TestChannelDetailFetchModelsErrorKeepsText(t *testing.T) {
	st, _ := uitest.New(t)
	v, err := st.Svc.Channels.Create("", "nokey", "https://api.example.com", "openai", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	st.Page = "channels"
	st.Channels.View = "detail"
	openDetail(st, v.ID)
	st.Channels.SuggText = "keep-me"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 900, 700)
	if err := tt.Click("拉取模型"); err != nil {
		t.Fatalf("拉取模型按钮不可点: %v", err)
	}
	if st.Channels.SuggText != "keep-me" {
		t.Fatalf("失败不应覆盖文本, %q", st.Channels.SuggText)
	}
	if !strings.Contains(st.Channels.TestMsg, "失败") && !strings.Contains(st.Channels.TestMsg, "密钥") {
		t.Fatalf("chTestMsg = %q", st.Channels.TestMsg)
	}
}

// TestChannelNewFetchModelsDraft：新建表单点「拉取模型」用表单里的
// baseURL/protocol/key 直接拉取并覆盖建议文本（渠道尚未入库）。
func TestChannelNewFetchModelsDraft(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"data":[{"id":"d-1"},{"id":"d-2"},{"id":"d-3"}]}`))
	}))
	defer up.Close()
	st, _ := uitest.New(t)
	st.Page = "channels"
	st.Channels.View = "new"
	st.Channels.Form = state.ChannelForm{
		Protocol:    "openai",
		BaseURL:     up.URL,
		APIKey:      "sk-draft-999",
		Suggestions: "stale-old-model",
	}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 900, 700)
	if err := tt.Click("拉取模型"); err != nil {
		t.Fatalf("拉取模型按钮不可点: %v", err)
	}
	if st.Channels.Form.Suggestions != "d-1\nd-2\nd-3" {
		t.Fatalf("suggestions = %q", st.Channels.Form.Suggestions)
	}
	if !strings.Contains(st.Channels.Form.Msg, "3") {
		t.Fatalf("chForm.msg = %q", st.Channels.Form.Msg)
	}
}

// TestChannelNewFetchModelsDraftRequiresKey：新建表单缺 Key 时点「拉取模型」
// 报错且不覆盖建议文本。
func TestChannelNewFetchModelsDraftRequiresKey(t *testing.T) {
	st, _ := uitest.New(t)
	st.Page = "channels"
	st.Channels.View = "new"
	st.Channels.Form = state.ChannelForm{
		Protocol:    "openai",
		BaseURL:     "https://api.example.com",
		Suggestions: "keep-draft",
	}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 900, 700)
	if err := tt.Click("拉取模型"); err != nil {
		t.Fatalf("拉取模型按钮不可点: %v", err)
	}
	if st.Channels.Form.Suggestions != "keep-draft" {
		t.Fatalf("缺 Key 不应覆盖建议文本, %q", st.Channels.Form.Suggestions)
	}
	if !strings.Contains(st.Channels.Form.Msg, "Key") {
		t.Fatalf("chForm.msg = %q", st.Channels.Form.Msg)
	}
}
