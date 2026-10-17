package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/telemetry"
)

// presetLabels 供 Select 展示；返回 labels 与 label→id 映射。
func presetLabels() ([]string, map[string]string) {
	ps := provider.Presets()
	labels := make([]string, 0, len(ps))
	byLabel := map[string]string{}
	for _, p := range ps {
		labels = append(labels, p.Label)
		byLabel[p.Label] = p.ID
	}
	return labels, byLabel
}

func presetOf(typeID string) provider.Preset {
	p, _ := provider.LookupPreset(typeID)
	return p
}

// resolveTypeID 把表单里的类型值解析回预设 id：mygo Select 把选中的选项
// 原文（label）写回 *selected，而初始化/载入所选/测试直赋的是 id——
// label 经映射还原为 id，其余（含裸 id）原样返回。预填、描述与提交共用。
func resolveTypeID(byLabel map[string]string, v string) string {
	if id, ok := byLabel[v]; ok {
		return id
	}
	return v
}

// channelStats 统计遥测环里该渠道名的近况：ok=2xx、fail=Status==0||>=400
// （与概览 recentStats 的失败口径一致），按 rec.Channel（渠道名）过滤。
func channelStats(recs []telemetry.RequestRecord, channel string) (ok, fail int) {
	for _, r := range recs {
		if r.Channel != channel {
			continue
		}
		switch {
		case r.Status >= 200 && r.Status < 300:
			ok++
		case r.Status == 0 || r.Status >= 400:
			fail++
		}
	}
	return ok, fail
}

// keyStats 统计遥测环里该渠道+密钥名的近况：口径同 channelStats（ok=2xx、
// fail=Status==0||>=400），按 rec.Channel==渠道名 && rec.Account==密钥名
// 过滤（渠道化后 Account 语义 = 密钥名）。密钥池「近 100 条」列消费。
func keyStats(recs []telemetry.RequestRecord, channel, account string) (ok, fail int) {
	for _, r := range recs {
		if r.Channel != channel || r.Account != account {
			continue
		}
		switch {
		case r.Status >= 200 && r.Status < 300:
			ok++
		case r.Status == 0 || r.Status >= 400:
			fail++
		}
	}
	return ok, fail
}

// clearDetailContext 渠道上下文切换清位（原 prevChannelID 块语义平移）：
// 清子表选中、两个确认位、confirmOwnerID 与三处提示——确认位武装后换渠道
// 必须解除（防落刀他渠道），提示属上一渠道操作语境、残留即误导（审查
// Finding 2 / Task 8 复审 Finding）。
func (u *uiState) clearDetailContext() {
	u.chKeySel, u.chModelSel = -1, -1
	u.keyConfirmDelete, u.chModelConfirmDel = false, false
	u.confirmOwnerID = ""
	u.keyForm.msg, u.chModelForm.msg, u.chTestMsg = "", "", ""
}

// openChannelDetail 跳渠道详情页：锚定渠道 id、分段归零；目标渠道变化即
// 清上下文（确认位/子选中/提示）——切换即解除武装，不待渲染
// （TestDetailConfirmDisarmOnChannelSwitch）。
func (u *uiState) openChannelDetail(id string) {
	if u.chDetailID != id {
		u.clearDetailContext()
	}
	u.page = "channel-detail"
	u.chDetailID = id
	u.chDetailSeg = 0
}

// toggleChannelEnabled 落渠道启停；失败提示留在 chTestMsg（列表/详情共用
// 信息位）。SetEnabled 是整渠道启停——不触碰字段，Card Toggle 不丢配置。
func (u *uiState) toggleChannelEnabled(id string, on bool) {
	if err := u.svc.Channels.SetEnabled(id, on); err != nil {
		u.chTestMsg = err.Error()
	}
}

