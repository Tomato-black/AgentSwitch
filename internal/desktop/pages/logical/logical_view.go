// logical_view.go 是逻辑模型页的渲染：卡片列表（启用徽章/目标链摘要 +
// 行内操作/删除确认）与新建/编辑页（元数据表单、目标链行编辑与下拉）。
package logical

import (
	"context"
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
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	chans := st.Svc.Channels.List()
	cool := coolingChannels(st)
	ui.Column(c).Gap(12).Grow(1).Children(func() {
		uikit.PageHeader(c, t, uikit.PageHeaderOpts{
			Title: "逻辑模型", Subtitle: fmt.Sprintf("%d 个", len(models)),
			Actions: func() {
				if uikit.PillButton(c, t, "＋ 新建逻辑模型").Clicked() {
					openNew(st)
				}
			},
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				ui.Text(c, "「测试模型」会按目标链发送最小请求，可能产生少量费用；不会修改冷却或请求统计").TextColor(t.TextMuted)
				uikit.Feedback(c, t, uikit.InferFeedback(st.Logical.TestMsg), st.Logical.TestMsg)
				if len(models) == 0 {
					if len(chans) > 0 {
						uikit.EmptyState(c, t, uikit.EmptyStateOpts{
							Title: "还没有逻辑模型", Hint: "把已建渠道挂进逻辑模型",
							ActionLabel: "＋ 新建逻辑模型", OnAction: func() { openNew(st) },
						})
					} else {
						uikit.EmptyState(c, t, uikit.EmptyStateOpts{
							Title: "还没有逻辑模型", Hint: "先建渠道，再把渠道挂进逻辑模型",
							ActionLabel: "去添加渠道",
							OnAction: func() {
								st.Go("channels", nil)
							},
						})
					}
				}
				if len(models) > 8 {
					ui.TextInput(c, &st.Logical.Filter).Grow(1).
						Placeholder("搜索逻辑模型：名称 / 描述 / 目标链")
				}
				filter := strings.ToLower(strings.TrimSpace(st.Logical.Filter))
				visible := make([]bridge.ModelView, 0, len(models))
				for _, m := range models {
					if filter == "" || strings.Contains(strings.ToLower(
						m.Name+" "+m.Description+" "+formatTargetChain(m)), filter) {
						visible = append(visible, m)
					}
				}
				if filter != "" && len(visible) == 0 {
					ui.Text(c, "没有匹配的逻辑模型").TextColor(t.TextMuted)
				}
				for _, m := range visible {
					model := m
					uikit.Card(c, t, func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, model.Name).Bold().Grow(1).SingleLine()
							if model.Enabled {
								uikit.SoftBadge(c, t, s, "ok", "启用")
							} else {
								uikit.SoftBadge(c, t, s, "gray", "停用")
							}
							if issue := targetIssue(model); issue != "" {
								uikit.SoftBadge(c, t, s, "warn", issue)
							}
							if modelCooling(model, cool) {
								uikit.SoftBadge(c, t, s, "warn", "冷却中")
							}
							switch {
							case st.Logical.TestTask.IsRunning() && st.Logical.TestModelID == model.ID:
								_, busyLabel, _ := st.Logical.TestTask.State()
								uikit.BusyRow(c, t, busyLabel, nil)
							case st.Logical.TestTask.IsRunning():
								uikit.SoftButton(c, t, s, "gray", "测试模型").Disabled(true)
							default:
								if uikit.SoftButton(c, t, s, "gray", "测试模型").Clicked() {
									id, name := model.ID, model.Name
									st.Logical.TestModelID = id
									state.RunAsync(st, &st.Logical.TestTask,
										"正在测试（会发送最小请求，可能产生少量费用）…",
										func(ctx context.Context) (bridge.ModelProbeResult, error) {
											return st.Svc.Gateway.TestLogicalModel(ctx, name)
										}, func(res bridge.ModelProbeResult, err error) {
											st.Logical.TestModelID = ""
											if err != nil {
												st.Logical.TestMsg = "测试失败：" + err.Error()
												return
											}
											if st.Logical.TestResults == nil {
												st.Logical.TestResults = map[string]bridge.ModelProbeResult{}
											}
											st.Logical.TestResults[id] = res
											st.Logical.TestMsg = ""
										})
								}
							}
							if uikit.SoftButton(c, t, s, "gray", "编辑").Clicked() {
								openEdit(st, model.ID)
							}
							if uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
								st.Logical.DeleteArm = true
								st.Logical.DeleteID = model.ID
								st.Logical.DeleteRefs = accessRefNames(st, model.ID)
							}
							toggleLabel := "停用"
							if !model.Enabled {
								toggleLabel = "启用"
							}
							if uikit.SoftButton(c, t, s, "act", toggleLabel).Clicked() && st.Svc.Logical != nil {
								st.Svc.Logical.SetEnabled(model.ID, !model.Enabled)
							}
						})
						ui.Text(c, formatTargetChain(model)).TextColor(t.TextMuted)
						if res, ok := st.Logical.TestResults[model.ID]; ok {
							probeResultView(c, t, res)
						}
					})
				}
			})
		})
	})
	// 删除二次确认（复用渠道页两段式删除确认交互）：armed 位跨帧保留，
	// 弹窗标题带模型名、确认才 Remove。
	armedName := ""
	for _, m := range models {
		if m.ID == st.Logical.DeleteID {
			armedName = m.Name
		}
	}
	deleteConfirm(c, st, armedName)
}

