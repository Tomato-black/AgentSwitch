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
		Gateway: bridge.Gateway{A: a},
		// 渠道页测试不得探测/写真实钥匙环（裁定 2）：密钥落临时文件，
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
// 可见（提示块挂在 view 层——停在任意页点 X 时提示都出现、拦截都生效），
// 且点"继续关闭窗口"记录 closeConfirmed 放行关闭。
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
	for _, page := range []string{"channels", "models"} {
		u.closeConfirmed = false
		u.page = page
		tt.Frame()
		if !tt.HasText("驻留托盘") {
			t.Fatalf("notice missing on %s page: %v", page, tt.Texts())
		}
	}
}

// TestNavigationSwitchesPage 覆盖边栏导航：点「渠道」切到渠道页（概览内容
// 退场），点「概览」切回——边栏三页（概览/渠道/模型）的分发主干。
func TestNavigationSwitchesPage(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 900, 600)
	if !tt.HasText("概览") || !tt.HasText("渠道") || !tt.HasText("模型") {
		t.Fatalf("nav items missing: %v", tt.Texts())
	}
	if err := tt.Click("渠道"); err != nil {
		t.Fatal(err)
	}
	if u.page != "channels" {
		t.Fatalf("page = %q, want channels", u.page)
	}
	if tt.HasText("访问令牌") {
		t.Fatalf("overview leaked into channels page: %v", tt.Texts())
	}
	if err := tt.Click("概览"); err != nil {
		t.Fatal(err)
	}
	if u.page != "overview" {
		t.Fatalf("page = %q, want overview", u.page)
	}
	if !tt.HasText("访问令牌") {
		t.Fatalf("overview view missing: %v", tt.Texts())
	}
}

func TestNavSelMapping(t *testing.T) {
	u, _ := testUI(t)
	for page, want := range map[string]string{
		"overview": "overview", "channels": "channels", "models": "models",
		"channel-detail": "channels", "channel-new": "channels",
	} {
		u.page = page
		if got := u.navSel(); got != want {
			t.Fatalf("navSel(%q)=%q want %q", page, got, want)
		}
	}
}
func TestSidebarSwitchesPages(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 1080, 720)
	if err := tt.Click("渠道"); err != nil {
		t.Fatal(err)
	}
	if u.page != "channels" {
		t.Fatalf("page %q", u.page)
	}
	if err := tt.Click("概览"); err != nil {
		t.Fatal(err)
	}
	if u.page != "overview" {
		t.Fatalf("page %q", u.page)
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
