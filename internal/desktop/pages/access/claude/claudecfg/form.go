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
// 当前层缺失的键不复制继承值：直接渲染空控件 + 「下层：」继承提示行，
// 首次输入才写入当前文档（2026-11-07 去「覆盖」门槛）。
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

// groupCard 渲染表单一组的独立卡片（原型 2026-11-07 §4.1 卡片流）：
// Card + 区块标题/说明，body 是组内字段。取代旧的单卡内 formText 小标题。
func groupCard(c *ui.Context, t *ui.Theme, title, hint string, body func()) {
	uikit.Card(c, t, func() {
		ui.Column(c).Gap(8).Children(func() {
			uikit.SectionTitle(c, t, title, hint)
			body()
		})
	})
}

// ---------------------------------------------------------------------------
// 1. 模型与响应
// ---------------------------------------------------------------------------

// modelGroup 只保留推理强度与思考开关——主模型/回退模型归接入抽屉设置
// （2026-11-07 用户裁定「接入只配置模型和网关」；写入接入文档顶层键经投影
// 进 settings.json，且被受管集合自动只读）。
func modelGroup(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState) {
	groupCard(c, t, "推理与响应", "推理强度与思考开关；模型选择在「接入」抽屉设置", func() {
		enumField(c, t, s, cf, "effortLevel", "推理强度",
			[]string{"low", "medium", "high", "xhigh"}, "继承（未设置）")
		boolField(c, t, s, cf, "alwaysThinkingEnabled", "始终开启思考", "")
	})
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
	groupCard(c, t, "环境变量", "接入管理的 13 个键只读；其余变量可自由增删", func() {
		// 列头（原型 §6.6.2 env 表格）：与行布局对齐。
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "变量").Width(310).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			ui.Text(c, "值").Grow(1).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			ui.Text(c, "来源 / 操作").Width(150).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		})
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
		// 「打开接入」：跳到同页接入抽屉改受管键（宿主注入回调；独立渲染时隐藏）。
		if cf.OpenAccess != nil {
			if uikit.SoftButton(c, t, s, "gray", "打开接入").Clicked() {
				cf.OpenAccess()
			}
		}
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
	groupCard(c, t, "权限", "默认模式、规则列表与危险模式确认", func() {
		enumField(c, t, s, cf, "permissions.defaultMode", "默认模式",
			[]string{"default", "acceptEdits", "plan", "auto", "bypassPermissions"}, "继承（未设置）")
		listField(c, t, s, cf, "permissions.allow", "允许规则", "逗号分隔，例如 Bash(go test:*)")
		listField(c, t, s, cf, "permissions.ask", "询问规则", "逗号分隔")
		listField(c, t, s, cf, "permissions.deny", "拒绝规则", "逗号分隔")
		listField(c, t, s, cf, "permissions.additionalDirectories", "附加目录", "逗号分隔")
		dangerRow(c, t, s, cf)
	})
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
	groupCard(c, t, "状态栏", "statusLine 对象只写入当前层；字段留空即移除", func() {
		const path = "statusLine"
		disabled, reason := fieldBlocked(cf, path)
		if disabled {
			uikit.FormRowRaw(c, t, "状态栏", func() {
				ui.Text(c, statusLineText(cf)).Font(uikit.MonoFont)
				ui.Text(c, reason).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			})
			return
		}
		_ = path // statusLine 缺键直接渲染空控件（下方继承提示）
		statusLineInput(c, t, cf, "type", "类型", "command / static")
		statusLineInput(c, t, cf, "command", "命令", "例如 ~/.claude/statusline.sh")
		statusLineInput(c, t, cf, "text", "静态文本", "type=static 时显示")
		statusLineInput(c, t, cf, "padding", "内边距", "整数，例如 0")
		inheritHint(c, t, cf, "statusLine")
	})
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
	groupCard(c, t, "MCP 与插件", "项目 MCP 开关、插件启用与 Claude.ai 同步", func() {
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
	groupCard(c, t, "记忆与上下文", "自动压缩、自动记忆与清理周期", func() {
		boolField(c, t, s, cf, "autoCompactEnabled", "启用自动压缩", "")
		boolField(c, t, s, cf, "autoMemoryEnabled", "启用自动记忆", "")
		intField(c, t, s, cf, "cleanupPeriodDays", "清理周期（天）", "默认 30")
	})
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
	cur := stringAt(cf.Doc, path)
	uikit.FormRow(c, t, label, func() ui.Element {
		in := ui.TextInput(c, &cur).Placeholder(placeholder)
		if in.Changed() {
			setString(cf.Doc, path, cur)
			RecomputeDirty(cf)
		}
		return in
	})
	inheritHint(c, t, cf, path)
}

// bool 三态分段的段序（继承 / 开启 / 关闭）。
var boolChoices = []string{"继承", "开启", "关闭"}

func boolField(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, path, label, hint string) {
	disabled, reason := fieldBlocked(cf, path)
	if disabled {
		uikit.FormRowRaw(c, t, label, func() {
			ui.Text(c, effectiveText(cf, path)).Font(uikit.MonoFont)
			ui.Text(c, reason).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		})
		return
	}
	// 三态分段（原型 §6.6）：缺键=继承（不写入），开启/关闭写当前层；下层
	// 有效值以提示行展示，继承值不抄进当前文件。
	choice := 0
	if v, ok := corecfg.Get(cf.Doc, path); ok {
		if b, _ := v.(bool); b {
			choice = 1
		} else {
			choice = 2
		}
	}
	var changed bool
	uikit.FormRow(c, t, label, func() ui.Element {
		sel := uikit.Segmented(c, t, s, &choice, boolChoices...)
		changed = sel.Changed()
		return sel
	})
	if changed {
		// 写路径走 setBoolChoice（终审 Important#5：测试与生产不得各走一份）。
		switch choice {
		case 1:
			setBoolChoice(cf, path, true)
		case 2:
			setBoolChoice(cf, path, false)
		default:
			setBoolChoice(cf, path, nil)
		}
	}
	if entry, ok := cf.MergeEntries[path]; ok {
		b, _ := entry.Value.(bool)
		ui.Text(c, "下层："+boolLabel(b)+"（"+sourceLabel(entry.Source)+"）").
			FontSize(t.FontSize - 2).TextColor(t.TextMuted)
	}
	if hint != "" {
		ui.Text(c, hint).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
	}
}

// setBoolChoice 是三态写路径（测试直调）：true/false 写入，其余视为继承
// （删除键）。
func setBoolChoice(cf *state.ClaudeCfgState, path string, choice any) {
	switch v := choice.(type) {
	case bool:
		corecfg.Set(cf.Doc, path, v)
	default:
		corecfg.Delete(cf.Doc, path)
	}
	RecomputeDirty(cf)
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
	inheritHint(c, t, cf, path)
}

// orderedChipPaths 是顺序有语义的列表键（fallbackModel 按序尝试），chip 带
// ↑ 调整；其余列表（权限规则/附加目录/MCP 名单）是并集语义，顺序无意义。
var orderedChipPaths = map[string]bool{"fallbackModel": true}

func listField(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, path, label, placeholder string) {
	disabled, reason := fieldBlocked(cf, path)
	if disabled {
		uikit.FormRowRaw(c, t, label, func() {
			ui.Text(c, effectiveText(cf, path)).Font(uikit.MonoFont)
			ui.Text(c, reason).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		})
		return
	}
	items := listItems(cf.Doc, path)
	uikit.FormRowRaw(c, t, label, func() {
		ui.Column(c).Gap(6).Children(func() {
			if len(items) == 0 {
				ui.Text(c, "未设置").TextColor(t.TextMuted)
			} else {
				ui.Row(c).Gap(6).Wrap().Children(func() {
					for i, item := range items {
						chip(c, t, s, cf, path, i, item)
					}
				})
			}
			if cf.ChipInputs == nil {
				cf.ChipInputs = map[string]string{}
			}
			cur := cf.ChipInputs[path]
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				in := ui.TextInput(c, &cur).Placeholder(placeholder).Grow(1)
				if in.Changed() {
					cf.ChipInputs[path] = cur
				}
				if uikit.SoftButton(c, t, s, "gray", "添加").Clicked() {
					chipAdd(cf, path, cf.ChipInputs[path])
				}
			})
		})
	})
	inheritHint(c, t, cf, path)
}

