package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"AgentSwitch/internal/claudecfg"
)

// SkillView 是配置中心 Skills 卡片的一行。
type SkillView struct {
	Name          string
	Description   string
	PluginID      string
	Source        claudecfg.SkillSource
	Effective     claudecfg.SkillState
	LockedBy      string
	TokenEstimate int
}

type installedPlugin struct {
	InstallPath string `json:"installPath"`
}

type installedPluginsFile struct {
	Plugins map[string][]installedPlugin `json:"plugins"`
}

var builtinSkillCatalog = []SkillView{
	{Name: "claude-api", Description: "Claude API 参考与模型迁移", Source: claudecfg.SkillSourceBuiltIn, Effective: claudecfg.SkillOn},
	{Name: "security-review", Description: "安全审查", Source: claudecfg.SkillSourceBuiltIn, Effective: claudecfg.SkillOn},
	{Name: "dataviz", Description: "图表与可视化规范", Source: claudecfg.SkillSourceBuiltIn, Effective: claudecfg.SkillOn},
	{Name: "code-review", Description: "代码审查", Source: claudecfg.SkillSourceBuiltIn, Effective: claudecfg.SkillOn},
	{Name: "simplify", Description: "简化与重构", Source: claudecfg.SkillSourceBuiltIn, Effective: claudecfg.SkillOn},
	{Name: "loop", Description: "定时循环任务", Source: claudecfg.SkillSourceBuiltIn, Effective: claudecfg.SkillOn},
	{Name: "run", Description: "启动并驱动项目应用", Source: claudecfg.SkillSourceBuiltIn, Effective: claudecfg.SkillOn},
	{Name: "init", Description: "初始化 CLAUDE.md", Source: claudecfg.SkillSourceBuiltIn, Effective: claudecfg.SkillOn},
}

// ListClaudeSkills 扫描内置、用户、项目与插件来源的 Skills，并计算有效状态。
func (s ClaudeConfigs) ListClaudeSkills(project string) ([]SkillView, error) {
	home, err := s.home()
	if err != nil {
		return nil, err
	}
	layers := s.claudeLayers(home, project)
	pluginEnabled := mergedPluginEnabled(layers)

	skills := append([]SkillView(nil), builtinSkillCatalog...)
	skills = append(skills, scanSkillDir(filepath.Join(home, ".claude", "skills"), claudecfg.SkillSourceUser)...)
	if project != "" {
		skills = append(skills, scanSkillDir(filepath.Join(project, ".claude", "skills"), claudecfg.SkillSourceProject)...)
	}
	skills = append(skills, scanPluginSkills(home)...)

	seen := map[string]bool{}
	for i := range skills {
		seen[skills[i].Name] = true
		state, locked := claudecfg.ResolveSkill(claudecfg.Skill{
			Name: skills[i].Name, Source: skills[i].Source,
			PluginID: skills[i].PluginID, LockedBy: skills[i].LockedBy,
		}, layers, pluginEnabled)
		skills[i].Effective = state
		skills[i].LockedBy = locked
	}
	for name, state := range claudecfg.CollectSkillOverrides(layers) {
		if seen[name] {
			continue
		}
		skills = append(skills, SkillView{
			Name: name, Description: "未在本地 Skill 清单中找到；可能来自已卸载插件或历史配置",
			Source: claudecfg.SkillSourceUnknown, Effective: state,
		})
	}
	sort.SliceStable(skills, func(i, j int) bool {
		if skills[i].Source != skills[j].Source {
			return skillSourceRank(skills[i].Source) < skillSourceRank(skills[j].Source)
		}
		return skills[i].Name < skills[j].Name
	})
	return skills, nil
}

