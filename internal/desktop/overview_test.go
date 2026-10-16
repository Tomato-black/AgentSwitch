package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/telemetry"
)

func TestOverviewTokenAndSnippet(t *testing.T) {
	u, a := testUI(t)
	st, _ := a.CurrentSettings()
	u.token = st.GatewayToken
	tt := ui.NewTester(u.overviewView, 900, 700)
	if !tt.HasText("访问令牌") || !tt.HasText("ANTHROPIC_BASE_URL") {
		t.Fatalf("texts %v", tt.Texts())
	}
	if err := tt.Click("显示"); err != nil {
		t.Fatal(err)
	}
	if !u.showToken {
		t.Fatal("showToken toggle failed")
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

func recent3() []telemetry.RequestRecord {
	return []telemetry.RequestRecord{
		{Model: "claude-opus", Account: "a1", Status: 200, DurationMs: 1200},
		{Model: "claude-sonnet", Account: "a1", Status: 429, DurationMs: 90, ErrorClass: "rate_limited"},
		{Model: "claude-opus", Account: "a1", Status: 200, DurationMs: 800},
	}
}
