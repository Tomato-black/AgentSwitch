package protocol

import "encoding/json"

// ExtractUsage 从 Anthropic/OpenAI Chat/Responses 非流式响应体中提取 token。
// 只读 usage 字段；缺失或全 0 返回 ok=false。流式响应暂不使用本函数。
func ExtractUsage(body []byte) (in, out int, ok bool) {
	var env struct {
		Usage *struct {
			InputTokens      int `json:"input_tokens"`
			OutputTokens     int `json:"output_tokens"`
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Usage == nil {
		return 0, 0, false
	}
	in, out = env.Usage.InputTokens, env.Usage.OutputTokens
	if env.Usage.PromptTokens > in {
		in = env.Usage.PromptTokens
	}
	if env.Usage.CompletionTokens > out {
		out = env.Usage.CompletionTokens
	}
	if in == 0 && out == 0 {
		return 0, 0, false
	}
	return in, out, true
}
