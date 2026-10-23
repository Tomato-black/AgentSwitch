package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseResponsesRequestCodexShape(t *testing.T) {
	body := `{"model":"my-code","stream":true,"instructions":"be brief",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}],
		"tool_choice":"auto","max_output_tokens":256,"temperature":0.2}`
	r, err := ParseResponsesRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != "my-code" || !r.Stream || r.Instructions != "be brief" {
		t.Fatalf("top-level fields: %+v", r)
	}
	if len(r.Tools) != 1 || r.MaxOutputTokens == nil || *r.MaxOutputTokens != 256 {
		t.Fatalf("tools/limits: %+v", r)
	}
	if r.Temperature == nil || *r.Temperature != 0.2 {
		t.Fatalf("temperature: %+v", r)
	}
}

func TestParseResponsesRequestRejectsMissingModel(t *testing.T) {
	_, err := ParseResponsesRequest([]byte(`{"input":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("want model error, got %v", err)
	}
}

func TestParseResponsesRequestRejectsPreviousResponseID(t *testing.T) {
	_, err := ParseResponsesRequest([]byte(`{"model":"m","input":"hi","previous_response_id":"resp_1"}`))
	if err == nil || !strings.Contains(err.Error(), "previous_response_id") {
		t.Fatalf("want previous_response_id error, got %v", err)
	}
}

func TestParseResponsesRequestAllowsNullPreviousResponseID(t *testing.T) {
	if _, err := ParseResponsesRequest([]byte(`{"model":"m","input":"hi","previous_response_id":null}`)); err != nil {
		t.Fatalf("null previous_response_id must be accepted: %v", err)
	}
}

func TestResponsesInputMessages(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{
		"model":"m","instructions":"system rules",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"dev rules"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:x"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"previous"}]}
		]}`))
	tc := NewResponseToolContext(r)
	msgs, err := responsesInputToMessages(r.Input, r.Instructions, tc)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages: %+v", msgs)
	}
	if msgs[0]["role"] != "system" || msgs[0]["content"] != "system rules\n\ndev rules" {
		t.Fatalf("system merge: %+v", msgs[0])
	}
	if msgs[1]["role"] != "user" || msgs[1]["content"] != "hello" {
		t.Fatalf("user text: %+v", msgs[1])
	}
	if msgs[2]["role"] != "assistant" || msgs[2]["content"] != "previous" {
		t.Fatalf("assistant text: %+v", msgs[2])
	}
}

func TestResponsesInputToolHistory(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{
		"model":"m",
		"tools":[{"type":"custom","name":"apply_patch"},
		         {"type":"namespace","name":"ns","tools":[{"type":"function","name":"run"}]}],
		"input":[
			{"type":"message","role":"user","content":"hi"},
			{"type":"function_call","call_id":"c1","name":"ns__run","namespace":"ns","arguments":"{\"a\":1}"},
			{"type":"function_call","call_id":"c2","name":"apply_patch","arguments":"{}"},
			{"type":"function_call_output","call_id":"c1","output":"ok"},
			{"type":"custom_tool_call","call_id":"c2","name":"apply_patch","input":"*** Begin Patch"},
			{"type":"custom_tool_call_output","call_id":"c2","output":"done"},
			{"type":"reasoning","summary":[]},
			{"type":"function_call_output","call_id":"missing","output":"orphan"}
		]}`))
	tc := NewResponseToolContext(r)
	msgs, err := responsesInputToMessages(r.Input, r.Instructions, tc)
	if err != nil {
		t.Fatal(err)
	}
	// user, assistant(tool_calls c1+c2), tool c1, assistant(tool_calls custom c2), tool c2, user(orphan)
	if len(msgs) != 6 {
		t.Fatalf("messages: %d %+v", len(msgs), msgs)
	}
	asst := msgs[1]
	calls := asst["tool_calls"].([]map[string]any)
	if len(calls) != 2 || calls[0]["id"] != "c1" {
		t.Fatalf("merged tool calls: %+v", calls)
	}
	fn := calls[0]["function"].(map[string]any)
	if fn["name"] != "ns__run" {
		t.Fatalf("namespace name must stay flattened on history replay: %+v", fn)
	}
	if msgs[2]["role"] != "tool" || msgs[2]["tool_call_id"] != "c1" {
		t.Fatalf("tool result: %+v", msgs[2])
	}
	if msgs[2]["content"] != "ok" {
		t.Fatalf("tool result content: %+v", msgs[2])
	}
	customAsst := msgs[3]
	customCalls := customAsst["tool_calls"].([]map[string]any)
	customFn := customCalls[0]["function"].(map[string]any)
	var customArgs map[string]any
	if err := json.Unmarshal([]byte(customFn["arguments"].(string)), &customArgs); err != nil {
		t.Fatalf("custom arguments must be JSON: %v (%+v)", err, customFn)
	}
	if len(customArgs) != 1 || customArgs[customToolInputField] != "*** Begin Patch" {
		t.Fatalf("custom arguments must wrap input field: %+v", customArgs)
	}
	if msgs[4]["role"] != "tool" || msgs[4]["tool_call_id"] != "c2" {
		t.Fatalf("custom tool result: %+v", msgs[4])
	}
	if msgs[4]["content"] != "done" {
		t.Fatalf("custom tool result content: %+v", msgs[4])
	}
	orphan, _ := msgs[5]["content"].(string)
	if msgs[5]["role"] != "user" || !strings.Contains(orphan, "without a matching tool_call") {
		t.Fatalf("orphan output: %+v", msgs[5])
	}
}

func TestResponsesInputToolSearchHistory(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{
		"model":"m",
		"tools":[{"type":"tool_search"}],
		"input":[
			{"type":"tool_search_call","call_id":"s1","arguments":{"query":"git"}},
			{"type":"tool_search_output","call_id":"s1","output":[{"type":"input_text","text":"found"}]}
		]}`))
	tc := NewResponseToolContext(r)
	msgs, err := responsesInputToMessages(r.Input, r.Instructions, tc)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 {
		t.Fatalf("messages: %d %+v", len(msgs), msgs)
	}
	calls := msgs[0]["tool_calls"].([]map[string]any)
	if len(calls) != 1 || calls[0]["id"] != "s1" {
		t.Fatalf("tool_search call: %+v", calls)
	}
	fn := calls[0]["function"].(map[string]any)
	if fn["name"] != "tool_search" {
		t.Fatalf("tool_search name: %+v", fn)
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(fn["arguments"].(string)), &args); err != nil {
		t.Fatalf("tool_search arguments must be JSON: %v (%+v)", err, fn)
	}
	if args["query"] != "git" {
		t.Fatalf("tool_search arguments must pass through: %+v", args)
	}
	if msgs[1]["role"] != "tool" || msgs[1]["tool_call_id"] != "s1" {
		t.Fatalf("tool_search result: %+v", msgs[1])
	}
}

