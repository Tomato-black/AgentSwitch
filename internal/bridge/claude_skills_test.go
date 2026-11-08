package bridge

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	got, _, err := cfgs.ListClaudeSkills(project)
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
	got, _, err := cfgs.ListClaudeSkills("")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range got {
		if s.Source != claudecfg.SkillSourceBuiltIn {
			t.Fatalf("unexpected non-builtin skill: %#v", s)
		}
	}
}

// TestClaudeLayersLocalUsesGitRoot 回归（I3）：合并预览的项目本地层曾硬编码
// <project>/.claude/settings.local.json，与编辑器（Git 根默认）不一致——
// 仓库子目录项目的本地层必须落到 Git 根路径。
func TestClaudeLayersLocalUsesGitRoot(t *testing.T) {
	home := t.TempDir()
	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(repo, "sub", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	gitRootLocal := filepath.Join(repo, ".claude", "settings.local.json")
	if err := os.MkdirAll(filepath.Dir(gitRootLocal), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gitRootLocal, []byte(`{"env":{"LOCAL":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	layers := cfgs.claudeLayers(home, sub)
	var local *claudecfg.Layer
	for i := range layers {
		if layers[i].Kind == claudecfg.ScopeLocal {
			local = &layers[i]
		}
	}
	if local == nil {
		t.Fatalf("local layer missing: %#v", layers)
	}
	want, err := filepath.EvalSymlinks(gitRootLocal)
	if err != nil {
		t.Fatal(err)
	}
	if local.File != want {
		t.Fatalf("local layer file = %q, want git root %q", local.File, want)
	}
	// 合并视图也要能取到该本地键。
	merged, entries, err := cfgs.MergeClaudeConfigView(sub)
	if err != nil {
		t.Fatal(err)
	}
	env, _ := merged["env"].(map[string]any)
	if env["LOCAL"] != "1" {
		t.Fatalf("merged env = %v", env)
	}
	if entries["env.LOCAL"].Source != claudecfg.ScopeLocal {
		t.Fatalf("env.LOCAL source = %#v", entries["env.LOCAL"])
	}
}

// TestReadManagedLayerMergesDropins 回归（I2，spec §6.1.1）：托管层必须合并
// managed-settings.json 与 managed-settings.d/*.json（文件名序）。
func TestReadManagedLayerMergesDropins(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "managed-settings.json")
	if err := os.WriteFile(base, []byte(`{"model":"sonnet","permissions":{"allow":["Bash"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dropDir := filepath.Join(dir, "managed-settings.d")
	if err := os.MkdirAll(dropDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 文件名序：10-a 先于 20-b；非 .json 与子目录跳过；损坏文件跳过。
	if err := os.WriteFile(filepath.Join(dropDir, "20-b.json"), []byte(`{"permissions":{"allow":["Read"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropDir, "10-a.json"), []byte(`{"model":"opus","permissions":{"deny":["Web"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropDir, "30-c.txt"), []byte(`{"model":"ignored"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropDir, "40-d.json"), []byte(`{broken`), 0o600); err != nil {
		t.Fatal(err)
	}

	layer := readManagedLayer(base)
	if layer == nil {
		t.Fatal("managed layer missing")
	}
	if layer.Kind != claudecfg.ScopeManaged || layer.File != base {
		t.Fatalf("layer = %#v", layer)
	}
	if layer.Doc["model"] != "opus" {
		t.Fatalf("drop-in scalar must win: %v", layer.Doc["model"])
	}
	perm := layer.Doc["permissions"].(map[string]any)
	if len(perm["allow"].([]any)) != 2 || len(perm["deny"].([]any)) != 1 {
		t.Fatalf("permissions merge = %#v", perm)
	}
}

// TestReadManagedLayerOnlyDropins：base 不存在但 drop-in 存在时仍构成托管层。
func TestReadManagedLayerOnlyDropins(t *testing.T) {
	dir := t.TempDir()
	dropDir := filepath.Join(dir, "managed-settings.d")
	if err := os.MkdirAll(dropDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dropDir, "a.json"), []byte(`{"env":{"A":"1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	layer := readManagedLayer(filepath.Join(dir, "managed-settings.json"))
	if layer == nil {
		t.Fatal("drop-in-only managed layer missing")
	}
	env := layer.Doc["env"].(map[string]any)
	if env["A"] != "1" {
		t.Fatalf("doc = %v", layer.Doc)
	}
}

// TestReadManagedLayerEmpty：base 与 drop-in 都不可读时无托管层。
func TestReadManagedLayerEmpty(t *testing.T) {
	if layer := readManagedLayer(filepath.Join(t.TempDir(), "managed-settings.json")); layer != nil {
		t.Fatalf("layer = %#v", layer)
	}
}

// TestClaudeEnvPassesRuntimeGOOS：托管层平台路径由 GOOS 决定，bridge 必须
// 透传 runtime.GOOS（纯路径的 Windows 断言见 claudecfg paths_test）。
func TestClaudeEnvPassesRuntimeGOOS(t *testing.T) {
	env := claudeEnv("/tmp/home")
	if env.Home != "/tmp/home" || env.GOOS != runtime.GOOS {
		t.Fatalf("env = %#v", env)
	}
}

// TestListClaudeSkillsScanWarnings 覆盖 D2：目录缺失静默；目录存在但不可读、
// installed_plugins.json 损坏时上浮为告警（列表仍返回），Skill.md 缺失的
// 子目录不计。
func TestListClaudeSkillsScanWarnings(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := t.TempDir()
	// 用户 Skills 目录存在但无读权限 → 告警。
	skillsDir := filepath.Join(home, ".claude", "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(skillsDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(skillsDir, 0o755) })
	// installed_plugins.json 损坏 → 告警。
	if err := os.MkdirAll(filepath.Join(home, ".claude", "plugins"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	got, warns, err := cfgs.ListClaudeSkills("")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("内置 Skills 仍应返回")
	}
	if len(warns) != 2 {
		t.Fatalf("warns = %v", warns)
	}
	foundDir, foundJSON := false, false
	for _, w := range warns {
		if strings.Contains(w, "skills") && strings.Contains(w, "不可读") {
			foundDir = true
		}
		if strings.Contains(w, "installed_plugins.json") {
			foundJSON = true
		}
	}
	if !foundDir || !foundJSON {
		t.Fatalf("warns = %v", warns)
	}
}
