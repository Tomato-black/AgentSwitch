// access.go 是接入页（spec 2026-10-18-agent-access-config-design）：每 Agent
// （Claude Code / Codex CLI）多实际配置 + 全局配置（合并进所有实际配置），
// 「应用」即受管键投影写入 agent 真实配置文件；未应用的配置经持久文件 +
// 启动命令 / 「打开终端」在启动 Agent 时手动指定。取代旧「片段复制」页
// （旧 bridge.Access 已删除）。令牌展示不落本页（命令复制给真值、文件由
// bridge 层写），审计与错误经 bridge 方法返回的中文文案内联显示。
package desktop

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// accessForm 是接入配置新建/编辑表单状态（本页私有；open 非空即编辑）。
type accessForm struct {
	open          bool
	editID        string // 非空 = 编辑该配置
	name          string
	modelLabel    string // 选中的逻辑模型名（保存时映射回 ID）
	overrides     [][2]string
	showOverrides bool // 高级：覆盖项折叠开关
	msg           string
}

// accessTabTitles 返回两个 Agent tab 的标题（字符串快照锚点）。
func accessTabTitles() (claude, codex string) { return "Claude Code", "Codex CLI" }

// accessRowBadge 返回配置行「使用中」徽标文本（非 current 为空串）。
func accessRowBadge(isCurrent bool) string {
	if isCurrent {
		return "使用中"
	}
	return ""
}

// accessOverridesFromRows 把键值行转成 overrides map（空键/空值剔除；
// 同键后者胜——与合成层 overrides 优先语义一致）。
func accessOverridesFromRows(rows [][2]string) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		k := strings.TrimSpace(r[0])
		if k == "" {
			continue
		}
		out[k] = r[1]
	}
	return out
}

// accessModelLabels 返回 enabled 逻辑模型名列表（编辑器 Select 数据源）。
func accessModelLabels(models []bridge.ModelView) []string {
	var out []string
	for _, m := range models {
		if m.Enabled {
			out = append(out, m.Name)
		}
	}
	return out
}

// accessModelIDByLabel 按名映射回逻辑模型 ID（未命中返回空串；
// disabled 项也参与命中——编辑旧配置时回显不应丢引用）。
func accessModelIDByLabel(models []bridge.ModelView, label string) string {
	for _, m := range models {
		if m.Name == label {
			return m.ID
		}
	}
	return ""
}

// accessGlobalRows 把全局 env map 转成有序编辑行（键字典序，UI 稳定渲染）。
func accessGlobalRows(env map[string]string) [][2]string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sortStrings(keys)
	rows := make([][2]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, [2]string{k, env[k]})
	}
	return rows
}

// sortStrings 是包内极小排序助手（避免为一处调用引 sort 包后与其他
// 文件的将来改动纠缠；accessGlobalRows 单键场景）。
func sortStrings(keys []string) {
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
}

// accessGatewayGate 返回网关是否可用（应用/启动类操作的前提）与原因。
// 网关运行状态取实际服务状态（Status().State），不是按钮状态（架构不变量）。
func (u *uiState) accessGatewayGate() (bool, string) {
	st := u.svc.Gateway.Status()
	if st.State != "running" && st.State != "degraded" {
		return false, "网关未运行——请先在侧栏底条启动网关"
	}
	return true, ""
}

// accessSvc 返回本页的接入配置桥接（每帧构造，与既有页面惯例一致）。
func (u *uiState) accessSvc() bridge.AccessConfigs {
	return bridge.AccessConfigs{A: u.svc.Gateway.A}
}

