package protocol

import (
	"context"
	"io"
	"strings"
	"testing"
)

func streamEvents(t *testing.T, chatSSE string) string {
	t.Helper()
	rc := ChatStreamToResponsesStream(context.Background(), strings.NewReader(chatSSE), "logical",
		NewResponseToolContext(ResponsesRequest{}))
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func TestChatStreamToResponsesText(t *testing.T) {
	chat := `data: {"id":"chatcmpl_1","created":9,"choices":[{"delta":{"role":"assistant"}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":"po"}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":"ng"}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	want := []string{
		"event: response.created",
		"event: response.in_progress",
		"event: response.output_item.added",
		"event: response.content_part.added",
		`"delta":"po"`,
		`"delta":"ng"`,
		"event: response.output_text.done",
		"event: response.content_part.done",
		"event: response.output_item.done",
		"event: response.completed",
	}
	pos := 0
	for _, token := range want {
		idx := strings.Index(out[pos:], token)
		if idx < 0 {
			t.Fatalf("missing %q in order; output:\n%s", token, out)
		}
		pos += idx + len(token)
	}
	if strings.Contains(out, "response.failed") {
		t.Fatalf("normal stream must not fail:\n%s", out)
	}
}

// 上游模型名不得外泄：响应对象的 model 必须回显客户端请求的逻辑模型名。
func TestChatStreamToResponsesEchoesRequestedModel(t *testing.T) {
	chat := `data: {"id":"chatcmpl_1","model":"upstream-secret","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	if !strings.Contains(out, `"model":"logical"`) {
		t.Fatalf("model must echo requested logical name; output:\n%s", out)
	}
	if strings.Contains(out, "upstream-secret") {
		t.Fatalf("upstream model leaked; output:\n%s", out)
	}
}

// 上游无终止标记即结束（截断）时不得发 response.completed，必须发失败事件。
func TestChatStreamToResponsesTruncatedFails(t *testing.T) {
	chat := `data: {"id":"chatcmpl_1","choices":[{"delta":{"content":"partial"}}]}` + "\n\n"
	out := streamEvents(t, chat)
	if strings.Contains(out, "response.completed") {
		t.Fatalf("truncated stream must not complete:\n%s", out)
	}
	if !strings.Contains(out, "event: response.failed") {
		t.Fatalf("truncated stream must fail:\n%s", out)
	}
	if !strings.Contains(out, `"delta":"partial"`) {
		t.Fatalf("already-emitted delta must be preserved:\n%s", out)
	}
}
