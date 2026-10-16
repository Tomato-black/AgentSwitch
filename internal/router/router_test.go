package router

import (
	"testing"
	"time"
)

func cand(id string, cp, kp int, cooling bool) Candidate {
	c := Candidate{ChannelID: "c" + id, KeyID: id, UpstreamModel: "u-" + id,
		ChannelPriority: cp, KeyPriority: kp}
	if cooling {
		c.CoolingUntil = time.Now().Add(time.Minute)
		c.CoolingReason = "rate_limited"
	}
	return c
}

func TestPriorityRouterOrderingAndFiltering(t *testing.T) {
	cands := []Candidate{cand("k3", 10, 20, false), cand("k1", 10, 10, false),
		cand("k2", 5, 99, false), cand("kc", 10, 10, true)}
	d := PriorityRouter{}.Resolve("m", cands)
	if len(d.Ordered) != 3 || len(d.Filtered) != 1 {
		t.Fatalf("ordered %d filtered %d", len(d.Ordered), len(d.Filtered))
	}
	// 期望序：k2(渠道5) → k1(渠道10,key10) → k3(渠道10,key20)
	want := []string{"k2", "k1", "k3"}
	for i, id := range want {
		if d.Ordered[i].KeyID != id {
			t.Fatalf("pos %d = %s want %s", i, d.Ordered[i].KeyID, id)
		}
	}
	if d.Filtered[0].Reason != "cooling:rate_limited" || d.Filtered[0].Candidate.KeyID != "kc" {
		t.Fatalf("filtered: %+v", d.Filtered[0])
	}
	if d.Model != "m" {
		t.Fatalf("model %q", d.Model)
	}
}

func TestPriorityRouterExpiredCoolingPasses(t *testing.T) {
	c := cand("k", 10, 10, false)
	c.CoolingUntil = time.Now().Add(-time.Second) // 已过期
	d := PriorityRouter{}.Resolve("m", []Candidate{c})
	if len(d.Ordered) != 1 || len(d.Filtered) != 0 {
		t.Fatalf("expired cooling must pass: %+v", d)
	}
}
