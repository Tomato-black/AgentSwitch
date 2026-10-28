package settings

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

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
}

// TestSettingsPageRenders 覆盖设置页替换占位：端口/超时/透传预留/版本/
// 保存按钮都在场；不再出现 TODO 占位。
func TestSettingsPageRenders(t *testing.T) {
	st, _ := uitest.New(t)
	st.Page = "settings"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 900, 600)
	for _, want := range []string{"端口", "请求超时", "透传", "预留", "版本", "保存设置", "网关诊断", "最近请求"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("TODO: 设置") {
		t.Fatalf("placeholder leaked: %v", tt.Texts())
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
