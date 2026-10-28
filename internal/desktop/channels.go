// channels.go 是渠道页（logical-models spec §6.3 / Task 14）：卡片列表（单
// Key 语义）+ 预设网格新建页 + 详情页（协议可编辑、Key 更新留空不改、建议
// 模型编辑、连通测试、删除引用警告）。纯助手 applyPreset / channelForm.validate /
// referencedBy 供测试直断言；渲染薄壳不测字符串细节。密钥池与 public/
// upstream 模型映射 UI 已随渠道单 Key 重构退役。
package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/presets"
	"AgentSwitch/internal/telemetry"
)

// channelForm 是渠道新建/详情编辑的表单状态：editID 非空 = 编辑该渠道
// （apiKey 留空 = 不改密钥）；presetID 由预设网格点选写入（自定义为空）。
type channelForm struct {
	presetID     string
	protocol     string
	name         string
	baseURL      string
	apiKey       string // 新建必填；详情留空=保持原 Key
	suggestions  string // 逗号/换行分隔；预设回填默认模型
	enabled      bool
	allowPrivate bool
	msg          string
	editID       string
}

// applyPreset 把预设回填进表单（spec §5）：presetID、第一可用协议（openai
// 优先）的 base_url 与建议模型；name 空时回填 DisplayName（已填不覆盖）；
// apiKey 等其余字段不动（预设只带连接默认值）。
func applyPreset(form *channelForm, p presets.ChannelPreset) {
	form.presetID = p.ID
	proto, ep, ok := presets.FirstProtocol(p)
	if !ok {
		return
	}
	form.protocol = proto
	form.baseURL = ep.BaseURL
	form.suggestions = strings.Join(ep.DefaultModels, "\n")
	if strings.TrimSpace(form.name) == "" {
		form.name = p.DisplayName
	}
}

// switchProtocol 是协议下拉联动（spec §5）：目标协议在预置中→当当前值
// 为空或等于任一协议预设值（未手改）时替换为目标协议预设值；目标协议不在
// 预置中→同条件下清空待手填。空值视为未修改（切走再切回可恢复预设）。
// baseURL 与建议模型各自独立判定，互不牵连。
func switchProtocol(form *channelForm, p presets.ChannelPreset, newProto string) {
	form.protocol = newProto
	replace := func(cur string, cands []string, next string) string {
		if cur == "" {
			return next
		}
		for _, c := range cands {
			if cur == c {
				return next
			}
		}
		return cur // 用户已手改，保留
	}
	var baseCands, modelCands []string
	for _, ep := range p.Protocols {
		baseCands = append(baseCands, ep.BaseURL)
		modelCands = append(modelCands, strings.Join(ep.DefaultModels, "\n"))
	}
	ep, has := p.Protocols[newProto]
	nextBase, nextModels := "", ""
	if has {
		nextBase = ep.BaseURL
		nextModels = strings.Join(ep.DefaultModels, "\n")
	}
	form.baseURL = replace(form.baseURL, baseCands, nextBase)
	form.suggestions = replace(form.suggestions, modelCands, nextModels)
}

// validate 校验表单：缺 name/key/base_url 依次报中文错误；编辑态
// （editID 非空）Key 留空 = 不改，不拦。
func (f channelForm) validate() error {
	if strings.TrimSpace(f.name) == "" {
		return fmt.Errorf("需要填写渠道名称")
	}
	if f.editID == "" && strings.TrimSpace(f.apiKey) == "" {
		return fmt.Errorf("需要填写 API Key")
	}
	if strings.TrimSpace(f.baseURL) == "" {
		return fmt.Errorf("需要填写 Base URL")
	}
	return nil
}

