// access.go 是接入页（spec 2026-10-18-agent-access-config-design）：每 Agent
// （Claude Code / Codex CLI）多实际配置 + 全局配置（合并进所有实际配置），
// 「应用」即受管键投影写入 agent 真实配置文件；未应用的配置经持久文件 +
// 启动命令 / 「打开终端」在启动 Agent 时手动指定。取代旧「片段复制」页
// （旧 bridge.Access 已删除）。编辑器默认渲染真实网关令牌（2026-10-21 用户
// 裁定）；保存时锁定键仍由 bridge 剔除、令牌永不落库。审计与错误经 bridge
// 方法返回的中文文案内联显示。
package desktop

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// accessForm 是接入配置新建/编辑表单状态（open 非空即编辑）。docJSON 是唯一
// 共享存储（spec 2026-10-19 §5.2）：编辑器显示 PreviewDoc 合成结果（含真实
// 网关令牌），保存原文送 bridge 归一化。
type accessForm struct {
	open       bool
	editID     string // 非空 = 编辑该配置
	name       string
	modelLabel string // 选中的逻辑模型名（保存时映射回 ID）
	docJSON    string
	msg        string
	// 角色映射行状态（accessClaudeRoles 同序；由 docJSON 双向同步）
	roleModels []string
	roleNames  []string
	roleOneM   []bool
	// mainOneM 是主模型 1M 勾选（ANTHROPIC_MODEL 半锁定后缀，由 docJSON 回填）
	mainOneM bool
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

// accessOverridesFromRows 把键值行转成 overrides map（空键/空值都剔除
// ——半填行是误操作，清空请删行；同键后者胜——与合成层 overrides 优先
// 语义一致）。用于全局配置卡。
func accessOverridesFromRows(rows [][2]string) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		k := strings.TrimSpace(r[0])
		v := strings.TrimSpace(r[1])
		if k == "" || v == "" {
			continue
		}
		out[k] = r[1]
	}
	return out
}

// accessDocSetEnv 解析 raw 文档并对 env 键打补丁（value=="" 删除该键），2 空格
// 缩进序列化。解析失败返回 error（调用方保留旧文本）。仅 UI 本地补丁用，不做
// appType 校验（保存校验在 bridge）。
func accessDocSetEnv(raw, key, value string) (string, error) {
	doc := map[string]any{}
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
			return "", err
		}
		if doc == nil {
			return "", fmt.Errorf("配置必须是 JSON 对象")
		}
	}
	env := map[string]any{}
	if v, ok := doc["env"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return "", fmt.Errorf("env 必须是 JSON 对象")
		}
		env = m
	}
	if value == "" {
		delete(env, key)
	} else {
		env[key] = value
	}
	doc["env"] = env
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// accessDocEnvStrings 提取文档 env 字符串 map（角色行回填数据源）；env 值非
// 字符串/JSON 非法返回 error——调用方保留上一合法状态。
func accessDocEnvStrings(raw string) (map[string]string, error) {
	doc := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return nil, err
		}
		if doc == nil {
			return nil, fmt.Errorf("配置必须是 JSON 对象")
		}
	}
	out := map[string]string{}
	if v, ok := doc["env"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("env 必须是 JSON 对象")
		}
		for k, val := range m {
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("env 值必须是字符串: %s", k)
			}
			out[k] = s
		}
	}
	return out, nil
}

// accessParseOneM 剥离值尾部 "[1M]"（大小写不敏感）——1M 勾选状态回显。
func accessParseOneM(v string) (string, bool) {
	if len(v) >= 4 && strings.EqualFold(v[len(v)-4:], "[1M]") {
		return v[:len(v)-4], true
	}
	return v, false
}

// accessRole 是 Claude 模型映射一行（spec 2026-10-19 §3.2）：角色 → 文档 env
// 键。nameKey 为空的角色无「显示名称」列（Subagent 不进 /model 菜单）。
type accessRole struct{ label, envKey, nameKey string }

