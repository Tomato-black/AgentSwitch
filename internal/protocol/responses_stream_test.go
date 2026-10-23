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
	// added item 形状必须还原原始工具语义，不能退化为 function_call。
	if !strings.Contains(out, `"input":""`) {
		t.Fatalf("custom added item must carry empty input:\n%s", out)
	}
	for _, want := range []string{`"execution":"client"`, `"arguments":{}`} {
		if !strings.Contains(out, want) {
			t.Fatalf("tool_search added item missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "response.function_call_arguments.delta") {
		t.Fatalf("custom/tool_search must not stream argument deltas:\n%s", out)
	}
}

// namespace 子函数不属于 custom/tool_search，参数增量必须继续流式下发
// （多分片到达时逐片转发），收口 item 用短名 + namespace。
func TestChatStreamToResponsesNamespaceToolStreamsArgumentDeltas(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{"model":"m","tools":[{"type":"namespace","name":"multi_agent_v1","tools":[{"type":"function","name":"spawn_agent"}]}]}`))
	tc := NewResponseToolContext(r)
	chat := `data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_ns","function":{"name":"multi_agent_v1__spawn_agent","arguments":"{\"task\":"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"explore\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	rc := ChatStreamToResponsesStream(context.Background(), strings.NewReader(chat), "m", tc)
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	out := string(b)

	if n := strings.Count(out, "event: response.function_call_arguments.delta"); n != 2 {
		t.Fatalf("expected 2 argument deltas, got %d:\n%s", n, out)
	}
	for _, want := range []string{`"delta":"{\"task\":"`, `"delta":"\"explore\"}"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing delta fragment %q:\n%s", want, out)
		}
	}
	data := sseEventData(t, out, "response.completed")
	resp, _ := data["response"].(map[string]any)
	output, _ := resp["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("completed output must contain the tool call, got %#v", resp["output"])
	}
	finalItem, _ := output[0].(map[string]any)
	if finalItem["name"] != "spawn_agent" || finalItem["namespace"] != "multi_agent_v1" {
		t.Fatalf("completed item must use short name + namespace, got %#v", finalItem)
	}
	if finalItem["arguments"] != `{"task":"explore"}` {
		t.Fatalf("completed item arguments mismatch, got %#v", finalItem["arguments"])
	}
}

// reasoning 增量先于正文：必须开启 reasoning summary 项、逐片下发 delta，
// 正文开始时收口；completed.output 按 output_index 同时包含 reasoning 与 message。
func TestChatStreamToResponsesReasoning(t *testing.T) {
	chat := `data: {"id":"c","choices":[{"delta":{"reasoning_content":"think"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"content":"answer"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	for _, want := range []string{
		`"type":"reasoning"`, "response.reasoning_summary_text.delta", `"delta":"think"`,
		"response.reasoning_summary_text.done", "response.reasoning_summary_part.done",
		"event: response.completed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}

	data := sseEventData(t, out, "response.completed")
	resp, _ := data["response"].(map[string]any)
	output, _ := resp["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("completed output must contain reasoning + message, got %#v", resp["output"])
	}
	reasoning, _ := output[0].(map[string]any)
	if reasoning["type"] != "reasoning" || reasoning["id"] != "rs_resp_c" {
		t.Fatalf("first item must be the reasoning item, got %#v", reasoning)
	}
	if _, ok := reasoning["status"]; ok {
		t.Fatalf("completed reasoning item must not carry status, got %#v", reasoning)
	}
	summary, _ := reasoning["summary"].([]any)
	if len(summary) != 1 {
		t.Fatalf("reasoning summary must hold one part, got %#v", reasoning["summary"])
	}
	part, _ := summary[0].(map[string]any)
	if part["type"] != "summary_text" || part["text"] != "think" {
		t.Fatalf("reasoning summary part mismatch, got %#v", part)
	}
	if second, _ := output[1].(map[string]any); second["type"] != "message" {
		t.Fatalf("second item must be the message, got %#v", second)
	}
}

// 正文先于 reasoning 到达：pushReasoning 先收口文本项，reasoning 在 Finish 时收口；
// 两个 item 都必须保留并按 output_index 排列（message 在前、reasoning 在后）。
func TestChatStreamToResponsesReasoningAfterText(t *testing.T) {
	chat := `data: {"id":"c","choices":[{"delta":{"content":"answer"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"reasoning_content":"think"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	if strings.Contains(out, "response.failed") {
		t.Fatalf("interleaved stream must not fail:\n%s", out)
	}
	if n := strings.Count(out, "event: response.reasoning_summary_text.done"); n != 1 {
		t.Fatalf("reasoning must be closed exactly once, got %d:\n%s", n, out)
	}
	data := sseEventData(t, out, "response.completed")
	resp, _ := data["response"].(map[string]any)
	output, _ := resp["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("completed output must keep both items, got %#v", resp["output"])
	}
	first, _ := output[0].(map[string]any)
	second, _ := output[1].(map[string]any)
	if first["id"] != "resp_c_msg" || second["id"] != "rs_resp_c" {
		t.Fatalf("output must be ordered by output_index, got %#v", resp["output"])
	}
}

// reasoning → 正文 → reasoning：第二段 reasoning 到达时首段已收口，
// 迟到的 delta 必须丢弃（既不能下发、也不能只存在于流事件而不在 completed 快照里）。
func TestChatStreamToResponsesLateReasoningDeltaDropped(t *testing.T) {
	chat := `data: {"id":"c","choices":[{"delta":{"reasoning_content":"think"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"content":"answer"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"reasoning_content":"late"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	if n := strings.Count(out, "event: response.reasoning_summary_text.delta"); n != 1 {
		t.Fatalf("late reasoning delta must be dropped, got %d deltas:\n%s", n, out)
	}
	if strings.Contains(out, `"delta":"late"`) {
		t.Fatalf("late reasoning delta leaked into stream:\n%s", out)
	}

	data := sseEventData(t, out, "response.completed")
	resp, _ := data["response"].(map[string]any)
	output, _ := resp["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("completed output must hold one reasoning + one message, got %#v", resp["output"])
	}
	reasoning, _ := output[0].(map[string]any)
	if reasoning["type"] != "reasoning" || reasoning["id"] != "rs_resp_c" {
		t.Fatalf("first item must be the reasoning item, got %#v", reasoning)
	}
	summary, _ := reasoning["summary"].([]any)
	part, _ := summary[0].(map[string]any)
	if part["text"] != "think" {
		t.Fatalf("reasoning snapshot must keep only the closed text, got %#v", part)
	}
	message, _ := output[1].(map[string]any)
	content, _ := message["content"].([]any)
	textPart, _ := content[0].(map[string]any)
	if message["type"] != "message" || textPart["text"] != "answer" {
		t.Fatalf("second item must be the message, got %#v", message)
	}
}

// 正文 → reasoning → 正文：第二段正文到达时首条 message 已收口，
// 迟到的文本 delta 必须丢弃，completed 快照与流事件保持一致。
func TestChatStreamToResponsesLateTextDeltaDropped(t *testing.T) {
	chat := `data: {"id":"c","choices":[{"delta":{"content":"first"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"reasoning_content":"think"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"content":"late"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	if n := strings.Count(out, "event: response.output_text.delta"); n != 1 {
		t.Fatalf("late text delta must be dropped, got %d deltas:\n%s", n, out)
	}
	if strings.Contains(out, `"delta":"late"`) {
		t.Fatalf("late text delta leaked into stream:\n%s", out)
	}

	data := sseEventData(t, out, "response.completed")
	resp, _ := data["response"].(map[string]any)
	output, _ := resp["output"].([]any)
	if len(output) != 2 {
		t.Fatalf("completed output must hold one message + one reasoning, got %#v", resp["output"])
	}
	message, _ := output[0].(map[string]any)
	content, _ := message["content"].([]any)
	textPart, _ := content[0].(map[string]any)
	if message["id"] != "resp_c_msg" || textPart["text"] != "first" {
		t.Fatalf("message snapshot must keep only the closed text, got %#v", message)
	}
	if reasoning, _ := output[1].(map[string]any); reasoning["id"] != "rs_resp_c" {
		t.Fatalf("second item must be the reasoning item, got %#v", output[1])
	}
}

// reasoning 仍在流式中时上游截断：失败收尾必须先补齐 reasoning done 事件，
// 且 response.failed 的 output 保留已收口的 reasoning item。
func TestChatStreamToResponsesFailClosesOpenReasoning(t *testing.T) {
	chat := `data: {"id":"c","choices":[{"delta":{"reasoning_content":"think"}}]}` + "\n\n"
	out := streamEvents(t, chat)
	if !strings.Contains(out, "event: response.failed") {
		t.Fatalf("truncated reasoning stream must fail:\n%s", out)
	}
	doneIdx := strings.Index(out, "event: response.reasoning_summary_text.done")
	failIdx := strings.Index(out, "event: response.failed")
	if doneIdx < 0 || failIdx < 0 || doneIdx > failIdx {
		t.Fatalf("reasoning must be closed before response.failed:\n%s", out)
	}

	data := sseEventData(t, out, "response.failed")
	resp, _ := data["response"].(map[string]any)
	if resp["status"] != "failed" {
		t.Fatalf("failed response status mismatch: %#v", resp["status"])
	}
	output, _ := resp["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("failed output must contain the closed reasoning item, got %#v", resp["output"])
	}
	item, _ := output[0].(map[string]any)
	summary, _ := item["summary"].([]any)
	part, _ := summary[0].(map[string]any)
	if item["type"] != "reasoning" || part["text"] != "think" {
		t.Fatalf("failed reasoning item mismatch, got %#v", item)
	}
}

// 兼容只给 delta.reasoning（无 _content 后缀）的上游：同样按 reasoning 流式下发。
func TestChatStreamToResponsesReasoningFallbackField(t *testing.T) {
	chat := `data: {"id":"c","choices":[{"delta":{"reasoning":"why"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	if !strings.Contains(out, `"delta":"why"`) || !strings.Contains(out, "response.reasoning_summary_text.done") {
		t.Fatalf("delta.reasoning fallback must stream as reasoning:\n%s", out)
	}
	if strings.Contains(out, "response.failed") {
		t.Fatalf("reasoning stream must not fail:\n%s", out)
	}
}

// 上游在流中回 error chunk：必须立即发 response.failed 并带上游错误信息；
// 其后的 [DONE] 不得把失败流改写成 completed。
func TestChatStreamToResponsesErrorChunkEmitsFailed(t *testing.T) {
	chat := `data: {"id":"c","choices":[{"delta":{"content":"half"}}]}` + "\n\n" +
		`data: {"error":{"message":"upstream exploded","type":"server_error"}}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	if !strings.Contains(out, "event: response.failed") {
		t.Fatalf("error chunk must emit response.failed:\n%s", out)
	}
	if strings.Contains(out, "event: response.completed") {
		t.Fatalf("error chunk must never complete:\n%s", out)
	}
	if !strings.Contains(out, "upstream exploded") {
		t.Fatalf("upstream error message must surface:\n%s", out)
	}
	// 已输出文本随失败收口一并下发（不是空 output）。
	if !strings.Contains(out, `"delta":"half"`) {
		t.Fatalf("already-emitted delta must be preserved:\n%s", out)
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
