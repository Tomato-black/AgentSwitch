package accesskit

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// EditorHooks 让各 Agent 子包注入自己的编辑器段落：
//   - ModelSection 替换默认的「逻辑模型下拉」段落（Claude 用它渲染下拉 +
//     1M + 模型映射；Codex 用它渲染模型列表/元数据/高级设置）；
//   - AfterDoc 在配置 JSON 文本域之后追加内容（Codex 的只读 config.toml
//     预览）。
type EditorHooks struct {
	ModelSection func(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, labels []string, models []bridge.ModelView)
	AfterDoc     func(c *ui.Context, t *ui.Theme, s uikit.SoftPalette)
}

// EditorCard 渲染接入配置的新建/编辑表单骨架（spec 2026-10-19 §5.1）：
// 名称、模型段落（hooks 或默认逻辑模型下拉）、网关只读行、配置 JSON
// （TextArea，令牌渲染真值）、提示与保存/取消。保存原文送 bridge
// （校验+归一化在 bridge）。
func EditorCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, gateOK bool, hooks EditorHooks) {
	f := &st.Access.Form
	uikit.Card(c, t, func() {
		ui.Column(c).Gap(10).Children(func() {
			uikit.FormRow(c, t, "名称:", func() ui.Element {
				return ui.TextInput(c, &f.Name).Grow(1)
			})

			var models []bridge.ModelView
			if st.Svc.Logical != nil {
				models = st.Svc.Logical.List()
			}
			labels := ModelLabels(models)
			docErr := ValidateDocJSON(f.DocJSON)
			modelSection := func() {
				if hooks.ModelSection != nil {
					hooks.ModelSection(c, t, s, labels, models)
				} else {
					DefaultModelSection(c, t, st, acc, labels, ModelHooks{})
				}
			}
			if docErr != nil {
				// JSON 非法时表单失同步：禁用模型/角色编辑区，避免「行变了
				// 文档没变」；用户先修 JSON 才能继续。
				ui.Box(c).Disabled(true).Children(modelSection)
				uikit.Feedback(c, t, uikit.FeedbackError, docErr.Error())
			} else {
				modelSection()
			}

			gs := st.Svc.Gateway.Status()
			ui.Text(c, fmt.Sprintf("网关地址 http://127.0.0.1:%d · 令牌自动生成并填入下方 JSON（保存时自动剔除，不落库）", gs.Port)).TextColor(t.TextMuted)

			ui.Text(c, "配置 JSON（最终写入 Agent 的完整文档；网关地址/令牌自动锁定，模型名仅可追加 1M 声明）").TextColor(t.TextMuted)
			ta := ui.TextArea(c, &f.DocJSON).Height(320).Grow(1).Font(uikit.MonoFont)
			if ta.Changed() {
				// 现状语义保留：任何 Agent 的文本域变更都同步角色行（Codex
				// 不用角色行，但成本低且保持与旧实现逐字一致）。
				SyncRolesFromDoc(f)
			}
			ui.Text(c, "与全局/默认值相同的键保存时自动视为未定制（保持继承）；要覆盖请改成不同值").TextColor(t.TextMuted)

			if hooks.AfterDoc != nil {
				hooks.AfterDoc(c, t, s)
			}

			if f.EditID != "" {
				if lines := DocDiff(f.OriginalDoc, f.DocJSON); len(lines) > 0 {
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

			ui.Row(c).Gap(8).Children(func() {
				if !gateOK {
					ui.Text(c, "网关未运行——保存后需启动网关才能「应用」").TextColor(t.TextMuted)
				}
				if gateOK {
					if docErr != nil {
						uikit.PillButton(c, t, "保存并应用").Disabled(true)
						uikit.SoftButton(c, t, s, "gray", "仅保存").Disabled(true)
					} else {
						if uikit.PillButton(c, t, "保存并应用").Clicked() {
							saveFormWithApply(st, acc, true)
						}
						if uikit.SoftButton(c, t, s, "gray", "仅保存").Clicked() {
							saveForm(st, acc)
						}
					}
				} else if docErr != nil {
					uikit.PillButton(c, t, "保存").Disabled(true)
				} else if uikit.PillButton(c, t, "保存").Clicked() {
					saveForm(st, acc)
				}
				if uikit.SoftButton(c, t, s, "gray", "取消").Clicked() {
					CloseEditor(st)
				}
			})
		})
	})
}

// ModelHooks 让 Agent 在主模型下拉行注入自己的行为（差异归 Agent 子包，
// 共享 kit 不含任何 Agent 专属分支）：
//   - OnChanged 在主模型切换后、重新预览前调用（Agent 可重定位派生键、
//     同步 1M 声明等）；
//   - Extra 在下拉行内追加 Agent 控件（如 Claude 的 1M 勾选）。
type ModelHooks struct {
	OnChanged func(prev string)
	Extra     func()
}

// DefaultModelSection 是通用的「逻辑模型下拉」段落：切换主模型后经
// hooks.OnChanged 交给 Agent 处理，再重新取合成预览；hooks.Extra 渲染在
// 下拉行内。
func DefaultModelSection(c *ui.Context, t *ui.Theme, st *state.State, acc bridge.AccessConfigs, labels []string, hooks ModelHooks) {
	f := &st.Access.Form
	uikit.FormRowRaw(c, t, "逻辑模型:", func() {
		if len(labels) == 0 {
			ui.Text(c, "还没有启用的逻辑模型——先在「逻辑模型」页创建并启用").TextColor(t.TextMuted)
			return
		}
		prev := f.ModelLabel
		sel := ui.Select(c, &f.ModelLabel, labels)
		if sel.Changed() && f.ModelLabel != prev {
			if hooks.OnChanged != nil {
				hooks.OnChanged(prev)
			}
			RepreviewForm(st, acc)
		}
		if hooks.Extra != nil {
			hooks.Extra()
		}
	})
}

// RepreviewForm 用当前表单模型重新合成预览（基线变化后调用）；失败内联报错。
func RepreviewForm(st *state.State, acc bridge.AccessConfigs) {
	f := &st.Access.Form
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	modelID := ModelIDByLabel(models, f.ModelLabel)
	preview, err := acc.PreviewDoc(st.Access.App, modelID, f.DocJSON)
	if err != nil {
		f.Msg = "预览失败：" + err.Error()
		return
	}
	f.DocJSON = preview
	f.Msg = ""
}

// saveForm 校验并仅保存表单；文档原文送 bridge（parse+归一化错误内联）。
func saveForm(st *state.State, acc bridge.AccessConfigs) {
	saveFormWithApply(st, acc, false)
}

// saveFormWithApply 是保存主路径：apply 为真时保存后立刻异步「应用」到
// Agent 配置文件（P1-1 保存/应用断点）。仅保存保留给需要先检查配置的用户。
func saveFormWithApply(st *state.State, acc bridge.AccessConfigs, apply bool) {
	f := &st.Access.Form
	if err := ValidateDocJSON(f.DocJSON); err != nil {
		f.Msg = err.Error()
		return
	}
	if strings.TrimSpace(f.Name) == "" {
		f.Msg = "请填写配置名"
		return
	}
	modelLabel := f.ModelLabel
	if st.Access.App == "codex" {
		// 默认模型 = 模型列表首项（spec 2026-10-27 §4.2 不变量）。
		if len(f.CodexModels) == 0 {
			f.Msg = "请至少添加一个模型"
			return
		}
		modelLabel = f.CodexModels[0]
	}
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	modelID := ModelIDByLabel(models, modelLabel)
	if modelID == "" {
		f.Msg = "请选择逻辑模型"
		return
	}
	settingsJSON := strings.TrimSpace(f.DocJSON)
	if settingsJSON == "" {
		settingsJSON = "{}"
	}
	name := f.Name
	var saved bridge.AccessConfigView
	if f.EditID != "" {
		if err := acc.Update(f.EditID, name, modelID, settingsJSON); err != nil {
			f.Msg = err.Error()
			return
		}
		for _, v := range acc.List(st.Access.App) {
			if v.ID == f.EditID {
				saved = v
				break
			}
		}
	} else {
		v, err := acc.Create(st.Access.App, name, modelID, settingsJSON)
		if err != nil {
			f.Msg = err.Error()
			return
		}
		saved = v
	}
	st.Access.Form = state.AccessForm{}
	if apply && saved.ID != "" {
		st.Access.Msg = "已保存「" + name + "」，正在应用…"
		ApplyAsync(st, acc, saved)
		return
	}
	if saved.IsCurrent && saved.NeedsApply {
		st.Access.Msg = "已保存「" + name + "」；点击「应用」后才会写入 Agent 文件"
	} else {
		st.Access.Msg = "已保存「" + name + "」"
	}
}
