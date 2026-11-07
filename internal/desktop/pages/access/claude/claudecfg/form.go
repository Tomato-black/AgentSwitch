package claudecfg

import (
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// FormSection 渲染设置区「表单」视图的七组结构化表单：模型、环境变量、
// 权限、状态栏、MCP/插件、Skills、记忆。它只修改 st.ClaudeCfg.Doc，绝不
// 直接写盘；保存由 Section 顶部的「保存设置」经 bridge 完成。
//
// 当前层缺失的键不复制继承值：显示「继承自下层」+ 来源 +「覆盖」，点覆盖
// 只在 Overrides 记录编辑意图，控件从空白开始，首次输入才写入当前文档。
func FormSection(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	cf := &st.ClaudeCfg
	ensureFormState(cf)

	modelGroup(c, t, s, cf)
	envGroup(c, t, s, cf)
	permissionGroup(c, t, s, cf)
	statusLineGroup(c, t, s, cf)
	mcpGroup(c, t, s, cf)
	SkillsCard(c, st)
	memoryGroup(c, t, s, cf)

	if cf.FormMsg != "" {
		uikit.Feedback(c, t, uikit.FeedbackWarning, cf.FormMsg)
	}
}

func ensureFormState(cf *state.ClaudeCfgState) {
	if cf.Doc == nil {
		cf.Doc = map[string]any{}
	}
	if cf.Baseline == nil {
		cf.Baseline = map[string]any{}
	}
	if cf.Overrides == nil {
		cf.Overrides = map[string]bool{}
	}
	if cf.Reveal == nil {
		cf.Reveal = map[string]bool{}
	}
	if cf.SkillStates == nil {
		cf.SkillStates = map[string]string{}
	}
}

// RecomputeDirty 按 Doc 与 Baseline 的深比较更新脏标记。所有表单编辑在
// 写入 Doc 后必须调用它。
func RecomputeDirty(cf *state.ClaudeCfgState) {
	cf.Dirty = !reflect.DeepEqual(cf.Doc, cf.Baseline)
	// 成功编辑后清除上一次的字段校验提示；非法输入路径不调用本函数。
	cf.FormMsg = ""
	// 保存结果条持续到下一次编辑：一旦重新变脏就收起，避免旧备份路径
	// 与当前未保存内容并列误导用户。
	if cf.Dirty {
		cf.ResultVisible = false
	}
}

// formText 是各分组的小标题（现有设置区卡片内的次级标题）。
func formText(c *ui.Context, t *ui.Theme, title, hint string) {
	ui.Column(c).Gap(3).Children(func() {
		ui.Text(c, title).Bold()
		if hint != "" {
			ui.Text(c, hint).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		}
	})
}

// ---------------------------------------------------------------------------
// 1. 模型与响应
// ---------------------------------------------------------------------------

func modelGroup(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState) {
	formText(c, t, "模型与响应", "主模型、回退模型、推理强度与常驻思考")
	stringField(c, t, s, cf, "model", "主模型", "例如 my-code / sonnet / opus")
	listField(c, t, s, cf, "fallbackModel", "回退模型", "逗号分隔，按顺序尝试")
	enumField(c, t, s, cf, "effortLevel", "推理强度",
		[]string{"low", "medium", "high", "xhigh"}, "继承（未设置）")
	boolField(c, t, s, cf, "alwaysThinkingEnabled", "始终开启思考", "")
}

// ---------------------------------------------------------------------------
// 2. 环境变量
// ---------------------------------------------------------------------------

var coreManagedEnv = map[string]bool{
	"ANTHROPIC_BASE_URL":   true,
	"ANTHROPIC_AUTH_TOKEN": true,
	"ANTHROPIC_MODEL":      true,
}

func envGroup(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState) {
	formText(c, t, "环境变量", "接入管理的 13 个键只读；其余变量可自由增删")

	managed := map[string]bool{}
	for _, k := range corecfg.AccessManagedEnv {
		managed[k] = true
	}
	for _, k := range corecfg.AccessManagedEnv {
		if !coreManagedEnv[k] && !cf.EnvExpanded {
			continue
		}
		managedEnvRow(c, t, s, cf, k)
	}
	label := "展开其余 10 个接入投影键"
	if cf.EnvExpanded {
		label = "收起接入投影键"
	}
	if uikit.SoftButton(c, t, s, "gray", label).Clicked() {
		cf.EnvExpanded = !cf.EnvExpanded
	}

	keys := customEnvKeys(cf, managed)
	if len(keys) == 0 {
		ui.Text(c, "暂无自定义环境变量").TextColor(t.TextMuted)
	}
	for _, k := range keys {
		customEnvRow(c, t, s, cf, k)
	}

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c, &cf.EnvNewKey).Width(260).Placeholder("KEY")
		ui.TextInput(c, &cf.EnvNewValue).Width(320).Placeholder("值")
		if uikit.SoftButton(c, t, s, "act", "添加变量").Clicked() {
			addEnvKey(cf, managed)
		}
	})
}

