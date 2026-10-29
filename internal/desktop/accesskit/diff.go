package accesskit

import (
	"encoding/json"
	"sort"
	"strings"
)

// DocDiffLine 是配置文档的一处键级变化。
type DocDiffLine struct {
	Op   string // "+" 新增 | "-" 删除 | "~" 修改
	Path string
	Old  string
	New  string
}

// DocDiff 对比两份 JSON 文档的叶子键，返回按键名排序的变化。
// 敏感路径（TOKEN/KEY/SECRET/AUTH/PASSWORD）的值一律替换为 ***。
func DocDiff(oldJSON, newJSON string) []DocDiffLine {
	oldMap := flattenJSON(oldJSON)
	newMap := flattenJSON(newJSON)
	keys := make([]string, 0, len(oldMap)+len(newMap))
	seen := map[string]bool{}
	for k := range oldMap {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	for k := range newMap {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []DocDiffLine
	for _, k := range keys {
		ov, okO := oldMap[k]
		nv, okN := newMap[k]
		switch {
		case !okO:
			out = append(out, DocDiffLine{Op: "+", Path: k, New: maskDiffValue(k, nv)})
		case !okN:
			out = append(out, DocDiffLine{Op: "-", Path: k, Old: maskDiffValue(k, ov)})
		case ov != nv:
			out = append(out, DocDiffLine{Op: "~", Path: k,
				Old: maskDiffValue(k, ov), New: maskDiffValue(k, nv)})
		}
	}
	return out
}

func flattenJSON(raw string) map[string]string {
	out := map[string]string{}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return out
	}
	flattenValue("", v, out)
	return out
}

func flattenValue(prefix string, v any, out map[string]string) {
	if m, ok := v.(map[string]any); ok {
		for k, child := range m {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			flattenValue(path, child, out)
		}
		return
	}
	if prefix == "" {
		return
	}
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	out[prefix] = string(b)
}

func maskDiffValue(path, value string) string {
	up := strings.ToUpper(path)
	for _, sensitive := range []string{"TOKEN", "KEY", "SECRET", "AUTH", "PASSWORD"} {
		if strings.Contains(up, sensitive) {
			return "***"
		}
	}
	return value
}
