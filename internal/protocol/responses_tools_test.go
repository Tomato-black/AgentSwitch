package protocol

import "testing"

func toolCtx(t *testing.T, tools string) *ResponseToolContext {
	t.Helper()
	r, err := ParseResponsesRequest([]byte(`{"model":"m","tools":` + tools + `}`))
	if err != nil {
		t.Fatal(err)
	}
	return NewResponseToolContext(r)
}

func TestToolContextFunctionTool(t *testing.T) {
	tc := toolCtx(t, `[{"type":"function","name":"exec_command","description":"run","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}]`)
	got := tc.ChatTools()
	if len(got) != 1 {
		t.Fatalf("chat tools: %+v", got)
	}
	fn := got[0]["function"].(map[string]any)
	if fn["name"] != "exec_command" || fn["description"] != "run" {
		t.Fatalf("function: %+v", fn)
	}
	spec, ok := tc.Spec("exec_command")
	if !ok || spec.Kind != responseToolFunction || spec.Name != "exec_command" {
		t.Fatalf("spec: %+v ok=%v", spec, ok)
	}
	if tc.ChatName("exec_command", "") != "exec_command" {
		t.Fatal("plain function name must be unchanged")
	}
	if tc.DroppedTools() != 0 {
		t.Fatalf("dropped=%d", tc.DroppedTools())
	}
}

func TestToolContextNormalizesBadParameters(t *testing.T) {
	tc := toolCtx(t, `[{"type":"function","name":"a"},{"type":"function","name":"b","parameters":null}]`)
	for _, tool := range tc.ChatTools() {
		p := tool["function"].(map[string]any)["parameters"].(map[string]any)
		if p["type"] != "object" || p["properties"] == nil {
			t.Fatalf("parameters must normalize to object: %+v", p)
		}
	}
}

func TestToolContextDropsHostedAndDuplicateTools(t *testing.T) {
	tc := toolCtx(t, `[{"type":"web_search"},{"type":"function","name":"a"},{"type":"function","name":"a"},{"type":"unknown"}]`)
	if len(tc.ChatTools()) != 1 || tc.DroppedTools() != 3 {
		t.Fatalf("tools=%d dropped=%d", len(tc.ChatTools()), tc.DroppedTools())
	}
}
