package app

import (
	"time"

	"AgentSwitch/internal/provider"
	"sync/atomic"
	"testing"

	"AgentSwitch/internal/storage"
)

// countingStore 包住真实 Store，只统计 candidates 装配路径上的读查询：
// 逻辑模型按名查、目标链列举、渠道行读取。其余方法经 embedding 原样转发。
type countingStore struct {
	storage.Store
	routeReads atomic.Int64
}

func (c *countingStore) Channels() storage.ChannelRepo {
	return &countingChannels{ChannelRepo: c.Store.Channels(), n: &c.routeReads}
}

func (c *countingStore) LogicalModels() storage.LogicalModelRepo {
	return &countingLogicalModels{LogicalModelRepo: c.Store.LogicalModels(), n: &c.routeReads}
}

func (c *countingStore) LogicalModelTargets() storage.LogicalModelTargetRepo {
	return &countingTargets{LogicalModelTargetRepo: c.Store.LogicalModelTargets(), n: &c.routeReads}
}

type countingChannels struct {
	storage.ChannelRepo
	n *atomic.Int64
}

func (c *countingChannels) Get(id string) (storage.Channel, error) {
	c.n.Add(1)
	return c.ChannelRepo.Get(id)
}

type countingLogicalModels struct {
	storage.LogicalModelRepo
	n *atomic.Int64
}

func (c *countingLogicalModels) GetByName(name string) (storage.LogicalModel, error) {
	c.n.Add(1)
	return c.LogicalModelRepo.GetByName(name)
}

type countingTargets struct {
	storage.LogicalModelTargetRepo
	n *atomic.Int64
}

func (c *countingTargets) ListByModel(modelID string) ([]storage.LogicalModelTarget, error) {
	c.n.Add(1)
	return c.LogicalModelTargetRepo.ListByModel(modelID)
}

// TestCandidatesCacheHitAvoidsStoreReads：同一路由版本下重复装配只查一次库。
// 夹具 alias→t1→c1 单目标，一次装配 = GetByName + ListByModel + Channel.Get
// 共 3 次读；缓存生效时 3 次 candidates 调用应仍是 3 次读（无缓存是 9 次）。
func TestCandidatesCacheHitAvoidsStoreReads(t *testing.T) {
	a := newTestApp(t, "http://u1", true)
	cs := &countingStore{Store: a.store}
	a.store = cs

	var first []string
	for i := 0; i < 3; i++ {
		cands, err := a.candidates("alias")
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, c := range cands {
			got = append(got, c.TargetID+"/"+c.UpstreamModel)
		}
		if i == 0 {
			first = got
			continue
		}
		if len(got) != len(first) {
			t.Fatalf("第 %d 次候选数 %d，首次 %d", i+1, len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("第 %d 次候选 %v，首次 %v", i+1, got, first)
			}
		}
	}
	if n := cs.routeReads.Load(); n != 3 {
		t.Fatalf("3 次调用应只装配一次（3 次读），实际读库 %d 次", n)
	}
}

// TestCandidatesCacheInvalidatedByStoreWrite：直接写 Store（不经桥接）改目标链后，
// 下一次 candidates 必须立即看到新配置——「配置变更立即生效」不变量不能因为
// 缓存而依赖调用方记得手动清理。失效键是 Store.RouteVersion。
func TestCandidatesCacheInvalidatedByStoreWrite(t *testing.T) {
	a := newTestApp(t, "http://u1", true)
	cands, err := a.candidates("alias")
	if err != nil || len(cands) != 1 {
		t.Fatalf("预热失败: %v %+v", err, cands)
	}
	if got := cands[0].UpstreamModel; got != "model-v1" {
		t.Fatalf("首次上游模型 %q", got)
	}
	if _, err := a.Store().LogicalModelTargets().Save(storage.LogicalModelTarget{
		ID: "t1", LogicalModelID: "lm1", ChannelID: "c1", ModelName: "model-v2", Priority: 10,
	}); err != nil {
		t.Fatal(err)
	}
	cands, err = a.candidates("alias")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].UpstreamModel != "model-v2" {
		t.Fatalf("写库后仍是旧配置: %+v", cands)
	}
}

// TestCandidatesCoolingOverlayNotCached：冷却标记每次调用现叠，不随缓存冻结。
// 冷却期间命中缓存也要带上 CoolingUntil；过期后同一次调用必须立即恢复为未冷却。
func TestCandidatesCoolingOverlayNotCached(t *testing.T) {
	a := newTestApp(t, "http://u1", true)
	if _, err := a.candidates("alias"); err != nil { // 预热缓存
		t.Fatal(err)
	}
	a.Cooldowns().MarkClassified("c1", provider.ClassRateLimit, 1, time.Now())
	cands, err := a.candidates("alias")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].CoolingUntil.IsZero() {
		t.Fatalf("缓存命中时应叠加当前冷却: %+v", cands)
	}
	time.Sleep(1100 * time.Millisecond) // 冷却 TTL=1s（MarkClassified 秒级取整）
	cands, err = a.candidates("alias")
	if err != nil {
		t.Fatal(err)
	}
	if !cands[0].CoolingUntil.IsZero() {
		t.Fatalf("冷却过期后仍被缓存冻结: until=%v", cands[0].CoolingUntil)
	}
}

// TestCandidatesReturnsCopy：返回的切片是缓存副本，调用方改动不得污染后续调用。
func TestCandidatesReturnsCopy(t *testing.T) {
	a := newTestApp(t, "http://u1", true)
	cands, err := a.candidates("alias")
	if err != nil || len(cands) != 1 {
		t.Fatalf("%v %+v", err, cands)
	}
	cands[0].UpstreamModel = "tampered"
	again, err := a.candidates("alias")
	if err != nil {
		t.Fatal(err)
	}
	if again[0].UpstreamModel != "model-v1" {
		t.Fatalf("返回切片未隔离: %q", again[0].UpstreamModel)
	}
}
