// Package claudecfg 是「Agent 接入」页 Claude Code 页签内的设置区子视图：
// 分层 settings、表单、JSON、合并预览与 Skills 开关。它不新增导航项，由
// pages/access/claude 组合根挂载。
package claudecfg

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// Section 渲染 Claude Code 设置区。首次进入同步加载当前层；之后的编辑
// 只写内存，保存时由 bridge 做冲突检测/备份/原子写。
func Section(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: st.ClaudeCfg.Home}
	ensure(&st.ClaudeCfg)
	if !st.ClaudeCfg.Loaded {
		load(st, cfgs)
	}

	uikit.Card(c, t, func() {
		ui.Column(c).Gap(10).Children(func() {
			header(c, t, s, st, cfgs)
			if !st.ClaudeCfg.Loaded {
				uikit.Feedback(c, t, uikit.InferFeedback(st.ClaudeCfg.Msg), st.ClaudeCfg.Msg)
				return
			}
			layerSelector(c, t, s, st, cfgs)
			fileLine(c, t, s, st)
			viewTabs(c, t, s, st, cfgs)
			switch st.ClaudeCfg.View {
			case "json":
				jsonView(c, t, st)
			case "merged":
				mergedView(c, t, st)
			default:
				FormSection(c, st)
			}
			uikit.Feedback(c, t, uikit.InferFeedback(st.ClaudeCfg.Msg), st.ClaudeCfg.Msg)
		})
	})
}

func ensure(cf *state.ClaudeCfgState) {
	if cf.Layer == "" {
		cf.Layer = "user"
	}
	if cf.Sub == "" {
		cf.Sub = "shared"
	}
	if cf.View == "" {
		cf.View = "form"
	}
	if cf.SkillStates == nil {
		cf.SkillStates = map[string]string{}
	}
}

func load(st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	cf.Msg = ""
	if cf.Projects == nil {
		cf.Projects, _ = cfgs.ListClaudeProjects()
	}
	if cf.Layer == "project" && cf.Project == "" && len(cf.Projects) > 0 {
		cf.Project = cf.Projects[0].Path
	}
	req := bridge.ReadLayerRequest{Kind: corecfg.ScopeUser, Project: cf.Project, Sub: cf.Sub}
	switch cf.Layer {
	case "project":
		if cf.Project == "" {
			cf.Loaded = true
			cf.Doc = map[string]any{}
			cf.Baseline = map[string]any{}
			cf.File = ""
			cf.Display = "请选择项目"
			cf.Exists = false
			cf.Hash = ""
			cf.Msg = "项目级设置需要先选择项目"
			return
		}
		if cf.Sub == "local" {
			req.Kind = corecfg.ScopeLocal
		} else {
			req.Kind = corecfg.ScopeProject
		}
	case "managed":
		req.Kind = corecfg.ScopeManaged
	}
	view, err := cfgs.ReadClaudeConfigLayer(req)
	if err != nil {
		cf.Loaded = false
		cf.Msg = err.Error()
		return
	}
	cf.Loaded = true
	cf.File = view.File
	cf.Display = view.Display
	cf.Exists = view.Exists
	cf.Hash = view.Hash
	cf.Doc = view.Doc
	cf.Baseline = cloneDoc(view.Doc)
	cf.Managed = view.Managed
	cf.Violations = view.Violations
	cf.Overrides = map[string]bool{}
	cf.Reveal = map[string]bool{}
	cf.ParseErr = ""
	if view.ParseError != "" {
		cf.ParseErr = view.ParseError
	}
	cf.JSONText = pretty(view.Doc)
	cf.MergeText = ""
	if merged, entries, err := cfgs.MergeClaudeConfigView(cf.Project); err == nil {
		cf.MergeText = pretty(merged)
		cf.MergeEntries = entries
	}
	skills, skillErr := cfgs.ListClaudeSkills(cf.Project)
	cf.Skills = skills
	if skillErr != nil {
		cf.SkillErr = skillErr.Error()
	} else {
		cf.SkillErr = ""
	}
	syncInputs(cf)
}

