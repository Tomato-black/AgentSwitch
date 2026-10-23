package protocol

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// sseEventData 取出指定 event 名对应的 data JSON。
func sseEventData(t *testing.T, out, event string) map[string]any {
	t.Helper()
	for _, frame := range strings.Split(out, "\n\n") {
		if !strings.HasPrefix(frame, "event: "+event+"\n") {
			continue
		}
		for _, line := range strings.Split(frame, "\n") {
			payload, ok := strings.CutPrefix(line, "data: ")
			if !ok {
				continue
			}
			var m map[string]any
			if err := json.Unmarshal([]byte(payload), &m); err != nil {
				t.Fatalf("invalid %s payload: %v\n%s", event, err, payload)
			}
			return m
		}
	}
	t.Fatalf("missing event %q; output:\n%s", event, out)
	return nil
}

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

// 无文本输出时 output 必须是空数组而不是 JSON null。
func TestChatStreamToResponsesEmptyOutputIsArray(t *testing.T) {
	chat := `data: {"id":"chatcmpl_1","choices":[{"delta":{"role":"assistant"}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	if strings.Contains(out, `"output":null`) {
		t.Fatalf("output must not be null; output:\n%s", out)
	}
	data := sseEventData(t, out, "response.completed")
	resp, _ := data["response"].(map[string]any)
	output, ok := resp["output"].([]any)
	if !ok || len(output) != 0 {
		t.Fatalf("output must be [], got %#v", resp["output"])
	}
}

// [DONE] 到达即收口，不等待上游关闭连接（否则空闲看门狗会把正常完成误判为失败）。
func TestChatStreamToResponsesFinishesOnDONEWithoutEOF(t *testing.T) {
	pr, pw := io.Pipe()
	rc := ChatStreamToResponsesStream(context.Background(), pr, "logical",
		NewResponseToolContext(ResponsesRequest{}))
	defer rc.Close()
	defer pw.Close()
	go func() {
		_, _ = pw.Write([]byte(`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":"hi"}}]}` + "\n\n" +
			`data: [DONE]` + "\n\n"))
		// 故意不关闭写端：模拟上游在 [DONE] 后保持连接。
	}()
	got := make(chan string, 1)
	go func() {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := rc.Read(buf)
			sb.Write(buf[:n])
			if strings.Contains(sb.String(), "event: response.completed") || err != nil {
				got <- sb.String()
				return
			}
		}
	}()
	select {
	case out := <-got:
		if !strings.Contains(out, "event: response.completed") {
			t.Fatalf("stream ended without completed; output:\n%s", out)
		}
	case <-time.After(time.Second):
		t.Fatal("stream did not finish immediately after [DONE]")
	}
}

// 仅 [DONE] 的空流也必须先发 created/in_progress 再 completed。
func TestChatStreamToResponsesDONEOnlyLifecycle(t *testing.T) {
	out := streamEvents(t, "data: [DONE]\n\n")
	pos := 0
	for _, token := range []string{"event: response.created", "event: response.in_progress", "event: response.completed"} {
		idx := strings.Index(out[pos:], token)
		if idx < 0 {
			t.Fatalf("missing %q in order; output:\n%s", token, out)
		}
		pos += idx + len(token)
	}
}

// Fail 也必须补齐 created/in_progress，且 output 为空数组。
func TestChatStreamToResponsesFailEnsuresStarted(t *testing.T) {
	t1 := &chatToResponsesStream{respID: "resp_local", model: "logical"}
	t1.Fail("boom")
	out := string(t1.drain())
	pos := 0
	for _, token := range []string{"event: response.created", "event: response.in_progress", "event: response.failed", `"output":[]`} {
		idx := strings.Index(out[pos:], token)
		if idx < 0 {
			t.Fatalf("missing %q in order; output:\n%s", token, out)
		}
		pos += idx + len(token)
	}
}

