package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/storage"
)

// TestRootSmokeAllPages 覆盖组合根分发：四个侧栏页各自渲染关键内容
// （分包重构后的接线冒烟）。
func TestRootSmokeAllPages(t *testing.T) {
	u, _ := testUI(t)
	cases := map[string][]string{
		"logical":  {"新建逻辑模型"},
		"channels": {"添加渠道"},
		"access":   {"Claude Code 接入"},
		"settings": {"保存设置"},
	}
	for page, wants := range cases {
		u.Page = page
		tt := ui.NewTester(u.view, 900, 700)
		for _, want := range wants {
			if !tt.HasText(want) {
				t.Fatalf("page %s missing %q in %v", page, want, tt.Texts())
			}
		}
	}
}

// TestRootSmokeAccessTabs 覆盖接入页四个页签的分发：Claude Code / Codex 的
// 编辑器段落、Claude Desktop 的角色映射与连接指南卡片。
func TestRootSmokeAccessTabs(t *testing.T) {
	u, a := testUI(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	u.Page = "access"

	// Claude Code：默认页签 + 编辑器（模型映射段）。
	u.Access.App = "claude"
	u.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	if tt := ui.NewTester(u.view, 1000, 1200); !tt.HasText("模型映射") {
		t.Fatalf("claude tab missing 模型映射: %v", tt.Texts())
	}

	// Codex：模型列表编辑器段（首项默认 + 元数据编辑）。
	u.Access.App = "codex"
	u.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	if tt := ui.NewTester(u.view, 1000, 1400); !tt.HasText("模型列表") || !tt.HasText("模型元数据") {
		t.Fatalf("codex tab missing sections: %v", tt.Texts())
	}

	// Claude Desktop：角色映射表单。
	u.Access.App = "claude_desktop"
	u.Access.Form = state.AccessForm{}
	u.Access.DesktopForm = state.DesktopForm{Open: true}
	if tt := ui.NewTester(u.view, 1000, 900); !tt.HasText("角色映射") {
		t.Fatalf("desktop tab missing 角色映射: %v", tt.Texts())
	}

	// 连接指南：通用连接信息卡（先关闭编辑器，模拟从列表切页签）。
	u.Access.DesktopForm = state.DesktopForm{}
	u.Access.Form = state.AccessForm{}
	u.Access.App = "guide"
	if tt := ui.NewTester(u.view, 1000, 900); !tt.HasText("通用连接信息") {
		t.Fatalf("guide tab missing 通用连接信息: %v", tt.Texts())
	}
}
