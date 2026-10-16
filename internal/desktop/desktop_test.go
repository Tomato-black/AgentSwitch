package desktop

import (
	"path/filepath"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func testUI(t *testing.T) (*uiState, *app.App) {
	t.Helper()
	a, err := app.Open(app.Options{
		DBPath: filepath.Join(t.TempDir(), "d.db"), LogPath: filepath.Join(t.TempDir(), "d.log"), Host: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	svc := Services{
		Gateway: bridge.Gateway{A: a}, Providers: bridge.Providers{A: a},
		Accounts: bridge.Accounts{A: a, Creds: a.Creds()}, Models: bridge.Models{A: a},
	}
	return newUIState(svc, a), a
}

func TestOverviewShowsState(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("stopped") {
		t.Fatalf("texts %v", tt.Texts())
	}
}

func TestCloseNoticeBlocksFirstClose(t *testing.T) {
	u, _ := testUI(t)
	u.closeNotice = true
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("驻留托盘") {
		t.Fatalf("notice missing: %v", tt.Texts())
	}
	if err := tt.Click("继续关闭窗口"); err != nil {
		t.Fatal(err)
	}
	if !u.closeConfirmed {
		t.Fatal("confirm not recorded")
	}
}

// TestNeedsReopen 覆盖托盘"打开主窗口"的重建判据：无窗口 → 重建；存活
// （未销毁）窗口 → 复用 Restore/Focus。destroyed=true 的一例依赖真实关闭
// 的窗口（destroyed 字段未导出），无头测试无法伪造，由 mygo 的
// Window.IsDestroyed（window.go:597-598）保证，此处只测两条可构造路径。
func TestNeedsReopen(t *testing.T) {
	if !needsReopen(nil) {
		t.Fatal("nil window should reopen")
	}
	if needsReopen(&mygo.Window{}) {
		t.Fatal("live window should be reused")
	}
}
