// logicalmodels.go 是逻辑模型列表页与新建/编辑页（spec §6.2 / Task 12–13）：
// 列表＝卡片流（名称、enabled 徽章、目标链摘要）+ 行内操作（编辑/删除/启用
// 停用）+ 空态引导；删除走 AlertDialog 二次确认。编辑页＝名称/描述 + 元数据
// （显示名、上下文窗口、思考等级、默认等级）+ 目标链行编辑（渠道下拉、模型名
// 输入或建议选择、上移/下移/删除、添加目标）+ 保存（Create/Update）。纯助手
// moveTarget / lmForm.validate / lmForm.meta 供测试直断言；渲染薄壳不测字符串
// 细节。
package logical

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// lmReasoningLevels 是 Codex 认可的思考等级（spec §4），与 bridge 的
// reasoningLevelWhitelist 保持一致。桌面层提前校验，错误直接进表单信息位；
// bridge 保存时仍会再校验一次（防御纵深）。
var lmReasoningLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// lmParsedLevels 把思考等级输入框（逗号分隔）解析成选项列表：去空格、
// 去空项、按首次出现去重。渲染默认等级下拉与 meta() 共用同一规则。
func lmParsedLevels(raw string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, lv := range strings.Split(raw, ",") {
		lv = strings.TrimSpace(lv)
		if lv == "" || seen[lv] {
			continue
		}
		seen[lv] = true
		out = append(out, lv)
	}
	return out
}

// meta 把表单字符串解析成桥接层元数据：上下文窗口空串=0、否则须为非负
// 整数；思考等级须在白名单内；默认等级空时取列表首项（spec §4「非空但
// 默认空 → 列表第一项」），非空须属于列表。错误信息中文、可进信息位。
func meta(f state.LMForm) (bridge.ModelMeta, error) {
	meta := bridge.ModelMeta{DisplayName: strings.TrimSpace(f.DisplayName)}
	if raw := strings.TrimSpace(f.ContextWindow); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return meta, fmt.Errorf("上下文窗口需要是非负整数，如 1000000")
		}
		meta.ContextWindow = n
	}
	known := make(map[string]bool, len(lmReasoningLevels))
	for _, lv := range lmReasoningLevels {
		known[lv] = true
	}
	meta.ReasoningLevels = lmParsedLevels(f.ReasoningLevels)
	for _, lv := range meta.ReasoningLevels {
		if !known[lv] {
			return meta, fmt.Errorf("未知思考等级「%s」，可用：%s",
				lv, strings.Join(lmReasoningLevels, "、"))
		}
	}
	def := strings.TrimSpace(f.DefaultReasoningLevel)
	if def == "" && len(meta.ReasoningLevels) > 0 {
		def = meta.ReasoningLevels[0]
	}
	inList := slices.Contains(meta.ReasoningLevels, def)
	if def != "" && !inList {
		return meta, fmt.Errorf("默认思考等级「%s」不在思考等级列表中", def)
	}
	meta.DefaultReasoningLevel = def
	return meta, nil
}

// moveTarget 把 targets[i] 与相邻行换位（delta=-1 上移 / +1 下移）；首行
// 上移、末行下移与越界下标都是 no-op。只换序不写 Priority——落库前按下标
// 重排（saveLogicalModel）。
func moveTarget(targets []bridge.TargetSpec, i, delta int) {
	j := i + delta
	if i < 0 || i >= len(targets) || j < 0 || j >= len(targets) {
		return
	}
	targets[i], targets[j] = targets[j], targets[i]
}

// validate 是保存前校验：名称必填、至少一个目标、每行渠道与模型名必填。
// 错误信息中文、进表单信息位。
func validate(f state.LMForm) error {
	if strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("需要填写模型名称")
	}
	if len(f.Targets) == 0 {
		return fmt.Errorf("至少需要一个目标")
	}
	for i, tg := range f.Targets {
		if tg.ChannelID == "" {
			return fmt.Errorf("目标 %d：需要选择渠道", i+1)
		}
		if strings.TrimSpace(tg.ModelName) == "" {
			return fmt.Errorf("目标 %d：需要填写模型名", i+1)
		}
	}
	return nil
}

// formatTargetChain 把目标链摘要成一行 `渠道A·model → 渠道B·model`（spec
// §6.2）；无目标返回 `（无目标）`。渠道名缺失时退回渠道 id。
func formatTargetChain(m bridge.ModelView) string {
	if len(m.Targets) == 0 {
		return "（无目标）"
	}
	parts := make([]string, 0, len(m.Targets))
	for _, tg := range m.Targets {
		ch := tg.ChannelName
		if ch == "" {
			ch = tg.ChannelID
		}
		parts = append(parts, ch+"·"+tg.ModelName)
	}
	return strings.Join(parts, " → ")
}

