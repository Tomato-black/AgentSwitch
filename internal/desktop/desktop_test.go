package desktop

import (
	"path/filepath"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
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
