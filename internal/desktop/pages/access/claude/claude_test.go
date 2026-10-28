package claude

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/desktop/uitest"
	"AgentSwitch/internal/storage"
)

// TestEditorCardRendersClaudeSections：Claude 编辑器渲染主模型下拉 + 1M 与
// 5 角色映射行（Sonnet/Opus/Fable/Haiku/Subagent）；不出现 Codex 专属段落。
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
		"逻辑模型:", "1M", "模型映射", "Sonnet", "Opus", "Fable", "Haiku", "Subagent",
		"配置 JSON", "将写入 ~/.claude/settings.json", "刷新预览",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{"模型列表", "模型元数据", "子代理模型"} {
		if tt.HasText(unwanted) {
			t.Fatalf("Codex 段落泄漏进 Claude 编辑器: %q in %v", unwanted, tt.Texts())
		}
	}
}
