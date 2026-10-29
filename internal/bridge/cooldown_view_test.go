package bridge

import (
	"testing"
	"time"

	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/storage"
)

// TestCooldownViewResolvesChannelName 覆盖 P1-4：冷却快照带渠道名、剩余秒数
// 与错误原因，供 UI 显示倒计时。
func TestCooldownViewResolvesChannelName(t *testing.T) {
	a := tempApp(t)
	ch, err := a.Store().Channels().Save(storage.Channel{
		Name: "主渠道", Protocol: "openai", BaseURL: "https://api.example.com", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	a.Cooldowns().MarkClassified(ch.ID, provider.ClassRateLimit, 30, time.Now())
	got := (Gateway{A: a}).Cooldowns()
	if len(got) != 1 {
		t.Fatalf("cooldowns = %+v", got)
	}
	if got[0].ChannelID != ch.ID || got[0].ChannelName != "主渠道" {
		t.Fatalf("channel = %+v", got[0])
	}
	if got[0].Reason != string(provider.ClassRateLimit) {
		t.Fatalf("reason = %q", got[0].Reason)
	}
	if got[0].RemainingSeconds < 1 || got[0].RemainingSeconds > 30 {
		t.Fatalf("remaining = %d", got[0].RemainingSeconds)
	}
}