func managedEnvRow(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, key string) {
	path := "env." + key
	value, current := envCurrentValue(cf, key)
	entry, inherited := cf.MergeEntries[path]

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, key).Font(uikit.MonoFont).Width(310)
		switch {
		case current:
			ui.Text(c, envDisplay(path, value)).Font(uikit.MonoFont).Grow(1)
		case inherited:
			ui.Text(c, "继承自下层").TextColor(t.TextMuted)
			if src := sourceLabel(entry.Source); src != "" {
				ui.Text(c, "来源："+src).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			}
			ui.Text(c, envDisplay(path, stringValue(entry.Value))).Font(uikit.MonoFont).Grow(1)
		default:
			ui.Text(c, "未设置").TextColor(t.TextMuted).Grow(1)
		}
		uikit.SoftBadge(c, t, s, "gray", "锁定")
		ui.Text(c, "由 AgentSwitch 接入管理").FontSize(t.FontSize - 2).TextColor(t.TextMuted)
	})
}

func customEnvKeys(cf *state.ClaudeCfgState, managed map[string]bool) []string {
	m := envMap(cf.Doc)
	keys := make([]string, 0, len(m))
	for k := range m {
		if managed[k] {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func customEnvRow(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, key string) {
	path := "env." + key
	value, _ := envCurrentValue(cf, key)
	sensitive := corecfg.IsSensitivePath(path)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, key).Font(uikit.MonoFont).Width(310)
		if isWeakManagedEnv(cf, path) {
			uikit.SoftBadge(c, t, s, "warn", "接入应用可能覆盖")
		}
		if sensitive && !cf.Reveal[key] {
			ui.Text(c, corecfg.MaskSecret(value)).Font(uikit.MonoFont).Grow(1)
			if uikit.SoftButton(c, t, s, "gray", "显示").Clicked() {
				cf.Reveal[key] = true
			}
		} else {
			cur := value
			in := ui.TextInput(c, &cur).Grow(1)
			if in.Changed() {
				setEnvKey(cf, key, cur)
			}
			if sensitive {
				if uikit.SoftButton(c, t, s, "gray", "隐藏").Clicked() {
					delete(cf.Reveal, key)
				}
			}
		}
		if uikit.SoftButton(c, t, s, "danger", "移除").Clicked() {
			setEnvKey(cf, key, "")
		}
	})
}

func envMap(doc map[string]any) map[string]any {
	m, _ := doc["env"].(map[string]any)
	return m
}

