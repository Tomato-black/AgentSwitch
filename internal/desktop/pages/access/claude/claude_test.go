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
		DrawerCard(c, c.Theme(), uikit.Softs(c.Theme()), st, bridge.AccessConfigs{A: a}, true, DrawerHooks{})
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

// TestDrawerCardConfigManagementRenders：编辑既有配置时抽屉渲染配置选择行
// 与次级操作（复制启动命令/打开终端/备份恢复/删除）；新建草稿不显示
// EditID 相关次级操作。
func TestDrawerCardConfigManagementRenders(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := acc.Create("claude", "工作", "lm1", "{}"); err != nil {
		t.Fatal(err)
	}
	v := acc.List("claude")[0]
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true, EditID: v.ID, Name: "工作", ModelLabel: "my-code", DocJSON: "{}"}
	st.Access.Form.Baseline = st.Access.Form.Signature("claude")
	tt := ui.NewTester(func(c *ui.Context) {
		DrawerCard(c, c.Theme(), uikit.Softs(c.Theme()), st, acc, true, DrawerHooks{})
	}, 1000, 1400)
	for _, want := range []string{"工作", "新建", "复制启动命令", "打开终端", "备份与恢复", "还原最近备份", "删除"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	// 新建草稿：无 EditID，不出现次级操作。
	st.Access.Form = state.AccessForm{Open: true}
	tt = ui.NewTester(func(c *ui.Context) {
		DrawerCard(c, c.Theme(), uikit.Softs(c.Theme()), st, acc, true, DrawerHooks{})
	}, 1000, 1400)
	if tt.HasText("复制启动命令") || tt.HasText("还原最近备份") {
		t.Fatalf("新建草稿不应渲染 EditID 次级操作: %v", tt.Texts())
	}
}

// TestDrawerSelectSwitch：下拉切换配置——干净草稿直接切换；dirty 草稿先武装
// 「放弃未保存的接入修改？」确认，确认后才切换（原型 2026-11-07 §6.2.2）。
func TestDrawerSelectSwitch(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := acc.Create("claude", "工作", "lm1", "{}"); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.Create("claude", "备用", "lm1", "{}"); err != nil {
		t.Fatal(err)
	}
	list := acc.List("claude")
	var workID, otherID string
	for _, v := range list {
		switch v.Name {
		case "工作":
			workID = v.ID
		case "备用":
			otherID = v.ID
		}
	}
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true, EditID: workID, Name: "工作", DocJSON: "{}"}
	st.Access.Form.Baseline = st.Access.Form.Signature("claude")
	var switched string
	hooks := DrawerHooks{SwitchTo: func(id string) { switched = id }}

	// 干净：直接切换。
	drawerSelect(st, acc, hooks, "备用")
	if st.Access.DrawerSwitchArm {
		t.Fatal("clean switch must not arm the confirm")
	}
	if switched != otherID {
		t.Fatalf("clean switch = %q, want %q", switched, otherID)
	}

	// dirty：武装确认，不切换；确认后切换（表单仍编辑「工作」，选「备用」）。
	st.Access.Form.DocJSON = `{"env":{"X":"1"}}` // 与基线不同 → dirty
	switched = ""
	drawerSelect(st, acc, hooks, "备用")
	if !st.Access.DrawerSwitchArm || st.Access.DrawerSwitchID == "" {
		t.Fatalf("dirty switch must arm confirm: arm=%v id=%q", st.Access.DrawerSwitchArm, st.Access.DrawerSwitchID)
	}
	if switched != "" {
		t.Fatal("dirty switch must not switch before confirm")
	}
	confirmDrawerSwitch(st, hooks)
	if st.Access.DrawerSwitchArm {
		t.Fatal("confirm must disarm")
	}
	if st.Access.DrawerSwitchID != "" {
		t.Fatal("confirm must clear the pending id")
	}
	// 「（新建草稿）」等价 NewDraft：dirty 同样先武装确认，确认后才新建。
	newDraft := false
	hooks2 := DrawerHooks{NewDraft: func() { newDraft = true }}
	drawerSelect(st, acc, hooks2, "（新建草稿）")
	if !st.Access.DrawerSwitchArm || newDraft {
		t.Fatal("dirty new-draft must arm confirm and not switch")
	}
	confirmDrawerSwitch(st, hooks2)
	if !newDraft {
		t.Fatal("confirm must apply the new-draft switch")
	}
	// 干净草稿直接新建。
	newDraft = false
	st.Access.Form.Baseline = st.Access.Form.Signature("claude")
	drawerSelect(st, acc, hooks2, "（新建草稿）")
	if !newDraft {
		t.Fatal("clean new-draft must call NewDraft directly")
	}
}
