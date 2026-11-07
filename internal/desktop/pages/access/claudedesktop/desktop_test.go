package claudedesktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/desktop/uitest"
	"AgentSwitch/internal/storage"
)

// TestDesktopFormDocRoundTrip：表单行 ↔ 角色映射文档双向转换（UI 层唯一
// 业务逻辑；空角色不落键、oneM 只在启用时生效）。
func TestDesktopFormDocRoundTrip(t *testing.T) {
	f := state.DesktopForm{Models: [4]string{"my-code", "", "mimo", ""}, OneM: [4]bool{true, false, false, false}}
	raw, err := desktopDocFromForm(&f)
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{}
	desktopFormFromDoc(st, raw)
	if st.Access.DesktopForm.Models[0] != "my-code" || !st.Access.DesktopForm.OneM[0] {
		t.Fatalf("sonnet 回填: %+v", st.Access.DesktopForm)
	}
	if st.Access.DesktopForm.Models[1] != "" || st.Access.DesktopForm.Models[3] != "" {
		t.Fatalf("未启用角色不得回填: %+v", st.Access.DesktopForm)
	}
	if st.Access.DesktopForm.Models[2] != "mimo" || st.Access.DesktopForm.OneM[2] {
		t.Fatalf("haiku 回填: %+v", st.Access.DesktopForm)
	}
	// 全空必须拒绝保存语义（docFromForm 报错）。
	empty := state.DesktopForm{}
	if _, err := desktopDocFromForm(&empty); err == nil {
		t.Fatal("全空角色应报错")
	}
}

// TestEditorCardShowsProfilePreview：Desktop 编辑器渲染只读网关 profile
// 预览区与刷新按钮。
func TestEditorCardShowsProfilePreview(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Access.DesktopForm = state.DesktopForm{Open: true, Models: [4]string{"my-code", "", "", ""}}
	tt := ui.NewTester(func(c *ui.Context) {
		EditorCard(c, c.Theme(), uikit.Softs(c.Theme()), st, bridge.AccessConfigs{A: a}, true)
	}, 1000, 1200)
	for _, want := range []string{"将写入的网关 profile", "刷新预览"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

// TestConfigRowDeleteViaMenu（2026-11-07 组件升级）：「更多」为主题化菜单——
// 选中「删除」即武装确认且菜单随选关闭（区别于旧展开行常开）。
func TestConfigRowDeleteViaMenu(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := acc.Create("claude_desktop", "桌面", "lm1", `{"sonnet":{"model":"my-code"}}`); err != nil {
		t.Fatal(err)
	}
	v := acc.List("claude_desktop")[0]
	st.Page = "access"
	st.Access.App = "claude_desktop"
	tt := ui.NewTester(func(c *ui.Context) {
		ConfigRow(c, c.Theme(), uikit.Softs(c.Theme()), st, acc, v, false)
	}, 1000, 800)
	if err := tt.Click("更多"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !st.Access.DeleteArm || st.Access.DeleteID != v.ID {
		t.Fatalf("删除应武装确认: arm=%v id=%q", st.Access.DeleteArm, st.Access.DeleteID)
	}
	if tt.HasText("删除") {
		t.Fatalf("菜单应随选中关闭: %v", tt.Texts())
	}
}