func envCurrentValue(cf *state.ClaudeCfgState, key string) (string, bool) {
	m := envMap(cf.Doc)
	if m == nil {
		return "", false
	}
	v, ok := m[key]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

func envDisplay(path, value string) string {
	if corecfg.IsSensitivePath(path) {
		return corecfg.MaskSecret(value)
	}
	return value
}

func setEnvKey(cf *state.ClaudeCfgState, key, value string) {
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	m := envMap(cf.Doc)
	if m == nil {
		m = map[string]any{}
		cf.Doc["env"] = m
	}
	if strings.TrimSpace(value) == "" {
		delete(m, key)
	} else {
		m[key] = value
	}
	if len(m) == 0 {
		delete(cf.Doc, "env")
	}
	RecomputeDirty(cf)
}

func addEnvKey(cf *state.ClaudeCfgState, managed map[string]bool) {
	key := strings.TrimSpace(cf.EnvNewKey)
	if key == "" {
		cf.FormMsg = "环境变量名不能为空"
		return
	}
	if strings.ContainsAny(key, " =\t\n") {
		cf.FormMsg = "环境变量名不能包含空格或等号"
		return
	}
	if managed[key] {
		cf.FormMsg = "受管键由接入管理，不能手动添加"
		return
	}
	setEnvKey(cf, key, cf.EnvNewValue)
	cf.EnvNewKey = ""
	cf.EnvNewValue = ""
}

// ---------------------------------------------------------------------------
// 3. 权限
// ---------------------------------------------------------------------------

func permissionGroup(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState) {
	formText(c, t, "权限", "默认模式、规则列表与危险模式确认")
	enumField(c, t, s, cf, "permissions.defaultMode", "默认模式",
		[]string{"default", "acceptEdits", "plan", "auto", "bypassPermissions"}, "继承（未设置）")
	listField(c, t, s, cf, "permissions.allow", "允许规则", "逗号分隔，例如 Bash(go test:*)")
	listField(c, t, s, cf, "permissions.ask", "询问规则", "逗号分隔")
	listField(c, t, s, cf, "permissions.deny", "拒绝规则", "逗号分隔")
	listField(c, t, s, cf, "permissions.additionalDirectories", "附加目录", "逗号分隔")
	dangerRow(c, t, s, cf)
}

func dangerRow(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState) {
	const path = "skipDangerousModePermissionPrompt"
	disabled, reason := fieldBlocked(cf, path)
	if disabled {
		uikit.FormRowRaw(c, t, "危险模式确认", func() {
			ui.Text(c, effectiveText(cf, path)).Font(uikit.MonoFont)
			ui.Text(c, reason).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		})
		return
	}
	choice := dangerChoice(cf)
	var changed bool
	uikit.FormRow(c, t, "危险模式确认", func() ui.Element {
		sel := ui.Select(c, &choice, dangerOptions)
		changed = sel.Changed()
		return sel
	})
	if changed {
		setDangerChoice(cf, choice)
	}
	ui.Text(c, "合并规则：任一启用层为 true 即跳过确认（OR）").FontSize(t.FontSize - 2).TextColor(t.TextMuted)
	if _, ok := corecfg.Get(cf.Doc, path); !ok && cf.Layer == "user" {
		ui.Text(c, "用户级默认：跳过确认（true）").FontSize(t.FontSize - 2).TextColor(t.TextMuted)
	}
	if entry, ok := cf.MergeEntries[path]; ok {
		if b, ok := entry.Value.(bool); ok {
			effective := dangerShow
			if b {
				effective = dangerSkip
			}
			ui.Text(c, "当前有效："+effective).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		}
	}
}

const (
	dangerInherit = "继承（未设置）"
	dangerSkip    = "跳过确认"
	dangerShow    = "显示确认"
)

var dangerOptions = []string{dangerInherit, dangerSkip, dangerShow}

func dangerChoice(cf *state.ClaudeCfgState) string {
	v, ok := corecfg.Get(cf.Doc, "skipDangerousModePermissionPrompt")
	if !ok {
		return dangerInherit
	}
	b, _ := v.(bool)
	if b {
		return dangerSkip
	}
	return dangerShow
}

func setDangerChoice(cf *state.ClaudeCfgState, choice string) {
	switch choice {
	case dangerSkip:
		corecfg.Set(cf.Doc, "skipDangerousModePermissionPrompt", true)
	case dangerShow:
		corecfg.Set(cf.Doc, "skipDangerousModePermissionPrompt", false)
	default:
		corecfg.Delete(cf.Doc, "skipDangerousModePermissionPrompt")
	}
	RecomputeDirty(cf)
}

// ---------------------------------------------------------------------------
// 4. 状态栏
// ---------------------------------------------------------------------------

func statusLineGroup(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState) {
	formText(c, t, "状态栏", "statusLine 对象只写入当前层；字段留空即移除")
	const path = "statusLine"
	disabled, reason := fieldBlocked(cf, path)
	if disabled {
		uikit.FormRowRaw(c, t, "状态栏", func() {
			ui.Text(c, statusLineText(cf)).Font(uikit.MonoFont)
			ui.Text(c, reason).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		})
		return
	}
	if _, ok := corecfg.Get(cf.Doc, path); !ok && !cf.Overrides[path] {
		if inheritedRow(c, t, s, cf, path) {
			// 「覆盖」后本帧继续渲染空白控件。
		} else {
			return
		}
	}
	statusLineInput(c, t, cf, "type", "类型", "command / static")
	statusLineInput(c, t, cf, "command", "命令", "例如 ~/.claude/statusline.sh")
	statusLineInput(c, t, cf, "text", "静态文本", "type=static 时显示")
	statusLineInput(c, t, cf, "padding", "内边距", "整数，例如 0")
}

func statusLineInput(c *ui.Context, t *ui.Theme, cf *state.ClaudeCfgState, key, label, placeholder string) {
	cur := statusLineValue(cf, key)
	uikit.FormRow(c, t, label, func() ui.Element {
		in := ui.TextInput(c, &cur).Placeholder(placeholder)
		if in.Changed() {
			setStatusLineField(cf, key, cur)
		}
		return in
	})
}

func statusLineValue(cf *state.ClaudeCfgState, key string) string {
	obj, _ := cf.Doc["statusLine"].(map[string]any)
	if obj == nil {
		return ""
	}
	switch v := obj[key].(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	}
	return ""
}

func setStatusLineField(cf *state.ClaudeCfgState, key, value string) {
	obj, _ := cf.Doc["statusLine"].(map[string]any)
	if obj == nil {
		obj = map[string]any{}
		cf.Doc["statusLine"] = obj
	}
	value = strings.TrimSpace(value)
	switch {
	case value == "":
		delete(obj, key)
	case key == "padding":
		n, err := strconv.Atoi(value)
		if err != nil {
			cf.FormMsg = "statusLine.padding 需要整数"
			return
		}
		obj[key] = n
	default:
		obj[key] = value
	}
	if len(obj) == 0 {
		delete(cf.Doc, "statusLine")
	}
	RecomputeDirty(cf)
}

func statusLineText(cf *state.ClaudeCfgState) string {
	v, ok := corecfg.Get(cf.Doc, "statusLine")
	if !ok {
		if entry, ok := cf.MergeEntries["statusLine"]; ok {
			return "继承自下层（" + sourceLabel(entry.Source) + "）：" + displayValue("statusLine", entry.Value)
		}
		return "未设置"
	}
	return displayValue("statusLine", v)
}

// ---------------------------------------------------------------------------
// 5. MCP 与插件
// ---------------------------------------------------------------------------

func mcpGroup(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState) {
	formText(c, t, "MCP 与插件", "项目 MCP 开关、插件启用与 Claude.ai 同步")
	boolField(c, t, s, cf, "enableAllProjectMcpServers", "启用全部项目 MCP 服务器（enableAllProjectMcpServers）", "")
	listField(c, t, s, cf, "enabledMcpjsonServers", "允许的 MCP 服务器", "逗号分隔")
	listField(c, t, s, cf, "disabledMcpjsonServers", "禁用的 MCP 服务器", "逗号分隔")
	boolField(c, t, s, cf, "syncClaudeAiPlugins", "同步 Claude.ai 插件", "")
	boolField(c, t, s, cf, "syncClaudeAiSkills", "同步 Claude.ai Skills", "")

	ui.Text(c, "已启用插件").TextColor(t.TextMuted)
	plugins := pluginIDs(cf)
	if len(plugins) == 0 {
		ui.Text(c, "未配置插件").TextColor(t.TextMuted)
	}
	for _, id := range plugins {
		cur := pluginEnabled(cf, id)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, id).Font(uikit.MonoFont).Grow(1)
			// 继承值也显示为勾选；用户切换时才写入当前层。
			cb := ui.Checkbox(c, &cur, "")
			if cb.Changed() {
				setPluginEnabled(cf, id, cur)
			}
		})
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c, &cf.PluginNew).Width(360).Placeholder("plugin@marketplace")
		if uikit.SoftButton(c, t, s, "gray", "启用插件").Clicked() {
			id := strings.TrimSpace(cf.PluginNew)
			if id == "" {
				cf.FormMsg = "插件 id 不能为空"
			} else {
				setPluginEnabled(cf, id, true)
				cf.PluginNew = ""
			}
		}
	})
}

