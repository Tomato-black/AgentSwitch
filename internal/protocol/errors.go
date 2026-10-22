package protocol

import "encoding/json"

// AnthropicErrorEvent 构造 Anthropic SSE error 事件（流已提交后的唯一错误通道）。
func AnthropicErrorEvent(msg string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "api_error", "message": msg},
	})
	return []byte("event: error\ndata: " + string(b) + "\n\n")
}

// OpenAIStreamErrorChunk 构造 OpenAI 流式 error chunk（截断后不补 [DONE]）。
func OpenAIStreamErrorChunk(msg string) []byte {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]any{"message": msg, "type": "api_error"},
	})
	return []byte("data: " + string(b) + "\n\n")
}
