package claudecfg

import (
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// jsonParseDebounce 是 JSON 编辑后重新解析的等待时间。TextArea 每次按键都
// 改动文本，立即解析既浪费又会在输入中途闪错误，因此等待用户停手。
const jsonParseDebounce = 300 * time.Millisecond

// jsonView 渲染当前层的原始 settings JSON。它只改 cf.Doc，不写盘；保存由
// Section 顶部按钮经 bridge 完成。托管层只读。
func jsonView(c *ui.Context, t *ui.Theme, st *state.State) {
	cf := &st.ClaudeCfg
	s := uikit.Softs(t)
	// 只读：托管层，以及磁盘文件损坏的修复态（I5——原文只可查看，修复走
	// 备份恢复或「用空配置重建」）。
	readOnly := cf.Layer == "managed" || cf.BrokenFile

	uikit.Feedback(c, t, uikit.FeedbackWarning,
		"JSON 视图显示明文（含密钥）；截图或分享前请遮蔽。日志与遥测不会记录值。")

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if uikit.SoftButton(c, t, s, "gray", "格式化").Clicked() && !readOnly {
			formatJSON(cf)
		}
		if uikit.SoftButton(c, t, s, "gray", "重置").Clicked() && !readOnly {
			cf.JSONResetArm = true
		}
		if uikit.SoftButton(c, t, s, "act", "校验").Clicked() && !readOnly {
			validateJSON(cf)
		}
		if cf.JSONPending {
			ui.Text(c, "解析中…").FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		}
		ui.Box(c).Grow(1)
		if cf.ParseErr != "" {
			uikit.SoftBadge(c, t, s, "danger", "JSON 错误")
		} else {
			uikit.SoftBadge(c, t, s, "ok", "JSON 合法")
		}
	})

	ta := ui.TextArea(c, &cf.JSONText).Height(320).Font(uikit.MonoFont).ReadOnly(readOnly)
	if ta.Changed() && !readOnly {
		cf.JSONPending = true
		cf.JSONDueNano = c.Now().Add(jsonParseDebounce).UnixNano()
		cf.JSONHint = ""
		c.After(jsonParseDebounce)
	}
	// 防抖到期后只解析一次；解析失败保留当前 Doc，避免半截输入覆盖文档。
	if cf.JSONPending && c.Now().UnixNano() >= cf.JSONDueNano {
		cf.JSONPending = false
		parseJSONText(cf, cf.JSONText)
	}

	if cf.ParseErr != "" {
		uikit.Feedback(c, t, uikit.FeedbackError, cf.ParseErr)
	}
	if cf.JSONHint != "" {
		uikit.Feedback(c, t, uikit.InferFeedback(cf.JSONHint), cf.JSONHint)
	}
	renderViolations(c, t, cf)
	jsonResetDialog(c, st)
}

// parseJSONText 严格解析 JSON 对象并同步到 Doc。失败时只写 ParseErr，
// 保留上一份合法 Doc；错误文本带「第 X 行第 Y 列」。
func parseJSONText(cf *state.ClaudeCfgState, text string) bool {
	cf.JSONPending = false
	doc, err := corecfg.ParseDoc([]byte(text))
	if err != nil {
		cf.ParseErr = err.Error()
		cf.JSONHint = ""
		return false
	}
	cf.Doc = doc.Raw
	cf.ParseErr = ""
	cf.JSONHint = ""
	RecomputeDirty(cf)
	return true
}

// formatJSON 先解析再按 2 空格缩进重排；语法错误时不改动文本。
func formatJSON(cf *state.ClaudeCfgState) bool {
	if !parseJSONText(cf, cf.JSONText) {
		return false
	}
	cf.JSONText = pretty(cf.Doc)
	return true
}

// validateJSON 立即解析并给出阻断性问题摘要，供「校验」按钮使用。
func validateJSON(cf *state.ClaudeCfgState) {
	if !parseJSONText(cf, cf.JSONText) {
		return
	}
	if issues := blockingIssues(cf); len(issues) > 0 {
		cf.JSONHint = "校验失败：" + strings.Join(issues, "；")
		return
	}
	cf.JSONHint = "JSON 合法，未发现阻断性问题"
}

// resetJSON 放弃未保存编辑，恢复打开/上次保存时的基线文档。
func resetJSON(cf *state.ClaudeCfgState) {
	cf.Doc = cloneDoc(cf.Baseline)
	cf.JSONText = pretty(cf.Doc)
	cf.ParseErr = ""
	cf.JSONHint = ""
	cf.JSONPending = false
	RecomputeDirty(cf)
}