// chip 渲染一枚列表项：可选 ↑（顺序键）+ mono 文本 + ×。
func chip(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, cf *state.ClaudeCfgState, path string, i int, item string) {
	ui.Box(c).Border(1, t.Border).Radius(8).Padding(3, 7).Shrink(0).Children(func() {
		ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
			if orderedChipPaths[path] && i > 0 {
				if uikit.SoftButton(c, t, s, "gray", "↑").Clicked() {
					chipMoveUp(cf, path, i)
				}
			}
			ui.Text(c, item).Font(uikit.MonoFont).FontSize(t.FontSize - 1)
			if uikit.SoftButton(c, t, s, "danger", "×").Clicked() {
				chipRemove(cf, path, i)
			}
		})
	})
}

// listItems 读取列表键的字符串切片（缺键/类型不符为 nil）。
func listItems(doc map[string]any, path string) []string {
	v, ok := corecfg.Get(doc, path)
	if !ok {
		return nil
	}
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// chipAdd 追加一项（trim 非空；同层重复提示不改文档）。
func chipAdd(cf *state.ClaudeCfgState, path, value string) {
	value = strings.TrimSpace(value)
	if value == "" {
		cf.FormMsg = "列表项不能为空"
		return
	}
	items := listItems(cf.Doc, path)
	for _, it := range items {
		if it == value {
			cf.FormMsg = "当前层已存在：" + value
			return
		}
	}
	delete(cf.ChipInputs, path)
	writeChipList(cf, path, append(items, value))
}

// chipRemove 删除下标 i；越界忽略。
func chipRemove(cf *state.ClaudeCfgState, path string, i int) {
	items := listItems(cf.Doc, path)
	if i < 0 || i >= len(items) {
		return
	}
	writeChipList(cf, path, append(items[:i], items[i+1:]...))
}

// chipMoveUp 把下标 i 与前一元素交换（i<=0 忽略）。
func chipMoveUp(cf *state.ClaudeCfgState, path string, i int) {
	if i <= 0 {
		return
	}
	items := listItems(cf.Doc, path)
	if i >= len(items) {
		return
	}
	items[i-1], items[i] = items[i], items[i-1]
	writeChipList(cf, path, items)
}

// writeChipList 写回字符串数组；空列表删除该键。
func writeChipList(cf *state.ClaudeCfgState, path string, items []string) {
	if len(items) == 0 {
		corecfg.Delete(cf.Doc, path)
	} else {
		out := make([]any, 0, len(items))
		for _, it := range items {
			out = append(out, it)
		}
		corecfg.Set(cf.Doc, path, out)
	}
	RecomputeDirty(cf)
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
	cur := intText(cf.Doc, path)
	uikit.FormRow(c, t, label, func() ui.Element {
		in := ui.TextInput(c, &cur).Placeholder(hint)
		if in.Changed() {
			setIntField(cf, path, cur)
		}
		return in
	})
	inheritHint(c, t, cf, path)
}

// inheritHint 在编辑控件下方渲染继承信息（只读提示，2026-11-07 用户实测：
// 去掉「覆盖」门槛后，继承值降为提示行——不写进当前层）。
func inheritHint(c *ui.Context, t *ui.Theme, cf *state.ClaudeCfgState, path string) {
	entry, ok := cf.MergeEntries[path]
	if !ok {
		return
	}
	ui.Text(c, "下层："+displayValue(path, entry.Value)+"（来源："+sourceLabel(entry.Source)+"）").
		FontSize(t.FontSize - 2).TextColor(t.TextMuted)
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
