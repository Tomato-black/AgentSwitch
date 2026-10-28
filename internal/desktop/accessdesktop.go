package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/uikit"
)

// Claude Desktop 接入页签（2026-10-22）：与 Claude Code/Codex 不同，Desktop
// 配置是「角色映射」而非 env 文档——每条配置为 4 个角色（sonnet/opus/haiku/
// fable）各选一个逻辑模型（未设置=不启用）与 1M 声明；「应用」把受管网关键
// patch 进平台对应的 Claude/claude_desktop_config.json（Windows 为
// %LOCALAPPDATA%，见 bridge.desktopPathsFor）并持久化网关别名表。

// desktopForm 是 Desktop 接入的新建/编辑表单状态。
type desktopForm struct {
	open   bool
	editID string // 非空 = 编辑该配置
	name   string
	// models/oneM 与 bridge.desktopRoleOrder 同序（sonnet/opus/haiku/fable）：
	// models[i] 为空串 = 该角色未启用。
	models [4]string
	oneM   [4]bool
	msg    string
}

// desktopRoleLabels 是角色行显示名（与 bridge 角色固定序一致）。
var desktopRoleLabels = [4]string{"Sonnet", "Opus", "Haiku", "Fable"}

// desktopDocFromForm 把表单行转成 bridge 角色映射文档；无启用角色返回 error。
func (f *desktopForm) desktopDocFromForm() (string, error) {
	doc := map[string]any{}
	roles := []string{"sonnet", "opus", "haiku", "fable"}
	for i, role := range roles {
		model := strings.TrimSpace(f.models[i])
		if model == "" {
			continue
		}
		entry := map[string]any{"model": model}
		if f.oneM[i] {
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
func (u *uiState) desktopFormFromDoc(raw string) {
	doc := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return
		}
	}
	f := &u.desktopForm
	roles := []string{"sonnet", "opus", "haiku", "fable"}
	for i, role := range roles {
		f.models[i] = ""
		f.oneM[i] = false
		v, ok := doc[role]
		if !ok || v == nil {
			continue
		}
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		if s, ok := m["model"].(string); ok {
			f.models[i] = s
		}
		if b, ok := m["oneM"].(bool); ok {
			f.oneM[i] = b
		}
	}
}

// desktopPreloadForm 打开 Desktop 编辑器：新建空表单；编辑回填配置。
func (u *uiState) desktopPreloadForm(acc bridge.AccessConfigs) {
	f := &u.desktopForm
	if f.editID == "" {
		return
	}
	for _, v := range acc.List("claude_desktop") {
		if v.ID == f.editID {
			f.name = v.Name
			u.desktopFormFromDoc(v.Settings)
			return
		}
	}
}

// desktopEditorCard 渲染 Desktop 配置的新建/编辑表单。
func (u *uiState) desktopEditorCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, acc bridge.AccessConfigs, gateOK bool) {
	uikit.Card(c, t, func() {
		ui.Column(c).Gap(10).Children(func() {
			title := "新建配置"
			if u.desktopForm.editID != "" {
				title = "编辑配置"
			}
			ui.Text(c, title).Bold()
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
				ui.TextInput(c, &u.desktopForm.name).Grow(1)
			})

			var models []bridge.ModelView
			if u.Svc.Logical != nil {
				models = u.Svc.Logical.List()
			}
			labels := accessModelLabels(models)
			ui.Text(c, "角色映射（未设置 = 该角色不启用；1M 为 1M 上下文声明）").TextColor(t.TextMuted)
			if len(labels) == 0 {
				ui.Text(c, "还没有启用的逻辑模型——先在「逻辑模型」页创建并启用").TextColor(t.TextMuted)
			}
			for i := range desktopRoleLabels {
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, desktopRoleLabels[i]).Width(80).TextColor(t.TextMuted)
					opts := append([]string{"未设置"}, labels...)
					ui.Select(c, &u.desktopForm.models[i], opts).Grow(1)
					if strings.TrimSpace(u.desktopForm.models[i]) != "" {
						ui.Checkbox(c, &u.desktopForm.oneM[i], "1M")
					}
				})
			}
			st := u.Svc.Gateway.Status()
			ui.Text(c, fmt.Sprintf("「应用」写入 Desktop 配置（网关 http://127.0.0.1:%d）；改后需重启 Claude Desktop 生效", st.Port)).TextColor(t.TextMuted)

			if u.desktopForm.msg != "" {
				ui.Text(c, u.desktopForm.msg).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Children(func() {
				if !gateOK {
					ui.Text(c, "网关未运行——保存后需启动网关才能「应用」").TextColor(t.TextMuted)
				}
				if uikit.PillButton(c, t, "保存").Clicked() {
					u.desktopSaveForm(acc, models)
				}
				if uikit.SoftButton(c, t, s, "gray", "取消").Clicked() {
					u.desktopForm = desktopForm{}
				}
			})
		})
	})
}

