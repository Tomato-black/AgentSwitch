package channels

import (
	"fmt"

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
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "渠道").Bold().FontSize(t.FontSize + 2)
			ui.Textf(c, "%d 个渠道", len(chans)).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
			if uikit.PillButton(c, t, "＋ 添加渠道").Clicked() {
				openNew(st)
			}
		})
		if st.Channels.TestMsg != "" {
			ui.Text(c, st.Channels.TestMsg)
		}
		if len(chans) == 0 {
			ui.Column(c).Gap(10).AlignItems(ui.Center).Padding(24).Children(func() {
				ui.Text(c, "还没有渠道").Bold()
				ui.Text(c, "添加渠道后，逻辑模型目标才能挂上").TextColor(t.TextMuted)
				if uikit.PillButton(c, t, "＋ 添加渠道").Clicked() {
					openNew(st)
				}
			})
			return
		}
		for _, ch := range chans {
			channelCard(c, t, s, st, ch)
		}
	})
}

// channelCard 是一枚渠道卡：tile + 名称/徽章行（启用、HasKey）+ 协议·
// base_url + 建议模型数与近况 meta。卡体 ButtonBase 点击进详情。
func channelCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, ch bridge.ChannelView) {
	okN, failN := channelStats(st.Recent, ch.Name)
	uikit.Card(c, t, func() {
		b := ui.ButtonBase(c).Grow(1).Gap(12).Children(func() {
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
				})
				ui.Text(c, ch.Protocol+" · "+ch.BaseURL).Font(uikit.MonoFont).TextColor(t.TextMuted).SingleLine()
				meta := fmt.Sprintf("建议模型 %d 个", ch.SuggestionCount)
				if okN+failN > 0 {
					meta += fmt.Sprintf(" · 近 %d 条 %d✓ %d✗", okN+failN, okN, failN)
				}
				ui.Text(c, meta).TextColor(t.TextMuted)
			})
		})
		if b.Clicked() {
			openDetail(st, ch.ID)
		}
	})
}
