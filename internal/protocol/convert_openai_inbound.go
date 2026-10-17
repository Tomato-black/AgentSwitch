package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// 本文件是 OpenAI 入站方向：OpenAI 客户端（chat.completions）↔ Anthropic 上游
// （/v1/messages）非流式互转。与 openai.go 的反方向（Anthropic 客户端 → OpenAI
// 上游）对称；两侧共用 oaixToolCall / anthxMessage / anthxTool 形状，互不改语义。

// ---------- OpenAI 入站请求 ----------

// OpenAIChatRequest 是 chat.completions 请求的解析视图：Model/Stream/Messages
// 为路由与转换所需字段；其余未知字段收入 Extra，Marshal 时原样合并回去（透传）。
type OpenAIChatRequest struct {
	Model               string                     `json:"model"`
	Stream              bool                       `json:"stream"`
	Messages            []json.RawMessage          `json:"messages"`
	Temperature         *float64                   `json:"temperature,omitempty"`
	TopP                *float64                   `json:"top_p,omitempty"`
	MaxTokens           *int                       `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                       `json:"max_completion_tokens,omitempty"`
	Stop                json.RawMessage            `json:"stop,omitempty"`  // string | []string
	Tools               json.RawMessage            `json:"tools,omitempty"` // []{type:function,function:{...}}
	ToolChoice          json.RawMessage            `json:"tool_choice,omitempty"`
	Extra               map[string]json.RawMessage `json:"-"` // 其余透传字段
}

var openAIChatKnownFields = map[string]bool{
	"model": true, "stream": true, "messages": true, "temperature": true, "top_p": true,
	"max_tokens": true, "max_completion_tokens": true, "stop": true,
	"tools": true, "tool_choice": true,
}

func (r *OpenAIChatRequest) UnmarshalJSON(data []byte) error {
	type known OpenAIChatRequest
	var k known
	if err := json.Unmarshal(data, &k); err != nil {
		return err
	}
	*r = OpenAIChatRequest(k)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return err
	}
	for key, val := range top {
		if openAIChatKnownFields[key] {
			continue
		}
		if r.Extra == nil {
			r.Extra = map[string]json.RawMessage{}
		}
		r.Extra[key] = val
	}
	return nil
}

func (r OpenAIChatRequest) MarshalJSON() ([]byte, error) {
	type known OpenAIChatRequest
	b, err := json.Marshal(known(r))
	if err != nil {
		return nil, err
	}
	if len(r.Extra) == 0 {
		return b, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, err
	}
	for k, v := range r.Extra {
		top[k] = v
	}
	return json.Marshal(top)
}

// ParseOpenAIChatRequest 解析 OpenAI 入站请求体；缺 model 与 JSON 非法均报
// invalid_request_error（与 ParseChatRequest 同风格）。
func ParseOpenAIChatRequest(body []byte) (OpenAIChatRequest, error) {
	var req OpenAIChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return OpenAIChatRequest{}, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
	}
	if req.Model == "" {
		return OpenAIChatRequest{}, errors.New(ErrTypeInvalidRequest + ": model is required")
	}
	return req, nil
}

// ---------- OpenAI 请求 → Anthropic 请求 ----------

type oaiInboundMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"` // string | []part | null
	ToolCalls  []oaixToolCall  `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
}

type oaiInboundTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// anthropicOutRequest 是 /v1/messages 请求输出形状（字段名同 Anthropic 官方；
// 可选字段 omitempty，避免 null 污染请求体）。
type anthropicOutRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        string          `json:"system,omitempty"`
	Messages      []anthxMessage  `json:"messages"`
	Tools         []anthxTool     `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
}

