// Package claude 是接入页的 Claude Code 页签编辑器：默认逻辑模型下拉 + 1M
// 声明，下接 5 角色模型映射（Sonnet/Opus/Fable/Haiku/Subagent），行状态由
// 配置 JSON 双向同步（accesskit 角色助手）。
package claude

import (
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// EditorCard 渲染 Claude Code 接入编辑器。
func EditorCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, gateOK bool) {
	accesskit.EditorCard(c, t, s, st, acc, gateOK, accesskit.EditorHooks{
		ModelSection: func(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, labels []string, models []bridge.ModelView) {
			modelSection(c, t, st, acc, labels)
			rolesSection(c, t, st, labels)
		},
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
