package channels

import (
	"context"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/presets"
)

// channelDetailView 是详情页：连接配置（协议可编辑、Key 更新框留空=不改）+
// 建议模型多行编辑 + 测试连通 + 删除（引用警告）。
func detailView(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	d, err := st.Svc.Channels.Get(st.Channels.DetailID)
	if err != nil {
		ui.Column(c).Gap(12).Children(func() {
			uikit.Feedback(c, t, uikit.FeedbackError, "渠道不存在或已删除")
			if ui.Button(c, "返回列表").Clicked() {
				st.Channels.View = ""
			}
		})
		return
	}
	dirty := st.Channels.Signature() != st.Channels.Baseline
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			uikit.PageHeader(c, t, uikit.PageHeaderOpts{
				Title: d.Name, BackLabel: "← 返回列表",
				OnBack: func() { st.Go("channels", func() { st.Channels.View = "" }) },
				Meta: func() {
					if d.HasKey {
						uikit.SoftBadge(c, t, s, "act", "有 Key")
					} else {
						uikit.SoftBadge(c, t, s, "warn", "无 Key")
					}
				},
			})
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					uikit.SectionTitle(c, t, "连接配置", "")
					uikit.FormRow(c, t, "名称:", func() ui.Element {
						return ui.TextInput(c, &st.Channels.Form.Name).Grow(1)
					})
					uikit.FormRow(c, t, "Base URL:", func() ui.Element {
						return ui.TextInput(c, &st.Channels.Form.BaseURL).Grow(1)
					})
					uikit.FormRow(c, t, "协议:", func() ui.Element {
						// 协议可编辑（2026-10-28 修订，偏离 2026-10-23 spec §5
						// 「创建后不可变」）：切换联动与新建页同款（手改保护见
						// switchProtocol）；自定义渠道（presetID 查不到）只切协议。
						sel := ui.Select(c, &st.Channels.Form.Protocol, []string{"openai", "anthropic"})
						if sel.Changed() {
							if p, ok := presets.ById(st.Channels.Form.PresetID); ok {
								detailProtocolSwitched(st, p)
							}
						}
						return sel
					})
					uikit.FormRow(c, t, "API Key:", func() ui.Element {
						return ui.TextInput(c, &st.Channels.KeyInput).Grow(1).Placeholder("留空保持不变")
					})
					uikit.FormRowRaw(c, t, "", func() {
						ui.Text(c, "启用").TextColor(t.TextMuted)
						ui.Switch(c, &st.Channels.Form.Enabled)
						ui.Box(c).Width(12)
						ui.Text(c, "允许私网上游").TextColor(t.TextMuted)
						ui.Switch(c, &st.Channels.Form.AllowPrivate)
					})
				})
			})
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "建议模型").Bold().Grow(1)
						if st.Channels.FetchTask.IsRunning() {
							uikit.BusyRow(c, t, st.Channels.FetchTask.BusyLabel(), st.Channels.FetchTask.Cancel)
						} else {
							btn := uikit.SoftButton(c, t, s, "act", "拉取模型")
							if dirty {
								btn.Disabled(true)
							} else if btn.Clicked() {
								fetchModels(st)
							}
						}
					})
					ui.Text(c, "逗号或换行分隔；逻辑模型目标编辑的下拉数据源").TextColor(t.TextMuted)
					ui.TextArea(c, &st.Channels.SuggText).Grow(1).MinHeight(0).Height(96).
						Font(uikit.MonoFont)
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				if uikit.PillButton(c, t, "保存").Clicked() {
					saveDetail(st)
				}
				if st.Channels.TestTask.IsRunning() {
					uikit.BusyRow(c, t, st.Channels.TestTask.BusyLabel(), st.Channels.TestTask.Cancel)
				} else {
					btn := uikit.SoftButton(c, t, s, "act", "测试连通")
					if dirty {
						btn.Disabled(true)
					} else if btn.Clicked() {
						testDetail(st)
					}
				}
				if st.Channels.BalanceTask.IsRunning() {
					uikit.BusyRow(c, t, st.Channels.BalanceTask.BusyLabel(), st.Channels.BalanceTask.Cancel)
				} else if balanceBtn := uikit.SoftButton(c, t, s, "gray", "查余额"); balanceBtn.Clicked() {
					state.RunAsync(st, &st.Channels.BalanceTask, "正在查询余额…",
						func(ctx context.Context) (bridge.BalanceResult, error) {
							return st.Svc.Channels.BalanceContext(ctx, d.ID), nil
						}, func(res bridge.BalanceResult, err error) {
							if err != nil {
								st.Channels.BalanceMsg = "查询失败：" + err.Error()
								return
							}
							st.Channels.BalanceMsg = balanceText(res)
						})
				}
				copyBtn := uikit.SoftButton(c, t, s, "gray", "复制渠道")
				if dirty {
					copyBtn.Disabled(true)
				} else if copyBtn.Clicked() {
					v, err := st.Svc.Channels.Copy(d.ID, false)
					if err != nil {
						st.Channels.TestMsg = "复制失败：" + err.Error()
					} else {
						msg := "已复制渠道（不含 Key）"
						if st.Svc.Logical != nil {
							if models, err := st.Svc.Logical.AddCopyToReferencing(d.ID, v.ID); err == nil && len(models) > 0 {
								msg += "；已加入目标链：" + strings.Join(models, "、")
							}
						}
						openDetail(st, v.ID)
						st.Channels.TestMsg = msg + "；请填写新 Key 后保存"
					}
				}
				ui.Box(c).Grow(1)
				if uikit.SoftButton(c, t, s, "danger", "删除渠道").Clicked() {
					st.Channels.DeleteID, st.Channels.DeleteArm = d.ID, true
				}
			})
			uikit.Feedback(c, t, uikit.InferFeedback(st.Channels.TestMsg), st.Channels.TestMsg)
			uikit.Feedback(c, t, uikit.InferFeedback(st.Channels.BalanceMsg), st.Channels.BalanceMsg)
			deleteConfirm(c, st, d)
		})
	})
}

