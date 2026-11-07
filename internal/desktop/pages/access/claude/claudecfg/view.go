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

// Section 渲染 Claude Code 设置区（原型 2026-11-07 §4.1：区块头在卡外 +
// 当前文件条 + 层级切换 + 视图切换 + 内容卡片流）。首次进入同步加载当前层；
// 之后的编辑只写内存，保存时由 bridge 做冲突检测/备份/原子写。
func Section(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: st.ClaudeCfg.Home}
	ensure(&st.ClaudeCfg)
	if !st.ClaudeCfg.Loaded {
		load(st, cfgs)
	}

	ui.Column(c).Gap(10).Children(func() {
		// 区块头（卡外）：标题 + 受管键徽章 + 区块动作（刷新次操作 / 保存设置
		// 橙胶囊主操作）。2026-11-07 用户裁定：这两个动作只作用于 Claude 设置
		// 区，收回区块头渲染，不进全页签共享的页头（偏离需求 §4.1 页头动作区）。
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Claude Code 设置").Bold().FontSize(t.FontSize + 1)
			uikit.SoftBadge(c, t, s, "gray", "受管 13 键")
			ui.Box(c).Grow(1)
			if uikit.SoftButton(c, t, s, "gray", "刷新").Clicked() {
				guardReload(st, func() { load(st, cfgs) })
			}
			canSave := st.ClaudeCfg.Loaded && st.ClaudeCfg.Layer != "managed" && st.ClaudeCfg.Dirty &&
				saveBlockReason(&st.ClaudeCfg) == ""
			btn := uikit.PillButton(c, t, "保存设置")
			if !canSave {
				btn.Disabled(true)
			}
			if btn.Clicked() && canSave {
				st.ClaudeCfg.SaveArm = true
			}
		})
		ui.Text(c, "分层管理 Claude Code 自身设置；接入投影键只读").TextColor(t.TextMuted)
		if !st.ClaudeCfg.Loaded {
			uikit.Feedback(c, t, uikit.InferFeedback(st.ClaudeCfg.Msg), st.ClaudeCfg.Msg)
			return
		}
		fileStrip(c, t, s, st)
		layerSelector(c, t, s, st, cfgs)
		if st.ClaudeCfg.Warning != "" {
			uikit.Feedback(c, t, uikit.FeedbackWarning, st.ClaudeCfg.Warning)
		}
		// 结果条与备份面板放在内容区顶部：保存后无需滚到表单底部。
		saveResultBar(c, t, s, st, cfgs)
		backupPanel(c, t, s, st, cfgs)
		viewTabs(c, t, s, st, cfgs)
		switch st.ClaudeCfg.View {
		case "json":
			uikit.Card(c, t, func() { jsonView(c, t, st) })
		case "merged":
			uikit.Card(c, t, func() { mergedView(c, t, st) })
		default:
			if st.ClaudeCfg.BrokenFile {
				repairCard(c, t, s, st, cfgs)
			} else {
				FormSection(c, st)
			}
		}
		uikit.Feedback(c, t, uikit.InferFeedback(st.ClaudeCfg.Msg), st.ClaudeCfg.Msg)
	})
	saveDialog(c, st, cfgs)
	conflictDialog(c, st, cfgs)
	restoreBackupDialog(c, st, cfgs)
	reloadDialog(c, st)
	rebuildDialog(c, st, cfgs)
}

// fileStrip 是当前文件条（原型 §4.1）：浅底条内等宽路径 + 状态徽章（有未
// 保存修改 / JSON 错误 / 只读 / 文件不存在 / 已保存）；JSON 损坏原文与保存
// 阻断原因紧随其下。
func fileStrip(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State) {
	cf := &st.ClaudeCfg
	ui.Box(c).Radius(10).Background(t.SurfaceHover).Padding(8, 10).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, cf.Display).Font(uikit.MonoFont).TextColor(t.TextMuted).Grow(1)
			switch {
			case cf.BrokenFile || cf.ParseErr != "":
				uikit.SoftBadge(c, t, s, "danger", "JSON 错误")
			case cf.Layer == "managed":
				uikit.SoftBadge(c, t, s, "gray", "只读")
			case cf.Dirty:
				uikit.SoftBadge(c, t, s, "warn", "有未保存修改")
			case !cf.Exists:
				uikit.SoftBadge(c, t, s, "gray", "文件不存在")
			default:
				uikit.SoftBadge(c, t, s, "ok", "已保存")
			}
		})
	})
	if cf.ParseErr != "" {
		uikit.Feedback(c, t, uikit.FeedbackError, cf.ParseErr)
	}
	if cf.Loaded && cf.Dirty {
		if reason := saveBlockReason(cf); reason != "" {
			uikit.Feedback(c, t, uikit.FeedbackError, reason)
		}
	}
}

