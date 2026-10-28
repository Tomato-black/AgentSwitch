package accesskit

import (
	"sort"
	"strings"

	"AgentSwitch/internal/bridge"
)

// TabTitles 返回接入页四个 tab 的标题（字符串快照锚点）：三个受管
// Agent + 连接指南（第三方系统照指南手工接入，AgentSwitch 不写其配置）。
func TabTitles() (claude, codex, desktop, guide string) {
	return "Claude Code", "Codex", "Claude Desktop", "连接指南"
}

// TabTitle 返回 Agent 页签的显示名（未知 app 原样返回）。
func TabTitle(app string) string {
	claude, codex, desktop, guide := TabTitles()
	switch app {
	case "claude":
		return claude
	case "codex":
		return codex
	case "claude_desktop":
		return desktop
	case "guide":
		return guide
	}
	return app
}

// TabKind 把选中态译成软按钮 kind（选中 act 高亮，未选 gray）。
func TabKind(selected bool) string {
	if selected {
		return "act"
	}
	return "gray"
}

// RowBadge 返回配置行「使用中」徽标文本（非 current 为空串）。
func RowBadge(isCurrent bool) string {
	if isCurrent {
		return "使用中"
	}
	return ""
}

// OverridesFromRows 把键值行转成 overrides map（空键/空值都剔除
// ——半填行是误操作，清空请删行；同键后者胜——与合成层 overrides 优先
// 语义一致）。用于全局配置卡。
func OverridesFromRows(rows [][2]string) map[string]string {
	out := map[string]string{}
	for _, r := range rows {
		k := strings.TrimSpace(r[0])
		v := strings.TrimSpace(r[1])
		if k == "" || v == "" {
			continue
		}
		out[k] = r[1]
	}
	return out
}

// ModelLabels 返回 enabled 逻辑模型名列表（编辑器 Select 数据源）。
func ModelLabels(models []bridge.ModelView) []string {
	var out []string
	for _, m := range models {
		if m.Enabled {
			out = append(out, m.Name)
		}
	}
	return out
}

// ModelIDByLabel 按名映射回逻辑模型 ID（未命中返回空串；
// disabled 项也参与命中——编辑旧配置时回显不应丢引用）。
func ModelIDByLabel(models []bridge.ModelView, label string) string {
	for _, m := range models {
		if m.Name == label {
			return m.ID
		}
	}
	return ""
}

// GlobalRows 把全局 env map 转成有序编辑行（键字典序，UI 稳定渲染）。
func GlobalRows(env map[string]string) [][2]string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	rows := make([][2]string, 0, len(keys))
	for _, k := range keys {
		rows = append(rows, [2]string{k, env[k]})
	}
	return rows
}

// GatewayGate 返回网关是否可用（应用/启动类操作的前提）与原因。网关运行
// 状态取实际服务状态（Status().State），不是按钮状态（架构不变量）。
func GatewayGate(gw bridge.Gateway) (bool, string) {
	st := gw.Status()
	if st.State != "running" && st.State != "degraded" {
		return false, "网关未运行——请先在侧栏底条启动网关"
	}
	return true, ""
}
