package uikit

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestBuildThemeTokens 覆盖 cc-switch 重设计 spec §3.2/§3.3：橙主操作锚点
// （双外观同值）、Success 与 Accent 分离（绿=链路健康、橙=品牌操作，不再
// 旧档 §2.3 的「一个颜色想法」）、Focus 浅色随 Accent/深色降饱和一档、
// Radius 8（卡片 12 由 Box 显式盖）、应用级选中灰与侧栏灰、软色板双边
// 取值锚点与软底/前景字互异。
func TestBuildThemeTokens(t *testing.T) {
	light, dark := BuildTheme(false, 1), BuildTheme(true, 1)
	if light.Accent != ui.Hex("#F97316") || dark.Accent != ui.Hex("#F97316") {
		t.Fatal("accent")
	}
	if light.Success != ui.Hex("#16A34A") || dark.Success != ui.Hex("#22C55E") {
		t.Fatal("success")
	}
	if light.Success == light.Accent {
		t.Fatal("绿橙必须分离（spec §1）")
	}
	if light.Focus != light.Accent {
		t.Fatal("light focus")
	}
	if dark.Focus != ui.Hex("#D8783A") {
		t.Fatal("dark focus")
	}
	if light.Radius != 8 {
		t.Fatal("radius 8（卡片 12 由 Box 显式盖）")
	}
	if SelectedColor(light) != ui.Hex("#E4E4EA") || SelectedColor(dark) != ui.Hex("#3A3A40") {
		t.Fatal("selected")
	}
	if SidebarColor(light) != ui.Hex("#F5F5F7") || SidebarColor(dark) != ui.Hex("#232327") {
		t.Fatal("sidebar")
	}
	sl, sd := Softs(light), Softs(dark)
	if sl.Act != ui.Hex("#FFF1E6") || sl.ActText != ui.Hex("#C2410C") {
		t.Fatal("soft act light")
	}
	if sd.Link != ui.Hex("#5AA9F8") {
		t.Fatal("soft link dark")
	}
	for _, s := range []SoftPalette{sl, sd} {
		if s.Act == s.ActText || s.Ok == s.OkText || s.Warn == s.WarnText || s.Danger == s.DangerText {
			t.Fatal("soft fg/bg 互异")
		}
	}
	if light.Background == dark.Background {
		t.Fatal("appearances must differ")
	}
}

// TestBuildThemeTextScale 覆盖文字缩放接入（Review Focus 3）：自设主题必须
// 自己乘 TextScale，<=0 守卫为 1。
func TestBuildThemeTextScale(t *testing.T) {
	if got := BuildTheme(false, 1.25).FontSize; got != 16.25 {
		t.Fatalf("FontSize %v, want 16.25", got)
	}
	if got := BuildTheme(true, 0).FontSize; got != 13 {
		t.Fatalf("FontSize %v, want guard-to-1 => 13", got)
	}
}

// TestStateWordAndColor 覆盖状态视觉词汇（Review Focus 4）：四态映射 +
// 未知状态原样回显、落弱灰，四态颜色互异；running 从 Accent 改挂
// Success——状态色与品牌橙分离（新 spec §1）。
func TestStateWordAndColor(t *testing.T) {
	th := BuildTheme(true, 1)
	for state, want := range map[string]string{
		"running": "运行中", "degraded": "降级", "failed": "故障", "stopped": "已停止",
	} {
		if got := StateWord(state); got != want {
			t.Fatalf("StateWord(%q)=%q want %q", state, got, want)
		}
	}
	if got := StateWord("hibernating"); got != "hibernating" {
		t.Fatalf("unknown state must echo raw, got %q", got)
	}
	if stateColor(th, "hibernating") != th.TextMuted {
		t.Fatal("unknown state must fall back to TextMuted")
	}
	if got := stateColor(th, "running"); got != th.Success || got == th.Accent {
		t.Fatal("running 必须= Success 且不再= Accent（spec §1 绿橙分离）")
	}
	cols := map[string]ui.Color{
		"running": th.Success, "degraded": th.Warning, "failed": th.Danger, "stopped": th.TextMuted,
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

// TestSelectionStillVisible 守住文本选区暖色：框架 Theme.Selection 只管
// 文本选区，必须保持 #FFDDBB/#4A3423 可见度，与应用级选中灰
// （SelectedColor，导航/列表选中底）是两个东西——选区变灰会淹没光标。
func TestSelectionStillVisible(t *testing.T) {
	light, dark := BuildTheme(false, 1), BuildTheme(true, 1)
	if light.Selection != ui.Hex("#FFDDBB") {
		t.Fatalf("light selection %v, want #FFDDBB", light.Selection)
	}
	if dark.Selection != ui.Hex("#4A3423") {
		t.Fatalf("dark selection %v, want #4A3423", dark.Selection)
	}
	if light.Selection == SelectedColor(light) || dark.Selection == SelectedColor(dark) {
		t.Fatal("文本选区暖色不得与应用级选中灰同值")
	}
}

// TestLedDotRenders 冒烟：LED 组件在 Tester 帧内可构建不 panic（点本身
// 无文本，Texts 为空即正常）。
func TestLedDotRenders(t *testing.T) {
	th := BuildTheme(false, 1)
	ui.NewTester(func(c *ui.Context) { LedDot(c, th, "running") }, 40, 40).Frame()
}

// TestReasonWord 覆盖降级原因翻译：已知内部原因给中文，其余（系统错误
// 原文）原样透传便于排障。
func TestReasonWord(t *testing.T) {
	if got := ReasonWord("no enabled logical model or usable channel"); got != "没有可用的逻辑模型或渠道" {
		t.Fatalf("known reason = %q", got)
	}
	if got := ReasonWord("listen tcp 127.0.0.1:17890: bind: address already in use"); !strings.Contains(got, "bind") {
		t.Fatalf("unknown reason should pass through: %q", got)
	}
	if got := ReasonWord(""); got != "" {
		t.Fatalf("empty reason = %q", got)
	}
}