func jsonResetDialog(c *ui.Context, st *state.State) {
	cf := &st.ClaudeCfg
	uikit.ConfirmDialog(c, c.Theme(), uikit.ConfirmOpts{
		Open:         &cf.JSONResetArm,
		Title:        "重置 JSON 编辑？",
		Message:      "将丢弃未保存的修改，恢复为打开时的内容。",
		ConfirmLabel: "重置",
		Danger:       true,
		OnConfirm:    func() { resetJSON(cf) },
	})
}

// saveBlockReason 返回阻断保存的首个原因（空=可保存）。它同时覆盖
// JSON 语法错误、本次引入的 scope/受管键违规，以及被删除的受管键。
func saveBlockReason(cf *state.ClaudeCfgState) string {
	issues := blockingIssues(cf)
	if len(issues) == 0 {
		return ""
	}
	return "存在阻断保存的问题：" + strings.Join(issues, "；")
}

// blockingIssues 汇总所有会阻止保存的问题；路径去重后按字典序排列。
func blockingIssues(cf *state.ClaudeCfgState) []string {
	seen := map[string]bool{}
	var out []string
	add := func(path, reason string) {
		if path == "" || seen[path] {
			return
		}
		seen[path] = true
		out = append(out, path+"："+reason)
	}
	if cf.ParseErr != "" {
		add("JSON", "语法错误："+cf.ParseErr)
	}
	for _, v := range currentViolations(cf) {
		if v.Severity == "error" && v.Changed {
			add(v.Path, violationReason(v.Reason))
		}
	}
	for _, path := range managedChangedPaths(cf) {
		add(path, violationReason("managed"))
	}
	sort.Strings(out)
	return out
}

// currentViolations 用当前 Doc 对 Baseline 重新校验；历史遗留违规只警告。
func currentViolations(cf *state.ClaudeCfgState) []corecfg.Violation {
	return corecfg.Validate(cloneDoc(cf.Doc), scopeOf(cf.Layer, cf.Sub), cf.Baseline, cf.Managed)
}

func renderViolations(c *ui.Context, t *ui.Theme, cf *state.ClaudeCfgState) {
	for _, v := range currentViolations(cf) {
		kind := uikit.FeedbackWarning
		prefix := "历史遗留"
		if v.Changed {
			kind = uikit.FeedbackError
			prefix = "本次修改"
		}
		uikit.Feedback(c, t, kind, prefix+" · "+v.Path+"："+violationReason(v.Reason))
	}
}

func violationReason(reason string) string {
	switch reason {
	case "managed":
		return "受管键由「接入」管理，不能在设置区修改"
	case "weak-managed":
		return "弱受管键；接入应用后会覆盖此值"
	case "managed-only":
		return "仅企业托管层可写"
	case "user-only":
		return "仅用户级或企业托管可写"
	case "not-in-project-shared":
		return "不能写入项目共享设置"
	case "ineffective-at-project":
		return "项目级 auto/bypassPermissions 不生效"
	case "invalid-value":
		return "skillOverrides 取值非法（允许 on / name-only / user-invocable-only / off）"
	case "reserved-name":
		return "保留名称不能写入 skillOverrides"
	}
	return reason
}

// managedChangedPaths 基于 Baseline → Doc 的 diff 找出受管键变化（含删除）。
// corecfg.Validate 只能看到 Doc 中仍存在的路径；删除受管键必须在这里补上。
func managedChangedPaths(cf *state.ClaudeCfgState) []string {
	changed := corecfg.Diff(cf.Baseline, cf.Doc)
	strongEnv := make(map[string]bool, len(cf.Managed.StrongEnv))
	for _, k := range cf.Managed.StrongEnv {
		strongEnv[k] = true
	}
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		if path != "" && !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	for _, path := range changed {
		switch {
		case path == "env":
			for _, k := range cf.Managed.StrongEnv {
				if envDocHas(cf.Baseline, k) || envDocHas(cf.Doc, k) {
					add("env." + k)
				}
			}
		case strings.HasPrefix(path, "env."):
			key := strings.SplitN(strings.TrimPrefix(path, "env."), ".", 2)[0]
			if strongEnv[key] {
				add(path)
			}
		default:
			for _, k := range cf.Managed.StrongTopLevel {
				if path == k || strings.HasPrefix(path, k+".") {
					add(path)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func envDocHas(doc map[string]any, key string) bool {
	env, ok := doc["env"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = env[key]
	return ok
}
