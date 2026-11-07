package claude

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/desktop/uitest"
	"AgentSwitch/internal/storage"
)

// TestEditorCardRendersClaudeSections：Claude 编辑子页渲染名称、主模型下拉
// + 1M、5 角色映射行、Claude 模型选择与只读 settings.json 预览、底部
// 取消/仅保存/保存并应用；不出现 Codex 专属段落，也不提供可编辑的配置
// JSON 文本域（env 定制交给设置页）。
// （1M 后缀写回语义由 accesskit 的 PatchMainModel/SyncRolesFromDoc 测试覆盖。）
func TestEditorCardRendersClaudeSections(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	tt := ui.NewTester(func(c *ui.Context) {
		EditorCard(c, c.Theme(), uikit.Softs(c.Theme()), st, bridge.AccessConfigs{A: a}, true)
	}, 1000, 1400)
	for _, want := range []string{
		"名称:", "逻辑模型:", "1M", "模型映射", "Sonnet", "Opus", "Fable", "Haiku", "Subagent",
		"将写入 ~/.claude/settings.json（只读）", "刷新预览", "保存并应用", "仅保存", "取消",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{
		"配置 JSON（最终写入 Agent 的完整文档", "模型列表", "模型元数据", "子代理模型",
	} {
		if tt.HasText(unwanted) {
			t.Fatalf("不应出现的段落: %q in %v", unwanted, tt.Texts())
		}
	}
}

// TestEditorModelKeysSection（2026-11-07 模型键归接入）：编辑子页渲染「Claude
// 模型选择」段（主模型输入行 + 回退模型链）；带顶层键的文档回填行状态。
func TestEditorModelKeysSection(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := acc.Create("claude", "工作", "lm1",
		`{"model":"my-code","fallbackModel":["my-code","备份"]}`); err != nil {
		t.Fatal(err)
	}
	v := acc.List("claude")[0]
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true, EditID: v.ID, Name: "工作", ModelLabel: "my-code", DocJSON: v.Settings}
	st.Access.Form.Baseline = st.Access.Form.Signature("claude")
	tt := ui.NewTester(func(c *ui.Context) {
		EditorCard(c, c.Theme(), uikit.Softs(c.Theme()), st, acc, true)
	}, 1000, 1600)
	for _, want := range []string{"Claude 模型选择", "主模型:", "回退模型:", "添加回退", "my-code", "备份"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	if st.Access.Form.ModelKey != "my-code" || len(st.Access.Form.FallbackModels) != 2 {
		t.Fatalf("行状态回填错误: %q %v", st.Access.Form.ModelKey, st.Access.Form.FallbackModels)
	}
}

// TestEditorModelKeyDropdown（2026-11-07 用户指令）：主模型是下拉（含
// 「（跟随网关）」哨兵）；文档带 model 时回显对应选项。
func TestEditorModelKeyDropdown(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := acc.Create("claude", "工作", "lm1", `{"env":{}}`); err != nil {
		t.Fatal(err)
	}
	v := acc.List("claude")[0]
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true, EditID: v.ID, Name: "工作", ModelLabel: "my-code", DocJSON: v.Settings}
	st.Access.Form.Baseline = st.Access.Form.Signature("claude")
	tt := ui.NewTester(func(c *ui.Context) {
		EditorCard(c, c.Theme(), uikit.Softs(c.Theme()), st, acc, true)
	}, 1000, 1400)
	if !tt.HasText("（跟随网关）") {
		t.Fatalf("主模型下拉缺「跟随网关」选项: %v", tt.Texts())
	}
	if !tt.HasText("主模型:") {
		t.Fatalf("缺主模型行: %v", tt.Texts())
	}
}

// TestRoleNameAutofill（2026-11-07 用户指令）：角色显示名称随模型下拉自动
// 填充——选模型后名称=模型名并写文档；手动改过的名称不被覆盖；跟随主模型
// 清空名称。
func TestRoleNameAutofill(t *testing.T) {
	f := &state.AccessForm{Open: true, ModelLabel: "model-a", DocJSON: `{"env":{}}`}
	labels := []string{"model-a", "model-b"}
	// 初始回填（跟随主模型）。
	accesskit.SyncRolesFromDoc(f)
	// Sonnet 选 model-b → 显示名自动填 model-b。
	f.RoleModels[0] = "model-b"
	autofillRoleName(f, 0, labels)
	if f.RoleNames[0] != "model-b" {
		t.Fatalf("自动填充失败: %q", f.RoleNames[0])
	}
	// 手动改名后再换模型 → 不覆盖。
	f.RoleNames[0] = "我的别名"
	f.RoleModels[0] = "model-a"
	autofillRoleName(f, 0, labels)
	if f.RoleNames[0] != "我的别名" {
		t.Fatalf("手动名称被覆盖: %q", f.RoleNames[0])
	}
	// 跟随主模型 → 清空名称（含手动名——跟随时 NAME 键无意义）。
	f.RoleModels[0] = accesskit.FollowModel
	autofillRoleName(f, 0, labels)
	if f.RoleNames[0] != "" {
		t.Fatalf("跟随主模型应清空名称: %q", f.RoleNames[0])
	}
	// 自动填充痕迹（名称恰为某模型名）再换模型 → 覆盖。
	f.RoleModels[0] = "model-a"
	f.RoleNames[0] = "model-b" // 上一次自动填充的痕迹
	autofillRoleName(f, 0, labels)
	if f.RoleNames[0] != "model-a" {
		t.Fatalf("自动痕迹应被覆盖: %q", f.RoleNames[0])
	}
}
