package claudecfg

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/state"
)

func renderJSON(st *state.State) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		jsonView(c, c.Theme(), st)
	}, 1200, 700)
}

func TestParseJSONTextReportsLineAndColumn(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.Doc = map[string]any{"model": "sonnet"}
	cf.Baseline = cloneDoc(cf.Doc)

	ok := parseJSONText(cf, "{\n  \"model\": \"sonnet\",\n  \"bad\":\n}")
	if ok {
		t.Fatal("invalid JSON must not parse")
	}
	for _, want := range []string{"第 ", "行第", "列"} {
		if !strings.Contains(cf.ParseErr, want) {
			t.Fatalf("parse error %q must contain %q", cf.ParseErr, want)
		}
	}
	if got := cf.Doc["model"]; got != "sonnet" {
		t.Fatalf("invalid JSON must not replace doc: %v", cf.Doc)
	}
}

func TestParseJSONTextUpdatesDocAndDirty(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.Doc = map[string]any{"model": "sonnet"}
	cf.Baseline = cloneDoc(cf.Doc)

	ok := parseJSONText(cf, `{"model":"opus","number":3}`)
	if !ok || cf.ParseErr != "" {
		t.Fatalf("valid JSON must parse: %v", cf.ParseErr)
	}
	if cf.Doc["model"] != "opus" {
		t.Fatalf("doc must update: %v", cf.Doc)
	}
	if !cf.Dirty {
		t.Fatal("changed doc must be dirty")
	}

	ok = parseJSONText(cf, `{"model":"sonnet"}`)
	if !ok || cf.Dirty {
		t.Fatalf("restoring baseline must clear dirty: ok=%v dirty=%v", ok, cf.Dirty)
	}
}

func TestJSONParseDebounceWaitsForDueTime(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.JSONText = `{"model":"opus"}`
	tt := renderJSON(st)

	cf.JSONPending = true
	cf.JSONDueNano = tt.Now().Add(jsonParseDebounce).UnixNano()
	tt.Frame()
	if cf.Doc["model"] == "opus" {
		t.Fatal("must not parse before the debounce deadline")
	}

	tt.Advance(jsonParseDebounce + time.Millisecond)
	tt.Frame()
	if cf.Doc["model"] != "opus" {
		t.Fatalf("must parse after the debounce deadline: %v", cf.Doc)
	}
	if cf.JSONPending {
		t.Fatal("pending flag must clear after parse")
	}
}

func TestJSONFormatReformatsText(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.JSONText = `{"model":"opus"}`

	if !formatJSON(cf) {
		t.Fatalf("format must succeed: %v", cf.ParseErr)
	}
	if !strings.Contains(cf.JSONText, "\n  \"model\": \"opus\"") {
		t.Fatalf("formatted JSON = %q", cf.JSONText)
	}
}

func TestJSONResetRestoresBaseline(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.Baseline = map[string]any{"model": "sonnet"}
	cf.Doc = map[string]any{"model": "opus"}
	cf.JSONText = `{"model":"opus"}`
	cf.Dirty = true

	resetJSON(cf)
	if v, _ := corecfg.Get(cf.Doc, "model"); v != "sonnet" {
		t.Fatalf("reset doc = %v", cf.Doc)
	}
	if cf.Dirty {
		t.Fatal("reset must clear dirty")
	}
	if !strings.Contains(cf.JSONText, "sonnet") {
		t.Fatalf("reset text = %q", cf.JSONText)
	}
}

func TestSaveBlockReasonRejectsInvalidSkillOverrides(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.Doc = map[string]any{"skillOverrides": map[string]any{"deploy": "bogus"}}
	cf.Baseline = map[string]any{}

	reason := saveBlockReason(cf)
	if reason == "" {
		t.Fatal("invalid skillOverrides must block save")
	}
	if !strings.Contains(reason, "skillOverrides") {
		t.Fatalf("block reason must name the path: %q", reason)
	}
}

func TestSaveBlockReasonRejectsManagedKeyEdits(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.Managed = corecfg.ManagedSet{StrongEnv: []string{"ANTHROPIC_AUTH_TOKEN"}}
	cf.Doc = map[string]any{"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "secret-value"}}
	cf.Baseline = map[string]any{}

	reason := saveBlockReason(cf)
	if reason == "" {
		t.Fatal("managed-key edit must block save")
	}
	if !strings.Contains(reason, "受管") || !strings.Contains(reason, "ANTHROPIC_AUTH_TOKEN") {
		t.Fatalf("block reason = %q", reason)
	}
}

func TestJSONViewRendersWarningControlsAndError(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.JSONText = "{\n  \"model\": ,\n}"
	parseJSONText(cf, cf.JSONText)

	tt := renderJSON(st)
	for _, want := range []string{"JSON 视图显示明文", "格式化", "重置", "校验", "第 ", "列"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if !tt.HasText("JSON 错误") {
		t.Fatalf("error state may be missing: %v", tt.Texts())
	}
}