func pluginIDs(cf *state.ClaudeCfgState) []string {
	set := map[string]bool{}
	if m, ok := cf.Doc["enabledPlugins"].(map[string]any); ok {
		for id := range m {
			set[id] = true
		}
	}
	for path := range cf.MergeEntries {
		if id, ok := strings.CutPrefix(path, "enabledPlugins."); ok {
			set[id] = true
		}
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func pluginEnabled(cf *state.ClaudeCfgState, id string) bool {
	if m, ok := cf.Doc["enabledPlugins"].(map[string]any); ok {
		if v, ok := m[id].(bool); ok {
			return v
		}
	}
	if entry, ok := cf.MergeEntries["enabledPlugins."+id]; ok {
		if b, ok := entry.Value.(bool); ok {
			return b
		}
	}
	return false
}

func setPluginEnabled(cf *state.ClaudeCfgState, id string, enabled bool) {
	m, _ := cf.Doc["enabledPlugins"].(map[string]any)
	if m == nil {
		m = map[string]any{}
		cf.Doc["enabledPlugins"] = m
	}
	m[id] = enabled
	RecomputeDirty(cf)
}

// ---------------------------------------------------------------------------
// 6. Skills（见 skills.go）
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// 7. 记忆与上下文
// ---------------------------------------------------------------------------

func memoryGroup(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState) {
	formText(c, t, "记忆与上下文", "自动压缩、自动记忆与清理周期")
	boolField(c, t, s, cf, "autoCompactEnabled", "启用自动压缩", "")
	boolField(c, t, s, cf, "autoMemoryEnabled", "启用自动记忆", "")
	intField(c, t, s, cf, "cleanupPeriodDays", "清理周期（天）", "默认 30")
}

// ---------------------------------------------------------------------------
// 通用字段渲染
// ---------------------------------------------------------------------------

func stringField(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, path, label, placeholder string) {
	disabled, reason := fieldBlocked(cf, path)
	if disabled {
		uikit.FormRowRaw(c, t, label, func() {
			ui.Text(c, effectiveText(cf, path)).Font(uikit.MonoFont)
			ui.Text(c, reason).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		})
		return
	}
	if _, ok := corecfg.Get(cf.Doc, path); !ok && !cf.Overrides[path] {
		if !inheritedRow(c, t, s, cf, path) {
			return
		}
	}
	cur := stringAt(cf.Doc, path)
	uikit.FormRow(c, t, label, func() ui.Element {
		in := ui.TextInput(c, &cur).Placeholder(placeholder)
		if in.Changed() {
			setString(cf.Doc, path, cur)
			RecomputeDirty(cf)
		}
		return in
	})
}

func boolField(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, path, label, hint string) {
	disabled, reason := fieldBlocked(cf, path)
	if disabled {
		uikit.FormRowRaw(c, t, label, func() {
			ui.Text(c, effectiveText(cf, path)).Font(uikit.MonoFont)
			ui.Text(c, reason).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		})
		return
	}
	if _, ok := corecfg.Get(cf.Doc, path); !ok && !cf.Overrides[path] {
		if !inheritedRow(c, t, s, cf, path) {
			return
		}
	}
	cur := boolAt(cf.Doc, path)
	uikit.FormRow(c, t, label, func() ui.Element {
		cb := ui.Checkbox(c, &cur, "")
		if cb.Changed() {
			corecfg.Set(cf.Doc, path, cur)
			RecomputeDirty(cf)
		}
		return cb
	})
	if hint != "" {
		ui.Text(c, hint).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
	}
}

func enumField(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, path, label string, values []string, inheritLabel string) {
	disabled, reason := fieldBlocked(cf, path)
	if disabled {
		uikit.FormRowRaw(c, t, label, func() {
			ui.Text(c, effectiveText(cf, path)).Font(uikit.MonoFont)
			ui.Text(c, reason).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		})
		return
	}
	if _, ok := corecfg.Get(cf.Doc, path); !ok && !cf.Overrides[path] {
		if !inheritedRow(c, t, s, cf, path) {
			return
		}
	}
	cur := stringAt(cf.Doc, path)
	if cur == "" {
		cur = inheritLabel
	}
	options := append([]string{inheritLabel}, values...)
	var changed bool
	uikit.FormRow(c, t, label, func() ui.Element {
		sel := ui.Select(c, &cur, options)
		changed = sel.Changed()
		return sel
	})
	if changed {
		if cur == inheritLabel {
			corecfg.Delete(cf.Doc, path)
		} else {
			corecfg.Set(cf.Doc, path, cur)
		}
		RecomputeDirty(cf)
	}
}

func listField(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, path, label, placeholder string) {
	disabled, reason := fieldBlocked(cf, path)
	if disabled {
		uikit.FormRowRaw(c, t, label, func() {
			ui.Text(c, effectiveText(cf, path)).Font(uikit.MonoFont)
			ui.Text(c, reason).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		})
		return
	}
	if _, ok := corecfg.Get(cf.Doc, path); !ok && !cf.Overrides[path] {
		if !inheritedRow(c, t, s, cf, path) {
			return
		}
	}
	cur := listText(cf.Doc, path)
	uikit.FormRow(c, t, label, func() ui.Element {
		in := ui.TextInput(c, &cur).Placeholder(placeholder)
		if in.Changed() {
			setListField(cf, path, cur)
		}
		return in
	})
}

func intField(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, path, label, hint string) {
	disabled, reason := fieldBlocked(cf, path)
	if disabled {
		uikit.FormRowRaw(c, t, label, func() {
			ui.Text(c, effectiveText(cf, path)).Font(uikit.MonoFont)
			ui.Text(c, reason).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		})
		return
	}
	if _, ok := corecfg.Get(cf.Doc, path); !ok && !cf.Overrides[path] {
		if !inheritedRow(c, t, s, cf, path) {
			if hint != "" {
				ui.Text(c, hint).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			}
			return
		}
	}
	cur := intText(cf.Doc, path)
	uikit.FormRow(c, t, label, func() ui.Element {
		in := ui.TextInput(c, &cur).Placeholder(hint)
		if in.Changed() {
			setIntField(cf, path, cur)
		}
		return in
	})
}

func inheritedRow(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, path string) bool {
	entry, ok := cf.MergeEntries[path]
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ok {
			ui.Text(c, "继承自下层").TextColor(t.TextMuted)
			ui.Text(c, "来源："+sourceLabel(entry.Source)).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			ui.Text(c, displayValue(path, entry.Value)).Font(uikit.MonoFont).Grow(1)
		} else {
			ui.Text(c, "当前层未设置").TextColor(t.TextMuted).Grow(1)
		}
		if uikit.SoftButton(c, t, s, "gray", "覆盖").Clicked() {
			cf.Overrides[path] = true
			return
		}
	})
	return cf.Overrides[path]
}

