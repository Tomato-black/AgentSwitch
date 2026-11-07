package bridge

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"AgentSwitch/internal/claudecfg"
)

// TestRestoreLatestBackupClaude：备份后覆盖 live，再还原应恢复备份内容。
func TestRestoreLatestBackupClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	acc := AccessConfigs{A: tempApp(t), Home: home}
	live := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte(`{"old":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := acc.backupLive("claude"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte(`{"new":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := acc.RestoreLatestBackup("claude"); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(live)
	if err != nil || string(b) != `{"old":true}` {
		t.Fatalf("restore = %s, %v", b, err)
	}
	// 恢复本身也被快照：第二次还原应回到恢复前的（已应用）状态。
	if err := acc.RestoreLatestBackup("claude"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(live)
	if string(b) != `{"new":true}` {
		t.Fatalf("second restore should be reversible: %s", b)
	}
}

// TestRestoreLatestBackupNoBackup：没有备份时报中文错误。
func TestRestoreLatestBackupNoBackup(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	if err := acc.RestoreLatestBackup("claude"); err == nil {
		t.Fatal("应报没有可恢复的备份")
	}
}

// TestRestoreLatestBackupDesktopSlots：Desktop 两个同名配置文件按槽位区分，
// 还原时各自回到原路径。
func TestRestoreLatestBackupDesktopSlots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	p := desktopPathsFor(home)
	seed := map[string]string{p.normalConfig: "N-old", p.threepConfig: "T-old"}
	for path, data := range seed {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	acc := AccessConfigs{A: tempApp(t), Home: home}
	if err := acc.backupLive("claude_desktop"); err != nil {
		t.Fatal(err)
	}
	for path := range seed {
		if err := os.WriteFile(path, []byte("changed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := acc.RestoreLatestBackup("claude_desktop"); err != nil {
		t.Fatal(err)
	}
	for path, want := range seed {
		b, err := os.ReadFile(path)
		if err != nil || string(b) != want {
			t.Fatalf("%s = %s, %v (want %s)", path, b, err, want)
		}
	}
}

// TestParseBackupName：解析「<ts>[-n]-<slot>」两种命名。
func TestParseBackupName(t *testing.T) {
	for name, want := range map[string]string{
		"1700000000000-settings.json":   "settings.json",
		"1700000000000-1-settings.json": "settings.json",
		"1700000000000-2-meta.json":     "meta.json",
	} {
		ts, slot, ok := parseBackupName(name)
		if !ok || ts != 1700000000000 || slot != want {
			t.Fatalf("%s => %d %q %v", name, ts, slot, ok)
		}
	}
	if _, _, ok := parseBackupName("garbage"); ok {
		t.Fatal("非法名字应拒绝")
	}
}

// TestRestoreBackupSerializesWithConfigSave 回归（C3，需 -race）：接入页
// RestoreBackup 与配置中心 SaveClaudeConfigLayer 是同一 ~/.claude/settings.json
// 的两个写入口，必须经 claudeSettingsMu 串行化。并发交替执行后文件仍是
// 合法 JSON（未被半写状态穿透）；冲突检测错误属正常竞争结果。
func TestRestoreBackupSerializesWithConfigSave(t *testing.T) {
	home := t.TempDir()
	a := tempApp(t)
	live := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(live), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(live, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a, Home: home}
	if err := acc.backupLive("claude"); err != nil {
		t.Fatal(err)
	}
	backups, err := acc.ListBackups("claude")
	if err != nil || len(backups) == 0 {
		t.Fatalf("backups = %v, %v", backups, err)
	}

	const rounds = 20
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { // 接入页还原循环（每轮取最新备份——目录只保留 10 份）
		defer wg.Done()
		for i := 0; i < rounds; i++ {
			if err := acc.RestoreLatestBackup("claude"); err != nil {
				t.Errorf("restore: %v", err)
				return
			}
		}
	}()
	go func() { // 配置中心保存循环
		defer wg.Done()
		cc := ClaudeConfigs{A: a, Home: home}
		for i := 0; i < rounds; i++ {
			view, err := cc.ReadClaudeConfigLayer(ReadLayerRequest{Kind: claudecfg.ScopeUser})
			if err != nil {
				t.Errorf("read layer: %v", err)
				return
			}
			doc := map[string]any{"model": "opus", "round": i}
			_, err = cc.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
				Kind: claudecfg.ScopeUser, Document: doc,
				ExpectedHash: view.Hash, Create: !view.Exists,
			})
			var ce *ConflictError
			if err != nil && !errors.As(err, &ce) && !errors.Is(err, ErrNoChanges) {
				t.Errorf("save: %v", err)
				return
			}
		}
	}()
	wg.Wait()

	if _, err := claudecfg.ParseDoc(mustReadFile(t, live)); err != nil {
		t.Fatalf("settings.json corrupted after concurrent restore+save: %v", err)
	}
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
