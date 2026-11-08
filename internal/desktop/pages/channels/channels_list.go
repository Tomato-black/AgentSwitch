package channels

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// channelsView 是渠道页列表态：页头「＋ 添加渠道」+ 渠道卡流（名、协议、
// base_url、HasKey、建议模型数）。卡体点击进详情；空态引导添加。
func listView(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	chans := st.Svc.Channels.List()
	ui.Column(c).Gap(12).Grow(1).Children(func() {
		uikit.PageHeader(c, t, uikit.PageHeaderOpts{
			Title: "渠道", Subtitle: fmt.Sprintf("%d 个渠道", len(chans)),
			Actions: func() {
				if uikit.PillButton(c, t, "＋ 添加渠道").Clicked() {
					openNew(st)
				}
			},
		})
		if st.Channels.TestMsg != "" {
			uikit.Feedback(c, t, uikit.InferFeedback(st.Channels.TestMsg), st.Channels.TestMsg)
		}
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				if len(chans) == 0 {
					uikit.EmptyState(c, t, uikit.EmptyStateOpts{
						Title: "还没有渠道", Hint: "添加渠道后，逻辑模型目标才能挂上",
						ActionLabel: "＋ 添加渠道", OnAction: func() { openNew(st) },
					})
				}
				if len(chans) > 8 {
					ui.TextInput(c, &st.Channels.Filter).Grow(1).
						Placeholder("搜索渠道：名称 / 协议 / Base URL")
				}
				filter := strings.ToLower(strings.TrimSpace(st.Channels.Filter))
				visible := make([]bridge.ChannelView, 0, len(chans))
				for _, ch := range chans {
					if filter == "" || strings.Contains(strings.ToLower(ch.Name+" "+ch.Protocol+" "+ch.BaseURL), filter) {
						visible = append(visible, ch)
					}
				}
				if filter != "" && len(visible) == 0 {
					ui.Text(c, "没有匹配的渠道").TextColor(t.TextMuted)
				}
				cool := cooldownsByChannel(st)
				for _, ch := range visible {
					channelCard(c, t, s, st, ch, cool)
				}
			})
		})
	})
}

// channelCard 是一枚渠道卡：tile + 名称/徽章行（启用、HasKey）+ 协议·
// base_url + 建议模型数与近况 meta。卡体 ButtonBase 点击进详情。
func channelCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, ch bridge.ChannelView, cool map[string]bridge.CooldownView) {
	okN, failN := channelStats(st.Recent, ch.Name)
	uikit.Card(c, t, func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			b := ui.ButtonBase(c).Grow(1).Gap(12)
			if b.Hovered() {
				b.Background(t.SurfaceHover).Radius(8)
			}
			b.Children(func() {
				uikit.Tile(c, t, s, ch.Name, 40)
				ui.Column(c).Gap(4).Grow(1).Children(func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, ch.Name).Bold().SingleLine()
						if ch.Enabled {
							uikit.SoftBadge(c, t, s, "ok", "启用")
						} else {
							uikit.SoftBadge(c, t, s, "gray", "已停用")
						}
						if ch.HasKey {
							uikit.SoftBadge(c, t, s, "act", "有 Key")
						} else {
							uikit.SoftBadge(c, t, s, "warn", "无 Key")
						}
						if cd, ok := cool[ch.ID]; ok {
							if cd.HalfOpen {
								uikit.SoftBadge(c, t, s, "ok", "恢复中")
							} else {
								uikit.SoftBadge(c, t, s, "warn", "冷却 "+uikit.CooldownRemainingWord(cd.RemainingSeconds))
							}
						}
					})
					ui.Text(c, ch.Protocol+" · "+ch.BaseURL).Font(uikit.MonoFont).TextColor(t.TextMuted).SingleLine()
					meta := fmt.Sprintf("建议模型 %d 个", ch.SuggestionCount)
					if okN+failN > 0 {
						meta += fmt.Sprintf(" · 近 %d 条 %d✓ %d✗", okN+failN, okN, failN)
					}
					if cd, ok := cool[ch.ID]; ok {
						meta += " · " + uikit.CooldownReasonWord(cd.Reason)
					}
					ui.Text(c, meta).TextColor(t.TextMuted)
				})
			})
			if b.Clicked() {
				openDetail(st, ch.ID)
			}
			// 行内启停：与逻辑模型列表保持一致，临时停用不必进详情。
			toggle := "停用"
			kind := "gray"
			if !ch.Enabled {
				toggle = "启用"
				kind = "act"
			}
			if uikit.SoftButton(c, t, s, kind, toggle).Clicked() {
				st.Svc.Channels.SetEnabled(ch.ID, !ch.Enabled)
			}
			if uikit.SoftButton(c, t, s, "gray", "复制").Clicked() {
				armChannelCopy(st, ch.ID, nil)
			}
		})
	})
}
