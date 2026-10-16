// theme.go 是「交换机面板」设计系统的 token 表与状态视觉词汇。
// spec：docs/superpowers/specs/2026-10-17-desktop-ui-mygo-native-design.md §2/§3。
// 已知偏离 spec §3.1：buildTheme 增加 textScale 入参——框架默认主题随系统
// 文字缩放（Preferences.TextScale），自设主题必须自己接上，否则可访问性回退。
package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// monoFont 是机器读数（地址、令牌、密钥尾、耗时等）的等宽字体族；
// 非 macOS 的回退行为见 spec §8.3（当前仅 darwin 一等公民）。
const monoFont = "Menlo"

// buildTheme 按 dark 构造品牌主题：以框架主题为底、覆写 spec §3.2 全部
// 19 个颜色 token 与度量（未列字段保持框架默认）。textScale<=0 视为 1。
func buildTheme(dark bool, textScale float32) *ui.Theme {
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
	}
	if textScale <= 0 {
		textScale = 1
	}
	if dark {
		t.Background = ui.Hex("#16181D")
		t.Surface = ui.Hex("#1E2128")
		t.SurfaceHover = ui.Hex("#262A33")
		t.SurfacePressed = ui.Hex("#2E333E")
		t.Border = ui.Hex("#2A2E37")
		t.Text = ui.Hex("#E8EAF0")
		t.TextMuted = ui.Hex("#9AA1B0")
		t.Accent = ui.Hex("#45D0B4")
		t.AccentHover = ui.Hex("#5FDFC6")
		t.AccentPressed = ui.Hex("#2FA891")
		t.AccentText = ui.Hex("#0D1512")
		t.Danger = ui.Hex("#E5484D")
		t.Warning = ui.Hex("#F5B83D")
		t.Selection = ui.Hex("#1F4A41")
		t.Inverse = ui.Hex("#E8EAF0")
		t.InverseText = ui.Hex("#16181D")
		t.Scrollbar = ui.Hex("#3A404C")
	} else {
		t.Background = ui.Hex("#F3F4F2")
		t.Surface = ui.Hex("#FFFFFF")
		t.SurfaceHover = ui.Hex("#F0F2F1")
		t.SurfacePressed = ui.Hex("#E4E8E6")
		t.Border = ui.Hex("#D5DAD6")
		t.Text = ui.Hex("#1C2320")
		t.TextMuted = ui.Hex("#67736D")
		t.Accent = ui.Hex("#0E7A5F")
		t.AccentHover = ui.Hex("#0B6550")
		t.AccentPressed = ui.Hex("#08503F")
		t.AccentText = ui.Hex("#FFFFFF")
		t.Danger = ui.Hex("#C63D3D")
		t.Warning = ui.Hex("#B97A0A")
		t.Selection = ui.Hex("#C9E8DF")
		t.Inverse = ui.Hex("#1C2320")
		t.InverseText = ui.Hex("#F3F4F2")
		t.Scrollbar = ui.Hex("#B9C1BB")
	}
	// 一个颜色想法（spec §2.3）：品牌青就是链路健康色。
	t.Success = t.Accent
	t.Focus = t.Accent
	t.Radius = 6
	t.Spacing = 4
	t.FontSize = 13 * textScale
	return t
}

// stateWord 把网关状态译成面板状态词；未知状态原样回显（Review Focus 4）。
func stateWord(state string) string {
	switch state {
	case "running":
		return "运行中"
	case "degraded":
		return "降级"
	case "failed":
		return "故障"
	case "stopped":
		return "已停止"
	default:
		return state
	}
}

// stateColor 给状态配 LED 色：running=健康青（=Accent），degraded=琥珀，
// failed=红，stopped/未知=弱灰。
func stateColor(t *ui.Theme, state string) ui.Color {
	switch state {
	case "running":
		return t.Accent
	case "degraded":
		return t.Warning
	case "failed":
		return t.Danger
	default:
		return t.TextMuted
	}
}

// ledDot 画状态 LED：10px 圆点，色由 stateColor 决定；running 时用 Loop
// 做缓慢呼吸。preferences.go 语义：Animate 在 ReduceMotion 下直达目标，
// 而 Loop「shows that something is going on」继续走——呼吸恰属后者，无需
// 分支。人工验收若呼吸表现异常，删掉 Loop 块退化为静态点（spec §8.4）。
func ledDot(c *ui.Context, t *ui.Theme, state string) {
	b := ui.Box(c).Width(10).Height(10).Radius(5).Background(stateColor(t, state))
	if state == "running" {
		v := b.Loop("gw-led", 2100*time.Millisecond, ui.EaseInOut)
		b.Opacity(0.55 + 0.45*v)
	}
}
