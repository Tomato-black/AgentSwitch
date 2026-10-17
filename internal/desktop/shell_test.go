package desktop

import "testing"

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

// TestPageDefaultLogical 覆盖零值归一：uiState{} 的 page 零值是 ""，
// 渲染/导航处归一为 "logical"（默认页 = 逻辑模型列表，spec §6.1）。
func TestPageDefaultLogical(t *testing.T) {
	u := &uiState{}
	if got := u.normalizedPage(); got != "logical" {
		t.Fatalf("normalizedPage() = %q, want logical", got)
	}
	u.page = "channels"
	if got := u.normalizedPage(); got != "channels" {
		t.Fatalf("normalizedPage() = %q, want channels", got)
	}
}
