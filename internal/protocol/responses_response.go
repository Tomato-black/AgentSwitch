package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// chatResponse 是 chat.completions 响应的解析视图，仅保留转换为 Responses
// 对象所需的字段。
type chatResponse struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
	Choices []struct {
		Message struct {
			Content          *string        `json:"content"`
			ReasoningContent string         `json:"reasoning_content"`
			Reasoning        string         `json:"reasoning"`
			ToolCalls        []chatToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatUsage struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// ChatCompletionToResponses 把上游 chat.completions 非流式响应转换为 Responses
// 响应对象。requestedModel 是客户端请求的逻辑模型名，回写给客户端；上游模型名
// 不外泄。
func ChatCompletionToResponses(chatBody []byte, requestedModel string, tc *ResponseToolContext) ([]byte, error) {
	var cr chatResponse
	if err := json.Unmarshal(chatBody, &cr); err != nil {
		return nil, fmt.Errorf("invalid chat completion: %w", err)
	}
	id := responseIDFromChatID(cr.ID)
	status := "completed"
	incomplete := ""
	if len(cr.Choices) > 0 && cr.Choices[0].FinishReason == "length" {
		status, incomplete = "incomplete", "max_output_tokens"
	}
	var output []map[string]any
	if len(cr.Choices) > 0 {
		output = chatMessageToResponseItems(cr.Choices[0].Message.Content, cr.Choices[0].Message.ReasoningContent,
			cr.Choices[0].Message.Reasoning, cr.Choices[0].Message.ToolCalls, id, tc)
	}
	resp := responsesResponseObject(id, requestedModel, status, cr.Created, output, chatUsageToResponsesUsage(cr.Usage))
	if incomplete != "" {
		resp["incomplete_details"] = map[string]any{"reason": incomplete}
	}
	return json.Marshal(resp)
}

// responseIDFromChatID 把 chat 侧响应 ID 映射为 Responses 侧 ID；空 ID 使用
// 固定本地 ID，避免下游拿到空字符串。
func responseIDFromChatID(id string) string {
	if id == "" {
		return "resp_local"
	}
	return "resp_" + strings.TrimLeft(strings.ReplaceAll(id, " ", "_"), "_")
}

func responsesResponseObject(id, model, status string, createdAt int64, output []map[string]any, usage map[string]any) map[string]any {
	if output == nil {
		output = []map[string]any{}
	}
	return map[string]any{
		"id": id, "object": "response", "created_at": createdAt,
		"status": status, "model": model, "output": output, "usage": usage,
	}
}

// chatUsageToResponsesUsage 映射 token 统计。total 缺失（0）时按 in+out 补齐。
func chatUsageToResponsesUsage(u *chatUsage) map[string]any {
	var in, out, total, cached, reasoning int
	if u != nil {
		in, out, total = u.PromptTokens, u.CompletionTokens, u.TotalTokens
		if u.PromptTokensDetails != nil {
			cached = u.PromptTokensDetails.CachedTokens
		}
		if u.CompletionTokensDetails != nil {
			reasoning = u.CompletionTokensDetails.ReasoningTokens
		}
	}
	if total == 0 {
		total = in + out
	}
	return map[string]any{
		"input_tokens": in, "input_tokens_details": map[string]any{"cached_tokens": cached},
		"output_tokens": out, "output_tokens_details": map[string]any{"reasoning_tokens": reasoning},
		"total_tokens": total,
	}
}

// chatMessageToResponseItems 把 chat 助手消息转为 Responses output 项。
// 文本与 reasoning 之后，按 ResponseToolContext 记录的原始工具语义还原
// function / custom / tool_search 三类工具调用项。
func chatMessageToResponseItems(content *string, reasoningContent, reasoning string,
	calls []chatToolCall, respID string, tc *ResponseToolContext) []map[string]any {
	var out []map[string]any
	if content != nil && *content != "" {
		out = append(out, map[string]any{
			"id": respID + "_msg", "type": "message", "status": "completed",
			"role":    "assistant",
			"content": []map[string]any{{"type": "output_text", "text": *content, "annotations": []any{}}},
		})
	}
	text := reasoningContent
	if text == "" {
		text = reasoning
	}
	if text != "" {
		out = append(out, map[string]any{
			"id": "rs_" + respID, "type": "reasoning",
			"summary": []map[string]any{{"type": "summary_text", "text": text}},
		})
	}
	for _, call := range calls {
		name := call.Function.Name
		spec, ok := tc.Spec(name)
		if !ok {
			// 未登记的工具名按普通 function 透传，避免丢调用。
			spec = responseToolSpec{Kind: responseToolFunction, Name: name}
		}
		callID := call.ID
		if callID == "" {
			callID = "call_" + fmt.Sprint(len(out))
		}
		switch spec.Kind {
		case responseToolCustom:
			out = append(out, map[string]any{
				"id": "ctc_" + callID, "type": "custom_tool_call", "status": "completed",
				"call_id": callID, "name": spec.Name, "input": customToolInput(call.Function.Arguments),
			})
		case responseToolSearch:
			out = append(out, map[string]any{
				"id": "ts_" + callID, "type": "tool_search_call", "status": "completed",
				"call_id": callID, "execution": "client",
				"arguments": parseToolArgumentsObject(call.Function.Arguments),
			})
		default:
			item := map[string]any{
				"id": "fc_" + callID, "type": "function_call", "status": "completed",
				"call_id": callID, "name": spec.Name, "arguments": call.Function.Arguments,
			}
			if spec.Namespace != "" {
				item["namespace"] = spec.Namespace
			}
			out = append(out, item)
		}
	}
	return out
}

// customToolInput 从 {"input":"..."} 解包自由文本；解析失败时原样返回。
func customToolInput(arguments string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(arguments), &obj) == nil {
		if s, ok := obj[customToolInputField].(string); ok {
			return s
		}
	}
	return arguments
}

// parseToolArgumentsObject 把参数 JSON 解析为对象；失败时退化为 {"query": 原文}。
func parseToolArgumentsObject(arguments string) map[string]any {
	var obj map[string]any
	if json.Unmarshal([]byte(arguments), &obj) == nil && obj != nil {
		return obj
	}
	return map[string]any{"query": arguments}
}
