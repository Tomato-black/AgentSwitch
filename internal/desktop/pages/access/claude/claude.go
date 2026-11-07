// Package claude 是接入页 Claude Code 页签的接入抽屉正文：名称、默认逻辑
// 模型下拉 + 1M 声明、5 角色模型映射（Sonnet/Opus/Fable/Haiku/Subagent，
// 行状态由配置 JSON 双向同步）与只读 settings.json 预览（accesskit 角色
// 助手）。2026-11-07 原型对齐：编辑器从独立子页改为右侧抽屉，且不再提供
// 可编辑的配置 JSON 文本域——env 定制交给同页「Claude Code 设置」区。
package claude

import (
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// newDraftOption 是抽屉配置下拉的「新建草稿」伪选项（EditID 为空的草稿）。
const newDraftOption = "（新建草稿）"

// DrawerHooks 让宿主接入页注入抽屉的配置切换/新建回调（宿主负责重置表单
// 并预载文档；claude 子包不反向依赖宿主）。
type DrawerHooks struct {
	SwitchTo func(id string) // 切换到既有配置（EditID=id）
	NewDraft func()          // 切换到新建草稿（EditID 置空）
}

// drawerSelect 处理配置下拉的选择变化：目标与当前一致时无操作；dirty 草稿
// 先武装确认（DrawerSwitchID 空=新建草稿），干净则直接切换。
func drawerSelect(st *state.State, acc bridge.AccessConfigs, hooks DrawerHooks, name string) {
	target := "" // 「（新建草稿）」
	if name != newDraftOption {
		for _, v := range acc.List("claude") {
			if v.Name == name {
				target = v.ID
				break
			}
		}
		if target == "" {
			return // 未知选项：忽略
		}
	}
	if target == st.Access.Form.EditID {
		return
	}
	if st.Access.Form.Dirty("claude") {
		st.Access.DrawerSwitchArm = true
		st.Access.DrawerSwitchID = target
		return
	}
	applyDrawerSwitch(st, hooks, target)
}

// ConfirmDrawerSwitch 是 dirty 切换确认后的执行入口（宿主在抽屉开/关两态
// 都可调用）：执行 DrawerSwitchID 挂起的切换并清武装。
func ConfirmDrawerSwitch(st *state.State, hooks DrawerHooks) {
	target := st.Access.DrawerSwitchID
	st.Access.DrawerSwitchArm = false
	st.Access.DrawerSwitchID = ""
	applyDrawerSwitch(st, hooks, target)
}

func applyDrawerSwitch(st *state.State, hooks DrawerHooks, target string) {
	if target == "" {
		if hooks.NewDraft != nil {
			hooks.NewDraft()
		}
		return
	}
	if hooks.SwitchTo != nil {
		hooks.SwitchTo(target)
	}
}

// DrawerCard 渲染 Claude 接入抽屉正文（原型 2026-11-07 §6.2.2）：配置选择 +
// 新建、名称、默认模型/1M、角色映射、受管键只读预览、变更对比与底部
// 关闭/仅保存/保存并应用。角色行编辑直接写回内存文档（DocJSON），保存时
// 经 bridge 归一化；文档无手编入口后恒为合法 JSON，保存路径仍会再校验。
func DrawerCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, gateOK bool, hooks DrawerHooks) {
	f := &st.Access.Form
	ui.Column(c).Gap(10).Children(func() {
		// 配置行：下拉（既有配置 + 新建草稿伪选项）+「新建」。
		list := acc.List("claude")
		names := make([]string, 0, len(list))
		cur := newDraftOption
		for _, v := range list {
			names = append(names, v.Name)
			if v.ID == f.EditID {
				cur = v.Name
			}
		}
		uikit.FormRowRaw(c, t, "接入配置:", func() {
			options := append([]string{newDraftOption}, names...)
			sel := ui.Select(c, &cur, options).Grow(1)
			if sel.Changed() {
				drawerSelect(st, acc, hooks, cur)
			}
			if uikit.SoftButton(c, t, s, "gray", "新建").Clicked() {
				if st.Access.Form.Dirty("claude") {
					st.Access.DrawerSwitchArm = true
					st.Access.DrawerSwitchID = ""
				} else if hooks.NewDraft != nil {
					hooks.NewDraft()
				}
			}
		})
		// 次级操作行（编辑既有配置时）：启动命令/终端/备份恢复/删除。
		if f.EditID != "" {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				if gateOK {
					if uikit.SoftButton(c, t, s, "gray", "复制启动命令").Clicked() {
						if cmd, err := acc.LaunchCommand(f.EditID); err != nil {
							f.Msg = "生成启动命令失败：" + err.Error()
						} else {
							mygo.Clipboard.Write(transfer.TextData(cmd))
							f.Msg = "启动命令已复制"
						}
					}
					if uikit.SoftButton(c, t, s, "gray", "打开终端").Clicked() {
						if err := acc.OpenTerminal(f.EditID); err != nil {
							f.Msg = "打开终端失败：" + err.Error()
						} else {
							f.Msg = ""
						}
					}
				}
				if uikit.SoftButton(c, t, s, "gray", "备份与恢复").Clicked() {
					st.Access.BackupOpen = true
					st.Access.BackupMsg = ""
					st.Access.Backups, _ = acc.ListBackups("claude")
				}
				if uikit.SoftButton(c, t, s, "gray", "还原最近备份").Clicked() {
					st.Access.RestoreArm = true
				}
				if uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
					st.Access.DeleteArm = true
					st.Access.DeleteID = f.EditID
				}
			})
		}

		uikit.FormRow(c, t, "名称:", func() ui.Element {
			return ui.TextInput(c, &f.Name).Grow(1)
		})

		var models []bridge.ModelView
		if st.Svc.Logical != nil {
			models = st.Svc.Logical.List()
		}
		labels := accesskit.ModelLabels(models)
		if len(labels) == 0 {
			ui.Text(c, "还没有启用的逻辑模型——先在「逻辑模型」页创建并启用").TextColor(t.TextMuted)
		} else {
			modelSection(c, t, st, acc, labels)
			rolesSection(c, t, st, labels)
		}

		gs := st.Svc.Gateway.Status()
		ui.Text(c, fmt.Sprintf("网关地址 http://127.0.0.1:%d · 令牌自动生成并填入预览（保存时自动剔除，不落库）", gs.Port)).TextColor(t.TextMuted)

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "将写入 ~/.claude/settings.json（只读）").Bold()
			ui.Box(c).Grow(1)
			if uikit.SoftButton(c, t, s, "gray", "刷新预览").Clicked() {
				refreshPreview(st, acc, true)
			}
		})
		ui.TextArea(c, &f.ClaudePreview).Height(200).ReadOnly(true).Font(uikit.MonoFont)

		if f.EditID != "" {
			if lines := accesskit.DocDiff(f.OriginalDoc, f.DocJSON); len(lines) > 0 {
				ui.Text(c, "变更对比（保存后，敏感值已遮蔽）").Bold()
				for i, line := range lines {
					if i >= 40 {
						ui.Textf(c, "…另有 %d 项变更", len(lines)-40).TextColor(t.TextMuted)
						break
					}
					text := line.Op + " " + line.Path
					switch line.Op {
					case "+":
						text += " = " + line.New
					case "-":
						text += " = " + line.Old
					default:
						text += " = " + line.Old + " → " + line.New
					}
					ui.Text(c, text).Font(uikit.MonoFont).TextColor(t.TextMuted).SingleLine()
				}
			}
		}

		uikit.Feedback(c, t, uikit.InferFeedback(f.Msg), f.Msg)

		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if !gateOK {
				ui.Text(c, "网关未运行——先启动网关才能「保存并应用」").TextColor(t.TextMuted)
			}
			ui.Box(c).Grow(1)
			if uikit.SoftButton(c, t, s, "gray", "关闭").Clicked() {
				CloseDrawer(st)
			}
			// 仅保存恒可用（只写数据库，不依赖网关）；保存并应用在网关停止时
			// 禁用（需求 2026-11-07 §6.2.2）。
			if uikit.SoftButton(c, t, s, "gray", "仅保存").Clicked() {
				accesskit.SaveFormWithApply(st, acc, false)
			}
			applyBtn := uikit.PillButton(c, t, "保存并应用")
			if !gateOK {
				applyBtn.Disabled(true)
			}
			if applyBtn.Clicked() && gateOK {
				accesskit.SaveFormWithApply(st, acc, true)
			}
		})

	})
}

