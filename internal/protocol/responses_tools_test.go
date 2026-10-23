package protocol

import (
	"regexp"
	"strings"
	"testing"
)

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

func TestFlattenResponseToolNameShort(t *testing.T) {
	if got := flattenResponseToolName("ns", "run"); got != "ns__run" {
		t.Fatalf("short name: %q", got)
	}
}

func TestFlattenResponseToolNameLongIsBoundedAndHashed(t *testing.T) {
	suffix := regexp.MustCompile(`__[0-9a-f]{8}$`)
	got := flattenResponseToolName("ns", strings.Repeat("a", 70))
	if len(got) > 64 {
		t.Fatalf("len=%d must be <= 64: %q", len(got), got)
	}
	if !suffix.MatchString(got) {
		t.Fatalf("long name must end with FNV-1a suffix: %q", got)
	}
}

func TestFlattenResponseToolNameDeterministic(t *testing.T) {
	long := strings.Repeat("a", 70)
	first := flattenResponseToolName("ns", long)
	second := flattenResponseToolName("ns", long)
	if first != second {
		t.Fatalf("same input must map to same name: %q != %q", first, second)
	}
	if other := flattenResponseToolName("ns", strings.Repeat("b", 70)); other == first {
		t.Fatalf("different inputs must map to different names: %q", first)
	}
}

func TestToolContextCustomTool(t *testing.T) {
	raw := `{
		"type":"custom","name":"apply_patch",
		"description":"Use the apply_patch tool",
		"format":{"type":"grammar","syntax":"lark","definition":"start: patch"},
		"parameters":{"type":"object"}}`
	tc := toolCtx(t, "["+raw+"]")
	got := tc.ChatTools()
	if len(got) != 1 {
		t.Fatalf("chat tools: %+v", got)
	}
	fn := got[0]["function"].(map[string]any)
	if fn["name"] != "apply_patch" {
		t.Fatalf("name: %+v", fn)
	}
	desc, _ := fn["description"].(string)
	if !strings.Contains(desc, "Original tool definition:") || !strings.Contains(desc, "grammar") {
		t.Fatalf("custom definition must be embedded: %q", desc)
	}
	params := fn["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)
	if props["input"].(map[string]any)["type"] != "string" {
		t.Fatalf("custom tool must accept {input:string}: %+v", params)
	}
	spec, ok := tc.Spec("apply_patch")
	if !ok || spec.Kind != responseToolCustom || spec.Name != "apply_patch" {
		t.Fatalf("spec: %+v ok=%v", spec, ok)
	}
}

func TestToolContextChatNameFallback(t *testing.T) {
	tc := toolCtx(t, `[{"type":"function","name":"exec"}]`)
	if got := tc.ChatName("unknown", "ns"); got != "unknown" {
		t.Fatalf("unknown tool must fall back to name: %q", got)
	}
}
