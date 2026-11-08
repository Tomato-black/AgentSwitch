package bridge

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ClaudeProject 是项目选择器的一行。
type ClaudeProject struct {
	Path         string
	Name         string
	Exists       bool
	SharedFile   string
	LocalFile    string
	SharedExists bool
	LocalExists  bool
	RecentAt     int64
	RecentBasis  string
}

// ListClaudeProjects 读取 ~/.claude.json 的 projects（map 契约，兼容数组旧形态）。
func (s ClaudeConfigs) ListClaudeProjects() ([]ClaudeProject, error) {
	home, err := s.home()
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var root map[string]any
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, nil
	}
	paths := projectPaths(root["projects"])
	out := make([]ClaudeProject, 0, len(paths))
	for _, p := range paths {
		if strings.TrimSpace(p) == "" {
			continue
		}
		p = filepath.Clean(p)
		fi, statErr := os.Stat(p)
		item := ClaudeProject{
			Path:       p,
			Name:       filepath.Base(p),
			Exists:     statErr == nil && fi.IsDir(),
			SharedFile: filepath.Join(p, ".claude", "settings.json"),
			LocalFile:  filepath.Join(p, ".claude", "settings.local.json"),
		}
		item.SharedExists = fileExists(item.SharedFile)
		item.LocalExists = fileExists(item.LocalFile)
		item.RecentAt = recentAt(home, p, fi)
		if item.RecentAt > 0 {
			item.RecentBasis = "最近活动"
		}
		out = append(out, item)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].RecentAt != out[j].RecentAt {
			return out[i].RecentAt > out[j].RecentAt
		}
		return out[i].Path < out[j].Path
	})
	return out, nil
}

func projectPaths(v any) []string {
	switch x := v.(type) {
	case map[string]any:
		out := make([]string, 0, len(x))
		for p := range x {
			out = append(out, p)
		}
		return out
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func fileExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && !fi.IsDir()
}

func recentAt(home, project string, dirInfo os.FileInfo) int64 {
	encoded := strings.NewReplacer("/", "-", "\\", "-", ":", "-").Replace(project)
	dir := filepath.Join(home, ".claude", "projects", encoded)
	var newest int64
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			if fi, err := e.Info(); err == nil && fi.ModTime().UnixMilli() > newest {
				newest = fi.ModTime().UnixMilli()
			}
		}
	}
	if newest > 0 {
		return newest
	}
	if dirInfo != nil {
		return dirInfo.ModTime().UnixMilli()
	}
	return 0
}

// claudeRecentProjectsKey 记录用户在应用内选择过的项目（E2：选择器置顶）。
const claudeRecentProjectsKey = "claude.recent_projects"

// RecentProjects 返回应用内最近选择的项目路径（新→旧，上限 5）。
func (s ClaudeConfigs) RecentProjects() []string {
	raw, err := s.A.Store().Settings().Get(claudeRecentProjectsKey)
	if err != nil || raw == "" {
		return nil
	}
	var out []string
	if json.Unmarshal([]byte(raw), &out) != nil {
		return nil
	}
	return out
}

// RememberProject 把项目头插进最近列表（去重、上限 5）并持久化。
func (s ClaudeConfigs) RememberProject(path string) []string {
	if strings.TrimSpace(path) == "" {
		return s.RecentProjects()
	}
	prev := s.RecentProjects()
	out := make([]string, 0, 5)
	out = append(out, filepath.Clean(path))
	for _, p := range prev {
		if len(out) >= 5 {
			break
		}
		if p == out[0] {
			continue
		}
		out = append(out, p)
	}
	if b, err := json.Marshal(out); err == nil {
		_ = s.A.Store().Settings().Set(claudeRecentProjectsKey, string(b))
	}
	return out
}