// channelsView 是渠道页列表态（spec §5.2）：页头统计 + 「＋ 添加渠道」+
// 渠道卡流。卡体 ButtonBase 点击进详情；卡内 Switch/⋯ 是更内层交互件，
// 命中即截获不冒泡进详情（input.go 命中链取最内层交互件）。空态引导添加。
// 旧表单/详情面板 UI 随 Task 6/7 迁往整页详情/新建，本页只留列表。
func (u *uiState) channelsView(c *ui.Context) {
	t := c.Theme()
	s := softs(t)
	chans := u.svc.Channels.List()
	// 页头统计：渠道数/启用数/密钥池 a/b（a=ΣKeyCount−冷却数，钳 0——
	// 与概览 poolCard 同口径；b=ΣKeyCount）。首选徽章的门槛是启用渠道里
	// 优先级数值最小（数值小=优先级高）。
	enabled, keys, minPrio := 0, 0, 0
	for _, ch := range chans {
		keys += ch.KeyCount
		if !ch.Enabled {
			continue
		}
		enabled++
		if enabled == 1 || ch.Priority < minPrio {
			minPrio = ch.Priority
		}
	}
	ready := keys - len(u.svc.Gateway.PoolStatus())
	if ready < 0 {
		ready = 0
	}
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "渠道").Bold().FontSize(t.FontSize + 2)
			ui.Textf(c, "%d 个渠道 · %d 个启用 · 密钥池 %d/%d 可用",
				len(chans), enabled, ready, keys).TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
			if pillButton(c, t, "＋ 添加渠道").Clicked() {
				u.resetChannelForm()
				u.page = "channel-new"
			}
		})
		if u.chTestMsg != "" {
			ui.Text(c, u.chTestMsg)
		}
		if len(chans) == 0 {
			// 空态：引导不是空白（spec §5.2）——同「＋ 添加渠道」路径。
			ui.Column(c).Gap(10).AlignItems(ui.Center).Padding(24).Children(func() {
				ui.Text(c, "还没有渠道").Bold()
				ui.Text(c, "添加渠道后，Agent 请求才会经网关转发").TextColor(t.TextMuted)
				if pillButton(c, t, "＋ 添加渠道").Clicked() {
					u.resetChannelForm()
					u.page = "channel-new"
				}
			})
			return
		}
		for _, ch := range chans {
			u.channelCard(c, t, s, ch, minPrio)
		}
	})
}

// channelCard 是一枚渠道卡（spec §5.2）：tile + 名称/徽章行 + BaseURL +
// meta 统计行 + 右侧启停 Switch 与「⋯」菜单。卡体 ButtonBase 底点击进
// 详情；删除走 AlertDialog，弹窗仅对 chDeleteID 匹配的卡触发。
func (u *uiState) channelCard(c *ui.Context, t *ui.Theme, s softPalette, ch bridge.ChannelView, minPrio int) {
	okN, failN := channelStats(u.recent, ch.Name)
	card(c, t, func() {
		b := ui.ButtonBase(c).Grow(1).Gap(12).Children(func() {
			tile(c, t, s, ch.Name, 40)
			ui.Column(c).Gap(4).Grow(1).Children(func() {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, ch.Name).Bold().SingleLine()
					if ch.Enabled && ch.Priority == minPrio {
						softBadge(c, t, s, "act", "首选")
					}
					if ch.Enabled {
						softBadge(c, t, s, "ok", "启用")
					} else {
						softBadge(c, t, s, "gray", "已停用")
					}
					if ch.CoolingCount > 0 {
						softBadge(c, t, s, "warn", fmt.Sprintf("冷却 %d 把", ch.CoolingCount))
					}
				})
				ui.Text(c, ch.BaseURL).Font(monoFont).TextColor(s.Link).SingleLine()
				meta := fmt.Sprintf("优先级 %d · %d 把密钥", ch.Priority, ch.KeyCount)
				if okN+failN > 0 { // 统计段：a+b==0 不显（没跑过请求不该编数字）
					meta += fmt.Sprintf(" · 近 %d 条 %d✓ %d✗", okN+failN, okN, failN)
				}
				ui.Text(c, meta).TextColor(t.TextMuted)
			})
			ui.Row(c).Gap(10).AlignItems(ui.Center).Shrink(0).Children(func() {
				on := ch.Enabled
				if ui.Switch(c, &on).Changed() {
					u.toggleChannelEnabled(ch.ID, on)
				}
				ui.MenuButton(c, "⋯", func(m *ui.Menu) {
					if m.Item("测试连通").Chosen() {
						res := u.svc.Channels.Test(ch.ID)
						if res.OK {
							u.chTestMsg = "连通正常 (" + res.Stage + ")"
						} else {
							u.chTestMsg = res.Stage + " 失败: " + res.Error
						}
					}
					if m.Item("编辑").Chosen() {
						u.openChannelDetail(ch.ID) // 「编辑」与卡体点击同走详情
					}
					if m.Item("删除").Chosen() {
						u.chDeleteID, u.chDeleteArm = ch.ID, true
					}
				})
			})
		})
		if b.Clicked() {
			u.openChannelDetail(ch.ID)
		}
		// 删除确认（spec §3.4「取消居末位」）：仅对武装对象匹配的卡开弹窗，
		// 确认才落刀；AlertDialog 自关 open 位，这里只需清武装 id。
		if u.chDeleteID == ch.ID {
			if i := ui.AlertDialog(c, &u.chDeleteArm, "删除渠道", ch.Name, "确认删除", "取消"); i == 0 {
				_ = u.svc.Channels.Remove(u.chDeleteID)
				u.chDeleteID = ""
			}
		}
	})
}