// logicalModelsView 是逻辑模型列表页主体：页头新建钮 + 卡片行列表（名称/
// 启用徽章/目标链摘要 + 编辑/删除/启用停用），空态引导「先建渠道，再把渠道
// 挂进逻辑模型」。数据每帧自 st.Svc.Logical.List() 取（进程内直调）。
func listView(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "逻辑模型").Bold().FontSize(16).Grow(1)
		if uikit.PillButton(c, t, "＋ 新建逻辑模型").Clicked() {
			openNew(st)
		}
	})
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	if len(models) == 0 {
		uikit.Card(c, t, func() {
			ui.Text(c, "先建渠道，再把渠道挂进逻辑模型").TextColor(t.TextMuted)
		})
	}
	for _, m := range models {
		uikit.Card(c, t, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.Name).Bold().Grow(1).SingleLine()
				if m.Enabled {
					uikit.SoftBadge(c, t, s, "ok", "启用")
				} else {
					uikit.SoftBadge(c, t, s, "gray", "停用")
				}
				if uikit.SoftButton(c, t, s, "gray", "编辑").Clicked() {
					openEdit(st, m.ID)
				}
				if uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
					st.Logical.DeleteArm = true
					st.Logical.DeleteID = m.ID
				}
				toggleLabel := "停用"
				if !m.Enabled {
					toggleLabel = "启用"
				}
				if uikit.SoftButton(c, t, s, "act", toggleLabel).Clicked() && st.Svc.Logical != nil {
					st.Svc.Logical.SetEnabled(m.ID, !m.Enabled)
				}
			})
			ui.Text(c, formatTargetChain(m)).TextColor(t.TextMuted)
		})
	}
	// 删除二次确认（复用渠道页两段式删除确认交互）：armed 位跨帧保留，
	// 弹窗标题带模型名、确认（下标 1）才 Remove。
	armedName := ""
	for _, m := range models {
		if m.ID == st.Logical.DeleteID {
			armedName = m.Name
		}
	}
	deleteConfirm(c, st, armedName)
}

// logicalDeleteConfirm 是删除逻辑模型的 AlertDialog 二次确认：open 位
// lmDeleteArm、对象 lmDeleteID；框架在选定后自关 open 位，Escape 走
// AlertDismissed 同样收场。按钮序「取消, 删除」——末位是主钮。
func deleteConfirm(c *ui.Context, st *state.State, name string) {
	if !st.Logical.DeleteArm {
		return
	}
	msg := "确定删除该逻辑模型？"
	if name != "" {
		msg = fmt.Sprintf("确定删除逻辑模型「%s」？", name)
	}
	switch ui.AlertDialog(c, &st.Logical.DeleteArm, "删除逻辑模型", msg, "取消", "删除") {
	case 1:
		if st.Svc.Logical != nil {
			st.Svc.Logical.Remove(st.Logical.DeleteID)
		}
		st.Logical.DeleteID = ""
	case 0, ui.AlertDismissed:
		st.Logical.DeleteID = ""
	}
}

// openLogicalNew 进新建页并复位表单（editModelID 空=新建）。
func openNew(st *state.State) {
	st.Logical.EditModelID = ""
	st.Logical.Edit = state.LMForm{}
	st.Page = "logical-edit"
}

// openLogicalEdit 进编辑页并回显表单：名称/描述/目标链取自 Logical.Get，
// 目标行 Priority 保序（保存前按下标重排）。模型已删则信息位提示。
func openEdit(st *state.State, id string) {
	st.Logical.EditModelID = id
	st.Logical.Edit = state.LMForm{}
	if id == "" {
		st.Page = "logical-edit"
		return
	}
	if st.Svc.Logical != nil {
		if m, err := st.Svc.Logical.Get(id); err != nil {
			st.Logical.Edit.Msg = "逻辑模型不存在或已删除"
		} else {
			st.Logical.Edit.Name = m.Name
			st.Logical.Edit.Description = m.Description
			st.Logical.Edit.DisplayName = m.DisplayName
			if m.ContextWindow > 0 {
				st.Logical.Edit.ContextWindow = strconv.FormatInt(m.ContextWindow, 10)
			}
			st.Logical.Edit.ReasoningLevels = strings.Join(m.ReasoningLevels, ",")
			st.Logical.Edit.DefaultReasoningLevel = m.DefaultReasoningLevel
			for _, tg := range m.Targets {
				st.Logical.Edit.Targets = append(st.Logical.Edit.Targets, bridge.TargetSpec{
					ChannelID: tg.ChannelID, ModelName: tg.ModelName, Priority: tg.Priority,
				})
			}
		}
	}
	st.Page = "logical-edit"
}

