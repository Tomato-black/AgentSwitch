package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/state"
)

// TestAccessPageRenders 覆盖接入页骨架：两 tab、全局配置卡、实际配置卡、
// 空态引导与网关未运行提示在场；旧片段页元素（当前逻辑模型选择器）不再出现。
func TestAccessPageRenders(t *testing.T) {
	u, _ := testUI(t)
	u.Page = "access"
	tt := ui.NewTester(u.view, 900, 600)
	for _, want := range []string{"Claude Code", "Codex", "全局配置", "实际配置", "还没有配置", "网关未运行"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("当前逻辑模型") {
		t.Fatalf("旧片段页元素泄漏: %v", tt.Texts())
	}
}

// TestAccessCodexPageMentionsDesktop 覆盖 Codex 页签的桌面版说明：Codex CLI /
// 桌面版 / IDE 插件共用同一份 ~/.codex/config.toml，应用后需完全退出重启。
func TestAccessCodexPageMentionsDesktop(t *testing.T) {
	u, _ := testUI(t)
	u.Page = "access"
	u.Access.App = "codex"
	tt := ui.NewTester(u.view, 900, 600)
	for _, want := range []string{"桌面版", "~/.codex/config.toml", "完全退出", "模型选择器", "新建对话"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

// TestAccessPageScrollsWhenEditorOpen 回归：编辑器打开后接入页内容超出视口，
// 页面必须可纵向滚动到底部的保存按钮（此前缺 Scroll 容器，底部内容不可达）。
func TestAccessPageScrollsWhenEditorOpen(t *testing.T) {
	u, _ := testUI(t)
	u.Page = "access"
	u.Access.Form = state.AccessForm{Open: true}
	tt := ui.NewTester(u.view, 900, 600)
	before, ok := tt.Find("保存")
	if !ok {
		t.Fatalf("编辑器卡未渲染：%v", tt.Texts())
	}
	if before.Y < 600 {
		t.Fatalf("前置条件不成立：保存按钮已在视口内 y=%v", before.Y)
	}
	tt.Scroll(450, 300, 0, 1000)
	after, ok := tt.Find("保存")
	if !ok {
		t.Fatal("滚动后保存按钮从标签树消失")
	}
	if after.Y >= 600 {
		t.Fatalf("接入页不可滚动：保存按钮滚动前后 y=%v → %v", before.Y, after.Y)
	}
}