func effectiveText(cf *state.ClaudeCfgState, path string) string {
	if v, ok := corecfg.Get(cf.Doc, path); ok {
		return displayValue(path, v)
	}
	if entry, ok := cf.MergeEntries[path]; ok {
		return "继承自下层（" + sourceLabel(entry.Source) + "）：" + displayValue(path, entry.Value)
	}
	return "未设置"
}

func fieldBlocked(cf *state.ClaudeCfgState, path string) (bool, string) {
	if cf.Layer == "managed" {
		return true, "企业托管层只读"
	}
	if path == "skipDangerousModePermissionPrompt" && cf.Layer == "project" && cf.Sub == "shared" {
		return true, "项目共享不能设置危险模式确认；请切到用户级或项目本地"
	}
	for _, k := range corecfg.NoProjectShared {
		if k == path && cf.Layer == "project" && cf.Sub == "shared" {
			return true, "项目共享不能设置该键；请切到用户级或项目本地"
		}
	}
	// 接入投影的强受管顶层键（含子路径）任何层都只读（I4）。
	for _, k := range cf.Managed.StrongTopLevel {
		if path == k || strings.HasPrefix(path, k+".") {
			return true, "接入投影键由「接入」管理，请到接入页修改"
		}
	}
	spec, ok := corecfg.Lookup(path)
	if !ok {
		return false, ""
	}
	if !spec.Scopes.Has(scopeOf(cf.Layer, cf.Sub)) {
		return true, "该键不支持写入当前层级"
	}
	return false, ""
}

