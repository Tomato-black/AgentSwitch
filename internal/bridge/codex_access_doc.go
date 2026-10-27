package bridge

import (
	"fmt"
	"sort"
	"strings"
)

// codexAccessSettings 是 Codex 接入文档中 models/codex 两个顶层键的结构化
// 视图（spec 2026-10-27 §4.2）。语义校验（模型是否存在、是否启用、是否为
// 绑定的默认模型）在保存路径由 resolveCodexModels 完成，这里只做结构校验。
type codexAccessSettings struct {
	Models                  []string
	SubagentModel           string
	SubagentReasoningEffort string
	AutoReviewModel         string
}

// codexDocKeys 是 Codex 文档 codex 对象允许的键。
var codexDocKeys = map[string]bool{
	"subagent_model":            true,
	"subagent_reasoning_effort": true,
	"auto_review_model":         true,
}

// parseCodexSettings 校验 Codex 文档的 models/codex 结构：models 缺省视为
// 空（兼容旧 env-only 配置），存在时必须是非空、无重复的字符串数组；
// codex 缺省视为空，存在时仅允许三个已知键且值必须是字符串，
// subagent_reasoning_effort 非空时须在思考等级白名单内。
func parseCodexSettings(doc map[string]any) (codexAccessSettings, error) {
	var out codexAccessSettings
	if v, ok := doc["models"]; ok && v != nil {
		models, ok := asStringList(v)
		if !ok || len(models) == 0 {
			return out, fmt.Errorf("models 必须是非空字符串数组")
		}
		seen := make(map[string]bool, len(models))
		trimmed := make([]string, 0, len(models))
		for _, raw := range models {
			name := strings.TrimSpace(raw)
			if name == "" {
				return out, fmt.Errorf("models 中的模型名不能为空")
			}
			if seen[name] {
				return out, fmt.Errorf("models 中存在重复模型：%s", name)
			}
			seen[name] = true
			trimmed = append(trimmed, name)
		}
		out.Models = trimmed
	}

	if v, ok := doc["codex"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return out, fmt.Errorf("codex 必须是 JSON 对象")
		}
		for k := range m {
			if !codexDocKeys[k] {
				return out, fmt.Errorf("codex 仅支持 subagent_model、subagent_reasoning_effort、auto_review_model 键")
			}
		}
		fields := []struct {
			key string
			dst *string
		}{
			{"subagent_model", &out.SubagentModel},
			{"subagent_reasoning_effort", &out.SubagentReasoningEffort},
			{"auto_review_model", &out.AutoReviewModel},
		}
		for _, f := range fields {
			v, ok := m[f.key]
			if !ok || v == nil {
				continue
			}
			s, ok := v.(string)
			if !ok {
				return out, fmt.Errorf("codex.%s 必须是字符串", f.key)
			}
			*f.dst = strings.TrimSpace(s)
		}
		if out.SubagentReasoningEffort != "" && !reasoningLevelWhitelist[out.SubagentReasoningEffort] {
			return out, fmt.Errorf("未知思考等级：%s", out.SubagentReasoningEffort)
		}
	}
	return out, nil
}

// codexSettingsFromDoc 宽松提取 models/codex（不报错）：坏字段按缺省处理，
// 供目录生成等只读路径使用；保存路径必须走 parseCodexSettings。
func codexSettingsFromDoc(doc map[string]any) codexAccessSettings {
	var out codexAccessSettings
	if models, ok := codexModelsFromDoc(doc); ok {
		out.Models = models
	}
	m, ok := doc["codex"].(map[string]any)
	if !ok {
		return out
	}
	out.SubagentModel = lenientTrimmedString(m["subagent_model"])
	out.SubagentReasoningEffort = lenientTrimmedString(m["subagent_reasoning_effort"])
	out.AutoReviewModel = lenientTrimmedString(m["auto_review_model"])
	return out
}