func (s ClaudeConfigs) claudeLayers(home, project string) []claudecfg.Layer {
	paths := []struct {
		kind claudecfg.ScopeKind
		file string
	}{
		{claudecfg.ScopeUser, filepath.Join(home, ".claude", "settings.json")},
	}
	if project != "" {
		paths = append(paths,
			struct {
				kind claudecfg.ScopeKind
				file string
			}{claudecfg.ScopeProject, filepath.Join(project, ".claude", "settings.json")},
			struct {
				kind claudecfg.ScopeKind
				file string
			}{claudecfg.ScopeLocal, filepath.Join(project, ".claude", "settings.local.json")},
		)
	}
	paths = append(paths, struct {
		kind claudecfg.ScopeKind
		file string
	}{claudecfg.ScopeManaged, managedSettingsPath()})
	var out []claudecfg.Layer
	for _, p := range paths {
		b, err := os.ReadFile(p.file)
		if err != nil {
			continue
		}
		doc, err := claudecfg.ParseDoc(b)
		if err != nil {
			continue
		}
		out = append(out, claudecfg.Layer{Kind: p.kind, File: p.file, Doc: doc.Raw})
	}
	return out
}

func managedSettingsPath() string {
	return "/Library/Application Support/ClaudeCode/managed-settings.json"
}

func mergedPluginEnabled(layers []claudecfg.Layer) map[string]bool {
	merged, _ := claudecfg.Merge(layers)
	out := map[string]bool{}
	if m, ok := merged["enabledPlugins"].(map[string]any); ok {
		for id, v := range m {
			if b, ok := v.(bool); ok {
				out[id] = b
			}
		}
	}
	return out
}

func scanSkillDir(dir string, source claudecfg.SkillSource) []SkillView {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []SkillView
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name(), "SKILL.md")
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		name, desc, locked := parseSkillFile(string(b), e.Name())
		out = append(out, SkillView{
			Name: name, Description: desc, Source: source, Effective: claudecfg.SkillOn, LockedBy: locked,
		})
	}
	return out
}

func scanPluginSkills(home string) []SkillView {
	b, err := os.ReadFile(filepath.Join(home, ".claude", "plugins", "installed_plugins.json"))
	if err != nil {
		return nil
	}
	var file installedPluginsFile
	if err := json.Unmarshal(b, &file); err != nil {
		return nil
	}
	var out []SkillView
	for pluginID, installs := range file.Plugins {
		if len(installs) == 0 || installs[0].InstallPath == "" {
			continue
		}
		pluginName := pluginID
		if i := strings.Index(pluginName, "@"); i >= 0 {
			pluginName = pluginName[:i]
		}
		for _, skill := range scanSkillDir(filepath.Join(installs[0].InstallPath, "skills"), claudecfg.SkillSourcePlugin) {
			skill.Name = pluginName + ":" + skill.Name
			skill.PluginID = pluginID
			out = append(out, skill)
		}
	}
	return out
}

func parseSkillFile(raw, fallbackName string) (name, desc, lockedBy string) {
	name = fallbackName
	lines := strings.Split(raw, "\n")
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		end := -1
		for i := 1; i < len(lines); i++ {
			if strings.TrimSpace(lines[i]) == "---" {
				end = i
				break
			}
		}
		if end > 0 {
			for _, line := range lines[1:end] {
				key, val, ok := strings.Cut(line, ":")
				if !ok {
					continue
				}
				switch strings.TrimSpace(key) {
				case "name":
					if v := strings.TrimSpace(val); v != "" {
						name = v
					}
				case "description":
					desc = strings.TrimSpace(val)
				case "disable-model-invocation":
					if strings.EqualFold(strings.TrimSpace(val), "true") {
						lockedBy = "author"
					}
				}
			}
		}
	}
	if desc == "" {
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && line != "---" && !strings.Contains(line, ":") {
				desc = line
				break
			}
		}
	}
	return name, desc, lockedBy
}

func skillSourceRank(s claudecfg.SkillSource) int {
	switch s {
	case claudecfg.SkillSourceBuiltIn:
		return 0
	case claudecfg.SkillSourceUser:
		return 1
	case claudecfg.SkillSourceProject:
		return 2
	case claudecfg.SkillSourcePlugin:
		return 3
	default:
		return 4
	}
}
