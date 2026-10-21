package desktop

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"

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
		Gateway: bridge.Gateway{A: a},
		// 凭据走 app 的加密存储（库内 AES-256-GCM），Channels.Creds 留空走 a.Creds() 兜底。
		Channels: bridge.Channels{A: a},
		Logical:  &bridge.LogicalModels{A: a},
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

// TestCloseNoticeShowsModalOnAnyPage 覆盖首关驻留模态：模态挂在 view 根层，
// 停在任意页点 X 都能看到「网关继续运行 / 最小化到托盘」。
func TestCloseNoticeShowsModalOnAnyPage(t *testing.T) {
	u, _ := testUI(t)
	u.closeNotice = true
	for _, page := range []string{"logical", "channels", "access", "settings"} {
		u.page = page
		tt := ui.NewTester(u.view, 900, 700)
		if !tt.HasText("网关继续运行") || !tt.HasText("最小化到托盘") {
			t.Fatalf("modal missing on %s: %v", page, tt.Texts())
		}
	}
}

// TestCloseNoticeConfirmHidesWindow 覆盖确认分支：点「最小化到托盘」必须
// **推迟**隐藏窗口（不能在 GTK 点击事件里同步 Hide——实测会让 MyGo 的 GPU
// Presenter 在失效表面上提交帧，SIGABRT），并记录 closeConfirmed、收起模态。
func TestCloseNoticeConfirmHidesWindow(t *testing.T) {
	u, _ := testUI(t)
	hidden := false
	u.hideWindow = func() { hidden = true }
	var scheduled func()
	u.schedule = func(fn func()) { scheduled = fn }
	u.closeNotice = true
	tt := ui.NewTester(u.view, 900, 700)
	if err := tt.Click("最小化到托盘"); err != nil {
		t.Fatal(err)
	}
	if hidden {
		t.Fatal("hide must be deferred out of the GTK event handler")
	}
	if scheduled == nil {
		t.Fatal("hide was not scheduled")
	}
	scheduled()
	if !hidden {
		t.Fatal("confirm must hide the window once scheduled")
	}
	if !u.closeConfirmed {
		t.Fatal("confirm not recorded")
	}
	if u.closeNotice {
		t.Fatal("modal must close after confirm")
	}
}

// TestAfterEventRuns 覆盖默认调度器：窗口操作必须异步执行（经 timer
// goroutine 投递回主线程），而不是在调用栈里同步执行。
func TestAfterEventRuns(t *testing.T) {
	done := make(chan struct{})
	afterEvent(func() { close(done) })
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("scheduled window operation never ran")
	}
}

// TestCloseNoticeCancelKeepsWindow 覆盖取消分支：点「取消」只收起模态，
// 不隐藏、不记录已确认。
func TestCloseNoticeCancelKeepsWindow(t *testing.T) {
	u, _ := testUI(t)
	hidden := false
	u.hideWindow = func() { hidden = true }
	u.closeNotice = true
	tt := ui.NewTester(u.view, 900, 700)
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if hidden {
		t.Fatal("cancel must not hide the window")
	}
	if u.closeConfirmed {
		t.Fatal("cancel must not record confirmation")
	}
	if u.closeNotice {
		t.Fatal("modal must close on cancel")
	}
}

// TestRequestCloseEducatesOnceThenHides 覆盖关窗主路径：首次关窗只弹模态
// （不隐藏）；确认后的后续关窗直接隐藏。隐藏而非销毁是 Linux 上避免
// GTK 在事件派发中析构窗口导致 SIGSEGV 的关键。
func TestRequestCloseEducatesOnceThenHides(t *testing.T) {
	u, _ := testUI(t)
	hidden, redraw := 0, 0
	var scheduled func()
	u.schedule = func(fn func()) { scheduled = fn }
	u.requestClose(func() { hidden++ }, func() { redraw++ })
	if !u.closeNotice || hidden != 0 || redraw != 1 {
		t.Fatalf("first close: notice=%v hidden=%d redraw=%d", u.closeNotice, hidden, redraw)
	}
	u.closeConfirmed = true
	u.closeNotice = false
	u.requestClose(func() { hidden++ }, func() { redraw++ })
	if hidden != 0 {
		t.Fatalf("acknowledged close must not hide inside the close event, hidden=%d", hidden)
	}
	if scheduled == nil {
		t.Fatal("acknowledged close did not schedule the hide")
	}
	scheduled()
	if hidden != 1 {
		t.Fatalf("scheduled hide did not run, hidden=%d", hidden)
	}
	if u.closeNotice {
		t.Fatal("acknowledged close must not reopen the notice")
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

// TestTrayReferenceRetained 覆盖托盘强引用：MyGo 的菜单项注册表用
// weak.Pointer 指向 MenuItem，*Tray 被 GC 后 native 菜单仍在、点击却会在
// lookupMenuItem 拿到 nil 并静默丢弃——托盘「退出」因此点了没反应。
// keepTray 记录的引用必须在 GC 后仍然可达。
func TestTrayReferenceRetained(t *testing.T) {
	prev := appTray
	t.Cleanup(func() { appTray = prev })
	tr := &mygo.Tray{}
	keepTray(tr)
	runtime.GC()
	runtime.GC()
	if appTray != tr {
		t.Fatal("tray reference was not retained; tray menu clicks would silently stop working")
	}
}
