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

// TestRecentStats 覆盖概览「近 N 条 · M 失败」徽章的统计口径：failed =
// Status==0（未收到响应即中断）或 >=400；2xx/3xx 不算失败。
func TestRecentStats(t *testing.T) {
	recs := []telemetry.RequestRecord{{Status: 200}, {Status: 429}, {Status: 0}, {Status: 502}}
	total, failed := recentStats(recs)
	if total != 4 || failed != 3 {
		t.Fatalf("%d %d", total, failed)
	}
}

// TestOverviewHeroAndCards 覆盖四卡骨架（spec §5.3）：hero 状态词、接入卡、
// 密钥池/设置并排小卡、最近请求卡，以及 hero 卡内恒显的「日志:」行。
func TestOverviewHeroAndCards(t *testing.T) {
	u, _ := testUI(t)
	u.status.State = "running"
	tt := ui.NewTester(u.overviewView, 1080, 720)
	for _, want := range []string{"运行中", "接入 Claude Code", "密钥池", "设置", "最近请求", "日志:"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
}

// TestOverviewTokenMasking 覆盖掩码不变量（spec §8.1）：默认令牌不得明文
// 出现，「显示」单按钮换 label 切换后明文可见。
func TestOverviewTokenMasking(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.overviewView, 1080, 720)
	if tt.HasText(u.token) {
		t.Fatal("默认不得明文")
	}
	if err := tt.Click("显示"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(u.token) {
		t.Fatal("显示后须明文")
	}
}

// TestOverviewSnippetMasked 覆盖接入卡片段：BASE_URL 行恒在、展示行默认
// 掩码 ***、真值不出现（复制用真实片段、展示恒掩码的分离语义照旧）。
func TestOverviewSnippetMasked(t *testing.T) {
	u, a := testUI(t)
	st, _ := a.CurrentSettings()
	if st.GatewayToken == "" {
		t.Fatal("fresh app must have a gateway token")
	}
	tt := ui.NewTester(u.overviewView, 1080, 720)
	if !tt.HasText("ANTHROPIC_BASE_URL") || !tt.HasText("***") {
		t.Fatalf("snippet missing: %v", tt.Texts())
	}
	if tt.HasText(u.token) {
		t.Fatalf("token leaked before show: %v", tt.Texts())
	}
}

// TestOverviewRegenConfirm 覆盖重新生成令牌的 AlertDialog 二次确认：只点
// 「重新生成」不得换令牌，弹窗里点「确认重新生成」才换。
func TestOverviewRegenConfirm(t *testing.T) {
	u, _ := testUI(t)
	old := u.token
	tt := ui.NewTester(u.overviewView, 1080, 720)
	if err := tt.Click("重新生成"); err != nil {
		t.Fatal(err)
	}
	if u.token != old {
		t.Fatal("未确认不得换令牌")
	}
	if err := tt.Click("确认重新生成"); err != nil {
		t.Fatal(err)
	}
	if u.token == old {
		t.Fatal("确认后须换令牌")
	}
}

// TestOverviewStartStopGuards 覆盖启停按钮的状态守卫（spec §8.3 放行集合
// 不变）：running 只出现停止钮、stopped 只出现启动钮。
func TestOverviewStartStopGuards(t *testing.T) {
	u, _ := testUI(t)
	u.status.State = "running"
	tt := ui.NewTester(u.overviewView, 1080, 720)
	if tt.HasText("启动网关") {
		t.Fatal("running 不得出现启动钮")
	}
	if !tt.HasText("停止网关") {
		t.Fatal("running 须有停止钮")
	}
	u.status.State = "stopped"
	tt = ui.NewTester(u.overviewView, 1080, 720)
	if !tt.HasText("启动网关") || tt.HasText("停止网关") {
		t.Fatal("stopped 守卫")
	}
}

// TestOverviewStartButtonAllowedFromFailed 覆盖 I3：Failed 态下「启动网关」必须
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

// TestOverviewShowsLogPath 覆盖 I3 的日志路径提示：hero 卡内「日志: <path>」
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

// TestOverviewRecentTableRenders 覆盖最近请求卡：七列表头（「渠道/密钥」为
// 新列）、单元格 Channel/Account 等宽两段显示，以及 hero 右侧「近 N 条 ·
// M 失败」统计徽章同帧可见（total>0 才显）。
func TestOverviewRecentTableRenders(t *testing.T) {
	u, _ := testUI(t)
	u.recent = recent3()
	tt := ui.NewTester(u.overviewView, 1080, 720)
	if !tt.HasText("模型") || !tt.HasText("渠道/密钥") || !tt.HasText("耗时") {
		t.Fatalf("table headers missing: %v", tt.Texts())
	}
	if !tt.HasText("m1/a1") {
		t.Fatalf("channel/key cell missing: %v", tt.Texts())
	}
	if !tt.HasText("近 3 条 · 1 失败") {
		t.Fatalf("stats badge missing: %v", tt.Texts())
	}
}

// TestOverviewDegradedBanner 覆盖降级横幅：DegradedReason 非空时 hero 卡下
// 出现原因文字条（语义照旧）。
func TestOverviewDegradedBanner(t *testing.T) {
	u, _ := testUI(t)
	u.status.State = "degraded"
	u.status.DegradedReason = "部分渠道冷却"
	tt := ui.NewTester(u.overviewView, 1080, 720)
	if !tt.HasText("降级原因: 部分渠道冷却") {
		t.Fatalf("degraded banner missing: %v", tt.Texts())
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

// TestOverviewPoolStatusShowsCooling 覆盖池状态小卡（spec §8）：冷却密钥以
// 「渠道/密钥 冷却 Ns」琥珀 chips 出现；PoolStatus 只回冷却中的密钥，
// 正常密钥不渲染成条目。
func TestOverviewPoolStatusShowsCooling(t *testing.T) {
	u, _ := testUI(t)
	if _, err := u.svc.Channels.Create("anthropic-compatible", "m", "https://m.example.com", 10, true, false); err != nil {
		t.Fatal(err)
	}
	cid := u.svc.Channels.List()[0].ID
	if _, err := u.svc.Channels.AddKeys(cid, "k1", "sk-abc12345", true); err != nil {
		t.Fatal(err)
	}
	d, _ := u.svc.Channels.Get(cid)
	u.svc.Gateway.A.Cooldowns().MarkClassified(d.Keys[0].ID, "rate_limited", 30, time.Now())
	tt := ui.NewTester(u.overviewView, 900, 600)
	if !tt.HasText("冷却") || !tt.HasText("m/k1") {
		t.Fatalf("pool strip missing: %v", tt.Texts())
	}
}

// TestSettingsAlwaysVisibleEchoes 覆盖设置卡常开（spec §5.3：Collapsible 改
// 常开小卡）：首帧输入框为空即回显当前生效端口/超时（原「打开瞬间回显」
// 的等价替换——回显只发生在输入框为空时）。断言走输入框字段值，避免与
// hero 地址行里的同值文本混淆。
func TestSettingsAlwaysVisibleEchoes(t *testing.T) {
	u, a := testUI(t)
	st, _ := a.CurrentSettings()
	tt := ui.NewTester(u.overviewView, 1080, 720)
	if !tt.HasText("端口:") || !tt.HasText("请求超时（秒）:") {
		t.Fatalf("settings labels missing: %v", tt.Texts())
	}
	if u.portInput != strconv.Itoa(st.Port) || u.timeoutInput != strconv.Itoa(st.RequestTimeoutSeconds) {
		t.Fatalf("echo missing: %q %q", u.portInput, u.timeoutInput)
	}
}

// TestSettingsSaveResetsAndReechoes 覆盖设置保存语义不变（提示文案、落库
// 值）+ 保存成功清空输入、下一帧按新生效值回显；停着的网关保存不提示
// 重启（restart 只在端口变更且非 stopped 时为真）。手输值带前导零：回显
// 归一掉前导零即证明走的是「清空→回显」而非「保留手输值」（Click 返回时
// 帧已 settle，「清空」与「回显」合并呈现，无法分帧断言）。
func TestSettingsSaveResetsAndReechoes(t *testing.T) {
	u, a := testUI(t)
	tt := ui.NewTester(u.overviewView, 1080, 720)
	u.portInput, u.timeoutInput = "018000", "060"
	tt.Frame()
	if err := tt.Click("保存设置"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("已保存") || tt.HasText("重启网关后生效") {
		t.Fatalf("stopped-state save message wrong: %v", tt.Texts())
	}
	st, _ := a.CurrentSettings()
	if st.Port != 18000 || st.RequestTimeoutSeconds != 60 {
		t.Fatalf("settings not saved: %d %d", st.Port, st.RequestTimeoutSeconds)
	}
	tt.Frame()
	if u.portInput != "18000" || u.timeoutInput != "60" {
		t.Fatalf("re-echo after save wrong: %q %q", u.portInput, u.timeoutInput)
	}
}

func recent3() []telemetry.RequestRecord {
	return []telemetry.RequestRecord{
		{Model: "claude-opus", Channel: "m1", Account: "a1", Status: 200, DurationMs: 1200},
		{Model: "claude-sonnet", Channel: "m1", Account: "a1", Status: 429, DurationMs: 90, ErrorClass: "rate_limited"},
		{Model: "claude-opus", Channel: "m1", Account: "a1", Status: 200, DurationMs: 800},
	}
}
