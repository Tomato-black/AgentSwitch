package storage

import "encoding/json"

// DesktopAliasesSettingsKey 是 Claude Desktop 角色别名表的 settings 键：
// map「Desktop 路由 ID（claude-sonnet-5 等）→ 逻辑模型名」，接入 Apply 时
// 整表覆盖（last-applied-wins），网关 candidates 解析入站名前查询。
const DesktopAliasesSettingsKey = "access.desktop_aliases"

// DesktopAliases 读持久化别名表；缺失/非法/为空返回 nil。
func DesktopAliases(store Store) map[string]string {
	raw, err := store.Settings().Get(DesktopAliasesSettingsKey)
	if err != nil || raw == "" {
		return nil
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil || len(m) == 0 {
		return nil
	}
	return m
}
