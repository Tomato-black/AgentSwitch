package claudecfg

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func canon(t *testing.T, path string) string {
	t.Helper()
	out, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestResolveUser(t *testing.T) {
	home := t.TempDir()
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{}`)
	got, err := Resolve(ScopeUser, "", Env{Home: home, GOOS: "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != ScopeUser {
		t.Fatalf("kind = %q", got.Kind)
	}
	if want := filepath.Join(canon(t, home), ".claude", "settings.json"); got.File != want {
		t.Fatalf("file = %q, want %q", got.File, want)
	}
	if got.Display != "~/.claude/settings.json" {
		t.Fatalf("display = %q", got.Display)
	}
	if !got.Exists {
		t.Fatal("Exists = false")
	}
}

func TestResolveProject(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "work", "demo")
	writeFile(t, filepath.Join(project, ".claude", "settings.json"), `{}`)
	got, err := Resolve(ScopeProject, project, Env{Home: home, GOOS: "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(canon(t, project), ".claude", "settings.json"); got.File != want {
		t.Fatalf("file = %q, want %q", got.File, want)
	}
	if !got.Exists {
		t.Fatal("Exists = false")
	}
}

func TestResolveLocalUsesGitRootAndKeepsProjectCandidate(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "repo")
	sub := filepath.Join(project, "pkg", "api")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(ScopeLocal, sub, Env{Home: home, GOOS: "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(project, ".claude", "settings.local.json")
	if got.File != want {
		t.Fatalf("file = %q, want %q", got.File, want)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("candidates = %v", got.Candidates)
	}
	if got.Candidates[0] != want || got.Candidates[1] != filepath.Join(sub, ".claude", "settings.local.json") {
		t.Fatalf("candidates = %v", got.Candidates)
	}
}

func TestResolveLocalNonGitFallsBackToProject(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(home, "plain")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(ScopeLocal, project, Env{Home: home, GOOS: "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(project, ".claude", "settings.local.json")
	if got.File != want {
		t.Fatalf("file = %q, want %q", got.File, want)
	}
	if len(got.Candidates) != 1 {
		t.Fatalf("candidates = %v", got.Candidates)
	}
}

func TestResolveLocalHomeGitRootFallsBack(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(home, "repo")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(ScopeLocal, project, Env{Home: home, GOOS: "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(project, ".claude", "settings.local.json"); got.File != want {
		t.Fatalf("file = %q, want %q", got.File, want)
	}
	if got.Note == "" {
		t.Fatal("expected fallback note")
	}
}

func TestResolveManagedPaths(t *testing.T) {
	cases := []struct {
		goos string
		want string
	}{
		{"darwin", "/Library/Application Support/ClaudeCode/managed-settings.json"},
		{"linux", "/etc/claude-code/managed-settings.json"},
		{"windows", `C:\Program Files\ClaudeCode\managed-settings.json`},
	}
	home := t.TempDir()
	for _, tc := range cases {
		got, err := Resolve(ScopeManaged, "", Env{Home: home, GOOS: tc.goos})
		if err != nil {
			t.Fatalf("%s: %v", tc.goos, err)
		}
		if got.File != tc.want {
			t.Fatalf("%s: file = %q, want %q", tc.goos, got.File, tc.want)
		}
	}
}

func TestResolveSymlinkKeepsRealPath(t *testing.T) {
	home := t.TempDir()
	real := filepath.Join(home, "real-settings.json")
	writeFile(t, real, `{}`)
	linkDir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(linkDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, filepath.Join(linkDir, "settings.json")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	got, err := Resolve(ScopeUser, "", Env{Home: home, GOOS: "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	if got.File != canon(t, real) {
		t.Fatalf("file = %q, want real %q", got.File, canon(t, real))
	}
}

func TestResolveRequiresProject(t *testing.T) {
	home := t.TempDir()
	for _, kind := range []ScopeKind{ScopeProject, ScopeLocal} {
		if _, err := Resolve(kind, "", Env{Home: home, GOOS: "darwin"}); err == nil {
			t.Fatalf("%s: expected error", kind)
		} else if !strings.Contains(err.Error(), "项目") {
			t.Fatalf("%s: error = %v", kind, err)
		}
	}
}
