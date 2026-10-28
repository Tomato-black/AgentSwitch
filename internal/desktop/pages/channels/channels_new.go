package channels

import (
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/presets"
)

// channelNewView 是新建页：预设网格（ID+DisplayName tile，点选 applyPreset
// 回填 protocol/base_url/建议）或「自定义」，再填名称/base_url/单 Key/
// allowPrivate 保存。内容列包 Scroll（小窗口可超视口）。
func newView(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			// 预置加载警告一行（spec §4：降级/跳过不阻塞，网格顶部提示）。
			if w := presets.Warning(); w != "" {
				ui.Text(c, "预置加载异常（已降级）："+w).TextColor(t.Danger)
			}
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "返回列表").Clicked() {
					st.Channels.View = ""
				}
				ui.Text(c, "添加渠道").Bold().FontSize(t.FontSize + 2)
			})
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for _, p := range presets.All() {
					presetTile(c, t, s, st, p)
				}
				customTile(c, t, s, st)
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
						// 协议切换联动（spec §5）：Changed() 当帧生效，预置含目标
						// 协议则换 base_url/建议模型（手改保护见 switchProtocol）。
						if ui.Select(c, &st.Channels.Form.Protocol, []string{"openai", "anthropic"}).Changed() {
							if p, ok := presets.ById(st.Channels.Form.PresetID); ok {
								switchProtocol(&st.Channels.Form, p, st.Channels.Form.Protocol)
							}
						}
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "API Key:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &st.Channels.Form.APIKey).Grow(1).
							Placeholder("单个密钥，AES-256-GCM 加密存储")
					})
					ui.Row(c).Gap(16).Children(func() {
						ui.Text(c, "允许私网上游").TextColor(t.TextMuted)
						ui.Switch(c, &st.Channels.Form.AllowPrivate)
					})
					if st.Channels.Form.Msg != "" {
						ui.Text(c, st.Channels.Form.Msg).TextColor(t.Danger)
					}
					ui.Row(c).Children(func() {
						ui.Box(c).Grow(1)
						if uikit.PillButton(c, t, "创建渠道").Clicked() {
							create(st)
						}
					})
				})
			})
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, "建议模型").Bold().Grow(1)
						if uikit.SoftButton(c, t, s, "act", "拉取模型").Clicked() {
							fetchModelsDraft(st)
						}
					})
					ui.Text(c, "逗号或换行分隔；预设会自动回填默认模型").TextColor(t.TextMuted)
					ui.TextArea(c, &st.Channels.Form.Suggestions).Grow(1).MinHeight(0).Height(96)
				})
			})
		})
	})
}

// presetTile 是预设网格一格：类型 tile + DisplayName/ID 两行，点选
// applyPreset 回填；选中底用应用级选中灰（不漂移）。
func presetTile(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, p presets.ChannelPreset) {
	b := ui.ButtonBase(c)
	bg := s.Gray
	if st.Channels.Form.PresetID == p.ID {
		bg = uikit.SelectedColor(t)
	} else if b.Hovered() {
		bg = t.SurfaceHover
	}
	b.Radius(8).Background(bg).Padding(8).Gap(8).AlignItems(ui.Center).
		Children(func() {
			uikit.Tile(c, t, s, p.DisplayName, 32)
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, p.DisplayName).Bold().SingleLine()
				ui.Text(c, p.ID).FontSize(t.FontSize - 2).TextColor(t.TextMuted).SingleLine()
			})
		})
	if b.Clicked() {
		applyPreset(&st.Channels.Form, p)
	}
}

// customTile 是预设网格的「自定义」格：点选清预设字段（protocol 落默认
// openai），base_url/建议全手填；name/apiKey 保留用户已填内容。
func customTile(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State) {
	b := ui.ButtonBase(c)
	bg := s.Gray
	if st.Channels.Form.PresetID == "" && st.Channels.Form.BaseURL == "" && st.Channels.Form.Suggestions == "" {
		bg = uikit.SelectedColor(t)
	} else if b.Hovered() {
		bg = t.SurfaceHover
	}
	b.Radius(8).Background(bg).Padding(8).Gap(8).AlignItems(ui.Center).
		Children(func() {
			uikit.Tile(c, t, s, "自定义", 32)
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "自定义").Bold().SingleLine()
				ui.Text(c, "custom").FontSize(t.FontSize - 2).TextColor(t.TextMuted).SingleLine()
			})
		})
	if b.Clicked() {
		st.Channels.Form.PresetID = ""
		st.Channels.Form.Protocol = "openai"
		st.Channels.Form.BaseURL = ""
		st.Channels.Form.Suggestions = ""
	}
}
