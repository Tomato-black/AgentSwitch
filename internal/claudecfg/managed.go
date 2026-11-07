package claudecfg

import "sort"

// ManagedSet 是接入页会覆盖的键集合：StrongEnv 强只读、StrongTopLevel 强只读、
// WeakEnv 可编辑但保存时警告。
type ManagedSet struct {
	StrongEnv      []string
	StrongTopLevel []string
	WeakEnv        []string
	Source         map[string]string
}

// ComputeManagedSet 根据全部 Claude 接入文档与当前合成 env 计算受管集合。
func ComputeManagedSet(configs []map[string]any, composedEnv map[string]string) ManagedSet {
	strongEnv := append([]string(nil), AccessManagedEnv...)
	strongSet := make(map[string]bool, len(strongEnv))
	for _, k := range strongEnv {
		strongSet[k] = true
	}

	topSet := map[string]bool{}
	for _, cfg := range configs {
		for k := range cfg {
			if k == "env" {
				continue
			}
			topSet[k] = true
		}
	}
	top := make([]string, 0, len(topSet))
	for k := range topSet {
		top = append(top, k)
	}
	sort.Strings(top)

	weak := make([]string, 0, len(composedEnv))
	for k := range composedEnv {
		if strongSet[k] {
			continue
		}
		weak = append(weak, "env."+k)
	}
	sort.Strings(weak)

	source := make(map[string]string, len(strongEnv)+len(top)+len(weak))
	for _, k := range strongEnv {
		source["env."+k] = "接入"
	}
	for _, k := range top {
		source[k] = "接入配置"
	}
	for _, k := range weak {
		source[k] = "全局配置/接入"
	}

	return ManagedSet{
		StrongEnv:      strongEnv,
		StrongTopLevel: top,
		WeakEnv:        weak,
		Source:         source,
	}
}
