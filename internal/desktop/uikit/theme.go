// Package uikit 提供桌面 UI 的视觉系统：主题 token、软色板、共享控件与
// 图标，供组合根与各页面子包共用（只依赖 mygo/ui，不依赖 state/pages）。
//
// theme.go 是桌面 UI 设计系统的 token 表与状态视觉词汇（cc-switch
// 风格：中性灰底 + 橙主操作 + 卡片流）。
// spec：docs/superpowers/specs/2026-10-17-desktop-ui-ccswitch-redesign-design.md §3.2/§3.3。
// 已知偏离旧档（2026-10-17-desktop-ui-mygo-native-design.md）§2.3 的「一个
// 颜色想法」（Success==Accent）：状态绿与品牌橙必须分离——LED/健康语义用
// 绿、主操作用橙，同屏并存不再互相染色（新 spec §1）。
// 另沿旧档已知偏离：BuildTheme 增加 textScale 入参——框架默认主题随系统
// 文字缩放（Preferences.TextScale），自设主题必须自己接上，否则可访问性回退。
package uikit

import (
	"fmt"
	"math"
	"sort"

	"github.com/egoist/mygo/ui"
)

// MonoFont 是机器读数（地址、令牌、密钥尾、耗时等）的等宽字体族。
// 必须带 monospace 回退：Menlo 在 Linux/Windows 不存在，MyGo 会退化成
// 比例字体，导致地址/令牌列无法对齐（2026-10-29 审计 P1-12）。
const MonoFont = "Menlo, monospace"

// BuildTheme 按 dark 构造品牌主题：以框架主题为底、覆写 cc-switch spec
// §3.2 全部 19 个颜色 token 与度量（未列字段保持框架默认）。色值全部
// 硬编码 ui.Hex，浅深两边逐字段给出，不派生。textScale<=0 视为 1。
func BuildTheme(dark bool, textScale float32) *ui.Theme {
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
		t.AccentText = ui.Hex("#18181B") // 橙底深字：对比度约 7.4:1（2026-10-29 决策）
		t.InactiveAccent = t.Accent      // mygo v0.8.0 起失焦默认褪灰；托盘驻留常失焦，钉死主操作橙
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
		t.SurfaceHover = ui.Hex("#ECECF1") // 侧栏/列表 hover 需与 SidebarColor 区分
		t.SurfacePressed = ui.Hex("#EFEFF2")
		t.Selection = ui.Hex("#FFDDBB") // 文本选区保持暖色可见，不用选中灰
		t.Border = ui.Hex("#E4E4E9")
		t.Text = ui.Hex("#18181B")
		t.TextMuted = ui.Hex("#6B6B73") // 原 #71717A 在浅色侧栏上仅 4.44:1
		t.Accent = ui.Hex("#F97316")
		t.AccentHover = ui.Hex("#EA620A")
		t.AccentPressed = ui.Hex("#DD5A0B")
		t.AccentText = ui.Hex("#18181B") // 橙底深字：对比度约 7.4:1（2026-10-29 决策）
		t.InactiveAccent = t.Accent      // mygo v0.8.0 起失焦默认褪灰；托盘驻留常失焦，钉死主操作橙
		t.Success = ui.Hex("#16A34A")
		t.Danger = ui.Hex("#DC2626")
		t.Warning = ui.Hex("#B45309") // 原 #D97706 白底仅 3.19:1
		t.Focus = t.Accent            // 浅色焦点环随品牌橙
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

// SoftPalette 是软色板（spec §3.3）：每类语义一对「软底 + 前景字」，
// 浅色取浅底深字、深色取深底亮字，供软徽章/软按钮/类型 tile 复用；
// Link 是软色板里唯一的单值（链接文字无底色）。
type SoftPalette struct {
	Act, ActText       ui.Color
	Ok, OkText         ui.Color
	Warn, WarnText     ui.Color
	Danger, DangerText ui.Color
	Gray, GrayText     ui.Color
	Link               ui.Color
}

// Softs 按 t.Dark 取边返回软色板；色值全部硬编码，不派生自 token 表。
func Softs(t *ui.Theme) SoftPalette {
	if t.Dark {
		return SoftPalette{
			Act: ui.Hex("#33221A"), ActText: ui.Hex("#FB923C"),
			Ok: ui.Hex("#12261A"), OkText: ui.Hex("#4ADE80"),
			Warn: ui.Hex("#2E2410"), WarnText: ui.Hex("#FBBF24"),
			Danger: ui.Hex("#2C1516"), DangerText: ui.Hex("#F87171"),
			Gray: ui.Hex("#2E2E33"), GrayText: ui.Hex("#A1A1AA"),
			Link: ui.Hex("#5AA9F8"),
		}
	}
	return SoftPalette{
		Act: ui.Hex("#FFF1E6"), ActText: ui.Hex("#C2410C"),
		Ok: ui.Hex("#E8F7EE"), OkText: ui.Hex("#007634"),
		Warn: ui.Hex("#FEF3C7"), WarnText: ui.Hex("#A64A00"),
		Danger: ui.Hex("#FEE2E2"), DangerText: ui.Hex("#B91C1C"),
		Gray: ui.Hex("#EFEFF2"), GrayText: ui.Hex("#52525B"),
		Link: ui.Hex("#0A6CD6"),
	}
}

// SelectedColor 是应用级选中灰（导航项/列表行/分段选中片的底），中性灰、
// 与品牌橙无关；框架 Theme.Selection 只管文本选区（保持暖色），两者分工
// 不得混用（spec §3.2 注）。
func SelectedColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#3A3A40")
	}
	return ui.Hex("#E4E4EA")
}

