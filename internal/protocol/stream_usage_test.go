package protocol

import (
	"strings"
	"testing"
)

func TestStreamUsageTrackerAnthropic(t *testing.T) {
	tr := &StreamUsageTracker{}
	// 分片写入，覆盖跨 Write 的半行。
	_, _ = tr.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_to"))
	_, _ = tr.Write([]byte("kens\":11}}}\n\n"))
	_, _ = tr.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":7}}\n\n"))
	tr.Flush()
	in, out := tr.Usage()
	if in != 11 || out != 7 {
		t.Fatalf("usage = %d/%d", in, out)
	}
}

func TestStreamUsageTrackerOpenAIAndResponses(t *testing.T) {
	tr := &StreamUsageTracker{}
	_, _ = tr.Write([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":3}}\n\n"))
	_, _ = tr.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"usage\":{\"input_tokens\":9,\"output_tokens\":4}}\n\n"))
	tr.Flush()
	in, out := tr.Usage()
	if in != 9 || out != 4 {
		t.Fatalf("usage = %d/%d", in, out)
	}
}

func TestEnableChatStreamUsage(t *testing.T) {
	in := []byte(`{"model":"m","stream":true}`)
	out := EnableChatStreamUsage(in)
	if !strings.Contains(string(out), `"include_usage":true`) {
		t.Fatalf("out = %s", out)
	}
	// 非法 JSON 原样返回。
	bad := []byte(`{broken`)
	if string(EnableChatStreamUsage(bad)) != string(bad) {
		t.Fatal("invalid body must be unchanged")
	}
}
