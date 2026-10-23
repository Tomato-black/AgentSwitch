package protocol

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
)

type responseToolKind int

const (
	responseToolFunction responseToolKind = iota
	responseToolCustom
	responseToolNamespace
	responseToolSearch
)

type responseToolSpec struct {
	Kind      responseToolKind
	Name      string
	Namespace string
}

// ResponseToolContext 持有本次请求「chat 名 → Responses 工具语义」映射。
// 同一请求内工具集合固定，转换器只读它，不并发写。
type ResponseToolContext struct {
	specsByChatName map[string]responseToolSpec
	chatNamesByKey  map[string]string // key: namespace + "\x00" + name
	tools           []map[string]any
	dropped         int
}

func NewResponseToolContext(req ResponsesRequest) *ResponseToolContext {
	tc := &ResponseToolContext{
		specsByChatName: map[string]responseToolSpec{},
		chatNamesByKey:  map[string]string{},
	}
	for _, raw := range req.Tools {
		tc.addTool(raw)
	}
	tc.addInputDeclaredTools(req.Input)
	return tc
}

func (tc *ResponseToolContext) ChatTools() []map[string]any { return tc.tools }
func (tc *ResponseToolContext) DroppedTools() int           { return tc.dropped }

func (tc *ResponseToolContext) Spec(chatName string) (responseToolSpec, bool) {
	s, ok := tc.specsByChatName[chatName]
	return s, ok
}

// ChatName 把 Responses 侧的 (name, namespace) 映射为下发给 chat 上游的名字。
func (tc *ResponseToolContext) ChatName(name, namespace string) string {
	if n, ok := tc.chatNamesByKey[namespace+"\x00"+name]; ok {
		return n
	}
	return name
}

type responseToolDef struct {
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  json.RawMessage   `json:"parameters"`
	Strict      *bool             `json:"strict"`
	Tools       []responseToolDef `json:"tools"`
}

func (tc *ResponseToolContext) addTool(raw json.RawMessage) {
	var t responseToolDef
	if err := json.Unmarshal(raw, &t); err != nil {
		tc.dropped++
		return
	}
	switch t.Type {
	case "function":
		tc.addFunction(t, "")
	case "custom":
		tc.addCustom(raw, t)
	case "namespace":
		tc.addNamespace(t)
	case "tool_search":
		tc.addToolSearch()
	default:
		// web_search 等托管工具无法由 chat/Anthropic 上游兑现。
		tc.dropped++
	}
}

func (tc *ResponseToolContext) addFunction(t responseToolDef, namespace string) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		tc.dropped++
		return
	}
	chatName := name
	if namespace != "" {
		chatName = flattenResponseToolName(namespace, name)
	}
	if _, exists := tc.specsByChatName[chatName]; exists {
		tc.dropped++
		return
	}
	fn := map[string]any{
		"name":       chatName,
		"parameters": normalizeToolParameters(t.Parameters),
	}
	if t.Description != "" {
		fn["description"] = t.Description
	}
	if t.Strict != nil {
		fn["strict"] = *t.Strict
	}
	kind := responseToolFunction
	if namespace != "" {
		kind = responseToolNamespace
	}
	tc.specsByChatName[chatName] = responseToolSpec{Kind: kind, Name: name, Namespace: namespace}
	tc.chatNamesByKey[namespace+"\x00"+name] = chatName
	tc.tools = append(tc.tools, map[string]any{"type": "function", "function": fn})
}

// flattenResponseToolName 生成 chat 上游可接受的名字（≤64 字节）；超长时截断
// 并追加 FNV-1a 短哈希，保证同一 namespace/name 总是得到同一 chat 名。
func flattenResponseToolName(namespace, name string) string {
	full := namespace + "__" + name
	if len(full) <= 64 {
		return full
	}
	h := fnv.New32a()
	h.Write([]byte(full))
	suffix := fmt.Sprintf("__%08x", h.Sum32())
	return full[:64-len(suffix)] + suffix
}

