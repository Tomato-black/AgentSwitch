// modelkey.go 是接入编辑子页的「主模型/回退模型」行状态 ↔ 文档顶层键双向同步
// （2026-11-07 用户裁定：模型选择归接入；model/fallbackModel 经既有投影链路
// 写进 ~/.claude/settings.json，并被受管集合自动只读）。模式与角色映射
// （roles.go）一致：Sync 从文档回填、Patch 从行写回；解析失败静默保留。
package accesskit

import (
	"AgentSwitch/internal/desktop/state"
)

// SyncModelKeysFromDoc JSON→表单回填：顶层 model（string）与
// fallbackModel（字符串数组，按序）；缺失/类型不符置空。解析失败保留旧状态。
func SyncModelKeysFromDoc(f *state.AccessForm) {
	doc, err := DocObject(f.DocJSON)
	if err != nil {
		return
	}
	if s, ok := doc["model"].(string); ok {
		f.ModelKey = s
	} else {
		f.ModelKey = ""
	}
	fallback := []string{}
	if arr, ok := doc["fallbackModel"].([]any); ok {
		for _, item := range arr {
			if s, ok := item.(string); ok {
				fallback = append(fallback, s)
			}
		}
	}
	f.FallbackModels = fallback
}

// PatchModelKey 主模型行 → 文档顶层 model 补丁（空＝删键＝跟随网关
// ANTHROPIC_MODEL）。
func PatchModelKey(f *state.AccessForm, value string) {
	raw, err := DocSetKey(f.DocJSON, "model", value)
	if err == nil {
		f.DocJSON = raw
	}
}

// PatchFallbackModels 回退链行 → 文档顶层 fallbackModel 补丁（有序数组；
// 空链删键）。
func PatchFallbackModels(f *state.AccessForm, models []string) {
	raw, err := DocSetKeyList(f.DocJSON, "fallbackModel", models)
	if err == nil {
		f.DocJSON = raw
	}
}
