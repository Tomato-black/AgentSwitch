package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// 空名字的 tool_use 是客户端历史里的坏块（如被 CLI 拒绝的空工具调用）：
// 转成 OpenAI tool_calls 后 function.name 为空，严格的上游（小米 mimo）会直接
// 400 "missing a function name"。转换器必须丢掉该 tool_use 以及配对的
// tool_result，保持 OpenAI 的 tool 消息配对不悬空（2026-10-22 排查结论）。
func TestAnthropicToOpenAIDropsNamelessToolUseAndResult(t *testing.T) {
	anth := []byte(`{
	  "model":"up-1","max_tokens":16,
	  "messages":[
	    {"role":"user","content":"hi"},
	    {"role":"assistant","content":[
	      {"type":"tool_use","id":"bad","name":"","input":{}},
	      {"type":"tool_use","id":"ok","name":"search","input":{"q":"g"}}
	    ]},
	    {"role":"user","content":[
	      {"type":"tool_result","tool_use_id":"bad","content":"no such tool available"},
	      {"type":"tool_result","tool_use_id":"ok","content":"found"}
	    ]}
	  ]
	}`)
	out, _, err := AnthropicRequestToOpenAI(anth)
	if err != nil {
		t.Fatal(err)
	}
	var req struct {
		Messages []struct {
			Role       string `json:"role"`
			ToolCalls  []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 3 {
		t.Fatalf("messages = %d, want 3 (user, assistant, tool): %+v", len(req.Messages), req.Messages)
	}
	asst := req.Messages[1]
	if asst.Role != "assistant" || len(asst.ToolCalls) != 1 || asst.ToolCalls[0].Function.Name != "search" {
		t.Fatalf("assistant must keep only the named tool call: %+v", asst)
	}
	tool := req.Messages[2]
	if tool.Role != "tool" || tool.ToolCallID != "ok" {
		t.Fatalf("tool message must keep only the paired result: %+v", tool)
	}
}

func TestAnthropicToOpenAIRequest(t *testing.T) {
	anth := []byte(`{
	  "model":"up-1","max_tokens":256,"stream":true,
	  "temperature":0.5,"top_p":0.9,"stop_sequences":["END"],
	  "metadata":{"x":1},"thinking":{"type":"enabled"},
	  "system":"be brief",
	  "messages":[
	    {"role":"user","content":"hi"},
	    {"role":"assistant","content":[
	      {"type":"text","text":"let me check"},
	      {"type":"tool_use","id":"tu1","name":"search","input":{"q":"g"}}
	    ]},
	    {"role":"user","content":[
	      {"type":"tool_result","tool_use_id":"tu1","content":"result text"},
	      {"type":"text","text":"thanks"}
	    ]}
	  ],
	  "tools":[{"name":"search","description":"s","input_schema":{"type":"object"}}],
	  "tool_choice":{"type":"any"}
	}`)
	out, dropped, err := AnthropicRequestToOpenAI(anth)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 2 || dropped[0] != "metadata" || dropped[1] != "thinking" {
		t.Fatalf("dropped %v", dropped)
	}
	var req struct {
		Model       string   `json:"model"`
		MaxTokens   int      `json:"max_tokens"`
		Stream      bool     `json:"stream"`
		Temperature float64  `json:"temperature"`
		TopP        float64  `json:"top_p"`
		Stop        []string `json:"stop"`
		Messages    []struct {
			Role       string           `json:"role"`
			Content    string           `json:"content"`
			ToolCalls  []map[string]any `json:"tool_calls"`
			ToolCallID string           `json:"tool_call_id"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice any `json:"tool_choice"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "up-1" || req.MaxTokens != 256 || !req.Stream || req.Stop[0] != "END" {
		t.Fatalf("params: %+v", req)
	}
	if req.Temperature != 0.5 || req.TopP != 0.9 {
		t.Fatalf("sampling: %+v", req)
	}
	if len(req.Messages) != 5 { // system + user + assistant(text+tool_calls) + user(text) + tool
		t.Fatalf("messages = %d: %+v", len(req.Messages), req.Messages)
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "be brief" {
		t.Fatalf("system: %+v", req.Messages[0])
	}
	asst := req.Messages[2]
	if asst.Role != "assistant" || len(asst.ToolCalls) != 1 {
		t.Fatalf("assistant: %+v", asst)
	}
	tc := asst.ToolCalls[0]
	if tc["type"] != "function" {
		t.Fatalf("tool call: %+v", tc)
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "search" || fn["arguments"] != `{"q":"g"}` {
		t.Fatalf("function: %+v", fn)
	}
	tool := req.Messages[3]
	if tool.Role != "tool" || tool.ToolCallID != "tu1" || tool.Content != "result text" {
		t.Fatalf("tool result: %+v", tool)
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "search" || req.Tools[0].Function.Parameters["type"] != "object" {
		t.Fatalf("tools: %+v", req.Tools)
	}
	if req.ToolChoice != "required" {
		t.Fatalf("tool_choice: %v", req.ToolChoice)
	}
}

func TestOpenAIToAnthropicResponse(t *testing.T) {
	up := mustJSON(t, map[string]any{
		"id": "cmpl-1", "model": "up-1",
		"choices": []map[string]any{{
			"message": map[string]any{
				"content": "hello",
				"tool_calls": []map[string]any{{
					"id": "call_1", "type": "function",
					"function": map[string]any{"name": "search", "arguments": `{"q":"g"}`},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
	})
	out, err := OpenAIResponseToAnthropic(up)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != "message" || resp.Role != "assistant" || resp.StopReason != "tool_use" {
		t.Fatalf("resp: %+v", resp)
	}
	if len(resp.Content) != 2 || resp.Content[0].Type != "text" || resp.Content[0].Text != "hello" {
		t.Fatalf("content: %+v", resp.Content)
	}
	tb := resp.Content[1]
	if tb.Type != "tool_use" || tb.ID != "call_1" || tb.Name != "search" {
		t.Fatalf("tool_use block: %+v", tb)
	}
	if !strings.Contains(string(tb.Input), `"q"`) {
		t.Fatalf("input: %s", tb.Input)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
}

// Review Focus 3：空 choices / 坏 finish 不 panic，end_turn 收口。
func TestOpenAIResponseEmptyChoicesDefensive(t *testing.T) {
	out, err := OpenAIResponseToAnthropic([]byte(`{"id":"x","model":"m","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"stop_reason":"end_turn"`) {
		t.Fatalf("defensive end_turn missing: %s", out)
	}
	if !strings.Contains(string(out), `"input_tokens":3`) {
		t.Fatalf("usage lost: %s", out)
	}
}

func TestStreamTransformerEventSequence(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	chunk1 := []byte("data: {\"id\":\"c1\",\"model\":\"up-1\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"he\"}}]}\n\n")
	chunk2 := []byte("data: {\"id\":\"c1\",\"model\":\"up-1\",\"choices\":[{\"delta\":{\"content\":\"llo\"},\"finish_reason\":null}]}\n\n")
	chunk3 := []byte("data: {\"id\":\"c1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	// 跨字节边界喂入：把 chunk2 拆两半
	var out []byte
	out = append(out, tu.Feed(chunk1)...)
	out = append(out, tu.Feed(chunk2[:20])...)
	out = append(out, tu.Feed(chunk2[20:])...)
	out = append(out, tu.Feed(chunk3)...)
	out = append(out, tu.Finish()...)
	s := string(out)
	for _, want := range []string{
		"event: message_start", `"model":"up-1"`,
		"event: content_block_start", `"type":"text"`,
		`"text":"he"`, `"text":"llo"`,
		"event: content_block_stop",
		"event: message_delta", `"stop_reason":"end_turn"`,
		"event: message_stop",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	// 事件顺序：message_start < content_block_start < message_delta
	if strings.Index(s, "message_start") > strings.Index(s, "content_block_start") ||
		strings.Index(s, "content_block_stop") > strings.Index(s, "message_delta") {
		t.Fatalf("event order wrong:\n%s", s)
	}
}

func TestStreamTransformerToolCallAccumulation(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	// NOTE: 简报原稿此处为 `feed := func(s string) { tu.Feed([]byte(s)) }`，丢弃了
	// Feed 返回值。spec §5.3.3 规定「逐事件 flush」——事件在 Feed 时即已交付且每
	// 字节恰好转发一次（实现计划 Task 4 的 streamBody 泵即按 Feed 返回值转发），
	// 因此收口断言必须累积各次 Feed 的返回值（与 TestStreamTransformerEventSequence
	// 同型），否则 out 恒为空、下列断言不可满足。断言本身逐字未动。
	var acc []byte
	feed := func(s string) { acc = append(acc, tu.Feed([]byte(s))...) }
	feed("data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
	feed("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"t1\",\"name\":\"f1\",\"arguments\":\"{\\\"a\\\":\"}]}}]}\n\n")
	feed("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"arguments\":\"1}\"}]}}]}\n\n")
	feed("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"t2\",\"name\":\"f2\",\"arguments\":\"{}\"}]}}]}\n\n")
	feed("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
	feed("data: [DONE]\n\n")
	acc = append(acc, tu.Feed(nil)...)
	acc = append(acc, tu.Finish()...)
	out := string(acc)
	// 两个 tool_use 块、参数累积完整、stop=tool_use
	if !strings.Contains(out, `"name":"f1"`) || !strings.Contains(out, `"name":"f2"`) {
		t.Fatalf("tool blocks missing:\n%s", out)
	}
	if !strings.Contains(out, `"partial_json":"{\"a\":1}"`) {
		t.Fatalf("args not accumulated into input_json_delta:\n%s", out)
	}
	if !strings.Contains(out, `"stop_reason":"tool_use"`) {
		t.Fatalf("stop reason:\n%s", out)
	}
	// NOTE: 简报原稿此处统计的是裸子串 "content_block_start"/"content_block_stop"。
	// 每个合法 Anthropic SSE 事件中该串出现两次（event: 行 + payload 的 "type"
	// 字段——真实 Anthropic API 形状，EventSequence 亦断言这两种形状），两个 tool
	// 块必然命中 4 次；按 event: 行统计才是「两开两关」的本意。
	if strings.Count(out, "event: content_block_start") != 2 || strings.Count(out, "event: content_block_stop") != 2 {
		t.Fatalf("block open/close mismatch:\n%s", out)
	}
}

// OpenAI 标准流式 tool_calls 把函数名/参数嵌在 function 下：
//
//	{"tool_calls":[{"index":0,"id":"call_1","type":"function",
//	                "function":{"name":"Bash","arguments":"{\"cmd\":"}}]}
//
// 只认顶层 name/arguments 的解析会把标准流读成「空名字 + 空参数」，
// 客户端随即报 "No such tool available: "（2026-10-22 mimo 渠道排查结论）。
func TestStreamTransformerStandardNestedToolCalls(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	var acc []byte
	feed := func(s string) { acc = append(acc, tu.Feed([]byte(s))...) }
	feed(`data: {"id":"c","choices":[{"delta":{"role":"assistant"}}]}` + "\n\n")
	feed(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Bash","arguments":"{\"cmd\":"}}]}}]}` + "\n\n")
	feed(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}` + "\n\n")
	feed(`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n")
	feed("data: [DONE]\n\n")
	acc = append(acc, tu.Finish()...)
	out := string(acc)
	if !strings.Contains(out, `"name":"Bash"`) {
		t.Fatalf("tool name lost from standard nested chunk:\n%s", out)
	}
	if !strings.Contains(out, `"id":"call_1"`) {
		t.Fatalf("tool id lost:\n%s", out)
	}
	// 参数必须走 input_json_delta（真实 Anthropic 形状）；塞进
	// content_block_start.input 会被客户端忽略（见下方用例）。
	if !strings.Contains(out, `"type":"input_json_delta"`) ||
		!strings.Contains(out, `"partial_json":"{\"cmd\":\"ls\"}"`) {
		t.Fatalf("arguments must be delivered via input_json_delta:\n%s", out)
	}
	if !strings.Contains(out, `"stop_reason":"tool_use"`) {
		t.Fatalf("stop reason:\n%s", out)
	}
}

// Claude Code 只从 input_json_delta 分片累积工具参数，忽略
// content_block_start.input：把完整参数塞进 start 会得到空对象，客户端报
// "Invalid tool parameters"（2026-10-22 mimo 渠道排查）。start 的 input
// 必须是空对象，参数全部经 input_json_delta 下发。
func TestStreamTransformerToolUseEmitsInputJSONDelta(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	var acc []byte
	feed := func(s string) { acc = append(acc, tu.Feed([]byte(s))...) }
	feed(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Bash","arguments":"{\"command\":"}}]}}]}` + "\n\n")
	feed(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls -la\"}"}}]}}]}` + "\n\n")
	feed(`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n")
	acc = append(acc, tu.Finish()...)
	out := string(acc)
	if !strings.Contains(out, `"type":"input_json_delta"`) {
		t.Fatalf("missing input_json_delta:\n%s", out)
	}
	if !strings.Contains(out, `"partial_json":"{\"command\":\"ls -la\"}"`) {
		t.Fatalf("partial_json must carry full accumulated args:\n%s", out)
	}
	if strings.Contains(out, `"input":{"command"`) {
		t.Fatalf("content_block_start.input must stay empty:\n%s", out)
	}
}

// Review Focus 3：截断流（无 finish_reason/[DONE]）Finish 兜底收口。
func TestStreamTransformerTruncatedFinish(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	tu.Feed([]byte("data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n"))
	out := string(append(tu.Feed([]byte("data: {\"choices\":[{\"delta\":{}}]}\n\n")), tu.Finish()...))
	if !strings.Contains(out, `"stop_reason":"end_turn"`) || !strings.Contains(out, "message_stop") {
		t.Fatalf("truncated stream must close end_turn:\n%s", out)
	}
}

func TestStreamTransformerTerminalMarker(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	if tu.SawTerminal() {
		t.Fatal("fresh transformer must not report terminal")
	}
	tu.Feed([]byte(`data: {"id":"c","choices":[{"delta":{"content":"x"},"finish_reason":null}]}` + "\n\n"))
	if tu.SawTerminal() {
		t.Fatal("finish_reason:null must not count as terminal")
	}
	tu.Feed([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
	if !tu.SawTerminal() {
		t.Fatal("finish_reason must mark terminal")
	}

	done := NewOpenAIStreamTransformer()
	done.Feed([]byte("data: [DONE]\n\n"))
	if !done.SawTerminal() {
		t.Fatal("[DONE] must mark terminal")
	}
}

func TestStreamTransformerDropsIncompleteToolArgs(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	var acc []byte
	acc = append(acc, tu.Feed([]byte(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"Write","arguments":"{\"file_path\":\"/tmp/a\",\"content\":\"abc"}}]}}]}`+"\n\n"))...)
	// 正常 finish_reason 但参数 JSON 不完整（上游自身 bug）：该块必须丢弃。
	acc = append(acc, tu.Feed([]byte(
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n"))...)
	out := string(acc)
	if strings.Contains(out, `"name":"Write"`) {
		t.Fatalf("incomplete tool block must not be emitted:\n%s", out)
	}
	if tu.DroppedTools() != 1 {
		t.Fatalf("DroppedTools = %d, want 1", tu.DroppedTools())
	}
}

// 空 arguments 是「无参工具」的合法表示，必须按 {} 下发，不算丢弃。
func TestStreamTransformerEmptyToolArgsKept(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	var acc []byte
	acc = append(acc, tu.Feed([]byte(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"Noop","arguments":""}}]}}]}`+"\n\n"))...)
	acc = append(acc, tu.Feed([]byte(
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n"))...)
	out := string(acc)
	if !strings.Contains(out, `"name":"Noop"`) {
		t.Fatalf("empty-args tool block must be emitted:\n%s", out)
	}
	if !strings.Contains(out, `"partial_json":"{}"`) {
		t.Fatalf("empty args must be delivered as {}:\n%s", out)
	}
	if tu.DroppedTools() != 0 {
		t.Fatalf("DroppedTools = %d, want 0", tu.DroppedTools())
	}
}

// 全部工具块参数残缺：不得声明 stop_reason tool_use（客户端会等一个
// 不存在的工具块），降级为 end_turn。
func TestStreamTransformerAllToolsDroppedDowngradesStopReason(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	var acc []byte
	acc = append(acc, tu.Feed([]byte(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"Write","arguments":"{\"file_path\":"}}]}}]}`+"\n\n"))...)
	acc = append(acc, tu.Feed([]byte(
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n"))...)
	out := string(acc)
	if strings.Contains(out, `"type":"tool_use"`) {
		t.Fatalf("dropped tool blocks must not be emitted:\n%s", out)
	}
	if strings.Contains(out, `"stop_reason":"tool_use"`) {
		t.Fatalf("stop_reason must not claim tool_use without tool blocks:\n%s", out)
	}
	if !strings.Contains(out, `"stop_reason":"end_turn"`) {
		t.Fatalf("stop_reason must downgrade to end_turn:\n%s", out)
	}
	if tu.DroppedTools() == 0 {
		t.Fatal("DroppedTools must be > 0")
	}
}

// usage-only 尾块（choices 为空）不得产生空事件或 panic。
func TestStreamTransformerUsageOnlyTail(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	head := tu.Feed([]byte("data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
	tail := tu.Feed([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"))
	out := string(append(head, tail...))
	if strings.Contains(out, `"input_tokens":0`) && !strings.Contains(out, `"input_tokens":5`) {
		t.Fatalf("usage lost:\n%s", out)
	}
}
