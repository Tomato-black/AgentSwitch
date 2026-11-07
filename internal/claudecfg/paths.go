// Package claudecfg 是 Claude Code 分层 settings 的纯逻辑层：路径解析、键
// 注册表、严格 JSON 文档、跨层合并、scope 校验、敏感值与 Skills 状态。
// 本包不依赖 UI、bridge 或 MyGo，所有平台/Home 差异通过 Env 注入。
package claudecfg

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ScopeKind 是 Claude Code settings 的层级。
type ScopeKind string

const (
	ScopeUser    ScopeKind = "user"
	ScopeProject ScopeKind = "project"
	ScopeLocal   ScopeKind = "local"
	ScopeManaged ScopeKind = "managed"
)

// Env 是路径解析所需的注入环境，便于测试与 Windows 映射。
type Env struct {
	Home string
	GOOS string
}

// Resolved 是一次层级解析的结果。File 是真实写入路径（已解析符号链接），
// Display 是 UI 展示路径（Home 前缀显示为 ~），Candidates 仅项目本地层使用。
type Resolved struct {
	Kind       ScopeKind
	File       string
	Display    string
	Project    string
	Exists     bool
	Note       string
	Candidates []string
}

// Resolve 解析指定层级的 settings 文件路径。
func Resolve(kind ScopeKind, project string, env Env) (Resolved, error) {
	if env.Home == "" {
		return Resolved{}, errors.New("未提供用户主目录")
	}
	home := filepath.Clean(env.Home)
	switch kind {
	case ScopeUser:
		return resolveFile(kind, home, filepath.Join(home, ".claude", "settings.json"), "", nil)
	case ScopeProject:
		if strings.TrimSpace(project) == "" {
			return Resolved{}, errors.New("项目共享配置需要选择项目目录")
		}
		abs, err := absPath(project)
		if err != nil {
			return Resolved{}, err
		}
		return resolveFile(kind, home, filepath.Join(abs, ".claude", "settings.json"), abs, nil)
	case ScopeLocal:
		if strings.TrimSpace(project) == "" {
			return Resolved{}, errors.New("项目本地配置需要选择项目目录")
		}
		abs, err := absPath(project)
		if err != nil {
			return Resolved{}, err
		}
		root, found := gitRoot(abs)
		primaryDir := abs
		note := ""
		switch {
		case !found:
			note = "未找到 Git 仓库根，使用项目目录"
		case samePath(root, home):
			note = "Git 仓库根是用户主目录，回退到项目目录"
		default:
			primaryDir = root
		}
		primary := filepath.Join(primaryDir, ".claude", "settings.local.json")
		candidates := []string{primary}
		if projectPath := filepath.Join(abs, ".claude", "settings.local.json"); !samePath(projectPath, primary) {
			candidates = append(candidates, projectPath)
		}
		return resolveFile(kind, home, primary, abs, candidates, note)
	case ScopeManaged:
		return resolveManaged(home, env.GOOS)
	default:
		return Resolved{}, fmt.Errorf("未知配置层级: %s", kind)
	}
}

func resolveFile(kind ScopeKind, home, path, project string, candidates []string, note ...string) (Resolved, error) {
	display := displayPath(home, path)
	exists := false
	real := path
	if _, err := os.Stat(path); err == nil {
		exists = true
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			real = resolved
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return Resolved{}, err
	}
	out := Resolved{
		Kind:       kind,
		File:       real,
		Display:    display,
		Project:    project,
		Exists:     exists,
		Candidates: candidates,
	}
	if len(note) > 0 {
		out.Note = note[0]
	}
	return out, nil
}

func resolveManaged(home, goos string) (Resolved, error) {
	var path string
	switch goos {
	case "windows":
		path = `C:\Program Files\ClaudeCode\managed-settings.json`
	case "linux":
		path = "/etc/claude-code/managed-settings.json"
	default: // darwin 及其它 Unix
		path = "/Library/Application Support/ClaudeCode/managed-settings.json"
	}
	exists := false
	if _, err := os.Stat(path); err == nil {
		exists = true
	}
	return Resolved{Kind: ScopeManaged, File: path, Display: path, Exists: exists}, nil
}

func absPath(p string) (string, error) {
	if filepath.IsAbs(p) {
		return filepath.Clean(p), nil
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	return abs, nil
}

func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

// gitRoot 从 dir 向上查找 .git（目录或 worktree 文件）。返回根目录与是否找到。
func gitRoot(dir string) (string, bool) {
	cur := filepath.Clean(dir)
	for {
		if _, err := os.Stat(filepath.Join(cur, ".git")); err == nil {
			return cur, true
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", false
		}
		cur = parent
	}
}

func displayPath(home, path string) string {
	clean := filepath.Clean(path)
	if clean == home {
		return "~"
	}
	prefix := home + string(filepath.Separator)
	if strings.HasPrefix(clean, prefix) {
		return "~" + string(filepath.Separator) + strings.TrimPrefix(clean, prefix)
	}
	return clean
}