// CloseDrawer 关闭抽屉并保留草稿：仍有未保存修改时置 Draft（摘要卡显示
// 「有未保存的接入更改」，切页签走脏确认）；干净草稿不标记。
func CloseDrawer(st *state.State) {
	f := &st.Access.Form
	f.Draft = f.Signature("claude") != f.Baseline
	f.Open = false
}

// modelSection 是 Claude 专属的主模型段落：切换主模型时先按旧模型的
// SMALL_FAST 派生值重定位（否则旧值会覆盖新基线并在保存时冻结），1M 勾选
// 写回 ANTHROPIC_MODEL 的 [1M] 后缀。
func modelSection(c *ui.Context, t *ui.Theme, st *state.State, acc bridge.AccessConfigs, labels []string) {
	f := &st.Access.Form
	accesskit.DefaultModelSection(c, t, st, acc, labels, accesskit.ModelHooks{
		OnChanged: func(prev string) {
			if rebased, err := bridge.RebaseAccessDoc(st.Access.App, f.DocJSON,
				map[string]string{"ANTHROPIC_SMALL_FAST_MODEL": prev}); err == nil {
				f.DocJSON = rebased
			}
			if f.MainOneM {
				accesskit.PatchMainModel(f) // 1M 声明跟随新模型名
			}
		},
		Extra: func() {
			beforeM := f.MainOneM
			cb := ui.Checkbox(c, &f.MainOneM, "1M")
			if cb.Changed() && f.MainOneM != beforeM {
				accesskit.PatchMainModel(f)
				accesskit.RepreviewForm(st, acc)
			}
		},
	})
}

