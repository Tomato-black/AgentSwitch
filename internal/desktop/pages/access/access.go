// access.go 是接入页（spec 2026-10-18-agent-access-config-design）：每 Agent
// （Claude Code / Codex，后者覆盖 CLI / 桌面版 / IDE 插件）多实际配置 +
// 全局配置（合并进所有实际配置），
// 「应用」即受管键投影写入 agent 真实配置文件；未应用的配置经持久文件 +
// 启动命令 / 「打开终端」在启动 Agent 时手动指定。取代旧「片段复制」页
// （旧 bridge.Access 已删除）。编辑器默认渲染真实网关令牌（2026-10-21 用户
// 裁定）；保存时锁定键仍由 bridge 剔除、令牌永不落库。审计与错误经 bridge
// 方法返回的中文文案内联显示。
package access

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/pages/access/claude"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// accessSyncCodexFromDoc JSON→表单回填（Codex）：显式 models 优先，缺失时
// 回退「绑定模型 + 启用逻辑模型」；同时回填 codex 高级设置。解析失败保留
// 旧状态（与角色回填同款「编辑到一半不闪烁」）。
func syncCodexFromDoc(st *state.State, models []bridge.ModelView) {
	doc, err := accesskit.DocObject(st.Access.Form.DocJSON)
	if err != nil {
		return
	}
	f := &st.Access.Form
	if names, ok := accesskit.DocModels(doc); ok {
		f.CodexModels = names
	} else {
		f.CodexModels = accesskit.CodexFallbackModels(f.ModelLabel, models)
	}
	codex, _ := doc["codex"].(map[string]any)
	f.SubagentModel = accesskit.DocCodexString(codex, "subagent_model")
	f.SubagentEffort = accesskit.DocCodexString(codex, "subagent_reasoning_effort")
	f.AutoReviewModel = accesskit.DocCodexString(codex, "auto_review_model")
}

// accessPatchCodexModels 表单→JSON 补丁：模型列表写回文档 models，默认
// 模型（保存时的 logical_model_id）跟随首行。补丁失败保留旧文档。
func patchCodexModels(st *state.State) {
	f := &st.Access.Form
	raw, err := accesskit.DocSetModels(f.DocJSON, f.CodexModels)
	if err != nil {
		return
	}
	f.DocJSON = raw
	if len(f.CodexModels) > 0 {
		f.ModelLabel = f.CodexModels[0]
	}
}

// accessCodexNextModel 返回 labels 中第一个未出现在 models 里的模型名
// （「＋ 添加模型」选中项）；全部已用返回 ok=false，避免重复行。
func accessCodexNextModel(labels, models []string) (string, bool) {
	for _, l := range labels {
		if !slices.Contains(models, l) {
			return l, true
		}
	}
	return "", false
}

// accessMoveCodexModel 把 models[i] 与相邻行换位（delta=-1 上移 / +1 下移）；
// 首行上移、末行下移与越界下标都是 no-op（与 moveTarget 同语义）。
func accessMoveCodexModel(models []string, i, delta int) {
	j := i + delta
	if i < 0 || i >= len(models) || j < 0 || j >= len(models) {
		return
	}
	models[i], models[j] = models[j], models[i]
}

// accessPatchCodexSettings 表单→JSON 补丁：三项高级设置写回 codex 对象
// （全空删除键）；补丁失败保留旧文档。
func patchCodexSettings(st *state.State) {
	f := &st.Access.Form
	raw, err := accesskit.DocSetCodex(f.DocJSON, f.SubagentModel, f.SubagentEffort, f.AutoReviewModel)
	if err != nil {
		return
	}
	f.DocJSON = raw
}

// Codex 高级设置下拉的空值显示项（空值语义 = 继承/跟随）。
const (
	accessCodexSubagentFollow    = "（跟随主模型）"
	accessCodexEffortDefault     = "默认"
	accessCodexAutoReviewDefault = "主模型自己审批（默认）"
)

// accessCodexEffortChoices 是子代理推理强度的可选值（Codex config.toml
// [agents].default_subagent_reasoning_effort 取值）。
var accessCodexEffortChoices = []string{"low", "medium", "high", "xhigh"}

