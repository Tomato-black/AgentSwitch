package router

import (
	"testing"
	"time"
)

func cand(channelID string, priority int, cooling bool) Candidate {
	c := Candidate{TargetID: "t-" + channelID, LogicalModel: "m-" + channelID,
		ChannelID: channelID, UpstreamModel: "u-" + channelID, Priority: priority}
	if cooling {
		c.CoolingUntil = time.Now().Add(time.Minute)
		c.CoolingReason = "rate_limited"
	}
	return c
}

func TestPriorityRouterOrderingAndFiltering(t *testing.T) {
	cands := []Candidate{cand("ch3", 10, false), cand("ch1", 10, false),
		cand("ch2", 5, false), cand("chc", 10, true)}
	d := PriorityRouter{}.Resolve("m", cands)
	if len(d.Ordered) != 3 || len(d.Filtered) != 1 {
		t.Fatalf("ordered %d filtered %d", len(d.Ordered), len(d.Filtered))
	}
	// 期望序：ch2(Priority 5) → ch1(Priority 10) → ch3(Priority 10)
	want := []string{"ch2", "ch1", "ch3"}
	for i, id := range want {
		if d.Ordered[i].ChannelID != id {
			t.Fatalf("pos %d = %s want %s", i, d.Ordered[i].ChannelID, id)
		}
	}
	if d.Filtered[0].Reason != "cooling:rate_limited" || d.Filtered[0].Candidate.ChannelID != "chc" {
		t.Fatalf("filtered: %+v", d.Filtered[0])
	}
	if d.Model != "m" {
		t.Fatalf("model %q", d.Model)
	}
}

func TestPriorityRouterExpiredCoolingPasses(t *testing.T) {
	c := cand("ch", 10, false)
	c.CoolingUntil = time.Now().Add(-time.Second) // 已过期
	d := PriorityRouter{}.Resolve("m", []Candidate{c})
	if len(d.Ordered) != 1 || len(d.Filtered) != 0 {
		t.Fatalf("expired cooling must pass: %+v", d)
	}
}

// 同 Priority 同 ChannelID（如同渠道多目标）保持输入序——稳定排序。
func TestPriorityRouterStableForSameChannel(t *testing.T) {
	a, b := cand("ch", 10, false), cand("ch", 10, false)
	a.TargetID, b.TargetID = "t-a", "t-b"
	d := PriorityRouter{}.Resolve("m", []Candidate{a, b})
	if len(d.Ordered) != 2 || d.Ordered[0].TargetID != "t-a" || d.Ordered[1].TargetID != "t-b" {
		t.Fatalf("stable order broken: %+v", d.Ordered)
	}
}
