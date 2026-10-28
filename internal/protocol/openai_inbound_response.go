// 本文件是 OpenAI 入站方向的非流式响应侧：Anthropic Messages 响应 →
// OpenAI chat.completions 响应，以及 OpenAI 错误体构造。
package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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

// ---------- 流式转换：Anthropic SSE → OpenAI chunk SSE ----------

// OpenAIErrorBody 构造 chat.completions 入站的错误体（OpenAI error 形状：
// {"error":{"message","type"}}），与 ErrorEnvelope（Anthropic 形状）对称——
// 错误 envelope 形状随入站协议而异。
func OpenAIErrorBody(errType, message string) []byte {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]string{"message": message, "type": errType},
	})
	return b
}