// saveLogicalModel 落「保存」：校验 → Priority 按下标重排 → 新建走 Create、
// 编辑走 Update（enabled 保持原值——表单不含启用开关）；成功回列表页，
// 失败错误进表单信息位（channels 页同款模式）。
func save(st *state.State) {
	if err := validate(st.Logical.Edit); err != nil {
		st.Logical.Edit.Msg = err.Error()
		return
	}
	if st.Svc.Logical == nil {
		st.Logical.Edit.Msg = "逻辑模型服务未就绪"
		return
	}
	modelMeta, err := meta(st.Logical.Edit)
	if err != nil {
		st.Logical.Edit.Msg = err.Error()
		return
	}
	targets := make([]bridge.TargetSpec, len(st.Logical.Edit.Targets))
	for i, tg := range st.Logical.Edit.Targets {
		targets[i] = bridge.TargetSpec{ChannelID: tg.ChannelID, ModelName: tg.ModelName, Priority: i}
	}
	name := strings.TrimSpace(st.Logical.Edit.Name)
	if st.Logical.EditModelID == "" {
		if _, err := st.Svc.Logical.Create(name, st.Logical.Edit.Description, modelMeta, targets); err != nil {
			st.Logical.Edit.Msg = err.Error()
			return
		}
	} else {
		enabled := true
		if m, err := st.Svc.Logical.Get(st.Logical.EditModelID); err == nil {
			enabled = m.Enabled
		}
		if err := st.Svc.Logical.Update(st.Logical.EditModelID, name, st.Logical.Edit.Description, modelMeta, enabled, targets); err != nil {
			st.Logical.Edit.Msg = err.Error()
			return
		}
	}
	st.Page = "logical"
}

// logicalModelEditView 是新建/编辑页（spec §6.2 / Task 13）：名称/描述 +
// 目标链行编辑（渠道下拉=Channel list、模型名输入或建议选择、上移/下移/
// 删除）+ 添加目标 + 保存。editModelID 空=新建；校验/保存错误进表单信息位，
// 保存成功回列表页（saveLogicalModel）。
func editView(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	chans := st.Svc.Channels.List()
	title := "新建逻辑模型"
	if st.Logical.EditModelID != "" {
		title = "编辑逻辑模型"
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "返回列表").Clicked() {
					st.Page = "logical"
				}
				ui.Text(c, title).Bold().FontSize(t.FontSize + 2)
			})
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Text(c, "基本信息").Bold()
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "名称:").Width(112).TextColor(t.TextMuted)
						ui.TextInput(c, &st.Logical.Edit.Name).Grow(1).Placeholder("网关对外模型名，如 my-code")
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "描述:").Width(112).TextColor(t.TextMuted)
						ui.TextInput(c, &st.Logical.Edit.Description).Grow(1)
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "显示名:").Width(112).TextColor(t.TextMuted)
						ui.TextInput(c, &st.Logical.Edit.DisplayName).Grow(1).
							Placeholder("Codex 模型菜单显示名（空 = 模型名）")
					})
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "上下文窗口:").Width(112).TextColor(t.TextMuted)
						ui.TextInput(c, &st.Logical.Edit.ContextWindow).Grow(1).
							Placeholder("如 1000000；空 = 不声明")
						if uikit.SoftButton(c, t, s, "gray", "1M").Clicked() {
							st.Logical.Edit.ContextWindow = "1000000"
						}
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "思考等级:").Width(112).TextColor(t.TextMuted)
						ui.TextInput(c, &st.Logical.Edit.ReasoningLevels).Grow(1).
							Placeholder("low,medium,high,xhigh")
					})
					ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "默认思考等级:").Width(112).TextColor(t.TextMuted)
						lmLevelSelect(c, t, &st.Logical.Edit.DefaultReasoningLevel,
							lmParsedLevels(st.Logical.Edit.ReasoningLevels))
						ui.Text(c, "空 = 模板默认；有等级时默认取首项").TextColor(t.TextMuted)
					})
				})
			})
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Text(c, "目标链").Bold()
					ui.Text(c, "按序故障转移：靠前的目标优先").TextColor(t.TextMuted)
					if len(chans) == 0 {
						ui.Text(c, "还没有渠道——先在「渠道」页添加").TextColor(t.TextMuted)
					}
					// 行内按钮只记动作，循环结束后统一应用：构建中改切片会
					// 让 range 跳行/越界。
					act, actI := "", -1
					for i := range st.Logical.Edit.Targets {
						if a := targetRow(c, t, s, st, i, chans); a != "" {
							act, actI = a, i
						}
					}
					switch act {
					case "up":
						moveTarget(st.Logical.Edit.Targets, actI, -1)
					case "down":
						moveTarget(st.Logical.Edit.Targets, actI, +1)
					case "del":
						st.Logical.Edit.Targets = append(st.Logical.Edit.Targets[:actI], st.Logical.Edit.Targets[actI+1:]...)
					}
					if uikit.SoftButton(c, t, s, "act", "＋ 添加目标").Clicked() {
						st.Logical.Edit.Targets = append(st.Logical.Edit.Targets, bridge.TargetSpec{})
					}
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				if uikit.PillButton(c, t, "保存").Clicked() {
					save(st)
				}
				ui.Box(c).Grow(1)
			})
			if st.Logical.Edit.Msg != "" {
				ui.Text(c, st.Logical.Edit.Msg).TextColor(t.Danger)
			}
		})
	})
}