// anthropicOutBlock 是请求侧 content block（text/tool_use/tool_result），带
// omitempty；不复用 openai.go 的 anthxBlock（解析用、无 omitempty）。
type anthropicOutBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// openAIContentText 把 OpenAI 消息 content（string | []part | null）归并为纯
// 文本（text-only：image_url 等多模态 part 丢弃，与包内既有纯文本归并一致）。
func openAIContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
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
		if p.Type == "text" && p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// parseOpenAIStop 兼容 OpenAI stop 的 string | []string 两种形状。
func parseOpenAIStop(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	return nil
}

// toolChoiceToAnthropic 映射 tool_choice："none" → nil（并由调用方丢弃 tools，
// Anthropic 无 none）；"auto"/"required" → {type:auto|any}；function 指定 →
// {type:tool,name}。其余形状退化为 nil（默认 auto 行为）。
func toolChoiceToAnthropic(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "auto":
			return json.RawMessage(`{"type":"auto"}`), false
		case "required":
			return json.RawMessage(`{"type":"any"}`), false
		case "none":
			return nil, true
		}
		return nil, false
	}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Function.Name != "" {
		b, _ := json.Marshal(map[string]string{"type": "tool", "name": obj.Function.Name})
		return b, false
	}
	return nil, false
}

// OpenAIRequestToAnthropic 把入站 chat.completions 请求转为 /v1/messages body；
// model 替换为 anthropicModel（上游真实模型名）。映射：system 升顶层 system，
// user/assistant/tool 按序入 messages（连续 tool 消息合并为同一条 user 的
// tool_result 块），temperature/top_p/stop_sequences 同名映射，tools/tool_choice
// 对换。max_tokens 缺省 4096——Anthropic 侧必填而 OpenAI 客户端常省略。
// text-only：多模态 content part 只取 text。
func OpenAIRequestToAnthropic(oa OpenAIChatRequest, anthropicModel string) ([]byte, error) {
	out := anthropicOutRequest{
		Model:         anthropicModel,
		Temperature:   oa.Temperature,
		TopP:          oa.TopP,
		Stream:        oa.Stream,
		StopSequences: parseOpenAIStop(oa.Stop),
	}
	out.MaxTokens = 4096
	if oa.MaxCompletionTokens != nil && *oa.MaxCompletionTokens > 0 {
		out.MaxTokens = *oa.MaxCompletionTokens
	} else if oa.MaxTokens != nil && *oa.MaxTokens > 0 {
		out.MaxTokens = *oa.MaxTokens
	}

	var systemParts []string
	var pendingTools []anthropicOutBlock // 累积的 tool_result，合并进同一条 user 消息
	flushTools := func() {
		if len(pendingTools) == 0 {
			return
		}
		content, err := json.Marshal(pendingTools)
		if err != nil {
			return
		}
		out.Messages = append(out.Messages, anthxMessage{Role: "user", Content: content})
		pendingTools = nil
	}

	for _, raw := range oa.Messages {
		var m oaiInboundMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
		}
		switch m.Role {
		case "system", "developer":
			if txt := openAIContentText(m.Content); txt != "" {
				systemParts = append(systemParts, txt)
			}
		case "user":
			flushTools()
			txt := openAIContentText(m.Content)
			if txt == "" {
				continue // text-only：纯多模态/空消息无文本可映射
			}
			content, _ := json.Marshal(txt)
			out.Messages = append(out.Messages, anthxMessage{Role: "user", Content: content})
		case "assistant":
			flushTools()
			var blocks []anthropicOutBlock
			if txt := openAIContentText(m.Content); txt != "" {
				blocks = append(blocks, anthropicOutBlock{Type: "text", Text: txt})
			}
			for _, tc := range m.ToolCalls {
				input := map[string]any{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
				if input == nil {
					input = map[string]any{}
				}
				rawIn, _ := json.Marshal(input)
				blocks = append(blocks, anthropicOutBlock{
					Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: rawIn,
				})
			}
			if len(blocks) == 0 {
				continue
			}
			content, _ := json.Marshal(blocks)
			out.Messages = append(out.Messages, anthxMessage{Role: "assistant", Content: content})
		case "tool":
			txt := openAIContentText(m.Content)
			content, _ := json.Marshal(txt)
			pendingTools = append(pendingTools, anthropicOutBlock{
				Type: "tool_result", ToolUseID: m.ToolCallID, Content: content,
			})
		}
	}
	flushTools()

	if len(systemParts) > 0 {
		out.System = strings.Join(systemParts, "\n")
	}

	if len(oa.Tools) > 0 {
		var tools []oaiInboundTool
		if err := json.Unmarshal(oa.Tools, &tools); err != nil {
			return nil, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
		}
		tc, disable := toolChoiceToAnthropic(oa.ToolChoice)
		if !disable {
			for _, tl := range tools {
				var t anthxTool
				t.Name, t.Description, t.InputSchema = tl.Function.Name, tl.Function.Description, tl.Function.Parameters
				out.Tools = append(out.Tools, t)
			}
			out.ToolChoice = tc
		}
	}

	return json.Marshal(out)
}