// accessCodexSelectLabel 把文档空值映射成下拉默认显示项（非空原样）。
func accessCodexSelectLabel(raw, defaultLabel string) string {
	if strings.TrimSpace(raw) == "" {
		return defaultLabel
	}
	return raw
}

// accessCodexSelectRaw 是 accessCodexSelectLabel 的逆映射：默认显示项
// 写回空串（不落 codex 键）。
func accessCodexSelectRaw(label, defaultLabel string) string {
	if label == defaultLabel {
		return ""
	}
	return strings.TrimSpace(label)
}

// accessCodexModelOptions 构造模型下拉选项：默认项置首，其后按序追加
// codexModels（去空去重），列表外旧引用补到末尾（编辑旧配置不丢值）。
func accessCodexModelOptions(defaultLabel string, models []string, current string) []string {
	opts := []string{defaultLabel}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(opts, v) {
			opts = append(opts, v)
		}
	}
	for _, m := range models {
		add(m)
	}
	add(current)
	return opts
}

// accessCodexEffortOptions 构造推理强度选项：默认项 + 固定取值；未知旧值
// 补到末尾，避免 Select 显示空值。
func accessCodexEffortOptions(current string) []string {
	opts := make([]string, 0, len(accessCodexEffortChoices)+2)
	opts = append(opts, accessCodexEffortDefault)
	opts = append(opts, accessCodexEffortChoices...)
	if v := strings.TrimSpace(current); v != "" && !slices.Contains(opts, v) {
		opts = append(opts, v)
	}
	return opts
}

// accessCodexModelMetaLines 把 Codex 模型列表渲染为只读元数据预览行
// （spec 2026-10-27 §6.2）：按 codexModels 顺序，首行标「默认」；每行含
// 名称、显示名（为空或与名称相同则省略）、上下文窗口与思考等级
// （含默认等级）；上下文窗口/思考等级为空时省略对应字段；模型不存在时
// 标「（模型不存在）」，避免预览与目录内容无声不一致。
func accessCodexModelMetaLines(codexModels []string, models []bridge.ModelView) []string {
	byName := make(map[string]bridge.ModelView, len(models))
	for _, m := range models {
		if _, ok := byName[m.Name]; !ok {
			byName[m.Name] = m
		}
	}
	out := make([]string, 0, len(codexModels))
	for i, name := range codexModels {
		line := name
		if i == 0 {
			line = "默认 · " + line
		}
		m, ok := byName[name]
		if !ok {
			out = append(out, line+"（模型不存在）")
			continue
		}
		display := strings.TrimSpace(m.DisplayName)
		parts := []string{line}
		if display != "" && display != name {
			parts = append(parts, display)
		}
		if m.ContextWindow > 0 {
			parts = append(parts, strconv.FormatInt(m.ContextWindow, 10))
		}
		if len(m.ReasoningLevels) > 0 {
			levels := strings.Join(m.ReasoningLevels, "/")
			if m.DefaultReasoningLevel != "" {
				levels += "（默认 " + m.DefaultReasoningLevel + "）"
			}
			parts = append(parts, levels)
		}
		out = append(out, strings.Join(parts, " · "))
	}
	return out
}

// accessGatewayGate 返回网关是否可用（应用/启动类操作的前提）与原因。
func accessGatewayGate(st *state.State) (bool, string) {
	return accesskit.GatewayGate(st.Svc.Gateway)
}

// accessSvc 返回本页的接入配置桥接（每帧构造，与既有页面惯例一致）。
func accessSvc(st *state.State) bridge.AccessConfigs {
	return bridge.AccessConfigs{A: st.Svc.Gateway.A}
}

