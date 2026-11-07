package bridge

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"AgentSwitch/internal/claudecfg"
)

// claudeSettingsMu 串行化接入 Apply 与配置中心保存对同一 settings.json 的写入。
var claudeSettingsMu sync.Mutex

// ErrManagedKey 表示保存试图修改接入投影键。
var ErrManagedKey = errors.New("受管键由「接入」管理，不能在设置区修改")

// ManagedKeyError 列出本次保存试图修改的受管键路径；它同时满足
// errors.Is(err, ErrManagedKey)。
type ManagedKeyError struct {
	Paths []string
}

func (e *ManagedKeyError) Error() string {
	return fmt.Sprintf("%v：%s", ErrManagedKey, strings.Join(e.Paths, ", "))
}

func (e *ManagedKeyError) Unwrap() error { return ErrManagedKey }

// ErrNoChanges 表示没有需要写入的变更。
var ErrNoChanges = errors.New("没有需要保存的修改")

// ConflictError 表示磁盘文件在打开后被外部修改。调用方确认覆盖时应以
// DiskHash 作为 ExpectedHash 重新保存。
type ConflictError struct {
	Path         string
	ExpectedHash string
	DiskHash     string
}

func (e *ConflictError) Error() string {
	return fmt.Sprintf("settings 已被外部修改（%s），请重新加载后再保存", e.Path)
}

// SaveClaudeConfigRequest 是一次分层保存请求。Changed 为空时按 Document 与
// 磁盘基线自动 diff。Rebuild 允许以空基线覆盖损坏（非法 JSON）的磁盘文件
// ——「用空配置重建」专用；损坏内容写入前仍会先备份。
type SaveClaudeConfigRequest struct {
	Kind         claudecfg.ScopeKind
	Project      string
	Sub          string
	LocalPath    string
	Document     map[string]any
	Changed      []string
	ExpectedHash string
	Create       bool
	Rebuild      bool
}

// SaveResult 是保存结果。
type SaveResult struct {
	File       string
	BackupPath string
	Changed    []string
	Effects    []string
}

// ClaudeBackupInfo 是一条 Claude 配置备份记录。ID 为 "<pathHash>/<backupFileName>"。
type ClaudeBackupInfo struct {
	ID        string
	File      string
	Path      string
	Size      int64
	CreatedAt int64
}

type claudeBackupMeta struct {
	TargetPath string `json:"targetPath"`
	Kind       string `json:"kind"`
	Project    string `json:"project"`
	Sub        string `json:"sub"`
	CreatedAt  int64  `json:"createdAt"`
}

// SaveClaudeConfigLayer 持锁重读、冲突检测、备份、patch 与原子写。
func (s ClaudeConfigs) SaveClaudeConfigLayer(req SaveClaudeConfigRequest) (SaveResult, error) {
	claudeSettingsMu.Lock()
	defer claudeSettingsMu.Unlock()

	home, err := s.home()
	if err != nil {
		return SaveResult{}, err
	}
	resolved, err := resolveClaudeLayer(home, ReadLayerRequest{
		Kind: req.Kind, Project: req.Project, Sub: req.Sub, LocalPath: req.LocalPath,
	})
	if err != nil {
		return SaveResult{}, err
	}
	if req.Kind == claudecfg.ScopeManaged {
		return SaveResult{}, fmt.Errorf("受管配置为只读，不能在此保存")
	}
	if req.Document == nil {
		return SaveResult{}, fmt.Errorf("保存请求缺少文档")
	}
	managed, err := s.ManagedSetForClaude()
	if err != nil {
		return SaveResult{}, err
	}

	base := map[string]any{}
	diskHash := ""
	exists := false
	b, readErr := os.ReadFile(resolved.File)
	if readErr == nil {
		exists = true
		doc, err := claudecfg.ParseDoc(b)
		switch {
		case err == nil:
			base = doc.Raw
			diskHash = doc.Hash
		case req.Rebuild:
			// 损坏文件以空文档为基线重建（ExpectedHash 仍须等于磁盘的空
			// Hash，绝不静默覆盖合法文件；写入前先备份损坏内容）。
		default:
			return SaveResult{}, fmt.Errorf("当前文件不是合法 JSON：%w", err)
		}
	} else if !errors.Is(readErr, fs.ErrNotExist) {
		return SaveResult{}, readErr
	} else if !req.Create {
		return SaveResult{}, fmt.Errorf("文件不存在：%s", resolved.File)
	}
	// 默认安全动作：ExpectedHash 必须与磁盘一致（含空 Hash 对不存在/坏文件），
	// 绝不静默覆盖。确认覆盖时调用方应以 ConflictError.DiskHash 重试。
	if req.ExpectedHash != diskHash {
		return SaveResult{}, &ConflictError{
			Path:         resolved.File,
			ExpectedHash: req.ExpectedHash,
			DiskHash:     diskHash,
		}
	}

	actual := claudecfg.Diff(base, req.Document)
	if paths := managedChangedPaths(actual, base, req.Document, managed); len(paths) > 0 {
		return SaveResult{}, &ManagedKeyError{Paths: paths}
	}
	changed := req.Changed
	if changed == nil {
		changed = actual
	}
	changed = append([]string(nil), changed...)
	sort.Strings(changed)
	if len(changed) == 0 {
		return SaveResult{}, ErrNoChanges
	}

	patched := claudecfg.ApplyPatch(base, changed, req.Document)
	out, err := (&claudecfg.Doc{Raw: patched}).Marshal()
	if err != nil {
		return SaveResult{}, err
	}
	backupPath := ""
	if exists {
		backupPath, err = backupClaudeSettings(home, resolved, req)
		if err != nil {
			return SaveResult{}, err
		}
	} else if err := os.MkdirAll(filepath.Dir(resolved.File), 0o755); err != nil {
		// 只有 Create 才会走到这里（文件不存在且未 Create 已在上面报错）。
		return SaveResult{}, err
	}
	if err := atomicWritePreserveMode(resolved.File, out); err != nil {
		return SaveResult{}, err
	}
	return SaveResult{
		File:       resolved.File,
		BackupPath: backupPath,
		Changed:    changed,
		Effects:    effectsFor(changed),
	}, nil
}

