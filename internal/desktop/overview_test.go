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

func recent3() []telemetry.RequestRecord {
	return []telemetry.RequestRecord{
		{Model: "claude-opus", Account: "a1", Status: 200, DurationMs: 1200},
		{Model: "claude-sonnet", Account: "a1", Status: 429, DurationMs: 90, ErrorClass: "rate_limited"},
		{Model: "claude-opus", Account: "a1", Status: 200, DurationMs: 800},
	}
}
