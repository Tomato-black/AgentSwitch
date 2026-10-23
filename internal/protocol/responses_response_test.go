package protocol

import (
	"encoding/json"
	"testing"
)

func TestChatCompletionToResponsesText(t *testing.T) {
	chat := `{"id":"chatcmpl_1","object":"chat.completion","created":123,"model":"up",
		"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,
		         "prompt_tokens_details":{"cached_tokens":4},
		         "completion_tokens_details":{"reasoning_tokens":1}}}`
	tc := NewResponseToolContext(ResponsesRequest{})
	b, err := ChatCompletionToResponses([]byte(chat), "logical", tc)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.Unmarshal(b, &out)
	if out["object"] != "response" || out["status"] != "completed" || out["model"] != "logical" {
		t.Fatalf("envelope: %s", b)
	}
	output := out["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("output: %s", b)
	}
	item := output[0].(map[string]any)
	if item["type"] != "message" {
		t.Fatalf("item: %+v", item)
	}
	part := item["content"].([]any)[0].(map[string]any)
	if part["type"] != "output_text" || part["text"] != "pong" {
		t.Fatalf("part: %+v", part)
	}
	usage := out["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(2) {
		t.Fatalf("usage: %+v", usage)
	}
	if usage["input_tokens_details"].(map[string]any)["cached_tokens"] != float64(4) {
		t.Fatalf("cached: %+v", usage)
	}
	if usage["output_tokens_details"].(map[string]any)["reasoning_tokens"] != float64(1) {
		t.Fatalf("reasoning: %+v", usage)
	}
}

func TestChatCompletionToResponsesReasoningContent(t *testing.T) {
	chat := `{"id":"r1","created":2,"choices":[{"message":{"role":"assistant","content":"answer",
		"reasoning_content":"think"},"finish_reason":"stop"}]}`
	b, err := ChatCompletionToResponses([]byte(chat), "m", NewResponseToolContext(ResponsesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	assertReasoningItem(t, b, "think")
}

func TestChatCompletionToResponsesReasoningFallback(t *testing.T) {
	chat := `{"id":"r2","created":2,"choices":[{"message":{"role":"assistant","content":"answer",
		"reasoning":"think"},"finish_reason":"stop"}]}`
	b, err := ChatCompletionToResponses([]byte(chat), "m", NewResponseToolContext(ResponsesRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	assertReasoningItem(t, b, "think")
}

// assertReasoningItem 断言 output 中存在 reasoning 项，且 summary[0] 为 summary_text。
func assertReasoningItem(t *testing.T, b []byte, want string) {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("invalid json: %v: %s", err, b)
	}
	output, ok := out["output"].([]any)
	if !ok {
		t.Fatalf("output: %s", b)
	}
	for _, raw := range output {
		item := raw.(map[string]any)
		if item["type"] != "reasoning" {
			continue
		}
		summary, ok := item["summary"].([]any)
		if !ok || len(summary) == 0 {
			t.Fatalf("summary: %+v", item)
		}
		part := summary[0].(map[string]any)
		if part["type"] != "summary_text" || part["text"] != want {
			t.Fatalf("summary part: %+v", part)
		}
		return
	}
	t.Fatalf("no reasoning item: %s", b)
}

func TestChatCompletionToResponsesLengthIncomplete(t *testing.T) {
	chat := `{"id":"c","created":1,"choices":[{"message":{"role":"assistant","content":"x"},"finish_reason":"length"}]}`
	b, _ := ChatCompletionToResponses([]byte(chat), "m", NewResponseToolContext(ResponsesRequest{}))
	var out map[string]any
	json.Unmarshal(b, &out)
	if out["status"] != "incomplete" {
		t.Fatalf("status: %s", b)
	}
	details, ok := out["incomplete_details"].(map[string]any)
	if !ok || details["reason"] != "max_output_tokens" {
		t.Fatalf("incomplete_details: %s", b)
	}
}

func TestChatCompletionToResponsesUsageTotalFallback(t *testing.T) {
	chat := `{"id":"u","created":3,"choices":[{"message":{"role":"assistant","content":"x"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":3,"completion_tokens":2,
		         "completion_tokens_details":{"reasoning_tokens":1}}}`
	b, _ := ChatCompletionToResponses([]byte(chat), "m", NewResponseToolContext(ResponsesRequest{}))
	var out map[string]any
	json.Unmarshal(b, &out)
	usage := out["usage"].(map[string]any)
	if usage["total_tokens"] != float64(5) {
		t.Fatalf("total fallback: %+v", usage)
	}
	if usage["output_tokens_details"].(map[string]any)["reasoning_tokens"] != float64(1) {
		t.Fatalf("reasoning: %+v", usage)
	}
}