// repairCard 是损坏 JSON 的只读修复卡（I5）：表单停用说明 + 备份恢复入口 +
// 「用空配置重建」（托管层只读，不提供重建）。
func repairCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	uikit.Feedback(c, t, uikit.FeedbackError,
		"文件不是合法 JSON，表单已停用：可切到 JSON 视图查看原文，从备份恢复，或用空配置重建（损坏内容会先备份）。")
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if uikit.SoftButton(c, t, s, "gray", "备份与恢复").Clicked() {
			openBackups(st, cfgs)
		}
		if cf.Layer != "managed" {
			if uikit.SoftButton(c, t, s, "danger", "用空配置重建").Clicked() {
				cf.RebuildArm = true
			}
		}
	})
}

// rebuildDialog 是「用空配置重建」的二次危险确认；确认后以空文档覆盖损坏
// 文件（bridge 先备份损坏内容），并重载当前层。
func rebuildDialog(c *ui.Context, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	if !cf.RebuildArm {
		return
	}
	t := c.Theme()
	uikit.RichDialog(c, t, uikit.RichDialogOpts{
		Open:     &cf.RebuildArm,
		Title:    "用空配置重建？",
		Subtitle: cf.File,
		Width:    520,
		Body: func() {
			ui.Text(c, "当前文件不是合法 JSON，无法按键合并保存。重建将以空对象 {} 覆盖整个文件；损坏的原文会先自动备份一份，可从「备份与恢复」找回。").TextColor(t.TextMuted)
		},
		Actions: []uikit.RichDialogAction{
			{Label: "取消", Default: true},
			{Label: "重建", Danger: true, OnClick: func() { rebuildBrokenFile(st, cfgs) }},
		},
	})
}

// rebuildBrokenFile 以空文档覆盖损坏文件（走 bridge 的 Rebuild 保存路径）。
func rebuildBrokenFile(st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	res, err := cfgs.SaveClaudeConfigLayer(bridge.SaveClaudeConfigRequest{
		Kind:         scopeOf(cf.Layer, cf.Sub),
		Project:      cf.Project,
		Sub:          cf.Sub,
		Document:     map[string]any{},
		ExpectedHash: cf.Hash,
		Create:       !cf.Exists,
		Rebuild:      true,
		Changed:      []string{"(rebuild)"},
	})
	if err != nil {
		cf.Msg = err.Error()
		return
	}
	applySaveResult(st, cfgs, res, "已重建")
}

// guardReload 是设置区统一的未保存修改守卫（I1）：干净时直接执行动作；
// 有修改时武装「放弃未保存修改」确认框，确认后才执行（刷新/切层/切项目/
// 切共享-本地共用）。
func guardReload(st *state.State, action func()) {
	cf := &st.ClaudeCfg
	if !cf.Dirty {
		action()
		return
	}
	cf.PendingReload = action
	cf.ReloadArm = true
}

// switchLayer 切层/切项目/切子层：经脏守卫后应用变更并重新加载。
func switchLayer(st *state.State, cfgs bridge.ClaudeConfigs, apply func(cf *state.ClaudeCfgState)) {
	guardReload(st, func() {
		apply(&st.ClaudeCfg)
		st.ClaudeCfg.Doc = nil
		st.ClaudeCfg.Loaded = false
		load(st, cfgs)
	})
}

