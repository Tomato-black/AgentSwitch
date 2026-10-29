// Package codex 是接入页的 Codex 页签编辑器：模型列表（首项即默认模型）、
// 只读模型元数据预览、子代理/自动审批高级设置，以及只读 config.toml 预览。
// 模型列表与高级设置由配置 JSON（models/codex）双向同步。
package codex

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// accessSyncCodexFromDoc JSON→表单回填（Codex）：显式 models 优先，缺失时
// 回退「绑定模型 + 启用逻辑模型」；同时回填 codex 高级设置。解析失败保留
// 旧状态（与角色回填同款「编辑到一半不闪烁」）。
func syncFromDoc(st *state.State, models []bridge.ModelView) {
	doc, err := accesskit.DocObject(st.Access.Form.DocJSON)
	if err != nil {
		return
	}
	f := &st.Access.Form
	if names, ok := accesskit.DocModels(doc); ok {
		f.CodexModels = names
	} else {
		f.CodexModels = accesskit.CodexFallbackModels(f.ModelLabel, models)
	}
	codex, _ := doc["codex"].(map[string]any)
	f.SubagentModel = accesskit.DocCodexString(codex, "subagent_model")
	f.SubagentEffort = accesskit.DocCodexString(codex, "subagent_reasoning_effort")
	f.AutoReviewModel = accesskit.DocCodexString(codex, "auto_review_model")
}

// accessPatchCodexModels 表单→JSON 补丁：模型列表写回文档 models，默认
// 模型（保存时的 logical_model_id）跟随首行。补丁失败保留旧文档。
func patchCodexModels(st *state.State) {
	f := &st.Access.Form
	raw, err := accesskit.DocSetModels(f.DocJSON, f.CodexModels)
	if err != nil {
		return
	}
	f.DocJSON = raw
	if len(f.CodexModels) > 0 {
		f.ModelLabel = f.CodexModels[0]
	}
}

// accessCodexNextModel 返回 labels 中第一个未出现在 models 里的模型名
// （「＋ 添加模型」选中项）；全部已用返回 ok=false，避免重复行。
func nextModel(labels, models []string) (string, bool) {
	for _, l := range labels {
		if !slices.Contains(models, l) {
			return l, true
		}
	}
	return "", false
}

// accessMoveCodexModel 把 models[i] 与相邻行换位（delta=-1 上移 / +1 下移）；
// 首行上移、末行下移与越界下标都是 no-op（与 moveTarget 同语义）。
func moveModel(models []string, i, delta int) {
	j := i + delta
	if i < 0 || i >= len(models) || j < 0 || j >= len(models) {
		return
	}
	models[i], models[j] = models[j], models[i]
}

// accessPatchCodexSettings 表单→JSON 补丁：三项高级设置写回 codex 对象
// （全空删除键）；补丁失败保留旧文档。
func patchCodexSettings(st *state.State) {
	f := &st.Access.Form
	raw, err := accesskit.DocSetCodex(f.DocJSON, f.SubagentModel, f.SubagentEffort, f.AutoReviewModel)
	if err != nil {
		return
	}
	f.DocJSON = raw
}

// accessCodexSelectLabel 把文档空值映射成下拉默认显示项（非空原样）。
func selectLabel(raw, defaultLabel string) string {
	if strings.TrimSpace(raw) == "" {
		return defaultLabel
	}
	return raw
}

// accessCodexSelectRaw 是 accessCodexSelectLabel 的逆映射：默认显示项
// 写回空串（不落 codex 键）。
func selectRaw(label, defaultLabel string) string {
	if label == defaultLabel {
		return ""
	}
	return strings.TrimSpace(label)
}

// accessCodexModelOptions 构造模型下拉选项：默认项置首，其后按序追加
// codexModels（去空去重），列表外旧引用补到末尾（编辑旧配置不丢值）。
func modelOptions(defaultLabel string, models []string, current string) []string {
	opts := []string{defaultLabel}
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v != "" && !slices.Contains(opts, v) {
			opts = append(opts, v)
		}
	}
	for _, m := range models {
		add(m)
	}
	add(current)
	return opts
}

// accessCodexEffortOptions 构造推理强度选项：默认项 + 固定取值；未知旧值
// 补到末尾，避免 Select 显示空值。
func effortOptions(current string) []string {
	opts := make([]string, 0, len(accessCodexEffortChoices)+2)
	opts = append(opts, accessCodexEffortDefault)
	opts = append(opts, accessCodexEffortChoices...)
	if v := strings.TrimSpace(current); v != "" && !slices.Contains(opts, v) {
		opts = append(opts, v)
	}
	return opts
}

