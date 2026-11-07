package claudecfg

// SkillState 是 skillOverrides 的四种取值。
type SkillState string

const (
	SkillOn                SkillState = "on"
	SkillNameOnly          SkillState = "name-only"
	SkillUserInvocableOnly SkillState = "user-invocable-only"
	SkillOff               SkillState = "off"
)

// SkillSource 是 Skill 的来源。
type SkillSource string

const (
	SkillSourceBuiltIn SkillSource = "built-in"
	SkillSourceUser    SkillSource = "user"
	SkillSourceProject SkillSource = "project"
	SkillSourcePlugin  SkillSource = "plugin"
	SkillSourceUnknown SkillSource = "unknown"
)

// Skill 是一个可配置的 Skill 条目（由 bridge 从文件系统与插件清单组装）。
type Skill struct {
	Name        string
	Description string
	PluginID    string
	Source      SkillSource
	// LockedBy 是作者声明造成的上限来源，例如 "author"。
	LockedBy string
}

// ResolveSkill 计算 Skill 的最终状态与锁定原因。
// locked 非空表示 UI 不可写；空字符串表示可由用户 override。
func ResolveSkill(skill Skill, layers []Layer, pluginEnabled map[string]bool) (SkillState, string) {
	if reservedSkillNames[skill.Name] {
		return SkillOff, "reserved-name"
	}

	if skill.Source == SkillSourcePlugin {
		enabled := true
		if pluginEnabled != nil {
			if v, ok := pluginEnabled[skill.PluginID]; ok {
				enabled = v
			}
		}
		if !enabled {
			return SkillOff, "plugin"
		}
		// 插件来源的 skillOverrides 不生效，由插件开关统一管理。
		return SkillOn, "plugin"
	}

	state := SkillOn
	locked := ""
	for _, kind := range []ScopeKind{ScopeManaged, ScopeLocal, ScopeProject, ScopeUser} {
		found := false
		for _, layer := range layers {
			if layer.Kind != kind {
				continue
			}
			raw, ok := skillOverrides(layer.Doc)[skill.Name]
			if !ok {
				continue
			}
			s, ok := raw.(string)
			if !ok || !validSkillStates[s] {
				continue
			}
			state = SkillState(s)
			if kind == ScopeManaged {
				locked = "managed"
			}
			found = true
			break
		}
		if found {
			break
		}
	}

	if skill.LockedBy != "" {
		if state == SkillOn || state == SkillNameOnly {
			state = SkillUserInvocableOnly
		}
		locked = skill.LockedBy
	}
	return state, locked
}

// CollectSkillOverrides 返回跨层合并后的全部合法 override，保留已不存在的 Skill 名。
func CollectSkillOverrides(layers []Layer) map[string]SkillState {
	out := map[string]SkillState{}
	// 低 → 高写入，高层覆盖低层。
	for _, layer := range layers {
		for name, raw := range skillOverrides(layer.Doc) {
			s, ok := raw.(string)
			if !ok || !validSkillStates[s] {
				continue
			}
			out[name] = SkillState(s)
		}
	}
	return out
}

func skillOverrides(doc map[string]any) map[string]any {
	v, ok := doc["skillOverrides"]
	if !ok {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}
