// 本文件是 OpenAI 上游方向的请求侧：Anthropic Messages 请求 → OpenAI
// chat.completions 请求（含工具调用与 system 提示映射）。
package protocol

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ---------- 请求转换：Anthropic → OpenAI ----------

type anthxMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthxTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthxRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        json.RawMessage `json:"system"`
	Messages      []anthxMessage  `json:"messages"`
	Tools         []anthxTool     `json:"tools"`
	ToolChoice    json.RawMessage `json:"tool_choice"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	StopSequences []string        `json:"stop_sequences"`
	Stream        bool            `json:"stream"`
}

type oaixMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content,omitempty"`
	ToolCalls  []oaixToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type oaixRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Messages    []oaixMessage   `json:"messages"`
	Tools       []oaixTool      `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type oaixTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// parseBlocks 解析 string | []block 两种 content 形状。
func parseBlocks(raw json.RawMessage) ([]anthxBlock, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []anthxBlock{{Type: "text", Text: s}}, true
	}
	var blocks []anthxBlock
	if json.Unmarshal(raw, &blocks) == nil {
		return blocks, false
	}
	return nil, false
}

// blocksText 把 string|[]block 归并为纯文本（tool_result content 用）。
func blocksText(raw json.RawMessage) string {
	blocks, _ := parseBlocks(raw)
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func systemText(raw json.RawMessage) string { return blocksText(raw) }

func convertToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &tc) != nil {
		return nil
	}
	switch tc.Type {
	case "auto":
		return json.RawMessage(`"auto"`)
	case "any":
		return json.RawMessage(`"required"`)
	case "tool":
		b, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]string{"name": tc.Name},
		})
		return b
	default:
		return nil
	}
}

// AnthropicRequestToOpenAI 转换请求体（spec §5.3.1）。dropped 是被丢弃的
// Anthropic 专有字段名（排序），调用方记一行日志。model 原样透传——调用方
// 已用 RewriteModel 重写为上游名。
func AnthropicRequestToOpenAI(body []byte) ([]byte, []string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
	}
	kept := map[string]bool{"model": true, "max_tokens": true, "system": true, "messages": true,
		"tools": true, "tool_choice": true, "temperature": true, "top_p": true,
		"stop_sequences": true, "stream": true}
	var dropped []string
	for k := range top {
		if !kept[k] {
			dropped = append(dropped, k)
		}
	}
	sort.Strings(dropped)

	var ar anthxRequest
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
	}
	out := oaixRequest{Model: ar.Model, MaxTokens: ar.MaxTokens, Stream: ar.Stream,
		Temperature: ar.Temperature, TopP: ar.TopP, Stop: ar.StopSequences,
		ToolChoice: convertToolChoice(ar.ToolChoice)}
	if sys := systemText(ar.System); sys != "" {
		out.Messages = append(out.Messages, oaixMessage{Role: "system", Content: &sys})
	}
	// 空名字的 tool_use 是客户端历史里的坏块（如被 CLI 拒绝的空工具调用）：
	// 转成 OpenAI tool_calls 会得到空 function.name，严格上游（小米 mimo）直接
	// 400 "missing a function name"。丢掉该 tool_use，并按其 id 丢掉配对的
	// tool_result，避免留下悬空的 tool 消息。
	droppedToolIDs := map[string]bool{}
	for _, m := range ar.Messages {
		blocks, _ := parseBlocks(m.Content)
		var textParts []string
		var calls []oaixToolCall
		var toolMsgs []oaixMessage
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					textParts = append(textParts, b.Text)
				}
			case "tool_use":
				if b.Name == "" {
					if b.ID != "" {
						droppedToolIDs[b.ID] = true
					}
					continue
				}
				args := string(b.Input)
				if args == "" {
					args = "{}"
				}
				tc := oaixToolCall{ID: b.ID, Type: "function"}
				tc.Function.Name, tc.Function.Arguments = b.Name, args
				calls = append(calls, tc)
			case "tool_result":
				if droppedToolIDs[b.ToolUseID] {
					continue
				}
				txt := blocksText(b.Content)
				toolMsgs = append(toolMsgs, oaixMessage{Role: "tool", Content: &txt, ToolCallID: b.ToolUseID})
			}
		}
		// tool 消息在前：OpenAI 要求 tool 消息紧随带 tool_calls 的 assistant 消息，
		// 本块剩余文本的 user 消息排其后。
		out.Messages = append(out.Messages, toolMsgs...)
		if len(textParts) > 0 || len(calls) > 0 {
			var c *string
			if len(textParts) > 0 {
				j := strings.Join(textParts, "\n")
				c = &j
			}
			out.Messages = append(out.Messages, oaixMessage{Role: m.Role, Content: c, ToolCalls: calls})
		}
	}
	for _, tl := range ar.Tools {
		var t oaixTool
		t.Type = "function"
		t.Function.Name, t.Function.Description, t.Function.Parameters = tl.Name, tl.Description, tl.InputSchema
		out.Tools = append(out.Tools, t)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, nil, err
	}
	return b, dropped, nil
}
