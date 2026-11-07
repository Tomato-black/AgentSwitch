package claudecfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/uitest"
)

func TestSectionRendersClaudeSettings(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	st.ClaudeCfg.Home = home
	tt := ui.NewTester(func(c *ui.Context) {
		Section(c, st)
	}, 1000, 1600)
	for _, want := range []string{
		"Claude Code 设置", "受管 13 键", "用户级", "项目级", "企业托管",
		"表单", "JSON", "合并预览", "模型与响应", "权限", "Skills",
		"危险模式确认", "保存设置",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

// TestLoadNumericJSONNotDirty 回归（C1）：cloneDoc 曾走 json.Marshal 往返，
// json.Number 变 float64 后与 Baseline 恒不等，含数字的文件零编辑即脏。
func TestLoadNumericJSONNotDirty(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	st.ClaudeCfg.Home = home
	file := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"cleanupPeriodDays":30,"statusLine":{"padding":0}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: home}
	load(st, cfgs)
	cf := &st.ClaudeCfg
	if !cf.Loaded {
		t.Fatalf("load failed: %s", cf.Msg)
	}
	if cf.Dirty {
		t.Fatal("file containing numbers must load clean without user edits")
	}
	// 编辑一个无关字符串键后，Diff 必须只列出该键（数字键不得串脏）。
	setString(cf.Doc, "model", "opus")
	RecomputeDirty(cf)
	if !cf.Dirty {
		t.Fatal("string edit must dirty the document")
	}
	changed := corecfg.Diff(cf.Baseline, cf.Doc)
	if len(changed) != 1 || changed[0] != "model" {
		t.Fatalf("diff = %v, want [model]", changed)
	}
}

// TestCloneDocPreservesJSONNumber：深拷贝不得把 json.Number 变 float64。
func TestCloneDocPreservesJSONNumber(t *testing.T) {
	src := map[string]any{
		"cleanupPeriodDays": mustJSONNumber(t, "30"),
		"statusLine":        map[string]any{"padding": mustJSONNumber(t, "0")},
		"nested":            []any{mustJSONNumber(t, "1.50")},
	}
	got := cloneDoc(src)
	if _, ok := got["cleanupPeriodDays"].(json.Number); !ok {
		t.Fatalf("top-level number flattened: %#v", got["cleanupPeriodDays"])
	}
	obj, _ := got["statusLine"].(map[string]any)
	if _, ok := obj["padding"].(json.Number); !ok {
		t.Fatalf("nested number flattened: %#v", obj)
	}
	arr, _ := got["nested"].([]any)
	if _, ok := arr[0].(json.Number); !ok {
		t.Fatalf("slice number flattened: %#v", arr)
	}
	// 拷贝必须独立于源文档。
	src["statusLine"].(map[string]any)["padding"] = mustJSONNumber(t, "9")
	if obj["padding"].(json.Number).String() != "0" {
		t.Fatal("clone must not alias source maps")
	}
}

func mustJSONNumber(t *testing.T, s string) json.Number {
	t.Helper()
	return json.Number(s)
}
