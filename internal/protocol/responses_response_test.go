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
}

func TestChatCompletionToResponsesLengthIncomplete(t *testing.T) {
	chat := `{"id":"c","created":1,"choices":[{"message":{"role":"assistant","content":"x"},"finish_reason":"length"}]}`
	b, _ := ChatCompletionToResponses([]byte(chat), "m", NewResponseToolContext(ResponsesRequest{}))
	var out map[string]any
	json.Unmarshal(b, &out)
	if out["status"] != "incomplete" {
		t.Fatalf("status: %s", b)
	}
}
