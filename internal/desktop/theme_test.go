package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestBuildThemeTokens 覆盖 spec §3.2：双外观主色锚点、Success==Accent
//（一个颜色想法）、语义色互异、度量常量。
func TestBuildThemeTokens(t *testing.T) {
	light, dark := buildTheme(false, 1), buildTheme(true, 1)
	if light.Accent != ui.Hex("#0E7A5F") {
		t.Fatalf("light accent %v", light.Accent)
	}
	if dark.Accent != ui.Hex("#45D0B4") {
		t.Fatalf("dark accent %v", dark.Accent)
	}
	if light.Success != light.Accent || dark.Success != dark.Accent {
		t.Fatal("Success must equal Accent (spec §3.2: one color idea)")
	}
	if light.Focus != light.Accent || dark.Focus != dark.Accent {
		t.Fatal("Focus must equal Accent")
	}
	for _, th := range []*ui.Theme{light, dark} {
		if th.Danger == th.Accent || th.Warning == th.Accent || th.TextMuted == th.Accent {
			t.Fatal("Danger/Warning/TextMuted must differ from Accent")
		}
		if th.Radius != 6 || th.Spacing != 4 {
			t.Fatalf("metrics %v %v", th.Radius, th.Spacing)
		}
	}
	if light.Background == dark.Background {
		t.Fatal("appearances must differ")
	}
}

// TestBuildThemeTextScale 覆盖文字缩放接入（Review Focus 3）：自设主题必须
// 自己乘 TextScale，<=0 守卫为 1。
func TestBuildThemeTextScale(t *testing.T) {
	if got := buildTheme(false, 1.25).FontSize; got != 16.25 {
		t.Fatalf("FontSize %v, want 16.25", got)
	}
	if got := buildTheme(true, 0).FontSize; got != 13 {
		t.Fatalf("FontSize %v, want guard-to-1 => 13", got)
	}
}

// TestStateWordAndColor 覆盖状态视觉词汇（Review Focus 4）：四态映射 +
// 未知状态原样回显、落弱灰，四态颜色互异。
func TestStateWordAndColor(t *testing.T) {
	th := buildTheme(true, 1)
	for state, want := range map[string]string{
		"running": "运行中", "degraded": "降级", "failed": "故障", "stopped": "已停止",
	} {
		if got := stateWord(state); got != want {
			t.Fatalf("stateWord(%q)=%q want %q", state, got, want)
		}
	}
	if got := stateWord("hibernating"); got != "hibernating" {
		t.Fatalf("unknown state must echo raw, got %q", got)
	}
	if stateColor(th, "hibernating") != th.TextMuted {
		t.Fatal("unknown state must fall back to TextMuted")
	}
	cols := map[string]ui.Color{
		"running": th.Accent, "degraded": th.Warning, "failed": th.Danger, "stopped": th.TextMuted,
	}
	seen := map[ui.Color]string{}
	for state, col := range cols {
		if stateColor(th, state) != col {
			t.Fatalf("stateColor(%q)", state)
		}
		if prev, dup := seen[col]; dup {
			t.Fatalf("color collision %s vs %s", prev, state)
		}
		seen[col] = state
	}
}

// TestLedDotRenders 冒烟：LED 组件在 Tester 帧内可构建不 panic（点本身
// 无文本，Texts 为空即正常）。
func TestLedDotRenders(t *testing.T) {
	th := buildTheme(false, 1)
	ui.NewTester(func(c *ui.Context) { ledDot(c, th, "running") }, 40, 40).Frame()
}