// desktopSaveForm 校验并保存 Desktop 配置：logicalModelID 绑定第一个启用
// 角色的逻辑模型（列表展示用）。
func (u *uiState) desktopSaveForm(acc bridge.AccessConfigs, models []bridge.ModelView) {
	f := &u.desktopForm
	if strings.TrimSpace(f.name) == "" {
		f.msg = "请填写配置名"
		return
	}
	settingsJSON, err := f.desktopDocFromForm()
	if err != nil {
		f.msg = err.Error()
		return
	}
	bindID := ""
	for i := range f.models {
		if m := strings.TrimSpace(f.models[i]); m != "" {
			bindID = accessModelIDByLabel(models, m)
			break
		}
	}
	if bindID == "" {
		f.msg = "至少启用一个角色"
		return
	}
	if f.editID != "" {
		if err := acc.Update(f.editID, f.name, bindID, settingsJSON); err != nil {
			f.msg = err.Error()
			return
		}
	} else {
		if _, err := acc.Create("claude_desktop", f.name, bindID, settingsJSON); err != nil {
			f.msg = err.Error()
			return
		}
	}
	u.accessMsg = "已保存「" + f.name + "」"
	u.desktopForm = desktopForm{}
}

// accessDesktopConfigRow 渲染 Desktop 配置行：应用/编辑/删除（无启动命令与
// 终端——Desktop 是 GUI，重启其应用生效）。逻辑模型摘要展示角色映射。
func (u *uiState) accessDesktopConfigRow(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, acc bridge.AccessConfigs, v bridge.AccessConfigView, gateOK bool) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, v.Name).Width(140)
		if badge := accessRowBadge(v.IsCurrent); badge != "" {
			uikit.SoftBadge(c, t, s, "ok", badge)
		}
		ui.Text(c, "→ "+desktopRolesSummary(v.Settings)).TextColor(t.TextMuted).Grow(1)
		if gateOK {
			label := "应用"
			if v.IsCurrent {
				label = "重新应用"
			}
			if uikit.SoftButton(c, t, s, "act", label).Clicked() {
				if err := acc.Apply(v.ID); err != nil {
					u.accessMsg = "应用失败：" + err.Error()
				} else {
					u.accessMsg = "已应用「" + v.Name + "」——重启 Claude Desktop 生效"
				}
			}
		}
		if uikit.SoftButton(c, t, s, "gray", "编辑").Clicked() {
			u.desktopForm = desktopForm{open: true, editID: v.ID}
			u.desktopPreloadForm(acc)
		}
		if uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
			if err := acc.Remove(v.ID); err != nil {
				u.accessMsg = "删除失败：" + err.Error()
			} else {
				u.accessMsg = "已删除「" + v.Name + "」——Desktop 配置文件保留最后状态"
			}
		}
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
		s := r.Role + "→" + r.Model
		if r.OneM {
			s += "[1M]"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, " · ")
}
