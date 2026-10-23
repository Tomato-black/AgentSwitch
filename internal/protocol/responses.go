package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ResponsesRequest 是 /v1/responses 请求的解析视图。只保留路由与转换需要的
// 字段；store/client_metadata/prompt_cache_key 等字段忽略（不下发上游）。
type ResponsesRequest struct {
	Model              string            `json:"model"`
	Stream             bool              `json:"stream"`
	Instructions       string            `json:"instructions"`
	Input              json.RawMessage   `json:"input"`
	Tools              []json.RawMessage `json:"tools"`
	ToolChoice         json.RawMessage   `json:"tool_choice"`
	MaxOutputTokens    *int              `json:"max_output_tokens"`
	Temperature        *float64          `json:"temperature"`
	TopP               *float64          `json:"top_p"`
	PreviousResponseID json.RawMessage   `json:"previous_response_id"`
}

// ParseResponsesRequest 解析并校验 Responses 请求。previous_response_id 需要
// 服务端会话存储，本网关不实现——显式 400，避免静默丢失上下文。
func ParseResponsesRequest(body []byte) (ResponsesRequest, error) {
	var r ResponsesRequest
	if err := json.Unmarshal(body, &r); err != nil {
		return r, fmt.Errorf("%s: invalid body: %w", ErrTypeInvalidRequest, err)
	}
	if strings.TrimSpace(r.Model) == "" {
		return r, fmt.Errorf("%s: model is required", ErrTypeInvalidRequest)
	}
	if len(r.PreviousResponseID) > 0 && string(r.PreviousResponseID) != "null" {
		return r, fmt.Errorf("%s: previous_response_id is not supported", ErrTypeInvalidRequest)
	}
	return r, nil
}

type responsesInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Namespace string          `json:"namespace"`
	Arguments json.RawMessage `json:"arguments"`
	Input     string          `json:"input"`
	Output    json.RawMessage `json:"output"`
}

// responsesInputToMessages 把 instructions + input 转成 chat messages。
// developer/system 文本合并为一条头部 system；非文本分片丢弃。
func responsesInputToMessages(input json.RawMessage, instructions string, tc *ResponseToolContext) ([]map[string]any, error) {
	var systemParts []string
	if strings.TrimSpace(instructions) != "" {
		systemParts = append(systemParts, instructions)
	}
	var msgs []map[string]any

	if len(input) > 0 && string(input) != "null" {
		var s string
		if json.Unmarshal(input, &s) == nil {
			if s != "" {
				msgs = append(msgs, map[string]any{"role": "user", "content": s})
			}
		} else {
			var items []responsesInputItem
			if err := json.Unmarshal(input, &items); err != nil {
				return nil, fmt.Errorf("%s: input must be a string or an array", ErrTypeInvalidRequest)
			}
			var pendingCalls []map[string]any
			knownCallIDs := map[string]bool{}
			flushCalls := func() {
				if len(pendingCalls) == 0 {
					return
				}
				// 相邻 assistant 文本与 tool_calls 合并成一条消息（部分上游拒绝
				// 连续 assistant 消息；cc-switch 同样合并）。
				if n := len(msgs); n > 0 && msgs[n-1]["role"] == "assistant" {
					existing, _ := msgs[n-1]["tool_calls"].([]map[string]any)
					msgs[n-1]["tool_calls"] = append(existing, pendingCalls...)
					pendingCalls = nil
					return
				}
				msgs = append(msgs, map[string]any{
					"role": "assistant", "content": nil, "tool_calls": pendingCalls,
				})
				pendingCalls = nil
			}
			appendToolResult := func(item responsesInputItem) {
				flushCalls()
				text := responsesOutputText(item.Output)
				if knownCallIDs[item.CallID] {
					msgs = append(msgs, map[string]any{
						"role": "tool", "tool_call_id": item.CallID, "content": text,
					})
					return
				}
				msgs = append(msgs, map[string]any{
					"role":    "user",
					"content": "[tool_result without a matching tool_call] " + text,
				})
			}
			for _, item := range items {
				switch item.Type {
				case "function_call":
					flushCalls()
					knownCallIDs[item.CallID] = true
					pendingCalls = append(pendingCalls, map[string]any{
						"id": item.CallID, "type": "function",
						"function": map[string]any{
							"name":      tc.ChatName(item.Name, item.Namespace),
							"arguments": rawString(item.Arguments, "{}"),
						},
					})
				case "custom_tool_call":
					flushCalls()
					knownCallIDs[item.CallID] = true
					args, _ := json.Marshal(map[string]any{customToolInputField: item.Input})
					pendingCalls = append(pendingCalls, map[string]any{
						"id": item.CallID, "type": "function",
						"function": map[string]any{"name": tc.ChatName(item.Name, ""), "arguments": string(args)},
					})
				case "tool_search_call":
					flushCalls()
					knownCallIDs[item.CallID] = true
					pendingCalls = append(pendingCalls, map[string]any{
						"id": item.CallID, "type": "function",
						"function": map[string]any{"name": "tool_search", "arguments": rawString(item.Arguments, "{}")},
					})
				case "function_call_output", "custom_tool_call_output", "tool_search_output":
					appendToolResult(item)
				case "reasoning":
					// 本网关不生成 reasoning item，历史中的 reasoning 直接忽略。
				case "item_reference":
					return nil, fmt.Errorf("%s: item_reference is not supported", ErrTypeInvalidRequest)
				case "message":
					text := responsesContentText(item.Content)
					switch item.Role {
					case "developer", "system":
						if text != "" {
							systemParts = append(systemParts, text)
						}
					case "assistant":
						flushCalls()
						if text != "" {
							msgs = append(msgs, map[string]any{"role": "assistant", "content": text})
						}
					default:
						flushCalls()
						if text != "" {
							msgs = append(msgs, map[string]any{"role": "user", "content": text})
						}
					}
				}
			}
			flushCalls()
		}
	}
	if len(systemParts) > 0 {
		msgs = append([]map[string]any{{"role": "system", "content": strings.Join(systemParts, "\n\n")}}, msgs...)
	}
	return msgs, nil
}