// normalizeToolParameters 保证 chat 上游拿到 {"type":"object","properties":{}}。
func normalizeToolParameters(raw json.RawMessage) map[string]any {
	var p map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil || p == nil {
		p = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if p["type"] != "object" {
		p["type"] = "object"
	}
	if p["properties"] == nil {
		p["properties"] = map[string]any{}
	}
	return p
}

const (
	customToolInputField        = "input"
	customToolInputDescription  = "Raw string input for the original custom tool. Preserve formatting exactly and follow the original tool definition embedded in the description."
	customToolDefinitionHeading = "Original tool definition:"
)

// addCustom 把 Responses 的 custom 工具（如 apply_patch 的自由文本输入）映射为
// chat 函数工具：入参收敛为 {"input": string}，原始工具定义（含 format/grammar）
// 原样嵌入 description，供上游模型理解自由文本语义。
func (tc *ResponseToolContext) addCustom(raw json.RawMessage, t responseToolDef) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		tc.dropped++
		return
	}
	if _, exists := tc.specsByChatName[name]; exists {
		tc.dropped++
		return
	}
	def, _ := json.Marshal(json.RawMessage(raw))
	fn := map[string]any{
		"name":        name,
		"description": customToolDefinitionHeading + "\n```json\n" + string(def) + "\n```",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				customToolInputField: map[string]any{
					"type":        "string",
					"description": customToolInputDescription,
				},
			},
			"required": []string{customToolInputField},
		},
	}
	tc.specsByChatName[name] = responseToolSpec{Kind: responseToolCustom, Name: name}
	tc.chatNamesByKey["\x00"+name] = name
	tc.tools = append(tc.tools, map[string]any{"type": "function", "function": fn})
}

// addNamespace 把 namespace 内的子 function 拍平为 "<namespace>__<name>" 注册，
// 规范记录保留原始 namespace/name，供响应侧还原。
func (tc *ResponseToolContext) addNamespace(t responseToolDef) {
	namespace := strings.TrimSpace(t.Name)
	if namespace == "" {
		tc.dropped++
		return
	}
	for _, child := range t.Tools {
		if child.Type == "function" {
			tc.addFunction(child, namespace)
		}
	}
}

// addToolSearch 把 Responses 的 tool_search 托管工具降级为一个 chat 函数代理，
// 让上游模型仍可发出搜索意图（实际加载由客户端处理）。
func (tc *ResponseToolContext) addToolSearch() {
	const name = "tool_search"
	if _, exists := tc.specsByChatName[name]; exists {
		return
	}
	fn := map[string]any{
		"name":        name,
		"description": "Search and load Codex tools, plugins, connectors, and MCP namespaces for the current task.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query for tools or connectors to load."},
				"limit": map[string]any{"type": "integer", "description": "Maximum number of tool groups to return."},
			},
			"required": []string{"query"},
		},
	}
	tc.specsByChatName[name] = responseToolSpec{Kind: responseToolSearch, Name: name}
	tc.chatNamesByKey["\x00"+name] = name
	tc.tools = append(tc.tools, map[string]any{"type": "function", "function": fn})
}

// addInputDeclaredTools 递归扫描 input：tool_search_output / additional_tools
// 中声明的工具（含 namespace 子工具）注册进本次工具上下文。
func (tc *ResponseToolContext) addInputDeclaredTools(input json.RawMessage) {
	if len(input) == 0 {
		return
	}
	var v any
	if json.Unmarshal(input, &v) != nil {
		return
	}
	tc.walkDeclaredTools(v)
}

func (tc *ResponseToolContext) walkDeclaredTools(v any) {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			tc.walkDeclaredTools(e)
		}
	case map[string]any:
		switch x["type"] {
		case "tool_search_output", "additional_tools":
			if arr, ok := x["tools"].([]any); ok {
				for _, t := range arr {
					if b, err := json.Marshal(t); err == nil {
						tc.addTool(b)
					}
				}
			}
		}
		for _, e := range x {
			tc.walkDeclaredTools(e)
		}
	}
}
