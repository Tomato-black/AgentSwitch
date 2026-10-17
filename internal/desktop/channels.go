package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/provider"
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

// channelsView 是渠道页（spec §8）：渠道表 + 选中渠道详情（连接配置/密钥/模型）。
func (u *uiState) channelsView(c *ui.Context) {
	t := c.Theme()
	chans := u.svc.Channels.List()
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "渠道").Bold().FontSize(t.FontSize + 2)
			ui.Text(c, strconv.Itoa(len(chans))).TextColor(t.TextMuted)
		})
		if len(chans) == 0 {
			ui.Text(c, "尚无渠道——在下方表单创建第一个渠道").TextColor(t.TextMuted)
		}
		cols := []ui.TableColumn{{Title: "名称"}, {Title: "类型"}, {Title: "状态"},
			{Title: "密钥数"}, {Title: "模型数"}, {Title: "优先级"}}
		ui.Table(c, &u.chList, cols, len(chans), func(row, col int) {
			ch := chans[row]
			switch col {
			case 0:
				ui.Text(c, ch.Name).SingleLine()
			case 1:
				ui.Text(c, ch.Type).SingleLine()
			case 2:
				if ch.Enabled {
					ui.Badge(c, "启用")
				} else {
					ui.Text(c, "停用").TextColor(t.TextMuted)
				}
			case 3:
				ui.Text(c, strconv.Itoa(ch.KeyCount)).SingleLine().Font(monoFont)
			case 4:
				ui.Text(c, strconv.Itoa(ch.ModelCount)).SingleLine().Font(monoFont)
			case 5:
				ui.Text(c, strconv.Itoa(ch.Priority)).SingleLine().Font(monoFont)
			}
		}).Height(96) // 定高内部滚动：详情并排后纵向预算充裕，定高保可预期
		ui.Divider(c)

		// 创建表单：类型 Select（预设）+ 名称 + BaseURL（类型变更时预填默认值）
		labels, byLabel := presetLabels()
		tid := resolveTypeID(byLabel, u.chForm.typeID)
		if u.prevType != u.chForm.typeID { // 类型变更 → 预填默认 BaseURL（新建态才覆盖）
			u.prevType = u.chForm.typeID
			if u.chForm.editID == "" {
				u.chForm.baseURL = presetOf(tid).DefaultBaseURL
			}
		}
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "类型:").Width(96).TextColor(t.TextMuted)
			ui.Select(c, &u.chForm.typeID, labelsOf(labels, byLabel, u.chForm.typeID))
			ui.Text(c, presetOf(tid).Description).TextColor(t.TextMuted)
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.chForm.name).Grow(1)
			ui.Text(c, "Base URL:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.chForm.baseURL).Grow(1)
		})
		ui.Row(c).Gap(16).Children(func() {
			ui.Text(c, "启用").TextColor(t.TextMuted)
			ui.Switch(c, &u.chForm.enabled)
			ui.Text(c, "允许私网上游").TextColor(t.TextMuted)
			ui.Switch(c, &u.chForm.allowPrivate)
		})
		if u.chForm.msg != "" {
			ui.Text(c, u.chForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			saveLabel := "保存渠道"
			if u.chForm.editID != "" {
				saveLabel = "更新渠道"
			}
			if ui.PrimaryButton(c, saveLabel).Clicked() {
				u.submitChannel()
			}
			if ui.Button(c, "载入所选").Clicked() {
				if sel := u.chList.Selected; sel != nil && *sel >= 0 && *sel < len(chans) {
					u.loadChannel(chans[*sel])
				}
			}
			if ui.Button(c, "删除").Clicked() && u.chForm.editID != "" {
				u.chConfirmDelete = true
			}
			if u.chConfirmDelete && ui.Button(c, "确认删除").Clicked() {
				if err := u.svc.Channels.Remove(u.chForm.editID); err != nil {
					u.chForm.msg = err.Error()
				} else {
					u.resetChannelForm()
				}
				u.chConfirmDelete = false
			}
			if ui.Button(c, "测试连通").Clicked() && u.chForm.editID != "" {
				res := u.svc.Channels.Test(u.chForm.editID)
				if res.OK {
					u.chTestMsg = "连通正常 (" + res.Stage + ")"
				} else {
					u.chTestMsg = res.Stage + " 失败: " + res.Error
				}
			}
		})
		if u.chTestMsg != "" {
			ui.Text(c, u.chTestMsg)
		}

		// ---- 选中渠道详情（跟随表格行选中；创建成功后自动选中新行，
		// 密钥/模型子面板立即可用）。简报的 editID 门与 Step 2 测试冲突：
		// 创建后表单复位 editID 必空，而 TestChannelsKeysBatchAdd /
		// ModelAddAndDuplicate 紧接着点子面板按钮——详情锚定行选中。----
		if u.chSel < 0 || u.chSel >= len(chans) {
			return
		}
		detail, err := u.svc.Channels.Get(chans[u.chSel].ID)
		if err != nil {
			ui.Textf(c, "渠道详情加载失败: %v", err).TextColor(t.Danger)
			return
		}
		// 渠道上下文切换（详情渠道变更）：清子表选中与两个确认位——
		// 既有三页「确认清位」模式的渠道级版（审查 Finding 2，防落刀他渠道）。
		if u.prevChannelID != detail.Channel.ID {
			u.prevChannelID = detail.Channel.ID
			u.chKeySel, u.chModelSel = -1, -1
			u.keyConfirmDelete, u.chModelConfirmDel = false, false
			u.confirmOwnerID = ""
		}
		ui.Divider(c)

		// 密钥 | 模型 两列并排：竖排堆叠时详情静态高约 730px，超出 720px
		// 生产视口（实测模型按钮行 Y≈775 被裁剪、点击落空）——并排后任何
		// 段的按钮都留在视口内；子表定高、内部滚动。
		ui.Row(c).Gap(16).Children(func() {
			// ---- 密钥列：子表 + 批量导入表单 + 操作行 ----
			ui.Column(c).Gap(12).Grow(1).Children(func() {
				ui.Text(c, "密钥").Bold()
				if len(detail.Keys) == 0 {
					ui.Text(c, "尚无密钥——导入一枚后该渠道才能接流量").TextColor(t.TextMuted)
				}
				keyCols := []ui.TableColumn{{Title: "名称"}, {Title: "尾4"}, {Title: "存储"},
					{Title: "冷却"}, {Title: "启用"}, {Title: "优先级"}}
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
						} else {
							ui.Text(c, "—").TextColor(t.TextMuted)
						}
					case 4:
						if k.Enabled {
							ui.Badge(c, "启用")
						} else {
							ui.Text(c, "停用").TextColor(t.TextMuted)
						}
					case 5:
						ui.Text(c, strconv.Itoa(k.Priority)).SingleLine().Font(monoFont)
					}
				}).Height(84) // 定高内部滚动（详情并排后纵向预算充裕，定高保可预期）
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "密钥名称:").Width(96).TextColor(t.TextMuted)
					ui.TextInput(c, &u.keyForm.name).Grow(1)
				})
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "API Keys（每行一枚）:").Width(140).TextColor(t.TextMuted)
					// 多行输入（ui.TextArea，mygo v0.5.1 实有）——计划注记的
					// 单行 + 逗号替换 hack 按审查授权移除，AddKeys 按行拆语义
					// 不变。定高（flagScrollY 内部滚动；默认 MinHeight 80px）。
					ui.TextArea(c, &u.keyForm.keysText).Grow(1).MinHeight(0).Height(64)
				})
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, "允许文件降级").TextColor(t.TextMuted)
					ui.Switch(c, &u.keyForm.allowFallback)
					if ui.PrimaryButton(c, "导入密钥").Clicked() {
						n, err := u.svc.Channels.AddKeys(detail.Channel.ID, u.keyForm.name,
							u.keyForm.keysText, u.keyForm.allowFallback)
						if err != nil {
							u.keyForm.msg = err.Error()
						} else {
							u.keyForm.msg = fmt.Sprintf("已导入 %d 枚密钥", n)
							u.keyForm.name, u.keyForm.keysText = "", ""
						}
					}
					if ui.Button(c, "停用/启用密钥").Clicked() {
						if sel := u.chKeyList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Keys) {
							k := detail.Keys[*sel]
							_ = u.svc.Channels.SetKeyEnabled(k.ID, !k.Enabled)
						}
					}
				})
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "测试所选密钥").Clicked() {
						if sel := u.chKeyList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Keys) {
							res := u.svc.Channels.TestKey(detail.Keys[*sel].ID)
							if res.OK {
								u.chTestMsg = "连通正常 (" + res.Stage + ")"
							} else {
								u.chTestMsg = res.Stage + " 失败: " + res.Error
							}
						}
					}
					if ui.Button(c, "删除密钥").Clicked() {
						if sel := u.chKeyList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Keys) {
							u.keyConfirmDelete = true
							u.confirmOwnerID = detail.Channel.ID // 确认位绑定渠道（审查 Finding 2）
						}
					}
					// 确认钮仅对武装时的渠道渲染：换渠道后 owner 不匹配，视同未武装。
					if u.keyConfirmDelete && u.confirmOwnerID == detail.Channel.ID && ui.Button(c, "确认删除密钥").Clicked() {
						if sel := u.chKeyList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Keys) {
							if err := u.svc.Channels.RemoveKey(detail.Keys[*sel].ID); err != nil {
								u.keyForm.msg = err.Error()
							}
						}
						u.keyConfirmDelete = false
						u.confirmOwnerID = ""
					}
				})
				if u.keyForm.msg != "" {
					ui.Text(c, u.keyForm.msg)
				}
			})
			// ---- 模型列：子表 + 添加表单 + 从上游获取 ----
			ui.Column(c).Gap(12).Grow(1).Children(func() {
				ui.Text(c, "模型").Bold()
				if len(detail.Models) == 0 {
					ui.Text(c, "尚无模型——该渠道不会接到任何请求，先添加公开模型").TextColor(t.TextMuted)
				}
				mCols := []ui.TableColumn{{Title: "公开模型"}, {Title: "上游模型"}, {Title: "启用"}}
				ui.Table(c, &u.chModelList, mCols, len(detail.Models), func(row, col int) {
					m := detail.Models[row]
					switch col {
					case 0:
						ui.Text(c, m.PublicModel).SingleLine()
					case 1:
						ui.Text(c, m.UpstreamModel).SingleLine().Font(monoFont)
					case 2:
						if m.Enabled {
							ui.Badge(c, "启用")
						} else {
							ui.Text(c, "停用").TextColor(t.TextMuted)
						}
					}
				}).Height(84) // 定高内部滚动（同密钥表）
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "公开模型名:").Width(96).TextColor(t.TextMuted)
					ui.TextInput(c, &u.chModelForm.public).Grow(1)
					ui.Text(c, "上游模型名:").Width(96).TextColor(t.TextMuted)
					ui.TextInput(c, &u.chModelForm.upstream).Grow(1)
				})
				ui.Row(c).Gap(8).Children(func() {
					if ui.PrimaryButton(c, "添加模型").Clicked() {
						if _, err := u.svc.Channels.AddModel(detail.Channel.ID, u.chModelForm.public, u.chModelForm.upstream); err != nil {
							u.chModelForm.msg = err.Error()
						} else {
							u.chModelForm = channelModelFormData{}
						}
					}
					if ui.Button(c, "停用/启用模型").Clicked() {
						if sel := u.chModelList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Models) {
							m := detail.Models[*sel]
							_ = u.svc.Channels.SetModelEnabled(m.ID, !m.Enabled)
						}
					}
				})
				ui.Row(c).Gap(8).Children(func() {
					if ui.Button(c, "从上游获取").Clicked() {
						if ids, err := u.svc.Channels.FetchModels(detail.Channel.ID); err != nil {
							u.chModelForm.msg = err.Error()
						} else {
							u.chModelForm.msg = "上游模型: " + strings.Join(ids, ", ")
						}
					}
					if ui.Button(c, "删除模型").Clicked() {
						if sel := u.chModelList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Models) {
							u.chModelConfirmDel = true
							u.confirmOwnerID = detail.Channel.ID // 确认位绑定渠道（审查 Finding 2）
						}
					}
					// 两段式确认（审查 Finding 1，全局约束「删除一律两段确认」，
					// 简报模型段代码漏了）：首击仅武装，确认才落刀。
					if u.chModelConfirmDel && u.confirmOwnerID == detail.Channel.ID && ui.Button(c, "确认删除模型").Clicked() {
						if sel := u.chModelList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Models) {
							if err := u.svc.Channels.RemoveModel(detail.Models[*sel].ID); err != nil {
								u.chModelForm.msg = err.Error()
							}
						}
						u.chModelConfirmDel = false
						u.confirmOwnerID = ""
					}
				})
				if u.chModelForm.msg != "" {
					ui.Text(c, u.chModelForm.msg)
				}
			})
		})
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