// SidebarColor 是自绘侧栏底色：比 Background 微沉一档，撑出侧栏与内容
// 区的层级（spec §5 页面骨架）。
func SidebarColor(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#232327")
	}
	return ui.Hex("#F5F5F7")
}

// ChartColors 是图表分类色板（8 槽固定顺序，slot 1 = 蓝）。色板刻意避开
// Success 绿 / Warning 琥珀 / Danger 红——它们已被状态语义占用，不能再当
// 系列色；slot 2 取品牌橙邻色 #EA580C，与 Accent #F97316 协调但不复用
// Accent 本身（橙仍只代表主操作）。
//
// 前 5 槽（蓝/橙/青绿/紫红/洋红）用于最常见的 3–5 渠道场景，任意两色在
// 深/浅底上的常规视觉 ΔE 都 ≥ 15.4；靛最接近蓝、紫最接近紫红、青最接近
// 青绿，因此后置到 6–8 槽，只在高系列数时出现。两套色值分别针对卡片
// Surface（浅 #FFFFFF / 深 #2C2C31）校验：OKLCH 亮度带、色度下限、相邻
// 色盲 ΔE ≥ 8、常规视觉 ΔE ≥ 15、与底对比 ≥ 3:1。顺序是区分度机制，
// 不得随意重排（见 docs/design/chart-palette.md）。
func ChartColors(t *ui.Theme) []ui.Color {
	if t.Dark {
		return []ui.Color{
			ui.Hex("#3B82F6"), // 蓝
			ui.Hex("#EA580C"), // 橙（品牌邻色）
			ui.Hex("#0D9488"), // 青绿
			ui.Hex("#A855F7"), // 紫红
			ui.Hex("#EC4899"), // 洋红
			ui.Hex("#6366F1"), // 靛（近蓝，后置）
			ui.Hex("#0891B2"), // 青（近青绿，后置）
			ui.Hex("#8B5CF6"), // 紫（近紫红，后置）
		}
	}
	return []ui.Color{
		ui.Hex("#2563EB"), // 蓝
		ui.Hex("#EA580C"), // 橙（品牌邻色）
		ui.Hex("#0D9488"), // 青绿
		ui.Hex("#A21CAF"), // 紫红
		ui.Hex("#DB2777"), // 洋红
		ui.Hex("#4338CA"), // 靛（近蓝，后置）
		ui.Hex("#0891B2"), // 青（近青绿，后置）
		ui.Hex("#7C3AED"), // 紫（近紫红，后置）
	}
}