// ---------- Anthropic 响应 → OpenAI 响应（非流式）----------

type oaiInboundOutMessage struct {
	Role      string         `json:"role"`
	Content   *string        `json:"content"`
	ToolCalls []oaixToolCall `json:"tool_calls,omitempty"`
}

type oaiInboundOutChoice struct {
	Index        int                  `json:"index"`
	Message      oaiInboundOutMessage `json:"message"`
	FinishReason string               `json:"finish_reason"`
}

type oaiInboundOutResponse struct {
	ID      string                `json:"id"`
	Object  string                `json:"object"`
	Created int64                 `json:"created"`
	Model   string                `json:"model"`
	Choices []oaiInboundOutChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// mapOpenAIFinishReason 把 Anthropic stop_reason 映射回 OpenAI finish_reason。
func mapOpenAIFinishReason(s string) string {
	switch s {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default: // end_turn / stop_sequence / 空
		return "stop"
	}
}

// AnthropicResponseToOpenAI 转换 Anthropic 非流响应为 chat.completions 形状
// （spec：id/choices/content/usage 映射）。model 用 requestedModel（逻辑模型名），
// 不泄露上游模型名；usage 落到 prompt_tokens/completion_tokens/total_tokens。
func AnthropicResponseToOpenAI(anthropicBody []byte, requestedModel string) ([]byte, error) {
	var ar anthxResponse
	if err := json.Unmarshal(anthropicBody, &ar); err != nil {
		return nil, fmt.Errorf("decode upstream body: %w", err)
	}
	id := "chatcmpl-0"
	if ar.ID != "" {
		id = "chatcmpl-" + ar.ID
	}
	resp := oaiInboundOutResponse{ID: id, Object: "chat.completion", Created: time.Now().Unix(), Model: requestedModel}
	resp.Usage.PromptTokens = ar.Usage.InputTokens
	resp.Usage.CompletionTokens = ar.Usage.OutputTokens
	resp.Usage.TotalTokens = ar.Usage.InputTokens + ar.Usage.OutputTokens

	var texts []string
	var calls []oaixToolCall
	for _, b := range ar.Content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "tool_use":
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			tc := oaixToolCall{ID: b.ID, Type: "function"}
			tc.Function.Name, tc.Function.Arguments = b.Name, args
			calls = append(calls, tc)
		}
	}
	var content *string
	switch {
	case len(texts) > 0:
		j := strings.Join(texts, "\n")
		content = &j
	case len(calls) == 0:
		j := ""
		content = &j
	default: // 仅 tool_calls：OpenAI content 为 null
	}

	resp.Choices = []oaiInboundOutChoice{{
		Message: oaiInboundOutMessage{
			Role:      "assistant",
			Content:   content,
			ToolCalls: calls,
		},
		FinishReason: mapOpenAIFinishReason(ar.StopReason),
	}}
	return json.Marshal(resp)
}
