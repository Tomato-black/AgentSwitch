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

// managedWholeReplaceKeys 在 managed 层内合并时整值替换（后写覆盖，不并集/
// 不逐键）：fallbackModel/modelPicker/availableModels/modelSettings（spec
// §6.1.1/§6.2 的 replace 例外）。
var managedWholeReplaceKeys = map[string]bool{
	"fallbackModel":   true,
	"modelPicker":     true,
	"availableModels": true,
	"modelSettings":   true,
}

// managedNamedEntryKeys 是「按名字索引、同名条目整值替换」的对象键：
// extraKnownMarketplaces/managedMcpServers。
var managedNamedEntryKeys = map[string]bool{
	"extraKnownMarketplaces": true,
	"managedMcpServers":      true,
}

// MergeManagedDocs 把 managed-settings.json 与 managed-settings.d/*.json
// （调用方按文件名排序后依序传入）合并成一个托管层文档（spec §6.1.1）：
// 标量后写覆盖；数组并集去重（保留先出现顺序）；对象逐键递归合并；
// managedWholeReplaceKeys 整值替换；extraKnownMarketplaces/managedMcpServers
// 同名条目整值替换。输入文档不会被修改。
func MergeManagedDocs(docs []map[string]any) map[string]any {
	out := map[string]any{}
	for _, doc := range docs {
		if len(doc) == 0 {
			continue
		}
		mergeManagedDoc(out, cloneMap(doc))
	}
	return out
}

func mergeManagedDoc(dst, src map[string]any) {
	for k, v := range src {
		cur, exists := dst[k]
		if !exists {
			dst[k] = v
			continue
		}
		if managedWholeReplaceKeys[k] {
			dst[k] = v
			continue
		}
		if managedNamedEntryKeys[k] {
			if curObj, ok1 := cur.(map[string]any); ok1 {
				if srcObj, ok2 := v.(map[string]any); ok2 {
					for name, entry := range srcObj {
						curObj[name] = entry // 同名条目整值替换
					}
					continue
				}
			}
			dst[k] = v
			continue
		}
		dst[k] = mergeManagedValue(cur, v)
	}
}

// mergeManagedValue 合并同键的两个值：数组并集去重、对象逐键递归、
// 其余（标量或类型变化）后写覆盖。
func mergeManagedValue(cur, next any) any {
	curArr, nextArr := toAnySlice(cur), toAnySlice(next)
	if curArr != nil && nextArr != nil {
		out := append([]any{}, curArr...)
		for _, item := range nextArr {
			if !containsValue(out, item) {
				out = append(out, item)
			}
		}
		return out
	}
	curObj, curIsObj := cur.(map[string]any)
	nextObj, nextIsObj := next.(map[string]any)
	if curIsObj && nextIsObj {
		mergeManagedDoc(curObj, nextObj)
		return curObj
	}
	return next
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
