package settings

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/config"
	"AgentSwitch/internal/desktop/uitest"
	"AgentSwitch/internal/telemetry"
)

// TestSettingsFormMapsPort 覆盖表单↔Settings 编解码纯函数：端口/超时/透传
// 往返一致（端口是重点锚点），非整数输入报「端口/超时必须是整数」。
func TestSettingsFormMapsPort(t *testing.T) {
	portIn, timeoutIn, pas := settingsToForm(config.Settings{
		Port: 8080, RequestTimeoutSeconds: 90, PassthroughUnregistered: true,
	})
	if portIn != "8080" || timeoutIn != "90" || !pas {
		t.Fatalf("encode = %q %q %v, want 8080 90 true", portIn, timeoutIn, pas)
	}
	s, err := formToSettings(portIn, timeoutIn, pas)
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 8080 || s.RequestTimeoutSeconds != 90 || !s.PassthroughUnregistered {
		t.Fatalf("decode = %+v", s)
	}
	if _, err := formToSettings("abc", "90", false); err == nil {
		t.Fatal("non-integer port must error")
	}
	if _, err := formToSettings("8080", "x", false); err == nil {
		t.Fatal("non-integer timeout must error")
	}
	if _, err := formToSettings("8080", "90", false); err != nil {
		t.Fatal(err)
	}
	if _, err := formToSettings("0", "90", false); err == nil {
		t.Fatal("port 0 must error")
	}
	if _, err := formToSettings("65536", "90", false); err == nil {
		t.Fatal("port >65535 must error")
	}
	if _, err := formToSettings("8080", "0", false); err == nil {
		t.Fatal("timeout 0 must error")
	}
	if _, err := formToSettings("8080", "86401", false); err == nil {
		t.Fatal("timeout >86400 must error")
	}
}

// TestSettingsPageRenders 覆盖设置页替换占位：端口/超时/透传预留/版本/
// 保存按钮都在场；不再出现 TODO 占位。
func TestSettingsPageRenders(t *testing.T) {
	st, _ := uitest.New(t)
	st.Page = "settings"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 900, 600)
	for _, want := range []string{"端口", "请求超时", "版本", "保存设置", "网关诊断", "最近请求"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("TODO: 设置") || tt.HasText("透传未登记模型") {
		t.Fatalf("placeholder/dead switch leaked: %v", tt.Texts())
	}
}

// TestRecentLine 覆盖诊断行格式：2xx 打勾、非 0 非 2xx 打叉、0 视为失败；
// 含渠道缺失占位、切换次数与流式标记。
func TestRecentLine(t *testing.T) {
	at := time.Date(2026, 10, 28, 15, 4, 5, 0, time.Local)
	ok := recentLine(telemetry.RequestRecord{Model: "my-code", Channel: "main", Status: 200, DurationMs: 120, StartedAt: at})
	for _, want := range []string{"15:04:05", "my-code", "main", "200 ✓", "120ms"} {
		if !strings.Contains(ok, want) {
			t.Fatalf("missing %q in %q", want, ok)
		}
	}
	fail := recentLine(telemetry.RequestRecord{Model: "m", Channel: "c", Status: 0, FailoverCount: 2, Streaming: true, StartedAt: at})
	for _, want := range []string{"失败", "切换2", "流"} {
		if !strings.Contains(fail, want) {
			t.Fatalf("missing %q in %q", want, fail)
		}
	}
	code := recentLine(telemetry.RequestRecord{Model: "m", Status: 500, StartedAt: at})
	if !strings.Contains(code, "500 ✗") || !strings.Contains(code, "· - ·") {
		t.Fatalf("channel placeholder/status = %q", code)
	}
}

// TestAttemptLine 覆盖尝试链文案：冷却跳过/过滤/失败/成功与耗时。
func TestAttemptLine(t *testing.T) {
	cases := []struct {
		a    telemetry.AttemptRecord
		want []string
	}{
		{telemetry.AttemptRecord{Channel: "A", Model: "m", Outcome: "ok", DurationMs: 120}, []string{"A·m", "成功", "120ms"}},
		{telemetry.AttemptRecord{Channel: "A", Model: "m", Outcome: "cooling_skip", Reason: "cooling:rate_limited"}, []string{"跳过", "上游限流"}},
		{telemetry.AttemptRecord{Channel: "A", Model: "m", Outcome: "filtered", Reason: "cooling:quota_exhausted"}, []string{"过滤", "额度耗尽"}},
		{telemetry.AttemptRecord{Channel: "A", Model: "m", Outcome: "canceled"}, []string{"客户端取消"}},
	}
	for _, tc := range cases {
		got := attemptLine(tc.a)
		for _, w := range tc.want {
			if !strings.Contains(got, w) {
				t.Fatalf("attemptLine(%+v) = %q, missing %q", tc.a, got, w)
			}
		}
	}
}