// isWeakManagedEnv 报告 path（形如 env.KEY）是否弱受管：可编辑，但接入
// 「应用」后会覆盖该值。
func isWeakManagedEnv(cf *state.ClaudeCfgState, path string) bool {
	for _, k := range cf.Managed.WeakEnv {
		if k == path {
			return true
		}
	}
	return false
}

func sourceLabel(k corecfg.ScopeKind) string {
	switch k {
	case corecfg.ScopeUser:
		return "用户级"
	case corecfg.ScopeProject:
		return "项目级"
	case corecfg.ScopeLocal:
		return "项目本地"
	case corecfg.ScopeManaged:
		return "企业托管"
	}
	return string(k)
}

func displayValue(path string, v any) string {
	switch x := v.(type) {
	case nil:
		return "（空）"
	case string:
		if corecfg.IsSensitivePath(path) {
			return corecfg.MaskSecret(x)
		}
		return x
	case bool:
		return boolLabel(x)
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case []any:
		parts := make([]string, 0, len(x))
		for _, item := range x {
			parts = append(parts, stringValue(item))
		}
		return strings.Join(parts, ", ")
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return displayValue("", v)
}

func boolAt(doc map[string]any, path string) bool {
	v, ok := corecfg.Get(doc, path)
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

func listText(doc map[string]any, path string) string {
	v, ok := corecfg.Get(doc, path)
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

// setListField 把逗号分隔文本写入字符串数组；空文本删除该键。
func setListField(cf *state.ClaudeCfgState, path, raw string) {
	out := make([]any, 0)
	for _, p := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		corecfg.Delete(cf.Doc, path)
	} else {
		corecfg.Set(cf.Doc, path, out)
	}
	RecomputeDirty(cf)
}

func intText(doc map[string]any, path string) string {
	v, ok := corecfg.Get(doc, path)
	if !ok {
		return ""
	}
	switch x := v.(type) {
	case json.Number:
		return x.String()
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	}
	return ""
}

// setIntField 写入整数；空文本删除，非法文本只提示不改文档。
func setIntField(cf *state.ClaudeCfgState, path, raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		corecfg.Delete(cf.Doc, path)
		RecomputeDirty(cf)
		return
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		cf.FormMsg = path + " 需要整数"
		return
	}
	corecfg.Set(cf.Doc, path, n)
	RecomputeDirty(cf)
}