func syncInputs(cf *state.ClaudeCfgState) {
	cf.ModelInput = stringAt(cf.Doc, "model")
	cf.FallbackInput = fallbackText(cf.Doc)
	if v, ok := corecfg.Get(cf.Doc, "skipDangerousModePermissionPrompt"); ok {
		b, _ := v.(bool)
		cf.DangerInput = b
	} else {
		cf.DangerInput = cf.Layer == "user"
	}
	cf.SkillStates = map[string]string{}
	if m, ok := cf.Doc["skillOverrides"].(map[string]any); ok {
		for k, v := range m {
			if s, ok := v.(string); ok {
				cf.SkillStates[k] = s
			}
		}
	}
	RecomputeDirty(cf)
}

func header(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, cfgs bridge.ClaudeConfigs) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Claude Code 设置").Bold()
		uikit.SoftBadge(c, t, s, "gray", "受管 13 键")
		ui.Box(c).Grow(1)
		if uikit.SoftButton(c, t, s, "gray", "刷新").Clicked() {
			load(st, cfgs)
		}
		canSave := st.ClaudeCfg.Loaded && st.ClaudeCfg.Layer != "managed" && st.ClaudeCfg.ParseErr == "" && st.ClaudeCfg.Dirty
		btn := uikit.SoftButton(c, t, s, "act", "保存设置")
		if !canSave {
			btn.Disabled(true)
		}
		if btn.Clicked() && canSave {
			save(st, cfgs)
		}
	})
}

func layerSelector(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	idx := 0
	if cf.Layer == "project" {
		idx = 1
	} else if cf.Layer == "managed" {
		idx = 2
	}
	if ui.Tabs(c, &idx, "用户级", "项目级", "企业托管").Changed() {
		if cf.Dirty {
			cf.Msg = "有未保存修改；请先保存或点「刷新」放弃"
			return
		}
		switch idx {
		case 1:
			cf.Layer = "project"
		case 2:
			cf.Layer = "managed"
		default:
			cf.Layer = "user"
		}
		cf.Doc = nil
		cf.Loaded = false
		load(st, cfgs)
	}
	if cf.Layer == "project" {
		paths := make([]string, 0, len(cf.Projects))
		for _, p := range cf.Projects {
			paths = append(paths, p.Path)
		}
		if len(paths) > 0 {
			project := cf.Project
			if ui.Select(c, &project, paths).Changed() && project != cf.Project {
				if cf.Dirty {
					cf.Msg = "有未保存修改；请先保存或点「刷新」放弃"
					return
				}
				cf.Project = project
				cf.Doc = nil
				cf.Loaded = false
				load(st, cfgs)
			}
		} else {
			ui.Text(c, "未读取到最近项目，请从接入页选择项目或手动输入路径").TextColor(t.TextMuted)
		}
		sub := 0
		if cf.Sub == "local" {
			sub = 1
		}
		if ui.Tabs(c, &sub, "共享 .claude/settings.json", "本地 .claude/settings.local.json").Changed() {
			if cf.Dirty {
				cf.Msg = "有未保存修改；请先保存或点「刷新」放弃"
				return
			}
			if sub == 1 {
				cf.Sub = "local"
			} else {
				cf.Sub = "shared"
			}
			cf.Doc = nil
			cf.Loaded = false
			load(st, cfgs)
		}
	}
	if cf.Layer == "managed" {
		ui.Text(c, "企业托管层只读；本页不写入托管文件").TextColor(t.TextMuted)
	}
	_ = s
}

func fileLine(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State) {
	cf := &st.ClaudeCfg
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, cf.Display).Font(uikit.MonoFont).TextColor(t.TextMuted)
		if cf.Exists {
			uikit.SoftBadge(c, t, s, "ok", "已存在")
		} else {
			uikit.SoftBadge(c, t, s, "gray", "文件不存在")
		}
		if cf.ParseErr != "" {
			uikit.SoftBadge(c, t, s, "danger", "JSON 错误")
		}
	})
	if cf.ParseErr != "" {
		uikit.Feedback(c, t, uikit.FeedbackError, cf.ParseErr)
	}
}

func viewTabs(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	idx := 0
	if cf.View == "json" {
		idx = 1
	} else if cf.View == "merged" {
		idx = 2
	}
	if ui.Tabs(c, &idx, "表单", "JSON", "合并预览").Changed() {
		if idx == 0 {
			cf.View = "form"
		} else if idx == 1 {
			cf.View = "json"
		} else {
			cf.View = "merged"
			if merged, err := cfgs.MergeClaudeConfig(cf.Project); err == nil {
				cf.MergeText = pretty(merged)
			}
		}
	}
	_ = s
}

