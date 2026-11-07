package bridge

import (
	"os"
	"path/filepath"
	"testing"

	"AgentSwitch/internal/claudecfg"
)

func writeSkill(t *testing.T, dir, name, frontmatter string) {
	t.Helper()
	path := filepath.Join(dir, name, "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(frontmatter), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestListClaudeSkillsSources(t *testing.T) {
	home := t.TempDir()
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "commit", "---\ndescription: Commit changes\n---\nbody\n")
	writeSkill(t, filepath.Join(home, ".claude", "skills"), "review", "---\ndescription: Review code\ndisable-model-invocation: true\n---\nbody\n")

	project := filepath.Join(home, "proj")
	writeSkill(t, filepath.Join(project, ".claude", "skills"), "audit", "---\ndescription: Project audit\n---\nbody\n")

	pluginRoot := filepath.Join(home, ".claude", "plugins", "cache", "market", "plug", "1.0")
	writeSkill(t, filepath.Join(pluginRoot, "skills"), "deploy", "---\ndescription: Deploy\n---\nbody\n")
	pluginsJSON := `{"version":2,"plugins":{"plug@market":[{"scope":"user","installPath":"` + pluginRoot + `","version":"1.0"}]}}`
	if err := os.WriteFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), []byte(pluginsJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"enabledPlugins":{"plug@market":false},"skillOverrides":{"old-skill":"off"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	got, err := cfgs.ListClaudeSkills(project)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]SkillView{}
	for _, s := range got {
		byName[s.Name] = s
	}
	if byName["commit"].Source != claudecfg.SkillSourceUser || byName["commit"].Effective != claudecfg.SkillOn {
		t.Fatalf("commit = %#v", byName["commit"])
	}
	if byName["audit"].Source != claudecfg.SkillSourceProject {
		t.Fatalf("audit = %#v", byName["audit"])
	}
	if byName["plug:deploy"].Source != claudecfg.SkillSourcePlugin || byName["plug:deploy"].Effective != claudecfg.SkillOff || byName["plug:deploy"].LockedBy != "plugin" {
		t.Fatalf("plugin = %#v", byName["plug:deploy"])
	}
	if byName["review"].LockedBy != "author" || byName["review"].Effective != claudecfg.SkillUserInvocableOnly {
		t.Fatalf("review = %#v", byName["review"])
	}
	if byName["old-skill"].Source != claudecfg.SkillSourceUnknown || byName["old-skill"].Effective != claudecfg.SkillOff {
		t.Fatalf("unknown = %#v", byName["old-skill"])
	}
}

func TestListClaudeSkillsMissingDirs(t *testing.T) {
	cfgs := ClaudeConfigs{A: tempApp(t), Home: t.TempDir()}
	got, err := cfgs.ListClaudeSkills("")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.Source != claudecfg.SkillSourceBuiltIn {
			t.Fatalf("unexpected non-builtin skill: %#v", s)
		}
	}
}
