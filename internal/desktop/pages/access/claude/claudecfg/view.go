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
			if st.ClaudeCfg.Warning != "" {
				uikit.Feedback(c, t, uikit.FeedbackWarning, st.ClaudeCfg.Warning)
			}
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
	saveDialog(c, st, cfgs)
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
	cf.ProjectInput = cf.Project
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
	if ov, ovErr := cfgs.ClaudeConfigOverview(); ovErr == nil {
		cf.Warning = ov.Warning
	}
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
		canSave := st.ClaudeCfg.Loaded && st.ClaudeCfg.Layer != "managed" && st.ClaudeCfg.Dirty &&
			saveBlockReason(&st.ClaudeCfg) == ""
		btn := uikit.SoftButton(c, t, s, "act", "保存设置")
		if !canSave {
			btn.Disabled(true)
		}
		if btn.Clicked() && canSave {
			st.ClaudeCfg.SaveArm = true
		}
	})
	if st.ClaudeCfg.Loaded && st.ClaudeCfg.Dirty {
		if reason := saveBlockReason(&st.ClaudeCfg); reason != "" {
			uikit.Feedback(c, t, uikit.FeedbackError, reason)
		}
	}
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
			ui.Text(c, "未读取到最近项目；可手动输入项目目录").TextColor(t.TextMuted)
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &cf.ProjectInput).Grow(1).Placeholder("/path/to/project")
			if uikit.SoftButton(c, t, s, "gray", "打开").Clicked() && strings.TrimSpace(cf.ProjectInput) != "" && cf.ProjectInput != cf.Project {
				if cf.Dirty {
					cf.Msg = "有未保存修改；请先保存或点「刷新」放弃"
					return
				}
				cf.Project = strings.TrimSpace(cf.ProjectInput)
				cf.Doc = nil
				cf.Loaded = false
				load(st, cfgs)
			}
		})
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
			if cf.View != "json" {
				// 从表单切回 JSON 时按当前 Doc 重建文本，避免显示旧快照。
				cf.JSONText = pretty(cf.Doc)
				cf.ParseErr = ""
				cf.JSONHint = ""
				cf.JSONPending = false
			}
			cf.View = "json"
		} else {
			if cf.View != "merged" {
				cf.View = "merged"
				if merged, entries, err := cfgs.MergeClaudeConfigView(cf.Project); err == nil {
					cf.MergeText = pretty(merged)
					cf.MergeEntries = entries
				}
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

func saveDialog(c *ui.Context, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	changed := corecfg.Diff(cf.Baseline, cf.Doc)
	msg := "目标文件：" + cf.Display + "\n变更 " + itoa(len(changed)) + " 个键"
	if len(changed) > 0 {
		msg += "：" + strings.Join(changed, "、")
	}
	msg += "\n写入前会备份当前文件；只改这些键，其余设置原样保留。"
	uikit.ConfirmDialog(c, c.Theme(), uikit.ConfirmOpts{
		Open: &cf.SaveArm, Title: "保存 Claude Code 设置？", Message: msg,
		ConfirmLabel: "保存", OnConfirm: func() { performSave(st, cfgs) },
	})
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func performSave(st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	// JSON 语法错误、本次引入的 scope 违规、非法 skillOverrides 与受管键
	// 修改都在这里统一阻断，并给出具体路径与原因。
	if reason := saveBlockReason(cf); reason != "" {
		cf.Msg = reason
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
