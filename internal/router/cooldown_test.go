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
		c.MarkClassified("ch", tc.class, tc.ra, now)
		sn := c.Snapshot(now)
		if len(sn) != 1 {
			t.Fatalf("case %d (%s): snapshot %v", i, tc.class, sn)
		}
		if sn[0].ChannelID != "ch" {
			t.Fatalf("case %d channelID %q", i, sn[0].ChannelID)
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
	c.MarkClassified("ch", provider.ClassParam, 0, now)
	c.MarkClassified("ch", provider.ClassUnknown, 0, now)
	if sn := c.Snapshot(now); len(sn) != 0 {
		t.Fatalf("param/unknown must not cool: %v", sn)
	}
}

func TestCooldownsTryAcquireAndExpiry(t *testing.T) {
	c := NewCooldowns()
	now := time.Now()
	c.MarkClassified("ch", provider.ClassServer, 0, now)
	if _, ok := c.TryAcquire("ch", now.Add(time.Second)); ok {
		t.Fatal("within cooldown TryAcquire must fail")
	}
	if _, ok := c.TryAcquire("ch", now.Add(CooldownServer)); !ok {
		t.Fatal("after expiry TryAcquire must pass")
	}
	// Review Focus 5：过期条目不留残留——半开探测中以 HalfOpen 条目保留
	// （B1 起对 UI 可见），探测成功后彻底清除。
	if sn := c.Snapshot(now.Add(CooldownServer + time.Second)); len(sn) != 1 || !sn[0].HalfOpen {
		t.Fatalf("probing entry must be visible as half-open: %v", sn)
	}
	c.MarkSuccess("ch")
	if sn := c.Snapshot(now.Add(CooldownServer + 2*time.Second)); len(sn) != 0 {
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
			c.MarkClassified("ch", provider.ClassRateLimit, i%7, now.Add(time.Duration(i)*time.Second))
			c.TryAcquire("ch", now)
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

// TestCooldownSharedAcrossModels：同一 ChannelID 被 MarkClassified 后 TryAcquire 失败。
// 冷却键=ChannelID（语义不变仅键名变），跨逻辑模型共享冷却——同一渠道在任一逻辑模型
// 下失败进入冷却后，其它逻辑模型复用该渠道时二次校验同样被拒。
func TestCooldownSharedAcrossModels(t *testing.T) {
	c := NewCooldowns()
	now := time.Now()
	const ch = "ch-shared"
	c.MarkClassified(ch, provider.ClassQuota, 0, now) // 逻辑模型 m1 的请求把渠道打入冷却
	if _, ok := c.TryAcquire(ch, now.Add(time.Second)); ok {
		t.Fatal("same ChannelID must stay cooled (shared across logical models)")
	}
}

func TestCooldownsHalfOpenSingleFlight(t *testing.T) {
	c := NewCooldowns()
	now := time.Now()
	c.MarkClassified("ch", provider.ClassServer, 0, now)
	after := now.Add(CooldownServer)
	if _, ok := c.TryAcquire("ch", after); !ok {
		t.Fatal("first request after expiry should be the probe")
	}
	if _, ok := c.TryAcquire("ch", after.Add(time.Second)); ok {
		t.Fatal("second request must wait for probe result")
	}
	c.MarkSuccess("ch")
	if _, ok := c.TryAcquire("ch", after.Add(2*time.Second)); !ok {
		t.Fatal("after success channel must be available")
	}
}

func TestCooldownsHalfOpenFailureExtends(t *testing.T) {
	c := NewCooldowns()
	now := time.Now()
	c.MarkClassified("ch", provider.ClassServer, 0, now)
	after := now.Add(CooldownServer)
	if _, ok := c.TryAcquire("ch", after); !ok {
		t.Fatal("first request after expiry should probe")
	}
	c.MarkClassified("ch", provider.ClassServer, 0, after)
	if _, ok := c.TryAcquire("ch", after.Add(time.Second)); ok {
		t.Fatal("failed probe must re-cool the channel")
	}
	if sn := c.Snapshot(after.Add(time.Second)); len(sn) != 1 {
		t.Fatalf("snapshot = %v", sn)
	}
}

// TestCooldownsSnapshotMarksHalfOpen 覆盖 B1：冷却到期、半开探测中的渠道
// 留在快照里且 HalfOpen=true（供 UI 显示「恢复中」）；冷却中为 false。
func TestCooldownsSnapshotMarksHalfOpen(t *testing.T) {
	c := NewCooldowns()
	now := time.Now()
	c.MarkClassified("ch", provider.ClassServer, 0, now)
	sn := c.Snapshot(now.Add(time.Second))
	if len(sn) != 1 || sn[0].HalfOpen {
		t.Fatalf("冷却中条目应 HalfOpen=false：%+v", sn)
	}
	if _, ok := c.TryAcquire("ch", now.Add(CooldownServer)); !ok {
		t.Fatal("到期首个请求应为探测")
	}
	sn = c.Snapshot(now.Add(CooldownServer + time.Second))
	if len(sn) != 1 || !sn[0].HalfOpen {
		t.Fatalf("半开探测中应保留条目且 HalfOpen=true：%+v", sn)
	}
	c.MarkSuccess("ch")
	if sn := c.Snapshot(now.Add(CooldownServer + 2*time.Second)); len(sn) != 0 {
		t.Fatalf("探测成功后条目应清除：%+v", sn)
	}
}