// accessClaudeRoles 是 5 角色固定顺序（与 cc-switch 对齐）。
var accessClaudeRoles = []accessRole{
	{"Sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"},
	{"Opus", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL_NAME"},
	{"Fable", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL_NAME"},
	{"Haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME"},
	{"Subagent", "CLAUDE_CODE_SUBAGENT_MODEL", ""},
}

// accessRoleFollow 是角色下拉「未设置」的显示值（回落主模型）。
const accessRoleFollow = "（跟随主模型）"

// accessRoleIndexByEnvKey 返回角色下标（测试与补丁用）。
func accessRoleIndexByEnvKey(envKey string) int {
	for i, r := range accessClaudeRoles {
		if r.envKey == envKey {
			return i
		}
	}
	return -1
}

// accessSyncRolesFromDoc JSON→表单回填：解析成功才覆盖角色行（失败保留旧状态，
// cc-switch 同款「编辑到一半不闪烁」）。
func (u *uiState) accessSyncRolesFromDoc() {
	env, err := accessDocEnvStrings(u.accessForm.docJSON)
	if err != nil {
		return
	}
	f := &u.accessForm
	_, f.mainOneM = accessParseOneM(env["ANTHROPIC_MODEL"])
	for len(f.roleModels) < len(accessClaudeRoles) {
		f.roleModels = append(f.roleModels, accessRoleFollow)
		f.roleNames = append(f.roleNames, "")
		f.roleOneM = append(f.roleOneM, false)
	}
	for i, r := range accessClaudeRoles {
		name, oneM := accessParseOneM(env[r.envKey])
		if name == "" {
			f.roleModels[i] = accessRoleFollow
		} else {
			f.roleModels[i] = name
		}
		f.roleOneM[i] = oneM
		if r.nameKey != "" {
			f.roleNames[i] = env[r.nameKey]
		}
	}
}

// accessPatchRole 表单→JSON 补丁：角色行变化写回文档 env（空/跟随=删键；
// 1M=值尾拼 "[1M]" 字面量）。
func (u *uiState) accessPatchRole(i int) {
	r := accessClaudeRoles[i]
	f := &u.accessForm
	val := f.roleModels[i]
	if val == accessRoleFollow {
		val = ""
	}
	if val != "" && f.roleOneM[i] {
		val += "[1M]"
	}
	if raw, err := accessDocSetEnv(f.docJSON, r.envKey, val); err == nil {
		f.docJSON = raw
	}
	if r.nameKey != "" {
		if raw, err := accessDocSetEnv(f.docJSON, r.nameKey, strings.TrimSpace(f.roleNames[i])); err == nil {
			f.docJSON = raw
		}
	}
}

// accessPatchMainModel 主模型 1M 勾选 → 文档 env 补丁（ANTHROPIC_MODEL =
// 逻辑模型名[1M]；未勾选写纯名，保存时被归一化剔除、保持继承）。
func (u *uiState) accessPatchMainModel() {
	val := u.accessForm.modelLabel
	if val == "" {
		return
	}
	if u.accessForm.mainOneM {
		val += "[1M]"
	}
	if raw, err := accessDocSetEnv(u.accessForm.docJSON, "ANTHROPIC_MODEL", val); err == nil {
		u.accessForm.docJSON = raw
	}
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
	sort.Strings(keys)
	rows := make([][2]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, [2]string{k, env[k]})
	}
	return rows
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

	// 内容列包 Scroll（同 channels/logicalmodels 的长页面）：编辑器（角色表 +
	// 320 高 TextArea）会让页面超出视口，没有滚动容器时底部内容不可达。
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			// Agent tab 行（切 tab 重置编辑态，避免表单跨 Agent 误存）
			ui.Row(c).Gap(8).Children(func() {
				if softButton(c, t, s, tabKind(u.accessApp == "claude"), claudeTitle).Clicked() {
					u.accessApp = "claude"
					u.accessMsg = ""
					u.accessForm = accessForm{}
					u.accessReloadGlobal(acc)
				}
				if softButton(c, t, s, tabKind(u.accessApp == "codex"), codexTitle).Clicked() {
					u.accessApp = "codex"
					u.accessMsg = ""
					u.accessForm = accessForm{}
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
					// 行内删除只记下标，循环结束后统一应用——构建中改切片会
					// 越界（logicalmodels.go 同款注释与模式）。
					globDel := -1
					for i := range u.accessGlob {
						row := &u.accessGlob[i]
						ui.Row(c).Gap(8).Children(func() {
							ui.TextInput(c, &row[0]).Width(220).Placeholder("键")
							ui.TextInput(c, &row[1]).Grow(1).Placeholder("值")
							if softButton(c, t, s, "danger", "删除").Clicked() {
								globDel = i
							}
						})
					}
					if globDel >= 0 {
						u.accessGlob = append(u.accessGlob[:globDel], u.accessGlob[globDel+1:]...)
					}
					ui.Row(c).Gap(8).Children(func() {
						if softButton(c, t, s, "gray", "＋ 添加一项").Clicked() {
							u.accessGlob = append(u.accessGlob, [2]string{})
						}
						if softButton(c, t, s, "act", "保存全局配置").Clicked() {
							// 行绑定随输入即时更新，此处 u.accessGlob 已是待保存
							// 的新值；重定位旧派生值需要保存前的持久基线。
							oldGlobal, _ := acc.GlobalConfig(u.accessApp)
							if err := acc.SaveGlobalConfig(u.accessApp, accessOverridesFromRows(u.accessGlob)); err != nil {
								u.accessGlobMsg = err.Error()
							} else {
								u.accessGlobMsg = "已保存"
								// 编辑器打开时全局基线变了：删除旧基线派生值并
								// 按新基线与当前表单模型重预览（Important#1）。
								if u.accessForm.open {
									if rebased, err := bridge.RebaseAccessDoc(u.accessApp, u.accessForm.docJSON, oldGlobal); err == nil {
										u.accessForm.docJSON = rebased
									}
									u.accessRepreviewForm(acc)
								}
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
				u.accessEditorCard(c, t, s, acc, gateOK)
			}
		})
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

// accessPreloadForm 打开编辑器：预填名称/模型，加载 PreviewDoc 合成预览
// （新建以 "{}" 为底；预览失败回退原文并内联报错）。
func (u *uiState) accessPreloadForm(acc bridge.AccessConfigs) {
	f := &u.accessForm
	var models []bridge.ModelView
	if u.svc.Logical != nil {
		models = u.svc.Logical.List()
	}
	if f.modelLabel == "" {
		if labels := accessModelLabels(models); len(labels) > 0 {
			f.modelLabel = labels[0]
		}
	}
	raw := "{}"
	if f.editID != "" {
		for _, v := range acc.List(u.accessApp) {
			if v.ID == f.editID {
				f.name = v.Name
				f.modelLabel = v.LogicalModelName
				raw = v.Settings
			}
		}
	}
	modelID := accessModelIDByLabel(models, f.modelLabel)
	preview, err := acc.PreviewDoc(u.accessApp, modelID, raw)
	if err != nil {
		f.docJSON = raw
		f.msg = "预览失败：" + err.Error()
		return
	}
	f.docJSON = preview
}

// accessRepreviewForm 用当前表单模型重新合成预览（基线变化后调用）；失败内联报错。
func (u *uiState) accessRepreviewForm(acc bridge.AccessConfigs) {
	var models []bridge.ModelView
	if u.svc.Logical != nil {
		models = u.svc.Logical.List()
	}
	modelID := accessModelIDByLabel(models, u.accessForm.modelLabel)
	preview, err := acc.PreviewDoc(u.accessApp, modelID, u.accessForm.docJSON)
	if err != nil {
		u.accessForm.msg = "预览失败：" + err.Error()
		return
	}
	u.accessForm.docJSON = preview
	u.accessForm.msg = ""
}

// accessConfigRow 渲染一行实际配置：名称 + 使用中徽标 + 逻辑模型名 +
// 操作按钮（应用/重新应用/编辑/复制启动命令/打开终端/删除）。网关不可用
// 时仅 应用/复制启动命令/打开终端 不渲染；编辑/删除是纯 DB 操作，始终在。
func (u *uiState) accessConfigRow(c *ui.Context, t *ui.Theme, s softPalette, acc bridge.AccessConfigs, v bridge.AccessConfigView, gateOK bool) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, v.Name).Width(140)
		if badge := accessRowBadge(v.IsCurrent); badge != "" {
			softBadge(c, t, s, "ok", badge)
		}
		ui.Text(c, "→ "+v.LogicalModelName).TextColor(t.TextMuted).Grow(1)
		// 应用/复制命令/打开终端依赖网关（终审#3：编辑/删除是纯 DB 操作，
		// 不受门禁影响——spec 只点名门禁前三个操作）。
		apply := func() {
			if err := acc.Apply(v.ID); err != nil {
				u.accessMsg = "应用失败：" + err.Error()
			} else {
				u.accessMsg = "已应用「" + v.Name + "」——Agent 下次启动生效"
			}
		}
		if gateOK {
			// 「使用中」也保留按钮（终审 Critical#1）：改全局配置/模型改名后
			// 必须能重新应用，验收场景④才可达；徽标已表达状态，不另放死文本。
			label := "应用"
			if v.IsCurrent {
				label = "重新应用"
			}
			if softButton(c, t, s, "act", label).Clicked() {
				apply()
			}
		}
		if softButton(c, t, s, "gray", "编辑").Clicked() {
			u.accessForm = accessForm{open: true, editID: v.ID}
			u.accessPreloadForm(acc)
		}
		if gateOK {
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
		}
		if softButton(c, t, s, "danger", "删除").Clicked() {
			if err := acc.Remove(v.ID); err != nil {
				u.accessMsg = "删除失败：" + err.Error()
			} else {
				u.accessMsg = "已删除「" + v.Name + "」——Agent 配置文件保留最后状态"
			}
		}
	})
}

// accessEditorCard 渲染新建/编辑表单（spec 2026-10-19 §5.1）：名称、逻辑模型
// 下拉（主模型，锁定键）、网关只读行、配置 JSON（TextArea，令牌渲染真值）。
// 保存原文送 bridge（校验+归一化在 bridge）。
func (u *uiState) accessEditorCard(c *ui.Context, t *ui.Theme, s softPalette, acc bridge.AccessConfigs, gateOK bool) {
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
					prev := u.accessForm.modelLabel
					sel := ui.Select(c, &u.accessForm.modelLabel, labels)
					if sel.Changed() && u.accessForm.modelLabel != prev {
						// 主模型切换：先按旧模型的 SMALL_FAST 派生值重定位
						//（否则旧值会覆盖新基线并在保存时被冻结），再重新
						// 取合成预览（锁定键随之刷新）。
						if u.accessApp == "claude" {
							if rebased, err := bridge.RebaseAccessDoc(u.accessApp, u.accessForm.docJSON,
								map[string]string{"ANTHROPIC_SMALL_FAST_MODEL": prev}); err == nil {
								u.accessForm.docJSON = rebased
							}
							if u.accessForm.mainOneM {
								u.accessPatchMainModel() // 1M 声明跟随新模型名
							}
						}
						u.accessRepreviewForm(acc)
					}
					if u.accessApp == "claude" {
						beforeM := u.accessForm.mainOneM
						cb := ui.Checkbox(c, &u.accessForm.mainOneM, "1M")
						if cb.Changed() && u.accessForm.mainOneM != beforeM {
							u.accessPatchMainModel()
							u.accessRepreviewForm(acc)
						}
					}
				}
			})

			if u.accessApp == "claude" {
				ui.Text(c, "模型映射").Bold()
				ui.Text(c, "显示名称只影响 /model 菜单；1M 只是给 Claude Code 的上下文能力声明").TextColor(t.TextMuted)
				u.accessSyncRolesFromDoc() // 打开/回填后对齐（解析失败静默保留）
				for i, r := range accessClaudeRoles {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, r.label).Width(80).TextColor(t.TextMuted)
						if r.nameKey != "" {
							before := u.accessForm.roleNames[i]
							nameIn := ui.TextInput(c, &u.accessForm.roleNames[i]).Width(180).Placeholder("显示名称")
							if nameIn.Changed() && u.accessForm.roleNames[i] != before {
								u.accessPatchRole(i)
							}
						}
						opts := append([]string{accessRoleFollow}, labels...)
						before := u.accessForm.roleModels[i]
						sel := ui.Select(c, &u.accessForm.roleModels[i], opts)
						if sel.Changed() && u.accessForm.roleModels[i] != before {
							u.accessPatchRole(i)
						}
						beforeB := u.accessForm.roleOneM[i]
						cb := ui.Checkbox(c, &u.accessForm.roleOneM[i], "1M")
						if cb.Changed() && u.accessForm.roleOneM[i] != beforeB {
							u.accessPatchRole(i)
						}
					})
				}
			}

			st := u.svc.Gateway.Status()
			ui.Text(c, fmt.Sprintf("网关地址 http://127.0.0.1:%d · 令牌自动生成并填入下方 JSON（保存时自动剔除，不落库）", st.Port)).TextColor(t.TextMuted)

			ui.Text(c, "配置 JSON（最终写入 Agent 的完整文档；网关地址/令牌自动锁定，模型名仅可追加 1M 声明）").TextColor(t.TextMuted)
			ta := ui.TextArea(c, &u.accessForm.docJSON).Height(320).Grow(1)
			if ta.Changed() {
				u.accessSyncRolesFromDoc()
			}
			ui.Text(c, "与全局/默认值相同的键保存时自动视为未定制（保持继承）；要覆盖请改成不同值").TextColor(t.TextMuted)

			if u.accessForm.msg != "" {
				ui.Text(c, u.accessForm.msg).TextColor(t.Danger)
			}

			ui.Row(c).Gap(8).Children(func() {
				if !gateOK {
					ui.Text(c, "网关未运行——保存后需启动网关才能「应用」").TextColor(t.TextMuted)
				}
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

// accessSaveForm 校验并保存表单；文档原文送 bridge（parse+归一化错误内联）。
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
	settingsJSON := strings.TrimSpace(f.docJSON)
	if settingsJSON == "" {
		settingsJSON = "{}"
	}
	if f.editID != "" {
		if err := acc.Update(f.editID, f.name, modelID, settingsJSON); err != nil {
			f.msg = err.Error()
			return
		}
	} else {
		if _, err := acc.Create(u.accessApp, f.name, modelID, settingsJSON); err != nil {
			f.msg = err.Error()
			return
		}
	}
	u.accessMsg = "已保存「" + f.name + "」"
	u.accessForm = accessForm{}
}