// channelNewView 是新建渠道页（spec §5.2）：返回钮 + 「添加渠道」标题 +
// 类型 chips 行（provider.Presets() 全量，取代类型 Select——本页无「更换
// 类型确认」，prevType 检测仅服务详情态）+ 表单（名称/Base URL/优先级/
// 启用/允许私网，控件与连接配置段同款）+ 密钥批量 TextArea（复用 keyForm，
// 随建随导）+ 「创建渠道」主钮。内容列包 ui.Scroll（小窗口可超视口）。
func (u *uiState) channelNewView(c *ui.Context) {
	t := c.Theme()
	s := softs(t)
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "返回列表").Clicked() {
					u.page = "channels"
				}
				ui.Text(c, "添加渠道").Bold().FontSize(t.FontSize + 2)
			})
			// 类型 chips：选中 Accent 实心白字，未选 soft gray；点选即写
			// typeID 并预填该预设默认 BaseURL（chips 取代 Select，对未保存
			// 渠道无「更换重置确认」）。
			ui.Row(c).Gap(8).Children(func() {
				for _, p := range provider.Presets() {
					bg, fg := s.Gray, s.GrayText
					hover, pressed := s.Gray.Mix(s.GrayText, 0.10), s.Gray.Mix(s.GrayText, 0.22)
					if u.chForm.typeID == p.ID {
						bg, fg = t.Accent, t.AccentText
						hover, pressed = t.AccentHover, t.AccentPressed
					}
					b := ui.ButtonBase(c)
					actual := bg
					if b.Pressed() {
						actual = pressed
					} else if b.Hovered() {
						actual = hover
					}
					b.Radius(8).Background(actual).TextColor(fg).Padding(6, 12).
						Children(func() { ui.Text(c, p.Label).SingleLine() })
					if b.Clicked() {
						u.chForm.typeID = p.ID
						u.chForm.baseURL = presetOf(p.ID).DefaultBaseURL
					}
				}
			})
			card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Text(c, "连接配置").Bold()
					u.channelFormRows(c, t)
					if u.chForm.msg != "" {
						ui.Text(c, u.chForm.msg)
					}
				})
			})
			card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Text(c, "批量密钥（可选）").Bold()
					ui.Text(c, "随渠道一并导入；也可建完后在详情「密钥池」里加").TextColor(t.TextMuted)
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "API Keys（每行一枚）:").Width(140).TextColor(t.TextMuted)
						ui.TextArea(c, &u.keyForm.keysText).Grow(1).MinHeight(0).Height(64)
					})
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, "允许文件降级").TextColor(t.TextMuted)
						ui.Switch(c, &u.keyForm.allowFallback)
					})
				})
			})
			ui.Row(c).Children(func() {
				ui.Box(c).Grow(1)
				if pillButton(c, t, "创建渠道").Clicked() {
					u.createFromNewPage()
				}
			})
		})
	})
}