// managedChangedPaths 返回本次实际变化中触及强受管键的路径。整段 env 被删除
// 时 diff 只给出 "env"，这里展开为具体受影响的 env.<key>，便于 UI 定位。
func managedChangedPaths(changed []string, base, incoming map[string]any, managed claudecfg.ManagedSet) []string {
	strongEnv := make(map[string]bool, len(managed.StrongEnv))
	for _, k := range managed.StrongEnv {
		strongEnv[k] = true
	}
	seen := map[string]bool{}
	var out []string
	add := func(path string) {
		if !seen[path] {
			seen[path] = true
			out = append(out, path)
		}
	}
	for _, path := range changed {
		switch {
		case path == "env":
			for _, k := range managed.StrongEnv {
				if envHas(base, k) || envHas(incoming, k) {
					add("env." + k)
				}
			}
		case strings.HasPrefix(path, "env."):
			key := strings.SplitN(strings.TrimPrefix(path, "env."), ".", 2)[0]
			if strongEnv[key] {
				add(path)
			}
		default:
			for _, k := range managed.StrongTopLevel {
				if path == k || strings.HasPrefix(path, k+".") {
					add(path)
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

func envHas(doc map[string]any, key string) bool {
	env, ok := doc["env"].(map[string]any)
	if !ok {
		return false
	}
	_, ok = env[key]
	return ok
}

func effectsFor(paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		spec, ok := claudecfg.Lookup(path)
		if !ok {
			out = append(out, "下次启动")
			continue
		}
		switch spec.Apply {
		case claudecfg.ApplyHot:
			out = append(out, "已热生效")
		case claudecfg.ApplyRestart:
			out = append(out, "需重启")
		default:
			out = append(out, "下次启动")
		}
	}
	sort.Strings(out)
	return out
}

func backupClaudeSettings(home string, resolved claudecfg.Resolved, req SaveClaudeConfigRequest) (string, error) {
	dir := claudeBackupDir(home, resolved.File)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	baseName := filepath.Base(resolved.File)
	ts := time.Now().UnixMilli()
	var name string
	for {
		name = fmt.Sprintf("%d-%s", ts, baseName)
		_, statErr := os.Stat(filepath.Join(dir, name))
		if errors.Is(statErr, fs.ErrNotExist) {
			break
		}
		if statErr != nil {
			return "", statErr
		}
		ts++ // 同毫秒撞名：递增时间戳，保持 <ts>-<filename> 形状
	}
	data, err := os.ReadFile(resolved.File)
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	meta := claudeBackupMeta{
		TargetPath: resolved.File,
		Kind:       string(req.Kind),
		Project:    req.Project,
		Sub:        req.Sub,
		CreatedAt:  ts,
	}
	mb, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), append(mb, '\n'), 0o600); err != nil {
		return "", err
	}
	pruneClaudeBackups(dir, 10)
	return path, nil
}

func claudeBackupDir(home, target string) string {
	sum := sha256.Sum256([]byte(target))
	return filepath.Join(home, ".agentswitch", "backups", "claude-settings", hex.EncodeToString(sum[:])[:16])
}

// resolveClaudeLayer 解析层路径；LocalPath 在候选路径中显式选择时，写入与
// 备份桶都绑定该候选的真实路径（存在符号链接时取 realpath）。
func resolveClaudeLayer(home string, req ReadLayerRequest) (claudecfg.Resolved, error) {
	resolved, err := claudecfg.Resolve(req.Kind, req.Project, claudeEnv(home))
	if err != nil {
		return resolved, err
	}
	if req.Kind != claudecfg.ScopeLocal || req.LocalPath == "" {
		return resolved, nil
	}
	for _, candidate := range resolved.Candidates {
		if filepath.Clean(candidate) != filepath.Clean(req.LocalPath) {
			continue
		}
		resolved.File = candidate
		if real, err := filepath.EvalSymlinks(candidate); err == nil {
			resolved.File = real
		} else if !errors.Is(err, fs.ErrNotExist) {
			return resolved, err
		}
		break
	}
	return resolved, nil
}

func pruneClaudeBackups(dir string, keep int) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || e.Name() == "meta.json" {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for len(names) > keep {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}

// ListClaudeConfigBackups 列出某层的备份，最新在前。
func (s ClaudeConfigs) ListClaudeConfigBackups(req ReadLayerRequest) ([]ClaudeBackupInfo, error) {
	home, err := s.home()
	if err != nil {
		return nil, err
	}
	resolved, err := resolveClaudeLayer(home, req)
	if err != nil {
		return nil, err
	}
	dir := claudeBackupDir(home, resolved.File)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	hash := filepath.Base(dir)
	out := make([]ClaudeBackupInfo, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || e.Name() == "meta.json" {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, ClaudeBackupInfo{
			ID:        hash + "/" + e.Name(),
			File:      e.Name(),
			Path:      filepath.Join(dir, e.Name()),
			Size:      fi.Size(),
			CreatedAt: claudeBackupTimestamp(e.Name(), fi.ModTime().UnixMilli()),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt == out[j].CreatedAt {
			return out[i].File > out[j].File
		}
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out, nil
}

// claudeBackupTimestamp 从 <unixMilli>-<filename> 解析时间戳；无法解析时回退
// 文件 mtime（兼容用户手工放入桶内的文件）。
func claudeBackupTimestamp(name string, fallback int64) int64 {
	i := strings.IndexByte(name, '-')
	if i <= 0 {
		return fallback
	}
	ts, err := strconv.ParseInt(name[:i], 10, 64)
	if err != nil {
		return fallback
	}
	return ts
}

// RestoreClaudeConfigBackup 把指定备份写回其 meta 记录的目标文件。id 为
// "<pathHash>/<backupFileName>"。恢复前持锁先快照当前目标文件（若存在），
// 使恢复本身可撤销；meta 的目标必须与 id 的桶一致，避免写错文件。
func (s ClaudeConfigs) RestoreClaudeConfigBackup(id string) (SaveResult, error) {
	claudeSettingsMu.Lock()
	defer claudeSettingsMu.Unlock()

	home, err := s.home()
	if err != nil {
		return SaveResult{}, err
	}
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" ||
		parts[1] != filepath.Base(parts[1]) || parts[1] == "meta.json" {
		return SaveResult{}, fmt.Errorf("备份 id 非法")
	}
	dir := filepath.Join(home, ".agentswitch", "backups", "claude-settings", parts[0])
	var meta claudeBackupMeta
	metaBytes, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return SaveResult{}, err
	}
	if err := json.Unmarshal(metaBytes, &meta); err != nil {
		return SaveResult{}, err
	}
	if meta.TargetPath == "" || filepath.Base(claudeBackupDir(home, meta.TargetPath)) != parts[0] {
		return SaveResult{}, fmt.Errorf("备份 meta 目标与备份桶不匹配，拒绝恢复")
	}
	data, err := os.ReadFile(filepath.Join(dir, parts[1]))
	if err != nil {
		return SaveResult{}, err
	}
	backupPath := ""
	if _, statErr := os.Stat(meta.TargetPath); statErr == nil {
		backupPath, err = backupClaudeSettings(home, claudecfg.Resolved{File: meta.TargetPath}, SaveClaudeConfigRequest{
			Kind: claudecfg.ScopeKind(meta.Kind), Project: meta.Project, Sub: meta.Sub,
		})
		if err != nil {
			return SaveResult{}, err
		}
	} else if !errors.Is(statErr, fs.ErrNotExist) {
		return SaveResult{}, statErr
	}
	if err := os.MkdirAll(filepath.Dir(meta.TargetPath), 0o755); err != nil {
		return SaveResult{}, err
	}
	if err := atomicWritePreserveMode(meta.TargetPath, data); err != nil {
		return SaveResult{}, err
	}
	return SaveResult{File: meta.TargetPath, BackupPath: backupPath, Changed: []string{"(restore)"}}, nil
}

func atomicWritePreserveMode(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