// parseSuggestions 把建议模型多行/逗号分隔文本拆成列表（去空白、空串与重复，
// 保序）；SetSuggestions 侧亦有剔除，双保险。
func parseSuggestions(text string) []string {
	fields := strings.FieldsFunc(text, func(r rune) bool {
		return r == ',' || r == '\n' || r == '\r'
	})
	out := make([]string, 0, len(fields))
	seen := map[string]bool{}
	for _, f := range fields {
		f = strings.TrimSpace(f)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	return out
}

// referencedBy 列出目标链引用了该渠道的逻辑模型名（删除警告用）；同一模型
// 多次引用只记一次，顺序即 List 顺序。
func referencedBy(models []bridge.ModelView, channelID string) []string {
	var names []string
	seen := map[string]bool{}
	for _, m := range models {
		if seen[m.Name] {
			continue
		}
		for _, t := range m.Targets {
			if t.ChannelID == channelID {
				names = append(names, m.Name)
				seen[m.Name] = true
				break
			}
		}
	}
	return names
}

// channelRefNames 是 referencedBy 的桌面侧取数（Services.Logical 接线，
// 未挂时返回空）。
func channelRefNames(u *uiState, channelID string) []string {
	if u.svc.Logical == nil {
		return nil
	}
	return referencedBy(u.svc.Logical.List(), channelID)
}

// channelStats 统计遥测环里该渠道名的近况：ok=2xx、fail=Status==0||>=400，
// 按 rec.Channel（渠道名）过滤。
func channelStats(recs []telemetry.RequestRecord, channel string) (ok, fail int) {
	for _, r := range recs {
		if r.Channel != channel {
			continue
		}
		switch {
		case r.Status == 0 || r.Status >= 400:
			fail++
		case r.Status >= 200 && r.Status < 300:
			ok++
		}
	}
	return ok, fail
}

// openChannelNew 进新建页并复位表单（预设网格从头选）。
func (u *uiState) openChannelNew() {
	u.resetChannelForm()
	u.chView = "new"
}

// openChannelDetail 进详情页并回显表单：Key 框恒空（留空=不改），建议模型
// 回显到多行文本。
func (u *uiState) openChannelDetail(id string) {
	u.chView = "detail"
	u.chDetailID = id
	u.chTestMsg = ""
	u.chKeyInput = ""
	u.chSuggText = ""
	u.chForm = channelForm{editID: id}
	d, err := u.svc.Channels.Get(id)
	if err != nil {
		u.chForm.msg = "渠道不存在或已删除"
		return
	}
	u.chForm = channelForm{
		editID: id, presetID: d.PresetID, protocol: d.Protocol,
		name: d.Name, baseURL: d.BaseURL,
		enabled: d.Enabled, allowPrivate: d.AllowPrivate,
	}
	u.chSuggText = strings.Join(d.Suggestions, "\n")
}

// detailProtocolSwitched 是详情页协议切换联动：与新建页共用 switchProtocol
// 的手改保护语义，唯一差别是详情页的建议模型承载在 chSuggText 而非
// chForm.suggestions——联动前同步进来、联动后写回去，保证「未手改才替换」
// 的判定基于用户实际看到的文本。presetID 查表（ById）由调用方完成。
func (u *uiState) detailProtocolSwitched(p presets.ChannelPreset) {
	u.chForm.suggestions = u.chSuggText
	switchProtocol(&u.chForm, p, u.chForm.protocol)
	u.chSuggText = u.chForm.suggestions
}

// resetChannelForm 复位新建表单：协议默认 openai、启用；预设字段清空待选。
func (u *uiState) resetChannelForm() {
	u.chForm = channelForm{protocol: "openai", enabled: true}
	u.chTestMsg = ""
	u.chKeyInput = ""
	u.chSuggText = ""
}

// createChannel 落「创建渠道」：校验 → Create（单 Key）→ 建议模型随建随写
// → 进详情（核对 Key 与建议）。
func (u *uiState) createChannel() {
	if err := u.chForm.validate(); err != nil {
		u.chForm.msg = err.Error()
		return
	}
	v, err := u.svc.Channels.Create(u.chForm.presetID, u.chForm.name, u.chForm.baseURL,
		u.chForm.protocol, u.chForm.apiKey, true, u.chForm.allowPrivate)
	if err != nil {
		u.chForm.msg = err.Error()
		return
	}
	if sugg := parseSuggestions(u.chForm.suggestions); len(sugg) > 0 {
		_ = u.svc.Channels.SetSuggestions(v.ID, sugg)
	}
	u.openChannelDetail(v.ID)
}

// saveChannelDetail 落详情「保存」：校验 → Update（Key 框留空=不改；协议
// 可编辑，2026-10-28 修订）→ 建议模型全量替换。
func (u *uiState) saveChannelDetail() {
	if err := u.chForm.validate(); err != nil {
		u.chTestMsg = err.Error()
		return
	}
	if err := u.svc.Channels.Update(u.chForm.editID, u.chForm.name, u.chForm.baseURL,
		u.chForm.protocol, u.chKeyInput, u.chForm.enabled, u.chForm.allowPrivate); err != nil {
		u.chTestMsg = err.Error()
		return
	}
	if err := u.svc.Channels.SetSuggestions(u.chForm.editID, parseSuggestions(u.chSuggText)); err != nil {
		u.chTestMsg = err.Error()
		return
	}
	u.chKeyInput = ""
	u.chTestMsg = "已保存"
}

// testChannelDetail 落「测试连通」：结果进 chTestMsg 信息位。
func (u *uiState) testChannelDetail() {
	res := u.svc.Channels.Test(u.chDetailID)
	if res.OK {
		u.chTestMsg = "连通正常 (" + res.Stage + ")"
		return
	}
	u.chTestMsg = res.Stage + " 失败: " + res.Error
}

// applyFetchedModels 把拉取结果写进指定文本框（覆盖）并给信息位；空列表
// 与错误都不覆盖（用户已填内容优先保留）。
func applyFetchedModels(dst *string, msg *string, ids []string, err error, suffix string) {
	if err != nil {
		*msg = "拉取模型失败: " + err.Error()
		return
	}
	if len(ids) == 0 {
		*msg = "上游未返回模型"
		return
	}
	*dst = strings.Join(ids, "\n")
	*msg = fmt.Sprintf("已从上游拉取 %d 个模型（%s）", len(ids), suffix)
}

// fetchChannelModels 落「拉取模型」（详情页）：用已保存渠道的 Base URL/协议/
// Key 拉上游模型列表，覆盖建议文本框；结果经 applyFetchedModels 统一处理。
func (u *uiState) fetchChannelModels() {
	ids, err := u.svc.Channels.FetchModels(u.chDetailID)
	applyFetchedModels(&u.chSuggText, &u.chTestMsg, ids, err, "保存后生效")
}

// fetchChannelModelsDraft 同上，但用新建表单的 baseURL/protocol/key——渠道
// 尚未入库，没有可读的 Key，只能走草稿拉取。
func (u *uiState) fetchChannelModelsDraft() {
	f := &u.chForm
	ids, err := u.svc.Channels.FetchModelsFromDraft(f.baseURL, f.protocol, f.apiKey, f.allowPrivate)
	applyFetchedModels(&f.suggestions, &f.msg, ids, err, "创建后生效")
}

// channelsView 是渠道页列表态：页头「＋ 添加渠道」+ 渠道卡流（名、协议、
// base_url、HasKey、建议模型数）。卡体点击进详情；空态引导添加。
func (u *uiState) channelsView(c *ui.Context) {
	t := c.Theme()
	s := uikit.Softs(t)
	chans := u.svc.Channels.List()
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "渠道").Bold().FontSize(t.FontSize + 2)
			ui.Textf(c, "%d 个渠道", len(chans)).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
			if uikit.PillButton(c, t, "＋ 添加渠道").Clicked() {
				u.openChannelNew()
			}
		})
		if u.chTestMsg != "" {
			ui.Text(c, u.chTestMsg)
		}
		if len(chans) == 0 {
			ui.Column(c).Gap(10).AlignItems(ui.Center).Padding(24).Children(func() {
				ui.Text(c, "还没有渠道").Bold()
				ui.Text(c, "添加渠道后，逻辑模型目标才能挂上").TextColor(t.TextMuted)
				if uikit.PillButton(c, t, "＋ 添加渠道").Clicked() {
					u.openChannelNew()
				}
			})
			return
		}
		for _, ch := range chans {
			u.channelCard(c, t, s, ch)
		}
	})
}