// channelFormRows 是连接配置共用字段行（名称/Base URL/优先级/启用/允许
// 私网）：详情「连接配置」段与新建页共用（新建页另有类型 chips，详情另有
// 类型 Select 与更换确认），控件与行式同款。
func (u *uiState) channelFormRows(c *ui.Context, t *ui.Theme) {
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
		ui.TextInput(c, &u.chForm.name).Grow(1)
	})
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "Base URL:").Width(96).TextColor(t.TextMuted)
		ui.TextInput(c, &u.chForm.baseURL).Grow(1)
	})
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "优先级:").Width(96).TextColor(t.TextMuted)
		ui.TextInput(c, &u.chForm.priorityInput).Width(96)
	})
	ui.Row(c).Gap(16).Children(func() {
		ui.Text(c, "启用").TextColor(t.TextMuted)
		ui.Switch(c, &u.chForm.enabled)
		ui.Text(c, "允许私网上游").TextColor(t.TextMuted)
		ui.Switch(c, &u.chForm.allowPrivate)
	})
}

// createFromNewPage 落新建页「创建渠道」（原 submitChannel Create 分支+
// 密钥随建随导）：优先级必须整数；Create 走类型预设 id（chips 写的就是 id，
// 仍经 resolveTypeID 兜底 label→id）；成功后 keysText 非空则 AddKeys 随建
// 随导（密钥名取渠道名）。Create 成功即复位表单、落回列表并选中新渠道——
// AddKeys 失败渠道保留不回滚，但**不得**把表单留在原地（重点击会克隆同名
// 渠道）；失败提示改走列表信息位 chTestMsg（resetChannelForm 会清 chForm.msg，
// 字面留在表单上不可观察），明示已创建并指路详情「密钥池」重试
// （TestNewPageAddKeysFailKeepsOneChannel）。
func (u *uiState) createFromNewPage() {
	prio, err := strconv.Atoi(u.chForm.priorityInput)
	if err != nil {
		u.chForm.msg = "优先级必须是整数"
		return
	}
	_, byLabel := presetLabels()
	tid := resolveTypeID(byLabel, u.chForm.typeID)
	v, err := u.svc.Channels.Create(tid, u.chForm.name, u.chForm.baseURL,
		prio, u.chForm.enabled, u.chForm.allowPrivate)
	if err != nil {
		u.chForm.msg = err.Error()
		return
	}
	var keyErr error
	if u.keyForm.keysText != "" {
		if _, err := u.svc.Channels.AddKeys(v.ID, u.chForm.name,
			u.keyForm.keysText, u.keyForm.allowFallback); err != nil {
			keyErr = err // 渠道已建不回滚
		}
	}
	u.resetChannelForm()
	if keyErr != nil { // reset 后写（reset 会清 chTestMsg）
		u.chTestMsg = "渠道已创建，密钥导入失败: " + keyErr.Error() + "——请到详情「密钥池」重试"
	}
	u.page = "channels"
	u.selectChannel(v.ID)
}

// saveDetailChannel 落详情页「保存渠道」（原 submitChannel Update 分支语义
// 平移）：优先级必须整数；Update 全字段回写（名称/BaseURL/优先级/启用/
// 允许私网）。成功留「已保存」提示（表单保留——详情页继续编辑不复位），
// 失败错误写 chForm.msg。
func (u *uiState) saveDetailChannel() {
	prio, err := strconv.Atoi(u.chForm.priorityInput)
	if err != nil {
		u.chForm.msg = "优先级必须是整数"
		return
	}
	if err := u.svc.Channels.Update(u.chDetailID, u.chForm.name, u.chForm.baseURL,
		prio, u.chForm.enabled, u.chForm.allowPrivate); err != nil {
		u.chForm.msg = err.Error()
		return
	}
	u.chForm.msg = "已保存"
}

