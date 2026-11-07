package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/claudecfg"
)

// ClaudeConfigs 是 Claude Code 分层 settings 的桥接服务。
type ClaudeConfigs struct {
	A    *app.App
	Home string
}

// ReadLayerRequest 选择要读取的层；Project 仅项目层使用，LocalPath 可在
// 候选路径中显式指定项目本地文件。
type ReadLayerRequest struct {
	Kind      claudecfg.ScopeKind
	Project   string
	Sub       string
	LocalPath string
}

// LayerView 是一层的只读视图。
type LayerView struct {
	Kind       claudecfg.ScopeKind
	File       string
	Display    string
	Project    string
	Note       string
	Exists     bool
	Doc        map[string]any
	RawJSON    string
	Hash       string
	ParseError string
	Violations []claudecfg.Violation
	Managed    claudecfg.ManagedSet
	Candidates []string
}

// ClaudeConfigOverview 是配置中心页头所需的汇总。
type ClaudeConfigOverview struct {
	Warning string
	Managed claudecfg.ManagedSet
}

func (s ClaudeConfigs) home() (string, error) {
	if s.Home != "" {
		return s.Home, nil
	}
	return os.UserHomeDir()
}

// claudeEnv 构造 Claude 层路径解析环境。GOOS 必须透传 runtime.GOOS——托管层
// 路径按平台分支（I2 回归：此前漏传导致非 darwin 平台读错托管文件）。
func claudeEnv(home string) claudecfg.Env {
	return claudecfg.Env{Home: home, GOOS: runtime.GOOS}
}

// ReadClaudeConfigLayer 读取并解析一层 settings。文件不存在返回空文档；
// JSON 损坏返回带 ParseError 的视图，不返回 error（页面进入只读修复态）。
func (s ClaudeConfigs) ReadClaudeConfigLayer(req ReadLayerRequest) (LayerView, error) {
	home, err := s.home()
	if err != nil {
		return LayerView{}, err
	}
	resolved, err := claudecfg.Resolve(req.Kind, req.Project, claudeEnv(home))
	if err != nil {
		return LayerView{}, err
	}
	if req.Kind == claudecfg.ScopeLocal && req.LocalPath != "" {
		for _, candidate := range resolved.Candidates {
			if filepath.Clean(candidate) == filepath.Clean(req.LocalPath) {
				resolved.File = candidate
				resolved.Display = candidate
				break
			}
		}
	}
	view := LayerView{
		Kind:       resolved.Kind,
		File:       resolved.File,
		Display:    resolved.Display,
		Project:    resolved.Project,
		Note:       resolved.Note,
		Exists:     resolved.Exists,
		Candidates: resolved.Candidates,
		Doc:        map[string]any{},
		RawJSON:    "{}",
	}
	managed, err := s.ManagedSetForClaude()
	if err != nil {
		return LayerView{}, err
	}
	view.Managed = managed

	b, err := os.ReadFile(resolved.File)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return view, nil
		}
		return LayerView{}, err
	}
	view.RawJSON = string(b)
	doc, err := claudecfg.ParseDoc(b)
	if err != nil {
		view.ParseError = err.Error()
		return view, nil
	}
	view.Doc = doc.Raw
	view.Hash = doc.Hash
	view.Violations = claudecfg.Validate(doc.Raw, req.Kind, doc.Raw, managed)
	return view, nil
}

// ManagedSetForClaude 汇总所有 Claude 接入文档的顶层键与 env，计算受管集合。
func (s ClaudeConfigs) ManagedSetForClaude() (claudecfg.ManagedSet, error) {
	if s.A == nil {
		return claudecfg.ComputeManagedSet(nil, nil), nil
	}
	configs, err := s.A.Store().AccessConfigs().ListByApp("claude")
	if err != nil {
		return claudecfg.ComputeManagedSet(nil, nil), err
	}
	docs := make([]map[string]any, 0, len(configs))
	env := map[string]string{}
	if raw, err := s.A.Store().Settings().Get("access.global.claude"); err == nil && raw != "" {
		_ = json.Unmarshal([]byte(raw), &env)
	}
	for _, c := range configs {
		var doc map[string]any
		if err := json.Unmarshal([]byte(c.Settings), &doc); err != nil {
			continue
		}
		docs = append(docs, doc)
		if m, ok := doc["env"].(map[string]any); ok {
			for k, v := range m {
				if str, ok := v.(string); ok {
					env[k] = str
				}
			}
		}
	}
	return claudecfg.ComputeManagedSet(docs, env), nil
}

// ClaudeConfigOverview 返回配置中心页头信息。
func (s ClaudeConfigs) ClaudeConfigOverview() (ClaudeConfigOverview, error) {
	managed, err := s.ManagedSetForClaude()
	if err != nil {
		return ClaudeConfigOverview{}, err
	}
	out := ClaudeConfigOverview{Managed: managed}
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		out.Warning = fmt.Sprintf("检测到 CLAUDE_CONFIG_DIR=%s；本版本仍固定读写 ~/.claude，不跟随该变量。", v)
	}
	return out, nil
}

// MergeClaudeConfig 返回当前用户/项目/托管层的合并结果（只读预览）。
func (s ClaudeConfigs) MergeClaudeConfig(project string) (map[string]any, error) {
	merged, _, err := s.MergeClaudeConfigView(project)
	return merged, err
}

// MergeClaudeConfigView 返回合并文档与按叶子路径索引的来源信息，供设置区
// 表单显示「继承自下层」及合并预览显示来源徽章。
func (s ClaudeConfigs) MergeClaudeConfigView(project string) (map[string]any, map[string]claudecfg.MergedEntry, error) {
	home, err := s.home()
	if err != nil {
		return nil, nil, err
	}
	merged, entries := claudecfg.Merge(s.claudeLayers(home, project))
	return merged, entries, nil
}