// rolesSection 是「模型映射」段落：显示名称只影响 /model 菜单；每行
// 模型下拉（含「跟随主模型」）+ 显示名输入 + 1M 勾选，变化即写回文档。
func rolesSection(c *ui.Context, t *ui.Theme, st *state.State, labels []string) {
	f := &st.Access.Form
	ui.Text(c, "模型映射").Bold()
	ui.Text(c, "显示名称只影响 /model 菜单；1M 只是给 Claude Code 的上下文能力声明").TextColor(t.TextMuted)
	accesskit.SyncRolesFromDoc(f) // 打开/回填后对齐（解析失败静默保留）
	for i, r := range accesskit.ClaudeRoles() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, r.Label).Width(80).TextColor(t.TextMuted)
			if r.NameKey != "" {
				before := f.RoleNames[i]
				nameIn := ui.TextInput(c, &f.RoleNames[i]).Width(180).Placeholder("显示名称")
				if nameIn.Changed() && f.RoleNames[i] != before {
					accesskit.PatchRole(f, i)
				}
			}
			opts := append([]string{accesskit.FollowModel}, labels...)
			before := f.RoleModels[i]
			sel := ui.Select(c, &f.RoleModels[i], opts)
			if sel.Changed() && f.RoleModels[i] != before {
				accesskit.PatchRole(f, i)
			}
			beforeB := f.RoleOneM[i]
			cb := ui.Checkbox(c, &f.RoleOneM[i], "1M")
			if cb.Changed() && f.RoleOneM[i] != beforeB {
				accesskit.PatchRole(f, i)
			}
		})
	}
}

// Prepare 在编辑器打开后生成只读 settings.json 预览（宿主 preloadForm 调用，
// 失败静默——内容尚不完整时不该弹提示）。
func Prepare(st *state.State, acc bridge.AccessConfigs) { refreshPreview(st, acc, false) }

// refreshPreview 重新生成 settings.json 预览（live 合并结果）；report 为真时
// 将错误写进表单信息位。
func refreshPreview(st *state.State, acc bridge.AccessConfigs, report bool) {
	f := &st.Access.Form
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	modelID := accesskit.ModelIDByLabel(models, f.ModelLabel)
	if modelID == "" {
		f.ClaudePreview = ""
		if report {
			f.Msg = "请选择逻辑模型后再预览"
		}
		return
	}
	p, err := acc.PreviewClaudeLive(modelID, f.DocJSON)
	if err != nil {
		f.ClaudePreview = ""
		if report {
			f.Msg = "预览失败：" + err.Error()
		}
		return
	}
	f.ClaudePreview = p
	if report {
		f.Msg = ""
	}
}