// lenientTrimmedString 取字符串值并去首尾空白；非字符串/缺失按空处理。
func lenientTrimmedString(v any) string {
	s, _ := v.(string)
	return strings.TrimSpace(s)
}

// codexModelsFromDoc 提取文档显式声明的模型列表；第二个返回值表示是否
// 存在可用的显式列表（缺省、null 或形状非法时为 false，调用方据此回退
// 到「绑定模型 + 全部启用模型」）。
func codexModelsFromDoc(doc map[string]any) ([]string, bool) {
	v, ok := doc["models"]
	if !ok || v == nil {
		return nil, false
	}
	models, ok := asStringList(v)
	if !ok || len(models) == 0 {
		return nil, false
	}
	trimmed := make([]string, 0, len(models))
	for _, raw := range models {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, false
		}
		trimmed = append(trimmed, name)
	}
	return trimmed, true
}

// resolveCodexModels 把 Codex 接入文档的模型列表解析为最终生效的有序列表
// （spec 2026-10-27 §4.2、§7）：
//   - 显式列表非空：每项必须存在且启用，首项必须是绑定的默认模型；
//   - 缺省/空列表：回退为「绑定模型 + 全部启用逻辑模型（按名排序）」，
//     供旧 env-only 配置在保存时固化下来。
//
// 绑定的默认模型必须存在且启用，否则报错（指明改绑/启用动作）。
func (s AccessConfigs) resolveCodexModels(bound string, settings codexAccessSettings) ([]string, error) {
	bound = strings.TrimSpace(bound)
	if bound == "" {
		return nil, fmt.Errorf("请先选择逻辑模型")
	}
	lms, err := s.A.Store().LogicalModels().List()
	if err != nil {
		return nil, err
	}
	enabled := make(map[string]bool, len(lms))
	for _, lm := range lms {
		enabled[lm.Name] = lm.Enabled
	}
	boundEnabled, ok := enabled[bound]
	if !ok {
		return nil, fmt.Errorf("绑定的默认模型 %s 不存在，请改绑", bound)
	}
	if !boundEnabled {
		return nil, fmt.Errorf("绑定的默认模型 %s 已停用，请启用或改绑", bound)
	}
	if len(settings.Models) == 0 {
		rest := make([]string, 0, len(lms))
		for _, lm := range lms {
			if lm.Enabled && lm.Name != bound {
				rest = append(rest, lm.Name)
			}
		}
		sort.Strings(rest)
		return append([]string{bound}, rest...), nil
	}
	out := make([]string, 0, len(settings.Models))
	seen := make(map[string]bool, len(settings.Models))
	for i, raw := range settings.Models {
		name := strings.TrimSpace(raw)
		if name == "" {
			return nil, fmt.Errorf("模型列表中的模型名不能为空")
		}
		if seen[name] {
			return nil, fmt.Errorf("模型列表中存在重复模型：%s", name)
		}
		seen[name] = true
		supported, ok := enabled[name]
		if !ok {
			return nil, fmt.Errorf("模型 %s 不存在，请先在逻辑模型页创建", name)
		}
		if !supported {
			return nil, fmt.Errorf("模型 %s 已停用，请启用或从模型列表中移除", name)
		}
		if i == 0 && name != bound {
			return nil, fmt.Errorf("第一个模型必须是绑定的默认模型 %s", bound)
		}
		out = append(out, name)
	}
	return out, nil
}

// toAnyStrings 把 []string 转为 JSON 编码用 []any（写回文档 models 键）。
func toAnyStrings(xs []string) []any {
	out := make([]any, len(xs))
	for i, x := range xs {
		out[i] = x
	}
	return out
}

// asStringList 接受 JSON 解码得到的 []any 或内部构造的 []string。
func asStringList(v any) ([]string, bool) {
	switch xs := v.(type) {
	case []string:
		return xs, true
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			s, ok := x.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	default:
		return nil, false
	}
}
