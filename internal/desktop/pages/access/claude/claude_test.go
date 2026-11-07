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

// TestDrawerCardRendersClaudeSections：Claude 接入抽屉渲染名称、主模型下拉
// + 1M、5 角色映射行与只读 settings.json 预览、底部三操作；不出现 Codex
// 专属段落，也不再有可编辑的配置 JSON 文本域（原型 2026-11-07：抽屉只读
// 预览受管键，env 定制交给设置区）。
// （1M 后缀写回语义由 accesskit 的 PatchMainModel/SyncRolesFromDoc 测试覆盖。）
func TestDrawerCardRendersClaudeSections(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	tt := ui.NewTester(func(c *ui.Context) {
		DrawerCard(c, c.Theme(), uikit.Softs(c.Theme()), st, bridge.AccessConfigs{A: a}, true)
	}, 1000, 1400)
	for _, want := range []string{
		"名称:", "逻辑模型:", "1M", "模型映射", "Sonnet", "Opus", "Fable", "Haiku", "Subagent",
		"将写入 ~/.claude/settings.json（只读）", "刷新预览", "保存并应用", "仅保存", "关闭",
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

// TestCloseDrawerKeepsDraft：关闭抽屉保留草稿语义——有未保存修改时 Draft
// 置位（Dirty 仍为真，摘要卡显示「有未保存的接入更改」）；干净草稿关闭
// 不标脏。
func TestCloseDrawerKeepsDraft(t *testing.T) {
	st, _ := uitest.New(t)
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true, Name: "x", ModelLabel: "m", DocJSON: `{"env":{}}`}
	st.Access.Form.Baseline = "claude\x00\x00\x00" // 与内容不同 → 有未保存修改
	CloseDrawer(st)
	if st.Access.Form.Open {
		t.Fatal("close must close the drawer")
	}
	if !st.Access.Form.Draft {
		t.Fatal("dirty close must keep the draft flag")
	}
	if !st.Access.Form.Dirty("claude") {
		t.Fatal("Dirty must count closed-drawer drafts")
	}

	st.Access.Form = state.AccessForm{Open: true, Name: "y"}
	st.Access.Form.Baseline = st.Access.Form.Signature("claude")
	CloseDrawer(st)
	if st.Access.Form.Draft || st.Access.Form.Dirty("claude") {
		t.Fatal("clean close must not mark a draft")
	}
}
