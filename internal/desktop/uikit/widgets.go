// widgets.go 是「交换机面板」的共享视觉词汇（spec §4 组件词汇表）：卡片、
// 软徽章、软底胶囊钮、胶囊主钮与渠道类型 tile。全部建立在 Task 1 的软色板
// （theme.go softs/SoftPalette）之上；Task 3–8 的页面按本文件签名消费，
// 签名即契约，不得改名。
package uikit

import (
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

// card 是各页容器卡片（spec §3.4「卡片一律 Radius 12」）：12 显式盖过主题
// 控件圆角 8，Surface 底 + 1px Border + 14 内边距。
func Card(c *ui.Context, t *ui.Theme, fn func()) {
	ui.Box(c).Radius(12).Background(t.Surface).Border(1, t.Border).Padding(14).Children(fn)
}

// softPair 按 kind 取软色板同组的一对（底，字）；未知 kind 落 gray——
// 调用方拼错字符串时宁可弱化为中性也不染错语义色。
func softPair(s SoftPalette, kind string) (bg, fg ui.Color) {
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
func SoftBadge(c *ui.Context, t *ui.Theme, s SoftPalette, kind, label string) {
	bg, fg := softPair(s, kind)
	ui.Box(c).Radius(999).Background(bg).TextColor(fg).Padding(2, 8).Shrink(0).
		Children(func() { ui.Text(c, label).FontSize(t.FontSize - 2) })
}

// pillButton 是胶囊主钮（spec §5.2「＋ 添加渠道」）：ButtonBase（可聚焦，
// 指针/Enter/Space 报 Clicked）之上全圆角 Accent 实心 + AccentText 字；
// 交互底色走主题现成的 AccentHover/AccentPressed token。返回 Element，
// 调用方以 if PillButton(...).Clicked() 消费（同帧有效）。
func PillButton(c *ui.Context, t *ui.Theme, label string) ui.Element {
	return capsuleButton(c, t, t.Accent, t.AccentHover, t.AccentPressed, t.AccentText, label)
}

// softButton 是软底胶囊钮（如概览「停止网关」danger 软底、密钥池「＋ 批量
// 导入」act 软底，spec §5.2/§5.3）：底与字取软色板同组；软色板没有 hover
// token，交互底色由 bg 向 fg 轻混派生（token 表本身仍全硬编码，不违
// theme.go 口径）。危险动作用 kind="danger"。
func SoftButton(c *ui.Context, t *ui.Theme, s SoftPalette, kind, label string) ui.Element {
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
func Tile(c *ui.Context, t *ui.Theme, s SoftPalette, name string, size float32) {
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

// LabelWidth 是表单标签列的统一宽度（DIP）。此前各页 80/96/110/112/120/150
// 混用，导致同一产品内标签与输入件对不齐；新代码一律用它，复杂行可另用
// FormRowRaw 自行布局。
const LabelWidth float32 = 112

// PageHeaderOpts 是统一页头的入参：标题/副标题、可选返回动作与右侧动作区。
// 返回按钮与主操作由调用方以闭包注入，页头只负责布局与层级。
type PageHeaderOpts struct {
	Title     string
	Subtitle  string
	BackLabel string
	OnBack    func()
	Meta      func() // 标题后的徽章/状态（如「使用中」）
	Actions   func()
}

// PageHeader 渲染页面唯一标题行：可选「← 返回列表」+ 标题（FontSize+3，
// 随 textScale）+ 副标题 + 弹性空白 + 右侧动作区（通常放一个主操作）。
func PageHeader(c *ui.Context, t *ui.Theme, o PageHeaderOpts) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		if o.BackLabel != "" && o.OnBack != nil {
			if ui.Button(c, o.BackLabel).Clicked() {
				o.OnBack()
			}
		}
		if o.Title != "" {
			ui.Text(c, o.Title).Bold().FontSize(t.FontSize + 3).SingleLine()
		}
		if o.Subtitle != "" {
			ui.Text(c, o.Subtitle).TextColor(t.TextMuted).SingleLine()
		}
		if o.Meta != nil {
			o.Meta()
		}
		ui.Box(c).Grow(1)
		if o.Actions != nil {
			o.Actions()
		}
	})
}

// SectionTitle 渲染卡片内区块标题 + 可选说明（说明用 TextMuted）。
func SectionTitle(c *ui.Context, t *ui.Theme, title, hint string) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, title).Bold()
		if hint != "" {
			ui.Text(c, hint).TextColor(t.TextMuted)
		}
	})
}

// FormRow 渲染统一表单行：固定宽标签 + 输入件，并把标签关联到输入件
// （供读屏器使用）。输入件以闭包返回，便于调用方决定宽度/绑定。
func FormRow(c *ui.Context, t *ui.Theme, label string, field func() ui.Element) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).Width(LabelWidth).TextColor(t.TextMuted)
		field().Label(label)
	})
}