// accessPage 是接入页（shell.go pageView 分发；签名即接线契约）。
// 结构：Agent tab 行 → 全局配置卡 → 实际配置列表卡 → 编辑器卡（表单 open 时）。
func (u *uiState) accessPage(c *ui.Context) {
	t := c.Theme()
	s := softs(t)
	acc := u.accessSvc()
	if u.accessApp == "" {
		u.accessApp = "claude"
	}
	claudeTitle, codexTitle := accessTabTitles()
	gateOK, gateReason := u.accessGatewayGate()

	ui.Column(c).Gap(12).Children(func() {
		// Agent tab 行
		ui.Row(c).Gap(8).Children(func() {
			if softButton(c, t, s, tabKind(u.accessApp == "claude"), claudeTitle).Clicked() {
				u.accessApp = "claude"
				u.accessMsg = ""
				u.accessReloadGlobal(acc)
			}
			if softButton(c, t, s, tabKind(u.accessApp == "codex"), codexTitle).Clicked() {
				u.accessApp = "codex"
				u.accessMsg = ""
				u.accessReloadGlobal(acc)
			}
		})
		if u.accessGlob == nil {
			u.accessReloadGlobal(acc)
		}

		// 全局配置卡
		card(c, t, func() {
			ui.Column(c).Gap(10).Children(func() {
				ui.Text(c, "全局配置").Bold()
				ui.Text(c, "以下环境变量会应用到本 Agent 的所有实际配置（实际配置的同名覆盖项优先）").TextColor(t.TextMuted)
				for i := range u.accessGlob {
					row := &u.accessGlob[i]
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c, &row[0]).Width(220).Placeholder("键")
						ui.TextInput(c, &row[1]).Grow(1).Placeholder("值")
						if softButton(c, t, s, "danger", "删除").Clicked() {
							u.accessGlob = append(u.accessGlob[:i], u.accessGlob[i+1:]...)
							return
						}
					})
				}
				ui.Row(c).Gap(8).Children(func() {
					if softButton(c, t, s, "gray", "＋ 添加一项").Clicked() {
						u.accessGlob = append(u.accessGlob, [2]string{})
					}
					if softButton(c, t, s, "act", "保存全局配置").Clicked() {
						if err := acc.SaveGlobalConfig(u.accessApp, accessOverridesFromRows(u.accessGlob)); err != nil {
							u.accessGlobMsg = err.Error()
						} else {
							u.accessGlobMsg = "已保存"
						}
					}
				})
				if u.accessGlobMsg != "" {
					ui.Text(c, u.accessGlobMsg).TextColor(t.TextMuted)
				}
			})
		})

		// 实际配置列表卡
		card(c, t, func() {
			ui.Column(c).Gap(10).Children(func() {
				ui.Text(c, "实际配置").Bold()
				if !gateOK {
					ui.Text(c, gateReason).TextColor(t.TextMuted)
				}
				list := acc.List(u.accessApp)
				if len(list) == 0 {
					ui.Text(c, "还没有配置——点击下方「新建配置」创建").TextColor(t.TextMuted)
				}
				hasCurrent := false
				for _, v := range list {
					if v.IsCurrent {
						hasCurrent = true
					}
					u.accessConfigRow(c, t, s, acc, v, gateOK)
				}
				if len(list) > 0 && !hasCurrent {
					ui.Text(c, "当前没有配置被应用，Agent 仍使用其原配置文件").TextColor(t.TextMuted)
				}
				if u.accessMsg != "" {
					ui.Text(c, u.accessMsg).TextColor(t.TextMuted)
				}
				ui.Row(c).Children(func() {
					ui.Box(c).Grow(1)
					if pillButton(c, t, "＋ 新建配置").Clicked() {
						u.accessForm = accessForm{open: true}
						u.accessPreloadForm(acc)
					}
				})
			})
		})

		// 编辑器卡（Task 15 表单）
		if u.accessForm.open {
			u.accessEditorCard(c, t, s, acc)
		}
	})
}

// tabKind 把选中态译成软按钮 kind（选中 act 高亮，未选 gray）。
func tabKind(selected bool) string {
	if selected {
		return "act"
	}
	return "gray"
}

// accessReloadGlobal 把全局配置读入编辑行（tab 切换/首帧；不覆盖正在
// 编辑的行以外的时机由调用点控制）。
func (u *uiState) accessReloadGlobal(acc bridge.AccessConfigs) {
	env, err := acc.GlobalConfig(u.accessApp)
	if err != nil {
		u.accessGlobMsg = err.Error()
		return
	}
	u.accessGlob = accessGlobalRows(env)
	u.accessGlobMsg = ""
}

// accessPreloadForm 为「编辑」预填表单（新建仅开空表单 + 默认模型名）。
func (u *uiState) accessPreloadForm(acc bridge.AccessConfigs) {
	var models []bridge.ModelView
	if u.svc.Logical != nil {
		models = u.svc.Logical.List()
	}
	labels := accessModelLabels(models)
	if u.accessForm.editID != "" {
		for _, v := range acc.List(u.accessApp) {
			if v.ID == u.accessForm.editID {
				u.accessForm.name = v.Name
				u.accessForm.modelLabel = v.LogicalModelName
				for k, val := range v.Overrides {
					u.accessForm.overrides = append(u.accessForm.overrides, [2]string{k, val})
				}
			}
		}
		return
	}
	if len(labels) > 0 {
		u.accessForm.modelLabel = labels[0]
	}
}