// selectChannel 把表格选中行指到 id 对应行（找不到则不动选中位）。
func (u *uiState) selectChannel(id string) {
	for i, c := range u.svc.Channels.List() {
		if c.ID == id {
			u.chSel = i
			return
		}
	}
}

// segmented 是自组分段控件（spec §4）：SegmentedBase 的框架默认选中片取
// pressedColor——浅色下是 Background 白片，白底隐身（toggle.go:45-54），
// 故选中片显式上应用级选中灰 selectedColor，Track 上 SurfacePressed。
func segmented(c *ui.Context, t *ui.Theme, selected *int, labels []string) {
	parts := ui.SegmentedBase(c, selected, len(labels))
	parts.Track.Background(t.SurfacePressed).Radius(8).Padding(3).Gap(3).Children(func() {
		for i, label := range labels {
			seg := parts.Segment(i)
			if i == *selected {
				seg.Background(selectedColor(t))
			}
			seg.Radius(6).Padding(6, 12).Center().Children(func() {
				ui.Text(c, label).SingleLine()
			})
		}
	})
}

// channelDetailView 是渠道详情页（spec §5.2）：锚定 chDetailID，页头
// （返回+tile+名称徽章+连通测试+保存）+ Segmented 三段（连接配置/密钥池/
// 渠道模型）+ 段内容。Get 失败或渠道已删 → 兜底回列表；进入详情（prevDetailID
// 变化）回显表单并清上下文。内容列包 ui.Scroll（三段内容可超视口）。
func (u *uiState) channelDetailView(c *ui.Context) {
	t := c.Theme()
	s := softs(t)
	detail, err := u.svc.Channels.Get(u.chDetailID)
	if err != nil {
		u.page = "channels" // 渠道已删/加载失败 → 兜底返回列表
		return
	}
	v := detail.Channel
	// 进入详情（prevDetailID 变化）：回显渠道现值进表单（原 loadChannel
	// 职责）+ 清上下文。openChannelDetail 已清过位，此处幂等再清防
	// chDetailID 直改路径漏清。
	if u.prevDetailID != v.ID {
		u.prevDetailID = v.ID
		u.clearDetailContext()
		u.chForm = chFormData{typeID: v.Type, name: v.Name, baseURL: v.BaseURL,
			priorityInput: strconv.Itoa(v.Priority), enabled: v.Enabled,
			allowPrivate: v.AllowPrivateUpstream, editID: v.ID}
		u.prevType = u.chForm.typeID
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			u.channelDetailHeader(c, t, s, detail)
			if u.chTestMsg != "" {
				ui.Text(c, u.chTestMsg)
			}
			segmented(c, t, &u.chDetailSeg, []string{"连接配置",
				fmt.Sprintf("密钥池 %d", len(detail.Keys)),
				fmt.Sprintf("渠道模型 %d", len(detail.Models))})
			switch u.chDetailSeg {
			case 1:
				u.channelKeyPoolSection(c, t, s, detail)
			case 2:
				u.channelModelSection(c, t, s, detail)
			default:
				u.channelConnSection(c, t)
			}
		})
	})
	// 类型变更检测放在段内容之后：同帧 Select 变更即时武装，不滞后一帧。
	// prevType 检测仅服务详情态（新建页 chips 自管预填、无更换确认）——
	// 已保存渠道换类型会重置 Base URL，须确认。
	_, byLabel := presetLabels()
	tid := resolveTypeID(byLabel, u.chForm.typeID)
	if u.prevType != u.chForm.typeID {
		u.prevType = u.chForm.typeID
		u.chTypeResetConfirm = true // 详情态：换类型会重置 Base URL，须确认
	}
	// 类型更换确认（取消居末位；==0 确认）：确认→预填新类型默认 URL，
	// 取消/Escape→typeID 回退为渠道现类型。
	if u.chTypeResetConfirm {
		switch i := ui.AlertDialog(c, &u.chTypeResetConfirm, "更换类型",
			"Base URL 将重置为该类型预设默认值。", "确认更换", "取消"); i {
		case 0:
			u.chForm.baseURL = presetOf(tid).DefaultBaseURL
		case 1, ui.AlertDismissed:
			u.chForm.typeID = detail.Channel.Type
			u.prevType = u.chForm.typeID
		}
	}
	// 密钥/模型删除确认（AlertDialog，==0 落刀）：confirmOwnerID 绑定渠道
	// ——弹窗仅对武装时的渠道渲染（审查 Finding 2）。
	if u.keyConfirmDelete && u.confirmOwnerID == v.ID {
		switch i := ui.AlertDialog(c, &u.keyConfirmDelete, "删除密钥",
			"确认删除该密钥？密钥本体将从密钥存储移除。", "确认删除密钥", "取消"); i {
		case 0:
			if u.chKeySel >= 0 && u.chKeySel < len(detail.Keys) {
				if err := u.svc.Channels.RemoveKey(detail.Keys[u.chKeySel].ID); err != nil {
					u.keyForm.msg = err.Error()
				}
			}
			u.confirmOwnerID = ""
		case 1, ui.AlertDismissed:
			u.confirmOwnerID = ""
		}
	}
	if u.chModelConfirmDel && u.confirmOwnerID == v.ID {
		switch i := ui.AlertDialog(c, &u.chModelConfirmDel, "删除模型",
			"确认删除该模型映射？", "确认删除模型", "取消"); i {
		case 0:
			if u.chModelSel >= 0 && u.chModelSel < len(detail.Models) {
				if err := u.svc.Channels.RemoveModel(detail.Models[u.chModelSel].ID); err != nil {
					u.chModelForm.msg = err.Error()
				}
			}
			u.confirmOwnerID = ""
		case 1, ui.AlertDismissed:
			u.confirmOwnerID = ""
		}
	}
}

