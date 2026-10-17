package router

import (
	"sort"
	"sync"
	"time"

	"AgentSwitch/internal/provider"
)

// 冷却时长（spec §6.2）：P0-B 常量，代码内定义不进配置；后续可调。
const (
	CooldownRateLimitDefault = 30 * time.Second
	CooldownRateLimitMax     = 10 * time.Minute
	CooldownQuota            = 30 * time.Minute
	CooldownServer           = 15 * time.Second
	CooldownConnect          = 15 * time.Second
	CooldownAuth             = time.Hour
)

// CooldownEntry 是一枚渠道的冷却快照。
type CooldownEntry struct {
	ChannelID string
	Until     time.Time
	Reason    string // 冷却原因（错误分类原文）
}

// Cooldowns 是内存冷却存储：键 = 渠道 ID（渠道是执行单元，冷却跨逻辑模型共享）。
// 原子性由 mu 保证（docx §6.3 冷却并发更新必须原子）；重启即清（spec §3.2 不落库）。
// 零值不可用，用 NewCooldowns。
type Cooldowns struct {
	mu sync.Mutex
	m  map[string]CooldownEntry
}

func NewCooldowns() *Cooldowns { return &Cooldowns{m: map[string]CooldownEntry{}} }

// cooldownFor 把错误分类映射为冷却时长；retryAfterSec 仅 rate_limited 使用。
func cooldownFor(class provider.ErrorClass, retryAfterSec int) (time.Duration, string) {
	switch class {
	case provider.ClassRateLimit:
		d := time.Duration(retryAfterSec) * time.Second
		if d <= 0 {
			d = CooldownRateLimitDefault
		}
		if d > CooldownRateLimitMax {
			d = CooldownRateLimitMax
		}
		return d, string(class)
	case provider.ClassQuota:
		return CooldownQuota, string(class)
	case provider.ClassServer:
		return CooldownServer, string(class)
	case provider.ClassConnect:
		return CooldownConnect, string(class)
	case provider.ClassAuth:
		return CooldownAuth, string(class)
	default:
		return 0, string(class) // param/unknown 不冷却
	}
}

// MarkClassified 按错误分类写冷却；不可冷却分类为 no-op。同渠道重复冷却只
// prolong（取更晚到期），并发 429 不互相回退。
func (c *Cooldowns) MarkClassified(channelID string, class provider.ErrorClass, retryAfterSec int, now time.Time) {
	d, reason := cooldownFor(class, retryAfterSec)
	if d <= 0 || channelID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := CooldownEntry{ChannelID: channelID, Until: now.Add(d), Reason: reason}
	if old, ok := c.m[channelID]; ok && old.Until.After(e.Until) {
		e.Until = old.Until
	}
	c.m[channelID] = e
}

// TryAcquire 二次校验（docx §7：路由器真正发请求前二次校验账号状态）：
// 未冷却返回 (零值,true)；冷却中返回 (条目,false)。本方法不写状态——写入只在 MarkClassified。
func (c *Cooldowns) TryAcquire(channelID string, now time.Time) (CooldownEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[channelID]
	if !ok || !now.Before(e.Until) {
		return CooldownEntry{}, true
	}
	return e, false
}

// Snapshot 返回未过期条目（按到期升序）；过期条目顺手清理。
func (c *Cooldowns) Snapshot(now time.Time) []CooldownEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]CooldownEntry, 0, len(c.m))
	for id, e := range c.m {
		if !now.Before(e.Until) {
			delete(c.m, id)
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Until.Before(out[j].Until) })
	return out
}