// function 工具调用：added（等 call_id 与 name 到齐）→ 参数增量 → done → completed。
func TestChatStreamToResponsesFunctionCall(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{"model":"m","tools":[{"type":"function","name":"exec_command"}]}`))
	tc := NewResponseToolContext(r)
	chat := `data: {"id":"chatcmpl_1","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"exec_command","arguments":""}}]}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	rc := ChatStreamToResponsesStream(context.Background(), strings.NewReader(chat), "m", tc)
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	out := string(b)
	for _, want := range []string{
		`"type":"function_call"`, `"call_id":"call_1"`, `"name":"exec_command"`,
		"response.function_call_arguments.delta", "response.function_call_arguments.done",
		`"arguments":"{\"cmd\":\"ls\"}"`, "event: response.completed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

// 部分上游把 name 与 id 分在不同分片：参数先到必须缓冲，added 时一次性补发，
// 且 added 必须先于任何 arguments.delta 发出。
func TestChatStreamToResponsesToolArgsBufferedUntilName(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{"model":"m","tools":[{"type":"function","name":"exec_command"}]}`))
	tc := NewResponseToolContext(r)
	chat := `data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"exec_command"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := func() string {
		rc := ChatStreamToResponsesStream(context.Background(), strings.NewReader(chat), "m", tc)
		defer rc.Close()
		b, _ := io.ReadAll(rc)
		return string(b)
	}()
	added := strings.Index(out, "event: response.output_item.added")
	delta := strings.Index(out, "response.function_call_arguments.delta")
	if added < 0 || delta < 0 || added > delta {
		t.Fatalf("output_item.added must precede arguments.delta; output:\n%s", out)
	}
	// 缓冲的首片参数经 added 一次性补发，最终 done 携带完整参数。
	if !strings.Contains(out, `"arguments":"{\"cmd\":\"ls\"}"`) {
		t.Fatalf("buffered arguments lost; output:\n%s", out)
	}
}

// 上游在 finish 时仍未给 call_id：必须合成非空 call_id，且先补 added 再收口，
// 不能静默丢弃模型发出的工具调用。
func TestChatStreamToResponsesToolCallIDSynthesized(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{"model":"m","tools":[{"type":"function","name":"exec_command"}]}`))
	tc := NewResponseToolContext(r)
	chat := `data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"exec_command","arguments":"{}"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	rc := ChatStreamToResponsesStream(context.Background(), strings.NewReader(chat), "m", tc)
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	out := string(b)
	if !strings.Contains(out, `"call_id":"call_0"`) {
		t.Fatalf("missing synthesized call_id; output:\n%s", out)
	}
	data := sseEventData(t, out, "response.completed")
	resp, _ := data["response"].(map[string]any)
	output, _ := resp["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("completed output must contain the tool call, got %#v", resp["output"])
	}
}

// namespace 内的子函数：added 与 completed 都必须使用短名 + namespace，
// 不能把拍平后的 chat 名（multi_agent_v1__spawn_agent）泄露给客户端。
func TestChatStreamToResponsesNamespaceToolNames(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{"model":"m","tools":[{"type":"namespace","name":"multi_agent_v1","tools":[{"type":"function","name":"spawn_agent"}]}]}`))
	tc := NewResponseToolContext(r)
	chat := `data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_ns","function":{"name":"multi_agent_v1__spawn_agent","arguments":"{}"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	rc := ChatStreamToResponsesStream(context.Background(), strings.NewReader(chat), "m", tc)
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	out := string(b)

	check := func(event string, item map[string]any) {
		t.Helper()
		if item["name"] != "spawn_agent" || item["namespace"] != "multi_agent_v1" || item["call_id"] != "call_ns" {
			t.Fatalf("%s item must use short name + namespace, got %#v\n%s", event, item, out)
		}
	}
	added := sseEventData(t, out, "response.output_item.added")
	addedItem, _ := added["item"].(map[string]any)
	check("output_item.added", addedItem)

	done := sseEventData(t, out, "response.output_item.done")
	doneItem, _ := done["item"].(map[string]any)
	check("output_item.done", doneItem)

	data := sseEventData(t, out, "response.completed")
	resp, _ := data["response"].(map[string]any)
	output, _ := resp["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("completed output must contain the tool call, got %#v", resp["output"])
	}
	finalItem, _ := output[0].(map[string]any)
	check("response.completed.output", finalItem)
	// id 仍以 call_id 派生，不得使用拍平名。
	if finalItem["id"] != "fc_call_ns" {
		t.Fatalf("item id must derive from call_id, got %#v", finalItem["id"])
	}
}

// 工具先于正文开启时，completed.output 仍必须按 output_index 升序排列，
// 而不是按收口顺序（text 先收口）。
func TestChatStreamToResponsesOutputSortedByIndex(t *testing.T) {
	chat := `data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"exec","arguments":"{}"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"content":"done"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	data := sseEventData(t, out, "response.completed")
	resp, _ := data["response"].(map[string]any)
	output, _ := resp["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("expected tool + message items, got %#v", resp["output"])
	}
	first, _ := output[0].(map[string]any)
	second, _ := output[1].(map[string]any)
	if first["id"] != "fc_call_1" || second["id"] != "resp_c_msg" {
		t.Fatalf("output must be ordered by output_index, got %#v", resp["output"])
	}
}

// custom 与 tool_search 工具：added 用各自的 item 形状，不流式输出参数增量；
// custom 收口解包 {"input":...} 并补发自定义输入事件，tool_search 收口下发参数对象。
func TestChatStreamToResponsesCustomAndToolSearch(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{"model":"m","tools":[
		{"type":"custom","name":"apply_patch"},{"type":"tool_search"}]}`))
	tc := NewResponseToolContext(r)
	chat := `data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_p","function":{"name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_t","function":{"name":"tool_search","arguments":"{\"query\":\"gmail\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	rc := ChatStreamToResponsesStream(context.Background(), strings.NewReader(chat), "m", tc)
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	out := string(b)
	for _, want := range []string{
		`"type":"custom_tool_call"`, `"input":"*** Begin Patch"`,
		"response.custom_tool_call_input.done",
		`"type":"tool_search_call"`, `"query":"gmail"`, "event: response.completed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "response.function_call_arguments.delta") {
		t.Fatalf("custom/tool_search must not stream argument deltas:\n%s", out)
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
