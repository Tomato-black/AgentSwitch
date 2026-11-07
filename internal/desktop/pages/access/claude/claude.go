// Package claude 是接入页 Claude Code 页签的编辑子页正文：名称、默认逻辑
// 模型下拉 + 1M 声明、5 角色模型映射（Sonnet/Opus/Fable/Haiku/Subagent，
// 行状态由配置 JSON 双向同步）、Claude 模型选择与只读 settings.json 预览
// （accesskit 角色助手）。2026-11-07 用户裁定：抽屉方案废弃、回归独立子页
// （设置已拆页）；不提供可编辑的配置 JSON 文本域，env 定制交给设置页。
package claude

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// EditorCard 渲染 Claude 接入编辑子页正文（2026-11-07 用户裁定：抽屉废弃，
// 回归独立子页；次级操作在列表行的「更多」菜单）：名称、默认模型/1M、
// 角色映射、Claude 模型选择、受管键只读预览、变更对比与底部
// 取消/仅保存/保存并应用。角色行编辑直接写回内存文档（DocJSON），保存时
// 经 bridge 归一化；文档无手编入口后恒为合法 JSON，保存路径仍会再校验。
func EditorCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, gateOK bool) {
	f := &st.Access.Form
	ui.Column(c).Gap(10).Children(func() {
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
			modelKeysSection(c, t, s, st, labels)
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
			if uikit.SoftButton(c, t, s, "gray", "取消").Clicked() {
				accesskit.CloseEditor(st)
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

// modelKeysSection 是「Claude 模型选择」段（2026-11-07 用户裁定归接入）：顶层
// model（空＝跟随网关）与 fallbackModel 有序回退链，写入接入文档顶层键，经
// 既有投影链路进 settings.json（设置页自动只读）。
func modelKeysSection(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, labels []string) {
	f := &st.Access.Form
	accesskit.SyncModelKeysFromDoc(f) // 打开/回填后对齐（解析失败静默保留）
	ui.Text(c, "Claude 模型选择").Bold()
	ui.Text(c, "留空＝跟随网关（ANTHROPIC_MODEL）；值需为网关可路由的逻辑模型名").TextColor(t.TextMuted)

	// 主模型是下拉（2026-11-07 用户指令）：选项＝（跟随网关）哨兵 + 逻辑模型；
	// 哨兵＝删 model 键（跟随 ANTHROPIC_MODEL）。
	followGateway := "（跟随网关）"
	uikit.FormRow(c, t, "主模型:", func() ui.Element {
		cur := f.ModelKey
		if cur == "" {
			cur = followGateway
		}
		sel := ui.Select(c, &cur, append([]string{followGateway}, labels...)).Grow(1)
		if sel.Changed() {
			v := cur
			if v == followGateway {
				v = ""
			}
			f.ModelKey = v
			accesskit.PatchModelKey(f, v)
		}
		return sel
	})
	uikit.FormRowRaw(c, t, "回退模型:", func() {
		ui.Column(c).Gap(6).Children(func() {
			if len(f.FallbackModels) == 0 {
				ui.Text(c, "未设置（主模型失败即报错）").TextColor(t.TextMuted)
			} else {
				ui.Row(c).Gap(6).Wrap().Children(func() {
					for i, m := range f.FallbackModels {
						i, m := i, m
						ui.Box(c).Border(1, t.Border).Radius(8).Padding(3, 7).Shrink(0).Children(func() {
							ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
								if i > 0 {
									if uikit.SoftButton(c, t, s, "gray", "↑").Clicked() {
										f.FallbackModels[i-1], f.FallbackModels[i] = f.FallbackModels[i], f.FallbackModels[i-1]
										accesskit.PatchFallbackModels(f, f.FallbackModels)
									}
								}
								ui.Text(c, m).Font(uikit.MonoFont).FontSize(t.FontSize - 1)
								if uikit.SoftButton(c, t, s, "danger", "×").Clicked() {
									f.FallbackModels = append(f.FallbackModels[:i], f.FallbackModels[i+1:]...)
									accesskit.PatchFallbackModels(f, f.FallbackModels)
								}
							})
						})
					}
				})
			}
			if len(labels) > 0 {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					cur := ""
					if len(labels) > 0 {
						cur = labels[0]
					}
					sel := ui.Select(c, &cur, labels)
					if uikit.SoftButton(c, t, s, "gray", "添加回退").Clicked() {
						f.FallbackModels = append(f.FallbackModels, cur)
						accesskit.PatchFallbackModels(f, f.FallbackModels)
					}
					_ = sel
				})
			}
		})
	})
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