func TestResponsesInputRejectsItemReference(t *testing.T) {
	_, err := responsesInputToMessages([]byte(`[{"type":"item_reference","id":"x"}]`), "", NewResponseToolContext(ResponsesRequest{}))
	if err == nil {
		t.Fatal("item_reference must be rejected")
	}
}

func TestResponsesInputString(t *testing.T) {
	msgs, err := responsesInputToMessages([]byte(`"hi"`), "", NewResponseToolContext(ResponsesRequest{}))
	if err != nil || len(msgs) != 1 || msgs[0]["role"] != "user" {
		t.Fatalf("string input: %+v err=%v", msgs, err)
	}
}

func TestResponsesRequestToChat(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{
		"model":"logical","stream":true,"instructions":"sys","max_output_tokens":512,
		"temperature":0.3,"top_p":0.9,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"exec_command"},
		"input":[{"type":"message","role":"user","content":"hi"}],
		"store":false,"reasoning":{"effort":"high"},"include":["reasoning.encrypted_content"]}`))
	tc := NewResponseToolContext(r)
	b, err := ResponsesRequestToChat(r, tc, "upstream-model")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["model"] != "upstream-model" || out["stream"] != true {
		t.Fatalf("model/stream: %+v", out)
	}
	if out["max_tokens"] != float64(512) || out["temperature"] != 0.3 || out["top_p"] != 0.9 {
		t.Fatalf("limits: %+v", out)
	}
	if _, ok := out["store"]; ok {
		t.Fatalf("store must not be forwarded: %+v", out)
	}
	if _, ok := out["reasoning"]; ok {
		t.Fatalf("reasoning must not be forwarded: %+v", out)
	}
	choice := out["tool_choice"].(map[string]any)
	if choice["function"].(map[string]any)["name"] != "exec_command" {
		t.Fatalf("tool_choice: %+v", choice)
	}
	msgs := out["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("messages: %+v", msgs)
	}
}

func TestMapResponsesToolChoice(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{
		"model":"m",
		"tools":[{"type":"custom","name":"apply_patch"},{"type":"tool_search"},
		         {"type":"namespace","name":"ns","tools":[{"type":"function","name":"run"}]}]}`))
	tc := NewResponseToolContext(r)

	// 字符串形式（auto/none/required）原样透传。
	if got := mapResponsesToolChoice(json.RawMessage(`"auto"`), tc); string(got) != `"auto"` {
		t.Fatalf("auto passthrough: %s", got)
	}
	// custom 工具在 chat 侧仍是同名函数。
	if got := string(mapResponsesToolChoice(json.RawMessage(`{"type":"custom","name":"apply_patch"}`), tc)); got != `{"function":{"name":"apply_patch"},"type":"function"}` {
		t.Fatalf("custom choice: %s", got)
	}
	// tool_search 托管工具映射为代理函数。
	if got := string(mapResponsesToolChoice(json.RawMessage(`{"type":"tool_search"}`), tc)); got != `{"function":{"name":"tool_search"},"type":"function"}` {
		t.Fatalf("tool_search choice: %s", got)
	}
	// namespace 内 function 使用拍平后的 chat 名。
	if got := string(mapResponsesToolChoice(json.RawMessage(`{"type":"function","name":"run","namespace":"ns"}`), tc)); got != `{"function":{"name":"ns__run"},"type":"function"}` {
		t.Fatalf("namespace choice: %s", got)
	}
	// 缺失/null 返回 nil，调用方据此不下发 tool_choice。
	if got := mapResponsesToolChoice(nil, tc); got != nil {
		t.Fatalf("nil choice: %s", got)
	}
	if got := mapResponsesToolChoice(json.RawMessage(`null`), tc); got != nil {
		t.Fatalf("null choice: %s", got)
	}
}
