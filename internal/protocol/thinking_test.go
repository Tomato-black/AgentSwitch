package protocol

import (
	"encoding/json"
	"testing"
)

func TestStripThinking(t *testing.T) {
	body := []byte(`{
		"model":"m","max_tokens":8,"stream":true,
		"thinking":{"type":"enabled","budget_tokens":1024},
		"messages":[
			{"role":"user","content":"hi"},
			{"role":"assistant","content":[
				{"type":"thinking","thinking":"secret","signature":"sig"},
				{"type":"text","text":"ok"}
			]},
			{"role":"user","content":[{"type":"text","text":"again"}]}
		]
	}`)
	out, changed, err := StripThinking(body)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(out, &top); err != nil {
		t.Fatal(err)
	}
	if _, ok := top["thinking"]; ok {
		t.Fatal("top-level thinking must be removed")
	}
	if string(top["max_tokens"]) != "8" {
		t.Fatal("other fields must be preserved")
	}
	var msgs []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(top["messages"], &msgs); err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages = %+v", msgs)
	}
	var blocks []map[string]any
	if err := json.Unmarshal(msgs[1].Content, &blocks); err != nil || len(blocks) != 1 || blocks[0]["type"] != "text" {
		t.Fatalf("assistant content = %s (%v)", msgs[1].Content, err)
	}
}

func TestStripThinkingNoThinking(t *testing.T) {
	body := []byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`)
	out, changed, err := StripThinking(body)
	if err != nil || changed || string(out) != string(body) {
		t.Fatalf("changed=%v out=%s err=%v", changed, out, err)
	}
}
