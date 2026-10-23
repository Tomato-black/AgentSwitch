package protocol

import (
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

func TestResponsesInputString(t *testing.T) {
	msgs, err := responsesInputToMessages([]byte(`"hi"`), "", NewResponseToolContext(ResponsesRequest{}))
	if err != nil || len(msgs) != 1 || msgs[0]["role"] != "user" {
		t.Fatalf("string input: %+v err=%v", msgs, err)
	}
}