// channelDetailHeader 是详情页头：返回钮 + 类型 tile + 名称/徽章 + 右侧
// 「连通测试」（结果写 chTestMsg，与列表页共用信息位）与「保存渠道」主钮。
func (u *uiState) channelDetailHeader(c *ui.Context, t *ui.Theme, s softPalette, detail bridge.ChannelDetail) {
	v := detail.Channel
	ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "返回列表").Clicked() {
			u.page = "channels"
		}
		tile(c, t, s, v.Name, 40)
		ui.Column(c).Gap(4).Grow(1).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, v.Name).Bold().SingleLine()
				if v.Enabled {
					softBadge(c, t, s, "ok", "启用")
				} else {
					softBadge(c, t, s, "gray", "已停用")
				}
				if v.CoolingCount > 0 {
					softBadge(c, t, s, "warn", fmt.Sprintf("冷却 %d 把", v.CoolingCount))
				}
			})
			ui.Text(c, v.BaseURL).Font(monoFont).TextColor(s.Link).SingleLine()
		})
		if softButton(c, t, s, "act", "连通测试").Clicked() {
			res := u.svc.Channels.Test(v.ID)
			if res.OK {
				u.chTestMsg = "连通正常 (" + res.Stage + ")"
			} else {
				u.chTestMsg = res.Stage + " 失败: " + res.Error
			}
		}
		if pillButton(c, t, "保存渠道").Clicked() {
			u.saveDetailChannel() // Update 全字段回写（原 submitChannel Update 分支）
		}
	})
}

// channelConnSection 是「连接配置」段：类型 Select（详情态换类型走确认）+
// 共用字段行（channelFormRows）+ 提示位。
func (u *uiState) channelConnSection(c *ui.Context, t *ui.Theme) {
	card(c, t, func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Text(c, "连接配置").Bold()
			labels, byLabel := presetLabels()
			tid := resolveTypeID(byLabel, u.chForm.typeID)
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "类型:").Width(96).TextColor(t.TextMuted)
				ui.Select(c, &u.chForm.typeID, labelsOf(labels, byLabel, u.chForm.typeID))
				ui.Text(c, presetOf(tid).Description).TextColor(t.TextMuted)
			})
			u.channelFormRows(c, t)
			if u.chForm.msg != "" {
				ui.Text(c, u.chForm.msg)
			}
		})
	})
}