// lmTargetRow 渲染一行目标编辑控件并返回行操作（""|"up"|"down"|"del"），
// 由调用方在循环后统一应用。渠道用 SelectBase 绑 ChannelID、显示渠道名；
// 模型名有建议时走建议下拉、否则文本输入。
func targetRow(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, i int, chans []bridge.ChannelView) string {
	tg := &st.Logical.Edit.Targets[i]
	act := ""
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("%d.", i+1)).Width(20).TextColor(t.TextMuted)
		lmChannelSelect(c, t, &tg.ChannelID, chans)
		sugg := st.Svc.Channels.Suggestions(tg.ChannelID)
		if len(sugg) > 0 {
			opts := sugg
			if tg.ModelName != "" && !slices.Contains(opts, tg.ModelName) {
				opts = append([]string{tg.ModelName}, sugg...)
			}
			ui.Select(c, &tg.ModelName, opts).Grow(1)
		} else {
			ui.TextInput(c, &tg.ModelName).Grow(1).Placeholder("模型名")
		}
		if uikit.SoftButton(c, t, s, "gray", "上移").Clicked() {
			act = "up"
		}
		if uikit.SoftButton(c, t, s, "gray", "下移").Clicked() {
			act = "down"
		}
		if uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
			act = "del"
		}
	})
	return act
}

// lmChannelSelect 是目标行的渠道下拉（spec §6.2「渠道下拉=Channel list」）：
// SelectBase 值绑 ChannelID，触发器与选项显示渠道名。
func lmChannelSelect(c *ui.Context, t *ui.Theme, id *string, chans []bridge.ChannelView) {
	label := "选择渠道"
	for _, ch := range chans {
		if ch.ID == *id {
			label = ch.Name
			break
		}
	}
	sel := ui.SelectBase(c, id)
	sel.Trigger.Padding(6, 10).Border(1, t.Border).Radius(t.Radius).
		Background(t.Surface).MinWidth(140).Children(func() {
		ui.Text(c, label).SingleLine()
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(4).Background(t.Surface).Border(1, t.Border).Radius(t.Radius)
		for _, ch := range chans {
			item := sel.Item(ch.ID).Padding(6, 10).Radius(t.Radius)
			switch {
			case item.Highlighted():
				item.Background(t.Accent).TextColor(t.AccentText)
			case ch.ID == *id:
				item.Background(t.SurfaceHover)
			}
			item.Children(func() { ui.Text(c, ch.Name).SingleLine() })
		}
	})
}

// lmLevelSelect 是默认思考等级下拉：值绑等级字符串，空串显示「模板默认」。
// 选项由当前已填写的等级实时解析（spec §4），首项永远是「模板默认」以便
// 清空；选中不在列表中的旧值时仍显示原值，保存时由 meta() 报错提示。
func lmLevelSelect(c *ui.Context, t *ui.Theme, level *string, levels []string) {
	label := *level
	if label == "" {
		label = "模板默认"
	}
	sel := ui.SelectBase(c, level)
	sel.Trigger.Padding(6, 10).Border(1, t.Border).Radius(t.Radius).
		Background(t.Surface).MinWidth(140).Children(func() {
		ui.Text(c, label).SingleLine()
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(4).Background(t.Surface).Border(1, t.Border).Radius(t.Radius)
		for _, lv := range append([]string{""}, levels...) {
			item := sel.Item(lv).Padding(6, 10).Radius(t.Radius)
			switch {
			case item.Highlighted():
				item.Background(t.Accent).TextColor(t.AccentText)
			case lv == *level:
				item.Background(t.SurfaceHover)
			}
			text := lv
			if text == "" {
				text = "模板默认"
			}
			item.Children(func() { ui.Text(c, text).SingleLine() })
		}
	})
}

// Page 是逻辑模型页入口（组合根 pageView 分发）：编辑态渲染新建/编辑页，
// 其余（含零值与未知 id 的兜底）渲染列表页。
func Page(c *ui.Context, st *state.State) {
	if st.Page == "logical-edit" {
		editView(c, st)
		return
	}
	listView(c, st)
}
