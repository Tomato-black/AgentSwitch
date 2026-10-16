package desktop

import (
	"path/filepath"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/credential"
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
		// 渠道页测试不得探测/写真实钥匙环（裁定 2 同款）：密钥落临时文件，
		// KeychainAvailable 恒 true —— allowFallback=false 的导入路径可离线
		// 确定地走 "os-keychain" 分支（生产 Run() 仍注入 a.Creds()）。
		Channels: bridge.Channels{A: a, Creds: &credential.Manager{
			Primary:       credential.NewFallbackFile(filepath.Join(t.TempDir(), "ch-keys.json")),
			AllowFallback: true,
		}, KeychainAvailable: func() bool { return true }},
	}
	return newUIState(svc, a), a
}

func TestOverviewShowsState(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("已停止") {
		t.Fatalf("texts %v", tt.Texts())
	}
}

// TestUnknownPageFallsBackToOverview 覆盖分发默认分支：未识别的 u.page
// 落回概览（边栏 id 与 u.page 集合一致性的兜底）。
func TestUnknownPageFallsBackToOverview(t *testing.T) {
	u, _ := testUI(t)
	u.page = "bogus"
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("访问令牌") {
		t.Fatalf("unknown page must fall back to overview: %v", tt.Texts())
	}
}

// TestCloseNoticeBlocksFirstClose 覆盖首关教育提示（I4）：提示必须在**所有页**
// 可见（原只在 overviewView 渲染——停在供应商/账号/模型页点 X 时提示不出现、
// 拦截却生效，用户不知所措），且点"继续关闭窗口"记录 closeConfirmed 放行关闭。
func TestCloseNoticeBlocksFirstClose(t *testing.T) {
	u, _ := testUI(t)
	u.closeNotice = true
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("驻留托盘") {
		t.Fatalf("notice missing on overview: %v", tt.Texts())
	}
	if err := tt.Click("继续关闭窗口"); err != nil {
		t.Fatal(err)
	}
	if !u.closeConfirmed {
		t.Fatal("confirm not recorded")
	}
	// 非概览页同样必须渲染（提示块挂在 view 导航之后、页面分发之前）。
	u.closeConfirmed = false
	u.page = "providers"
	tt.Frame()
	if !tt.HasText("驻留托盘") {
		t.Fatalf("notice missing on providers page: %v", tt.Texts())
	}
	u.page = "accounts"
	tt.Frame()
	if !tt.HasText("驻留托盘") {
		t.Fatalf("notice missing on accounts page: %v", tt.Texts())
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
