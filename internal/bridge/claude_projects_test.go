package bridge

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListClaudeProjectsMapAndLegacyArray(t *testing.T) {
	home := t.TempDir()
	a := filepath.Join(home, "work", "a")
	b := filepath.Join(home, "work", "b")
	for _, dir := range []string{a, b} {
		if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	body := `{"projects":{"` + a + `":{},"` + b + `":{"lastSessionId":"x"}}}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	got, err := cfgs.ListClaudeProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("projects = %#v", got)
	}

	legacy := `{"projects":["` + a + `"]}`
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = cfgs.ListClaudeProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Path != a {
		t.Fatalf("legacy projects = %#v", got)
	}
}

func TestListClaudeProjectsBadFileDoesNotBlock(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, ".claude.json"), []byte("{broken"), 0o600)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	got, err := cfgs.ListClaudeProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("projects = %#v", got)
	}
}