// channelKeyPoolSection 是「密钥池」段：工具行（「＋ 批量导入」切表单）+
// 批量导入表单（开时显示）+ 密钥表（名称/尾4/存储/状态/优先级/近 100 条/
// ⋯ 菜单：停用启用、测试、删除——删除走 AlertDialog 且确认位绑定渠道）。
func (u *uiState) channelKeyPoolSection(c *ui.Context, t *ui.Theme, s softPalette, detail bridge.ChannelDetail) {
	v := detail.Channel
	card(c, t, func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "密钥池").Bold().Grow(1)
				if softButton(c, t, s, "act", "＋ 批量导入").Clicked() {
					u.keyFormOpen = !u.keyFormOpen
				}
			})
			if u.keyFormOpen {
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "密钥名称:").Width(96).TextColor(t.TextMuted)
					ui.TextInput(c, &u.keyForm.name).Grow(1)
				})
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "API Keys（每行一枚）:").Width(140).TextColor(t.TextMuted)
					ui.TextArea(c, &u.keyForm.keysText).Grow(1).MinHeight(0).Height(64)
				})
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, "允许文件降级").TextColor(t.TextMuted)
					ui.Switch(c, &u.keyForm.allowFallback)
					if pillButton(c, t, "导入密钥").Clicked() {
						n, err := u.svc.Channels.AddKeys(v.ID, u.keyForm.name,
							u.keyForm.keysText, u.keyForm.allowFallback)
						if err != nil {
							u.keyForm.msg = err.Error()
						} else {
							u.keyForm.msg = fmt.Sprintf("已导入 %d 枚密钥", n)
							u.keyForm.name, u.keyForm.keysText = "", ""
						}
					}
				})
			}
			if len(detail.Keys) == 0 {
				ui.Text(c, "尚无密钥——导入一枚后该渠道才能接流量").TextColor(t.TextMuted)
			} else {
				keyCols := []ui.TableColumn{{Title: "名称"}, {Title: "尾4"}, {Title: "存储"},
					{Title: "状态"}, {Title: "优先级"}, {Title: "近 100 条"}, {Title: ""}}
				ui.Table(c, &u.chKeyList, keyCols, len(detail.Keys), func(row, col int) {
					k := detail.Keys[row]
					switch col {
					case 0:
						ui.Text(c, k.Name).SingleLine()
					case 1:
						ui.Text(c, k.KeyTail).SingleLine().Font(monoFont)
					case 2:
						ui.Text(c, k.SecretBackend).SingleLine().Font(monoFont)
					case 3:
						if k.Cooling {
							ui.Textf(c, "冷却 %ds (%s)", k.CoolingRemainSec, k.CoolingReason).TextColor(t.Warning)
						} else if k.Enabled {
							softBadge(c, t, s, "ok", "启用")
						} else {
							softBadge(c, t, s, "gray", "已停用")
						}
					case 4:
						ui.Text(c, strconv.Itoa(k.Priority)).SingleLine().Font(monoFont)
					case 5:
						okN, failN := keyStats(u.recent, v.Name, k.Name)
						if okN+failN == 0 {
							ui.Text(c, "—").TextColor(t.TextMuted)
						} else {
							ui.Textf(c, "%d✓ %d✗", okN, failN).SingleLine()
						}
					case 6:
						ui.MenuButton(c, "⋯", func(m *ui.Menu) {
							if m.Item("停用/启用").Chosen() {
								_ = u.svc.Channels.SetKeyEnabled(k.ID, !k.Enabled)
							}
							if m.Item("测试").Chosen() {
								res := u.svc.Channels.TestKey(k.ID)
								if res.OK {
									u.chTestMsg = "连通正常 (" + res.Stage + ")"
								} else {
									u.chTestMsg = res.Stage + " 失败: " + res.Error
								}
							}
							if m.Item("删除").Chosen() {
								u.chKeySel = row // 确认落刀目标 = 本行
								u.keyConfirmDelete = true
								u.confirmOwnerID = v.ID // 确认位绑定渠道（审查 Finding 2）
							}
						})
					}
				}).Height(140) // 定高内部滚动（防详情页超视口）
			}
			if u.keyForm.msg != "" {
				ui.Text(c, u.keyForm.msg)
			}
		})
	})
}

