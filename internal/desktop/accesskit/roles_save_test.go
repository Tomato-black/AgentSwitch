package accesskit

import (
	"testing"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
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
	i := RoleIndexByEnvKey("ANTHROPIC_DEFAULT_HAIKU_MODEL")
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
	SaveForm(st, acc)
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
	SaveForm(st, acc)
	if st.Access.Form.Msg != "请至少添加一个模型" {
		t.Fatalf("空列表应报错: %q", st.Access.Form.Msg)
	}
	if len(acc.List("codex")) != 0 {
		t.Fatal("空列表不得落库")
	}
}