// deleteConfirm 是删除渠道的安全确认：消息带引用警告（仍被逻辑模型引用
// 则列出引用者）；取消居末位并自动聚焦，危险动作必须显式点击。
func deleteConfirm(c *ui.Context, st *state.State, d bridge.ChannelDetail) {
	if st.Channels.DeleteID != d.ID {
		return
	}
	if !st.Channels.DeleteArm {
		st.Channels.DeleteID = ""
		return
	}
	msg := d.Name
	if refs := refNames(st, d.ID); len(refs) > 0 {
		msg = fmt.Sprintf("%s\n仍被逻辑模型引用：%s。删除后这些目标将失效。", d.Name, strings.Join(refs, "、"))
	}
	uikit.ConfirmDialog(c, c.Theme(), uikit.ConfirmOpts{
		Open: &st.Channels.DeleteArm, Title: "删除渠道", Message: msg,
		CancelLabel: "取消", ConfirmLabel: "确认删除", Danger: true,
		OnConfirm: func() {
			st.Svc.Channels.Remove(st.Channels.DeleteID)
			st.Channels.DeleteID = ""
			st.Navigate("channels", func() { st.Channels.View = "" })
			st.Channels.TestMsg = "已删除 " + d.Name
		},
	})
}

// balanceText 把余额查询结果压成一行提示；不支持时展示供应商说明，不伪造数字。
func balanceText(res bridge.BalanceResult) string {
	if !res.Supported {
		return res.Message
	}
	amt := fmt.Sprintf("%.2f", res.Available)
	if res.Currency != "" {
		amt += " " + res.Currency
	}
	msg := "上游余额：" + amt
	if res.Granted > 0 || res.ToppedUp > 0 {
		msg += fmt.Sprintf("（赠送 %.2f / 充值 %.2f）", res.Granted, res.ToppedUp)
	}
	if res.Message != "" {
		msg = "余额查询失败：" + res.Message
	}
	return msg
}
