package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseChatRequest(t *testing.T) {
	cr, err := ParseChatRequest([]byte(`{"model":"claude-opus","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
	if err != nil || cr.Model != "claude-opus" || !cr.Stream {
		t.Fatalf("%+v %v", cr, err)
	}
	if _, err := ParseChatRequest([]byte(`{"stream":true}`)); err == nil {
		t.Fatal("missing model must fail")
	}
	if _, err := ParseChatRequest([]byte(`not-json`)); err == nil {
		t.Fatal("bad json must fail")
	}
	// 畸形 messages 不得出网关（2026-10-18 破坏性测试回归）。
	if _, err := ParseChatRequest([]byte(`{"model":"claude-opus"}`)); err == nil {
		t.Fatal("missing messages must fail")
	}
	if _, err := ParseChatRequest([]byte(`{"model":"claude-opus","messages":[]}`)); err == nil {
		t.Fatal("empty messages must fail")
	}
}

func TestRewriteModelKeepsFields(t *testing.T) {
	out, err := RewriteModel([]byte(`{"model":"public","stream":false,"messages":[{"role":"user"}]}`), "up-opus")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "up-opus" {
		t.Fatalf("model %v", m["model"])
	}
	if _, ok := m["messages"]; !ok {
		t.Fatal("messages dropped")
	}
}

func TestErrorEnvelopeShape(t *testing.T) {
	b := ErrorEnvelope(ErrTypeNotFound, "unknown model")
	// NOTE: brief 写的是 map[string]map[string]string，但 envelope 顶层 "type" 是字符串，
	// 该类型无法编译（m["type"] 是 map 不是 string）也无法 Unmarshal；改用 map[string]any。
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	errObj, ok := m["error"].(map[string]any)
	if !ok {
		t.Fatalf("error object missing: %s", b)
	}
	if m["type"] != "error" || errObj["type"] != ErrTypeNotFound {
		t.Fatalf("envelope: %s", b)
	}
	if !strings.Contains(string(b), "unknown model") {
		t.Fatal("message missing")
	}
}