// ChartSlots 把一组系列名映射到固定色槽：按名称排序后依次取色，颜色跟随
// 实体而不是数值排名（排名变化不会换色）。调用方应把**两张图出现的全部
// 渠道名**一起传入，同一渠道在柱状图与折线图中就会同色；超过 8 个名称时
// 循环复用色板，靠直接标签/图例区分。
func ChartSlots(t *ui.Theme, names []string) map[string]ui.Color {
	palette := ChartColors(t)
	uniq := make(map[string]struct{}, len(names))
	ordered := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := uniq[n]; ok {
			continue
		}
		uniq[n] = struct{}{}
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	slots := make(map[string]ui.Color, len(ordered))
	for i, n := range ordered {
		slots[n] = palette[i%len(palette)]
	}
	return slots
}

// ChartGrid 是图表内部网格线：比 Border 更淡一档，只做读数辅助、不抢数据。
func ChartGrid(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#34343A")
	}
	return ui.Hex("#EEF0F3")
}

// ChartBaseline 是图表基线/坐标轴：比网格线略强，仍远弱于数据标记。
func ChartBaseline(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#4A4A52")
	}
	return ui.Hex("#DCDCE2")
}

// ChartAxis 是坐标轴与刻度文字色：柔和但保持可读（等同 TextMuted，浅色
// 5.1:1 / 深色 5.8:1）。
func ChartAxis(t *ui.Theme) ui.Color {
	return t.TextMuted
}

// ChartInkOn 返回叠在色块上的标签墨色：在白字与深字 #18181B 之间取对比度
// 更高的一侧，保证小号数值标签在任意系列色上都尽量可读。
func ChartInkOn(c ui.Color) ui.Color {
	white, dark := ui.Hex("#FFFFFF"), ui.Hex("#18181B")
	if contrastRatio(c, white) >= contrastRatio(c, dark) {
		return white
	}
	return dark
}

// contrastRatio 是两色的 WCAG 对比度（1..21）。
func contrastRatio(a, b ui.Color) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	lum := func(c ui.Color) float64 {
		return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
	}
	la, lb := lum(a), lum(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// StateWord 把网关状态译成面板状态词；未知状态原样回显（Review Focus 4）。
func StateWord(state string) string {
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

// ReasonWord 把网关降级/失败原因译成用户可读文案：已知内部原因给中文，
// 其余（系统错误原文，如端口占用）原样返回，便于用户排障。
func ReasonWord(reason string) string {
	if reason == "no enabled logical model or usable channel" {
		return "没有可用的逻辑模型或渠道"
	}
	return reason
}

// CooldownReasonWord 把冷却原因（provider.ErrorClass）译成用户可读中文；
// 未知原因原样返回，便于排障。
func CooldownReasonWord(reason string) string {
	switch reason {
	case "rate_limited":
		return "上游限流"
	case "quota_exhausted":
		return "额度耗尽"
	case "server":
		return "上游 5xx"
	case "connect":
		return "连接失败"
	case "auth":
		return "鉴权失败"
	case "endpoint":
		return "端点错误"
	case "param":
		return "参数错误"
	default:
		return reason
	}
}

// CooldownRemainingWord 把剩余秒数译成「45 秒 / 3 分」。
func CooldownRemainingWord(sec int) string {
	if sec >= 120 {
		return fmt.Sprintf("%d 分", (sec+59)/60)
	}
	return fmt.Sprintf("%d 秒", sec)
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

// LedDot 画状态 LED：10px 圆点，色由 stateColor 决定，常亮不呼吸。
// spec §8.4 的降级路径已启用：Loop 会调 c.AnimationFrame()，让**整个视图**
// 按显示刷新率重建（不只是这个点），实测常驻约 20% 单核——对一个 10px 装
// 饰点不划算。状态由颜色 + 底条「运行中/已降级」文字表达，不靠动效传递。
// 不要加回 Loop/Animate：需要动效时先测空闲 CPU。
func LedDot(c *ui.Context, t *ui.Theme, state string) {
	ui.Box(c).Width(10).Height(10).Radius(5).Background(stateColor(t, state))
}
