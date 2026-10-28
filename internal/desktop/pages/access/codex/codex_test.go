package codex

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/desktop/uitest"
	"AgentSwitch/internal/storage"
)

// TestAccessSyncCodexFallback：文档无 models 时回落为「绑定模型 + 启用模型」
// （去重、忽略停用项），并回填 codex 高级设置为空。
func TestAccessSyncCodexFallback(t *testing.T) {
	st := &state.State{}
	st.Access.Form = state.AccessForm{DocJSON: `{"env":{}}`, ModelLabel: "my-code"}
	syncFromDoc(st, []bridge.ModelView{
		{Name: "my-code", Enabled: true}, {Name: "test", Enabled: true}, {Name: "off"},
	})
	if len(st.Access.Form.CodexModels) != 2 || st.Access.Form.CodexModels[0] != "my-code" ||
		st.Access.Form.CodexModels[1] != "test" {
		t.Fatalf("%v", st.Access.Form.CodexModels)
	}
	if st.Access.Form.SubagentModel != "" || st.Access.Form.SubagentEffort != "" ||
		st.Access.Form.AutoReviewModel != "" {
		t.Fatalf("codex 高级设置应回填空: %+v", st.Access.Form)
	}
}

// TestAccessSyncCodexFromDocExplicit：文档显式 models/codex 优先；解析失败
// 保留上一合法状态。
func TestAccessSyncCodexFromDocExplicit(t *testing.T) {
	st := &state.State{}
	st.Access.Form = state.AccessForm{
		ModelLabel: "bound",
		DocJSON: `{"models":["a","b"],"codex":{"subagent_model":"b",` +
			`"subagent_reasoning_effort":"high","auto_review_model":"a"}}`,
	}
	syncFromDoc(st, []bridge.ModelView{{Name: "bound", Enabled: true}})
	if len(st.Access.Form.CodexModels) != 2 || st.Access.Form.CodexModels[0] != "a" ||
		st.Access.Form.CodexModels[1] != "b" {
		t.Fatalf("显式列表应优先: %v", st.Access.Form.CodexModels)
	}
	if st.Access.Form.SubagentModel != "b" || st.Access.Form.SubagentEffort != "high" ||
		st.Access.Form.AutoReviewModel != "a" {
		t.Fatalf("高级设置回填: %+v", st.Access.Form)
	}
	// 非法 JSON：保留上一状态
	st.Access.Form.DocJSON = `{bad`
	syncFromDoc(st, nil)
	if len(st.Access.Form.CodexModels) != 2 || st.Access.Form.SubagentModel != "b" {
		t.Fatalf("解析失败应保留旧状态: %+v", st.Access.Form)
	}
}

// TestAccessPatchCodexModels：列表写回文档，且默认模型跟随首行。
func TestAccessPatchCodexModels(t *testing.T) {
	st := &state.State{}
	st.Access.Form = state.AccessForm{DocJSON: `{"env":{}}`, CodexModels: []string{"b", "a"}}
	patchCodexModels(st)
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(st.Access.Form.DocJSON), &doc); err != nil {
		t.Fatal(err)
	}
	got := doc["models"].([]any)
	if len(got) != 2 || got[0] != "b" || got[1] != "a" {
		t.Fatalf("models 写回: %s", st.Access.Form.DocJSON)
	}
	if st.Access.Form.ModelLabel != "b" {
		t.Fatalf("默认模型应跟随首行: %q", st.Access.Form.ModelLabel)
	}
	// 空列表：删除 models 键（保存时回退绑定模型 + 启用模型），默认模型
	// 标签保持不变——空列表不会把表单的绑定模型清空。
	st.Access.Form = state.AccessForm{
		DocJSON: `{"models":["b"],"env":{}}`, ModelLabel: "keep", CodexModels: nil,
	}
	patchCodexModels(st)
	doc = map[string]any{}
	if err := json.Unmarshal([]byte(st.Access.Form.DocJSON), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["models"]; ok {
		t.Fatalf("空列表应删除 Models: %s", st.Access.Form.DocJSON)
	}
	if st.Access.Form.ModelLabel != "keep" {
		t.Fatalf("空列表不应改默认模型: %q", st.Access.Form.ModelLabel)
	}
	// 非法 JSON：补丁失败保留旧文本。
	st.Access.Form = state.AccessForm{DocJSON: `{bad`, ModelLabel: "keep", CodexModels: []string{"x"}}
	patchCodexModels(st)
	if st.Access.Form.DocJSON != `{bad` {
		t.Fatalf("解析失败应保留旧文档: %s", st.Access.Form.DocJSON)
	}
}

