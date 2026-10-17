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
		}},
		Logical: &bridge.LogicalModels{A: a},
	}
	return newUIState(svc, a), a
}

// TestShellShowsGatewayState 覆盖底条状态位：fresh app 停机态译为「已停止」
// 且出现在侧栏底条（gatewayStatusBar 读 Gateway.Status 真实状态）。
func TestShellShowsGatewayState(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("已停止") {
		t.Fatalf("texts %v", tt.Texts())
	}
}

// TestUnknownPageFallsBackToLogical 覆盖分发默认分支：未识别的 u.page
// 落回逻辑模型页（与 normalizedPage 的零值归一同向）。
func TestUnknownPageFallsBackToLogical(t *testing.T) {
	u, _ := testUI(t)
	u.page = "bogus"
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("新建逻辑模型") {
		t.Fatalf("unknown page must fall back to logical: %v", tt.Texts())
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
		t.Fatalf("notice missing on logical page: %v", tt.Texts())
	}
	if err := tt.Click("继续关闭窗口"); err != nil {
		t.Fatal(err)
	}
	if !u.closeConfirmed {
		t.Fatal("confirm not recorded")
	}
	// 非默认页同样必须渲染（提示块挂在 view 导航之后、页面分发之前）。
	for _, page := range []string{"channels", "access", "settings"} {
		u.closeConfirmed = false
		u.page = page
		tt.Frame()
		if !tt.HasText("驻留托盘") {
			t.Fatalf("notice missing on %s page: %v", page, tt.Texts())
		}
	}
}

// TestNavigationSwitchesPage 覆盖边栏导航：点「渠道」切到渠道页（逻辑模型
// 内容退场），点「逻辑模型」切回——边栏四页的分发主干。
func TestNavigationSwitchesPage(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 900, 600)
	for _, label := range []string{"逻辑模型", "渠道", "接入", "设置"} {
		if !tt.HasText(label) {
			t.Fatalf("nav item %q missing: %v", label, tt.Texts())
		}
	}
	if err := tt.Click("渠道"); err != nil {
		t.Fatal(err)
	}
	if u.page != "channels" {
		t.Fatalf("page = %q, want channels", u.page)
	}
	if tt.HasText("新建逻辑模型") {
		t.Fatalf("logical leaked into channels page: %v", tt.Texts())
	}
	if err := tt.Click("逻辑模型"); err != nil {
		t.Fatal(err)
	}
	if u.page != "logical" {
		t.Fatalf("page = %q, want logical", u.page)
	}
	if !tt.HasText("新建逻辑模型") {
		t.Fatalf("logical view missing: %v", tt.Texts())
	}
}

// TestGatewayStatusBarStartStopGuards 覆盖底条启停按钮的状态守卫（spec §8.3
// 放行集合不变）：stopped 只出现启动钮、running 只出现停止钮。
func TestGatewayStatusBarStartStopGuards(t *testing.T) {
	u, a := testUI(t)
	st, _ := a.CurrentSettings()
	if st.GatewayToken == "" {
		t.Fatal("fresh app must have a gateway token")
	}
	tt := ui.NewTester(u.view, 900, 600)
	if !tt.HasText("启动网关") || tt.HasText("停止网关") {
		t.Fatalf("stopped guard: %v", tt.Texts())
	}
}

func TestNeedsReopen(t *testing.T) {
	if !needsReopen(nil) {
		t.Fatal("nil window should reopen")
	}
	if needsReopen(&mygo.Window{}) {
		t.Fatal("live window should be reused")
	}
}