// accessCodexModelsSection 渲染 Codex 模型列表编辑（spec 2026-10-27 §4.2）：
// 有序 Select 行 + 上移/下移/删除，首行「默认」徽标，底部「＋ 添加模型」。
// 行内按钮只记动作、循环结束后统一应用（构建中改切片会越界）；任何变化
// 写回文档 models（accessPatchCodexModels）。只剩一项时不渲染删除按钮
// ——列表至少保留一项（保存另有空列表校验）。返回列表是否发生变化（调用
// 方据此刷新只读 config.toml 预览）。
func modelsSection(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, labels []string) bool {
	f := &st.Access.Form
	ui.Text(c, "模型列表").Bold()
	ui.Text(c, "按序显示在 Codex 模型菜单；第一行为默认模型").TextColor(t.TextMuted)
	if len(labels) == 0 {
		ui.Text(c, "还没有启用的逻辑模型——先在「逻辑模型」页创建并启用").TextColor(t.TextMuted)
	}
	act, actI := "", -1
	changed := false
	for i := range f.CodexModels {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("%d.", i+1)).Width(20).TextColor(t.TextMuted)
			// 编辑旧配置时列表可能含停用模型：把当前值补进选项，避免
			// Select 显示空值、一次误点就丢掉引用。
			opts := labels
			if v := f.CodexModels[i]; v != "" && !slices.Contains(opts, v) {
				opts = append([]string{v}, labels...)
			}
			before := f.CodexModels[i]
			sel := ui.Select(c, &f.CodexModels[i], opts)
			if sel.Changed() && f.CodexModels[i] != before {
				changed = true
			}
			if i == 0 {
				uikit.SoftBadge(c, t, s, "ok", "默认")
			}
			if uikit.SoftButton(c, t, s, "gray", "上移").Clicked() {
				act, actI = "up", i
			}
			if uikit.SoftButton(c, t, s, "gray", "下移").Clicked() {
				act, actI = "down", i
			}
			if len(f.CodexModels) > 1 && uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
				act, actI = "del", i
			}
		})
	}
	switch act {
	case "up":
		moveModel(f.CodexModels, actI, -1)
	case "down":
		moveModel(f.CodexModels, actI, +1)
	case "del":
		f.CodexModels = append(f.CodexModels[:actI], f.CodexModels[actI+1:]...)
	}
	dirty := act != "" || changed
	if dirty {
		patchCodexModels(st)
	}
	if uikit.SoftButton(c, t, s, "act", "＋ 添加模型").Clicked() {
		if next, ok := nextModel(labels, f.CodexModels); ok {
			f.CodexModels = append(f.CodexModels, next)
			patchCodexModels(st)
			dirty = true
		}
	}
	return dirty
}

// Codex 元数据（显示名/上下文窗口/思考等级）的编辑与校验（2026-10-28 从
// 逻辑模型编辑页迁入）：数据仍存在逻辑模型上（Codex 目录按模型名取），
// 但在接入页集中维护。
var reasoningLevelChoices = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// parsedLevels 把思考等级输入框（逗号分隔）解析成选项列表：去空格、去空项、
// 按首次出现去重。下拉与保存校验共用同一规则。
func parsedLevels(raw string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, lv := range strings.Split(raw, ",") {
		lv = strings.TrimSpace(lv)
		if lv == "" || seen[lv] {
			continue
		}
		seen[lv] = true
		out = append(out, lv)
	}
	return out
}

