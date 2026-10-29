package accesskit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/desktop/uitest"
	"AgentSwitch/internal/modelid"
	"AgentSwitch/internal/storage"
)

// 主模型 1M（2026-10-21）：文档 env 的 ANTHROPIC_MODEL 带 [1M] 时回填勾选；
// 勾选/取消把「逻辑模型名[1M]」/纯名写回文档（保存归一化再剔除纯名）。
func TestAccessMainModelOneMPatchAndSync(t *testing.T) {
	f := &state.AccessForm{}
	f = &state.AccessForm{ModelLabel: "my-code", DocJSON: `{"env":{"ANTHROPIC_MODEL":"my-code[1M]"}}`}
	SyncRolesFromDoc(f)
	if !f.MainOneM {
		t.Fatal("应从文档回填 1M 勾选")
	}
	f.MainOneM = false
	PatchMainModel(f)
	env, err := DocEnvStrings(f.DocJSON)
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_MODEL"] != "my-code" {
		t.Fatal(f.DocJSON)
	}
	f.MainOneM = true
	PatchMainModel(f)
	env, err = DocEnvStrings(f.DocJSON)
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_MODEL"] != "my-code[1M]" {
		t.Fatal(f.DocJSON)
	}
}

func TestAccessSyncRolesAndPatch(t *testing.T) {
	f := &state.AccessForm{}
	f = &state.AccessForm{DocJSON: `{"env":{"ANTHROPIC_DEFAULT_HAIKU_MODEL":"fast[1M]"}}`}
	SyncRolesFromDoc(f)
	i := roleIndexByEnvKey("ANTHROPIC_DEFAULT_HAIKU_MODEL")
	if f.RoleModels[i] != "fast" || !f.RoleOneM[i] {
		t.Fatalf("%v %v", f.RoleModels, f.RoleOneM)
	}
	f.RoleOneM[i] = false
	PatchRole(f, i)
	env, err := DocEnvStrings(f.DocJSON)
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "fast" {
		t.Fatal(f.DocJSON)
	}
}