// accessPage 是接入页（shell.go pageView 分发；签名即接线契约）。
// 结构：Agent tab 行 → 全局配置卡 → 实际配置列表卡 → 编辑器卡（表单 open 时）。
func Page(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	acc := accessSvc(st)
	if st.Access.App == "" {
		st.Access.App = "claude"
	}
	claudeTitle, codexTitle, desktopTitle, guideTitle := accesskit.TabTitles()
	gateOK, gateReason := accessGatewayGate(st)

	// 内容列包 Scroll（同 channels/logicalmodels 的长页面）：编辑器（角色表 +
	// 320 高 TextArea）会让页面超出视口，没有滚动容器时底部内容不可达。
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			// Agent tab 行（切 tab 重置编辑态，避免表单跨 Agent 误存）
			ui.Row(c).Gap(8).Children(func() {
				if uikit.SoftButton(c, t, s, accesskit.TabKind(st.Access.App == "claude"), claudeTitle).Clicked() {
					st.Access.App = "claude"
					st.Access.Msg = ""
					st.Access.Form = state.AccessForm{}
					reloadGlobal(st, acc)
				}
				if uikit.SoftButton(c, t, s, accesskit.TabKind(st.Access.App == "codex"), codexTitle).Clicked() {
					st.Access.App = "codex"
					st.Access.Msg = ""
					st.Access.Form = state.AccessForm{}
					st.Access.DesktopForm = state.DesktopForm{}
					reloadGlobal(st, acc)
				}
				if uikit.SoftButton(c, t, s, accesskit.TabKind(st.Access.App == "claude_desktop"), desktopTitle).Clicked() {
					st.Access.App = "claude_desktop"
					st.Access.Msg = ""
					st.Access.Form = state.AccessForm{}
					st.Access.DesktopForm = state.DesktopForm{}
				}
				if uikit.SoftButton(c, t, s, accesskit.TabKind(st.Access.App == "guide"), guideTitle).Clicked() {
					st.Access.App = "guide"
					st.Access.Msg = ""
					st.Access.Form = state.AccessForm{}
					st.Access.DesktopForm = state.DesktopForm{}
				}
			})
			// 连接指南页签：只渲染通用连接信息卡（第三方系统如 Kun 照此
			// 在目标系统里手工填写），跳过 Agent 专属的全局配置/实际配置/
			// 编辑器（提前退出本闭包，下方卡片不再渲染）。
			if st.Access.App == "guide" {
				guideCard(c, t, s, st)
				return
			}
			if st.Access.Glob == nil {
				reloadGlobal(st, acc)
			}

			// 全局配置卡（Desktop 无 env 合成层，不显示）
			if st.Access.App != "claude_desktop" {
				uikit.Card(c, t, func() {
					ui.Column(c).Gap(10).Children(func() {
						ui.Text(c, "全局配置").Bold()
						ui.Text(c, "以下环境变量会应用到本 Agent 的所有实际配置（实际配置的同名覆盖项优先）").TextColor(t.TextMuted)
						// 行内删除只记下标，循环结束后统一应用——构建中改切片会
						// 越界（logicalmodels.go 同款注释与模式）。
						globDel := -1
						for i := range st.Access.Glob {
							row := &st.Access.Glob[i]
							ui.Row(c).Gap(8).Children(func() {
								ui.TextInput(c, &row[0]).Width(220).Placeholder("键")
								ui.TextInput(c, &row[1]).Grow(1).Placeholder("值")
								if uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
									globDel = i
								}
							})
						}
						if globDel >= 0 {
							st.Access.Glob = append(st.Access.Glob[:globDel], st.Access.Glob[globDel+1:]...)
						}
						ui.Row(c).Gap(8).Children(func() {
							if uikit.SoftButton(c, t, s, "gray", "＋ 添加一项").Clicked() {
								st.Access.Glob = append(st.Access.Glob, [2]string{})
							}
							if uikit.SoftButton(c, t, s, "act", "保存全局配置").Clicked() {
								// 行绑定随输入即时更新，此处 st.Access.Glob 已是待保存
								// 的新值；重定位旧派生值需要保存前的持久基线。
								oldGlobal, _ := acc.GlobalConfig(st.Access.App)
								if err := acc.SaveGlobalConfig(st.Access.App, accesskit.OverridesFromRows(st.Access.Glob)); err != nil {
									st.Access.GlobMsg = err.Error()
								} else {
									st.Access.GlobMsg = "已保存"
									// 编辑器打开时全局基线变了：删除旧基线派生值并
									// 按新基线与当前表单模型重预览（Important#1）。
									// Codex 另有只读 config.toml 预览，同样需要按新
									// 文档刷新，否则停留在保存前的结果。
									if st.Access.Form.Open {
										if rebased, err := bridge.RebaseAccessDoc(st.Access.App, st.Access.Form.DocJSON, oldGlobal); err == nil {
											st.Access.Form.DocJSON = rebased
										}
										accesskit.RepreviewForm(st, acc)
										if st.Access.App == "codex" {
											refreshCodexPreview(st, acc)
										}
									}
								}
							}
						})
						if st.Access.GlobMsg != "" {
							ui.Text(c, st.Access.GlobMsg).TextColor(t.TextMuted)
						}
					})
				})
			}

			// 实际配置列表卡
			uikit.Card(c, t, func() {
				ui.Column(c).Gap(10).Children(func() {
					ui.Text(c, "实际配置").Bold()
					if st.Access.App == "codex" {
						ui.Text(c, "Codex CLI / 桌面版 / IDE 插件共用 ~/.codex/config.toml；应用后需完全退出 Codex 再重新打开").TextColor(t.TextMuted)
						ui.Text(c, "桌面版模型选择器只显示 AgentSwitch 逻辑模型；已有对话的供应商不会迁移，请新建对话").TextColor(t.TextMuted)
					}
					if !gateOK {
						ui.Text(c, gateReason).TextColor(t.TextMuted)
					}
					list := acc.List(st.Access.App)
					if len(list) == 0 {
						ui.Text(c, "还没有配置——点击下方「新建配置」创建").TextColor(t.TextMuted)
					}
					hasCurrent := false
					for _, v := range list {
						if v.IsCurrent {
							hasCurrent = true
						}
						if st.Access.App == "claude_desktop" {
							desktopConfigRow(c, t, s, st, acc, v, gateOK)
						} else {
							configRow(c, t, s, st, acc, v, gateOK)
						}
					}
					if len(list) > 0 && !hasCurrent {
						ui.Text(c, "当前没有配置被应用，Agent 仍使用其原配置文件").TextColor(t.TextMuted)
					}
					if st.Access.Msg != "" {
						ui.Text(c, st.Access.Msg).TextColor(t.TextMuted)
					}
					ui.Row(c).Children(func() {
						ui.Box(c).Grow(1)
						if uikit.PillButton(c, t, "＋ 新建配置").Clicked() {
							if st.Access.App == "claude_desktop" {
								st.Access.DesktopForm = state.DesktopForm{Open: true}
							} else {
								st.Access.Form = state.AccessForm{Open: true}
								preloadForm(st, acc)
							}
						}
					})
				})
			})

			// 编辑器卡（通用表单按 Agent 分发 / Desktop 角色映射表单）
			if st.Access.App == "claude_desktop" {
				if st.Access.DesktopForm.Open {
					desktopEditorCard(c, t, s, st, acc, gateOK)
				}
			} else if st.Access.Form.Open {
				if st.Access.App == "codex" {
					codexEditorCard(c, t, s, st, acc, gateOK)
				} else {
					claude.EditorCard(c, t, s, st, acc, gateOK)
				}
			}
		})
	})
}