// parseMeta 把编辑行解析成 bridge 元数据：上下文窗口空串=0、否则须为非负
// 整数；思考等级须在白名单内；默认等级空时取列表首项，非空须属于列表。
func parseMeta(ed *state.MetaEditState) (bridge.ModelMeta, error) {
	meta := bridge.ModelMeta{DisplayName: strings.TrimSpace(ed.DisplayName)}
	if raw := strings.TrimSpace(ed.ContextWindow); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return meta, fmt.Errorf("上下文窗口需要是非负整数，如 1000000")
		}
		meta.ContextWindow = n
	}
	known := make(map[string]bool, len(reasoningLevelChoices))
	for _, lv := range reasoningLevelChoices {
		known[lv] = true
	}
	meta.ReasoningLevels = parsedLevels(ed.ReasoningLevels)
	for _, lv := range meta.ReasoningLevels {
		if !known[lv] {
			return meta, fmt.Errorf("未知思考等级「%s」，可用：%s", lv, strings.Join(reasoningLevelChoices, "、"))
		}
	}
	def := strings.TrimSpace(ed.DefaultReasoningLevel)
	if def == "" && len(meta.ReasoningLevels) > 0 {
		def = meta.ReasoningLevels[0]
	}
	if def != "" && !slices.Contains(meta.ReasoningLevels, def) {
		return meta, fmt.Errorf("默认思考等级「%s」不在思考等级列表中", def)
	}
	meta.DefaultReasoningLevel = def
	return meta, nil
}

// ensureMetaEntries 为模型列表补齐元数据编辑行（只补缺失项，不覆盖用户
// 正在编辑的内容）；条目取自逻辑模型的现有元数据。
func ensureMetaEntries(f *state.AccessForm, models []bridge.ModelView) {
	if f.CodexMeta == nil {
		f.CodexMeta = map[string]*state.MetaEditState{}
	}
	byName := map[string]bridge.ModelView{}
	for _, m := range models {
		if _, ok := byName[m.Name]; !ok {
			byName[m.Name] = m
		}
	}
	doc, _ := accesskit.DocObject(f.DocJSON)
	for _, name := range f.CodexModels {
		if _, ok := f.CodexMeta[name]; ok {
			continue
		}
		ed := &state.MetaEditState{}
		if m, ok := byName[name]; ok {
			ed.DisplayName = m.DisplayName
			if m.ContextWindow > 0 {
				ed.ContextWindow = strconv.FormatInt(m.ContextWindow, 10)
			}
			ed.ReasoningLevels = strings.Join(m.ReasoningLevels, ",")
			ed.DefaultReasoningLevel = m.DefaultReasoningLevel
		}
		// 文档 model_meta 的非空字段覆盖库值（per-config 覆盖项优先）。
		if meta, ok := accesskit.DocModelMeta(doc, name); ok {
			if meta.DisplayName != "" {
				ed.DisplayName = meta.DisplayName
			}
			if meta.ContextWindow > 0 {
				ed.ContextWindow = strconv.FormatInt(meta.ContextWindow, 10)
			}
			if len(meta.ReasoningLevels) > 0 {
				ed.ReasoningLevels = strings.Join(meta.ReasoningLevels, ",")
			}
			if meta.DefaultReasoningLevel != "" {
				ed.DefaultReasoningLevel = meta.DefaultReasoningLevel
			}
		}
		f.CodexMeta[name] = ed
	}
}

// metaLevelSelect 是默认思考等级下拉：值绑等级字符串，空串显示「模板默认」。
func metaLevelSelect(c *ui.Context, t *ui.Theme, level *string, levels []string) {
	label := *level
	if label == "" {
		label = "模板默认"
	}
	sel := ui.SelectBase(c, level)
	sel.Trigger.Padding(6, 10).Border(1, t.Border).Radius(t.Radius).
		Background(t.Surface).MinWidth(120 * (t.FontSize / 13)).Children(func() {
		ui.Text(c, label).SingleLine()
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(4).Background(t.Surface).Border(1, t.Border).Radius(t.Radius)
		for _, lv := range append([]string{""}, levels...) {
			item := sel.Item(lv).Padding(6, 10).Radius(t.Radius)
			switch {
			case item.Highlighted():
				item.Background(t.Accent).TextColor(t.AccentText)
			case lv == *level:
				item.Background(t.SurfaceHover)
			}
			text := lv
			if text == "" {
				text = "模板默认"
			}
			item.Children(func() { ui.Text(c, text).SingleLine() })
		}
	})
}

