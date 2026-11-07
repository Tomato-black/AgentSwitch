// Package claudedesktop 是接入页的 Claude Desktop 页签：角色映射（sonnet/
// opus/haiku/fable → 逻辑模型 + 1M 声明）表单与配置行；「应用」由 bridge
// 把网关 profile 写入 Desktop 配置并持久化网关别名表。
package claudedesktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/modelid"
)

// Claude Desktop 接入页签（2026-10-22）：与 Claude Code/Codex 不同，Desktop
// 配置是「角色映射」而非 env 文档——每条配置为 4 个角色（sonnet/opus/haiku/
// fable）各选一个逻辑模型（未设置=不启用）与 1M 声明；「应用」把受管网关键
// patch 进平台对应的 Claude/claude_desktop_config.json（Windows 为
// %LOCALAPPDATA%，见 bridge.desktopPathsFor）并持久化网关别名表。

// desktopRoleLabels 是角色行显示名（与 bridge 角色固定序一致）。
var desktopRoleLabels = [4]string{"Sonnet", "Opus", "Haiku", "Fable"}

// desktopDocFromForm 把表单行转成 bridge 角色映射文档；无启用角色返回 error。
func desktopDocFromForm(f *state.DesktopForm) (string, error) {
	doc := map[string]any{}
	roles := []string{"sonnet", "opus", "haiku", "fable"}
	for i, role := range roles {
		model := strings.TrimSpace(f.Models[i])
		if model == "" {
			continue
		}
		entry := map[string]any{"model": model}
		if f.OneM[i] {
			entry["oneM"] = true
		}
		doc[role] = entry
	}
	if len(doc) == 0 {
		return "", fmt.Errorf("至少启用一个角色")
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// desktopFormFromDoc 把角色映射文档回填进表单行（解析失败保留旧行）。
func desktopFormFromDoc(st *state.State, raw string) {
	doc := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return
		}
	}
	f := &st.Access.DesktopForm
	roles := []string{"sonnet", "opus", "haiku", "fable"}
	for i, role := range roles {
		f.Models[i] = ""
		f.OneM[i] = false
		v, ok := doc[role]
		if !ok || v == nil {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if s, ok := m["model"].(string); ok {
			f.Models[i] = s
		}
		if b, ok := m["oneM"].(bool); ok {
			f.OneM[i] = b
		}
	}
}

// preloadForm 打开 Desktop 编辑器：新建空表单；编辑回填配置。
func preloadForm(st *state.State, acc bridge.AccessConfigs) {
	f := &st.Access.DesktopForm
	if f.EditID == "" {
		f.Baseline = f.Signature()
		return
	}
	for _, v := range acc.List("claude_desktop") {
		if v.ID == f.EditID {
			f.Name = v.Name
			desktopFormFromDoc(st, v.Settings)
			f.Baseline = f.Signature()
			refreshPreview(st, acc, false)
			return
		}
	}
	f.Baseline = f.Signature()
}

// Prepare 在编辑器打开后生成只读 profile 预览（新建表单由宿主调用；内容
// 尚不完整时静默失败）。
func Prepare(st *state.State, acc bridge.AccessConfigs) { refreshPreview(st, acc, false) }

// refreshPreview 重新生成网关 profile JSON 预览；report 为真时错误进
// 表单信息位。
func refreshPreview(st *state.State, acc bridge.AccessConfigs, report bool) {
	f := &st.Access.DesktopForm
	doc, err := desktopDocFromForm(f)
	if err != nil {
		if report {
			f.Msg = err.Error()
		}
		return
	}
	p, err := acc.PreviewDesktopProfile(doc)
	if err != nil {
		if report {
			f.Msg = "预览失败：" + err.Error()
		}
		return
	}
	f.Preview = p
	if report {
		f.Msg = ""
	}
}

// desktopEditorCard 渲染 Desktop 配置的新建/编辑表单。
func EditorCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, gateOK bool) {
	uikit.Card(c, t, func() {
		ui.Column(c).Gap(10).Children(func() {
			uikit.FormRow(c, t, "名称:", func() ui.Element {
				return ui.TextInput(c, &st.Access.DesktopForm.Name).Grow(1)
			})

			var models []bridge.ModelView
			if st.Svc.Logical != nil {
				models = st.Svc.Logical.List()
			}
			labels := accesskit.ModelLabels(models)
			ui.Text(c, "角色映射（未设置 = 该角色不启用；1M 为 1M 上下文声明）").TextColor(t.TextMuted)
			if len(labels) == 0 {
				ui.Text(c, "还没有启用的逻辑模型——先在「逻辑模型」页创建并启用").TextColor(t.TextMuted)
			}
			for i := range desktopRoleLabels {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, desktopRoleLabels[i]).Width(80).TextColor(t.TextMuted)
					opts := append([]string{"未设置"}, labels...)
					ui.Select(c, &st.Access.DesktopForm.Models[i], opts).Grow(1)
					if strings.TrimSpace(st.Access.DesktopForm.Models[i]) != "" {
						ui.Checkbox(c, &st.Access.DesktopForm.OneM[i], "1M")
					}
				})
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "将写入的网关 profile（只读）").Bold()
				ui.Box(c).Grow(1)
				if uikit.SoftButton(c, t, s, "gray", "刷新预览").Clicked() {
					refreshPreview(st, acc, true)
				}
			})
			ui.TextArea(c, &st.Access.DesktopForm.Preview).Height(200).ReadOnly(true).Font(uikit.MonoFont)

			gs := st.Svc.Gateway.Status()
			ui.Text(c, fmt.Sprintf("「应用」写入 Desktop 配置（网关 http://127.0.0.1:%d）；改后需重启 Claude Desktop 生效", gs.Port)).TextColor(t.TextMuted)

			uikit.Feedback(c, t, uikit.InferFeedback(st.Access.DesktopForm.Msg), st.Access.DesktopForm.Msg)
			ui.Row(c).Gap(8).Children(func() {
				if !gateOK {
					ui.Text(c, "网关未运行——保存后需启动网关才能「应用」").TextColor(t.TextMuted)
				}
				if uikit.PillButton(c, t, "保存").Clicked() {
					desktopSaveForm(st, acc, models)
				}
				if uikit.SoftButton(c, t, s, "gray", "取消").Clicked() {
					accesskit.CloseEditor(st)
				}
			})
		})
	})
}

