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
