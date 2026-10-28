package channels

import (
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
			ui.Text(c, "渠道不存在或已删除").TextColor(t.Danger)
			if ui.Button(c, "返回列表").Clicked() {
				st.Channels.View = ""
			}
		})
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "返回列表").Clicked() {
					st.Channels.View = ""
				}
				ui.Text(c, d.Name).Bold().FontSize(t.FontSize + 2)
				if d.HasKey {
					uikit.SoftBadge(c, t, s, "act", "有 Key")
				} else {
					uikit.SoftBadge(c, t, s, "warn", "无 Key")
				}
			})
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Text(c, "连接配置").Bold()
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &st.Channels.Form.Name).Grow(1)
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "Base URL:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &st.Channels.Form.BaseURL).Grow(1)
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "协议:").Width(96).TextColor(t.TextMuted)
						// 协议可编辑（2026-10-28 修订，偏离 2026-10-23 spec §5
						// 「创建后不可变」）：切换联动与新建页同款（手改保护见
						// switchProtocol）；自定义渠道（presetID 查不到）只切协议。
						if ui.Select(c, &st.Channels.Form.Protocol, []string{"openai", "anthropic"}).Changed() {
							if p, ok := presets.ById(st.Channels.Form.PresetID); ok {
								detailProtocolSwitched(st, p)
							}
						}
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "API Key:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &st.Channels.KeyInput).Grow(1).Placeholder("留空保持不变")
					})
					ui.Row(c).Gap(16).Children(func() {
						ui.Text(c, "启用").TextColor(t.TextMuted)
						ui.Switch(c, &st.Channels.Form.Enabled)
						ui.Text(c, "允许私网上游").TextColor(t.TextMuted)
						ui.Switch(c, &st.Channels.Form.AllowPrivate)
					})
				})
			})
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, "建议模型").Bold().Grow(1)
						if uikit.SoftButton(c, t, s, "act", "拉取模型").Clicked() {
							fetchModels(st)
						}
					})
					ui.Text(c, "逗号或换行分隔；逻辑模型目标编辑的下拉数据源").TextColor(t.TextMuted)
					ui.TextArea(c, &st.Channels.SuggText).Grow(1).MinHeight(0).Height(96)
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				if uikit.PillButton(c, t, "保存").Clicked() {
					saveDetail(st)
				}
				if uikit.SoftButton(c, t, s, "act", "测试连通").Clicked() {
					testDetail(st)
				}
				ui.Box(c).Grow(1)
				if uikit.SoftButton(c, t, s, "danger", "删除渠道").Clicked() {
					st.Channels.DeleteID, st.Channels.DeleteArm = d.ID, true
				}
			})
			if st.Channels.TestMsg != "" {
				ui.Text(c, st.Channels.TestMsg)
			}
			deleteConfirm(c, st, d)
		})
	})
}

// channelDeleteConfirm 是删除渠道的 AlertDialog：消息带引用警告（仍被
// 逻辑模型引用则列出引用者）；按钮序「取消, 确认删除」——末位是主钮
// （AlertDialog 约定），确认（下标 1）才 Remove 并回列表。
func deleteConfirm(c *ui.Context, st *state.State, d bridge.ChannelDetail) {
	if st.Channels.DeleteID != d.ID {
		return
	}
	msg := d.Name
	if refs := refNames(st, d.ID); len(refs) > 0 {
		msg = fmt.Sprintf("%s\n仍被逻辑模型引用：%s。删除后这些目标将失效。", d.Name, strings.Join(refs, "、"))
	}
	if i := ui.AlertDialog(c, &st.Channels.DeleteArm, "删除渠道", msg, "取消", "确认删除"); i == 1 {
		st.Svc.Channels.Remove(st.Channels.DeleteID)
		st.Channels.DeleteID = ""
		st.Channels.View = ""
		st.Channels.TestMsg = "已删除 " + d.Name
	}
}