// accessReloadGlobal 把全局配置读入编辑行（tab 切换/首帧；不覆盖正在
// 编辑的行以外的时机由调用点控制）。
func reloadGlobal(st *state.State, acc bridge.AccessConfigs) {
	env, err := acc.GlobalConfig(st.Access.App)
	if err != nil {
		st.Access.GlobMsg = err.Error()
		return
	}
	st.Access.Glob = accesskit.GlobalRows(env)
	st.Access.GlobMsg = ""
}

// accessPreloadForm 打开编辑器：预填名称/模型，加载 PreviewDoc 合成预览
// （新建以 "{}" 为底；预览失败回退原文并内联报错）。
func preloadForm(st *state.State, acc bridge.AccessConfigs) {
	f := &st.Access.Form
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	if f.ModelLabel == "" {
		if labels := accesskit.ModelLabels(models); len(labels) > 0 {
			f.ModelLabel = labels[0]
		}
	}
	raw := "{}"
	if f.EditID != "" {
		for _, v := range acc.List(st.Access.App) {
			if v.ID == f.EditID {
				f.Name = v.Name
				f.ModelLabel = v.LogicalModelName
				raw = v.Settings
			}
		}
	}
	modelID := accesskit.ModelIDByLabel(models, f.ModelLabel)
	preview, err := acc.PreviewDoc(st.Access.App, modelID, raw)
	if err != nil {
		f.DocJSON = raw
		f.Msg = "预览失败：" + err.Error()
		return
	}
	f.DocJSON = preview
	if st.Access.App == "codex" {
		// 编辑器打开即生成只读 config.toml 预览（模型列表先由文档回填）。
		syncCodexFromDoc(st, models)
		refreshCodexPreview(st, acc)
	}
}

