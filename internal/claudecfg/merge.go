package claudecfg

import (
	"reflect"
	"sort"
	"strings"
)

// Layer 是一层已解析的 settings 文档。输入顺序必须低 → 高
// （user → project → local → managed）。
type Layer struct {
	Kind ScopeKind
	File string
	Doc  map[string]any
}

// Contribution 是某层对某个路径的贡献。
type Contribution struct {
	Layer     ScopeKind
	File      string
	Value     any
	Effective bool
}

// MergedEntry 是一个叶子路径的合并结果。
type MergedEntry struct {
	Path          string
	Value         any
	Kind          MergeKind
	Source        ScopeKind
	Contributions []Contribution
}

// Merge 返回跨层合并后的文档与按叶子路径索引的合并视图。
func Merge(layers []Layer) (map[string]any, map[string]MergedEntry) {
	byLayer := make([]map[string]any, 0, len(layers))
	pathSet := map[string]bool{}
	for _, layer := range layers {
		flat := flattenLayer(layer.Doc)
		byLayer = append(byLayer, flat)
		for p := range flat {
			pathSet[p] = true
		}
	}
	paths := make([]string, 0, len(pathSet))
	for p := range pathSet {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	merged := map[string]any{}
	entries := make(map[string]MergedEntry, len(paths))
	for _, path := range paths {
		contribs := make([]Contribution, 0, len(layers))
		for i, flat := range byLayer {
			v, ok := flat[path]
			if !ok {
				continue
			}
			contribs = append(contribs, Contribution{Layer: layers[i].Kind, File: layers[i].File, Value: v})
		}
		kind := kindFor(path)
		value, source, out := mergePath(path, kind, contribs)
		Set(merged, path, value)
		entries[path] = MergedEntry{Path: path, Value: value, Kind: kind, Source: source, Contributions: out}
	}
	return merged, entries
}

func flattenLayer(doc map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range doc {
		switch k {
		case "env", "enabledPlugins", "skillOverrides":
			m, ok := v.(map[string]any)
			if !ok {
				out[k] = v
				continue
			}
			for key, val := range m {
				out[k+"."+key] = val
			}
		case "hooks", "permissions":
			m, ok := v.(map[string]any)
			if !ok {
				out[k] = v
				continue
			}
			for key, val := range m {
				out[k+"."+key] = val
			}
		default:
			out[k] = v
		}
	}
	return out
}

func kindFor(path string) MergeKind {
	if spec, ok := Lookup(path); ok {
		return spec.Merge
	}
	switch {
	case strings.HasPrefix(path, "env."):
		return MergeEnv
	case strings.HasPrefix(path, "enabledPlugins."):
		return MergePlugins
	case strings.HasPrefix(path, "skillOverrides."):
		return MergeSkill
	case strings.HasPrefix(path, "hooks."):
		return MergeHooks
	case strings.HasPrefix(path, "permissions."):
		return MergeOverride
	default:
		return MergeOverride
	}
}

func mergePath(path string, kind MergeKind, cs []Contribution) (any, ScopeKind, []Contribution) {
	if len(cs) == 0 {
		return nil, "", nil
	}
	switch kind {
	case MergeUnion:
		return mergeUnion(cs)
	case MergeHooks:
		return mergeHooks(cs)
	case MergeOr:
		return mergeOr(cs)
	default:
		if path == "permissions.defaultMode" {
			return mergeDefaultMode(cs)
		}
		last := len(cs) - 1
		for i := range cs {
			cs[i].Effective = i == last
		}
		return cs[last].Value, cs[last].Layer, cs
	}
}

func mergeUnion(cs []Contribution) (any, ScopeKind, []Contribution) {
	var out []any
	source := cs[0].Layer
	for i := range cs {
		items := toAnySlice(cs[i].Value)
		if len(items) == 0 {
			continue
		}
		source = cs[i].Layer
		cs[i].Effective = true
		for _, item := range items {
			if !containsValue(out, item) {
				out = append(out, item)
			}
		}
	}
	if out == nil {
		out = []any{}
	}
	return out, source, cs
}

func mergeHooks(cs []Contribution) (any, ScopeKind, []Contribution) {
	var out []any
	for i := range cs {
		items := toAnySlice(cs[i].Value)
		cs[i].Effective = len(items) > 0
		out = append(out, items...)
	}
	if out == nil {
		out = []any{}
	}
	return out, cs[len(cs)-1].Layer, cs
}

func mergeOr(cs []Contribution) (any, ScopeKind, []Contribution) {
	source := cs[len(cs)-1].Layer
	anyTrue := false
	for i := range cs {
		truth, _ := cs[i].Value.(bool)
		if truth {
			anyTrue = true
			source = cs[i].Layer
		}
		cs[i].Effective = truth
	}
	if !anyTrue {
		for i := range cs {
			cs[i].Effective = false
		}
	}
	return anyTrue, source, cs
}

func mergeDefaultMode(cs []Contribution) (any, ScopeKind, []Contribution) {
	winner := len(cs) - 1
	for i := len(cs) - 1; i >= 0; i-- {
		mode, _ := cs[i].Value.(string)
		projectLevel := cs[i].Layer == ScopeProject || cs[i].Layer == ScopeLocal
		if projectLevel && (mode == "auto" || mode == "bypassPermissions") {
			continue
		}
		winner = i
		break
	}
	for i := range cs {
		cs[i].Effective = i == winner
	}
	return cs[winner].Value, cs[winner].Layer, cs
}

func toAnySlice(v any) []any {
	switch x := v.(type) {
	case []any:
		return x
	case []string:
		out := make([]any, len(x))
		for i, s := range x {
			out[i] = s
		}
		return out
	default:
		return nil
	}
}

func containsValue(list []any, v any) bool {
	for _, item := range list {
		if reflect.DeepEqual(item, v) {
			return true
		}
	}
	return false
}
