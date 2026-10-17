package protocol

import (
	"encoding/json"
	"testing"
)

func TestParseOpenAIChatRequestPassthrough(t *testing.T) {
	body := []byte(`{
	  "model":"logical-1","stream":true,"max_completion_tokens":64,
	  "messages":[{"role":"user","content":"hi"}],
	  "user":"u1","logit_bias":{"1":2}
	}`)
	req, err := ParseOpenAIChatRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "logical-1" || !req.Stream || len(req.Messages) != 1 {
		t.Fatalf("req: %+v", req)
	}
	if req.MaxCompletionTokens == nil || *req.MaxCompletionTokens != 64 {
		t.Fatalf("max_completion_tokens: %v", req.MaxCompletionTokens)
	}
	if string(req.Extra["user"]) != `"u1"` || string(req.Extra["logit_bias"]) != `{"1":2}` {
		t.Fatalf("extra: %+v", req.Extra)
	}
	if _, ok := req.Extra["model"]; ok {
		t.Fatalf("model should not land in Extra: %+v", req.Extra)
	}
	// 其余透传：round-trip 后未知字段仍在
	out, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatal(err)
	}
	if string(top["user"]) != `"u1"` || string(top["model"]) != `"logical-1"` {
		t.Fatalf("round-trip: %s", out)
	}
}

func TestParseOpenAIChatRequestRequiresModel(t *testing.T) {
	if _, err := ParseOpenAIChatRequest([]byte(`{"messages":[]}`)); err == nil {
		t.Fatal("expected error for missing model")
	}
	if _, err := ParseOpenAIChatRequest([]byte(`{`)); err == nil {
		t.Fatal("expected error for bad JSON")
	}
}

