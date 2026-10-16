package router

import (
	"sync"
	"testing"
	"time"

	"AgentSwitch/internal/provider"
)

func TestCooldownsClassifiedDurations(t *testing.T) {
	now := time.Now()
	cases := []struct {
		class   provider.ErrorClass
		ra      int
		wantMin time.Duration // 实际时长落在 [wantMin, wantMax]
		wantMax time.Duration
	}{
		{provider.ClassRateLimit, 0, CooldownRateLimitDefault, CooldownRateLimitDefault},
		{provider.ClassRateLimit, 120, 120 * time.Second, 120 * time.Second},
		{provider.ClassRateLimit, 99999, CooldownRateLimitMax, CooldownRateLimitMax}, // 封顶
		{provider.ClassQuota, 0, CooldownQuota, CooldownQuota},
		{provider.ClassServer, 0, CooldownServer, CooldownServer},
		{provider.ClassConnect, 0, CooldownConnect, CooldownConnect},
		{provider.ClassAuth, 0, CooldownAuth, CooldownAuth},
	}
	for i, tc := range cases {
		c := NewCooldowns()
		c.MarkClassified("k", tc.class, tc.ra, now)
		sn := c.Snapshot(now)
		if len(sn) != 1 {
			t.Fatalf("case %d (%s): snapshot %v", i, tc.class, sn)
		}
		d := sn[0].Until.Sub(now)
		if d < tc.wantMin || d > tc.wantMax {
			t.Fatalf("case %d (%s): got %v want [%v,%v]", i, tc.class, d, tc.wantMin, tc.wantMax)
		}
		if sn[0].Reason != string(tc.class) {
			t.Fatalf("case %d reason %q", i, sn[0].Reason)
		}
	}
}

func TestCooldownsParamAndUnknownNotCooled(t *testing.T) {
	c := NewCooldowns()
	now := time.Now()
	c.MarkClassified("k", provider.ClassParam, 0, now)
	c.MarkClassified("k", provider.ClassUnknown, 0, now)
	if sn := c.Snapshot(now); len(sn) != 0 {
		t.Fatalf("param/unknown must not cool: %v", sn)
	}
}

func TestCooldownsTryAcquireAndExpiry(t *testing.T) {
	c := NewCooldowns()
	now := time.Now()
	c.MarkClassified("k", provider.ClassServer, 0, now)
	if _, ok := c.TryAcquire("k", now.Add(time.Second)); ok {
		t.Fatal("within cooldown TryAcquire must fail")
	}
	if _, ok := c.TryAcquire("k", now.Add(CooldownServer)); !ok {
		t.Fatal("after expiry TryAcquire must pass")
	}
	// Review Focus 5：过期条目被快照过滤，不留残留
	if sn := c.Snapshot(now.Add(CooldownServer + time.Second)); len(sn) != 0 {
		t.Fatalf("expired entries must be filtered: %v", sn)
	}
}

func TestCooldownsConcurrent429SingleEntryAndProlong(t *testing.T) {
	c := NewCooldowns()
	now := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.MarkClassified("k", provider.ClassRateLimit, i%7, now.Add(time.Duration(i)*time.Second))
			c.TryAcquire("k", now)
		}(i)
	}
	wg.Wait()
	// 快照时刻取最后一个标记时刻（now+49s）之后、条目过期（now+79s）之前，
	// 否则按契约过期条目会被过滤，断言无从谈起。
	sn := c.Snapshot(now.Add(50 * time.Second))
	if len(sn) != 1 {
		t.Fatalf("concurrent marks must collapse to one entry: %v", sn)
	}
	// 重复冷却只 prolong 不回退：最终 Until = 最大 now_i+RA_i。i=49 时 RA=0
	// 走默认 30s，故最大值 = now+49s+30s = now+79s（prolong 取更晚，与顺序无关）。
	if want := now.Add(49 * time.Second).Add(CooldownRateLimitDefault); !sn[0].Until.Equal(want) {
		t.Fatalf("until %v want %v", sn[0].Until, want)
	}
}
