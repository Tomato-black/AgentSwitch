package desktop

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/telemetry"
)

func TestOverviewTokenAndSnippet(t *testing.T) {
	u, a := testUI(t)
	st, _ := a.CurrentSettings()
	u.token = st.GatewayToken
	if u.token == "" {
		t.Fatal("fresh app must have a gateway token")
	}
	tt := ui.NewTester(u.overviewView, 900, 700)
	if !tt.HasText("访问令牌") || !tt.HasText("ANTHROPIC_BASE_URL") {
		t.Fatalf("texts %v", tt.Texts())
	}
	// 默认未点"显示"：令牌行与配置片段都必须掩码，真值不得出现
	if !tt.HasText("***") {
		t.Fatalf("mask missing: %v", tt.Texts())
	}
	if tt.HasText(u.token) {
		t.Fatalf("token leaked before show: %v", tt.Texts())
	}
	if err := tt.Click("显示"); err != nil {
		t.Fatal(err)
	}
	if !u.showToken {
		t.Fatal("showToken toggle failed")
	}
	// 点"显示"后：掩码解除，真值出现在展示行
	if !tt.HasText(u.token) {
		t.Fatalf("token should show after toggle: %v", tt.Texts())
	}
	if err := tt.Click("重新生成令牌"); err != nil {
		t.Fatal(err)
	}
	if !u.confirmRegen {
		t.Fatal("regen must require confirm click")
	}
	if err := tt.Click("确认重新生成"); err != nil {
		t.Fatal(err)
	}
	if u.confirmRegen {
		t.Fatal("confirm cleared")
	}
}

func TestOverviewRecentTableRenders(t *testing.T) {
	u, _ := testUI(t)
	u.recent = recent3()
	tt := ui.NewTester(u.overviewView, 1000, 700)
	if !tt.HasText("模型") || !tt.HasText("耗时") {
		t.Fatalf("table headers missing: %v", tt.Texts())
	}
}

// TestOverviewStartButtonAllowedFromFailed 覆盖 I3：Failed 态下"启动网关"必须
// 放行（守卫只认 stopped 时按钮死在 Failed 展示态上，UI 无路可走）。点击后
// app 真实离开 stopped 即证放行。端口取临时空闲口，避免与并行测试包撞 17890。
func TestOverviewStartButtonAllowedFromFailed(t *testing.T) {
	u, a := testUI(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveSettings(port, 120); err != nil {
		t.Fatal(err)
	}
	u.status.State = "failed" // 模拟 Failed 展示态（状态机实际为 stopped）
	tt := ui.NewTester(u.overviewView, 900, 700)
	if err := tt.Click("启动网关"); err != nil {
		t.Fatal(err)
	}
	// 启动经 go 异步执行——轮询等待状态机离开 stopped。
	deadline := time.Now().Add(5 * time.Second)
	for string(a.State().State) == "stopped" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := string(a.State().State); got == "stopped" {
		t.Fatalf("start button must be allowed from failed state, app stuck at %s", got)
	}
	if err := a.StopGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestOverviewShowsLogPath 覆盖 I3 的日志路径提示：概览页渲染"日志: <path>"
// （恒显——降级/失败只是它最被需要的时刻，恒显免去条件分支与文案分支测试）。
func TestOverviewShowsLogPath(t *testing.T) {
	u, a := testUI(t)
	if a.LogPath() == "" {
		t.Fatal("test app must be opened with a log path")
	}
	tt := ui.NewTester(u.overviewView, 900, 700)
	if !tt.HasText("日志:") || !tt.HasText(a.LogPath()) {
		t.Fatalf("log path line missing: %v", tt.Texts())
	}
}

func recent3() []telemetry.RequestRecord {
	return []telemetry.RequestRecord{
		{Model: "claude-opus", Account: "a1", Status: 200, DurationMs: 1200},
		{Model: "claude-sonnet", Account: "a1", Status: 429, DurationMs: 90, ErrorClass: "rate_limited"},
		{Model: "claude-opus", Account: "a1", Status: 200, DurationMs: 800},
	}
}

// TestSettingsCollapsibleOpens 覆盖设置折叠面板：点「设置」展开（原独立
// 按钮改 Collapsible），打开瞬间回显当前端口/超时（原「设置」按钮做的事，
// 改为帧内 prev/cur 检测）。
func TestSettingsCollapsibleOpens(t *testing.T) {
	u, a := testUI(t)
	st, _ := a.CurrentSettings()
	tt := ui.NewTester(u.overviewView, 900, 700)
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !u.settingsOpen {
		t.Fatal("collapsible must open on click")
	}
	if !tt.HasText("端口:") || !tt.HasText(strconv.Itoa(st.Port)) {
		t.Fatalf("settings echo missing: %v", tt.Texts())
	}
}

// TestOverviewEmptyRequestsHint 覆盖空请求表引导（spec §5.4：空态是邀请，
// 不是空白）。
func TestOverviewEmptyRequestsHint(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.overviewView, 900, 700)
	if !tt.HasText("暂无请求") {
		t.Fatalf("empty hint missing: %v", tt.Texts())
	}
}
