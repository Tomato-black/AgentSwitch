// overview_test.go 占位：旧概览四卡页测试依赖的 overviewView/poolCard/
// AddKeys 等已退役。保留 recentStats 纯函数测试（Task 15 迁移片段样式时
// 仍引用）；本文件随 Task 16 与 overview.go 一并删除。
package desktop

import (
	"testing"

	"AgentSwitch/internal/telemetry"
)

// TestRecentStats 覆盖「近 N 条 · M 失败」徽章的统计口径：failed =
// Status==0（未收到响应即中断）或 >=400；2xx/3xx 不算失败。
func TestRecentStats(t *testing.T) {
	recs := []telemetry.RequestRecord{{Status: 200}, {Status: 429}, {Status: 0}, {Status: 502}}
	total, failed := recentStats(recs)
	if total != 4 || failed != 3 {
		t.Fatalf("%d %d", total, failed)
	}
}