// FormRowRaw 是 FormRow 的多控件版本：标签只做视觉对齐，不强行关联某一个
// 控件（用于「启用 + 允许私网」这类一行多开关）。
func FormRowRaw(c *ui.Context, t *ui.Theme, label string, field func()) {
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		if label != "" {
			ui.Text(c, label).Width(LabelWidth).TextColor(t.TextMuted)
		} else {
			ui.Box(c).Width(LabelWidth)
		}
		field()
	})
}

// BusyRow 渲染进行中状态：Spinner + 进行中文案 + 可选「取消」按钮。
// Spinner 只在任务进行中挂载（MyGo 只重绘该控件，不重建整视图）。
func BusyRow(c *ui.Context, t *ui.Theme, label string, onCancel func()) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Spinner(c).Label(label)
		ui.Text(c, label).TextColor(t.TextMuted)
		if onCancel != nil {
			if SoftButton(c, t, Softs(t), "gray", "取消").Clicked() {
				onCancel()
			}
		}
	})
}

// FeedbackKind 是统一反馈的语义：成功/警告/错误/中性。
type FeedbackKind int

const (
	FeedbackNeutral FeedbackKind = iota
	FeedbackSuccess
	FeedbackWarning
	FeedbackError
)

// Feedback 渲染软底反馈条：成功=软绿、警告=软琥珀、错误=软红、中性=软灰。
// 空白文本不渲染；成功/失败不再共用同一 muted 文本样式。
func Feedback(c *ui.Context, t *ui.Theme, kind FeedbackKind, text string) {
	if strings.TrimSpace(text) == "" {
		return
	}
	s := Softs(t)
	var bg, fg ui.Color
	switch kind {
	case FeedbackSuccess:
		bg, fg = s.Ok, s.OkText
	case FeedbackWarning:
		bg, fg = s.Warn, s.WarnText
	case FeedbackError:
		bg, fg = s.Danger, s.DangerText
	default:
		bg, fg = s.Gray, s.GrayText
	}
	ui.Box(c).Radius(8).Background(bg).Padding(8, 10).Children(func() {
		ui.Text(c, text).TextColor(fg)
	})
}

// InferFeedback 把既有字符串消息过渡性地归到反馈语义：错误关键词优先，
// 其次警告、成功，其余中性。新代码应直接传 FeedbackKind；此函数用于逐步
// 迁移历史 Msg 字段，避免一次性改遍所有状态结构。
func InferFeedback(text string) FeedbackKind {
	for _, k := range []string{"失败", "错误", "无法", "不存在", "无效", "非法", "冲突"} {
		if strings.Contains(text, k) {
			return FeedbackError
		}
	}
	for _, k := range []string{"警告", "过期", "未运行", "请先", "需要", "未生效", "未应用"} {
		if strings.Contains(text, k) {
			return FeedbackWarning
		}
	}
	for _, k := range []string{"已保存", "已应用", "已复制", "已删除", "已还原", "成功", "正常"} {
		if strings.Contains(text, k) {
			return FeedbackSuccess
		}
	}
	return FeedbackNeutral
}

// EmptyStateOpts 是统一空态入参：标题、说明、可选主操作与身份 tile 文本。
type EmptyStateOpts struct {
	Title       string
	Hint        string
	ActionLabel string
	OnAction    func()
	Tile        string
}

// EmptyState 渲染统一空态：Card 内居中（不套第二层 Card），一个标题、一句
// 下一步说明、最多一个主操作。列表页与设置页共用，避免「卡中卡 / 无卡 /
// 纯文本」三种空态并存。
func EmptyState(c *ui.Context, t *ui.Theme, o EmptyStateOpts) {
	Card(c, t, func() { EmptyStateBare(c, t, o) })
}

// EmptyStateBare 是空态的无卡片版本：已处在卡片内的区域（如接入页的
// 「实际配置」卡）使用它，避免卡中卡的双层留白。
func EmptyStateBare(c *ui.Context, t *ui.Theme, o EmptyStateOpts) {
	ui.Column(c).Gap(10).AlignItems(ui.Center).Children(func() {
		if o.Tile != "" {
			Tile(c, t, Softs(t), o.Tile, 40)
		}
		if o.Title != "" {
			ui.Text(c, o.Title).Bold()
		}
		if o.Hint != "" {
			ui.Text(c, o.Hint).TextColor(t.TextMuted)
		}
		if o.ActionLabel != "" && o.OnAction != nil {
			if PillButton(c, t, o.ActionLabel).Clicked() {
				o.OnAction()
			}
		}
	})
}

// RichDialogAction 是 RichDialog 的一个动作按钮。Danger 使用软红底
// （危险动作应放在 Actions 末位）；Primary 使用品牌橙主钮；Default 标记
// 安全动作（取消/继续编辑）作为初始焦点。点击后对话框先关闭，再执行
// OnClick，便于回调里打开下一个对话框。
type RichDialogAction struct {
	Label   string
	Danger  bool
	Primary bool
	Default bool
	OnClick func()
}

