// widgets.go 是「交换机面板」的共享视觉词汇（spec §4 组件词汇表）：卡片、
// 软徽章、软底胶囊钮、胶囊主钮与渠道类型 tile。全部建立在 Task 1 的软色板
// （theme.go softs/softPalette）之上；Task 3–8 的页面按本文件签名消费，
// 签名即契约，不得改名。
package desktop

import (
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// card 是各页容器卡片（spec §3.4「卡片一律 Radius 12」）：12 显式盖过主题
// 控件圆角 8，Surface 底 + 1px Border + 14 内边距。
func card(c *ui.Context, t *ui.Theme, fn func()) {
	ui.Box(c).Radius(12).Background(t.Surface).Border(1, t.Border).Padding(14).Children(fn)
}

// softPair 按 kind 取软色板同组的一对（底，字）；未知 kind 落 gray——
// 调用方拼错字符串时宁可弱化为中性也不染错语义色。
func softPair(s softPalette, kind string) (bg, fg ui.Color) {
	switch kind {
	case "act":
		return s.Act, s.ActText
	case "ok":
		return s.Ok, s.OkText
	case "warn":
		return s.Warn, s.WarnText
	case "danger":
		return s.Danger, s.DangerText
	default:
		return s.Gray, s.GrayText
	}
}

// softBadge 是软底胶囊徽章（首选/启用/冷却/停用/计数，spec §5.2）：999
// 全圆底 + 小一号字（FontSize-2），底与字取软色板同组。
func softBadge(c *ui.Context, t *ui.Theme, s softPalette, kind, label string) {
	bg, fg := softPair(s, kind)
	ui.Box(c).Radius(999).Background(bg).TextColor(fg).Padding(2, 8).Shrink(0).
		Children(func() { ui.Text(c, label).FontSize(t.FontSize - 2) })
}

// pillButton 是胶囊主钮（spec §5.2「＋ 添加渠道」）：ButtonBase（可聚焦，
// 指针/Enter/Space 报 Clicked）之上全圆角 Accent 实心 + AccentText 字；
// 交互底色走主题现成的 AccentHover/AccentPressed token。返回 Element，
// 调用方以 if pillButton(...).Clicked() 消费（同帧有效）。
func pillButton(c *ui.Context, t *ui.Theme, label string) ui.Element {
	return capsuleButton(c, t, t.Accent, t.AccentHover, t.AccentPressed, t.AccentText, label)
}

// softButton 是软底胶囊钮（如概览「停止网关」danger 软底、密钥池「＋ 批量
// 导入」act 软底，spec §5.2/§5.3）：底与字取软色板同组；软色板没有 hover
// token，交互底色由 bg 向 fg 轻混派生（token 表本身仍全硬编码，不违
// theme.go 口径）。危险动作用 kind="danger"。
func softButton(c *ui.Context, t *ui.Theme, s softPalette, kind, label string) ui.Element {
	bg, fg := softPair(s, kind)
	return capsuleButton(c, t, bg, bg.Mix(fg, 0.10), bg.Mix(fg, 0.22), fg, label)
}

// capsuleButton 是两个胶囊钮共用的底座：ButtonBase + 999 全圆 + 字色 +
// 框架按钮同款内边距比例（styleButton：Space 1.5×3.5）；hover/pressed 底
// 色按当帧指针状态换入（styleFn 未公开，同效做法是逐帧查 Hovered/Pressed）。
func capsuleButton(c *ui.Context, t *ui.Theme, bg, hover, pressed, fg ui.Color, label string) ui.Element {
	b := ui.ButtonBase(c)
	actual := bg
	if b.Pressed() {
		actual = pressed
	} else if b.Hovered() {
		actual = hover
	}
	return b.Radius(999).Background(actual).TextColor(fg).
		Padding(t.Space(1.5), t.Space(3.5)).
		Children(func() { ui.Text(c, label).SingleLine() })
}

// tileRot 是 tile 的轮转色组：软色板之外按名称哈希取的固定（底，字）对，
// 浅深两边各一套，与软色板同族浅底深字/深底亮字。
type tileRot struct {
	bgL, fgL, bgD, fgD ui.Color
}

// tileRots 是三组轮转色（蓝/紫/青，spec §4「自定义=蓝/紫/青按名称哈希取
// 一组预设」）；与软色板 act/ok 合计恰五组，到「不超过五组防彩虹」上限。
var tileRots = [3]tileRot{
	{ui.Hex("#EAF2FE"), ui.Hex("#1D4ED8"), ui.Hex("#16233B"), ui.Hex("#60A5FA")}, // 蓝
	{ui.Hex("#F3EEFD"), ui.Hex("#7C3AED"), ui.Hex("#241A38"), ui.Hex("#C084FC")}, // 紫
	{ui.Hex("#E6F7F6"), ui.Hex("#0F766E"), ui.Hex("#0F2A28"), ui.Hex("#2DD4BF")}, // 青
}

// tileSoft 把渠道名/类型 id 译成 tile 底色 kind：含 anthropic/claude →
// act（软橙），含 openai/gpt → ok（软绿）；其余 fnv32a(name)%3 轮转
// rot0/rot1/rot2 三组固定色。匹配大小写不敏感（用户起的渠道名大小写
// 任意），同输入恒同输出（tile 底色不许逐帧漂移）。
func tileSoft(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "anthropic") || strings.Contains(n, "claude"):
		return "act"
	case strings.Contains(n, "openai") || strings.Contains(n, "gpt"):
		return "ok"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return "rot" + strconv.FormatUint(uint64(h.Sum32()%3), 10)
}

// tile 是渠道类型色块（渠道卡图标位，spec §5.2「40px 类型 tile」）：size
// 见方、Radius=size/4 圆角方块，居中名称前 2 字符缩写（按 rune 截，中文
// 渠道名不碎字）；字号随 size 缩放（40px tile ≈ 14）。
func tile(c *ui.Context, t *ui.Theme, s softPalette, name string, size float32) {
	var bg, fg ui.Color
	switch kind := tileSoft(name); {
	case kind == "act":
		bg, fg = s.Act, s.ActText
	case kind == "ok":
		bg, fg = s.Ok, s.OkText
	case kind == "rot0" || kind == "rot1" || kind == "rot2":
		r := tileRots[kind[3]-'0']
		bg, fg = r.bgL, r.fgL
		if t.Dark {
			bg, fg = r.bgD, r.fgD
		}
	default:
		bg, fg = s.Gray, s.GrayText
	}
	abbr := []rune(name)
	if len(abbr) > 2 {
		abbr = abbr[:2]
	}
	ui.Box(c).Size(size, size).Radius(size * 0.25).Background(bg).Shrink(0).Center().
		Children(func() { ui.Text(c, string(abbr)).TextColor(fg).FontSize(size * 0.35).Bold() })
}
