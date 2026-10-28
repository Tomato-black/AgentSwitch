// Package protocol 是 Agent 入站协议与上游协议之间的纯转换层：Anthropic
// Messages ↔ OpenAI Chat/Responses 的请求/响应/流式转换（含工具调用映射），
// 不持可变状态、不做网络 IO（由 provider/gateway 调用）。
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	ErrTypeInvalidRequest = "invalid_request_error"
	ErrTypeAuth           = "authentication_error"
	ErrTypeNotFound       = "not_found_error"
	ErrTypeAPI            = "api_error"
)

type ChatRequest struct {
	Model    string            `json:"model"`
	Stream   bool              `json:"stream"`
	Messages []json.RawMessage `json:"messages"`
}

func ParseChatRequest(body []byte) (ChatRequest, error) {
	var cr ChatRequest
	if err := json.Unmarshal(body, &cr); err != nil {
		return ChatRequest{}, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
	}
	if cr.Model == "" {
		return ChatRequest{}, errors.New(ErrTypeInvalidRequest + ": model is required")
	}
	if len(cr.Messages) == 0 {
		return ChatRequest{}, errors.New(ErrTypeInvalidRequest + ": messages is required")
	}
	return cr, nil
}

// RewriteModel 只替换顶层 model 字段，保留其余字段（messages/tools/stream 等）。
func RewriteModel(body []byte, upstream string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["model"] = json.RawMessage(`"` + jsonEscape(upstream) + `"`)
	return json.Marshal(m)
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func ErrorEnvelope(errType, message string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": errType, "message": message},
	})
	return b
}