// modelMetaSection 渲染「模型元数据」编辑区：按模型列表顺序为每个模型提供
// 显示名/上下文窗口/思考等级/默认等级编辑，保存写回逻辑模型（对所有 Codex
// 接入共享），并刷新只读 config.toml 预览。
func modelMetaSection(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, models []bridge.ModelView) {
	f := &st.Access.Form
	ui.Text(c, "模型元数据").Bold()
	ui.Text(c, "仅影响 Codex 模型菜单；改动即时写入本配置（覆盖逻辑模型默认值），保存配置并「应用」后生效").TextColor(t.TextMuted)
	ensureMetaEntries(f, models)
	byName := map[string]bridge.ModelView{}
	for _, m := range models {
		if _, ok := byName[m.Name]; !ok {
			byName[m.Name] = m
		}
	}
	changed := false
	for _, name := range f.CodexModels {
		modelName := name
		if _, ok := byName[modelName]; !ok {
			ui.Text(c, modelName+"（模型不存在）").TextColor(t.Danger)
			continue
		}
		ed := f.CodexMeta[modelName]
		if ed == nil {
			continue
		}
		scale := t.FontSize / 13
		ui.Column(c).Gap(6).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				ui.Text(c, modelName).Bold().MinWidth(120 * scale).SingleLine()
				if ui.TextInput(c, &ed.DisplayName).MinWidth(140 * scale).Placeholder("显示名").Changed() {
					changed = applyMetaEdit(f, modelName, ed) || changed
				}
				if ui.TextInput(c, &ed.ContextWindow).MinWidth(110 * scale).Placeholder("上下文窗口").Changed() {
					changed = applyMetaEdit(f, modelName, ed) || changed
				}
				beforeLevel := ed.DefaultReasoningLevel
				metaLevelSelect(c, t, &ed.DefaultReasoningLevel, parsedLevels(ed.ReasoningLevels))
				if ed.DefaultReasoningLevel != beforeLevel {
					changed = applyMetaEdit(f, modelName, ed) || changed
				}
			})
			if ui.TextInput(c, &ed.ReasoningLevels).Grow(1).Placeholder("思考等级：low,medium,high").Changed() {
				changed = applyMetaEdit(f, modelName, ed) || changed
			}
			uikit.Feedback(c, t, uikit.InferFeedback(ed.Msg), ed.Msg)
		})
	}
	if changed {
		RefreshPreview(st, acc)
	}
}

// applyMetaEdit 校验并写入本配置文档的 model_meta（失败内联提示、不改文档）；
// 返回是否成功写入。
func applyMetaEdit(f *state.AccessForm, modelName string, ed *state.MetaEditState) bool {
	meta, err := parseMeta(ed)
	if err != nil {
		ed.Msg = err.Error()
		return false
	}
	raw, err := accesskit.DocSetModelMeta(f.DocJSON, modelName, accesskit.ModelMetaDoc{
		DisplayName:           meta.DisplayName,
		ContextWindow:         meta.ContextWindow,
		ReasoningLevels:       meta.ReasoningLevels,
		DefaultReasoningLevel: meta.DefaultReasoningLevel,
	})
	if err != nil {
		ed.Msg = "写入配置文档失败：" + err.Error()
		return false
	}
	f.DocJSON = raw
	ed.Msg = "已写入配置文档（保存并「应用」后生效）"
	return true
}

// accessCodexAdvancedSection 渲染 Codex 高级设置（spec 2026-10-27 §4.2）：
// 子代理模型、子代理推理强度、自动审批模型三个下拉。空值分别显示
// 「跟随主模型」/「默认」/「主模型自己审批（默认）」；任何变化写回文档
// codex 对象（accessPatchCodexSettings），下次回填保持一致。返回是否有变化
// （调用方据此刷新只读 config.toml 预览）。
func advancedSection(c *ui.Context, t *ui.Theme, st *state.State) bool {
	f := &st.Access.Form
	ui.Text(c, "高级设置").Bold()
	ui.Text(c, "子代理与自动审批为可选配置，默认跟随主模型").TextColor(t.TextMuted)
	changed := false
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "子代理模型:").Width(uikit.LabelWidth).TextColor(t.TextMuted)
		label := selectLabel(f.SubagentModel, accessCodexSubagentFollow)
		before := label
		sel := ui.Select(c, &label,
			modelOptions(accessCodexSubagentFollow, f.CodexModels, f.SubagentModel))
		if sel.Changed() && label != before {
			f.SubagentModel = selectRaw(label, accessCodexSubagentFollow)
			changed = true
		}
	})
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "子代理推理强度:").Width(uikit.LabelWidth).TextColor(t.TextMuted)
		label := selectLabel(f.SubagentEffort, accessCodexEffortDefault)
		before := label
		sel := ui.Select(c, &label, effortOptions(f.SubagentEffort))
		if sel.Changed() && label != before {
			f.SubagentEffort = selectRaw(label, accessCodexEffortDefault)
			changed = true
		}
	})
	ui.Row(c).Gap(12).Children(func() {
		ui.Text(c, "自动审批模型:").Width(uikit.LabelWidth).TextColor(t.TextMuted)
		label := selectLabel(f.AutoReviewModel, accessCodexAutoReviewDefault)
		before := label
		sel := ui.Select(c, &label,
			modelOptions(accessCodexAutoReviewDefault, f.CodexModels, f.AutoReviewModel))
		if sel.Changed() && label != before {
			f.AutoReviewModel = selectRaw(label, accessCodexAutoReviewDefault)
			changed = true
		}
	})
	if changed {
		patchCodexSettings(st)
	}
	return changed
}

