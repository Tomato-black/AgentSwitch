package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/state"
)

// TestNavItemSet 覆盖侧栏导航项集合：恰为四页 ID 且有序（logical →
// channels → access → settings），与 uiState.page 的分发 id 一一对应
// （spec §6.1 导航骨架）。
func TestNavItemSet(t *testing.T) {
	want := []string{"logical", "channels", "access", "settings"}
	if len(navItems) != len(want) {
		t.Fatalf("nav items = %d, want %d", len(navItems), len(want))
	}
	for i, w := range want {
		if navItems[i].id != w {
			t.Fatalf("navItems[%d].id = %q, want %q", i, navItems[i].id, w)
		}
	}
}

// TestShellFullHeightLayout 覆盖 cc-switch 式全高铺满（截图回归）：侧栏
// 品牌行贴顶、底条状态贴底、导航/状态在左侧固定宽内、内容列在右侧——
// 根 Row 必须 Fill() 而非仅 Grow(1)，否则整窗内容自然高居中悬空。
func TestShellFullHeightLayout(t *testing.T) {
	u, _ := testUI(t)
	const winW, winH = 900, 640
	tt := ui.NewTester(u.view, winW, winH)
	brand, ok := tt.Find("AgentSwitch")
	if !ok {
		t.Fatalf("brand row missing: %v", tt.Texts())
	}
	// 品牌行贴顶（padding 后仍在顶部 40px 内）。
	if brand.Y > 40 {
		t.Fatalf("brand row not near top: Y=%v", brand.Y)
	}
	st, ok := tt.Find("已停止")
	if !ok {
		t.Fatalf("status text missing: %v", tt.Texts())
	}
	// 底条状态贴底（距底 < 120px）且在侧栏列内（X < 200）。
	if st.Y+st.H < winH-120 {
		t.Fatalf("status bar not near bottom: Y=%v H=%v (winH=%d)", st.Y, st.H, winH)
	}
	if st.X >= 200 {
		t.Fatalf("status bar not in sidebar column: X=%v", st.X)
	}
	// 内容列（新建按钮）必须在侧栏右侧，而不是被侧栏挤到中间。
	newBtn, ok := tt.Find("＋ 新建逻辑模型")
	if !ok {
		t.Fatalf("new-model button missing: %v", tt.Texts())
	}
	if newBtn.X < 200 {
		t.Fatalf("content column overlapping sidebar: X=%v", newBtn.X)
	}
}

// TestPageDefaultLogical 覆盖零值归一：uiState{} 的 page 零值是 ""，
// 渲染/导航处归一为 "logical"（默认页 = 逻辑模型列表，spec §6.1）。
func TestPageDefaultLogical(t *testing.T) {
	u := &uiState{State: &state.State{}}
	if got := u.normalizedPage(); got != "logical" {
		t.Fatalf("normalizedPage() = %q, want logical", got)
	}
	u.Page = "channels"
	if got := u.normalizedPage(); got != "channels" {
		t.Fatalf("normalizedPage() = %q, want channels", got)
	}
}