// TestAccessPatchCodexSettings：高级设置写回 codex 对象；清空后删除键。
func TestAccessPatchCodexSettings(t *testing.T) {
	st := &state.State{}
	st.Access.Form = state.AccessForm{
		DocJSON:        `{"models":["m"]}`,
		SubagentModel:  "m",
		SubagentEffort: "low",
	}
	patchCodexSettings(st)
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(st.Access.Form.DocJSON), &doc); err != nil {
		t.Fatal(err)
	}
	codex, ok := doc["codex"].(map[string]any)
	if !ok || codex["subagent_model"] != "m" || codex["subagent_reasoning_effort"] != "low" {
		t.Fatalf("高级设置写回: %s", st.Access.Form.DocJSON)
	}
	st.Access.Form.SubagentModel = ""
	st.Access.Form.SubagentEffort = ""
	patchCodexSettings(st)
	doc = map[string]any{}
	json.Unmarshal([]byte(st.Access.Form.DocJSON), &doc)
	if _, ok := doc["codex"]; ok {
		t.Fatalf("全空应删除 codex: %s", st.Access.Form.DocJSON)
	}
	if _, ok := doc["models"]; !ok {
		t.Fatalf("models 应保留: %s", st.Access.Form.DocJSON)
	}
	// 非法 JSON：补丁失败保留旧文本。
	st.Access.Form = state.AccessForm{DocJSON: `{bad`, SubagentModel: "m"}
	patchCodexSettings(st)
	if st.Access.Form.DocJSON != `{bad` {
		t.Fatalf("解析失败应保留旧文档: %s", st.Access.Form.DocJSON)
	}
}

// TestAccessCodexNextModel：添加模型取第一个未在列表中的逻辑模型；全部
// 已在列表时不重复添加。
func TestAccessCodexNextModel(t *testing.T) {
	next, ok := nextModel([]string{"a", "b", "c"}, []string{"b"})
	if !ok || next != "a" {
		t.Fatalf("应取第一个未使用项: %q %v", next, ok)
	}
	next, ok = nextModel([]string{"a", "b"}, []string{"a"})
	if !ok || next != "b" {
		t.Fatalf("应跳过已用项: %q %v", next, ok)
	}
	if _, ok := nextModel([]string{"a"}, []string{"a"}); ok {
		t.Fatal("全部已用不应重复添加")
	}
}

// TestAccessMoveCodexModel：上移/下移换位；首行上移、末行下移与越界都
// no-op（与 moveTarget 同语义）。
func TestAccessMoveCodexModel(t *testing.T) {
	ms := []string{"a", "b", "c"}
	moveModel(ms, 1, -1)
	if ms[0] != "b" || ms[1] != "a" {
		t.Fatal(ms)
	}
	moveModel(ms, 0, -1) // 边界：首行上移不动
	moveModel(ms, 2, +1) // 边界：末行下移不动
	moveModel(ms, -1, +1)
	moveModel(ms, 3, -1)
	if ms[0] != "b" || ms[1] != "a" || ms[2] != "c" {
		t.Fatal(ms)
	}
}