// reloadDialog 是「放弃未保存修改」确认框：继续编辑为默认焦点，放弃修改为
// 危险动作居末位。
func reloadDialog(c *ui.Context, st *state.State) {
	cf := &st.ClaudeCfg
	if !cf.ReloadArm {
		return
	}
	t := c.Theme()
	uikit.RichDialog(c, t, uikit.RichDialogOpts{
		Open:     &cf.ReloadArm,
		Title:    "放弃未保存的修改？",
		Subtitle: cf.Display,
		Width:    480,
		Body: func() {
			ui.Text(c, "当前层有未保存的修改；重新加载或切换将丢弃这些修改（可先点「保存设置」）。").TextColor(t.TextMuted)
		},
		Actions: []uikit.RichDialogAction{
			{Label: "继续编辑", Default: true},
			{Label: "放弃修改", Danger: true, OnClick: func() {
				action := cf.PendingReload
				cf.PendingReload = nil
				if action != nil {
					action()
				}
			}},
		},
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
	cf.ParseErr = ""
	cf.BrokenFile = false
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
	if view.ParseError != "" {
		// 只读修复态（I5）：JSON 视图显示磁盘原文，表单停用，保存阻断。
		cf.ParseErr = view.ParseError
		cf.BrokenFile = true
		cf.JSONText = view.RawJSON
	} else {
		cf.JSONText = pretty(view.Doc)
	}
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

func layerSelector(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	idx := 0
	if cf.Layer == "project" {
		idx = 1
	} else if cf.Layer == "managed" {
		idx = 2
	}
	// 层级切换用 Segmented（原型 §4.1：等宽三段）；语义同旧 Tabs——变更走
	// switchLayer 的脏守卫。
	if ui.Segmented(c, &idx, "用户级", "项目级", "企业托管").Changed() {
		layer := "user"
		if idx == 1 {
			layer = "project"
		} else if idx == 2 {
			layer = "managed"
		}
		switchLayer(st, cfgs, func(cf *state.ClaudeCfgState) { cf.Layer = layer })
	}
	if cf.Layer == "project" {
		projectContextCard(c, t, s, st, cfgs)
	}
	if cf.Layer == "managed" {
		managedContextCard(c, t, s, st)
	}
}

// projectContextCard 是项目级上下文卡（原型 §4.1）：项目选择器 + 手动目录 +
// 共享/本地分段 + 实际解析路径条。
func projectContextCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	uikit.Card(c, t, func() {
		ui.Column(c).Gap(8).Children(func() {
			paths := make([]string, 0, len(cf.Projects))
			for _, p := range cf.Projects {
				paths = append(paths, p.Path)
			}
			if len(paths) > 0 {
				project := cf.Project
				if ui.Select(c, &project, paths).Grow(1).Changed() && project != cf.Project {
					switchLayer(st, cfgs, func(cf *state.ClaudeCfgState) { cf.Project = project })
				}
			} else {
				ui.Text(c, "未读取到最近项目；可手动输入项目目录").TextColor(t.TextMuted)
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &cf.ProjectInput).Grow(1).Placeholder("/path/to/project")
				if uikit.SoftButton(c, t, s, "gray", "打开").Clicked() && strings.TrimSpace(cf.ProjectInput) != "" && cf.ProjectInput != cf.Project {
					target := strings.TrimSpace(cf.ProjectInput)
					switchLayer(st, cfgs, func(cf *state.ClaudeCfgState) { cf.Project = target })
				}
			})
			sub := 0
			if cf.Sub == "local" {
				sub = 1
			}
			if ui.Segmented(c, &sub, "共享 settings.json", "本地 settings.local.json").Changed() {
				next := "shared"
				if sub == 1 {
					next = "local"
				}
				switchLayer(st, cfgs, func(cf *state.ClaudeCfgState) { cf.Sub = next })
			}
			if cf.File != "" {
				ui.Box(c).Radius(8).Background(t.SurfaceHover).Padding(7, 10).Children(func() {
					ui.Text(c, cf.File).Font(uikit.MonoFont).FontSize(t.FontSize - 1).
						TextColor(t.TextMuted)
				})
			}
		})
	})
}

// managedContextCard 是托管层上下文卡：托管策略文件来源（bridge 目前只解析
// managed-settings.json + drop-in 合并；MDM/远程来源不可探测，不假装可见）
// + 只读说明。violations 展示沿用 Violations 反馈（Section 顶部 Warning）。
func managedContextCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State) {
	cf := &st.ClaudeCfg
	uikit.Card(c, t, func() {
		ui.Column(c).Gap(6).Children(func() {
			ui.Text(c, "托管策略来源").Bold()
			ui.Box(c).Radius(8).Background(t.SurfaceHover).Padding(7, 10).Children(func() {
				ui.Text(c, cf.File).Font(uikit.MonoFont).FontSize(t.FontSize - 1).
					TextColor(t.TextMuted)
			})
			ui.Text(c, "系统托管策略文件（managed-settings.json 及 managed-settings.d 合并）；MDM/远程策略来源不可见，预览可能不完整").
				FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			ui.Text(c, "托管配置只读，本页不写入").TextColor(t.TextMuted)
		})
	})
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
			if cf.View != "json" && !cf.BrokenFile {
				// 从表单切回 JSON 时按当前 Doc 重建文本，避免显示旧快照。
				// 损坏文件例外：JSON 视图必须保留磁盘原文（I5）。
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

// cloneDoc 深拷贝文档（递归 map/slice），标量原样保留——尤其 json.Number。
// 不能走 json.Marshal 往返：数字会统一变 float64，与解析基线
// （json.Number）深比较恒不等，导致含数字的文件加载即脏（C1 回归）。
func cloneDoc(doc map[string]any) map[string]any {
	if doc == nil {
		return map[string]any{}
	}
	out := make(map[string]any, len(doc))
	for k, v := range doc {
		out[k] = cloneDocValue(v)
	}
	return out
}

func cloneDocValue(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneDoc(x)
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = cloneDocValue(item)
		}
		return out
	default:
		return v
	}
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