// channelModelSection 是「渠道模型」段：添加表单 + 「从上游获取」+ 模型表
// （公开模型/上游模型/启用 + 行尾 ⋯ 菜单：停用启用、删除——删除走 AlertDialog
// 且确认位绑定渠道）。
func (u *uiState) channelModelSection(c *ui.Context, t *ui.Theme, s softPalette, detail bridge.ChannelDetail) {
	v := detail.Channel
	card(c, t, func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "渠道模型").Bold().Grow(1)
				if softButton(c, t, s, "gray", "从上游获取").Clicked() {
					if ids, err := u.svc.Channels.FetchModels(v.ID); err != nil {
						u.chModelForm.msg = err.Error()
					} else {
						u.chModelForm.msg = "上游模型: " + strings.Join(ids, ", ")
					}
				}
			})
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "公开模型名:").Width(96).TextColor(t.TextMuted)
				ui.TextInput(c, &u.chModelForm.public).Grow(1)
				ui.Text(c, "上游模型名:").Width(96).TextColor(t.TextMuted)
				ui.TextInput(c, &u.chModelForm.upstream).Grow(1)
				if pillButton(c, t, "添加模型").Clicked() {
					if _, err := u.svc.Channels.AddModel(v.ID,
						u.chModelForm.public, u.chModelForm.upstream); err != nil {
						u.chModelForm.msg = err.Error()
					} else {
						u.chModelForm = channelModelFormData{}
					}
				}
			})
			if u.chModelForm.msg != "" {
				ui.Text(c, u.chModelForm.msg)
			}
			if len(detail.Models) == 0 {
				ui.Text(c, "尚无模型——该渠道不会接到任何请求，先添加公开模型").TextColor(t.TextMuted)
			} else {
				mCols := []ui.TableColumn{{Title: "公开模型"}, {Title: "上游模型"}, {Title: "启用"}, {Title: ""}}
				ui.Table(c, &u.chModelList, mCols, len(detail.Models), func(row, col int) {
					m := detail.Models[row]
					switch col {
					case 0:
						ui.Text(c, m.PublicModel).SingleLine()
					case 1:
						ui.Text(c, m.UpstreamModel).SingleLine().Font(monoFont)
					case 2:
						if m.Enabled {
							softBadge(c, t, s, "ok", "启用")
						} else {
							softBadge(c, t, s, "gray", "已停用")
						}
					case 3:
						ui.MenuButton(c, "⋯", func(mn *ui.Menu) {
							if mn.Item("停用/启用").Chosen() {
								_ = u.svc.Channels.SetModelEnabled(m.ID, !m.Enabled)
							}
							if mn.Item("删除").Chosen() {
								u.chModelSel = row // 确认落刀目标 = 本行
								u.chModelConfirmDel = true
								u.confirmOwnerID = v.ID // 确认位绑定渠道（审查 Finding 2）
							}
						})
					}
				}).Height(140) // 定高内部滚动（同密钥表）
			}
		})
	})
}

func (u *uiState) resetChannelForm() {
	u.chForm = chFormData{typeID: "anthropic-official", baseURL: "https://api.anthropic.com",
		enabled: true, priorityInput: "10"}
	u.chTestMsg = ""
	u.keyForm = keyFormData{}
	u.chModelForm = channelModelFormData{}
	u.keyConfirmDelete, u.chModelConfirmDel = false, false
	u.confirmOwnerID = ""
}

// labelsOf 保证当前值在 Select 选项内（mygo Select 对未知值的行为未验证，
// 不假定——把当前 typeID 的 label 兜底拼接进选项）。
func labelsOf(labels []string, byLabel map[string]string, typeID string) []string {
	for _, l := range labels {
		if byLabel[l] == typeID {
			return labels
		}
	}
	p, _ := provider.LookupPreset(typeID)
	if p.ID != "" {
		return append([]string{p.Label}, labels...)
	}
	return labels
}