// accessConfigRow 渲染一行实际配置：名称 + 使用中徽标 + 逻辑模型名 +
// 操作按钮（应用/编辑/复制启动命令/打开终端/删除）。网关不可用时操作
// 按钮不渲染（列表仍可见）。
func (u *uiState) accessConfigRow(c *ui.Context, t *ui.Theme, s softPalette, acc bridge.AccessConfigs, v bridge.AccessConfigView, gateOK bool) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, v.Name).Width(140)
		if badge := accessRowBadge(v.IsCurrent); badge != "" {
			softBadge(c, t, s, "ok", badge)
		}
		ui.Text(c, "→ "+v.LogicalModelName).TextColor(t.TextMuted).Grow(1)
		if !gateOK {
			return
		}
		if v.IsCurrent {
			ui.Text(c, "已应用").TextColor(t.TextMuted)
		} else if softButton(c, t, s, "act", "应用").Clicked() {
			if err := acc.Apply(v.ID); err != nil {
				u.accessMsg = "应用失败：" + err.Error()
			} else {
				u.accessMsg = "已应用「" + v.Name + "」——Agent 下次启动生效"
			}
		}
		if softButton(c, t, s, "gray", "编辑").Clicked() {
			u.accessForm = accessForm{open: true, editID: v.ID}
			u.accessPreloadForm(acc)
		}
		if softButton(c, t, s, "gray", "复制启动命令").Clicked() {
			cmd, err := acc.LaunchCommand(v.ID)
			if err != nil {
				u.accessMsg = "生成启动命令失败：" + err.Error()
			} else {
				mygo.Clipboard.Write(transfer.TextData(cmd))
				u.accessMsg = "启动命令已复制"
			}
		}
		if softButton(c, t, s, "gray", "打开终端").Clicked() {
			if err := acc.OpenTerminal(v.ID); err != nil {
				u.accessMsg = "打开终端失败：" + err.Error()
			} else {
				u.accessMsg = ""
			}
		}
		if softButton(c, t, s, "danger", "删除").Clicked() {
			acc.Remove(v.ID)
			u.accessMsg = "已删除「" + v.Name + "」——Agent 配置文件保留最后状态"
		}
	})
}

// accessEditorCard 渲染新建/编辑表单：名称、逻辑模型下拉、覆盖项折叠区、
// 保存/取消。保存：编辑走 Update，新建走 Create；模型名映射回 ID。
func (u *uiState) accessEditorCard(c *ui.Context, t *ui.Theme, s softPalette, acc bridge.AccessConfigs) {
	card(c, t, func() {
		ui.Column(c).Gap(10).Children(func() {
			title := "新建配置"
			if u.accessForm.editID != "" {
				title = "编辑配置"
			}
			ui.Text(c, title).Bold()

			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
				ui.TextInput(c, &u.accessForm.name).Grow(1)
			})

			var models []bridge.ModelView
			if u.svc.Logical != nil {
				models = u.svc.Logical.List()
			}
			labels := accessModelLabels(models)
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "逻辑模型:").Width(96).TextColor(t.TextMuted)
				if len(labels) == 0 {
					ui.Text(c, "还没有启用的逻辑模型——先在「逻辑模型」页创建并启用").TextColor(t.TextMuted)
				} else {
					ui.Select(c, &u.accessForm.modelLabel, labels)
				}
			})

			if softButton(c, t, s, "gray", coverToggleLabel(u.accessForm.showOverrides)).Clicked() {
				u.accessForm.showOverrides = !u.accessForm.showOverrides
			}
			if u.accessForm.showOverrides {
				ui.Text(c, "覆盖项：仅本配置生效，优先于全局配置（不能改网关地址/令牌/模型名基础键）").TextColor(t.TextMuted)
				for i := range u.accessForm.overrides {
					row := &u.accessForm.overrides[i]
					ui.Row(c).Gap(8).Children(func() {
						ui.TextInput(c, &row[0]).Width(220).Placeholder("键")
						ui.TextInput(c, &row[1]).Grow(1).Placeholder("值")
						if softButton(c, t, s, "danger", "删除").Clicked() {
							u.accessForm.overrides = append(u.accessForm.overrides[:i], u.accessForm.overrides[i+1:]...)
						}
					})
				}
				if softButton(c, t, s, "gray", "＋ 添加覆盖项").Clicked() {
					u.accessForm.overrides = append(u.accessForm.overrides, [2]string{})
				}
			}

			if u.accessForm.msg != "" {
				ui.Text(c, u.accessForm.msg).TextColor(t.Danger)
			}

			ui.Row(c).Gap(8).Children(func() {
				if pillButton(c, t, "保存").Clicked() {
					u.accessSaveForm(acc, models)
				}
				if softButton(c, t, s, "gray", "取消").Clicked() {
					u.accessForm = accessForm{}
				}
			})
		})
	})
}

// coverToggleLabel 是覆盖项折叠开关的文案。
func coverToggleLabel(open bool) string {
	if open {
		return "高级：收起覆盖项"
	}
	return "高级：覆盖项"
}

// accessSaveForm 校验并保存表单；成功关闭编辑态，失败内联中文错误。
func (u *uiState) accessSaveForm(acc bridge.AccessConfigs, models []bridge.ModelView) {
	f := &u.accessForm
	if strings.TrimSpace(f.name) == "" {
		f.msg = "请填写配置名"
		return
	}
	modelID := accessModelIDByLabel(models, f.modelLabel)
	if modelID == "" {
		f.msg = "请选择逻辑模型"
		return
	}
	overrides := accessOverridesFromRows(f.overrides)
	if f.editID != "" {
		if err := acc.Update(f.editID, f.name, modelID, overrides); err != nil {
			f.msg = err.Error()
			return
		}
	} else {
		if _, err := acc.Create(u.accessApp, f.name, modelID, overrides); err != nil {
			f.msg = err.Error()
			return
		}
	}
	u.accessMsg = "已保存「" + f.name + "」"
	u.accessForm = accessForm{}
}
