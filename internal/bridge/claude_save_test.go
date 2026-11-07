package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/storage"
)

func writeClaudeSettings(t *testing.T, home, body string) string {
	t.Helper()
	path := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	return path
}

func readClaudeTestDoc(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func mustReadClaudeLayer(t *testing.T, cfgs ClaudeConfigs, req ReadLayerRequest) LayerView {
	t.Helper()
	view, err := cfgs.ReadClaudeConfigLayer(req)
	if err != nil {
		t.Fatal(err)
	}
	return view
}

func mustSaveClaudeModel(t *testing.T, cfgs ClaudeConfigs, req ReadLayerRequest, model string) SaveResult {
	t.Helper()
	view := mustReadClaudeLayer(t, cfgs, req)
	doc := view.Doc
	doc["model"] = model
	res, err := cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind: req.Kind, Project: req.Project, Sub: req.Sub, LocalPath: req.LocalPath,
		Document: doc, ExpectedHash: view.Hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSaveClaudeConfigPatchesOnlyChangedLeaves(t *testing.T) {
	home := t.TempDir()
	path := writeClaudeSettings(t, home, `{"model":"sonnet","unknown":{"keep":true},"env":{"A":"1"}}`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	view := mustReadClaudeLayer(t, cfgs, ReadLayerRequest{Kind: claudecfg.ScopeUser})
	doc := view.Doc
	doc["model"] = "opus"
	res, err := cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind:         claudecfg.ScopeUser,
		Document:     doc,
		ExpectedHash: view.Hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changed) != 1 || res.Changed[0] != "model" {
		t.Fatalf("changed = %v", res.Changed)
	}
	got := readClaudeTestDoc(t, path)
	if got["model"] != "opus" {
		t.Fatalf("model = %#v", got["model"])
	}
	if got["unknown"].(map[string]any)["keep"] != true {
		t.Fatal("unknown key lost")
	}
	if res.BackupPath == "" {
		t.Fatal("missing backup path")
	}
	if fi, err := os.Stat(res.BackupPath); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("backup mode/stat = %v %v", fi, err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o640 {
		t.Fatalf("target mode = %v %v", fi, err)
	}
}

func TestSaveClaudeConfigConflict(t *testing.T) {
	home := t.TempDir()
	writeClaudeSettings(t, home, `{"model":"sonnet"}`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	view := mustReadClaudeLayer(t, cfgs, ReadLayerRequest{Kind: claudecfg.ScopeUser})
	doc := view.Doc
	doc["model"] = "opus"
	_, err := cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind: claudecfg.ScopeUser, Document: doc, ExpectedHash: "wrong",
	})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v", err)
	}
	if conflict.DiskHash != view.Hash {
		t.Fatalf("disk hash = %q, want %q", conflict.DiskHash, view.Hash)
	}
}

// 空 ExpectedHash 对已存在文件也必须是冲突：默认绝不能静默覆盖。
func TestSaveClaudeConfigRequiresMatchingHashForExistingFile(t *testing.T) {
	home := t.TempDir()
	path := writeClaudeSettings(t, home, `{"model":"sonnet"}`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	_, err := cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind: claudecfg.ScopeUser, Document: map[string]any{"model": "opus"},
	})
	var conflict *ConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("error = %v", err)
	}
	if got := readClaudeTestDoc(t, path); got["model"] != "sonnet" {
		t.Fatalf("file was overwritten: %#v", got)
	}
}

func TestSaveClaudeConfigRejectsManagedKey(t *testing.T) {
	home := t.TempDir()
	writeClaudeSettings(t, home, `{"env":{"ANTHROPIC_MODEL":"old"}}`)
	a := tempApp(t)
	if _, err := a.Store().AccessConfigs().Save(storage.AccessConfig{
		AppType: "claude", Name: "main", LogicalModelID: "lm", Settings: `{"env":{"ANTHROPIC_MODEL":"old"}}`,
	}); err != nil {
		t.Fatal(err)
	}
	cfgs := ClaudeConfigs{A: a, Home: home}
	view := mustReadClaudeLayer(t, cfgs, ReadLayerRequest{Kind: claudecfg.ScopeUser})
	doc := view.Doc
	doc["env"].(map[string]any)["ANTHROPIC_MODEL"] = "new"
	_, err := cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind: claudecfg.ScopeUser, Document: doc, ExpectedHash: view.Hash,
	})
	if !errors.Is(err, ErrManagedKey) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "env.ANTHROPIC_MODEL") {
		t.Fatalf("error does not name path: %v", err)
	}
}

