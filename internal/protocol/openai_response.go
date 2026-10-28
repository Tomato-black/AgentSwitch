// 本文件是 OpenAI 上游方向的非流式响应侧：OpenAI chat.completions 响应 →
// Anthropic Messages 响应。
package protocol

import (
	"encoding/json"
	"fmt"
)

// ---------- 非流响应转换：OpenAI → Anthropic ----------

type oaixChoice struct {
	Message struct {
		Content   json.RawMessage `json:"content"`
		ToolCalls []oaixToolCall  `json:"tool_calls"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type oaixResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []oaixChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type anthxOutBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthxResponse struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Role       string          `json:"role"`
	Model      string          `json:"model"`
	Content    []anthxOutBlock `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func mapFinishReason(s string) string {
	switch s {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// OpenAIResponseToAnthropic 转换非流响应（spec §5.3.2）。空 choices（部分
// 聚合站的异常响应）防御性输出 end_turn 空消息，不报错。
func OpenAIResponseToAnthropic(body []byte) ([]byte, error) {
	var or oaixResponse
	if err := json.Unmarshal(body, &or); err != nil {
		return nil, fmt.Errorf("decode upstream body: %w", err)
	}
	resp := anthxResponse{ID: or.ID, Type: "message", Role: "assistant", Model: or.Model}
	resp.Usage.InputTokens = or.Usage.PromptTokens
	resp.Usage.OutputTokens = or.Usage.CompletionTokens
	if len(or.Choices) > 0 {
		ch := or.Choices[0]
		var s string
		if json.Unmarshal(ch.Message.Content, &s) == nil && s != "" {
			resp.Content = append(resp.Content, anthxOutBlock{Type: "text", Text: s})
		}
		for _, tc := range ch.Message.ToolCalls {
			input := map[string]any{}
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
			raw, _ := json.Marshal(input)
			resp.Content = append(resp.Content, anthxOutBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: raw})
		}
		resp.StopReason = mapFinishReason(ch.FinishReason)
	}
	if resp.StopReason == "" {
		resp.StopReason = "end_turn"
	}
	return json.Marshal(resp)
}
