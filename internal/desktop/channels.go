package desktop

import (
	"fmt"
	"strconv"

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

// openChannelDetail 跳渠道详情页（Task 6 渲染）：锚定渠道 id、分段归零。
func (u *uiState) openChannelDetail(id string) {
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

// submitChannel 分支：优先级必须整数；Create 走类型预设（Select 写回的
// label 先解析回 id）；Update 改字段。Create 成功后选中新行（详情三段
// 立即可用）并复位表单；Update 成功只复位表单。
func (u *uiState) submitChannel() {
	prio, err := strconv.Atoi(u.chForm.priorityInput)
	if err != nil {
		u.chForm.msg = "优先级必须是整数"
		return
	}
	_, byLabel := presetLabels()
	tid := resolveTypeID(byLabel, u.chForm.typeID)
	if u.chForm.editID == "" {
		v, err := u.svc.Channels.Create(tid, u.chForm.name, u.chForm.baseURL,
			prio, u.chForm.enabled, u.chForm.allowPrivate)
		if err != nil {
			u.chForm.msg = err.Error()
			return
		}
		u.resetChannelForm()
		u.selectChannel(v.ID)
		return
	}
	if err := u.svc.Channels.Update(u.chForm.editID, u.chForm.name, u.chForm.baseURL,
		prio, u.chForm.enabled, u.chForm.allowPrivate); err != nil {
		u.chForm.msg = err.Error()
		return
	}
	u.resetChannelForm()
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

func (u *uiState) loadChannel(v bridge.ChannelView) {
	u.chForm = chFormData{typeID: v.Type, name: v.Name, baseURL: v.BaseURL,
		priorityInput: strconv.Itoa(v.Priority), enabled: v.Enabled,
		allowPrivate: v.AllowPrivateUpstream, editID: v.ID}
	u.chTestMsg = ""
	u.chConfirmDelete = false
	u.keyConfirmDelete = false  // 既有三页模式：换行/换渠道必须清确认位（防落刀他行）
	u.chModelConfirmDel = false // 审查 Finding 1：模型确认位同清
	u.keyForm = keyFormData{}
	u.chModelForm = channelModelFormData{}
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