// accessRefreshCodexPreview 生成 Codex 只读 config.toml 预览（spec 2026-10-27
// §4.6）：默认模型取模型列表首项（空列表回退绑定模型），映射逻辑模型 ID 后
// 调 bridge 预览（含真实网关令牌、合并现有用户键，不写盘）。无可用模型或
// bridge 报错时清空预览并内联中文提示。
func refreshCodexPreview(st *state.State, acc bridge.AccessConfigs) {
	f := &st.Access.Form
	label := f.ModelLabel
	if len(f.CodexModels) > 0 {
		label = f.CodexModels[0]
	}
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	modelID := accesskit.ModelIDByLabel(models, label)
	if modelID == "" {
		f.CodexPreview = ""
		f.Msg = "请至少添加一个模型后再预览 config.toml"
		return
	}
	preview, err := acc.PreviewCodexConfig(modelID, f.DocJSON)
	if err != nil {
		f.CodexPreview = ""
		f.Msg = "预览失败：" + err.Error()
		return
	}
	f.CodexPreview = preview
	f.Msg = ""
}

// accessRepreviewForm 用当前表单模型重新合成预览（基线变化后调用）；失败内联报错。
func repreviewForm(st *state.State, acc bridge.AccessConfigs) {
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	modelID := accesskit.ModelIDByLabel(models, st.Access.Form.ModelLabel)
	preview, err := acc.PreviewDoc(st.Access.App, modelID, st.Access.Form.DocJSON)
	if err != nil {
		st.Access.Form.Msg = "预览失败：" + err.Error()
		return
	}
	st.Access.Form.DocJSON = preview
	st.Access.Form.Msg = ""
}

// accessConfigRow 渲染一行实际配置：名称 + 使用中徽标 + 逻辑模型名 +
// 操作按钮（应用/重新应用/编辑/复制启动命令/打开终端/删除）。网关不可用
// 时仅 应用/复制启动命令/打开终端 不渲染；编辑/删除是纯 DB 操作，始终在。
func configRow(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, v bridge.AccessConfigView, gateOK bool) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, v.Name).Width(140)
		if badge := accesskit.RowBadge(v.IsCurrent); badge != "" {
			uikit.SoftBadge(c, t, s, "ok", badge)
		}
		ui.Text(c, "→ "+v.LogicalModelName).TextColor(t.TextMuted).Grow(1)
		// 应用/复制命令/打开终端依赖网关（终审#3：编辑/删除是纯 DB 操作，
		// 不受门禁影响——spec 只点名门禁前三个操作）。
		apply := func() {
			if err := acc.Apply(v.ID); err != nil {
				st.Access.Msg = "应用失败：" + err.Error()
			} else if st.Access.App == "codex" {
				st.Access.Msg = "已应用「" + v.Name + "」——重启 Codex 后生效（桌面版需完全退出后重新打开）"
			} else {
				st.Access.Msg = "已应用「" + v.Name + "」——Agent 下次启动生效"
			}
		}
		if gateOK {
			// 「使用中」也保留按钮（终审 Critical#1）：改全局配置/模型改名后
			// 必须能重新应用，验收场景④才可达；徽标已表达状态，不另放死文本。
			label := "应用"
			if v.IsCurrent {
				label = "重新应用"
			}
			if uikit.SoftButton(c, t, s, "act", label).Clicked() {
				apply()
			}
		}
		if uikit.SoftButton(c, t, s, "gray", "编辑").Clicked() {
			st.Access.Form = state.AccessForm{Open: true, EditID: v.ID}
			preloadForm(st, acc)
		}
		if gateOK {
			if uikit.SoftButton(c, t, s, "gray", "复制启动命令").Clicked() {
				cmd, err := acc.LaunchCommand(v.ID)
				if err != nil {
					st.Access.Msg = "生成启动命令失败：" + err.Error()
				} else {
					mygo.Clipboard.Write(transfer.TextData(cmd))
					st.Access.Msg = "启动命令已复制"
				}
			}
			if uikit.SoftButton(c, t, s, "gray", "打开终端").Clicked() {
				if err := acc.OpenTerminal(v.ID); err != nil {
					st.Access.Msg = "打开终端失败：" + err.Error()
				} else {
					st.Access.Msg = ""
				}
			}
		}
		if uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
			if err := acc.Remove(v.ID); err != nil {
				st.Access.Msg = "删除失败：" + err.Error()
			} else {
				st.Access.Msg = "已删除「" + v.Name + "」——Agent 配置文件保留最后状态"
			}
		}
	})
}

