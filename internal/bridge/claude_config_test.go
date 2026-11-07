package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/storage"
)

func TestReadClaudeConfigLayerUser(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	view, err := cfgs.ReadClaudeConfigLayer(ReadLayerRequest{Kind: claudecfg.ScopeUser})
	if err != nil {
		t.Fatal(err)
	}
	if !view.Exists || view.Doc["model"] != "sonnet" {
		t.Fatalf("view = %#v", view)
	}
	if view.RawJSON == "" {
		t.Fatal("raw json empty")
	}
}

func TestReadClaudeConfigLayerMissingAndBadJSON(t *testing.T) {
	home := t.TempDir()
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	view, err := cfgs.ReadClaudeConfigLayer(ReadLayerRequest{Kind: claudecfg.ScopeUser})
	if err != nil {
		t.Fatal(err)
	}
	if view.Exists || len(view.Doc) != 0 {
		t.Fatalf("missing view = %#v", view)
	}

	path := filepath.Join(home, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte("{broken"), 0o600)
	view, err = cfgs.ReadClaudeConfigLayer(ReadLayerRequest{Kind: claudecfg.ScopeUser})
	if err != nil {
		t.Fatal(err)
	}
	if view.ParseError == "" {
		t.Fatal("expected parse error")
	}
}

func TestReadClaudeConfigLayerProjectLocal(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "repo")
	os.MkdirAll(filepath.Join(project, ".git"), 0o755)
	path := filepath.Join(project, ".claude", "settings.local.json")
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{"model":"opus"}`), 0o600)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	view, err := cfgs.ReadClaudeConfigLayer(ReadLayerRequest{Kind: claudecfg.ScopeLocal, Project: project})
	if err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if view.File != real || view.Doc["model"] != "opus" {
		t.Fatalf("view = %#v", view)
	}
}

func TestManagedSetForClaude(t *testing.T) {
	a := tempApp(t)
	if _, err := a.Store().AccessConfigs().Save(storage.AccessConfig{
		AppType:        "claude",
		Name:           "main",
		LogicalModelID: "lm",
		Settings:       `{"permissions":{"allow":["Bash"]},"env":{"HTTPS_PROXY":"http://proxy"}}`,
	}); err != nil {
		t.Fatal(err)
	}
	cfgs := ClaudeConfigs{A: a, Home: t.TempDir()}
	ms, err := cfgs.ManagedSetForClaude()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms.StrongEnv) != 13 {
		t.Fatalf("strong env = %v", ms.StrongEnv)
	}
	if len(ms.StrongTopLevel) != 1 || ms.StrongTopLevel[0] != "permissions" {
		t.Fatalf("strong top = %v", ms.StrongTopLevel)
	}
	if len(ms.WeakEnv) != 1 || ms.WeakEnv[0] != "env.HTTPS_PROXY" {
		t.Fatalf("weak env = %v", ms.WeakEnv)
	}
}

func TestClaudeConfigOverviewWarnsConfigDir(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "/tmp/custom")
	cfgs := ClaudeConfigs{A: tempApp(t), Home: t.TempDir()}
	ov, err := cfgs.ClaudeConfigOverview()
	if err != nil {
		t.Fatal(err)
	}
	if ov.Warning == "" {
		t.Fatal("expected CLAUDE_CONFIG_DIR warning")
	}
}
