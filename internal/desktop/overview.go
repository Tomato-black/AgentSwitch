// overview.go 占位：旧「概览」四卡页已随 cc-switch 式壳退役（spec §6.1
// 网关状态收进侧栏底条）。snippetBg 已迁入 access.go（Task 15）；本文件
// 只剩 recentStats（Task 16 连同 defaultSnippetPort 一并清理）。
package desktop

import (
	"AgentSwitch/internal/telemetry"
)

// defaultSnippetPort 是配置片段端口的兜底（config.Default().Port）；正常
// 路径取 bridge.Status.Port（Gateway.Status 从 config.Load 填充）。
const defaultSnippetPort = 17890

// recentStats 统计 hero 徽章「近 N 条 · M 失败」：数据只读自内存遥测环
// （重启清零、不落库不扩 schema，spec §8.7）；失败 = Status==0（未收到
// 响应即中断）或 >=400，2xx/3xx 都不算失败。
func recentStats(recs []telemetry.RequestRecord) (total, failed int) {
	total = len(recs)
	for _, r := range recs {
		if r.Status == 0 || r.Status >= 400 {
			failed++
		}
	}
	return total, failed
}