// accessCodexModelsSection 渲染 Codex 模型列表编辑（spec 2026-10-27 §4.2）：
// 有序 Select 行 + 上移/下移/删除，首行「默认」徽标，底部「＋ 添加模型」。
// 行内按钮只记动作、循环结束后统一应用（构建中改切片会越界）；任何变化
// 写回文档 models（accessPatchCodexModels）。只剩一项时不渲染删除按钮
// ——列表至少保留一项（保存另有空列表校验）。返回列表是否发生变化（调用
// 方据此刷新只读 config.toml 预览）。
func codexModelsSection(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, labels []string) bool {
	f := &st.Access.Form
	ui.Text(c, "模型列表").Bold()
	ui.Text(c, "按序显示在 Codex 模型菜单；第一行为默认模型").TextColor(t.TextMuted)
	if len(labels) == 0 {
		ui.Text(c, "还没有启用的逻辑模型——先在「逻辑模型」页创建并启用").TextColor(t.TextMuted)
	}
	act, actI := "", -1
	changed := false
	for i := range f.CodexModels {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("%d.", i+1)).Width(20).TextColor(t.TextMuted)
			// 编辑旧配置时列表可能含停用模型：把当前值补进选项，避免
			// Select 显示空值、一次误点就丢掉引用。
			opts := labels
			if v := f.CodexModels[i]; v != "" && !slices.Contains(opts, v) {
				opts = append([]string{v}, labels...)
			}
			before := f.CodexModels[i]
			sel := ui.Select(c, &f.CodexModels[i], opts)
			if sel.Changed() && f.CodexModels[i] != before {
				changed = true
			}
			if i == 0 {
				uikit.SoftBadge(c, t, s, "ok", "默认")
			}
			if uikit.SoftButton(c, t, s, "gray", "上移").Clicked() {
				act, actI = "up", i
			}
			if uikit.SoftButton(c, t, s, "gray", "下移").Clicked() {
				act, actI = "down", i
			}
			if len(f.CodexModels) > 1 && uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
				act, actI = "del", i
			}
		})
	}
	switch act {
	case "up":
		accessMoveCodexModel(f.CodexModels, actI, -1)
	case "down":
		accessMoveCodexModel(f.CodexModels, actI, +1)
	case "del":
		f.CodexModels = append(f.CodexModels[:actI], f.CodexModels[actI+1:]...)
	}
	dirty := act != "" || changed
	if dirty {
		patchCodexModels(st)
	}
	if uikit.SoftButton(c, t, s, "act", "＋ 添加模型").Clicked() {
		if next, ok := accessCodexNextModel(labels, f.CodexModels); ok {
			f.CodexModels = append(f.CodexModels, next)
			patchCodexModels(st)
			dirty = true
		}
	}
	return dirty
}

// accessCodexModelMetaSection 渲染只读「将写入 Codex 的模型」预览
// （spec 2026-10-27 §6.2）：数据来自逻辑模型元数据，按模型列表顺序展示
// 名称/显示名/上下文窗口/思考等级；仅文本行，不提供编辑控件（编辑入口在
// 「逻辑模型」页）。
func codexModelMetaSection(c *ui.Context, t *ui.Theme, st *state.State, models []bridge.ModelView) {
	ui.Text(c, "将写入 Codex 的模型").Bold()
	ui.Text(c, "来自逻辑模型元数据；此处只读").TextColor(t.TextMuted)
	for _, line := range accessCodexModelMetaLines(st.Access.Form.CodexModels, models) {
		ui.Text(c, line)
	}
}

