package channels

import (
	"strings"

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
				uikit.Feedback(c, t, uikit.FeedbackWarning, "预置加载异常（已降级）："+w)
			}
			uikit.PageHeader(c, t, uikit.PageHeaderOpts{
				Title: "添加渠道", BackLabel: "← 返回列表",
				OnBack: func() { st.Go("channels", func() { st.Channels.View = "" }) },
			})
			ui.TextInput(c, &st.Channels.PresetFilter).Grow(1).
				Placeholder("搜索预设：名称 / ID")
			filter := strings.ToLower(strings.TrimSpace(st.Channels.PresetFilter))
			matched := 0
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for _, p := range presets.All() {
					if filter != "" && !strings.Contains(strings.ToLower(p.DisplayName+" "+p.ID), filter) {
						continue
					}
					matched++
					presetTile(c, t, s, st, p)
				}
				customTile(c, t, s, st)
			})
			if filter != "" && matched == 0 {
				ui.Text(c, "没有匹配的预设；可直接使用自定义渠道").TextColor(t.TextMuted)
			}
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
						// 协议切换联动（spec §5）：Changed() 当帧生效，预置含目标
						// 协议则换 base_url/建议模型（手改保护见 switchProtocol）。
						sel := ui.Select(c, &st.Channels.Form.Protocol, []string{"openai", "anthropic"})
						if sel.Changed() {
							if p, ok := presets.ById(st.Channels.Form.PresetID); ok {
								switchProtocol(&st.Channels.Form, p, st.Channels.Form.Protocol)
							}
						}
						return sel
					})
					uikit.FormRow(c, t, "API Key:", func() ui.Element {
						return ui.TextInput(c, &st.Channels.Form.APIKey).Grow(1).
							Placeholder("单个密钥，AES-256-GCM 加密存储")
					})
					uikit.FormRowRaw(c, t, "允许私网上游", func() {
						ui.Switch(c, &st.Channels.Form.AllowPrivate)
					})
					uikit.Feedback(c, t, uikit.InferFeedback(st.Channels.Form.Msg), st.Channels.Form.Msg)
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
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "建议模型").Bold().Grow(1)
						if st.Channels.FetchTask.Running {
							uikit.BusyRow(c, t, st.Channels.FetchTask.Label, st.Channels.FetchTask.Cancel)
						} else if uikit.SoftButton(c, t, s, "act", "拉取模型").Clicked() {
							fetchModelsDraft(st)
						}
					})
					ui.Text(c, "逗号或换行分隔；预设会自动回填默认模型").TextColor(t.TextMuted)
					ui.TextArea(c, &st.Channels.Form.Suggestions).Grow(1).MinHeight(0).Height(96).
						Font(uikit.MonoFont)
				})
			})
		})
	})
}

// presetTile 是预设网格一格：类型 tile + DisplayName/ID 两行，点选
// applyPreset 回填；选中底用应用级选中灰（不漂移）。
func presetTile(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, p presets.ChannelPreset) {
	b := ui.ButtonBase(c)
	selected := st.Channels.Form.PresetID == p.ID
	bg := s.Gray
	if selected {
		bg = uikit.SelectedColor(t)
	} else if b.Hovered() {
		bg = t.SurfaceHover
	}
	if selected {
		b.Border(2, t.Accent)
	} else {
		b.Border(2, t.Border)
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
	selected := st.Channels.Form.PresetID == "" && st.Channels.Form.BaseURL == "" && st.Channels.Form.Suggestions == ""
	bg := s.Gray
	if selected {
		bg = uikit.SelectedColor(t)
	} else if b.Hovered() {
		bg = t.SurfaceHover
	}
	if selected {
		b.Border(2, t.Accent)
	} else {
		b.Border(2, t.Border)
	}
	b.Radius(8).Background(bg).Padding(8).Gap(8).AlignItems(ui.Center).
		Children(func() {
			uikit.Tile(c, t, s, "自定义", 32)
			ui.Column(c).Gap(2).Children(func() {
				ui.Text(c, "自定义").Bold().SingleLine()
				ui.Text(c, "手动填写").FontSize(t.FontSize - 2).TextColor(t.TextMuted).SingleLine()
			})
		})
	if b.Clicked() {
		st.Channels.Form.PresetID = ""
		st.Channels.Form.Protocol = "openai"
		st.Channels.Form.BaseURL = ""
		st.Channels.Form.Suggestions = ""
	}
}
