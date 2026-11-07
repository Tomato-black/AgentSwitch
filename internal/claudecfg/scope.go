package claudecfg

import (
	"reflect"
	"sort"
	"strings"
)

// Violation 是一条 scope 或受管键校验结果。
// Changed 区分「本次修改引入」与「历史遗留」：前者阻断保存，后者只警告。
type Violation struct {
	Path     string
	Reason   string
	Severity string // error / warn
	Present  bool
	Changed  bool
}

var reservedSkillNames = map[string]bool{
	"__proto__":   true,
	"constructor": true,
	"prototype":   true,
}

var validSkillStates = map[string]bool{
	"on":                  true,
	"name-only":           true,
	"user-invocable-only": true,
	"off":                 true,
}

// Validate 校验当前层文档：受管键、scope 限制、枚举与历史遗留。
func Validate(doc map[string]any, scope ScopeKind, base map[string]any, managed ManagedSet) []Violation {
	var out []Violation
	seen := map[string]bool{}

	add := func(path, reason, severity string, changed bool) {
		if !changed {
			severity = "warn"
		}
		key := path + "\x00" + reason
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, Violation{
			Path:     path,
			Reason:   reason,
			Severity: severity,
			Present:  true,
			Changed:  changed,
		})
	}

	for _, path := range leafPaths(doc) {
		value, _ := Get(doc, path)
		baseValue, hasBase := Get(base, path)
		changed := !hasBase || !reflect.DeepEqual(value, baseValue)

		// 强受管 env 键。
		for _, k := range managed.StrongEnv {
			if path == "env."+k {
				add(path, "managed", "error", changed)
			}
		}
		// 强受管顶层键：报变化的叶子路径，便于 UI 定位。
		for _, k := range managed.StrongTopLevel {
			if path == k || strings.HasPrefix(path, k+".") {
				add(path, "managed", "error", changed)
			}
		}
		// 弱受管 env 键：可编辑但警告。
		for _, p := range managed.WeakEnv {
			if path == p {
				add(path, "weak-managed", "warn", changed)
			}
		}
		// 仅托管可写。
		if item, ok := matchAny(path, ManagedOnly); ok {
			add(item, "managed-only", "error", changed)
		}
		// 仅用户级可写（项目共享与项目本地都不行）。
		if scope != ScopeUser && scope != ScopeManaged {
			if item, ok := matchAny(path, UserOnly); ok {
				add(item, "user-only", "error", changed)
			}
		}
		// 不能写入项目共享。
		if scope == ScopeProject {
			if item, ok := matchAny(path, NoProjectShared); ok {
				add(item, "not-in-project-shared", "error", changed)
			}
		}
		// 项目级的 auto / bypassPermissions 不生效。
		if scope == ScopeProject && path == "permissions.defaultMode" {
			if s, ok := value.(string); ok && (s == "auto" || s == "bypassPermissions") {
				add(path, "ineffective-at-project", "warn", changed)
			}
		}
		// skillOverrides 枚举与保留名。
		if strings.HasPrefix(path, "skillOverrides.") {
			name := strings.TrimPrefix(path, "skillOverrides.")
			if reservedSkillNames[name] {
				add(path, "reserved-name", "error", changed)
			} else if s, ok := value.(string); !ok || !validSkillStates[s] {
				add(path, "invalid-value", "error", changed)
			}
		}
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Path == out[j].Path {
			return out[i].Reason < out[j].Reason
		}
		return out[i].Path < out[j].Path
	})
	return out
}

func matchAny(path string, list []string) (string, bool) {
	for _, item := range list {
		if path == item || strings.HasPrefix(path, item+".") {
			return item, true
		}
	}
	return "", false
}

// leafPaths 返回文档的全部叶子路径；空对象与数组按叶子处理。
func leafPaths(doc map[string]any) []string {
	var out []string
	var walk func(prefix string, m map[string]any)
	walk = func(prefix string, m map[string]any) {
		for k, v := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			if child, ok := v.(map[string]any); ok && len(child) > 0 {
				walk(path, child)
				continue
			}
			out = append(out, path)
		}
	}
	walk("", doc)
	sort.Strings(out)
	return out
}
