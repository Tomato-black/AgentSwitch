package desktop

import (
	"testing"

	"AgentSwitch/internal/bridge"
)

// TestLogicalCardShowsTargetChain 覆盖目标链摘要纯函数：两目标按序拼成
// `渠道A·model → 渠道B·model`（spec §6.2 摘要格式）。渲染薄壳不测字符串细节。
func TestLogicalCardShowsTargetChain(t *testing.T) {
	m := bridge.ModelView{Name: "my-code", Targets: []bridge.TargetView{
		{ChannelName: "渠道A", ModelName: "m1", Priority: 0},
		{ChannelName: "渠道B", ModelName: "m2", Priority: 1},
	}}
	got := formatTargetChain(m)
	want := "渠道A·m1 → 渠道B·m2"
	if got != want {
		t.Fatalf("formatTargetChain() = %q, want %q", got, want)
	}
}

// TestFormatTargetChainEmpty 覆盖空链：无目标 → `（无目标）`。
func TestFormatTargetChainEmpty(t *testing.T) {
	if got := formatTargetChain(bridge.ModelView{Name: "empty"}); got != "（无目标）" {
		t.Fatalf("formatTargetChain() = %q, want （无目标）", got)
	}
}