// TestRecentAttemptsRender：展开状态的记录显示尝试链（含冷却原因）。
func TestRecentAttemptsRender(t *testing.T) {
	st, _ := uitest.New(t)
	st.Page = "settings"
	st.Recent = []telemetry.RequestRecord{{
		ID: "r1", Model: "my-code", Channel: "A", Status: 200, StartedAt: time.Now(),
		Attempts: []telemetry.AttemptRecord{{Channel: "A", Model: "m1", Outcome: "cooling_skip", Reason: "cooling:rate_limited"}},
	}}
	st.Settings.RecentOpen = "r1"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 900, 800)
	if !tt.HasText("上游限流") {
		t.Fatalf("attempt chain missing: %v", tt.Texts())
	}
}

func TestSettingsHistoryFilter(t *testing.T) {
	st, _ := uitest.New(t)
	st.Recent = []telemetry.RequestRecord{
		{ID: "a", Model: "my-code", Channel: "main", Status: 200},
		{ID: "b", Model: "other", Channel: "backup", Status: 429},
	}
	st.Settings.HistoryFilter = "main"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 1000)
	if !tt.HasText("my-code") || tt.HasText("other") {
		t.Fatalf("筛选未生效：%v", tt.Texts())
	}
}

func TestSettingsImportPreviewRenders(t *testing.T) {
	st, _ := uitest.New(t)
	st.Settings.ImportPreview = &bridge.ImportPreview{
		Version: 1, Channels: 2, LogicalModels: 1, AccessConfigs: 1,
		Conflicts: []string{"渠道「主渠道」已存在"},
		Warnings:  []string{"渠道 Key 不在导出文件中，导入后需重新填写。"},
	}
	st.Settings.ImportArm = true
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 1100)
	for _, want := range []string{"导入预览", "已存在", "重新填写", "确认导入"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
}

func TestSettingsUsageReportRenders(t *testing.T) {
	st, _ := uitest.New(t)
	st.Settings.UsageLoaded = true
	st.Settings.UsageReport = bridge.UsageReport{
		Days: 30, TotalInput: 1000, TotalOutput: 200, TotalCostUSD: 0.01, Priced: true,
		Rows: []bridge.UsageRow{{Channel: "main", Model: "m1", Requests: 1,
			InputTokens: 1000, OutputTokens: 200, CostUSD: 0.01, Priced: true}},
	}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 1400)
	for _, want := range []string{"用量（近 30 天）", "main", "m1", "1000"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
}

func TestSettingsAlertsToggle(t *testing.T) {
	st, _ := uitest.New(t)
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 1200)
	if !tt.HasText("启用冷却/故障告警") {
		t.Fatalf("alert toggle missing: %v", tt.Texts())
	}
	if err := tt.Click("启用冷却/故障告警"); err != nil {
		t.Fatal(err)
	}
	if st.Svc.Gateway.AlertsEnabled() {
		t.Fatal("alert toggle should persist false")
	}
}

func TestSettingsShowsOpenLogDir(t *testing.T) {
	st, _ := uitest.New(t)
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 1200)
	if !tt.HasText("打开目录") {
		t.Fatalf("open log dir button missing: %v", tt.Texts())
	}
}

func TestSettingsDiagnosticsAtBottom(t *testing.T) {
	st, _ := uitest.New(t)
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 2000)
	about, ok := tt.Find("关于")
	if !ok {
		t.Fatalf("about card missing")
	}
	diag, ok := tt.Find("网关诊断")
	if !ok {
		t.Fatalf("diagnostics card missing")
	}
	if diag.Y <= about.Y {
		t.Fatalf("diagnostics must be below about: about.Y=%v diag.Y=%v", about.Y, diag.Y)
	}
}