// FormRow / FormRowRaw 是本页对 uikit 表单行的薄封装，便于 form.go 保持
// 与现有页面一致的 label/control 对齐。
func FormRow(c *ui.Context, t *ui.Theme, label string, field func() ui.Element) {
	uikit.FormRow(c, t, label, field)
}

func FormRowRaw(c *ui.Context, t *ui.Theme, label string, field func()) {
	uikit.FormRowRaw(c, t, label, field)
}

func jsonView(c *ui.Context, t *ui.Theme, st *state.State) {
	cf := &st.ClaudeCfg
	readOnly := cf.Layer == "managed"
	ta := ui.TextArea(c, &cf.JSONText).Height(320).Font(uikit.MonoFont).ReadOnly(readOnly)
	if ta.Changed() && !readOnly {
		var doc map[string]any
		if err := json.Unmarshal([]byte(cf.JSONText), &doc); err != nil {
			cf.ParseErr = err.Error()
		} else {
			cf.Doc = doc
			cf.ParseErr = ""
			RecomputeDirty(cf)
		}
	}
	_ = t
}

func mergedView(c *ui.Context, t *ui.Theme, st *state.State) {
	cf := &st.ClaudeCfg
	ui.Text(c, "只读：跨层合并后的最终值；不会写回任何文件").TextColor(t.TextMuted)
	ui.TextArea(c, &cf.MergeText).Height(360).Font(uikit.MonoFont).ReadOnly(true)
	_ = t
}

func save(st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	if cf.View == "json" && cf.ParseErr != "" {
		cf.Msg = "JSON 有错误，先修复再保存"
		return
	}
	if cf.Layer == "user" {
		if _, ok := cf.Doc["skipDangerousModePermissionPrompt"]; !ok {
			cf.Doc["skipDangerousModePermissionPrompt"] = true
		}
	}
	req := bridge.SaveClaudeConfigRequest{
		Kind:         scopeOf(cf.Layer, cf.Sub),
		Project:      cf.Project,
		Sub:          cf.Sub,
		Document:     cf.Doc,
		ExpectedHash: cf.Hash,
		Create:       !cf.Exists,
	}
	if cf.Exists && cf.Hash == "" {
		req.ExpectedHash = ""
	}
	res, err := cfgs.SaveClaudeConfigLayer(req)
	if err != nil {
		cf.Msg = err.Error()
		return
	}
	cf.Msg = "已保存 " + strings.Join(res.Changed, ", ")
	cf.Loaded = false
	load(st, cfgs)
}

func scopeOf(layer, sub string) corecfg.ScopeKind {
	if layer == "managed" {
		return corecfg.ScopeManaged
	}
	if layer == "project" {
		if sub == "local" {
			return corecfg.ScopeLocal
		}
		return corecfg.ScopeProject
	}
	return corecfg.ScopeUser
}

func setString(doc map[string]any, path, value string) {
	if strings.TrimSpace(value) == "" {
		corecfg.Delete(doc, path)
		return
	}
	corecfg.Set(doc, path, value)
}

func stringAt(doc map[string]any, path string) string {
	v, _ := corecfg.Get(doc, path)
	s, _ := v.(string)
	return s
}

func fallbackText(doc map[string]any) string {
	v, ok := doc["fallbackModel"]
	if !ok {
		return ""
	}
	arr, ok := v.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

func pretty(doc map[string]any) string {
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return ""
	}
	return string(b)
}

func cloneDoc(doc map[string]any) map[string]any {
	b, _ := json.Marshal(doc)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	if out == nil {
		out = map[string]any{}
	}
	return out
}

func boolLabel(v bool) string {
	if v {
		return "开启"
	}
	return "关闭"
}

func skillLabel(v corecfg.SkillState) string {
	if s, ok := skillLabels[string(v)]; ok {
		return s
	}
	return string(v)
}

func lockLabel(reason string) string {
	switch reason {
	case "plugin":
		return "由插件管理"
	case "managed", "policy":
		return "托管策略锁定"
	case "author":
		return "作者锁定"
	case "reserved-name":
		return "保留名称"
	}
	return reason
}