// 删除整个 env 会连带删除强受管键，必须按受管键阻断。
func TestSaveClaudeConfigRejectsManagedEnvRemoval(t *testing.T) {
	home := t.TempDir()
	path := writeClaudeSettings(t, home, `{"model":"sonnet","env":{"ANTHROPIC_MODEL":"old","KEEP":"1"}}`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	view := mustReadClaudeLayer(t, cfgs, ReadLayerRequest{Kind: claudecfg.ScopeUser})
	_, err := cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind:         claudecfg.ScopeUser,
		Document:     map[string]any{"model": "sonnet"}, // env 被整体删除
		ExpectedHash: view.Hash,
	})
	if !errors.Is(err, ErrManagedKey) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(err.Error(), "env.ANTHROPIC_MODEL") {
		t.Fatalf("error does not name path: %v", err)
	}
	if got := readClaudeTestDoc(t, path); got["env"].(map[string]any)["KEEP"] != "1" {
		t.Fatalf("file changed despite managed-key rejection: %#v", got)
	}
}

func TestSaveClaudeConfigCreatesFile(t *testing.T) {
	home := t.TempDir()
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	_, err := cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind: claudecfg.ScopeUser, Document: map[string]any{"model": "sonnet"},
	})
	if err == nil {
		t.Fatal("expected missing-file error without Create")
	}
	if _, statErr := os.Stat(filepath.Join(home, ".claude")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf(".claude must not be created without Create: %v", statErr)
	}
	_, err = cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind: claudecfg.ScopeUser, Document: map[string]any{"model": "sonnet"}, Create: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readClaudeTestDoc(t, filepath.Join(home, ".claude", "settings.json")); got["model"] != "sonnet" {
		t.Fatalf("doc = %#v", got)
	}
}

func TestSaveClaudeConfigWriteFailurePreservesOriginal(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	home := t.TempDir()
	path := writeClaudeSettings(t, home, `{"model":"sonnet"}`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	view := mustReadClaudeLayer(t, cfgs, ReadLayerRequest{Kind: claudecfg.ScopeUser})
	doc := view.Doc
	doc["model"] = "opus"
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind: claudecfg.ScopeUser, Document: doc, ExpectedHash: view.Hash,
	}); err == nil {
		t.Fatal("expected write failure")
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := readClaudeTestDoc(t, path); got["model"] != "sonnet" {
		t.Fatalf("original file changed: %#v", got)
	}
}