// deleteConfirm 是删除逻辑模型的安全确认：取消居末位并自动聚焦，危险动作
// 必须显式点击；标题带模型名，被接入配置引用时列出引用者。
func deleteConfirm(c *ui.Context, st *state.State, name string) {
	msg := "确定删除该逻辑模型？"
	if name != "" {
		msg = fmt.Sprintf("确定删除逻辑模型「%s」？", name)
	}
	if len(st.Logical.DeleteRefs) > 0 {
		msg += "\n仍被接入配置引用：" + strings.Join(st.Logical.DeleteRefs, "、") +
			"。删除后这些配置需改绑或重建。"
	}
	uikit.ConfirmDialog(c, c.Theme(), uikit.ConfirmOpts{
		Open: &st.Logical.DeleteArm, Title: "删除逻辑模型", Message: msg,
		CancelLabel: "取消", ConfirmLabel: "删除", Danger: true,
		OnConfirm: func() {
			if st.Svc.Logical != nil {
				st.Svc.Logical.Remove(st.Logical.DeleteID)
			}
			st.Logical.DeleteID = ""
			st.Logical.DeleteRefs = nil
		},
	})
	if !st.Logical.DeleteArm {
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
	subtitle := ""
	if st.Logical.EditModelID != "" {
		title = "编辑逻辑模型"
		subtitle = st.Logical.Edit.Name
	}
	ui.Column(c).Gap(12).Grow(1).Children(func() {
		uikit.PageHeader(c, t, uikit.PageHeaderOpts{
			Title: title, Subtitle: subtitle, BackLabel: "← 返回列表",
			OnBack: func() { st.Go("logical", nil) },
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				uikit.Card(c, t, func() {
					ui.Column(c).Gap(12).Children(func() {
						uikit.SectionTitle(c, t, "基本信息", "")
						uikit.FormRow(c, t, "名称:", func() ui.Element {
							return ui.TextInput(c, &st.Logical.Edit.Name).Grow(1).
								Placeholder("网关对外模型名，如 my-code")
						})
						uikit.FormRow(c, t, "描述:", func() ui.Element {
							return ui.TextInput(c, &st.Logical.Edit.Description).Grow(1)
						})
					})
				})
				uikit.Card(c, t, func() {
					ui.Column(c).Gap(12).Children(func() {
						uikit.SectionTitle(c, t, "目标链", "按序故障转移：靠前的目标优先")
						if len(chans) == 0 {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "还没有渠道——先在「渠道」页添加").TextColor(t.TextMuted)
								if uikit.SoftButton(c, t, s, "gray", "去添加渠道").Clicked() {
									st.Go("channels", nil)
								}
							})
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
				uikit.Feedback(c, t, uikit.FeedbackError, st.Logical.Edit.Msg)
			})
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

// probeResultView 渲染一次「测试模型」结果：摘要 + 每个目标的成功/失败、
// 耗时、错误分类与下一步。错误不包含密钥或上游正文。
func probeResultView(c *ui.Context, t *ui.Theme, res bridge.ModelProbeResult) {
	ui.Column(c).Gap(2).Children(func() {
		color := t.Danger
		if res.OK {
			color = t.Success
		}
		ui.Text(c, res.Summary).TextColor(color).SingleLine()
		for _, tg := range res.Targets {
			prefix, col := "✗", t.Danger
			if tg.OK {
				prefix, col = "✓", t.Success
			}
			line := fmt.Sprintf("%s %s·%s %dms", prefix, tg.ChannelName, tg.Model, tg.DurationMs)
			if tg.ErrorClass != "" {
				line += " · " + tg.ErrorClass
			}
			if tg.NextStep != "" {
				line += " · " + tg.NextStep
			}
			if tg.Cooling {
				line += " · 冷却中"
			}
			ui.Text(c, line).TextColor(col).SingleLine()
		}
	})
}