// TestAccessCodexModelMetaLines：只读元数据预览按 codexModels 顺序渲染，
// 首行标「默认」，含显示名/上下文窗口/思考等级（含默认等级）；模型不存在
// 时标注「（模型不存在）」。
func TestAccessCodexModelMetaLines(t *testing.T) {
	models := []bridge.ModelView{
		{Name: "my-code", DisplayName: "My Code", ContextWindow: 1000000,
			ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high"},
		{Name: "test", Enabled: true},
	}
	lines := modelMetaLines([]string{"my-code", "test", "gone"}, models)
	if len(lines) != 3 {
		t.Fatalf("%v", lines)
	}
	if !strings.Contains(lines[0], "My Code") || !strings.Contains(lines[0], "1000000") ||
		!strings.Contains(lines[0], "low/high") || !strings.Contains(lines[0], "high") {
		t.Fatalf("%v", lines[0])
	}
	if !strings.Contains(lines[2], "模型不存在") {
		t.Fatalf("%v", lines[2])
	}
}

// TestAccessCodexEditorShowsModelList：Codex 编辑器不再显示单一「逻辑模型」
// 下拉，改为模型列表（首行「默认」）。
func TestAccessCodexEditorShowsModelList(t *testing.T) {
	st, a := uitest.New(t)
	a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	st.Page = "access"
	st.Access.App = "codex"
	st.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	tt := ui.NewTester(func(c *ui.Context) {
		EditorCard(c, c.Theme(), uikit.Softs(c.Theme()), st, bridge.AccessConfigs{A: a}, true)
	}, 1000, 900)
	for _, want := range []string{"默认", "模型列表"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("逻辑模型:") {
		t.Fatalf("Codex 编辑器不应再显示单一逻辑模型下拉: %v", tt.Texts())
	}
}

// TestAccessCodexEditorAddModelClick：「＋ 添加模型」点击后追加下一个可用
// 逻辑模型到列表末尾并写回文档 models（列表增长即可见，整帧重建后不丢）。
func TestAccessCodexEditorAddModelClick(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	template := `{"models":[{"slug":"gpt-5","base_instructions":"test",` +
		`"model_messages":{"instructions_template":"test"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "cc-switch-model-catalog.json"), []byte(template), 0o600); err != nil {
		t.Fatal(err)
	}
	st, a := uitest.New(t)
	for _, lm := range []storage.LogicalModel{
		{ID: "lm1", Name: "my-code", Enabled: true},
		{ID: "lm2", Name: "extra", Enabled: true},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	st.Page = "access"
	st.Access.App = "codex"
	st.Access.Form = state.AccessForm{
		Open: true, ModelLabel: "my-code", CodexModels: []string{"my-code"},
		DocJSON: `{"models":["my-code"],"env":{}}`,
	}
	tt := ui.NewTester(func(c *ui.Context) {
		EditorCard(c, c.Theme(), uikit.Softs(c.Theme()), st, bridge.AccessConfigs{A: a}, true)
	}, 1200, 4000)
	if err := tt.Click("＋ 添加模型"); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(st.Access.Form.CodexModels, []string{"my-code", "extra"}) {
		t.Fatalf("点击后应追加模型: %v", st.Access.Form.CodexModels)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(st.Access.Form.DocJSON), &doc); err != nil {
		t.Fatal(err)
	}
	models, ok := doc["models"].([]any)
	if !ok || len(models) != 2 || models[1] != "extra" {
		t.Fatalf("models 未写回文档: %s", st.Access.Form.DocJSON)
	}
}

// TestAccessCodexEditorShowsAdvancedSettings：Codex 编辑器渲染高级设置三项
// 下拉（子代理模型 / 子代理推理强度 / 自动审批模型）。
func TestAccessCodexEditorShowsAdvancedSettings(t *testing.T) {
	st, a := uitest.New(t)
	a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	st.Page = "access"
	st.Access.App = "codex"
	st.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	tt := ui.NewTester(func(c *ui.Context) {
		EditorCard(c, c.Theme(), uikit.Softs(c.Theme()), st, bridge.AccessConfigs{A: a}, true)
	}, 1000, 900)
	for _, want := range []string{"子代理模型", "子代理推理强度", "自动审批模型"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

// TestAccessCodexAdvancedMappingRoundTrip：高级设置「空值 ↔ 默认显示项」
// 双向映射；模型下拉以 codexModels 为选项，并保留列表外的旧引用。
func TestAccessCodexAdvancedMappingRoundTrip(t *testing.T) {
	defaults := []string{accessCodexSubagentFollow, accessCodexEffortDefault, accessCodexAutoReviewDefault}
	for _, d := range defaults {
		if got := selectLabel("", d); got != d {
			t.Fatalf("空值应显示 %q，得到 %q", d, got)
		}
		if got := selectRaw(d, d); got != "" {
			t.Fatalf("默认项应映射为空串，得到 %q", got)
		}
		if got := selectLabel("high", d); got != "high" {
			t.Fatalf("非空值应原样显示，得到 %q", got)
		}
		if got := selectRaw("high", d); got != "high" {
			t.Fatalf("非默认项应原样回写，得到 %q", got)
		}
	}
	opts := modelOptions(accessCodexSubagentFollow, []string{"a", "b"}, "gone")
	want := []string{accessCodexSubagentFollow, "a", "b", "gone"}
	if !slices.Equal(opts, want) {
		t.Fatalf("模型选项应为首项 + codexModels + 列表外旧引用: %v", opts)
	}
	efforts := effortOptions("high")
	wantEfforts := []string{accessCodexEffortDefault, "low", "medium", "high", "xhigh"}
	if !slices.Equal(efforts, wantEfforts) {
		t.Fatalf("强度选项不应重复当前项: %v", efforts)
	}
	if got := effortOptions("turbo"); !slices.Contains(got, "turbo") {
		t.Fatalf("未知旧值应补进选项避免显示空值: %v", got)
	}
}

// TestAccessCodexModelListSingleRowHidesDelete：删除会让列表为空，只剩一行
// 时不渲染行删除按钮（至少保留一个模型）。
func TestAccessCodexModelListSingleRowHidesDelete(t *testing.T) {
	st, a := uitest.New(t)
	a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	st.Page = "access"
	st.Access.App = "codex"
	st.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	tt := ui.NewTester(func(c *ui.Context) {
		EditorCard(c, c.Theme(), uikit.Softs(c.Theme()), st, bridge.AccessConfigs{A: a}, true)
	}, 1000, 900)
	if tt.HasText("删除") {
		t.Fatalf("单行不应渲染删除按钮: %v", tt.Texts())
	}
	if !tt.HasText("＋ 添加模型") {
		t.Fatalf("缺少添加按钮: %v", tt.Texts())
	}
}

// TestAccessCodexPreviewRendersManagedKeys 覆盖 Codex 编辑器只读 config.toml
// 预览（spec 2026-10-27 §4.6）：按 codexModels[0] 映射逻辑模型后调
// bridge.PreviewCodexConfig，渲染受管键（目录引用/禁用响应存储/投影模型名）；
// 预览路径只读，不写 config.toml 或模型目录。HOME 指向临时目录并放一份
// 本地模板目录，保证 bridge 的兜底模板可离线取到（桌面测试包无法替换
// bridge 的未导出模板加载器）。
func TestAccessCodexPreviewRendersManagedKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	template := `{"models":[{"slug":"gpt-5","base_instructions":"test",` +
		`"model_messages":{"instructions_template":"test"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "cc-switch-model-catalog.json"), []byte(template), 0o600); err != nil {
		t.Fatal(err)
	}
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Page = "access"
	st.Access.App = "codex"
	st.Access.Form = state.AccessForm{
		Open: true, ModelLabel: "my-code", CodexModels: []string{"my-code"},
		DocJSON: `{"env":{},"models":["my-code"]}`,
	}
	RefreshPreview(st, bridge.AccessConfigs{A: a})
	if st.Access.Form.Msg != "" {
		t.Fatalf("预览应成功: %q", st.Access.Form.Msg)
	}
	for _, want := range []string{"model_catalog_json", "disable_response_storage", "my-code"} {
		if !strings.Contains(st.Access.Form.CodexPreview, want) {
			t.Fatalf("预览缺少 %q：%s", want, st.Access.Form.CodexPreview)
		}
	}
	for _, name := range []string{"config.toml", "agentswitch-model-catalog.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("预览不应写 %s", name)
		}
	}
	// 无默认模型 → 中文内联提示，不调 bridge。
	st.Access.Form = state.AccessForm{Open: true}
	RefreshPreview(st, bridge.AccessConfigs{A: a})
	if st.Access.Form.CodexPreview != "" || !strings.Contains(st.Access.Form.Msg, "模型") {
		t.Fatalf("空模型列表应内联中文提示: %+v", st.Access.Form)
	}
}