func rawString(raw json.RawMessage, fallback string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return fallback
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// responsesOutputText 把工具输出（字符串或内容分片数组）压成文本。
func responsesOutputText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// ResponsesRequestToChat 把 Responses 请求转换成 chat.completions 请求体。
// 只下发 chat 上游可理解的字段；store/reasoning/include 等一律丢弃。
func ResponsesRequestToChat(req ResponsesRequest, tc *ResponseToolContext, chatModel string) ([]byte, error) {
	msgs, err := responsesInputToMessages(req.Input, req.Instructions, tc)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"model":    chatModel,
		"stream":   req.Stream,
		"messages": msgs,
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		out["max_tokens"] = *req.MaxOutputTokens
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if tools := tc.ChatTools(); len(tools) > 0 {
		out["tools"] = tools
	}
	if choice := mapResponsesToolChoice(req.ToolChoice, tc); len(choice) > 0 {
		out["tool_choice"] = choice
	}
	return json.Marshal(out)
}

// mapResponsesToolChoice 把 Responses 侧的 tool_choice 映射为 chat 形状：
// 字符串形式（auto/none/required）原样透传，工具对象改成 chat function 选择。
func mapResponsesToolChoice(raw json.RawMessage, tc *ResponseToolContext) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var obj struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	if json.Unmarshal(raw, &obj) != nil || obj.Type == "" {
		return raw // "auto"/"none"/"required" 原样
	}
	switch obj.Type {
	case "function":
		b, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]any{"name": tc.ChatName(obj.Name, obj.Namespace)},
		})
		return b
	case "custom":
		b, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]any{"name": obj.Name},
		})
		return b
	case "tool_search":
		b, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "tool_search"},
		})
		return b
	default:
		return raw
	}
}

// responsesContentText 提取 Responses 内容分片的文本（input_text/output_text/text）。
func responsesContentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var out []string
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text":
			if p.Text != "" {
				out = append(out, p.Text)
			}
		}
	}
	return strings.Join(out, "")
}