// channelCard 是一枚渠道卡：tile + 名称/徽章行（启用、HasKey）+ 协议·
// base_url + 建议模型数与近况 meta。卡体 ButtonBase 点击进详情。
func (u *uiState) channelCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, ch bridge.ChannelView) {
	okN, failN := channelStats(u.recent, ch.Name)
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
			u.openChannelDetail(ch.ID)
		}
	})
}

// channelNewView 是新建页：预设网格（ID+DisplayName tile，点选 applyPreset
// 回填 protocol/base_url/建议）或「自定义」，再填名称/base_url/单 Key/
// allowPrivate 保存。内容列包 Scroll（小窗口可超视口）。
func (u *uiState) channelNewView(c *ui.Context) {
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
					u.chView = ""
				}
				ui.Text(c, "添加渠道").Bold().FontSize(t.FontSize + 2)
			})
			ui.Row(c).Gap(8).Wrap().Children(func() {
				for _, p := range presets.All() {
					u.presetTile(c, t, s, p)
				}
				u.customTile(c, t, s)
			})
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Text(c, "连接配置").Bold()
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &u.chForm.name).Grow(1)
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "Base URL:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &u.chForm.baseURL).Grow(1)
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "协议:").Width(96).TextColor(t.TextMuted)
						// 协议切换联动（spec §5）：Changed() 当帧生效，预置含目标
						// 协议则换 base_url/建议模型（手改保护见 switchProtocol）。
						if ui.Select(c, &u.chForm.protocol, []string{"openai", "anthropic"}).Changed() {
							if p, ok := presets.ById(u.chForm.presetID); ok {
								switchProtocol(&u.chForm, p, u.chForm.protocol)
							}
						}
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "API Key:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &u.chForm.apiKey).Grow(1).
							Placeholder("单个密钥，AES-256-GCM 加密存储")
					})
					ui.Row(c).Gap(16).Children(func() {
						ui.Text(c, "允许私网上游").TextColor(t.TextMuted)
						ui.Switch(c, &u.chForm.allowPrivate)
					})
					if u.chForm.msg != "" {
						ui.Text(c, u.chForm.msg).TextColor(t.Danger)
					}
					ui.Row(c).Children(func() {
						ui.Box(c).Grow(1)
						if uikit.PillButton(c, t, "创建渠道").Clicked() {
							u.createChannel()
						}
					})
				})
			})
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, "建议模型").Bold().Grow(1)
						if uikit.SoftButton(c, t, s, "act", "拉取模型").Clicked() {
							u.fetchChannelModelsDraft()
						}
					})
					ui.Text(c, "逗号或换行分隔；预设会自动回填默认模型").TextColor(t.TextMuted)
					ui.TextArea(c, &u.chForm.suggestions).Grow(1).MinHeight(0).Height(96)
				})
			})
		})
	})
}