// desktopSaveForm 校验并保存 Desktop 配置：logicalModelID 绑定第一个启用
// 角色的逻辑模型（列表展示用）。
func desktopSaveForm(st *state.State, acc bridge.AccessConfigs, models []bridge.ModelView) {
	f := &st.Access.DesktopForm
	if strings.TrimSpace(f.Name) == "" {
		f.Msg = "请填写配置名"
		return
	}
	settingsJSON, err := desktopDocFromForm(f)
	if err != nil {
		f.Msg = err.Error()
		return
	}
	bindID := ""
	for i := range f.Models {
		if m := strings.TrimSpace(f.Models[i]); m != "" {
			bindID = accesskit.ModelIDByLabel(models, m)
			break
		}
	}
	if bindID == "" {
		f.Msg = "至少启用一个角色"
		return
	}
	if f.EditID != "" {
		if err := acc.Update(f.EditID, f.Name, bindID, settingsJSON); err != nil {
			f.Msg = err.Error()
			return
		}
	} else {
		if _, err := acc.Create("claude_desktop", f.Name, bindID, settingsJSON); err != nil {
			f.Msg = err.Error()
			return
		}
	}
	st.Access.Msg = "已保存「" + f.Name + "」"
	if f.EditID != "" {
		for _, v := range acc.List("claude_desktop") {
			if v.ID == f.EditID && v.IsCurrent && v.NeedsApply {
				st.Access.Msg = "已保存「" + f.Name + "」；点击「应用」后才会写入 Claude Desktop 配置"
				break
			}
		}
	}
	st.Access.DesktopForm = state.DesktopForm{}
}

// ConfigRow 渲染 Desktop 配置行：角色摘要与操作分两行，长名称单行截断。
func ConfigRow(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, v bridge.AccessConfigView, gateOK bool) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, v.Name).Bold().Width(180).SingleLine()
			if badge := accesskit.RowBadge(v.IsCurrent); badge != "" {
				uikit.SoftBadge(c, t, s, "ok", badge)
			}
			if v.NeedsApply {
				uikit.SoftBadge(c, t, s, "warn", "待应用")
			}
			ui.Text(c, "→ "+desktopRolesSummary(v.Settings)).TextColor(t.TextMuted).Grow(1).SingleLine()
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if gateOK {
				label := "应用"
				if v.IsCurrent && !v.NeedsApply {
					label = "重新应用"
				}
				switch {
				case st.Access.ApplyTask.Running && st.Access.ApplyID == v.ID:
					uikit.BusyRow(c, t, st.Access.ApplyTask.Label, nil)
				case st.Access.ApplyTask.Running:
					uikit.SoftButton(c, t, s, "gray", label).Disabled(true)
				default:
					if uikit.SoftButton(c, t, s, "act", label).Clicked() {
						accesskit.ApplyAsync(st, acc, v)
					}
				}
			}
			if uikit.SoftButton(c, t, s, "gray", "编辑").Clicked() {
				st.Access.DesktopForm = state.DesktopForm{Open: true, EditID: v.ID}
				preloadForm(st, acc)
			}
			// 次级操作走主题化菜单（mygo v0.7.2+）：删除为 Danger 红项，选中即关。
			ui.PopoverMenuButton(c, "更多", func(m *ui.Menu) {
				if m.Item("删除").Danger().Chosen() {
					st.Access.DeleteArm = true
					st.Access.DeleteID = v.ID
				}
			})
		})
	})
}

// desktopRolesSummary 把角色映射文档压成一行摘要（如「sonnet→my-code ·
// opus→mimo[1M]」）。
func desktopRolesSummary(docJSON string) string {
	routes, err := bridge.DesktopRoutesOf(docJSON)
	if err != nil || len(routes) == 0 {
		return "（未启用角色）"
	}
	parts := make([]string, 0, len(routes))
	for _, r := range routes {
		s := modelid.AppendOneM(r.Role+"→"+r.Model, r.OneM)
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}