// accessCodexAdvancedSection 渲染 Codex 高级设置（spec 2026-10-27 §4.2）：
// 子代理模型、子代理推理强度、自动审批模型三个下拉。空值分别显示
// 「跟随主模型」/「默认」/「主模型自己审批（默认）」；任何变化写回文档
// codex 对象（accessPatchCodexSettings），下次回填保持一致。返回是否有变化
// （调用方据此刷新只读 config.toml 预览）。
func codexAdvancedSection(c *ui.Context, t *ui.Theme, st *state.State) bool {
	f := &st.Access.Form
	ui.Text(c, "高级设置").Bold()
	ui.Text(c, "子代理与自动审批为可选配置，默认跟随主模型").TextColor(t.TextMuted)
	changed := false
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "子代理模型:").Width(120).TextColor(t.TextMuted)
		label := accessCodexSelectLabel(f.SubagentModel, accessCodexSubagentFollow)
		before := label
		sel := ui.Select(c, &label,
			accessCodexModelOptions(accessCodexSubagentFollow, f.CodexModels, f.SubagentModel))
		if sel.Changed() && label != before {
			f.SubagentModel = accessCodexSelectRaw(label, accessCodexSubagentFollow)
			changed = true
		}
	})
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "子代理推理强度:").Width(120).TextColor(t.TextMuted)
		label := accessCodexSelectLabel(f.SubagentEffort, accessCodexEffortDefault)
		before := label
		sel := ui.Select(c, &label, accessCodexEffortOptions(f.SubagentEffort))
		if sel.Changed() && label != before {
			f.SubagentEffort = accessCodexSelectRaw(label, accessCodexEffortDefault)
			changed = true
		}
	})
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "自动审批模型:").Width(120).TextColor(t.TextMuted)
		label := accessCodexSelectLabel(f.AutoReviewModel, accessCodexAutoReviewDefault)
		before := label
		sel := ui.Select(c, &label,
			accessCodexModelOptions(accessCodexAutoReviewDefault, f.CodexModels, f.AutoReviewModel))
		if sel.Changed() && label != before {
			f.AutoReviewModel = accessCodexSelectRaw(label, accessCodexAutoReviewDefault)
			changed = true
		}
	})
	if changed {
		patchCodexSettings(st)
	}
	return changed
}

// codexEditorCard 渲染 Codex 编辑器：模型列表 + 只读元数据 + 高级设置作为
// 模型段落注入通用 EditorCard，配置 JSON 之后追加只读 config.toml 预览。
// （T8 暂留宿主包，T9 迁入 codex 子包。）
func codexEditorCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, gateOK bool) {
	accesskit.EditorCard(c, t, s, st, acc, gateOK, accesskit.EditorHooks{
		ModelSection: func(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, labels []string, models []bridge.ModelView) {
			// 文档 → 模型列表回填（打开配置/编辑 JSON 后同步）；首项即
			// 默认模型（保存时的 logical_model_id）。
			syncCodexFromDoc(st, models)
			changed := codexModelsSection(c, t, s, st, labels)
			codexModelMetaSection(c, t, st, models)
			changed = codexAdvancedSection(c, t, st) || changed
			// 模型列表/高级设置变化后刷新只读 config.toml 预览；文本域手动
			// 编辑不逐键刷新（模板加载会跑 codex debug models，每帧调用卡顿），
			// 改用「刷新预览」按钮。
			if changed {
				refreshCodexPreview(st, acc)
			}
		},
		AfterDoc: func(c *ui.Context, t *ui.Theme, s uikit.SoftPalette) {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "将写入的 config.toml（只读）").Bold()
				ui.Box(c).Grow(1)
				if uikit.SoftButton(c, t, s, "gray", "刷新预览").Clicked() {
					refreshCodexPreview(st, acc)
				}
			})
			ui.TextArea(c, &st.Access.Form.CodexPreview).Height(240).ReadOnly(true)
		},
	})
}