// EditorCard 渲染 Codex 编辑器：模型列表 + 元数据编辑 + 高级设置作为
// 模型段落注入通用 EditorCard，配置 JSON 之后追加只读 config.toml 预览。
// （T8 暂留宿主包，T9 迁入 codex 子包。）
func EditorCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, gateOK bool) {
	accesskit.EditorCard(c, t, s, st, acc, gateOK, accesskit.EditorHooks{
		ModelSection: func(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, labels []string, models []bridge.ModelView) {
			// 文档 → 模型列表回填（打开配置/编辑 JSON 后同步）；首项即
			// 默认模型（保存时的 logical_model_id）。
			syncFromDoc(st, models)
			changed := modelsSection(c, t, s, st, labels)
			modelMetaSection(c, t, s, st, acc, models)
			changed = advancedSection(c, t, st) || changed
			// 模型列表/高级设置变化后刷新只读 config.toml 预览；文本域手动
			// 编辑不逐键刷新（模板加载会跑 codex debug models，每帧调用卡顿），
			// 改用「刷新预览」按钮。
			if changed {
				RefreshPreview(st, acc)
			}
		},
		AfterDoc: func(c *ui.Context, t *ui.Theme, s uikit.SoftPalette) {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "将写入的 config.toml（只读）").Bold()
				ui.Box(c).Grow(1)
				if uikit.SoftButton(c, t, s, "gray", "刷新预览").Clicked() {
					RefreshPreview(st, acc)
				}
			})
			ui.TextArea(c, &st.Access.Form.CodexPreview).Height(240).ReadOnly(true).Font(uikit.MonoFont)
		},
	})
}

// Codex 高级设置下拉的空值显示项（空值语义 = 继承/跟随）。
const (
	accessCodexSubagentFollow    = "（跟随主模型）"
	accessCodexEffortDefault     = "默认"
	accessCodexAutoReviewDefault = "主模型自己审批（默认）"
)

// accessCodexEffortChoices 是子代理推理强度的可选值（Codex config.toml
// [agents].default_subagent_reasoning_effort 取值）。
var accessCodexEffortChoices = []string{"low", "medium", "high", "xhigh"}

// accessRefreshCodexPreview 生成 Codex 只读 config.toml 预览（spec 2026-10-27
// §4.6）：默认模型取模型列表首项（空列表回退绑定模型），映射逻辑模型 ID 后
// 调 bridge 预览（含真实网关令牌、合并现有用户键，不写盘）。无可用模型或
// bridge 报错时清空预览并内联中文提示。
func RefreshPreview(st *state.State, acc bridge.AccessConfigs) {
	f := &st.Access.Form
	label := f.ModelLabel
	if len(f.CodexModels) > 0 {
		label = f.CodexModels[0]
	}
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	modelID := accesskit.ModelIDByLabel(models, label)
	if modelID == "" {
		f.CodexPreview = ""
		f.Msg = "请至少添加一个模型后再预览 config.toml"
		return
	}
	preview, err := acc.PreviewCodexConfig(modelID, f.DocJSON)
	if err != nil {
		f.CodexPreview = ""
		f.Msg = "预览失败：" + err.Error()
		return
	}
	f.CodexPreview = preview
	f.Msg = ""
}

// Prepare 在编辑器打开后准备 Codex 表单：文档→模型列表回填，并生成只读
// config.toml 预览（宿主 preloadForm 调用）。
func Prepare(st *state.State, acc bridge.AccessConfigs) {
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	st.Access.Form.CodexMeta = nil // 打开/重开时按最新库值重建编辑行
	syncFromDoc(st, models)
	RefreshPreview(st, acc)
}
