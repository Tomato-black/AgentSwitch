// theme.go 是「交换机面板」设计系统的 token 表与状态视觉词汇（cc-switch
// 风格：中性灰底 + 橙主操作 + 卡片流）。
// spec：docs/superpowers/specs/2026-10-17-desktop-ui-ccswitch-redesign-design.md §3.2/§3.3。
// 已知偏离旧档（2026-10-17-desktop-ui-mygo-native-design.md）§2.3 的「一个
// 颜色想法」（Success==Accent）：状态绿与品牌橙必须分离——LED/健康语义用
// 绿、主操作用橙，同屏并存不再互相染色（新 spec §1）。
// 另沿旧档已知偏离：buildTheme 增加 textScale 入参——框架默认主题随系统
// 文字缩放（Preferences.TextScale），自设主题必须自己接上，否则可访问性回退。
package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// monoFont 是机器读数（地址、令牌、密钥尾、耗时等）的等宽字体族；
// 非 macOS 的回退行为见旧档 §8.3（当前仅 darwin 一等公民）。
const monoFont = "Menlo"

// buildTheme 按 dark 构造品牌主题：以框架主题为底、覆写 cc-switch spec
// §3.2 全部 19 个颜色 token 与度量（未列字段保持框架默认）。色值全部
// 硬编码 ui.Hex，浅深两边逐字段给出，不派生。textScale<=0 视为 1。
func buildTheme(dark bool, textScale float32) *ui.Theme {
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
	}
	if textScale <= 0 {
		textScale = 1
	}
	if dark {
		t.Background = ui.Hex("#1B1B1F")
		t.Surface = ui.Hex("#2C2C31")
		t.SurfaceHover = ui.Hex("#333338")
		t.SurfacePressed = ui.Hex("#3A3A40")
		t.Selection = ui.Hex("#4A3423") // 文本选区保持暖色可见，不用选中灰
		t.Border = ui.Hex("#3F3F46")
		t.Text = ui.Hex("#E4E4E7")
		t.TextMuted = ui.Hex("#A1A1AA")
		t.Accent = ui.Hex("#F97316")
		t.AccentHover = ui.Hex("#EA620A")
		t.AccentPressed = ui.Hex("#DD5A0B")
		t.AccentText = ui.Hex("#FFFFFF")
		t.Success = ui.Hex("#22C55E")
		t.Danger = ui.Hex("#EF4444")
		t.Warning = ui.Hex("#FBBF24")
		t.Focus = ui.Hex("#D8783A") // 降饱和一档，暗底上橙描边不刺眼
		t.Inverse = ui.Hex("#E4E4E7")
		t.InverseText = ui.Hex("#18181B")
		t.Scrollbar = ui.Hex("#4A4A52")
	} else {
		t.Background = ui.Hex("#FFFFFF")
		t.Surface = ui.Hex("#FFFFFF")
		t.SurfaceHover = ui.Hex("#F4F4F5")
		t.SurfacePressed = ui.Hex("#EFEFF2")
		t.Selection = ui.Hex("#FFDDBB") // 文本选区保持暖色可见，不用选中灰
		t.Border = ui.Hex("#E4E4E9")
		t.Text = ui.Hex("#18181B")
		t.TextMuted = ui.Hex("#71717A")
		t.Accent = ui.Hex("#F97316")
		t.AccentHover = ui.Hex("#EA620A")
		t.AccentPressed = ui.Hex("#DD5A0B")
		t.AccentText = ui.Hex("#FFFFFF")
		t.Success = ui.Hex("#16A34A")
		t.Danger = ui.Hex("#DC2626")
		t.Warning = ui.Hex("#D97706")
		t.Focus = t.Accent // 浅色焦点环随品牌橙
		t.Inverse = ui.Hex("#18181B")
		t.InverseText = ui.Hex("#FFFFFF")
		t.Scrollbar = ui.Hex("#C9C9CF")
	}
	// Radius 8 是控件圆角基调；卡片 12 由 widgets 的 Box 显式盖，不走这里。
	t.Radius = 8
	t.Spacing = 4
	t.FontSize = 13 * textScale
	return t
}

// softPalette 是软色板（spec §3.3）：每类语义一对「软底 + 前景字」，
// 浅色取浅底深字、深色取深底亮字，供软徽章/软按钮/类型 tile 复用；
// Link 是软色板里唯一的单值（链接文字无底色）。
type softPalette struct {
	Act, ActText       ui.Color
	Ok, OkText         ui.Color
	Warn, WarnText     ui.Color
	Danger, DangerText ui.Color
	Gray, GrayText     ui.Color
	Link               ui.Color
}

// softs 按 t.Dark 取边返回软色板；色值全部硬编码，不派生自 token 表。
func softs(t *ui.Theme) softPalette {
	if t.Dark {
		return softPalette{
			Act: ui.Hex("#33221A"), ActText: ui.Hex("#FB923C"),
			Ok: ui.Hex("#12261A"), OkText: ui.Hex("#4ADE80"),
			Warn: ui.Hex("#2E2410"), WarnText: ui.Hex("#FBBF24"),
			Danger: ui.Hex("#2C1516"), DangerText: ui.Hex("#F87171"),
			Gray: ui.Hex("#2E2E33"), GrayText: ui.Hex("#A1A1AA"),
			Link: ui.Hex("#5AA9F8"),
		}
	}
	return softPalette{
		Act: ui.Hex("#FFF1E6"), ActText: ui.Hex("#C2410C"),
		Ok: ui.Hex("#E8F7EE"), OkText: ui.Hex("#007634"),
		Warn: ui.Hex("#FEF3C7"), WarnText: ui.Hex("#A64A00"),
		Danger: ui.Hex("#FEE2E2"), DangerText: ui.Hex("#B91C1C"),
		Gray: ui.Hex("#EFEFF2"), GrayText: ui.Hex("#52525B"),
		Link: ui.Hex("#0A6CD6"),
	}
}

// selectedColor 是应用级选中灰（导航项/列表行/分段选中片的底），中性灰、
// 与品牌橙无关；框架 Theme.Selection 只管文本选区（保持暖色），两者分工
// 不得混用（spec §3.2 注）。
func selectedColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#3A3A40")
	}
	return ui.Hex("#E4E4EA")
}

// sidebarColor 是自绘侧栏底色：比 Background 微沉一档，撑出侧栏与内容
// 区的层级（spec §5 页面骨架）。
func sidebarColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#232327")
	}
	return ui.Hex("#F5F5F7")
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

// stateColor 给状态配 LED 色：running=独立状态绿（Success，不再是品牌橙，
// spec §1 绿橙分离），degraded=琥珀，failed=红，cooling=琥珀（密钥冷却，
// 概览池状态条；常亮不呼吸），stopped/未知=弱灰。
func stateColor(t *ui.Theme, state string) ui.Color {
	switch state {
	case "running":
		return t.Success
	case "degraded":
		return t.Warning
	case "cooling":
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