func TestAccessRoleRoundTrip(t *testing.T) {
	// 补丁：选模型+1M+显示名
	raw, err := DocSetEnv(`{"env":{}}`, "ANTHROPIC_DEFAULT_SONNET_MODEL", "fast[1M]")
	if err != nil {
		t.Fatal(err)
	}
	raw, err = DocSetEnv(raw, "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME", "极速")
	if err != nil {
		t.Fatal(err)
	}
	env, err := DocEnvStrings(raw)
	if err != nil {
		t.Fatal(err)
	}
	name, oneM := modelid.SplitOneM(env["ANTHROPIC_DEFAULT_SONNET_MODEL"])
	if name != "fast" || !oneM || env["ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"] != "极速" {
		t.Fatal(raw)
	}
	// 回填：Subagent 无显示名键；空键 = 跟随主模型
	env2, err := DocEnvStrings(`{"env":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	name2, oneM2 := modelid.SplitOneM(env2["CLAUDE_CODE_SUBAGENT_MODEL"])
	if name2 != "" || oneM2 {
		t.Fatal("空值应回落跟随主模型")
	}
}

// TestAccessSaveFormCodexUsesFirstModel：Codex 保存以模型列表首项为绑定
// 模型（默认），不是旧的单选 modelLabel。
func TestAccessSaveFormCodexUsesFirstModel(t *testing.T) {
	st, a := uitest.New(t)
	t.Setenv("HOME", t.TempDir())
	for _, lm := range []storage.LogicalModel{
		{ID: "lm1", Name: "my-code", Enabled: true},
		{ID: "lm2", Name: "second", Enabled: true},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	st.Access.App = "codex"
	st.Access.Form = state.AccessForm{
		Open: true, Name: "cfg", ModelLabel: "second",
		CodexModels: []string{"my-code", "second"},
		DocJSON:     `{"models":["my-code","second"],"env":{}}`,
	}
	acc := bridge.AccessConfigs{A: a}
	saveForm(st, acc)
	if st.Access.Form.Msg != "" {
		t.Fatalf("保存应成功: %q", st.Access.Form.Msg)
	}
	list := acc.List("codex")
	if len(list) != 1 || list[0].LogicalModelID != "lm1" {
		t.Fatalf("默认模型应取列表首项: %+v", list)
	}
}

// TestAccessSaveFormCodexRejectsEmptyList：空模型列表保存报中文错误（明确
// 动作提示），不落库。
func TestAccessSaveFormCodexRejectsEmptyList(t *testing.T) {
	st, a := uitest.New(t)
	st.Access.App = "codex"
	st.Access.Form = state.AccessForm{Open: true, Name: "cfg"}
	acc := bridge.AccessConfigs{A: a}
	saveForm(st, acc)
	if st.Access.Form.Msg != "请至少添加一个模型" {
		t.Fatalf("空列表应报错: %q", st.Access.Form.Msg)
	}
	if len(acc.List("codex")) != 0 {
		t.Fatal("空列表不得落库")
	}
}

// TestSaveFormAndApplyWritesLive 覆盖 P1-1：主按钮「保存并应用」保存后
// 异步写 Agent live 文件，完成提示为「已应用」；仅保存不写 live。
func TestSaveFormAndApplyWritesLive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{
		Open: true, Name: "cfg", ModelLabel: "my-code", DocJSON: `{"env":{}}`,
	}
	done := make(chan struct{})
	st.Dispatch = func(fn func()) { fn(); close(done) }
	saveFormWithApply(st, bridge.AccessConfigs{A: a}, true)
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("save+apply did not finish")
	}
	if !strings.Contains(st.Access.Msg, "已应用") {
		t.Fatalf("msg = %q", st.Access.Msg)
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "my-code") {
		t.Fatalf("live file not written: %s", b)
	}
}

// TestInvalidDocJSONBlocksSave 覆盖 P1-11：JSON 非法时编辑器显示错误、
// 保存被拒绝且不落库（避免「表单行变了、文档没变」的静默失同步）。
func TestInvalidDocJSONBlocksSave(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{
		Open: true, Name: "cfg", ModelLabel: "my-code", DocJSON: `{bad`,
	}
	tt := ui.NewTester(func(c *ui.Context) {
		EditorCard(c, c.Theme(), uikit.Softs(c.Theme()), st, bridge.AccessConfigs{A: a}, true, EditorHooks{})
	}, 1000, 1200)
	if !tt.HasText("配置 JSON 无效") {
		t.Fatalf("invalid JSON error missing: %v", tt.Texts())
	}
	saveForm(st, bridge.AccessConfigs{A: a})
	if !strings.Contains(st.Access.Form.Msg, "配置 JSON 无效") {
		t.Fatalf("save must be rejected: %q", st.Access.Form.Msg)
	}
	if got := (bridge.AccessConfigs{A: a}).List("claude"); len(got) != 0 {
		t.Fatalf("invalid JSON must not be saved: %+v", got)
	}
}

func TestSaveOnlyOnCurrentShowsApplyHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(livePath, []byte(`{"old":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a, Home: home}
	v, err := acc.Create("claude", "cfg", "lm1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(livePath)

	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{
		Open: true, EditID: v.ID, Name: "cfg", ModelLabel: "my-code",
		DocJSON: `{"env":{"CUSTOM":"x"}}`,
	}
	saveFormWithApply(st, acc, false)
	if !strings.Contains(st.Access.Msg, "点击「应用」") {
		t.Fatalf("仅保存应提示点击应用：%q", st.Access.Msg)
	}
	after, _ := os.ReadFile(livePath)
	if string(before) != string(after) {
		t.Fatal("仅保存不得改写 live")
	}
}

func TestEditorShowsDocDiff(t *testing.T) {
	st, a := uitest.New(t)
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{
		Open: true, EditID: "x", Name: "cfg", ModelLabel: "my-code",
		OriginalDoc: `{"env":{"A":"1"}}`,
		DocJSON:     `{"env":{"A":"2"}}`,
	}
	tt := ui.NewTester(func(c *ui.Context) {
		EditorCard(c, c.Theme(), uikit.Softs(c.Theme()), st, bridge.AccessConfigs{A: a}, true, EditorHooks{})
	}, 1000, 1400)
	if !tt.HasText("变更对比") || !tt.HasText("env.A") {
		t.Fatalf("diff missing: %v", tt.Texts())
	}
}
