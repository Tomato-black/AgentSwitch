// logical_view.go 是逻辑模型页的渲染：卡片列表（启用徽章/目标链摘要 +
// 行内操作/删除确认）与新建/编辑页（元数据表单、目标链行编辑与下拉）。
package logical

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

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
			ui.Column(c).Gap(10).AlignItems(ui.Center).Padding(24).Children(func() {
				ui.Text(c, "还没有逻辑模型").Bold()
				ui.Text(c, "先建渠道，再把渠道挂进逻辑模型").TextColor(t.TextMuted)
				if uikit.PillButton(c, t, "去添加渠道").Clicked() {
					st.Page = "channels"
					st.Channels.View = ""
				}
			})
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
				if hasDanglingTarget(m) {
					uikit.SoftBadge(c, t, s, "warn", "目标失效")
				}
				if uikit.SoftButton(c, t, s, "gray", "编辑").Clicked() {
					openEdit(st, m.ID)
				}
				if uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
					st.Logical.DeleteArm = true
					st.Logical.DeleteID = m.ID
					st.Logical.DeleteRefs = accessRefNames(st, m.ID)
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
	if len(st.Logical.DeleteRefs) > 0 {
		msg += "\n仍被接入配置引用：" + strings.Join(st.Logical.DeleteRefs, "、") +
			"。删除后这些配置需改绑或重建。"
	}
	switch ui.AlertDialog(c, &st.Logical.DeleteArm, "删除逻辑模型", msg, "取消", "删除") {
	case 1:
		if st.Svc.Logical != nil {
			st.Svc.Logical.Remove(st.Logical.DeleteID)
		}
		st.Logical.DeleteID = ""
		st.Logical.DeleteRefs = nil
	case 0, ui.AlertDismissed:
		st.Logical.DeleteID = ""
		st.Logical.DeleteRefs = nil
	}
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

// accessRefNames 列出绑定到该逻辑模型的接入配置（名称 + Agent 标签），
// 供删除确认提示引用面。
func accessRefNames(st *state.State, modelID string) []string {
	acc := bridge.AccessConfigs{A: st.Svc.Gateway.A}
	var out []string
	for _, app := range []string{"claude", "codex", "claude_desktop"} {
		for _, v := range acc.List(app) {
			if v.LogicalModelID == modelID {
				out = append(out, v.Name+"（"+appLabel(app)+"）")
			}
		}
	}
	return out
}

// appLabel 把接入 appType 译成 UI 显示名。
func appLabel(app string) string {
	switch app {
	case "claude":
		return "Claude Code"
	case "codex":
		return "Codex"
	case "claude_desktop":
		return "Claude Desktop"
	}
	return app
}