func TestClaudeBackupRetentionAndRestore(t *testing.T) {
	home := t.TempDir()
	path := writeClaudeSettings(t, home, `{"model":"v0"}`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	req := ReadLayerRequest{Kind: claudecfg.ScopeUser}
	for i := 1; i <= 12; i++ {
		mustSaveClaudeModel(t, cfgs, req, "v"+strconv.Itoa(i))
	}
	backups, err := cfgs.ListClaudeConfigBackups(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 10 {
		t.Fatalf("backups = %d", len(backups))
	}
	// 恢复最早一份（当前保留区内的 v2 备份）。
	first := backups[len(backups)-1]
	if _, err := cfgs.RestoreClaudeConfigBackup(first.ID); err != nil {
		t.Fatal(err)
	}
	got := readClaudeTestDoc(t, path)
	if got["model"] == "v12" {
		t.Fatal("restore did not change file")
	}
}

func TestClaudeBackupMetaBindsTargetPath(t *testing.T) {
	home := t.TempDir()
	path := writeClaudeSettings(t, home, `{"model":"v0"}`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	res := mustSaveClaudeModel(t, cfgs, ReadLayerRequest{Kind: claudecfg.ScopeUser}, "v1")
	if res.BackupPath == "" {
		t.Fatal("missing backup path")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(real))
	wantBucket := filepath.Join(home, ".agentswitch", "backups", "claude-settings", hex.EncodeToString(sum[:])[:16])
	if filepath.Dir(res.BackupPath) != wantBucket {
		t.Fatalf("bucket = %q, want %q", filepath.Dir(res.BackupPath), wantBucket)
	}
	base := filepath.Base(res.BackupPath)
	if !strings.HasSuffix(base, "-settings.json") {
		t.Fatalf("backup name = %q", base)
	}
	tsPart := strings.TrimSuffix(base, "-settings.json")
	if _, err := strconv.ParseInt(tsPart, 10, 64); err != nil {
		t.Fatalf("backup timestamp prefix = %q: %v", tsPart, err)
	}
	var meta claudeBackupMeta
	b, err := os.ReadFile(filepath.Join(wantBucket, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.TargetPath != real || meta.Kind != "user" || meta.CreatedAt == 0 {
		t.Fatalf("meta = %#v", meta)
	}
}

func TestClaudeBackupBucketsPerTargetPath(t *testing.T) {
	home := t.TempDir()
	projects := []string{filepath.Join(home, "p1"), filepath.Join(home, "p2")}
	realPaths := make([]string, 0, len(projects))
	for _, project := range projects {
		if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(project, ".claude", "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(`{"model":"v0"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		real, err := filepath.EvalSymlinks(path)
		if err != nil {
			t.Fatal(err)
		}
		realPaths = append(realPaths, real)
	}
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	var dirs []string
	for i, project := range projects {
		req := ReadLayerRequest{Kind: claudecfg.ScopeProject, Project: project}
		mustSaveClaudeModel(t, cfgs, req, "v1")
		backups, err := cfgs.ListClaudeConfigBackups(req)
		if err != nil || len(backups) == 0 {
			t.Fatalf("backups = %v, err = %v", backups, err)
		}
		dir := filepath.Dir(backups[0].Path)
		dirs = append(dirs, dir)
		var meta claudeBackupMeta
		b, err := os.ReadFile(filepath.Join(dir, "meta.json"))
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, &meta); err != nil {
			t.Fatal(err)
		}
		if meta.TargetPath != realPaths[i] || meta.Project != project {
			t.Fatalf("meta = %#v", meta)
		}
	}
	if dirs[0] == dirs[1] {
		t.Fatalf("same bucket for different targets: %s", dirs[0])
	}
}

func TestRestoreClaudeConfigBackupSnapshotsCurrent(t *testing.T) {
	home := t.TempDir()
	path := writeClaudeSettings(t, home, `{"model":"v0"}`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	req := ReadLayerRequest{Kind: claudecfg.ScopeUser}
	mustSaveClaudeModel(t, cfgs, req, "v1") // backup v0
	mustSaveClaudeModel(t, cfgs, req, "v2") // backup v1
	backups, err := cfgs.ListClaudeConfigBackups(req)
	if err != nil || len(backups) != 2 {
		t.Fatalf("backups = %v, err = %v", backups, err)
	}
	oldest := backups[1]
	res, err := cfgs.RestoreClaudeConfigBackup(oldest.ID)
	if err != nil {
		t.Fatal(err)
	}
	if res.BackupPath == "" {
		t.Fatal("restore must snapshot current file before writing")
	}
	if got := readClaudeTestDoc(t, res.BackupPath); got["model"] != "v2" {
		t.Fatalf("snapshot = %#v", got)
	}
	if got := readClaudeTestDoc(t, path); got["model"] != "v0" {
		t.Fatalf("restored = %#v", got)
	}
	after, err := cfgs.ListClaudeConfigBackups(req)
	if err != nil || len(after) != 3 {
		t.Fatalf("backups after restore = %v, err = %v", after, err)
	}
}

func TestRestoreClaudeConfigBackupValidatesMetaTarget(t *testing.T) {
	home := t.TempDir()
	path := writeClaudeSettings(t, home, `{"model":"v0"}`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	req := ReadLayerRequest{Kind: claudecfg.ScopeUser}
	res := mustSaveClaudeModel(t, cfgs, req, "v1")
	bucket := filepath.Dir(res.BackupPath)
	var meta claudeBackupMeta
	b, err := os.ReadFile(filepath.Join(bucket, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	// 篡改 meta 指向另一个文件：恢复必须拒绝，不能把备份写到别的目标。
	meta.TargetPath = filepath.Join(home, "other.json")
	mb, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bucket, "meta.json"), mb, 0o600); err != nil {
		t.Fatal(err)
	}
	backups, err := cfgs.ListClaudeConfigBackups(req)
	if err != nil || len(backups) == 0 {
		t.Fatalf("backups = %v, err = %v", backups, err)
	}
	if _, err := cfgs.RestoreClaudeConfigBackup(backups[0].ID); err == nil {
		t.Fatal("expected meta/bucket mismatch error")
	}
	if got := readClaudeTestDoc(t, path); got["model"] != "v1" {
		t.Fatalf("target changed: %#v", got)
	}
}

func TestSaveClaudeConfigConcurrent(t *testing.T) {
	home := t.TempDir()
	writeClaudeSettings(t, home, `{"model":"v0"}`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	req := ReadLayerRequest{Kind: claudecfg.ScopeUser}
	var wg sync.WaitGroup
	var successes int32
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			view, err := cfgs.ReadClaudeConfigLayer(req)
			if err != nil {
				t.Error(err)
				return
			}
			doc := view.Doc
			doc["model"] = "v" + strconv.Itoa(i)
			_, err = cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
				Kind: claudecfg.ScopeUser, Document: doc, ExpectedHash: view.Hash,
			})
			if err == nil {
				atomic.AddInt32(&successes, 1)
				return
			}
			var conflict *ConflictError
			if !errors.As(err, &conflict) {
				t.Errorf("unexpected save error: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if successes == 0 {
		t.Fatal("no save succeeded")
	}
	got := readClaudeTestDoc(t, filepath.Join(home, ".claude", "settings.json"))
	if got["model"] == nil {
		t.Fatalf("corrupted file: %#v", got)
	}
}

// TestSaveClaudeConfigRebuildBrokenJSON 回归（I5）：损坏（非法 JSON）文件正常
// 保存被拒；Rebuild=true 时以空文档覆盖，损坏内容先备份，可再恢复。
func TestSaveClaudeConfigRebuildBrokenJSON(t *testing.T) {
	home := t.TempDir()
	path := writeClaudeSettings(t, home, `{"model":"sonnet", BROKEN`)
	cfgs := ClaudeConfigs{A: tempApp(t), Home: home}
	req := ReadLayerRequest{Kind: claudecfg.ScopeUser}

	// 正常保存对损坏文件报错，不覆盖。
	if _, err := cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind: claudecfg.ScopeUser, Document: map[string]any{}, ExpectedHash: "",
	}); err == nil || !strings.Contains(err.Error(), "不是合法 JSON") {
		t.Fatalf("plain save must refuse broken file: %v", err)
	}

	// Rebuild 以空基线覆盖：ExpectedHash 空与磁盘（解析失败的空 Hash）一致。
	res, err := cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind: claudecfg.ScopeUser, Document: map[string]any{},
		ExpectedHash: "", Changed: []string{"(rebuild)"}, Rebuild: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := readClaudeTestDoc(t, path); len(got) != 0 {
		t.Fatalf("rebuilt file must be empty object: %#v", got)
	}
	// 损坏内容已备份，恢复可找回。
	if res.BackupPath == "" {
		t.Fatal("rebuild must back up the broken content first")
	}
	broken, err := os.ReadFile(res.BackupPath)
	if err != nil || !strings.Contains(string(broken), "BROKEN") {
		t.Fatalf("backup must hold the broken content: %s %v", broken, err)
	}

	// Rebuild 不放过合法文件：ExpectedHash 空对真实磁盘 Hash 仍报冲突。
	view := mustReadClaudeLayer(t, cfgs, req)
	_, err = cfgs.SaveClaudeConfigLayer(SaveClaudeConfigRequest{
		Kind: claudecfg.ScopeUser, Document: map[string]any{}, ExpectedHash: "", Rebuild: true,
	})
	var ce *ConflictError
	if !errors.As(err, &ce) {
		t.Fatalf("rebuild over valid file must conflict, got %v", err)
	}
	_ = view
}
