// channels_test.go 占位：旧多密钥渠道页测试依赖的 AddKeys/Keys/PoolStatus 等
// 已随渠道单 Key 重构退役。保留 channelStats 纯函数测试；页面测试由 Task 14
// 大幅改写、死代码由 Task 16 清理。
package desktop

import (
	"testing"

	"AgentSwitch/internal/telemetry"
)

func TestChannelStats(t *testing.T) {
	recs := []telemetry.RequestRecord{{Channel: "main", Status: 200}, {Channel: "main", Status: 500}, {Channel: "other", Status: 200}}
	ok, fail := channelStats(recs, "main")
	if ok != 1 || fail != 1 {
		t.Fatalf("%d %d", ok, fail)
	}
}