// presetTile 是预设网格一格：类型 tile + DisplayName/ID 两行，点选
// applyPreset 回填；选中底用应用级选中灰（不漂移）。
func (u *uiState) presetTile(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, p presets.ChannelPreset) {
	b := ui.ButtonBase(c)
	bg := s.Gray
	if u.chForm.presetID == p.ID {
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
		applyPreset(&u.chForm, p)
	}
}

// customTile 是预设网格的「自定义」格：点选清预设字段（protocol 落默认
// openai），base_url/建议全手填；name/apiKey 保留用户已填内容。
func (u *uiState) customTile(c *ui.Context, t *ui.Theme, s uikit.SoftPalette) {
	b := ui.ButtonBase(c)
	bg := s.Gray
	if u.chForm.presetID == "" && u.chForm.baseURL == "" && u.chForm.suggestions == "" {
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
		u.chForm.presetID = ""
		u.chForm.protocol = "openai"
		u.chForm.baseURL = ""
		u.chForm.suggestions = ""
	}
}

// channelDetailView 是详情页：连接配置（协议可编辑、Key 更新框留空=不改）+
// 建议模型多行编辑 + 测试连通 + 删除（引用警告）。
func (u *uiState) channelDetailView(c *ui.Context) {
	t := c.Theme()
	s := uikit.Softs(t)
	d, err := u.svc.Channels.Get(u.chDetailID)
	if err != nil {
		ui.Column(c).Gap(12).Children(func() {
			ui.Text(c, "渠道不存在或已删除").TextColor(t.Danger)
			if ui.Button(c, "返回列表").Clicked() {
				u.chView = ""
			}
		})
		return
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "返回列表").Clicked() {
					u.chView = ""
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
						ui.TextInput(c, &u.chForm.name).Grow(1)
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "Base URL:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &u.chForm.baseURL).Grow(1)
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "协议:").Width(96).TextColor(t.TextMuted)
						// 协议可编辑（2026-10-28 修订，偏离 2026-10-23 spec §5
						// 「创建后不可变」）：切换联动与新建页同款（手改保护见
						// switchProtocol）；自定义渠道（presetID 查不到）只切协议。
						if ui.Select(c, &u.chForm.protocol, []string{"openai", "anthropic"}).Changed() {
							if p, ok := presets.ById(u.chForm.presetID); ok {
								u.detailProtocolSwitched(p)
							}
						}
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "API Key:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &u.chKeyInput).Grow(1).Placeholder("留空保持不变")
					})
					ui.Row(c).Gap(16).Children(func() {
						ui.Text(c, "启用").TextColor(t.TextMuted)
						ui.Switch(c, &u.chForm.enabled)
						ui.Text(c, "允许私网上游").TextColor(t.TextMuted)
						ui.Switch(c, &u.chForm.allowPrivate)
					})
				})
			})
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, "建议模型").Bold().Grow(1)
						if uikit.SoftButton(c, t, s, "act", "拉取模型").Clicked() {
							u.fetchChannelModels()
						}
					})
					ui.Text(c, "逗号或换行分隔；逻辑模型目标编辑的下拉数据源").TextColor(t.TextMuted)
					ui.TextArea(c, &u.chSuggText).Grow(1).MinHeight(0).Height(96)
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				if uikit.PillButton(c, t, "保存").Clicked() {
					u.saveChannelDetail()
				}
				if uikit.SoftButton(c, t, s, "act", "测试连通").Clicked() {
					u.testChannelDetail()
				}
				ui.Box(c).Grow(1)
				if uikit.SoftButton(c, t, s, "danger", "删除渠道").Clicked() {
					u.chDeleteID, u.chDeleteArm = d.ID, true
				}
			})
			if u.chTestMsg != "" {
				ui.Text(c, u.chTestMsg)
			}
			u.channelDeleteConfirm(c, d)
		})
	})
}

// channelDeleteConfirm 是删除渠道的 AlertDialog：消息带引用警告（仍被
// 逻辑模型引用则列出引用者）；按钮序「取消, 确认删除」——末位是主钮
// （AlertDialog 约定），确认（下标 1）才 Remove 并回列表。
func (u *uiState) channelDeleteConfirm(c *ui.Context, d bridge.ChannelDetail) {
	if u.chDeleteID != d.ID {
		return
	}
	msg := d.Name
	if refs := channelRefNames(u, d.ID); len(refs) > 0 {
		msg = fmt.Sprintf("%s\n仍被逻辑模型引用：%s。删除后这些目标将失效。", d.Name, strings.Join(refs, "、"))
	}
	if i := ui.AlertDialog(c, &u.chDeleteArm, "删除渠道", msg, "取消", "确认删除"); i == 1 {
		u.svc.Channels.Remove(u.chDeleteID)
		u.chDeleteID = ""
		u.chView = ""
		u.chTestMsg = "已删除 " + d.Name
	}
}