func TestOpenAIRequestToAnthropicMapsMessages(t *testing.T) {
	req := OpenAIChatRequest{
		Model:       "logical-1",
		Temperature: ptrF(0.7),
		TopP:        ptrF(0.9),
		MaxTokens:   ptrI(100),
		Stop:        json.RawMessage(`"END"`),
		Messages: []json.RawMessage{
			json.RawMessage(`{"role":"system","content":"be brief"}`),
			json.RawMessage(`{"role":"user","content":"hi"}`),
			json.RawMessage(`{"role":"assistant","content":"let me check"}`),
			json.RawMessage(`{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"search","arguments":"{\"q\":\"g\"}"}}]}`),
			json.RawMessage(`{"role":"tool","tool_call_id":"call_1","content":"result text"}`),
			json.RawMessage(`{"role":"user","content":[{"type":"text","text":"thanks"}]}`),
		},
	}
	out, err := OpenAIRequestToAnthropic(req, "claude-upstream-1")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Model       string   `json:"model"`
		MaxTokens   int      `json:"max_tokens"`
		System      string   `json:"system"`
		Temperature float64  `json:"temperature"`
		TopP        float64  `json:"top_p"`
		Stop        []string `json:"stop_sequences"`
		Messages    []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if body.Model != "claude-upstream-1" {
		t.Fatalf("model not rewritten: %s", body.Model)
	}
	if body.System != "be brief" || body.MaxTokens != 100 {
		t.Fatalf("system/max_tokens: %+v", body)
	}
	if body.Temperature != 0.7 || body.TopP != 0.9 || len(body.Stop) != 1 || body.Stop[0] != "END" {
		t.Fatalf("sampling: %+v", body)
	}
	// system 升顶层；余下 user/assistant/assistant(tool_use)/user(tool_result)/user
	if len(body.Messages) != 5 {
		t.Fatalf("messages = %d: %+v", len(body.Messages), body.Messages)
	}
	if body.Messages[0].Role != "user" || string(body.Messages[0].Content) != `"hi"` {
		t.Fatalf("user: %+v", body.Messages[0])
	}
	if body.Messages[1].Role != "assistant" {
		t.Fatalf("assistant role: %+v", body.Messages[1])
	}
	var asst []struct {
		Type  string          `json:"type"`
		Text  string          `json:"text"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(body.Messages[1].Content, &asst); err != nil {
		t.Fatal(err)
	}
	if len(asst) != 1 || asst[0].Type != "text" || asst[0].Text != "let me check" {
		t.Fatalf("assistant content: %+v", asst)
	}
	asst = nil
	if err := json.Unmarshal(body.Messages[2].Content, &asst); err != nil {
		t.Fatal(err)
	}
	if len(asst) != 1 || asst[0].Type != "tool_use" || asst[0].ID != "call_1" || asst[0].Name != "search" {
		t.Fatalf("tool_use: %+v", asst)
	}
	if string(asst[0].Input) != `{"q":"g"}` {
		t.Fatalf("tool input: %s", asst[0].Input)
	}
	var toolRes []struct {
		Type      string          `json:"type"`
		ToolUseID string          `json:"tool_use_id"`
		Content   json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(body.Messages[3].Content, &toolRes); err != nil {
		t.Fatal(err)
	}
	if body.Messages[3].Role != "user" || len(toolRes) != 1 || toolRes[0].Type != "tool_result" ||
		toolRes[0].ToolUseID != "call_1" || string(toolRes[0].Content) != `"result text"` {
		t.Fatalf("tool_result: %+v", body.Messages[3])
	}
	if body.Messages[4].Role != "user" || string(body.Messages[4].Content) != `"thanks"` {
		t.Fatalf("final user: %+v", body.Messages[4])
	}
}

func TestOpenAIRequestToAnthropicMapsTools(t *testing.T) {
	req := OpenAIChatRequest{
		Model:      "logical-1",
		Tools:      json.RawMessage(`[{"type":"function","function":{"name":"search","description":"s","parameters":{"type":"object"}}}]`),
		ToolChoice: json.RawMessage(`{"type":"function","function":{"name":"search"}}`),
		Messages:   []json.RawMessage{json.RawMessage(`{"role":"user","content":"hi"}`)},
	}
	out, err := OpenAIRequestToAnthropic(req, "claude-upstream-1")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"input_schema"`
		} `json:"tools"`
		ToolChoice json.RawMessage `json:"tool_choice"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Tools) != 1 || body.Tools[0].Name != "search" || body.Tools[0].Description != "s" {
		t.Fatalf("tools: %+v", body.Tools)
	}
	if string(body.Tools[0].InputSchema) != `{"type":"object"}` {
		t.Fatalf("input_schema: %s", body.Tools[0].InputSchema)
	}
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body.ToolChoice, &tc); err != nil {
		t.Fatal(err)
	}
	if tc.Type != "tool" || tc.Name != "search" {
		t.Fatalf("tool_choice: %+v", tc)
	}
}

func TestOpenAIRequestToAnthropicDefaultsMaxTokens(t *testing.T) {
	req := OpenAIChatRequest{
		Messages: []json.RawMessage{json.RawMessage(`{"role":"user","content":"hi"}`)},
	}
	out, err := OpenAIRequestToAnthropic(req, "claude-upstream-1")
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		MaxTokens int `json:"max_tokens"`
	}
	if err := json.Unmarshal(out, &body); err != nil {
		t.Fatal(err)
	}
	if body.MaxTokens <= 0 {
		t.Fatalf("Anthropic 必填 max_tokens，缺省应给出正值: %d", body.MaxTokens)
	}
}

func TestAnthropicResponseToOpenAIMapsUsage(t *testing.T) {
	up := mustJSON(t, map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": "claude-upstream-1",
		"content": []map[string]any{
			{"type": "text", "text": "hello"},
			{"type": "tool_use", "id": "tu1", "name": "search", "input": map[string]any{"q": "g"}},
		},
		"stop_reason": "tool_use",
		"usage":       map[string]any{"input_tokens": 11, "output_tokens": 7},
	})
	out, err := AnthropicResponseToOpenAI(up, "logical-1")
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content   *string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	// usage tokens 字段名必须是 prompt/completion（OpenAI 侧），不是 input/output
	if resp.Usage.PromptTokens != 11 || resp.Usage.CompletionTokens != 7 || resp.Usage.TotalTokens != 18 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
	if resp.Object != "chat.completion" || resp.Model != "logical-1" {
		t.Fatalf("envelope: %+v", resp)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("choices: %+v", resp.Choices)
	}
	msg := resp.Choices[0].Message
	if msg.Content == nil || *msg.Content != "hello" {
		t.Fatalf("content: %v", msg.Content)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].ID != "tu1" || msg.ToolCalls[0].Type != "function" ||
		msg.ToolCalls[0].Function.Name != "search" || msg.ToolCalls[0].Function.Arguments != `{"q":"g"}` {
		t.Fatalf("tool_calls: %+v", msg.ToolCalls)
	}
}

func ptrF(f float64) *float64 { return &f }
func ptrI(i int) *int         { return &i }
