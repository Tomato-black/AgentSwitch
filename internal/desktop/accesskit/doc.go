// Package accesskit 是接入页跨 Agent 共享的纯助手与视图管道：接入文档
// JSON 补丁、模型标签映射、页签标题、全局配置行与网关门。不 import 任何
// 页面包或组合根，保证 access 宿主与各 Agent 子包都能单向依赖它。
package accesskit

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"AgentSwitch/internal/bridge"
)

// DocObject 解析接入文档为通用 JSON 对象（空文本视为空对象；null/
// 非对象报错）。各文档补丁/回填函数共用的解析入口。
func DocObject(raw string) (map[string]any, error) {
	doc := map[string]any{}
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
			return nil, err
		}
		if doc == nil {
			return nil, fmt.Errorf("配置必须是 JSON 对象")
		}
	}
	return doc, nil
}

// MarshalDoc 把文档按 2 空格缩进序列化（UI 编辑器可读格式）。
func MarshalDoc(doc map[string]any) (string, error) {
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// DocSetModels 解析 raw 文档并整体替换 models 键（Codex 模型列表，
// 顺序即优先级）；空列表删除键（保存/应用时回退绑定模型 + 启用模型）。
// 解析失败返回 error（调用方保留旧文本）。
func DocSetModels(raw string, models []string) (string, error) {
	doc, err := DocObject(raw)
	if err != nil {
		return "", err
	}
	if len(models) == 0 {
		delete(doc, "models")
	} else {
		doc["models"] = models
	}
	return MarshalDoc(doc)
}

// DocSetCodex 解析 raw 文档并整体替换 codex 高级设置对象；三项
// （去空白后）全空时删除 codex 键，其余顶层键原样保留。
func DocSetCodex(raw, subagentModel, subagentEffort, autoReview string) (string, error) {
	doc, err := DocObject(raw)
	if err != nil {
		return "", err
	}
	codex := map[string]any{}
	if v := strings.TrimSpace(subagentModel); v != "" {
		codex["subagent_model"] = v
	}
	if v := strings.TrimSpace(subagentEffort); v != "" {
		codex["subagent_reasoning_effort"] = v
	}
	if v := strings.TrimSpace(autoReview); v != "" {
		codex["auto_review_model"] = v
	}
	if len(codex) == 0 {
		delete(doc, "codex")
	} else {
		doc["codex"] = codex
	}
	return MarshalDoc(doc)
}

// DocModels 从文档提取显式 models 列表；缺省/null/空数组/非字符串
// 元素均视为不可用（调用方回退到绑定模型 + 启用模型），与 bridge 的
// codexModelsFromDoc 语义一致。
func DocModels(doc map[string]any) ([]string, bool) {
	v, ok := doc["models"]
	if !ok || v == nil {
		return nil, false
	}
	xs, ok := v.([]any)
	if !ok || len(xs) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		s, ok := x.(string)
		if !ok {
			return nil, false
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}

// DocCodexString 取 codex 对象中的字符串字段（去空白；缺失/非字符串
// 按空处理）——编辑到一半的坏值不应阻塞回填。
func DocCodexString(codex map[string]any, key string) string {
	if codex == nil {
		return ""
	}
	s, _ := codex[key].(string)
	return strings.TrimSpace(s)
}

// CodexFallbackModels 返回未显式声明 models 时的回退列表：绑定模型
// 置首，其后按逻辑模型列表顺序追加启用项（去重）。
func CodexFallbackModels(label string, models []bridge.ModelView) []string {
	out := make([]string, 0, len(models)+1)
	if label = strings.TrimSpace(label); label != "" {
		out = append(out, label)
	}
	for _, m := range models {
		if !m.Enabled {
			continue
		}
		name := strings.TrimSpace(m.Name)
		if name == "" || slices.Contains(out, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// DocSetEnv 解析 raw 文档并对 env 键打补丁（value=="" 删除该键），2 空格
// 缩进序列化。解析失败返回 error（调用方保留旧文本）。仅 UI 本地补丁用，不做
// appType 校验（保存校验在 bridge）。
func DocSetEnv(raw, key, value string) (string, error) {
	doc, err := DocObject(raw)
	if err != nil {
		return "", err
	}
	env := map[string]any{}
	if v, ok := doc["env"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return "", fmt.Errorf("env 必须是 JSON 对象")
		}
		env = m
	}
	if value == "" {
		delete(env, key)
	} else {
		env[key] = value
	}
	doc["env"] = env
	return MarshalDoc(doc)
}

// DocEnvStrings 提取文档 env 字符串 map（角色行回填数据源）；env 值非
// 字符串/JSON 非法返回 error——调用方保留上一合法状态。
func DocEnvStrings(raw string) (map[string]string, error) {
	doc, err := DocObject(raw)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	if v, ok := doc["env"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("env 必须是 JSON 对象")
		}
		for k, val := range m {
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("env 值必须是字符串: %s", k)
			}
			out[k] = s
		}
	}
	return out, nil
}

// ModelMetaDoc 是 model_meta 条目在接入文档里的形态：空字段省略（= 回退
// 逻辑模型库值），per-config 覆盖只写用户改过的字段。
type ModelMetaDoc struct {
	DisplayName           string
	ContextWindow         int64
	ReasoningLevels       []string
	DefaultReasoningLevel string
}

// DocModelMeta 读取文档 model_meta.<model>：字段缺失按零值；类型错误/条目
// 非对象按缺失处理（编辑器回填不阻塞，坏值在应用时由 bridge 报错）。
func DocModelMeta(doc map[string]any, model string) (ModelMetaDoc, bool) {
	m, ok := doc["model_meta"].(map[string]any)
	if !ok {
		return ModelMetaDoc{}, false
	}
	e, ok := m[model].(map[string]any)
	if !ok {
		return ModelMetaDoc{}, false
	}
	out := ModelMetaDoc{}
	if s, ok := e["display_name"].(string); ok {
		out.DisplayName = strings.TrimSpace(s)
	}
	if n, err := docNumberInt64(e["context_window"]); err == nil {
		out.ContextWindow = n
	}
	if xs, ok := e["reasoning_levels"].([]any); ok {
		for _, x := range xs {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				out.ReasoningLevels = append(out.ReasoningLevels, strings.TrimSpace(s))
			}
		}
	}
	if s, ok := e["default_reasoning_level"].(string); ok {
		out.DefaultReasoningLevel = strings.TrimSpace(s)
	}
	return out, true
}

// DocSetModelMeta 把一行元数据写入文档 model_meta.<model>：空字段省略
// （回退库值）；条目为空则删除该模型键，model_meta 为空则删除该键。
func DocSetModelMeta(raw, model string, meta ModelMetaDoc) (string, error) {
	doc, err := DocObject(raw)
	if err != nil {
		return "", err
	}
	all, _ := doc["model_meta"].(map[string]any)
	if all == nil {
		all = map[string]any{}
	}
	entry := map[string]any{}
	if s := strings.TrimSpace(meta.DisplayName); s != "" {
		entry["display_name"] = s
	}
	if meta.ContextWindow > 0 {
		entry["context_window"] = meta.ContextWindow
	}
	if len(meta.ReasoningLevels) > 0 {
		entry["reasoning_levels"] = meta.ReasoningLevels
	}
	if s := strings.TrimSpace(meta.DefaultReasoningLevel); s != "" {
		entry["default_reasoning_level"] = s
	}
	if len(entry) == 0 {
		delete(all, model)
	} else {
		all[model] = entry
	}
	if len(all) == 0 {
		delete(doc, "model_meta")
	} else {
		doc["model_meta"] = all
	}
	return MarshalDoc(doc)
}

// docNumberInt64 把文档数字（UseNumber 解码的 json.Number 或原生数值）转成
// int64；非整数返回错误。
func docNumberInt64(v any) (int64, error) {
	switch n := v.(type) {
	case json.Number:
		return n.Int64()
	case int64:
		return n, nil
	case int:
		return int64(n), nil
	case float64:
		if n != float64(int64(n)) {
			return 0, fmt.Errorf("not an integer")
		}
		return int64(n), nil
	}
	return 0, fmt.Errorf("not a number")
}