// RichDialogOpts 是 RichDialog 的入参：标题、可选副标题、任意 body 视图
// 与 N 个动作按钮。Body 直接渲染在标题与动作行之间，可放路径、chip、
// 徽章等富内容。
type RichDialogOpts struct {
	Open     *bool
	Title    string
	Subtitle string
	Body     func()
	Actions  []RichDialogAction
	Width    float32
}

// RichDialog 是 ConfirmDialog 的富内容版本：复用同一 DialogBase 壳、遮罩
// 与焦点管理，但 body 可任意组合控件、动作数量不限。安全默认不变——未
// 显式标记 Default 时，焦点落在第一个非危险动作；危险动作按调用方顺序
// 渲染（应放末位）。ConfirmDialog 保持原样，现有调用方零改动。
func RichDialog(c *ui.Context, t *ui.Theme, o RichDialogOpts) {
	if o.Open == nil || !*o.Open {
		return
	}
	width := o.Width
	if width <= 0 {
		width = 480
	}
	focusIdx := -1
	for i, action := range o.Actions {
		if action.Default {
			focusIdx = i
			break
		}
	}
	if focusIdx == -1 {
		for i, action := range o.Actions {
			if !action.Danger {
				focusIdx = i
				break
			}
		}
	}
	if focusIdx == -1 && len(o.Actions) > 0 {
		focusIdx = 0
	}
	s := Softs(t)
	ui.DialogBase(c, o.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.45))
		panel.Width(width).Radius(12).Background(t.Surface).Border(1, t.Border).
			Padding(t.Space(5)).Children(func() {
			ui.Column(c).Gap(t.Space(3)).Children(func() {
				ui.Text(c, o.Title).Bold().FontSize(t.FontSize + 2)
				if o.Subtitle != "" {
					ui.Text(c, o.Subtitle).TextColor(t.TextMuted)
				}
				if o.Body != nil {
					o.Body()
				}
				ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
					for i, action := range o.Actions {
						var btn ui.Element
						switch {
						case action.Danger:
							btn = SoftButton(c, t, s, "danger", action.Label)
						case action.Primary:
							btn = PillButton(c, t, action.Label)
						default:
							btn = SoftButton(c, t, s, "gray", action.Label)
						}
						if i == focusIdx {
							btn.AutoFocus()
						}
						if btn.Clicked() {
							*o.Open = false
							if action.OnClick != nil {
								action.OnClick()
							}
						}
					}
				})
			})
		})
	})
}

// ConfirmOpts 是统一确认框入参。Danger 为真时确认按钮用软红底，不再使用
// 品牌橙主样式；取消按钮恒居末位并自动聚焦，Enter 只会触发取消。
type ConfirmOpts struct {
	Open         *bool
	Title        string
	Message      string
	CancelLabel  string
	ConfirmLabel string
	Danger       bool
	OnConfirm    func()
}

// ConfirmDialog 是安全默认的确认框：自绘 DialogBase（卡片同款 Radius 12），
// 取消在视觉末端且 AutoFocus，危险动作必须显式点击。取代各处直接调用
// ui.AlertDialog 的写法——后者把末位按钮设为 Accent + AutoFocus + Enter，
// 会让「删除/停止」成为默认动作。
func ConfirmDialog(c *ui.Context, t *ui.Theme, o ConfirmOpts) {
	if o.Open == nil || !*o.Open {
		return
	}
	s := Softs(t)
	cancelLabel := o.CancelLabel
	if cancelLabel == "" {
		cancelLabel = "取消"
	}
	confirmLabel := o.ConfirmLabel
	if confirmLabel == "" {
		confirmLabel = "确定"
	}
	ui.DialogBase(c, o.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.45))
		panel.Width(420).Radius(12).Background(t.Surface).Border(1, t.Border).
			Padding(t.Space(5)).Children(func() {
			ui.Column(c).Gap(t.Space(3)).Children(func() {
				ui.Text(c, o.Title).Bold().FontSize(t.FontSize + 2)
				if o.Message != "" {
					ui.Text(c, o.Message).TextColor(t.TextMuted)
				}
				ui.Row(c).Gap(t.Space(2)).Justify(ui.End).Children(func() {
					var confirm ui.Element
					if o.Danger {
						confirm = SoftButton(c, t, s, "danger", confirmLabel)
					} else {
						confirm = PillButton(c, t, confirmLabel)
					}
					cancel := SoftButton(c, t, s, "gray", cancelLabel).AutoFocus()
					if confirm.Clicked() {
						*o.Open = false
						if o.OnConfirm != nil {
							o.OnConfirm()
						}
					}
					if cancel.Clicked() {
						*o.Open = false
					}
				})
			})
		})
	})
}
