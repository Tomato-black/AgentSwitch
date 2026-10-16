# 渠道管理与模型维护 + P0-B 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 P0-A′ 的「供应商+账号+全局别名」重构为 new-api 式 channels 聚合根（渠道含密钥们+渠道级模型清单），新增 OpenAI 出站协议转换，并完成 P0-B 账号池故障转移（冷却/二次校验/首字节前切换）。

**Architecture:** 分层依序落地：storage 新三表（增量迁移过渡，最后合并单 v1）→ router 冷却存储与优先级路由 → protocol OpenAI 转换器 → provider 预设与适配器 → gateway 候选循环（转移/冷却/提交点）→ bridge 渠道服务 → desktop 渠道/模型/概览三页 → 删旧收尾。每个任务结束整树可编译、测试全绿。

**Tech Stack:** Go 1.27.2、mygo v0.5.1（桌面 UI）、modernc.org/sqlite、httptest Fake Provider（全部测试离线）。

**Spec:** `docs/superpowers/specs/2026-10-17-channels-models-p0b-design.md`（本计划从 spec 论证，执行者两份都要读）。

## Global Constraints

- Go 1.27.2；不新增任何第三方依赖（mygo/modernc.org/sqlite 已在 go.mod）。
- 全部测试离线：上游一律 httptest Fake；不打真实供应商。
- 涉冷却/并发的测试必须在 `-race` 下运行；每个任务收尾跑 `go build ./... && go vet ./... && go test ./...`，涉并发的任务加 `-race`。
- 零密钥不变量：日志、错误、UI、测试断言文本不得出现密钥本体/Authorization/完整 prompt；key_tail 只存尾 4；审计只记成功变更（`actor=ui`）。
- 凭据只入 OS Keychain / 显式确认的 0600 降级文件；SQLite 只存 `secret_key` 引用（docx §8，channel_keys 语义不变）。
- `base_url` 校验沿用 `provider.ValidateBaseURL`（https / 回环 http / 显式 allowPrivate 放行），不改语义。
- 路由不变量（docx §6.3/§7）：首字节前才可跨候选转移；流开始后绝不重放；400 类参数错误不转移不冷却；客户端取消直接取消上游不转移；发送前 `TryAcquire` 锁内二次校验。
- UI 文案不变量：既有可断言字符串尽量逐字保留；删除一律两段确认。
- migration 纪律的既定偏离（spec 决策 5）：Task 1-9 追加 migration 003；Task 10 合并为单一 v1，用户删库重建一次。
- 提交信息中文 conventional 风格（`feat:`/`test:`/`refactor:`/`docs:`），结尾 `Co-Authored-By: Claude Code <noreply@anthropic.com>`。

## Review Focus

spec 未被常规测试覆盖、最易咬到真实用户的五类输入，pinned 到所属任务：

1. **多渠道混布时单个坏渠道（BaseURL 打错/DNS 不通）**——期望：该渠道被冷却跳过，其余渠道照常服务同一公开模型。→ Task 6 `TestFailoverSkipsBadChannel`。
2. **同一渠道重复添加同一公开模型名**——期望：明确中文错误，而非 SQL UNIQUE 原文。→ Task 7 `TestChannelsAddModelDuplicateFriendlyError`。
3. **OpenAI 上游异常响应（空 choices / 缺 finish_reason / usage-only 尾块）**——期望：转换器不 panic，按 end_turn 收口。→ Task 3 `TestOpenAIResponseEmptyChoicesDefensive`、`TestStreamTransformerUsageOnlyTail`。
4. **批量密钥文本含空行/首尾空白**——期望：空行跳过、其余照存。→ Task 7 `TestChannelsAddKeysSkipsBlankLines`。
5. **冷却刚过期的渲染**——期望：快照过滤过期条目，UI 不出现负剩余秒。→ Task 2 `TestCooldownsSnapshotFiltersExpired` + Task 7 PoolStatus `RemainingSec` 钳制断言。

---

### Task 1: storage 三表与 Repo（增量迁移 003，旧表暂留）

**Files:**
- Modify: `internal/storage/db.go`（追加 migration003）
- Modify: `internal/storage/storage.go`（新结构体 + Store 接口扩三个方法）
- Modify: `internal/storage/repos.go`（三个新 repo 实现）
- Test: `internal/storage/storage_test.go`（追加新用例，旧用例不动）

**Interfaces:**
- Consumes: 现有 `dbStore`/`migrate`/`NewID`/`boolInt`/`mapNotFound`。
- Produces（后续任务依赖的精确签名）:
  - `type Channel struct { ID, Name, Type, Protocol, BaseURL string; Priority int; Enabled, AllowPrivateUpstream bool; CreatedAt, UpdatedAt time.Time }`
  - `type ChannelKey struct { ID, ChannelID, Name, SecretKey, SecretBackend, KeyTail string; Priority int; Enabled bool; CreatedAt, UpdatedAt time.Time }`
  - `type ChannelModel struct { ID, ChannelID, PublicModel, UpstreamModel string; Enabled bool; CreatedAt, UpdatedAt time.Time }`
  - `Store` 新增 `Channels() ChannelRepo` / `ChannelKeys() ChannelKeyRepo` / `ChannelModels() ChannelModelRepo`
  - `ChannelRepo`: `List() ([]Channel, error)`（ORDER BY priority ASC, id ASC）、`Get(id) (Channel, error)`、`Save(Channel) error`、`Delete(id) error`、`SetEnabled(id string, on bool) error`、`HasUsableKey() (bool, error)`
  - `ChannelKeyRepo`: `List() ([]ChannelKey, error)`、`ListByChannel(cid string) ([]ChannelKey, error)`（ORDER BY priority ASC, id ASC）、`Get(id)`、`Save(ChannelKey) error`、`Delete(id) error`、`SetEnabled(id, on)`、`CountByChannel(cid) (int, error)`
  - `ChannelModelRepo`: `List() ([]ChannelModel, error)`、`ListByChannel(cid)`、`ListByPublic(public string) ([]ChannelModel, error)`、`Get(id)`、`Save(ChannelModel) error`（UNIQUE(channel_id,public_model) 冲突冒泡为非 nil error）、`Delete(id)`、`SetEnabled(id, on)`、`PublicModels() ([]string, error)`（DISTINCT enabled）、`CountByChannel(cid) (int, error)`

- [ ] **Step 1: 在 db.go 追加 migration003 并注册**

```go
// migration003 是渠道化重构（spec 2026-10-17 §3.1）的三张新表。过渡期与旧
// providers/accounts/models 并存；Task 10 合并为单一 v1 并删旧表（spec 决策 5：
// 发布前无存量用户，删库重建）。
const migration003 = `
CREATE TABLE channels (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, type TEXT NOT NULL,
  protocol TEXT NOT NULL, base_url TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 10, enabled INTEGER NOT NULL DEFAULT 1,
  allow_private_upstream INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE channel_keys (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  name TEXT NOT NULL, secret_key TEXT NOT NULL, secret_backend TEXT NOT NULL,
  key_tail TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL DEFAULT 10,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX idx_channel_keys_channel ON channel_keys(channel_id);
CREATE TABLE channel_models (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  public_model TEXT NOT NULL, upstream_model TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(channel_id, public_model)
);
CREATE INDEX idx_channel_models_channel ON channel_models(channel_id);
CREATE INDEX idx_channel_models_public ON channel_models(public_model);
`
```

在 `migrations` 切片追加 `{3, migration003},`。

- [ ] **Step 2: 在 storage.go 追加结构体与接口（§Interfaces 的逐字实现）**

三个结构体按 Interfaces 块定义；`Store` 接口追加三行（旧四个方法不动）：

```go
// Store 是持久层入口（过渡期新旧并存，Task 10 删旧）。
type Store interface {
	Providers() ProviderRepo
	Accounts() AccountRepo
	Models() ModelRepo
	Channels() ChannelRepo
	ChannelKeys() ChannelKeyRepo
	ChannelModels() ChannelModelRepo
	Settings() SettingsRepo
	Close() error
}
```

repo 接口定义：

```go
// ChannelRepo 读写 channels 表；List 已按路由序（priority,id）。
type ChannelRepo interface {
	List() ([]Channel, error)
	Get(id string) (Channel, error)
	Save(c Channel) error
	Delete(id string) error
	SetEnabled(id string, on bool) error
	// HasUsableKey 报告是否存在 enabled 渠道×enabled 密钥（readyz/Degraded 口径）。
	HasUsableKey() (bool, error)
}

// ChannelKeyRepo 读写 channel_keys（secret_key 是凭据引用，本体在密钥存储）。
type ChannelKeyRepo interface {
	List() ([]ChannelKey, error)
	ListByChannel(channelID string) ([]ChannelKey, error)
	Get(id string) (ChannelKey, error)
	Save(k ChannelKey) error
	Delete(id string) error
	SetEnabled(id string, on bool) error
	CountByChannel(channelID string) (int, error)
}

// ChannelModelRepo 读写渠道级模型清单；UNIQUE(channel_id, public_model)。
type ChannelModelRepo interface {
	List() ([]ChannelModel, error)
	ListByChannel(channelID string) ([]ChannelModel, error)
	ListByPublic(public string) ([]ChannelModel, error)
	Get(id string) (ChannelModel, error)
	Save(m ChannelModel) error
	Delete(id string) error
	SetEnabled(id string, on bool) error
	// PublicModels 去重输出 enabled 公开模型名（/v1/models 并集）。
	PublicModels() ([]string, error)
	CountByChannel(channelID string) (int, error)
}
```

- [ ] **Step 3: 在 repos.go 追加三个 repo 实现**

实现完全沿用现有 providerRepo 的模式（`boolInt`/`mapNotFound`/RFC3339 时间/Save 保留 CreatedAt/ON CONFLICT upsert）。要点：

```go
type channelRepo struct{ db *sql.DB }

const channelCols = `id,name,type,protocol,base_url,priority,enabled,allow_private_upstream,created_at,updated_at`

func scanChannel(sc func(...any) error) (Channel, error) {
	var c Channel
	var pr, en, ap int
	var ca, ua string
	if err := sc(&c.ID, &c.Name, &c.Type, &c.Protocol, &c.BaseURL, &pr, &en, &ap, &ca, &ua); err != nil {
		return Channel{}, err
	}
	c.Priority, c.Enabled, c.AllowPrivateUpstream = pr, en == 1, ap == 1
	c.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	c.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return c, nil
}

func (r *channelRepo) List() ([]Channel, error) {
	rows, err := r.db.Query(`SELECT ` + channelCols + ` FROM channels ORDER BY priority ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		c, err := scanChannel(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
```

`Get` 用 `QueryRow(...WHERE id=?).Scan(...)` 经 `scanChannel`，错误 `mapNotFound`。`Save` 照 providerRepo.Save 模式（Get 保留 CreatedAt → INSERT ... ON CONFLICT(id) DO UPDATE 全字段）。`Delete`/`SetEnabled` 单语句。特殊方法：

```go
func (r *channelRepo) HasUsableKey() (bool, error) {
	var n int
	err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM channels c
		JOIN channel_keys k ON k.channel_id = c.id
		WHERE c.enabled=1 AND k.enabled=1)`).Scan(&n)
	return n == 1, err
}

type channelKeyRepo struct{ db *sql.DB }

const keyCols = `id,channel_id,name,secret_key,secret_backend,key_tail,priority,enabled,created_at,updated_at`

// scanKey 与 scanChannel 同构；ListByChannel 排序 ORDER BY priority ASC, id ASC，
// List 全表 ORDER BY channel_id ASC, priority ASC, id ASC。
// CountByChannel: SELECT COUNT(*) FROM channel_keys WHERE channel_id=?

type channelModelRepo struct{ db *sql.DB }

const modelCols = `id,channel_id,public_model,upstream_model,enabled,created_at,updated_at`

// Save 照 modelRepo 模式；UNIQUE(channel_id,public_model) 冲突由驱动冒泡为
// 非 nil error（与旧 models.public_model UNIQUE 同口径，bridge 负责友好化）。
// PublicModels:
//   SELECT DISTINCT public_model FROM channel_models WHERE enabled=1 ORDER BY public_model
// ListByPublic: SELECT ... WHERE public_model=? ORDER BY channel_id ASC
```

- [ ] **Step 4: 在 dbStore 追加三个访问器**

```go
func (s *dbStore) Channels() ChannelRepo         { return &channelRepo{s.db} }
func (s *dbStore) ChannelKeys() ChannelKeyRepo   { return &channelKeyRepo{s.db} }
func (s *dbStore) ChannelModels() ChannelModelRepo { return &channelModelRepo{s.db} }
```

- [ ] **Step 5: 写失败测试（storage_test.go 追加）**

```go
func channelFixture(t *testing.T, s Store) (Channel, Channel) {
	t.Helper()
	a := Channel{ID: NewID(), Name: "b", Type: "anthropic-compatible", Protocol: "anthropic",
		BaseURL: "https://b.example.com", Priority: 20, Enabled: true}
	z := Channel{ID: NewID(), Name: "a", Type: "anthropic-official", Protocol: "anthropic",
		BaseURL: "https://api.anthropic.com", Priority: 10, Enabled: true}
	for _, c := range []Channel{a, z} {
		if err := s.Channels().Save(c); err != nil {
			t.Fatal(err)
		}
	}
	return a, z
}

func TestChannelRepoOrderingAndLifecycle(t *testing.T) {
	s := openStore(t)
	a, z := channelFixture(t, s)
	list, err := s.Channels().List()
	if err != nil {
		t.Fatal(err)
	}
	// priority ASC：10 的 z 在前（路由序由 SQL 保证）
	if len(list) != 2 || list[0].ID != z.ID || list[1].ID != a.ID {
		t.Fatalf("ordering: %+v", list)
	}
	if err := s.Channels().SetEnabled(z.ID, false); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Channels().Get(z.ID)
	if got.Enabled {
		t.Fatal("SetEnabled(false) failed")
	}
	if err := s.Channels().Delete(z.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Channels().Get(z.ID); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestChannelKeyCascadeAndCount(t *testing.T) {
	s := openStore(t)
	a, z := channelFixture(t, s)
	k1 := ChannelKey{ID: NewID(), ChannelID: a.ID, Name: "k1", SecretKey: "account:x1",
		SecretBackend: "os-keychain", KeyTail: "x1", Priority: 10, Enabled: true}
	k2 := ChannelKey{ID: NewID(), ChannelID: a.ID, Name: "k2", SecretKey: "account:x2",
		SecretBackend: "os-keychain", KeyTail: "x2", Priority: 5, Enabled: true}
	// z 优先级更低但 key 优先级更小——只验证渠道内排序
	k3 := ChannelKey{ID: NewID(), ChannelID: z.ID, Name: "k3", SecretKey: "account:x3",
		SecretBackend: "os-keychain", Priority: 1, Enabled: true}
	for _, k := range []ChannelKey{k1, k2, k3} {
		if err := s.ChannelKeys().Save(k); err != nil {
			t.Fatal(err)
		}
	}
	keys, err := s.ChannelKeys().ListByChannel(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 || keys[0].ID != k2.ID || keys[1].ID != k1.ID {
		t.Fatalf("keys ordering: %+v", keys)
	}
	n, _ := s.ChannelKeys().CountByChannel(a.ID)
	if n != 2 {
		t.Fatalf("count = %d", n)
	}
	// 级联：删渠道 → keys 消失
	if err := s.Channels().Delete(z.ID); err != nil {
		t.Fatal(err)
	}
	if ks, _ := s.ChannelKeys().ListByChannel(z.ID); len(ks) != 0 {
		t.Fatal("cascade delete failed")
	}
}

func TestChannelModelUniquePerChannelAndQueries(t *testing.T) {
	s := openStore(t)
	a, z := channelFixture(t, s)
	m := func(cid, pub, up string) ChannelModel {
		return ChannelModel{ID: NewID(), ChannelID: cid, PublicModel: pub, UpstreamModel: up, Enabled: true}
	}
	if err := s.ChannelModels().Save(m(a.ID, "claude-x", "up-x")); err != nil {
		t.Fatal(err)
	}
	// 同渠道同公开名 → UNIQUE 冲突
	if err := s.ChannelModels().Save(m(a.ID, "claude-x", "up-y")); err == nil {
		t.Fatal("duplicate (channel,public) must fail")
	}
	// 跨渠道同公开名 → 允许（渠道级语义核心）
	if err := s.ChannelModels().Save(m(z.ID, "claude-x", "up-z")); err != nil {
		t.Fatal(err)
	}
	// 禁用一条 → PublicModels 不再输出
	byPub, _ := s.ChannelModels().ListByPublic("claude-x")
	if len(byPub) != 2 {
		t.Fatalf("ListByPublic = %d", len(byPub))
	}
	first := byPub[0]
	if err := s.ChannelModels().SetEnabled(first.ID, false); err != nil {
		t.Fatal(err)
	}
	pubs, _ := s.ChannelModels().PublicModels()
	if len(pubs) != 1 || pubs[0] != "claude-x" {
		t.Fatalf("PublicModels = %v (distinct+enabled)", pubs)
	}
}

func TestHasUsableKey(t *testing.T) {
	s := openStore(t)
	a, _ := channelFixture(t, s)
	ok, err := s.Channels().HasUsableKey()
	if err != nil || ok {
		t.Fatalf("no key yet: ok=%v err=%v", ok, err)
	}
	if err := s.ChannelKeys().Save(ChannelKey{ID: NewID(), ChannelID: a.ID, Name: "k",
		SecretKey: "account:h", SecretBackend: "os-keychain", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	ok, err = s.Channels().HasUsableKey()
	if err != nil || !ok {
		t.Fatalf("usable: ok=%v err=%v", ok, err)
	}
}
```

（若 `openStore` 助手与现测试名不同，沿用现文件里的既有开库助手名。）

- [ ] **Step 6: 跑测试确认通过**

Run: `go test ./internal/storage -v`
Expected: 全部 PASS（新旧用例同绿）

- [ ] **Step 7: 整树编译与全测**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿（纯增量，下游不受影响）

- [ ] **Step 8: Commit**

```bash
git add internal/storage
git commit -m "feat(storage): channels/channel_keys/channel_models 三表与 Repo（迁移 003，过渡并存）

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 2: router 冷却存储 Cooldowns（内存、原子、-race）

**Files:**
- Create: `internal/router/cooldown.go`
- Test: `internal/router/cooldown_test.go`

**Interfaces:**
- Consumes: `provider.ErrorClass`（`internal/provider/classify.go`：`ClassQuota/ClassRateLimit/ClassAuth/ClassServer/ClassParam/ClassConnect`）。
- Produces:
  - `type CooldownEntry struct { KeyID string; Until time.Time; Reason string }`
  - `func NewCooldowns() *Cooldowns`
  - `func (c *Cooldowns) MarkClassified(keyID string, class provider.ErrorClass, retryAfterSec int, now time.Time)`
  - `func (c *Cooldowns) TryAcquire(keyID string, now time.Time) (CooldownEntry, bool)`（true=未冷却可发）
  - `func (c *Cooldowns) Snapshot(now time.Time) []CooldownEntry`（仅未过期，按 Until 升序；顺手清理过期）
  - 常量：`CooldownRateLimitDefault=30s`、`CooldownRateLimitMax=10m`、`CooldownQuota=30m`、`CooldownServer=15s`、`CooldownConnect=15s`、`CooldownAuth=1h`

- [ ] **Step 1: 写失败测试**

```go
package router

import (
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
```

（`sync` 需要import；若同包已有 cooldown_test 外的 helper 注意别重名。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/router -v`
Expected: FAIL（`NewCooldowns` 未定义）

- [ ] **Step 3: 实现 cooldown.go**

```go
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

// CooldownEntry 是一枚密钥的冷却快照。
type CooldownEntry struct {
	KeyID  string
	Until  time.Time
	Reason string // 冷却原因（错误分类原文）
}

// Cooldowns 是内存冷却存储：键 = channel_keys.id。原子性由 mu 保证（docx §6.3
// 冷却并发更新必须原子）；重启即清（spec §3.2 不落库）。零值不可用，用 NewCooldowns。
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

// MarkClassified 按错误分类写冷却；不可冷却分类为 no-op。同钥重复冷却只
// prolong（取更晚到期），并发 429 不互相回退。
func (c *Cooldowns) MarkClassified(keyID string, class provider.ErrorClass, retryAfterSec int, now time.Time) {
	d, reason := cooldownFor(class, retryAfterSec)
	if d <= 0 || keyID == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := CooldownEntry{KeyID: keyID, Until: now.Add(d), Reason: reason}
	if old, ok := c.m[keyID]; ok && old.Until.After(e.Until) {
		e.Until = old.Until
	}
	c.m[keyID] = e
}

// TryAcquire 二次校验（docx §7：路由器真正发请求前二次校验账号状态）：
// 未冷却返回 (零值,true)；冷却中返回 (条目,false)。本方法不写状态——写入只在 MarkClassified。
func (c *Cooldowns) TryAcquire(keyID string, now time.Time) (CooldownEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[keyID]
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
```

- [ ] **Step 4: 跑测试确认通过（带 -race）**

Run: `go test -race ./internal/router -v`
Expected: 全 PASS

- [ ] **Step 5: 整树验证 + Commit**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿

```bash
git add internal/router
git commit -m "feat(router): 内存冷却存储 Cooldowns——分类时长表/原子二次校验/过期清理

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 3: protocol OpenAI 转换器（请求/非流响应/流式）

**Files:**
- Create: `internal/protocol/openai.go`
- Test: `internal/protocol/openai_test.go`

**Interfaces:**
- Consumes: 无（纯转换包，不 import 其他内部包）。
- Produces:
  - `func AnthropicRequestToOpenAI(body []byte) (converted []byte, dropped []string, err error)`（dropped = 被丢弃的 Anthropic 专有字段名，已排序）
  - `func OpenAIResponseToAnthropic(body []byte) ([]byte, error)`
  - `type OpenAIStreamTransformer`、`func NewOpenAIStreamTransformer() *OpenAIStreamTransformer`、`func (t *OpenAIStreamTransformer) Feed(p []byte) []byte`、`func (t *OpenAIStreamTransformer) Finish() []byte`
  - stop reason 映射：`stop→end_turn`、`length→max_tokens`、`tool_calls/function_call→tool_use`、其余→`end_turn`

- [ ] **Step 1: 写失败测试（完整文件）**

```go
package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAnthropicToOpenAIRequest(t *testing.T) {
	anth := []byte(`{
	  "model":"up-1","max_tokens":256,"stream":true,
	  "temperature":0.5,"top_p":0.9,"stop_sequences":["END"],
	  "metadata":{"x":1},"thinking":{"type":"enabled"},
	  "system":"be brief",
	  "messages":[
	    {"role":"user","content":"hi"},
	    {"role":"assistant","content":[
	      {"type":"text","text":"let me check"},
	      {"type":"tool_use","id":"tu1","name":"search","input":{"q":"g"}}
	    ]},
	    {"role":"user","content":[
	      {"type":"tool_result","tool_use_id":"tu1","content":"result text"},
	      {"type":"text","text":"thanks"}
	    ]}
	  ],
	  "tools":[{"name":"search","description":"s","input_schema":{"type":"object"}}],
	  "tool_choice":{"type":"any"}
	}`)
	out, dropped, err := AnthropicRequestToOpenAI(anth)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 2 || dropped[0] != "metadata" || dropped[1] != "thinking" {
		t.Fatalf("dropped %v", dropped)
	}
	var req struct {
		Model       string  `json:"model"`
		MaxTokens   int     `json:"max_tokens"`
		Stream      bool    `json:"stream"`
		Temperature float64 `json:"temperature"`
		TopP        float64 `json:"top_p"`
		Stop        []string `json:"stop"`
		Messages    []struct {
			Role       string `json:"role"`
			Content    string `json:"content"`
			ToolCalls  []map[string]any `json:"tool_calls"`
			ToolCallID string `json:"tool_call_id"`
		} `json:"messages"`
		Tools []struct {
			Type     string `json:"type"`
			Function struct {
				Name       string         `json:"name"`
				Parameters map[string]any `json:"parameters"`
			} `json:"function"`
		} `json:"tools"`
		ToolChoice any `json:"tool_choice"`
	}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if req.Model != "up-1" || req.MaxTokens != 256 || !req.Stream || req.Stop[0] != "END" {
		t.Fatalf("params: %+v", req)
	}
	if req.Temperature != 0.5 || req.TopP != 0.9 {
		t.Fatalf("sampling: %+v", req)
	}
	if len(req.Messages) != 5 { // system + user + assistant(text+tool_calls) + user(text) + tool
		t.Fatalf("messages = %d: %+v", len(req.Messages), req.Messages)
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "be brief" {
		t.Fatalf("system: %+v", req.Messages[0])
	}
	asst := req.Messages[2]
	if asst.Role != "assistant" || len(asst.ToolCalls) != 1 {
		t.Fatalf("assistant: %+v", asst)
	}
	tc := asst.ToolCalls[0]
	if tc["type"] != "function" {
		t.Fatalf("tool call: %+v", tc)
	}
	fn := tc["function"].(map[string]any)
	if fn["name"] != "search" || fn["arguments"] != `{"q":"g"}` {
		t.Fatalf("function: %+v", fn)
	}
	tool := req.Messages[3]
	if tool.Role != "tool" || tool.ToolCallID != "tu1" || tool.Content != "result text" {
		t.Fatalf("tool result: %+v", tool)
	}
	if len(req.Tools) != 1 || req.Tools[0].Function.Name != "search" || req.Tools[0].Function.Parameters["type"] != "object" {
		t.Fatalf("tools: %+v", req.Tools)
	}
	if req.ToolChoice != "required" {
		t.Fatalf("tool_choice: %v", req.ToolChoice)
	}
}

func TestOpenAIToAnthropicResponse(t *testing.T) {
	up := mustJSON(t, map[string]any{
		"id": "cmpl-1", "model": "up-1",
		"choices": []map[string]any{{
			"message": map[string]any{
				"content": "hello",
				"tool_calls": []map[string]any{{
					"id": "call_1", "type": "function",
					"function": map[string]any{"name": "search", "arguments": `{"q":"g"}`},
				}},
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
	})
	out, err := OpenAIResponseToAnthropic(up)
	if err != nil {
		t.Fatal(err)
	}
	var resp struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Type != "message" || resp.Role != "assistant" || resp.StopReason != "tool_use" {
		t.Fatalf("resp: %+v", resp)
	}
	if len(resp.Content) != 2 || resp.Content[0].Type != "text" || resp.Content[0].Text != "hello" {
		t.Fatalf("content: %+v", resp.Content)
	}
	tb := resp.Content[1]
	if tb.Type != "tool_use" || tb.ID != "call_1" || tb.Name != "search" {
		t.Fatalf("tool_use block: %+v", tb)
	}
	if !strings.Contains(string(tb.Input), `"q"`) {
		t.Fatalf("input: %s", tb.Input)
	}
	if resp.Usage.InputTokens != 11 || resp.Usage.OutputTokens != 7 {
		t.Fatalf("usage: %+v", resp.Usage)
	}
}

// Review Focus 3：空 choices / 坏 finish 不 panic，end_turn 收口。
func TestOpenAIResponseEmptyChoicesDefensive(t *testing.T) {
	out, err := OpenAIResponseToAnthropic([]byte(`{"id":"x","model":"m","choices":[],"usage":{"prompt_tokens":3,"completion_tokens":0}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"stop_reason":"end_turn"`) {
		t.Fatalf("defensive end_turn missing: %s", out)
	}
	if !strings.Contains(string(out), `"input_tokens":3`) {
		t.Fatalf("usage lost: %s", out)
	}
}

func TestStreamTransformerEventSequence(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	chunk1 := []byte("data: {\"id\":\"c1\",\"model\":\"up-1\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"he\"}}]}\n\n")
	chunk2 := []byte("data: {\"id\":\"c1\",\"model\":\"up-1\",\"choices\":[{\"delta\":{\"content\":\"llo\"},\"finish_reason\":null}]}\n\n")
	chunk3 := []byte("data: {\"id\":\"c1\",\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	// 跨字节边界喂入：把 chunk2 拆两半
	var out []byte
	out = append(out, tu.Feed(chunk1)...)
	out = append(out, tu.Feed(chunk2[:20])...)
	out = append(out, tu.Feed(chunk2[20:])...)
	out = append(out, tu.Feed(chunk3)...)
	out = append(out, tu.Finish()...)
	s := string(out)
	for _, want := range []string{
		"event: message_start", `"model":"up-1"`,
		"event: content_block_start", `"type":"text"`,
		`"text":"he"`, `"text":"llo"`,
		"event: content_block_stop",
		"event: message_delta", `"stop_reason":"end_turn"`,
		"event: message_stop",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	// 事件顺序：message_start < content_block_start < message_delta
	if strings.Index(s, "message_start") > strings.Index(s, "content_block_start") ||
		strings.Index(s, "content_block_stop") > strings.Index(s, "message_delta") {
		t.Fatalf("event order wrong:\n%s", s)
	}
}

func TestStreamTransformerToolCallAccumulation(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	feed := func(s string) { tu.Feed([]byte(s)) }
	feed("data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
	feed("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"t1\",\"name\":\"f1\",\"arguments\":\"{\\\"a\\\":\"}]}}]}\n\n")
	feed("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"arguments\":\"1}\"}]}}]}\n\n")
	feed("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":1,\"id\":\"t2\",\"name\":\"f2\",\"arguments\":\"{}\"}]}}]}\n\n")
	feed("data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
	feed("data: [DONE]\n\n")
	out := string(append(tu.Feed(nil), tu.Finish()...))
	// 两个 tool_use 块、参数累积完整、stop=tool_use
	if !strings.Contains(out, `"name":"f1"`) || !strings.Contains(out, `"name":"f2"`) {
		t.Fatalf("tool blocks missing:\n%s", out)
	}
	if !strings.Contains(out, `{"a":1}`) {
		t.Fatalf("args not accumulated:\n%s", out)
	}
	if !strings.Contains(out, `"stop_reason":"tool_use"`) {
		t.Fatalf("stop reason:\n%s", out)
	}
	if strings.Count(out, "content_block_start") != 2 || strings.Count(out, "content_block_stop") != 2 {
		t.Fatalf("block open/close mismatch:\n%s", out)
	}
}

// Review Focus 3：截断流（无 finish_reason/[DONE]）Finish 兜底收口。
func TestStreamTransformerTruncatedFinish(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	tu.Feed([]byte("data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n"))
	out := string(append(tu.Feed([]byte("data: {\"choices\":[{\"delta\":{}}]}\n\n")), tu.Finish()...))
	if !strings.Contains(out, `"stop_reason":"end_turn"`) || !strings.Contains(out, "message_stop") {
		t.Fatalf("truncated stream must close end_turn:\n%s", out)
	}
}

// usage-only 尾块（choices 为空）不得产生空事件或 panic。
func TestStreamTransformerUsageOnlyTail(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	head := tu.Feed([]byte("data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
	tail := tu.Feed([]byte("data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\ndata: [DONE]\n\n"))
	out := string(append(head, tail...))
	if strings.Contains(out, `"input_tokens":0`) && !strings.Contains(out, `"input_tokens":5`) {
		t.Fatalf("usage lost:\n%s", out)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/protocol -v`
Expected: FAIL（符号未定义）

- [ ] **Step 3: 实现 openai.go（完整文件）**

```go
package protocol

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ---------- 公共形状 ----------

type anthxBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
}

type oaixToolCall struct {
	Index    int    `json:"index,omitempty"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ---------- 请求转换：Anthropic → OpenAI ----------

type anthxMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type anthxTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type anthxRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        json.RawMessage `json:"system"`
	Messages      []anthxMessage  `json:"messages"`
	Tools         []anthxTool     `json:"tools"`
	ToolChoice    json.RawMessage `json:"tool_choice"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	StopSequences []string        `json:"stop_sequences"`
	Stream        bool            `json:"stream"`
}

type oaixMessage struct {
	Role       string         `json:"role"`
	Content    *string        `json:"content,omitempty"`
	ToolCalls  []oaixToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type oaixRequest struct {
	Model       string          `json:"model"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	Messages    []oaixMessage   `json:"messages"`
	Tools       []oaixTool      `json:"tools,omitempty"`
	ToolChoice  json.RawMessage `json:"tool_choice,omitempty"`
	Temperature *float64        `json:"temperature,omitempty"`
	TopP        *float64        `json:"top_p,omitempty"`
	Stop        []string        `json:"stop,omitempty"`
	Stream      bool            `json:"stream,omitempty"`
}

type oaixTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// parseBlocks 解析 string | []block 两种 content 形状。
func parseBlocks(raw json.RawMessage) ([]anthxBlock, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []anthxBlock{{Type: "text", Text: s}}, true
	}
	var blocks []anthxBlock
	if json.Unmarshal(raw, &blocks) == nil {
		return blocks, false
	}
	return nil, false
}

// blocksText 把 string|[]block 归并为纯文本（tool_result content 用）。
func blocksText(raw json.RawMessage) string {
	blocks, _ := parseBlocks(raw)
	var parts []string
	for _, b := range blocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func systemText(raw json.RawMessage) string { return blocksText(raw) }

func convertToolChoice(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return nil
	}
	var tc struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &tc) != nil {
		return nil
	}
	switch tc.Type {
	case "auto":
		return json.RawMessage(`"auto"`)
	case "any":
		return json.RawMessage(`"required"`)
	case "tool":
		b, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]string{"name": tc.Name},
		})
		return b
	default:
		return nil
	}
}

// AnthropicRequestToOpenAI 转换请求体（spec §5.3.1）。dropped 是被丢弃的
// Anthropic 专有字段名（排序），调用方记一行日志。model 原样透传——调用方
// 已用 RewriteModel 重写为上游名。
func AnthropicRequestToOpenAI(body []byte) ([]byte, []string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
	}
	kept := map[string]bool{"model": true, "max_tokens": true, "system": true, "messages": true,
		"tools": true, "tool_choice": true, "temperature": true, "top_p": true,
		"stop_sequences": true, "stream": true}
	var dropped []string
	for k := range top {
		if !kept[k] {
			dropped = append(dropped, k)
		}
	}
	sort.Strings(dropped)

	var ar anthxRequest
	if err := json.Unmarshal(body, &ar); err != nil {
		return nil, nil, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
	}
	out := oaixRequest{Model: ar.Model, MaxTokens: ar.MaxTokens, Stream: ar.Stream,
		Temperature: ar.Temperature, TopP: ar.TopP, Stop: ar.StopSequences,
		ToolChoice: convertToolChoice(ar.ToolChoice)}
	if sys := systemText(ar.System); sys != "" {
		out.Messages = append(out.Messages, oaixMessage{Role: "system", Content: &sys})
	}
	for _, m := range ar.Messages {
		blocks, _ := parseBlocks(m.Content)
		var textParts []string
		var calls []oaixToolCall
		var toolMsgs []oaixMessage
		for _, b := range blocks {
			switch b.Type {
			case "text":
				if b.Text != "" {
					textParts = append(textParts, b.Text)
				}
			case "tool_use":
				args := string(b.Input)
				if args == "" {
					args = "{}"
				}
				tc := oaixToolCall{ID: b.ID, Type: "function"}
				tc.Function.Name, tc.Function.Arguments = b.Name, args
				calls = append(calls, tc)
			case "tool_result":
				txt := blocksText(b.Content)
				toolMsgs = append(toolMsgs, oaixMessage{Role: "tool", Content: &txt, ToolCallID: b.ToolUseID})
			}
		}
		if len(textParts) > 0 || len(calls) > 0 {
			var c *string
			if len(textParts) > 0 {
				j := strings.Join(textParts, "\n")
				c = &j
			}
			out.Messages = append(out.Messages, oaixMessage{Role: m.Role, Content: c, ToolCalls: calls})
		}
		out.Messages = append(out.Messages, toolMsgs...)
	}
	for _, tl := range ar.Tools {
		var t oaixTool
		t.Type = "function"
		t.Function.Name, t.Function.Description, t.Function.Parameters = tl.Name, tl.Description, tl.InputSchema
		out.Tools = append(out.Tools, t)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, nil, err
	}
	return b, dropped, nil
}

// ---------- 非流响应转换：OpenAI → Anthropic ----------

type oaixChoice struct {
	Message struct {
		Content   json.RawMessage `json:"content"`
		ToolCalls []oaixToolCall  `json:"tool_calls"`
	} `json:"message"`
	FinishReason string `json:"finish_reason"`
}

type oaixResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Choices []oaixChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type anthxOutBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type anthxResponse struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Role       string          `json:"role"`
	Model      string          `json:"model"`
	Content    []anthxOutBlock `json:"content"`
	StopReason string          `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func mapFinishReason(s string) string {
	switch s {
	case "length":
		return "max_tokens"
	case "tool_calls", "function_call":
		return "tool_use"
	default:
		return "end_turn"
	}
}

// OpenAIResponseToAnthropic 转换非流响应（spec §5.3.2）。空 choices（部分
// 聚合站的异常响应）防御性输出 end_turn 空消息，不报错。
func OpenAIResponseToAnthropic(body []byte) ([]byte, error) {
	var or oaixResponse
	if err := json.Unmarshal(body, &or); err != nil {
		return nil, fmt.Errorf("decode upstream body: %w", err)
	}
	resp := anthxResponse{ID: or.ID, Type: "message", Role: "assistant", Model: or.Model}
	resp.Usage.InputTokens = or.Usage.PromptTokens
	resp.Usage.OutputTokens = or.Usage.CompletionTokens
	if len(or.Choices) > 0 {
		ch := or.Choices[0]
		var s string
		if json.Unmarshal(ch.Message.Content, &s) == nil && s != "" {
			resp.Content = append(resp.Content, anthxOutBlock{Type: "text", Text: s})
		}
		for _, tc := range ch.Message.ToolCalls {
			input := map[string]any{}
			_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
			raw, _ := json.Marshal(input)
			resp.Content = append(resp.Content, anthxOutBlock{Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: raw})
		}
		resp.StopReason = mapFinishReason(ch.FinishReason)
	}
	if resp.StopReason == "" {
		resp.StopReason = "end_turn"
	}
	return json.Marshal(resp)
}

// ---------- 流式转换：OpenAI chunk SSE → Anthropic SSE ----------

type oaixChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index     int    `json:"index"`
				ID        string `json:"id"`
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type toolAcc struct {
	id, name string
	args     strings.Builder
}

// OpenAIStreamTransformer 把 OpenAI chunk 流逐步转换为 Anthropic SSE 字节。
// Feed 接受任意切分的上游字节（内部缓冲到完整 SSE 事件），返回可转发字节；
// Finish 收口（截断流按 end_turn）。非并发安全：单一 goroutine 驱动。
type OpenAIStreamTransformer struct {
	raw      []byte // 不完整事件的字节缓冲
	out      bytes.Buffer
	started  bool
	finished bool
	msgID    string
	model    string
	inTok    int
	outTok   int
	blockIdx int  // 下一个 content block 的 index
	textOpen bool // 当前有打开的 text 块
	toolIdx  int  // 当前打开的 tool 块下标；-1 无
	tools    map[int]*toolAcc
}

func NewOpenAIStreamTransformer() *OpenAIStreamTransformer {
	return &OpenAIStreamTransformer{toolIdx: -1, tools: map[int]*toolAcc{}}
}

func (t *OpenAIStreamTransformer) emit(event string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	t.out.WriteString("event: " + event + "\ndata: ")
	t.out.Write(b)
	t.out.WriteString("\n\n")
}

// startMessage 发 message_start（首个上游 chunk 触发；截断流由 Finish 兜底）。
func (t *OpenAIStreamTransformer) startMessage() {
	if t.started {
		return
	}
	t.started = true
	if t.msgID == "" {
		t.msgID = "msg_openai"
	}
	t.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": t.msgID, "type": "message", "role": "assistant", "model": t.model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": t.inTok, "output_tokens": 0},
		},
	})
}

func (t *OpenAIStreamTransformer) closeText() {
	if !t.textOpen {
		return
	}
	t.textOpen = false
	t.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": t.blockIdx - 1})
}

func (t *OpenAIStreamTransformer) closeTool() {
	if t.toolIdx < 0 {
		return
	}
	t.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": t.toolIdx})
	t.toolIdx = -1
}

// close 收口全部打开块并发 message_delta+message_stop；幂等。
func (t *OpenAIStreamTransformer) close(reason string) {
	if t.finished {
		return
	}
	t.finished = true
	t.closeText()
	t.closeTool()
	t.emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": reason, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": t.outTok},
	})
	t.emit("message_stop", map[string]any{"type": "message_stop"})
}

func (t *OpenAIStreamTransformer) openTool(idx int, acc *toolAcc) {
	t.closeText()
	t.emit("content_block_start", map[string]any{
		"type": "content_block_start", "index": t.blockIdx,
		"content_block": map[string]any{"type": "tool_use", "id": acc.id, "name": acc.name, "input": map[string]any{}},
	})
	t.toolIdx = t.blockIdx
	t.blockIdx++
}

// handleChunk 处理一个已解析的 OpenAI chunk（spec §5.3.3 的事件编排）。
func (t *OpenAIStreamTransformer) handleChunk(data []byte) {
	var c oaixChunk
	if json.Unmarshal(data, &c) != nil {
		return
	}
	if !t.started {
		t.msgID, t.model = c.ID, c.Model
		if t.msgID != "" {
			t.msgID = "msg_" + t.msgID
		}
		t.startMessage()
	}
	if c.Usage != nil {
		t.inTok, t.outTok = c.Usage.PromptTokens, c.Usage.CompletionTokens
	}
	if len(c.Choices) == 0 { // usage-only 尾块或心跳
		return
	}
	ch := c.Choices[0]
	d := ch.Delta
	if d.Content != "" {
		if !t.textOpen {
			t.closeTool()
			t.emit("content_block_start", map[string]any{
				"type": "content_block_start", "index": t.blockIdx,
				"content_block": map[string]any{"type": "text", "text": ""},
			})
			t.textOpen = true
			t.blockIdx++
		}
		t.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": t.blockIdx - 1,
			"delta": map[string]any{"type": "text_delta", "text": d.Content},
		})
	}
	for _, tc := range d.ToolCalls {
		acc := t.tools[tc.Index]
		if acc == nil {
			acc = &toolAcc{}
			t.tools[tc.Index] = acc
		}
		if tc.ID != "" {
			acc.id = tc.ID
		}
		if tc.Name != "" {
			acc.name = tc.Name
		}
		if tc.Arguments != "" {
			acc.args.WriteString(tc.Arguments)
		}
		if t.toolIdx < 0 {
			t.openTool(tc.Index, acc)
		} else {
			t.emit("content_block_delta", map[string]any{
				"type": "content_block_delta", "index": t.toolIdx,
				"delta": map[string]any{"type": "input_json_delta", "partial_json": tc.Arguments},
			})
		}
	}
	if ch.FinishReason != nil && *ch.FinishReason != "" {
		t.close(mapFinishReason(*ch.FinishReason))
	}
}

// handleEvent 处理一个完整 SSE 事件字节（取全部 data: 行，通常一行）。
func (t *OpenAIStreamTransformer) handleEvent(event []byte) {
	for _, line := range strings.Split(string(event), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[len("data:"):])
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			t.startMessage()
			t.close("end_turn")
			return
		}
		t.handleChunk([]byte(payload))
	}
}

// findEvent 定位完整 SSE 事件（\n\n 或 \r\n\r\n 结尾），返回事件字节与消耗长度。
func findEvent(raw []byte) (event []byte, consumed int, ok bool) {
	i1 := bytes.Index(raw, []byte("\n\n"))
	i2 := bytes.Index(raw, []byte("\r\n\r\n"))
	switch {
	case i1 >= 0 && (i2 < 0 || i1 < i2):
		return raw[:i1], i1 + 2, true
	case i2 >= 0:
		return raw[:i2], i2 + 4, true
	default:
		return nil, 0, false
	}
}

func (t *OpenAIStreamTransformer) Feed(p []byte) []byte {
	t.raw = append(t.raw, p...)
	for {
		event, consumed, ok := findEvent(t.raw)
		if !ok {
			break
		}
		t.handleEvent(event)
		t.raw = t.raw[consumed:]
	}
	return t.drain()
}

// Finish 收口：已 started 但缺 finish_reason → end_turn；完全空流 → 最小完整消息。
func (t *OpenAIStreamTransformer) Finish() []byte {
	t.startMessage()
	t.close("end_turn")
	out := t.drain()
	t.raw = nil
	return out
}

func (t *OpenAIStreamTransformer) drain() []byte {
	if t.out.Len() == 0 {
		return nil
	}
	out := make([]byte, t.out.Len())
	copy(out, t.out.Bytes())
	t.out.Reset()
	return out
}
```

实现说明（写给执行者）：`openTool` 里 `tc.Index` 参数仅用于 map 键，块 index 用 `t.blockIdx` 单调分配；`close` 幂等由 `t.finished` 保证，`[DONE]` 与 finish_reason 双到时只收一次。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/protocol -v`
Expected: 全 PASS（旧 anthropic_test 同绿）

- [ ] **Step 5: 整树验证 + Commit**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿

```bash
git add internal/protocol
git commit -m "feat(protocol): OpenAI↔Anthropic 转换器——请求/非流响应/流式 SSE 三路转换

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 4: provider 类型预设 + OpenAIAdapter + Probe 协议化

**Files:**
- Create: `internal/provider/presets.go`
- Create: `internal/provider/openai.go`
- Modify: `internal/provider/anthropic.go`（抽 `sendUpstream` 助手；`Probe` 加 protocol 参数）
- Modify: `internal/provider/provider.go`（加 `AdapterForProtocol`）
- Modify: `internal/bridge/bridge.go`（两处 `Probe` 调用传 `"anthropic"`）
- Test: `internal/provider/provider_test.go`（追加；旧用例不动，`Probe` 相关调用补 protocol 参数）

**Interfaces:**
- Consumes: Task 3 的三个转换函数；现有 `DoRequest/ChatResult/Adapter`（不改）。
- Produces:
  - `type Preset struct { ID, Label, Protocol, DefaultBaseURL, Description string }`（带 json tag）
  - `func Presets() []Preset`、`func LookupPreset(id string) (Preset, bool)`
  - `func AdapterForProtocol(p string, log *slog.Logger) (Adapter, error)`
  - `type OpenAIAdapter struct{...}`、`func NewOpenAIAdapter(log *slog.Logger) *OpenAIAdapter`
  - `func Probe(ctx context.Context, protocol, baseURL, apiKey string, allowPrivate bool) ProbeResult`（签名变更）

- [ ] **Step 1: 写失败测试（provider_test.go 追加）**

```go
func TestPresetsCatalogAndLookup(t *testing.T) {
	ps := Presets()
	if len(ps) != 4 {
		t.Fatalf("presets %d", len(ps))
	}
	p, ok := LookupPreset("openai-compatible")
	if !ok || p.Protocol != "openai" || p.DefaultBaseURL != "" {
		t.Fatalf("lookup: %+v %v", p, ok)
	}
	if _, ok := LookupPreset("nope"); ok {
		t.Fatal("unknown preset must miss")
	}
	// 每个 preset 的 protocol 合法（anthropic|openai）且 official 有默认 URL
	for _, p := range ps {
		if p.Protocol != "anthropic" && p.Protocol != "openai" {
			t.Fatalf("bad protocol %q", p.Protocol)
		}
		if strings.HasSuffix(p.ID, "-official") && p.DefaultBaseURL == "" {
			t.Fatalf("%s missing default url", p.ID)
		}
	}
}

func TestAdapterForProtocolDispatch(t *testing.T) {
	a, err := AdapterForProtocol("anthropic", nil)
	if err != nil || a == nil {
		t.Fatalf("anthropic: %v %v", a, err)
	}
	o, err := AdapterForProtocol("openai", nil)
	if err != nil || o == nil {
		t.Fatalf("openai: %v %v", o, err)
	}
	if _, err := AdapterForProtocol("grpc", nil); err == nil {
		t.Fatal("unknown protocol must error")
	}
}

func TestOpenAIAdapterNonStreamEndToEnd(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"cmpl-1","model":"up-1","choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`))
	}))
	defer up.Close()
	ad := NewOpenAIAdapter(nil)
	res, err := ad.Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "up-1",
		RawBody: []byte(`{"model":"up-1","max_tokens":8,"system":"sys","messages":[{"role":"user","content":"q"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if gotAuth != "Bearer sk-x" || gotPath != "/v1/chat/completions" {
		t.Fatalf("auth=%q path=%q", gotAuth, gotPath)
	}
	if gotBody["messages"] == nil {
		t.Fatalf("body not converted: %v", gotBody)
	}
	out, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(out), `"stop_reason":"end_turn"`) {
		t.Fatalf("response not converted: %s", out)
	}
}

func TestOpenAIAdapterStreamEndToEnd(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, s := range []string{
			"data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"}}]}\n\n",
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
			"data: [DONE]\n\n",
		} {
			w.Write([]byte(s))
			f.Flush()
		}
	}))
	defer up.Close()
	ad := NewOpenAIAdapter(nil)
	res, err := ad.Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "up-1", Stream: true,
		RawBody: []byte(`{"model":"up-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	for _, want := range []string{"message_start", "text_delta", "message_stop"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("stream missing %q:\n%s", want, b)
		}
	}
}

func TestOpenAIAdapter429Passthrough(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "9")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	}))
	defer up.Close()
	res, err := NewOpenAIAdapter(nil).Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "m",
		RawBody: []byte(`{"model":"m","max_tokens":1,"messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	// 4xx 正常返回（err==nil），状态码与 Retry-After 保留供分类与冷却
	if res.StatusCode != 429 || res.RetryAfterSec != 9 {
		t.Fatalf("%d %d", res.StatusCode, res.RetryAfterSec)
	}
}

func TestProbeBothProtocols(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("openai probe path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"x"},"finish_reason":"stop"}]}`))
	}))
	defer up.Close()
	if res := Probe(context.Background(), "openai", up.URL, "sk-x", true); !res.OK {
		t.Fatalf("openai probe: %+v", res)
	}
	if res := Probe(context.Background(), "anthropic", "http://127.0.0.1:1", "k", true); res.OK {
		t.Fatal("anthropic probe against dead port must fail")
	}
}
```

（需要的 import：`context`/`encoding/json`/`io`/`net/http`/`net/http/httptest`/`strings`/`testing`，按现文件已有者合并。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/provider -v`
Expected: FAIL（`Presets`/`NewOpenAIAdapter` 未定义；`Probe` 签名不符）

- [ ] **Step 3: 实现 presets.go**

```go
package provider

// Preset 是渠道类型预设（spec §4）：提供 protocol 推导与默认 BaseURL，
// BaseURL 可被显式覆盖（ValidateBaseURL 校验不变）。
type Preset struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Protocol       string `json:"protocol"`
	DefaultBaseURL string `json:"defaultBaseUrl"`
	Description    string `json:"description"`
}

// Presets 渠道类型目录（顺序即 UI 展示序）。
func Presets() []Preset {
	return []Preset{
		{ID: "anthropic-official", Label: "Anthropic 官方", Protocol: "anthropic",
			DefaultBaseURL: "https://api.anthropic.com", Description: "Anthropic 官方 API"},
		{ID: "anthropic-compatible", Label: "Anthropic 兼容中转", Protocol: "anthropic",
			DefaultBaseURL: "", Description: "第三方 Anthropic 协议代理/中转站，Base URL 必填"},
		{ID: "openai-official", Label: "OpenAI 官方", Protocol: "openai",
			DefaultBaseURL: "https://api.openai.com", Description: "OpenAI 官方 API"},
		{ID: "openai-compatible", Label: "OpenAI 兼容", Protocol: "openai",
			DefaultBaseURL: "", Description: "OpenRouter/聚合站/自建网关等 OpenAI 协议端点，Base URL 必填"},
	}
}

// LookupPreset 按 id 查预设。
func LookupPreset(id string) (Preset, bool) {
	for _, p := range Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
```

- [ ] **Step 4: anthropic.go 抽 sendUpstream 并改 Probe 签名**

在 `AnthropicAdapter.Do` 里，从 `resp, err := a.Client.Do(req)` 起到函数尾的响应归一化逻辑抽为：

```go
// sendUpstream 执行请求并归一化结果：3xx 拒绝为 error（CheckRedirect 返回
// ErrUseLastResponse 后 3xx 以"正常响应"浮出，必须显式拒绝且不跟随）；
// 4xx/5xx 限长读体后作为正常结果返回（分类交给调用方）；2xx 透传 Body。
func sendUpstream(ctx context.Context, client *http.Client, req *http.Request) (ChatResult, error) {
	resp, err := client.Do(req)
	if err != nil {
		return ChatResult{}, fmt.Errorf("connect: %w", err)
	}
	ra := 0
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil {
			ra = n
		}
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, RetryAfterSec: ra,
				Body: io.NopCloser(bytes.NewReader(snippet))},
			fmt.Errorf("redirect denied: upstream status %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, RetryAfterSec: ra,
			Body: io.NopCloser(bytes.NewReader(snippet))}, nil
	}
	return ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body, RetryAfterSec: ra}, nil
}
```

`AnthropicAdapter.Do` 尾部改为 `return sendUpstream(ctx, a.Client, req)`。

`Probe` 改签名并按协议选 adapter（openai 的最小探测体与 anthropic 同形，无需转换）：

```go
// Probe 分级探测：dns → connect → auth（发一条最小请求，只报告分级与脱敏错误）。
// protocol 决定探测端点与鉴权头（anthropic→/v1/messages，openai→/v1/chat/completions）。
func Probe(ctx context.Context, protocol, baseURL, apiKey string, allowPrivate bool) ProbeResult {
	if err := ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return ProbeResult{Stage: "validate", Error: err.Error()}
	}
	u, _ := url.Parse(baseURL)
	host := u.Hostname()
	if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil && !isLoopbackHost(host) {
		return ProbeResult{Stage: "dns", Error: fmt.Sprintf("resolve %s failed", host)}
	}
	body := []byte(`{"model":"__probe__","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`)
	var ad Adapter = NewAnthropicAdapter()
	if protocol == "openai" {
		ad = NewOpenAIAdapter(nil)
	}
	res, err := ad.Do(ctx, DoRequest{BaseURL: baseURL, AllowPrivate: allowPrivate, APIKey: apiKey, RawBody: body})
	if err != nil {
		return ProbeResult{Stage: "connect", Error: MaskErr(err)}
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4<<10))
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return ProbeResult{Stage: "auth", Error: fmt.Sprintf("upstream status %d", res.StatusCode)}
	}
	return ProbeResult{Stage: "auth", OK: true}
}
```

- [ ] **Step 5: provider.go 加 AdapterForProtocol**

```go
// AdapterForProtocol 按渠道协议分派 adapter（spec §5.1）；未知协议报错。
// log 仅 openai adapter 使用（丢弃字段的提醒行），可为 nil。
func AdapterForProtocol(p string, log *slog.Logger) (Adapter, error) {
	switch p {
	case "anthropic":
		return NewAnthropicAdapter(), nil
	case "openai":
		return NewOpenAIAdapter(log), nil
	default:
		return nil, fmt.Errorf("unknown protocol %q", p)
	}
}
```

（provider.go 需补 `log/slog`、`fmt` import。）

- [ ] **Step 6: 实现 openai.go adapter**

```go
package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"AgentSwitch/internal/protocol"
)

// respLimit 是 openai 非流响应转换前的整体读取上限。
const respLimit = 64 << 20

// OpenAIAdapter 把 Anthropic Messages 请求转换为 ChatCompletions 发送，并把
// 响应/流转换回 Anthropic 形状（spec §5.3）。4xx/5xx 错误体保持上游原样——
// gateway 的 upstreamErrorBody 会兜底包装成 Anthropic envelope。
type OpenAIAdapter struct {
	Client *http.Client
	Log    *slog.Logger
}

func NewOpenAIAdapter(log *slog.Logger) *OpenAIAdapter {
	return &OpenAIAdapter{Client: &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随：3xx 原样交给分类
		},
	}, Log: log}
}

func (a *OpenAIAdapter) Do(ctx context.Context, r DoRequest) (ChatResult, error) {
	if err := ValidateBaseURL(r.BaseURL, r.AllowPrivate); err != nil {
		return ChatResult{}, err
	}
	converted, dropped, err := protocol.AnthropicRequestToOpenAI(r.RawBody)
	if err != nil {
		return ChatResult{}, err
	}
	if len(dropped) > 0 && a.Log != nil {
		a.Log.Info("openai adapter dropped anthropic-only fields", "fields", strings.Join(dropped, ","))
	}
	endpoint := strings.TrimRight(r.BaseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(converted))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	if r.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	res, err := sendUpstream(ctx, a.Client, req)
	if err != nil || res.StatusCode >= 400 {
		return res, err
	}
	if !r.Stream {
		full, err := io.ReadAll(io.LimitReader(res.Body, respLimit))
		res.Body.Close()
		if err != nil {
			return ChatResult{}, fmt.Errorf("read upstream: %w", err)
		}
		out, err := protocol.OpenAIResponseToAnthropic(full)
		if err != nil {
			return ChatResult{}, fmt.Errorf("convert response: %w", err)
		}
		res.Body = io.NopCloser(bytes.NewReader(out))
		return res, nil
	}
	res.Body = a.streamBody(res.Body)
	return res, nil
}

// streamBody 把 OpenAI chunk 流泵过转换器变成 Anthropic SSE 流（逐事件 flush）。
func (a *OpenAIAdapter) streamBody(src io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()
	t := protocol.NewOpenAIStreamTransformer()
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, rerr := src.Read(buf)
			if n > 0 {
				if out := t.Feed(buf[:n]); len(out) > 0 {
					if _, werr := pw.Write(out); werr != nil {
						return
					}
				}
			}
			if rerr != nil {
				if out := t.Finish(); len(out) > 0 {
					_, _ = pw.Write(out)
				}
				pw.Close()
				return
			}
		}
	}()
	return pr
}
```

- [ ] **Step 7: 修 bridge 两处 Probe 调用**

`internal/bridge/bridge.go` 中 `Providers.Test` 尾行改为：

```go
	return provider.Probe(context15(), "anthropic", p.BaseURL, apiKey, p.AllowPrivateUpstream)
```

`Accounts.Test` 尾行改为：

```go
	return provider.Probe(context15(), "anthropic", p.BaseURL, string(key), p.AllowPrivateUpstream)
```

- [ ] **Step 8: 跑测试确认通过 + 整树验证**

Run: `go test ./internal/provider ./internal/bridge -v && go build ./... && go vet ./... && go test ./...`
Expected: 全绿

- [ ] **Step 9: Commit**

```bash
git add internal/provider internal/bridge
git commit -m "feat(provider): 渠道类型预设 + OpenAI 协议 adapter + Probe 协议化

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 5: 路由管线切换——PriorityRouter + app 候选装配 + gateway 全量重构（含故障转移循环）

本任务把数据面从「全局别名 + 单 provider + 单账号」整体切到「渠道级候选集 + 优先级路由 + 冷却故障转移」。改动面大但内聚：router 接口换形、gateway Deps 换形、app 装配重写、app/smoke/gateway 三处测试重写。结束时数据面已是 P0-B 终态。

**Files:**
- Modify: `internal/router/router.go`（重写：新类型 + PriorityRouter；删 Single）
- Modify: `internal/router/router_test.go`（重写）
- Modify: `internal/telemetry/ring.go`（RequestRecord 加 Channel 字段）
- Modify: `internal/app/app.go`（候选装配 + Deps 换形 + 适配器表 + Cooldowns 持有）
- Modify: `internal/gateway/server.go`（Deps 换形 + 路由注册不变）
- Modify: `internal/gateway/handlers.go`（handleMessages 换候选循环；handleModels/handleReadyz 换数据源）
- Delete: `internal/account/account.go`（包删除）
- Test: `internal/gateway/gateway_test.go`（重写：新 Deps fixture + happy path；边界用例 Task 6 追加）
- Test: `internal/app/app_test.go`、`internal/app/smoke_test.go`（fixture 换渠道）

**Interfaces:**
- Consumes: Task 1 storage 三 repo；Task 2 Cooldowns；Task 4 `AdapterForProtocol`。
- Produces:
  - `router.Candidate`（字段见 Step 1）、`router.FilteredCandidate`、`router.Decision{Model, Ordered, Filtered}`、`type Router interface { Resolve(model string, cands []Candidate) Decision }`、`type PriorityRouter struct{}`
  - `gateway.Deps`：`Token/Limits/Candidates(func(publicModel string) ([]router.Candidate, error))/Cooldowns(*router.Cooldowns，可 nil)/Cred/Router(router.Router)/AdapterFor(func(protocol string) (provider.Adapter, error))/ListPublicModels(func() []string)/HasUsableChannel(func() bool)/Log/Ring/OnRecord`
  - `telemetry.RequestRecord` 新增 `Channel string`
  - `app.App` 新增 `func (a *App) Cooldowns() *router.Cooldowns`（Task 7 bridge 用）

- [ ] **Step 1: 重写 router/router.go（删 Single 与旧类型）**

```go
// Package router 是路由决策层：纯函数，不持共享可变状态（冷却状态由 gateway
// 持有并以快照形式随候选传入，docx §6.3 可解释决策在这里产出）。
package router

import (
	"sort"
	"time"
)

// Candidate 是一个可尝试的「渠道×密钥×模型」三元组（gateway/app 从 storage
// join 装配；CoolingUntil/Reason 来自冷却快照）。
type Candidate struct {
	ChannelID, ChannelName string
	KeyID, KeyName         string
	SecretKey              string // 凭据引用（本体在 SecretStore）
	Protocol, BaseURL      string
	AllowPrivate           bool
	UpstreamModel          string
	ChannelPriority        int
	KeyPriority            int
	CoolingUntil           time.Time // 零值=未冷却
	CoolingReason          string
}

// FilteredCandidate 是被过滤掉的候选与原因（可解释记录，docx §6.3）。
type FilteredCandidate struct {
	Candidate Candidate
	Reason    string
}

// Decision 是一次路由决策：Ordered 为尝试顺序，Filtered 附带剔除原因。
type Decision struct {
	Model    string
	Ordered  []Candidate
	Filtered []FilteredCandidate
}

// Router 依公开模型从候选集产出有序尝试计划。
type Router interface {
	Resolve(model string, cands []Candidate) Decision
}

type PriorityRouter struct{}

// Resolve 剔除冷却中候选（记原因），其余按 渠道优先级→密钥优先级→KeyID 稳定全序。
func (PriorityRouter) Resolve(model string, cands []Candidate) Decision {
	d := Decision{Model: model}
	now := time.Now()
	for _, c := range cands {
		if !c.CoolingUntil.IsZero() && c.CoolingUntil.After(now) {
			d.Filtered = append(d.Filtered, FilteredCandidate{Candidate: c, Reason: "cooling:" + c.CoolingReason})
			continue
		}
		d.Ordered = append(d.Ordered, c)
	}
	sort.Slice(d.Ordered, func(i, j int) bool {
		a, b := d.Ordered[i], d.Ordered[j]
		if a.ChannelPriority != b.ChannelPriority {
			return a.ChannelPriority < b.ChannelPriority
		}
		if a.KeyPriority != b.KeyPriority {
			return a.KeyPriority < b.KeyPriority
		}
		return a.KeyID < b.KeyID
	})
	return d
}
```

- [ ] **Step 2: 重写 router/router_test.go**

```go
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
```

- [ ] **Step 3: telemetry 加 Channel 字段**

`internal/telemetry/ring.go` 的 `RequestRecord` 加一行（放在 Account 后）：

```go
	Channel       string // 渠道名（渠道化后 Account 语义 = 密钥名）
```

- [ ] **Step 4: app.go 装配重写**

逐点修改 `internal/app/app.go`：

a) import：删 `AgentSwitch/internal/account`；确认有 `router`。

b) `App` 结构体加两个字段（`gw` 声明之后）：

```go
	cooldowns *router.Cooldowns
	adapters  map[string]provider.Adapter
```

c) `Open` 中 `a := &App{...}` 字面量后（return 前）补：

```go
	a.cooldowns = router.NewCooldowns()
	a.adapters = map[string]provider.Adapter{
		"anthropic": provider.NewAnthropicAdapter(),
		"openai":    provider.NewOpenAIAdapter(logger),
	}
```

d) 删除 `usableAccounts`，新增三个装配方法（放 `currentToken` 之后）：

```go
// Cooldowns 暴露冷却存储（bridge PoolStatus 读取用）。
func (a *App) Cooldowns() *router.Cooldowns { return a.cooldowns }

// candidates 装配公开模型的候选集：enabled 渠道 × enabled 密钥 × enabled
// channel_models(public=X)（spec §6.1），并标注冷却快照。SQL 已按渠道/密钥
// 优先级有序；router 内再排序一次是双保险。
func (a *App) candidates(public string) ([]router.Candidate, error) {
	chans, err := a.store.Channels().List()
	if err != nil {
		return nil, err
	}
	cool := map[string]router.CooldownEntry{}
	for _, e := range a.cooldowns.Snapshot(time.Now()) {
		cool[e.KeyID] = e
	}
	var out []router.Candidate
	for _, ch := range chans {
		if !ch.Enabled {
			continue
		}
		models, err := a.store.ChannelModels().ListByChannel(ch.ID)
		if err != nil {
			return nil, err
		}
		upstream, found := "", false
		for _, m := range models {
			if m.Enabled && m.PublicModel == public {
				upstream, found = m.UpstreamModel, true
				break
			}
		}
		if !found {
			continue
		}
		keys, err := a.store.ChannelKeys().ListByChannel(ch.ID)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if !k.Enabled {
				continue
			}
			c := router.Candidate{ChannelID: ch.ID, ChannelName: ch.Name,
				KeyID: k.ID, KeyName: k.Name, SecretKey: k.SecretKey,
				Protocol: ch.Protocol, BaseURL: ch.BaseURL,
				AllowPrivate: ch.AllowPrivateUpstream, UpstreamModel: upstream,
				ChannelPriority: ch.Priority, KeyPriority: k.Priority}
			if e, ok := cool[k.ID]; ok {
				c.CoolingUntil, c.CoolingReason = e.Until, e.Reason
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func (a *App) publicModels() []string {
	ms, err := a.store.ChannelModels().PublicModels()
	if err != nil {
		return nil
	}
	return ms
}

func (a *App) hasUsableChannel() bool {
	ok, err := a.store.Channels().HasUsableKey()
	return err == nil && ok
}
```

e) `StartGateway` 中 `d := gateway.Deps{...}` 整块替换为：

```go
	d := gateway.Deps{
		Token: a.currentToken,
		Limits: func() gateway.Limits {
			s, _ := a.settings()
			return gateway.Limits{
				MaxBodyBytes: s.MaxBodyBytes, MaxConcurrent: s.MaxConcurrent,
				RequestTimeout: time.Duration(s.RequestTimeoutSeconds) * time.Second,
			}
		},
		Candidates: a.candidates,
		Cooldowns:  a.cooldowns,
		Cred:       &a.creds,
		Router:     router.PriorityRouter{},
		AdapterFor: func(protocol string) (provider.Adapter, error) {
			ad, ok := a.adapters[protocol]
			if !ok {
				return nil, fmt.Errorf("unknown protocol %q", protocol)
			}
			return ad, nil
		},
		ListPublicModels: a.publicModels,
		HasUsableChannel: a.hasUsableChannel,
		Log:              a.log,
		Ring:             a.ring,
		OnRecord: func(rec telemetry.RequestRecord) {
			a.mu.Lock()
			cbs := append([]func(telemetry.RequestRecord){}, a.onRecord...)
			a.mu.Unlock()
			for _, fn := range cbs {
				fn(rec)
			}
		},
	}
```

f) Degraded 判定换口径：`if len(a.usableAccounts()) == 0 {` → `if !a.hasUsableChannel() {`。

- [ ] **Step 5: gateway/server.go Deps 换形**

`Deps` 结构体整体替换：

```go
// Deps 是 Server 的全部外部依赖（渠道化后的 P0-B 形态，spec §6）。
type Deps struct {
	Token   func() string
	Limits  func() Limits
	// Candidates 返回公开模型的候选集（enabled 渠道×密钥×模型，已含冷却快照）。
	Candidates func(publicModel string) ([]router.Candidate, error)
	// Cooldowns 冷却存储；nil = 不接冷却（最小 fixture 的测试场景）。
	Cooldowns *router.Cooldowns
	Cred      credential.Store
	Router    router.Router
	// AdapterFor 按渠道协议分派出站 adapter（anthropic|openai）。
	AdapterFor       func(protocol string) (provider.Adapter, error)
	ListPublicModels func() []string
	HasUsableChannel func() bool
	Log              *slog.Logger
	Ring             *telemetry.Ring
	OnRecord         func(telemetry.RequestRecord)
}
```

import 删 `AgentSwitch/internal/account`。

- [ ] **Step 6: gateway/handlers.go 全量重构**

`handleModels` 替换为（去掉 Accounts 依赖）：

```go
func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, 401, protocol.ErrTypeAuth, "invalid gateway token")
		return
	}
	type model struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		DisplayName string `json:"display_name"`
	}
	data := []model{}
	for _, m := range s.deps.ListPublicModels() {
		data = append(data, model{ID: m, Type: "model", DisplayName: m})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false})
}
```

`handleReadyz` 的账号检查行替换：

```go
	if !s.deps.HasUsableChannel() || s.deps.Token() == "" {
```

（reason 文案改为 `"no usable channel/key or token"`。）

`handleMessages` 整函数替换为最终形态（P0-B 候选循环；不变量逐条落位）：

```go
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	rec := telemetry.RequestRecord{ID: telemetryID(), StartedAt: started}
	switches := 0
	defer func() {
		rec.DurationMs = time.Since(started).Milliseconds()
		rec.FailoverCount = switches
		// 请求日志行（I5/spec §10 审计）：只记脱敏元数据；零密钥、零请求体、零 Authorization。
		if s.deps.Log != nil {
			s.deps.Log.Info("request", "request_id", rec.ID, "model", rec.Model,
				"upstream_model", rec.UpstreamModel, "channel", rec.Channel,
				"account", rec.Account, "status", rec.Status,
				"duration_ms", rec.DurationMs, "error_class", rec.ErrorClass,
				"streaming", rec.Streaming, "switches", switches)
		}
		if s.deps.Ring != nil {
			s.deps.Ring.Add(rec)
		}
		if s.deps.OnRecord != nil {
			s.deps.OnRecord(rec)
		}
	}()

	if !s.authorized(r) {
		rec.Status, rec.ErrorClass = 401, "auth"
		writeErr(w, 401, protocol.ErrTypeAuth, "invalid gateway token")
		return
	}
	lim := s.deps.Limits()
	if lim.MaxConcurrent > 0 && s.sem != nil {
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		default:
			rec.Status, rec.ErrorClass = 503, "busy"
			writeErr(w, 503, protocol.ErrTypeAPI, "gateway busy, retry later")
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, lim.MaxBodyBytes+1))
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErr(w, 400, protocol.ErrTypeInvalidRequest, "read body failed")
		return
	}
	if int64(len(body)) > lim.MaxBodyBytes {
		rec.Status, rec.ErrorClass = 413, protocol.ErrTypeInvalidRequest
		writeErr(w, 413, protocol.ErrTypeInvalidRequest, "request body too large")
		return
	}
	// 安全顺序：必须先 Parse（字面 null body 会死在 "model is required"）。
	cr, err := protocol.ParseChatRequest(body)
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErr(w, 400, protocol.ErrTypeInvalidRequest, "invalid body: model and valid JSON required")
		return
	}
	rec.Model, rec.Streaming = cr.Model, cr.Stream

	cands, err := s.deps.Candidates(cr.Model)
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErr(w, 500, protocol.ErrTypeAPI, "candidates unavailable")
		return
	}
	if len(cands) == 0 {
		rec.Status, rec.ErrorClass = 404, protocol.ErrTypeNotFound
		writeErr(w, 404, protocol.ErrTypeNotFound,
			"unknown model "+cr.Model+"; models: "+strings.Join(s.deps.ListPublicModels(), ", "))
		return
	}
	decision := s.deps.Router.Resolve(cr.Model, cands)
	if len(decision.Ordered) == 0 {
		rec.Status, rec.ErrorClass = 503, "no_candidate"
		reasons := make([]string, 0, len(decision.Filtered))
		for _, f := range decision.Filtered {
			reasons = append(reasons, f.Candidate.ChannelName+"/"+f.Candidate.KeyName+":"+f.Reason)
		}
		writeErr(w, 503, protocol.ErrTypeAPI, "no usable candidate (filtered: "+strings.Join(reasons, "; ")+")")
		return
	}

	ctx := r.Context()
	if lim.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lim.RequestTimeout)
		defer cancel()
	}

	committed := false
	lastClass := provider.ClassUnknown
	for _, c := range decision.Ordered {
		if ctx.Err() != nil { // 客户端已走：不再发任何上游请求（docx §6.3 取消不转移）
			return
		}
		// 发送前二次校验（docx §7；TryAcquire 锁内原子，不写状态）
		if s.deps.Cooldowns != nil {
			if _, ok := s.deps.Cooldowns.TryAcquire(c.KeyID, time.Now()); !ok {
				switches++
				continue
			}
		}
		apiKey, err := s.deps.Cred.Get(c.SecretKey)
		if err != nil {
			rec.Status, rec.ErrorClass = 500, "credential"
			writeErr(w, 500, protocol.ErrTypeAPI, "credential unavailable for key "+c.KeyName)
			return
		}
		adapter, err := s.deps.AdapterFor(c.Protocol)
		if err != nil {
			rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
			writeErr(w, 500, protocol.ErrTypeAPI, "adapter unavailable: "+err.Error())
			return
		}
		rewritten, err := protocol.RewriteModel(body, c.UpstreamModel)
		if err != nil {
			rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
			writeErr(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
			return
		}
		res, err := adapter.Do(ctx, provider.DoRequest{
			BaseURL: c.BaseURL, AllowPrivate: c.AllowPrivate, APIKey: string(apiKey),
			UpstreamModel: c.UpstreamModel, RawBody: rewritten, Stream: cr.Stream,
		})
		if err != nil {
			// Do 错误（连接层/3xx/请求转换失败）：冷却后换下一候选——此刻未向
			// 客户端写过任何字节，属可转移窗口。
			lastClass = provider.ClassConnect
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.KeyID, provider.ClassConnect, 0, time.Now())
			}
			switches++
			continue
		}
		if res.StatusCode >= 400 {
			snippet, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
			res.Body.Close()
			class := provider.Classify(res.StatusCode, res.RetryAfterSec, snippet)
			if class == provider.ClassParam {
				// 400 类=客户端自身问题：不冷却、不转移，原样回传（docx §6.3）
				rec.Status, rec.ErrorClass = res.StatusCode, string(class)
				rec.Account, rec.Channel = c.KeyName, c.ChannelName
				writeUpstreamError(w, res.StatusCode, snippet, res.Header)
				return
			}
			lastClass = class
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.KeyID, class, res.RetryAfterSec, time.Now())
			}
			switches++
			continue
		}
		// 2xx：提交点——写响应头之后绝不转移（首字节后不重放，docx §6.3）
		committed = true
		rec.Status = res.StatusCode
		rec.UpstreamModel, rec.Account, rec.Channel = c.UpstreamModel, c.KeyName, c.ChannelName
		for _, h := range responseHeaderWhitelist {
			if v := res.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.StatusCode)
		if cr.Stream {
			copyStream(w, res.Body)
		} else {
			io.Copy(w, res.Body)
		}
		res.Body.Close()
		break
	}
	if !committed {
		rec.Status, rec.ErrorClass = 502, string(lastClass)
		writeErr(w, 502, protocol.ErrTypeAPI, "all candidates failed")
	}
}

// writeUpstreamError 透传白名单头 + 原样/包装的错误体（原 handleMessages 内
// 内联逻辑提取；upstreamErrorBody 语义不变）。
func writeUpstreamError(w http.ResponseWriter, status int, snippet []byte, header http.Header) {
	for _, h := range responseHeaderWhitelist {
		if v := header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(upstreamErrorBody(snippet, status))
}
```

（`copyStream`/`upstreamErrorBody`/`upstreamErrType`/`telemetryID`/`subtleConstantTimeCompare` 保留不动。）

- [ ] **Step 7: 删除 internal/account 包**

```bash
rm internal/account/account.go && rmdir internal/account
```

- [ ] **Step 8: 重写 gateway_test.go（新 fixture + happy path）**

```go
package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

// fakeUpstream 起一个 anthropic 形状的假上游，记录被调次数与最后请求体。
func fakeUpstream(t *testing.T, status int, body string, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		b, _ := io.ReadAll(r.Body)
		t.Logf("upstream got: %s", b)
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

// newGW 组装最小 Deps：一个 anthropic 渠道 + 一枚密钥；tweak 里加更多候选。
func newGW(t *testing.T, baseURL string, tweak func(d *Deps)) *Server {
	t.Helper()
	creds := credential.NewFallbackFile(t.TempDir() + "/s.json")
	_ = creds.Set("account:k1", []byte("sk-test"))
	cand := router.Candidate{ChannelID: "c1", ChannelName: "main", KeyID: "k1", KeyName: "key1",
		SecretKey: "account:k1", Protocol: "anthropic", BaseURL: baseURL,
		AllowPrivate: true, UpstreamModel: "up-1", ChannelPriority: 10, KeyPriority: 10}
	d := Deps{
		Token:            func() string { return "tok" },
		Limits:           func() Limits { return Limits{MaxBodyBytes: 1 << 20} },
		// 模型感知：pub-1 才有候选，其余公开名返回空（驱动 404 路径）
		Candidates: func(publicModel string) ([]router.Candidate, error) {
			if publicModel != "pub-1" {
				return nil, nil
			}
			return []router.Candidate{cand}, nil
		},
		Cred:             creds,
		Router:           router.PriorityRouter{},
		AdapterFor:       func(p string) (provider.Adapter, error) { return provider.AdapterForProtocol(p, nil) },
		ListPublicModels: func() []string { return []string{"pub-1"} },
		HasUsableChannel: func() bool { return true },
		Ring:             telemetry.NewRing(10),
	}
	if tweak != nil {
		tweak(&d)
	}
	return New(d)
}

func post(t *testing.T, s *Server, body, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w.Result()
}

func TestMessagesHappyPathRewritesModel(t *testing.T) {
	var calls int
	var lastModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		m := map[string]any{}
		json.Unmarshal(b, &m)
		lastModel, _ = m["model"].(string)
		w.Write([]byte(`{"id":"ok","type":"message"}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	if calls != 1 || lastModel != "up-1" {
		t.Fatalf("calls %d model %q", calls, lastModel)
	}
}

func TestUnknownModelListsPublic(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", nil)
	resp := post(t, s, `{"model":"nope","messages":[]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 || !strings.Contains(string(b), "pub-1") {
		t.Fatalf("404 must list models: %d %s", resp.StatusCode, b)
	}
}

func TestMessagesAuthRequired(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", nil)
	if resp := post(t, s, `{"model":"pub-1","messages":[]}`, "wrong"); resp.StatusCode != 401 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestModelsEndpointUnion(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", func(d *Deps) {
		d.ListPublicModels = func() []string { return []string{"a", "b"} }
	})
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var out struct {
		Data []struct{ ID string } `json:"data"`
	}
	json.NewDecoder(w.Result().Body).Decode(&out)
	if len(out.Data) != 2 || out.Data[1].ID != "b" {
		t.Fatalf("models: %+v", out)
	}
}

func TestReadyzDegradedWithoutUsableChannel(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", func(d *Deps) { d.HasUsableChannel = func() bool { return false } })
	req, _ := http.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Result().StatusCode != 503 {
		t.Fatalf("readyz status %d", w.Result().StatusCode)
	}
}
```



- [ ] **Step 9: 修 app_test.go / smoke_test.go fixture**

`app_test.go` 的 `newTestApp` 改为渠道 fixture（其余用例体不动）：

```go
func newTestApp(t *testing.T, upstreamURL string, withKey bool) *App {
	t.Helper()
	a, err := Open(Options{
		DBPath:  filepath.Join(t.TempDir(), "app.db"),
		LogPath: filepath.Join(t.TempDir(), "app.log"),
		Version: "test",
		Host:    "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	// 测试不得写真实 OS 钥匙环：Primary 换本地 0600 文件（AllowFallback 本已 true）。
	a.creds.Primary = credential.NewFallbackFile(filepath.Join(t.TempDir(), "secrets.json"))
	if err := a.Store().Channels().Save(storage.Channel{
		ID: "c1", Name: "up", Type: "anthropic-compatible", Protocol: "anthropic",
		BaseURL: upstreamURL, Priority: 10, Enabled: true, AllowPrivateUpstream: true,
	}); err != nil {
		t.Fatal(err)
	}
	if withKey {
		if err := a.Store().ChannelKeys().Save(storage.ChannelKey{
			ID: "k1", ChannelID: "c1", Name: "acc", SecretKey: "account:a1",
			SecretBackend: "fallback-file", Priority: 10, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		if err := a.Creds().Set("account:a1", []byte("sk-test")); err != nil {
			t.Fatal(err)
		}
	}
	return a
}
```

调用处全部 `newTestApp(t, up.URL, true/false)`——签名未变，零改动。`TestLifecycleDegradedAndRunning` 里 `a3.Store().Accounts().SetEnabled("a1", false)` 改为 `a3.Store().ChannelKeys().SetEnabled("k1", false)`；`credential.OSKeychain{}.Delete("account:a1")` 清理行可删（本地文件后端）。

`smoke_test.go`：`a.Store().Models().Save(storage.ModelMapping{...})` 改为

```go
	if err := a.Store().ChannelModels().Save(storage.ChannelModel{
		ID: "m1", ChannelID: "c1", PublicModel: "alias", UpstreamModel: "model-v1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
```

改别名段改为：

```go
	m, err := a.Store().ChannelModels().Get("m1")
	if err != nil {
		t.Fatal(err)
	}
	m.UpstreamModel = "model-v2"
	if err := a.Store().ChannelModels().Save(m); err != nil {
		t.Fatal(err)
	}
```

- [ ] **Step 10: 跑测试确认通过**

Run: `go build ./... && go vet ./... && go test ./internal/router ./internal/gateway ./internal/app -v`
Expected: 全 PASS（旧 Single/别名相关断言已随文件重写移除）

- [ ] **Step 11: 全树验证（含 -race）**

Run: `go vet ./... && go test -race ./...`
Expected: 全绿（desktop/bridge 旧页面仍在但只依赖未动的旧 bridge 服务与 Probe 已修）

- [ ] **Step 12: Commit**

```bash
git add -A
git commit -m "feat(gateway,app,router): 渠道路由管线与故障转移循环——候选集/冷却/提交点/取消语义

Single 路由与全局别名退役；数据面切到 enabled 渠道×密钥×渠道模型候选集，
PriorityRouter 排序、发送前 TryAcquire 二次校验、首字节提交点后不转移、
400 不转移不冷却、客户端取消即停。internal/account 包删除。

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 6: 数据面边界测试补全（转移不变量逐条 pin）

**Files:**
- Test: `internal/gateway/gateway_test.go`（追加用例）

**Interfaces:**
- Consumes: Task 5 的全部产出（只用公开行为，不改实现）。
- Produces: docx §13 必测边界的落位测试。若任一用例暴露实现缺陷，修 `internal/gateway/handlers.go` 使其通过（改动仅限缺陷修复，语义不变）。

- [ ] **Step 1: 追加多候选 fixture 与边界用例**

```go
// twoCands 构造两个候选共享密钥 store：key1→upstream1、key2→upstream2。
// 返回 (Deps tweak, 共享 Cooldowns)——冷却断言用同一实例。
func twoCands(t *testing.T, up1, up2 string) (func(d *Deps), *router.Cooldowns) {
	creds := credential.NewFallbackFile(t.TempDir() + "/s.json")
	_ = creds.Set("account:k1", []byte("sk-1"))
	_ = creds.Set("account:k2", []byte("sk-2"))
	cds := router.NewCooldowns()
	return func(d *Deps) {
		d.Cred = creds
		c1 := router.Candidate{ChannelID: "c1", ChannelName: "ch1", KeyID: "k1", KeyName: "key1",
			SecretKey: "account:k1", Protocol: "anthropic", BaseURL: up1, AllowPrivate: true,
			UpstreamModel: "u1", ChannelPriority: 10, KeyPriority: 10}
		c2 := router.Candidate{ChannelID: "c2", ChannelName: "ch2", KeyID: "k2", KeyName: "key2",
			SecretKey: "account:k2", Protocol: "anthropic", BaseURL: up2, AllowPrivate: true,
			UpstreamModel: "u2", ChannelPriority: 20, KeyPriority: 10}
		d.Candidates = func(string) ([]router.Candidate, error) { return []router.Candidate{c1, c2}, nil }
		d.Cooldowns = cds
	}, cds
}

// Review Focus 1 + docx §13：首候选连接失败 → 冷却 connect → 次候选服务，200。
func TestFailoverSkipsBadChannel(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	dead := "http://127.0.0.1:1" // 必然连接失败
	tw, _ := twoCands(t, dead, good.URL)
	s := newGW(t, dead, tw)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 || calls2 != 1 {
		t.Fatalf("failover: status %d calls2 %d", resp.StatusCode, calls2)
	}
}

// docx §13：429 + Retry-After → 冷却 rate_limited → 次候选服务。
func Test429SwitchesAndMarksCooldown(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	busy := fakeUpstream(t, 429, `{"type":"error"}`, nil)
	defer busy.Close()
	tw, cds := twoCands(t, busy.URL, good.URL)
	s := newGW(t, busy.URL, tw)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	// key1 已被冷却（429 无 Retry-After → 默认 30s）；10s 后仍未解冻
	if e, ok := cds.TryAcquire("k1", time.Now().Add(10*time.Second)); !ok {
		t.Fatalf("k1 must be cooling: %+v", e)
	}
}

// docx §13：400 参数错误 → 不转移、不冷却，客户端拿到 400。
func Test400NoSwitchNoCooldown(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	bad := fakeUpstream(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`, nil)
	defer bad.Close()
	tw, cds := twoCands(t, bad.URL, good.URL)
	s := newGW(t, bad.URL, tw)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[]}`, "tok")
	defer resp.Body.Close()
	if _, cooled := cds.TryAcquire("k1", time.Now()); cooled {
		t.Fatal("400 must not cool the key")
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "invalid_request_error") {
		t.Fatalf("body %s", b)
	}
	if calls2 != 0 {
		t.Fatal("400 must not switch")
	}
}
```

- [ ] **Step 2: 追加首字节后不重放 / 客户端取消 / 并发 429 用例**

```go
// docx §13：流式首字节已写 → 上游中断只断流，绝不换候选重放。
func TestNoSwitchAfterFirstByte(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200) // 响应头已写=提交点已过
		w.(http.Flusher).Flush()
		w.Write([]byte(`{"partial":`))
		panic(http.ErrAbortHandler) // 模拟流中断
	}))
	defer flaky.Close()
	tw, _ := twoCands(t, flaky.URL, good.URL)
	s := newGW(t, flaky.URL, tw)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if calls2 != 0 {
		t.Fatalf("after commit must not switch, calls2=%d", calls2)
	}
}

// docx §13：客户端取消 → 取消上游、不转移。
func TestClientCancelNoSwitch(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // 挂住直到网关取消
	}))
	defer slow.Close()
	tw, _ := twoCands(t, slow.URL, good.URL)
	s := newGW(t, slow.URL, tw)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", "/v1/messages", strings.NewReader(`{"model":"pub-1","max_tokens":8,"messages":[]}`))
	req.Header.Set("Authorization", "Bearer tok")
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req) // 客户端已断，写响应无害
	if calls2 != 0 {
		t.Fatalf("cancel must not switch, calls2=%d", calls2)
	}
}

// docx §13 + -race：并发 429 下同一密钥冷却原子、其余候选接管。
func TestConcurrent429CooldownAtomic(t *testing.T) {
	var mu sync.Mutex
	calls2 := 0
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls2++
		mu.Unlock()
		w.Write([]byte(`{"id":"ok"}`))
	}))
	defer good.Close()
	busy := fakeUpstream(t, 429, `{"type":"error"}`, nil)
	defer busy.Close()
	tw, cds := twoCands(t, busy.URL, good.URL)
	s := newGW(t, busy.URL, tw)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[]}`, "tok")
			resp.Body.Close()
		}()
	}
	wg.Wait()
	// 首轮 429 后 k1 冷却：此后请求应全部直达 good（不再打 busy 由
	// Candidates 快照过滤保证——app 层职责，网关层断言冷却标记成立即可）。
	if _, ok := cds.TryAcquire("k1", time.Now().Add(time.Hour)); ok {
		t.Fatal("k1 must be cooling after concurrent 429s")
	}
	if calls2 == 0 {
		t.Fatal("good upstream must have served")
	}
}
```

// spec §10：OpenAI 协议候选端到端——Anthropic 入站 → 转换 → OpenAI 假上游
// （ChatCompletions）→ 逆转换 → 客户端收到 Anthropic envelope。
func TestOpenAIProtocolCandidateEndToEnd(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"cmpl-1","model":"u-oai","choices":[{"message":{"content":"hello from openai"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":3}}`))
	}))
	defer up.Close()
	creds := credential.NewFallbackFile(t.TempDir() + "/s.json")
	_ = creds.Set("account:ko", []byte("sk-oai"))
	cand := router.Candidate{ChannelID: "co", ChannelName: "oai", KeyID: "ko", KeyName: "key-oai",
		SecretKey: "account:ko", Protocol: "openai", BaseURL: up.URL, AllowPrivate: true,
		UpstreamModel: "u-oai", ChannelPriority: 10, KeyPriority: 10}
	s := newGW(t, up.URL, func(d *Deps) {
		d.Cred = creds
		d.Candidates = func(string) ([]router.Candidate, error) { return []router.Candidate{cand}, nil }
	})
	resp := post(t, s, `{"model":"pub-1","max_tokens":16,"system":"sys","messages":[{"role":"user","content":"q"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	if !strings.Contains(string(b), `"stop_reason":"end_turn"`) || !strings.Contains(string(b), "hello from openai") {
		t.Fatalf("anthropic envelope expected: %s", b)
	}
	if gotAuth != "Bearer sk-oai" {
		t.Fatalf("bearer auth: %q", gotAuth)
	}
	// system 已转换
	if gotBody["messages"] == nil {
		t.Fatalf("converted body: %v", gotBody)
	}
}

（import 补：`context`、`encoding/json`、`sync`、`time`。）

- [ ] **Step 3: 跑边界测试（-race 必跑）**

Run: `go test -race ./internal/gateway -v`
Expected: 全 PASS；若有失败，修 handlers.go 对应缺陷后复跑至绿

- [ ] **Step 4: 全树验证 + Commit**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: 全绿

```bash
git add internal/gateway
git commit -m "test(gateway): P0-B 转移边界落位——坏渠道跳过/首字节不重放/取消不转移/400 直返/并发429冷却

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 7: bridge Channels 服务 + Models 聚合 + PoolStatus

**Files:**
- Create: `internal/bridge/channels.go`
- Test: `internal/bridge/channels_test.go`

**Interfaces:**
- Consumes: storage 三 repo、`provider.Presets/LookupPreset/Probe/ValidateBaseURL`、`app.App.Cooldowns()`、现有 `credential` 机制与 `tail4`/`pickBackend`（沿用 bridge.go 内既有实现）。
- Produces（desktop Task 8/9 依赖的精确签名）:
  - `type Channels struct { A *app.App; Creds credential.Store; KeychainAvailable func() bool }`
  - `type ChannelView struct { ID, Name, Type, Protocol, BaseURL string; Priority int; Enabled, AllowPrivateUpstream bool; KeyCount, ModelCount, CoolingCount int }`
  - `type KeyView struct { ID, ChannelID, Name, KeyTail, SecretBackend string; Priority int; Enabled bool; Cooling bool; CoolingRemainSec int; CoolingReason string }`
  - `type ChannelModelRow struct { ID, ChannelID, PublicModel, UpstreamModel string; Enabled bool }`
  - `type ChannelDetail struct { Channel ChannelView; Keys []KeyView; Models []ChannelModelRow }`
  - 方法：`List() []ChannelView`、`Get(id) (ChannelDetail, error)`、`Create(typeID, name, baseURL string, priority int, enabled, allowPrivate bool) (ChannelView, error)`、`Update(id, name, baseURL string, priority int, enabled, allowPrivate bool) error`、`Remove(id) error`、`SetEnabled(id string, on bool) error`、`Test(id string) provider.ProbeResult`、`FetchModels(id string) ([]string, error)`、`AddKeys(channelID, name, keysText string, allowFallback bool) (int, error)`、`SetKeyEnabled(id string, on bool) error`、`RemoveKey(id string) error`、`TestKey(id string) provider.ProbeResult`、`AddModel(channelID, public, upstream string) (ChannelModelRow, error)`、`UpdateModel(id, public, upstream string) error`、`SetModelEnabled(id string, on bool) error`、`RemoveModel(id string) error`
  - `type ModelsAgg struct { A *app.App }`、`type ModelAggregateView struct { ID, ChannelID, ChannelName, PublicModel, UpstreamModel string; Enabled bool }`、`(ModelsAgg) ListAll() []ModelAggregateView`、`(ModelsAgg) SetEnabled(id string, on bool) error`
  - `Gateway.PoolStatus() []PoolEntry`、`type PoolEntry struct { ChannelID, ChannelName, KeyID, KeyName string; CoolingUntilMs int64; RemainingSec int; Reason string }`

- [ ] **Step 1: 写失败测试（channels_test.go，完整文件）**

```go
package bridge

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"AgentSwitch/internal/credential"
)

func channelsSvc(t *testing.T) (Channels, *credential.Manager) {
	t.Helper()
	a := tempApp(t)
	creds := &credential.Manager{
		Primary:       credential.NewFallbackFile(filepath.Join(t.TempDir(), "s.json")),
		AllowFallback: true,
	}
	return Channels{A: a, Creds: creds, KeychainAvailable: func() bool { return false }}, creds
}

func TestChannelsCreateValidatesPresetAndURL(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, err := cs.Create("anthropic-official", "official", "", 10, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if c.Protocol != "anthropic" || c.BaseURL != "https://api.anthropic.com" {
		t.Fatalf("preset default: %+v", c)
	}
	// 兼容类无默认 URL：空 BaseURL 报中文错
	if _, err := cs.Create("openai-compatible", "agg", "", 10, true, false); err == nil ||
		!strings.Contains(err.Error(), "Base URL") {
		t.Fatalf("empty baseURL: %v", err)
	}
	// 未知类型
	if _, err := cs.Create("nope", "x", "https://x.example.com", 10, true, false); err == nil {
		t.Fatal("unknown type must error")
	}
	// 非回环 http 拒绝（ValidateBaseURL 沿用）
	if _, err := cs.Create("anthropic-compatible", "x", "http://relay.example.com", 10, true, false); err == nil {
		t.Fatal("non-loopback http must be rejected")
	}
	_ = c
}

func TestChannelsUpdateListRemove(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "main", "https://r.example.com", 10, true, false)
	if _, err := cs.AddKeys(c.ID, "k", "sk-abc12345\n", true); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AddModel(c.ID, "pub-1", "up-1"); err != nil {
		t.Fatal(err)
	}
	if err := cs.Update(c.ID, "main2", "https://r2.example.com", 5, false, false); err != nil {
		t.Fatal(err)
	}
	list := cs.List()
	if len(list) != 1 || list[0].Name != "main2" || list[0].Priority != 5 || list[0].Enabled {
		t.Fatalf("list %+v", list)
	}
	if list[0].KeyCount != 1 || list[0].ModelCount != 1 {
		t.Fatalf("counts: %+v", list[0])
	}
	d, err := cs.Get(c.ID)
	if err != nil || len(d.Keys) != 1 || len(d.Models) != 1 {
		t.Fatalf("detail %+v %v", d, err)
	}
	// Remove 级联清密钥本体
	if err := cs.Remove(c.ID); err != nil {
		t.Fatal(err)
	}
	if len(cs.List()) != 0 {
		t.Fatal("remove failed")
	}
}

func TestChannelsAddKeysBatchAndBlankLines(t *testing.T) {
	cs, creds := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "m", "https://r.example.com", 10, true, false)
	// Review Focus 4：空行与首尾空白容忍
	n, err := cs.AddKeys(c.ID, "pool", "  sk-aaa11111  \n\nsk-bbb22222\n\n", true)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	d, _ := cs.Get(c.ID)
	if len(d.Keys) != 2 || d.Keys[0].KeyTail != "1111" || d.Keys[0].SecretBackend != "fallback-file" {
		t.Fatalf("keys %+v", d.Keys)
	}
	_ = creds // 密钥本体存在性由 Remove/RemoveKey 的级联清密钥路径覆盖
}

func TestChannelsRemoveKeyDeletesSecretAndCascade(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "m", "https://r.example.com", 10, true, false)
	if _, err := cs.AddKeys(c.ID, "k", "sk-abc12345", true); err != nil {
		t.Fatal(err)
	}
	d, _ := cs.Get(c.ID)
	if err := cs.RemoveKey(d.Keys[0].ID); err != nil {
		t.Fatal(err)
	}
	d, _ = cs.Get(c.ID)
	if len(d.Keys) != 0 {
		t.Fatal("key not removed")
	}
	// 删渠道级联删 key 行；密钥本体清理在 Remove 内逐个 Delete（不报错即过）
	if _, err := cs.AddKeys(c.ID, "k", "sk-def67890", true); err != nil {
		t.Fatal(err)
	}
	if err := cs.Remove(c.ID); err != nil {
		t.Fatal(err)
	}
}

// Review Focus 2：重复公开模型 → 中文友好错误。
func TestChannelsAddModelDuplicateFriendlyError(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "m", "https://r.example.com", 10, true, false)
	if _, err := cs.AddModel(c.ID, "pub", "up"); err != nil {
		t.Fatal(err)
	}
	_, err := cs.AddModel(c.ID, "pub", "up2")
	if err == nil || !strings.Contains(err.Error(), "已存在") {
		t.Fatalf("want friendly duplicate error, got %v", err)
	}
	// 跨渠道同公开名允许
	c2, _ := cs.Create("openai-compatible", "o", "https://o.example.com", 20, true, false)
	if _, err := cs.AddModel(c2.ID, "pub", "up-openai"); err != nil {
		t.Fatalf("cross-channel duplicate must be allowed: %v", err)
	}
}

func TestChannelsTestAndTestKey(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("probe path %q", r.URL.Path)
		}
		w.Write([]byte(`{}`))
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("anthropic-compatible", "m", up.URL, 10, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AddKeys(c.ID, "k", "sk-abc12345", true); err != nil {
		t.Fatal(err)
	}
	if res := cs.Test(c.ID); !res.OK {
		t.Fatalf("channel test: %+v", res)
	}
	d, _ := cs.Get(c.ID)
	if res := cs.TestKey(d.Keys[0].ID); !res.OK {
		t.Fatalf("key test: %+v", res)
	}
}

func TestChannelsFetchModelsBothProtocols(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") == "" && r.Header.Get("X-Api-Key") == "" {
			t.Error("no auth header")
		}
		w.Write([]byte(`{"data":[{"id":"m-a"},{"id":"m-b"}]}`))
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "m", up.URL, 10, true, false)
	if _, err := cs.AddKeys(c.ID, "k", "sk-abc12345", true); err != nil {
		t.Fatal(err)
	}
	ids, err := cs.FetchModels(c.ID)
	if err != nil || len(ids) != 2 || ids[1] != "m-b" {
		t.Fatalf("fetch: %v %v", ids, err)
	}
	// 无密钥渠道报错
	c2, _ := cs.Create("openai-compatible", "o", up.URL, 20, true, false)
	if _, err := cs.FetchModels(c2.ID); err == nil {
		t.Fatal("fetch without key must error")
	}
}

func TestChannelsPoolStatusAndCooldownKeyView(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, _ := cs.Create("anthropic-compatible", "m", "https://r.example.com", 10, true, false)
	if _, err := cs.AddKeys(c.ID, "k", "sk-abc12345", true); err != nil {
		t.Fatal(err)
	}
	// 直接冷却 k1
	d, _ := cs.Get(c.ID)
	cs.A.Cooldowns().MarkClassified(d.Keys[0].ID, "rate_limited", 30, time.Now())
	d, _ = cs.Get(c.ID)
	if !d.Keys[0].Cooling || d.Keys[0].CoolingRemainSec <= 0 || d.Keys[0].CoolingRemainSec > 30 {
		t.Fatalf("key cooling view: %+v", d.Keys[0])
	}
	if d.Channel.CoolingCount != 1 {
		t.Fatalf("channel cooling count: %+v", d.Channel)
	}
	ps := Gateway{A: cs.A}.PoolStatus()
	if len(ps) != 1 || ps[0].KeyName != "k" || ps[0].RemainingSec <= 0 {
		t.Fatalf("pool: %+v", ps)
	}
	// Review Focus 5：剩余秒恒为正（coolingMap +1 钳制），过期条目由 Snapshot 过滤
	for _, e := range ps {
		if e.RemainingSec < 0 {
			t.Fatalf("negative remaining: %+v", e)
		}
	}
}

func TestModelsAggJoinAndToggle(t *testing.T) {
	cs, _ := channelsSvc(t)
	c1, _ := cs.Create("anthropic-compatible", "a", "https://a.example.com", 10, true, false)
	c2, _ := cs.Create("openai-compatible", "b", "https://b.example.com", 20, true, false)
	cs.AddModel(c1.ID, "pub", "up-a")
	cs.AddModel(c2.ID, "pub", "up-b")
	agg := ModelsAgg{A: cs.A}
	rows := agg.ListAll()
	if len(rows) != 2 || rows[0].ChannelName != "a" || rows[1].UpstreamModel != "up-b" {
		t.Fatalf("agg rows %+v", rows)
	}
	if err := agg.SetEnabled(rows[0].ID, false); err != nil {
		t.Fatal(err)
	}
	for _, r := range agg.ListAll() {
		if r.ID == rows[0].ID && r.Enabled {
			t.Fatal("toggle failed")
		}
	}
	_ = c1
	_ = c2
}
```

（写文件时清理三处占位：`ID2Ref`、`A2GatewayPoolStatus`、测试尾部的 `_ =` 垃圾行——真实写法：`PoolStatus` 挂在 `Gateway` 服务上，用例里 `Gateway{A: cs.A}.PoolStatus()`；尾4断言 `KeyTail`；删除无用 import。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -v`
Expected: FAIL（`Channels` 等未定义）

- [ ] **Step 3: 实现 channels.go（完整）**

```go
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/storage"
)

// Channels 是渠道聚合根管理面（spec §7）：渠道字段 + 密钥子表 + 模型子表。
type Channels struct {
	A                 *app.App
	Creds             credential.Store
	KeychainAvailable func() bool
}

func (s Channels) creds() credential.Store {
	if s.Creds != nil {
		return s.Creds
	}
	return s.A.Creds()
}

func (s Channels) keychainAvailable() bool {
	if s.KeychainAvailable != nil {
		return s.KeychainAvailable()
	}
	return credential.OSKeychainAvailable()
}

type ChannelView struct {
	ID, Name, Type, Protocol, BaseURL string
	Priority                          int
	Enabled, AllowPrivateUpstream     bool
	KeyCount, ModelCount, CoolingCount int
}

type KeyView struct {
	ID, ChannelID, Name, KeyTail, SecretBackend string
	Priority                                    int
	Enabled                                     bool
	Cooling                                     bool
	CoolingRemainSec                            int
	CoolingReason                               string
}

type ChannelModelRow struct {
	ID, ChannelID, PublicModel, UpstreamModel string
	Enabled                                   bool
}

type ChannelDetail struct {
	Channel ChannelView
	Keys    []KeyView
	Models  []ChannelModelRow
}

// coolingMap 取当前冷却快照（key id → 剩余秒）。
func (s Channels) coolingMap() map[string]int {
	out := map[string]int{}
	for _, e := range s.A.Cooldowns().Snapshot(time.Now()) {
		out[e.KeyID] = int(e.Until.Sub(time.Now()).Seconds()) + 1 // 钳为正（Review Focus 5）
	}
	return out
}

func (s Channels) view(c storage.Channel) ChannelView {
	v := ChannelView{ID: c.ID, Name: c.Name, Type: c.Type, Protocol: c.Protocol,
		BaseURL: c.BaseURL, Priority: c.Priority, Enabled: c.Enabled,
		AllowPrivateUpstream: c.AllowPrivateUpstream}
	v.KeyCount, _ = s.A.Store().ChannelKeys().CountByChannel(c.ID)
	v.ModelCount, _ = s.A.Store().ChannelModels().CountByChannel(c.ID)
	cool := s.coolingMap()
	keys, _ := s.A.Store().ChannelKeys().ListByChannel(c.ID)
	for _, k := range keys {
		if _, ok := cool[k.ID]; ok {
			v.CoolingCount++
		}
	}
	return v
}

func (s Channels) List() []ChannelView {
	cs, err := s.A.Store().Channels().List()
	if err != nil {
		return nil
	}
	out := make([]ChannelView, 0, len(cs))
	for _, c := range cs {
		out = append(out, s.view(c))
	}
	return out
}

func (s Channels) Get(id string) (ChannelDetail, error) {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return ChannelDetail{}, err
	}
	d := ChannelDetail{Channel: s.view(c)}
	cool := s.coolingMap()
	keys, _ := s.A.Store().ChannelKeys().ListByChannel(id)
	for _, k := range keys {
		kv := KeyView{ID: k.ID, ChannelID: k.ChannelID, Name: k.Name, KeyTail: k.KeyTail,
			SecretBackend: k.SecretBackend, Priority: k.Priority, Enabled: k.Enabled}
		if sec, ok := cool[k.ID]; ok {
			kv.Cooling, kv.CoolingRemainSec = true, sec
			for _, e := range s.A.Cooldowns().Snapshot(time.Now()) {
				if e.KeyID == k.ID {
					kv.CoolingReason = e.Reason
				}
			}
		}
		d.Keys = append(d.Keys, kv)
	}
	models, _ := s.A.Store().ChannelModels().ListByChannel(id)
	for _, m := range models {
		d.Models = append(d.Models, ChannelModelRow{ID: m.ID, ChannelID: m.ChannelID,
			PublicModel: m.PublicModel, UpstreamModel: m.UpstreamModel, Enabled: m.Enabled})
	}
	return d, nil
}

func (s Channels) Create(typeID, name, baseURL string, priority int, enabled, allowPrivate bool) (ChannelView, error) {
	preset, ok := provider.LookupPreset(typeID)
	if !ok {
		return ChannelView{}, fmt.Errorf("未知渠道类型: %s", typeID)
	}
	if baseURL == "" {
		baseURL = preset.DefaultBaseURL
	}
	if baseURL == "" {
		return ChannelView{}, fmt.Errorf("该类型需要填写 Base URL")
	}
	if err := provider.ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return ChannelView{}, err
	}
	c := storage.Channel{ID: storage.NewID(), Name: name, Type: preset.ID,
		Protocol: preset.Protocol, BaseURL: baseURL, Priority: priority,
		Enabled: enabled, AllowPrivateUpstream: allowPrivate}
	if err := s.A.Store().Channels().Save(c); err != nil {
		return ChannelView{}, err
	}
	s.A.Log().Info("channel created", "id", c.ID, "type", preset.ID, "actor", "ui", "result", "ok")
	return s.view(c), nil
}

func (s Channels) Update(id, name, baseURL string, priority int, enabled, allowPrivate bool) error {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return err
	}
	if err := provider.ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return err
	}
	c.Name, c.BaseURL, c.Priority, c.Enabled, c.AllowPrivateUpstream = name, baseURL, priority, enabled, allowPrivate
	if err := s.A.Store().Channels().Save(c); err != nil {
		return err
	}
	s.A.Log().Info("channel updated", "id", id, "actor", "ui", "result", "ok")
	return nil
}

// Remove 删渠道并级联清理密钥本体（channel_keys 行由 FK CASCADE）。
func (s Channels) Remove(id string) error {
	keys, _ := s.A.Store().ChannelKeys().ListByChannel(id)
	for _, k := range keys {
		s.creds().Delete(k.SecretKey)
	}
	if err := s.A.Store().Channels().Delete(id); err != nil {
		return err
	}
	s.A.Log().Info("channel removed", "id", id, "actor", "ui", "result", "ok")
	return nil
}

func (s Channels) SetEnabled(id string, on bool) error {
	err := s.A.Store().Channels().SetEnabled(id, on)
	if err == nil {
		s.A.Log().Info("channel enabled", "id", id, "on", on, "actor", "ui", "result", "ok")
	}
	return err
}

func (s Channels) Test(id string) provider.ProbeResult {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	apiKey := ""
	keys, _ := s.A.Store().ChannelKeys().ListByChannel(id)
	for _, k := range keys {
		if k.Enabled {
			if v, err := s.creds().Get(k.SecretKey); err == nil {
				apiKey = string(v)
			}
			break
		}
	}
	return provider.Probe(context15(), c.Protocol, c.BaseURL, apiKey, c.AllowPrivateUpstream)
}

// FetchModels 拉上游 GET /v1/models（两种协议同形，鉴权头按协议区分）。
func (s Channels) FetchModels(id string) ([]string, error) {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return nil, err
	}
	key := ""
	keys, _ := s.A.Store().ChannelKeys().ListByChannel(id)
	for _, k := range keys {
		if k.Enabled {
			if v, gerr := s.creds().Get(k.SecretKey); gerr == nil {
				key = string(v)
			}
			break
		}
	}
	if key == "" {
		return nil, fmt.Errorf("渠道无可用密钥，无法从上游获取模型")
	}
	req, err := http.NewRequestWithContext(context15(), http.MethodGet,
		strings.TrimRight(c.BaseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if c.Protocol == "openai" {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.Header.Set("X-Api-Key", key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("上游请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("上游状态 %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}
	ids := make([]string, 0, len(out.Data))
	for _, d := range out.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

// AddKeys 批量导入：keysText 每行一枚（空行/首尾空白容忍）；多枚共享名称，
// 以尾4区分。逐枚落密钥存储+建行；中途失败返回已存数量与错误（部分成功明示）。
func (s Channels) AddKeys(channelID, name, keysText string, allowFallback bool) (int, error) {
	if _, err := s.A.Store().Channels().Get(channelID); err != nil {
		return 0, fmt.Errorf("渠道不存在: %w", err)
	}
	if name == "" {
		name = "key"
	}
	var lines []string
	for _, ln := range strings.Split(keysText, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			lines = append(lines, ln)
		}
	}
	if len(lines) == 0 {
		return 0, fmt.Errorf("至少需要一行密钥")
	}
	stored := 0
	for _, apiKey := range lines {
		backend, err := pickBackendFor(s, allowFallback)
		if err != nil {
			return stored, fmt.Errorf("第 %d 枚密钥: %w", stored+1, err)
		}
		id := storage.NewID()
		ref := credential.AccountKey(id)
		if err := s.creds().Set(ref, []byte(apiKey)); err != nil {
			return stored, fmt.Errorf("第 %d 枚密钥写入密钥存储失败: %w", stored+1, err)
		}
		row := storage.ChannelKey{ID: id, ChannelID: channelID, Name: name,
			SecretKey: ref, SecretBackend: backend, KeyTail: tail4(apiKey),
			Priority: 10, Enabled: true}
		if err := s.A.Store().ChannelKeys().Save(row); err != nil {
			s.creds().Delete(ref)
			return stored, fmt.Errorf("第 %d 枚密钥入库失败: %w", stored+1, err)
		}
		stored++
	}
	s.A.Log().Info("channel keys added", "channel", channelID, "count", stored, "actor", "ui", "result", "ok")
	return stored, nil
}

// pickBackendFor 复用 bridge.go 里 Accounts 的确认闸逻辑（该服务退役前两者并存）。
func pickBackendFor(s Channels, allowFallback bool) (string, error) {
	if s.keychainAvailable() {
		return "os-keychain", nil
	}
	if allowFallback {
		return "fallback-file", nil
	}
	return "", fmt.Errorf("系统密钥存储不可用；需显式允许文件降级（allowFallback）")
}

func (s Channels) SetKeyEnabled(id string, on bool) error {
	err := s.A.Store().ChannelKeys().SetEnabled(id, on)
	if err == nil {
		s.A.Log().Info("channel key enabled", "id", id, "on", on, "actor", "ui", "result", "ok")
	}
	return err
}

func (s Channels) RemoveKey(id string) error {
	k, err := s.A.Store().ChannelKeys().Get(id)
	if err != nil {
		return err
	}
	s.creds().Delete(k.SecretKey)
	if err := s.A.Store().ChannelKeys().Delete(id); err != nil {
		return err
	}
	s.A.Log().Info("channel key removed", "id", id, "actor", "ui", "result", "ok")
	return nil
}

func (s Channels) TestKey(id string) provider.ProbeResult {
	k, err := s.A.Store().ChannelKeys().Get(id)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	c, err := s.A.Store().Channels().Get(k.ChannelID)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	key, err := s.creds().Get(k.SecretKey)
	if err != nil {
		return provider.ProbeResult{Stage: "credential", Error: err.Error()}
	}
	return provider.Probe(context15(), c.Protocol, c.BaseURL, string(key), c.AllowPrivateUpstream)
}

func (s Channels) AddModel(channelID, public, upstream string) (ChannelModelRow, error) {
	if public == "" || upstream == "" {
		return ChannelModelRow{}, fmt.Errorf("公开模型名与上游模型名均不能为空")
	}
	if _, err := s.A.Store().Channels().Get(channelID); err != nil {
		return ChannelModelRow{}, fmt.Errorf("渠道不存在: %w", err)
	}
	m := storage.ChannelModel{ID: storage.NewID(), ChannelID: channelID,
		PublicModel: public, UpstreamModel: upstream, Enabled: true}
	if err := s.A.Store().ChannelModels().Save(m); err != nil {
		return ChannelModelRow{}, fmt.Errorf("该渠道已存在公开模型 %s", public)
	}
	s.A.Log().Info("channel model added", "channel", channelID, "public", public,
		"upstream", upstream, "actor", "ui", "result", "ok")
	return ChannelModelRow{ID: m.ID, ChannelID: channelID, PublicModel: public,
		UpstreamModel: upstream, Enabled: true}, nil
}

func (s Channels) UpdateModel(id, public, upstream string) error {
	if public == "" || upstream == "" {
		return fmt.Errorf("公开模型名与上游模型名均不能为空")
	}
	m, err := s.A.Store().ChannelModels().Get(id)
	if err != nil {
		return err
	}
	m.PublicModel, m.UpstreamModel = public, upstream
	if err := s.A.Store().ChannelModels().Save(m); err != nil {
		return fmt.Errorf("该渠道已存在公开模型 %s", public)
	}
	s.A.Log().Info("channel model updated", "id", id, "actor", "ui", "result", "ok")
	return nil
}

func (s Channels) SetModelEnabled(id string, on bool) error {
	err := s.A.Store().ChannelModels().SetEnabled(id, on)
	if err == nil {
		s.A.Log().Info("channel model enabled", "id", id, "on", on, "actor", "ui", "result", "ok")
	}
	return err
}

func (s Channels) RemoveModel(id string) error {
	err := s.A.Store().ChannelModels().Delete(id)
	if err == nil {
		s.A.Log().Info("channel model removed", "id", id, "actor", "ui", "result", "ok")
	}
	return err
}

// ---------- Models 聚合（模型页数据源，spec §7） ----------

type ModelAggregateView struct {
	ID, ChannelID, ChannelName, PublicModel, UpstreamModel string
	Enabled                                                bool
}

type ModelsAgg struct{ A *app.App }

func (s ModelsAgg) ListAll() []ModelAggregateView {
	ms, err := s.A.Store().ChannelModels().List()
	if err != nil {
		return nil
	}
	out := make([]ModelAggregateView, 0, len(ms))
	for _, m := range ms {
		name := m.ChannelID
		if c, err := s.A.Store().Channels().Get(m.ChannelID); err == nil {
			name = c.Name
		}
		out = append(out, ModelAggregateView{ID: m.ID, ChannelID: m.ChannelID,
			ChannelName: name, PublicModel: m.PublicModel,
			UpstreamModel: m.UpstreamModel, Enabled: m.Enabled})
	}
	return out
}

func (s ModelsAgg) SetEnabled(id string, on bool) error {
	return s.A.Store().ChannelModels().SetEnabled(id, on)
}

// ---------- Gateway.PoolStatus（概览池状态条，spec §8） ----------

type PoolEntry struct {
	ChannelID, ChannelName, KeyID, KeyName string
	CoolingUntilMs                         int64
	RemainingSec                           int
	Reason                                 string
}

// PoolStatus 返回当前冷却中的密钥（附渠道/密钥名）。正常密钥不出现。
func (g Gateway) PoolStatus() []PoolEntry {
	now := time.Now()
	sn := g.A.Cooldowns().Snapshot(now)
	if len(sn) == 0 {
		return nil
	}
	name := map[string][2]string{} // key id → [渠道名, 密钥名]
	cs, _ := g.A.Store().Channels().List()
	for _, c := range cs {
		keys, _ := g.A.Store().ChannelKeys().ListByChannel(c.ID)
		for _, k := range keys {
			name[k.ID] = [2]string{c.Name, k.Name}
		}
	}
	out := make([]PoolEntry, 0, len(sn))
	for _, e := range sn {
		p := PoolEntry{KeyID: e.KeyID, CoolingUntilMs: e.Until.UnixMilli(),
			RemainingSec: int(e.Until.Sub(now).Seconds()) + 1, Reason: e.Reason}
		if n, ok := name[e.KeyID]; ok {
			p.ChannelID, p.ChannelName, p.KeyName = chanIDOf(g, e.KeyID), n[0], n[1]
		}
		out = append(out, p)
	}
	return out
}

func chanIDOf(g Gateway, keyID string) string {
	keys, _ := g.A.Store().ChannelKeys().List()
	for _, k := range keys {
		if k.ID == keyID {
			return k.ChannelID
		}
	}
	return ""
}
```

实现说明：`PoolStatus` 的 ChannelID 其实可在一次 keys 遍历里取全——`chanIDOf` 的二次遍历仅为少改代码；执行者可合并为单个 map（`[3]string`），行为一致即可。

- [ ] **Step 4: 按测试里的占位说明清理测试代码并跑通过**

Run: `go test ./internal/bridge -v`
Expected: 全 PASS（旧 Provider/Account 测试同绿）

- [ ] **Step 5: 整树验证 + Commit**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: 全绿

```bash
git add internal/bridge
git commit -m "feat(bridge): Channels 聚合根服务——类型预设/批量密钥/渠道模型/FetchModels/PoolStatus

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 8: desktop 渠道页（master-detail）

**Files:**
- Create: `internal/desktop/channels.go`
- Modify: `internal/desktop/desktop.go`（Services 加 Channels、Bind、uiState 字段、边栏项、pageView、newUIState 初始化）
- Modify: `internal/desktop/desktop_test.go`（testUI 构造补 Channels）
- Modify: `internal/desktop/icons.go`（`iconProviders` 更名 `iconChannels`，图形不变）
- Test: `internal/desktop/channels_test.go`

**Interfaces:**
- Consumes: Task 7 的 `bridge.Channels` 全部方法与视图类型；Task 4 的 `provider.Presets()`。
- Produces: `u.channelsView(c)`；`uiState` 新字段 `chList ui.ListState; chSel int; chForm chFormData; chConfirmDelete bool; chTestMsg string; keyForm keyFormData; keyConfirmDelete bool; chModelForm channelModelFormData; prevType string`。

- [ ] **Step 1: desktop.go 接线**

a) `Services` 加字段（Providers/Accounts 暂留，Task 10 删）：

```go
type Services struct {
	Gateway   bridge.Gateway
	Providers bridge.Providers
	Accounts  bridge.Accounts
	Models    bridge.Models
	Channels  bridge.Channels
}
```

b) `Run` 里装配与 Bind（`Channels: bridge.Channels{A: a, Creds: a.Creds()}`）：

```go
	mygo.Bind(svc.Channels)
```

c) `uiState` 加字段：

```go
	// 渠道页（Task 8）：
	chList          ui.ListState
	chSel           int
	chForm          chFormData
	chConfirmDelete bool
	chTestMsg       string
	keyForm         keyFormData
	keyConfirmDelete bool
	chModelForm     channelModelFormData
	prevType        string // 类型 Select 变更检测（预填 BaseURL）
```

d) 表单结构体（desktop.go 底部）：

```go
// chFormData 是渠道页连接配置表单；editID 非空 = 编辑该渠道。
type chFormData struct {
	typeID, name, baseURL, priorityInput string
	enabled, allowPrivate                bool
	msg, editID                          string
}

// keyFormData 是密钥批量导入表单；keysText 每行一枚。
type keyFormData struct {
	name, keysText string
	allowFallback  bool
	msg            string
}

// channelModelFormData 是渠道模型添加表单。
type channelModelFormData struct {
	public, upstream, msg string
}
```

e) `newUIState` 初始化（`provForm: ...` 行后）：

```go
		chForm:   chFormData{typeID: "anthropic-official", baseURL: "https://api.anthropic.com", enabled: true, priorityInput: "10"},
		keyForm:  keyFormData{},
		chSel:    -1,
```

（`newUIState` 里 `u.chList.Selected = &u.chSel` 与既有 prov/acc 接线并列。）

f) 边栏与分发：

```go
				ui.SidebarItem(c, "overview", iconOverview, "概览")
				ui.SidebarItem(c, "channels", iconChannels, "渠道")
				ui.SidebarItem(c, "providers", iconProviders, "供应商")
				ui.SidebarItem(c, "accounts", iconAccounts, "账号")
				ui.SidebarItem(c, "models", iconModels, "模型")
```

```go
	case "channels":
		u.channelsView(c)
```

icons.go：`iconProviders` 常量更名为 `iconChannels`（保留原 SVG 体，供应商页 Task 10 删除前暂以别名 `iconProviders = iconChannels` 供给旧页引用——或直接把旧页引用改用 `iconChannels`，二选一，前者改动最小）。

g) `desktop_test.go` 的 `testUI` Services 补 `Channels: bridge.Channels{A: a, Creds: a.Creds()},`。

- [ ] **Step 2: 写失败测试（channels_test.go，完整文件）**

```go
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestChannelsPageCreateAndList(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	if !tt.HasText("渠道") || !tt.HasText("尚无渠道") {
		t.Fatalf("texts %v", tt.Texts())
	}
	u.chForm.name = "main"
	u.chForm.baseURL = "https://api.example.com"
	if err := tt.Click("保存渠道"); err != nil {
		t.Fatal(err)
	}
	if got := u.svc.Channels.List(); len(got) != 1 || got[0].Name != "main" || got[0].KeyCount != 0 {
		t.Fatalf("channels %+v", got)
	}
	if u.chForm.name != "" {
		t.Fatal("form must reset after create")
	}
}

func TestChannelsTypePresetPrefillsBaseURL(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	// 模拟下拉切到 OpenAI 兼容（UI 内即 Select 值变更）
	u.chForm.typeID = "openai-compatible"
	if err := tt.Click("保存渠道"); err != nil {
		t.Fatal(err)
	}
	// 兼容类无默认 URL 且表单为空 → 报错留在表单
	if u.chForm.msg == "" {
		t.Fatalf("empty baseURL must surface: %v", u.chForm.msg)
	}
	u.chForm.baseURL = "https://o.example.com"
	if err := tt.Click("保存渠道"); err != nil {
		t.Fatal(err)
	}
	got := u.svc.Channels.List()
	if len(got) != 1 || got[0].Protocol != "openai" {
		t.Fatalf("channels %+v", got)
	}
}

func TestChannelsLoadSelectedAndDeleteConfirm(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	u.chForm.name, u.chForm.baseURL = "a", "https://a.example.com"
	_ = tt.Click("保存渠道")
	u.chForm.name, u.chForm.baseURL = "b", "https://b.example.com"
	_ = tt.Click("保存渠道")
	u.chSel = 0
	if err := tt.Click("载入所选"); err != nil {
		t.Fatal(err)
	}
	if u.chForm.editID == "" || u.chForm.name != "a" {
		t.Fatalf("load selected: %+v", u.chForm)
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !u.chConfirmDelete {
		t.Fatal("first click arms confirm")
	}
	u.chSel = 1
	_ = tt.Click("载入所选")
	if u.chConfirmDelete {
		t.Fatal("loading other row disarms confirm")
	}
}

func TestChannelsKeysBatchAdd(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	u.chForm.name, u.chForm.baseURL = "m", "https://m.example.com"
	_ = tt.Click("保存渠道")
	u.chSel = 0
	u.keyForm.name = "pool"
	u.keyForm.keysText = "sk-aaa11111\n\nsk-bbb22222\n"
	if err := tt.Click("导入密钥"); err != nil {
		t.Fatal(err)
	}
	d, err := u.svc.Channels.Get(u.svc.Channels.List()[0].ID)
	if err != nil || len(d.Keys) != 2 {
		t.Fatalf("keys %+v err %v", d.Keys, err)
	}
	if u.keyForm.keysText != "" || u.keyForm.name != "" {
		t.Fatal("key form must reset after import")
	}
}

func TestChannelsModelAddAndDuplicate(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	u.chForm.name, u.chForm.baseURL = "m", "https://m.example.com"
	_ = tt.Click("保存渠道")
	u.chModelForm.public, u.chModelForm.upstream = "pub", "up"
	_ = tt.Click("添加模型")
	d, _ := u.svc.Channels.Get(u.svc.Channels.List()[0].ID)
	if len(d.Models) != 1 || d.Models[0].PublicModel != "pub" {
		t.Fatalf("models %+v", d.Models)
	}
	u.chModelForm.public, u.chModelForm.upstream = "pub", "up2"
	_ = tt.Click("添加模型")
	if u.chModelForm.msg == "" {
		t.Fatal("duplicate must surface in model form msg")
	}
}

func TestChannelsNavSwitch(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 900, 600)
	if err := tt.Click("渠道"); err != nil {
		t.Fatal(err)
	}
	if u.page != "channels" {
		t.Fatalf("page %q", u.page)
	}
	if !tt.HasText("创建第一个渠道") && !tt.HasText("尚无渠道") {
		t.Fatalf("channels page hint missing: %v", tt.Texts())
	}
}
```

- [ ] **Step 3: 跑测试确认失败**

Run: `go test ./internal/desktop -run TestChannels -v`
Expected: FAIL（`channelsView` 未定义）

- [ ] **Step 4: 实现 channels.go**

```go
package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/provider"
)

// presetLabels 供 Select 展示；返回 labels 与 label→id 映射。
func presetLabels() ([]string, map[string]string) {
	ps := provider.Presets()
	labels := make([]string, 0, len(ps))
	byLabel := map[string]string{}
	for _, p := range ps {
		labels = append(labels, p.Label)
		byLabel[p.Label] = p.ID
	}
	return labels, byLabel
}

func presetOf(typeID string) provider.Preset {
	p, _ := provider.LookupPreset(typeID)
	return p
}

// channelsView 是渠道页（spec §8）：渠道表 + 选中渠道详情（连接配置/密钥/模型）。
func (u *uiState) channelsView(c *ui.Context) {
	t := c.Theme()
	chans := u.svc.Channels.List()
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "渠道").Bold().FontSize(t.FontSize + 2)
			ui.Text(c, strconv.Itoa(len(chans))).TextColor(t.TextMuted)
		})
		if len(chans) == 0 {
			ui.Text(c, "尚无渠道——在下方表单创建第一个渠道").TextColor(t.TextMuted)
		}
		cols := []ui.TableColumn{{Title: "名称"}, {Title: "类型"}, {Title: "状态"},
			{Title: "密钥数"}, {Title: "模型数"}, {Title: "优先级"}}
		ui.Table(c, &u.chList, cols, len(chans), func(row, col int) {
			ch := chans[row]
			switch col {
			case 0:
				ui.Text(c, ch.Name).SingleLine()
			case 1:
				ui.Text(c, ch.Type).SingleLine()
			case 2:
				if ch.Enabled {
					ui.Badge(c, "启用")
				} else {
					ui.Text(c, "停用").TextColor(t.TextMuted)
				}
			case 3:
				ui.Text(c, strconv.Itoa(ch.KeyCount)).SingleLine().Font(monoFont)
			case 4:
				ui.Text(c, strconv.Itoa(ch.ModelCount)).SingleLine().Font(monoFont)
			case 5:
				ui.Text(c, strconv.Itoa(ch.Priority)).SingleLine().Font(monoFont)
			}
		}).Grow(1)
		ui.Divider()

		// 创建表单：类型 Select（预设）+ 名称 + BaseURL（类型变更时预填默认值）
		labels, byLabel := presetLabels()
		if u.prevType != u.chForm.typeID { // 类型变更 → 预填默认 BaseURL（新建态才覆盖）
			u.prevType = u.chForm.typeID
			if u.chForm.editID == "" {
				u.chForm.baseURL = presetOf(u.chForm.typeID).DefaultBaseURL
			}
		}
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "类型:").Width(96).TextColor(t.TextMuted)
			ui.Select(c, &u.chForm.typeID, labelsOf(labels, byLabel, u.chForm.typeID))
			ui.Text(c, presetOf(u.chForm.typeID).Description).TextColor(t.TextMuted)
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.chForm.name).Grow(1)
			ui.Text(c, "Base URL:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.chForm.baseURL).Grow(1)
		})
		ui.Row(c).Gap(16).Children(func() {
			ui.Text(c, "启用").TextColor(t.TextMuted)
			ui.Switch(c, &u.chForm.enabled)
			ui.Text(c, "允许私网上游").TextColor(t.TextMuted)
			ui.Switch(c, &u.chForm.allowPrivate)
		})
		if u.chForm.msg != "" {
			ui.Text(c, u.chForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			saveLabel := "保存渠道"
			if u.chForm.editID != "" {
				saveLabel = "更新渠道"
			}
			if ui.PrimaryButton(c, saveLabel).Clicked() {
				u.submitChannel()
			}
			if ui.Button(c, "载入所选").Clicked() {
				if sel := u.chList.Selected; sel != nil && *sel >= 0 && *sel < len(chans) {
					u.loadChannel(chans[*sel])
				}
			}
			if ui.Button(c, "删除").Clicked() && u.chForm.editID != "" {
				u.chConfirmDelete = true
			}
			if u.chConfirmDelete && ui.Button(c, "确认删除").Clicked() {
				if err := u.svc.Channels.Remove(u.chForm.editID); err != nil {
					u.chForm.msg = err.Error()
				} else {
					u.resetChannelForm()
				}
				u.chConfirmDelete = false
			}
			if ui.Button(c, "测试连通").Clicked() && u.chForm.editID != "" {
				res := u.svc.Channels.Test(u.chForm.editID)
				if res.OK {
					u.chTestMsg = "连通正常 (" + res.Stage + ")"
				} else {
					u.chTestMsg = res.Stage + " 失败: " + res.Error
				}
			}
		})
		if u.chTestMsg != "" {
			ui.Text(c, u.chTestMsg)
		}

		// ---- 选中渠道详情（editID 非空才渲染三段子面板）----
		if u.chForm.editID == "" {
			return
		}
		detail, err := u.svc.Channels.Get(u.chForm.editID)
		if err != nil {
			ui.Textf(c, "渠道详情加载失败: %v", err).TextColor(t.Danger)
			return
		}
		ui.Divider()

		// 密钥子表 + 批量导入表单
		ui.Text(c, "密钥").Bold()
		if len(detail.Keys) == 0 {
			ui.Text(c, "尚无密钥——导入一枚后该渠道才能接流量").TextColor(t.TextMuted)
		}
		keyCols := []ui.TableColumn{{Title: "名称"}, {Title: "尾4"}, {Title: "存储"},
			{Title: "冷却"}, {Title: "启用"}, {Title: "优先级"}}
		ui.Table(c, &u.chKeyList, keyCols, len(detail.Keys), func(row, col int) {
			k := detail.Keys[row]
			switch col {
			case 0:
				ui.Text(c, k.Name).SingleLine()
			case 1:
				ui.Text(c, k.KeyTail).SingleLine().Font(monoFont)
			case 2:
				ui.Text(c, k.SecretBackend).SingleLine().Font(monoFont)
			case 3:
				if k.Cooling {
					ui.Textf(c, "冷却 %ds (%s)", k.CoolingRemainSec, k.CoolingReason).TextColor(t.Warning)
				} else {
					ui.Text(c, "—").TextColor(t.TextMuted)
				}
			case 4:
				if k.Enabled {
					ui.Badge(c, "启用")
				} else {
					ui.Text(c, "停用").TextColor(t.TextMuted)
				}
			case 5:
				ui.Text(c, strconv.Itoa(k.Priority)).SingleLine().Font(monoFont)
			}
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "密钥名称:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.keyForm.name).Grow(1)
			ui.Text(c, "允许文件降级").TextColor(t.TextMuted)
			ui.Switch(c, &u.keyForm.allowFallback)
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "API Keys（每行一枚）:").Width(140).TextColor(t.TextMuted)
			ui.TextInput(c, &u.keyForm.keysText).Grow(1)
		})
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, "导入密钥").Clicked() {
				n, err := u.svc.Channels.AddKeys(u.chForm.editID, u.keyForm.name, u.keyForm.keysText, u.keyForm.allowFallback)
				if err != nil {
					u.keyForm.msg = err.Error()
				} else {
					u.keyForm.msg = fmt.Sprintf("已导入 %d 枚密钥", n)
					u.keyForm.name, u.keyForm.keysText = "", ""
				}
			}
			if ui.Button(c, "停用/启用密钥").Clicked() {
				if sel := u.chKeyList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Keys) {
					k := detail.Keys[*sel]
					_ = u.svc.Channels.SetKeyEnabled(k.ID, !k.Enabled)
				}
			}
			if ui.Button(c, "测试所选密钥").Clicked() {
				if sel := u.chKeyList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Keys) {
					res := u.svc.Channels.TestKey(detail.Keys[*sel].ID)
					if res.OK {
						u.chTestMsg = "连通正常 (" + res.Stage + ")"
					} else {
						u.chTestMsg = res.Stage + " 失败: " + res.Error
					}
				}
			}
			if ui.Button(c, "删除密钥").Clicked() {
				if sel := u.chKeyList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Keys) {
					u.keyConfirmDelete = true
				}
			}
			if u.keyConfirmDelete && ui.Button(c, "确认删除密钥").Clicked() {
				if sel := u.chKeyList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Keys) {
					if err := u.svc.Channels.RemoveKey(detail.Keys[*sel].ID); err != nil {
						u.keyForm.msg = err.Error()
					}
				}
				u.keyConfirmDelete = false
			}
		})
		if u.keyForm.msg != "" {
			ui.Text(c, u.keyForm.msg)
		}
		ui.Divider()

		// 模型子表 + 添加表单 + 从上游获取
		ui.Text(c, "模型").Bold()
		if len(detail.Models) == 0 {
			ui.Text(c, "尚无模型——该渠道不会接到任何请求，先添加公开模型").TextColor(t.TextMuted)
		}
		mCols := []ui.TableColumn{{Title: "公开模型"}, {Title: "上游模型"}, {Title: "启用"}}
		ui.Table(c, &u.chModelList, mCols, len(detail.Models), func(row, col int) {
			m := detail.Models[row]
			switch col {
			case 0:
				ui.Text(c, m.PublicModel).SingleLine()
			case 1:
				ui.Text(c, m.UpstreamModel).SingleLine().Font(monoFont)
			case 2:
				if m.Enabled {
					ui.Badge(c, "启用")
				} else {
					ui.Text(c, "停用").TextColor(t.TextMuted)
				}
			}
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "公开模型名:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.chModelForm.public).Grow(1)
			ui.Text(c, "上游模型名:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.chModelForm.upstream).Grow(1)
		})
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, "添加模型").Clicked() {
				if _, err := u.svc.Channels.AddModel(u.chForm.editID, u.chModelForm.public, u.chModelForm.upstream); err != nil {
					u.chModelForm.msg = err.Error()
				} else {
					u.chModelForm = channelModelFormData{}
				}
			}
			if ui.Button(c, "停用/启用模型").Clicked() {
				if sel := u.chModelList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Models) {
					m := detail.Models[*sel]
					_ = u.svc.Channels.SetModelEnabled(m.ID, !m.Enabled)
				}
			}
			if ui.Button(c, "从上游获取").Clicked() {
				if ids, err := u.svc.Channels.FetchModels(u.chForm.editID); err != nil {
					u.chModelForm.msg = err.Error()
				} else {
					u.chModelForm.msg = "上游模型: " + strings.Join(ids, ", ")
				}
			}
			if ui.Button(c, "删除模型").Clicked() {
				if sel := u.chModelList.Selected; sel != nil && *sel >= 0 && *sel < len(detail.Models) {
					_ = u.svc.Channels.RemoveModel(detail.Models[*sel].ID)
				}
			}
		})
		if u.chModelForm.msg != "" {
			ui.Text(c, u.chModelForm.msg)
		}
	})
}

// submitChannel 分支：优先级必须整数；Create 走类型预设；Update 改字段。
func (u *uiState) submitChannel() {
	prio, err := strconv.Atoi(u.chForm.priorityInput)
	if err != nil {
		u.chForm.msg = "优先级必须是整数"
		return
	}
	if u.chForm.editID == "" {
		if _, err := u.svc.Channels.Create(u.chForm.typeID, u.chForm.name, u.chForm.baseURL,
			prio, u.chForm.enabled, u.chForm.allowPrivate); err != nil {
			u.chForm.msg = err.Error()
			return
		}
		u.resetChannelForm()
		return
	}
	if err := u.svc.Channels.Update(u.chForm.editID, u.chForm.name, u.chForm.baseURL,
		prio, u.chForm.enabled, u.chForm.allowPrivate); err != nil {
		u.chForm.msg = err.Error()
		return
	}
	u.resetChannelForm()
}

func (u *uiState) loadChannel(v bridge.ChannelView) {
	u.chForm = chFormData{typeID: v.Type, name: v.Name, baseURL: v.BaseURL,
		priorityInput: strconv.Itoa(v.Priority), enabled: v.Enabled,
		allowPrivate: v.AllowPrivateUpstream, editID: v.ID}
	u.chTestMsg = ""
	u.chConfirmDelete = false
	u.keyForm = keyFormData{}
	u.chModelForm = channelModelFormData{}
}

func (u *uiState) resetChannelForm() {
	u.chForm = chFormData{typeID: "anthropic-official", baseURL: "https://api.anthropic.com",
		enabled: true, priorityInput: "10"}
	u.chTestMsg = ""
	u.keyForm = keyFormData{}
	u.chModelForm = channelModelFormData{}
}

// labelsOf 保证当前值在 Select 选项内（mygo Select 对未知值的行为未验证，
// 不假定——把当前 typeID 的 label 兜底拼接进选项）。
func labelsOf(labels []string, byLabel map[string]string, typeID string) []string {
	for _, l := range labels {
		if byLabel[l] == typeID {
			return labels
		}
	}
	p := provider.LookupPreset(typeID)
	if p.ID != "" {
		return append([]string{p.Label}, labels...)
	}
	return labels
}
```

补充接线（desktop.go）：`uiState` 再加三个子表状态字段并在 `newUIState` 接 `Selected` 指针：

```go
	chKeyList   ui.ListState
	chModelList ui.ListState
	chKeySel    int
	chModelSel  int
```

```go
	u.chKeyList.Selected = &u.chKeySel
	u.chModelList.Selected = &u.chModelSel
```

实现说明（写给执行者）：密钥批量导入的 keysText 用单行 `ui.TextInput` 承载（mygo 无 multiline 输入的 API 已验证面），以 `,` 或空白分隔亦可——为与测试一致，**按行分隔**：把 `AddKeys` 调用改为 `strings.ReplaceAll(u.keyForm.keysText, ",", "\n")` 后传入即可，文案「每行一枚」照旧。多行输入若验证可用（`ui.TextInput` 之外另有组件），换用之并删掉该替换。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/desktop -v`
Expected: 全 PASS（旧页面测试同绿；TestNavigationSwitchesPage 不受影响——渠道是新增项）

- [ ] **Step 6: 整树验证 + Commit**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: 全绿

```bash
git add internal/desktop
git commit -m "feat(desktop): 渠道页——类型预设/批量密钥/渠道级模型/冷却列，master-detail

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 9: desktop 模型聚合页重写 + 概览池状态条

**Files:**
- Modify: `internal/desktop/models.go`（重写为跨渠道聚合视图，spec 决策 7：只读+行内启停）
- Modify: `internal/desktop/overview.go`（启用 P0-B 槽位：池状态条）
- Modify: `internal/desktop/theme.go`（`stateColor`/`ledDot` 支持 `cooling` 态）
- Test: `internal/desktop/models_test.go`（重写）、`internal/desktop/overview_test.go`（追加池状态用例）

**Interfaces:**
- Consumes: Task 7 的 `bridge.ModelsAgg`（`ListAll/SetEnabled`）与 `Gateway.PoolStatus()`。
- Produces: `u.modelsView(c)` 新形态；`uiState` 字段调整（`modelList/modelSel` 复用，删 `modelForm/modelConfirmDelete`，增 `aggSvc`；池状态无状态——每帧直读）。

- [ ] **Step 1: 写失败测试（models_test.go 重写）**

```go
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

func aggFixture(t *testing.T) *uiState {
	u, _ := testUI(t)
	if _, err := u.svc.Channels.Create("anthropic-compatible", "a", "https://a.example.com", 10, true, false); err != nil {
		t.Fatal(err)
	}
	c2, err := u.svc.Channels.Create("openai-compatible", "b", "https://b.example.com", 20, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.Channels.AddModel(u.svc.Channels.List()[0].ID, "pub-1", "up-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.Channels.AddModel(c2.ID, "pub-1", "up-b"); err != nil {
		t.Fatal(err)
	}
	return u
}

func TestModelsAggregateShowsCrossChannel(t *testing.T) {
	u := aggFixture(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 1000, 700)
	if !tt.HasText("模型") || !tt.HasText("pub-1") {
		t.Fatalf("texts %v", tt.Texts())
	}
	// 聚合页提示编辑入口在渠道页（决策 7 的文案承载）
	if !tt.HasText("渠道") {
		t.Fatalf("channel column missing: %v", tt.Texts())
	}
}

func TestModelsAggregateToggleEnabled(t *testing.T) {
	u := aggFixture(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 1000, 700)
	u.modelSel = 0
	if err := tt.Click("停用/启用"); err != nil {
		t.Fatal(err)
	}
	rows := bridge.ModelsAgg{A: u.svc.Channels.A}.ListAll()
	if rows[0].Enabled {
		t.Fatalf("toggle failed: %+v", rows[0])
	}
}

func TestModelsAggregateEmptyHint(t *testing.T) {
	u, _ := testUI(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 1000, 700)
	if !tt.HasText("尚无模型") {
		t.Fatalf("texts %v", tt.Texts())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run TestModels -v`
Expected: FAIL（聚合语义未实现/旧两列表仍在）

- [ ] **Step 3: 重写 models.go**

```go
package desktop

import (
	"strconv"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// modelsView 是模型聚合页（spec §8，决策 7）：跨渠道只读视图 + 行内启停；
// 增删改入口收敛在渠道详情，避免双入口不一致。
func (u *uiState) modelsView(c *ui.Context) {
	t := c.Theme()
	rows := bridge.ModelsAgg{A: u.svc.Channels.A}.ListAll()
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "模型").Bold().FontSize(t.FontSize + 2)
			ui.Text(c, strconv.Itoa(len(rows))).TextColor(t.TextMuted)
		})
		ui.Text(c, "公开模型 × 渠道 × 上游模型（编辑请到渠道页对应渠道）").TextColor(t.TextMuted)
		if len(rows) == 0 {
			ui.Text(c, "尚无模型——在渠道页为渠道添加公开模型").TextColor(t.TextMuted)
		}
		cols := []ui.TableColumn{{Title: "公开模型"}, {Title: "渠道"}, {Title: "上游模型"}, {Title: "启用"}}
		ui.Table(c, &u.modelList, cols, len(rows), func(row, col int) {
			m := rows[row]
			switch col {
			case 0:
				ui.Text(c, m.PublicModel).SingleLine()
			case 1:
				ui.Text(c, m.ChannelName).SingleLine()
			case 2:
				ui.Text(c, m.UpstreamModel).SingleLine().Font(monoFont)
			case 3:
				if m.Enabled {
					ui.Badge(c, "启用")
				} else {
					ui.Text(c, "停用").TextColor(t.TextMuted)
				}
			}
		}).Grow(1)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "停用/启用").Clicked() {
				if sel := u.modelList.Selected; sel != nil && *sel >= 0 && *sel < len(rows) {
					_ = bridge.ModelsAgg{A: u.svc.Channels.A}.SetEnabled(rows[*sel].ID, !rows[*sel].Enabled)
				}
			}
		})
	})
}
```

`desktop.go` 的 `uiState`：删 `modelForm`/`modelConfirmDelete` 字段（引用处只剩旧 models.go，重写后已无）；`newUIState` 里 `modelForm` 初始化删除。

- [ ] **Step 4: overview.go 启用池状态条 + theme.go cooling 色**

theme.go：`stateColor` 加分支（`degraded` 分支后）：

```go
	case "cooling":
		return t.Warning
```

overview.go：把「P0-B 槽位」注释替换为池状态条实现（Divider 之后、请求表之前）：

```go
		// 池状态条（spec §8）：每枚密钥一枚状态点 + 渠道名/密钥名；
		// 冷却中 = Warning 常亮 + 剩余秒。正常密钥不出现（PoolStatus 只回冷却）。
		if pool := u.svc.Gateway.PoolStatus(); len(pool) > 0 {
			ui.Row(c).Gap(12).Children(func() {
				for _, e := range pool {
					p := e
					ui.Row(c).Gap(6).Children(func() {
						ledDot(c, t, "cooling")
						ui.Textf(c, "%s/%s 冷却 %ds", p.ChannelName, p.KeyName, p.RemainingSec).TextColor(t.Warning)
					})
				}
			})
		} else {
			ui.Text(c, "密钥池: 全部就绪").TextColor(t.TextMuted)
		}
```

- [ ] **Step 5: overview_test.go 追加池状态用例**

```go
func TestOverviewPoolStatusShowsCooling(t *testing.T) {
	u, _ := testUI(t)
	if _, err := u.svc.Channels.Create("anthropic-compatible", "m", "https://m.example.com", 10, true, false); err != nil {
		t.Fatal(err)
	}
	cid := u.svc.Channels.List()[0].ID
	if _, err := u.svc.Channels.AddKeys(cid, "k1", "sk-abc12345", true); err != nil {
		t.Fatal(err)
	}
	d, _ := u.svc.Channels.Get(cid)
	u.svc.Gateway.A.Cooldowns().MarkClassified(d.Keys[0].ID, "rate_limited", 30, time.Now())
	tt := ui.NewTester(u.overviewView, 900, 600)
	if !tt.HasText("冷却") || !tt.HasText("m/k1") {
		t.Fatalf("pool strip missing: %v", tt.Texts())
	}
}
```

（`overview_test.go` 需补 `time` import。`testUI` 已含 Channels（Task 8）。）

- [ ] **Step 6: 跑测试确认通过 + 整树验证**

Run: `go test -race ./internal/desktop -v && go build ./... && go vet ./... && go test ./...`
Expected: 全绿（旧 models 测试已随重写移除）

- [ ] **Step 7: Commit**

```bash
git add internal/desktop
git commit -m "feat(desktop): 模型聚合页（跨渠道只读+启停）与概览密钥池状态条

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

---

### Task 10: 删旧收尾——migration 合并单 v1、旧服务/旧页面删除、文档更新

**Files:**
- Modify: `internal/storage/db.go`（migrations 合并为单一 v1 = spec §3.1 全量 schema + settings；删 001/002/003）
- Modify: `internal/storage/storage.go`、`internal/storage/repos.go`（删 Provider/Account/ModelMapping 结构体、三个旧 repo 接口与实现、`Store` 旧三方法）
- Modify: `internal/storage/storage_test.go`（删旧用例）
- Modify: `internal/bridge/bridge.go`（删 `Providers`/`Accounts`/`ModelView`/`Models` 旧服务；保留 `Gateway`/`tail4`/`context15`）
- Modify: `internal/bridge/bridge_test.go`（删旧服务用例）
- Modify: `internal/desktop/desktop.go`（Services/Bind/uiState/边栏/pageView 清理为三页）
- Delete: `internal/desktop/providers.go`、`internal/desktop/providers_test.go`、`internal/desktop/accounts.go`、`internal/desktop/accounts_test.go`
- Modify: `internal/desktop/icons.go`（删 `iconAccounts`/`iconProviders` 残留）
- Modify: `internal/desktop/desktop_test.go`（testUI 删旧字段；导航用例更新）
- Modify: `CLAUDE.md`、`README.md`

**Interfaces:**
- Consumes: Task 1-9 全部产出。
- Produces: 终态代码树——`Store` 只含 `Channels/ChannelKeys/ChannelModels/Settings`；bridge 只含 `Gateway/Channels/ModelsAgg`；desktop 边栏 概览/渠道/模型 三页。

- [ ] **Step 1: migration 合并为单一 v1**

`db.go` 删除 `migration001/002/003` 三个常量，替换为：

```go
// migrationV1 是渠道化重构后的首发 schema（spec 2026-10-17 §3.1；用户决策：
// 发布前无存量用户，删库重建——勿改已发布语句的纪律自本 v1 起生效）。
const migrationV1 = `
CREATE TABLE channels (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, type TEXT NOT NULL,
  protocol TEXT NOT NULL, base_url TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 10, enabled INTEGER NOT NULL DEFAULT 1,
  allow_private_upstream INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE channel_keys (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  name TEXT NOT NULL, secret_key TEXT NOT NULL, secret_backend TEXT NOT NULL,
  key_tail TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL DEFAULT 10,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX idx_channel_keys_channel ON channel_keys(channel_id);
CREATE TABLE channel_models (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  public_model TEXT NOT NULL, upstream_model TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(channel_id, public_model)
);
CREATE INDEX idx_channel_models_channel ON channel_models(channel_id);
CREATE INDEX idx_channel_models_public ON channel_models(public_model);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`
```

`migrations` 切片只剩 `{1, migrationV1},`。

- [ ] **Step 2: storage 删旧**

`storage.go`：删 `Provider`/`Account`/`ModelMapping` 三个结构体与 `ProviderRepo/AccountRepo/ModelRepo` 三个接口；`Store` 接口定稿：

```go
// Store 是持久层入口。
type Store interface {
	Channels() ChannelRepo
	ChannelKeys() ChannelKeyRepo
	ChannelModels() ChannelModelRepo
	Settings() SettingsRepo
	Close() error
}
```

`repos.go`：删 `providerRepo/accountRepo/modelRepo` 全部实现；`db.go` 的 `dbStore` 删三个旧访问器。`storage_test.go`：删引用旧类型的用例，保留渠道用例与 settings 用例（若 settings 用例经旧 API，改为 `s.Settings()` 直用——现用例本就直用，无需动）。

- [ ] **Step 3: bridge 删旧服务**

`bridge.go` 保留：包注释（更新措辞）、`context15`、`Gateway`（Status/Start/Stop/RegenerateToken/SaveSettings/Recent/PoolStatus——PoolStatus 在 channels.go）、`tail4`。删 `ProviderView/providerView/Providers`（全部方法）、`AccountView/accountView/Accounts`（全部方法）、`ModelView/Models`（旧全局别名服务；`pickBackend` 随 Accounts 删——channels.go 里的 `pickBackendFor` 已独立）。包注释改为：

```go
// Package bridge 是管理面桥接层：把 app.App 的能力整理为经 mygo.Bind
// 暴露给 desktop 的三个服务——Gateway（网关控制+池状态）、
// Channels（渠道聚合根）、ModelsAgg（模型跨渠道聚合）。后者在 channels.go。
```

`bridge_test.go`：删 `TestProviderCRUDAndValidation`、`TestAccountSecretLifecycle` 及其他引用旧服务的用例；`tempApp` 保留（channels_test 用）。

- [ ] **Step 4: desktop 删旧页**

```bash
rm internal/desktop/providers.go internal/desktop/providers_test.go internal/desktop/accounts.go internal/desktop/accounts_test.go
```

`desktop.go`：
- `Services` 删 `Providers/Accounts/Models`（旧全局别名服务）字段，终态：

```go
type Services struct {
	Gateway  bridge.Gateway
	Channels bridge.Channels
}
```

- `Run` 里 `mygo.Bind` 删对应三行；装配只剩 Gateway/Channels。
- `uiState` 删 `provList/provSel/provForm/provConfirmDelete/provTestMsg/accList/accSel/accForm/accConfirmDelete/accTestMsg/modelForm/modelConfirmDelete` 及 `provFormData/accFormData`（modelFormData 若还在 models.go 已删）。
- 边栏终态：

```go
				ui.SidebarItem(c, "overview", iconOverview, "概览")
				ui.SidebarItem(c, "channels", iconChannels, "渠道")
				ui.SidebarItem(c, "models", iconModels, "模型")
```

- `pageView` 删 providers/accounts 分支。
- icons.go：删 `iconAccounts` 与 `iconProviders`（Task 8 已改名/别名，此刻无引用）。
- `desktop_test.go`：`testUI` 终态：

```go
	svc := Services{
		Gateway:  bridge.Gateway{A: a},
		Channels: bridge.Channels{A: a, Creds: a.Creds()},
	}
```

- `TestNavigationSwitchesPage`（providers_test.go 已删，若该用例在 desktop_test.go 中存在则更新）：断言「渠道」点击 → `u.page == "channels"`，「概览」→ overview。

- [ ] **Step 5: 全树验证**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: 全绿。若出现旧 API 残留引用（如 cmd、其他测试），逐一改为 Channels 语义——不得恢复旧 API。

- [ ] **Step 6: CLAUDE.md 更新**

`CLAUDE.md` 需改三处（在现有文本上改，不推倒重写）：

1. 「项目状态与权威文档」段追加一句：

```markdown
2026-10-17 起渠道模型为 new-api 式 **channels 聚合根**（`channels`/`channel_keys`/`channel_models` 三表，渠道级模型清单与重定向；providers/accounts/models 旧表已退役）——设计见 `docs/superpowers/specs/2026-10-17-channels-models-p0b-design.md`，实现计划见 `docs/superpowers/plans/2026-10-17-channels-models-p0b.md`。P0-B（优先级故障转移、冷却、二次校验）随该 spec 落地。**该重构采用删库重建**：schema 合并为单一 migration v1，老用户需删除一次 `~/Library/Application Support/AgentSwitch/agentswitch.db`。
```

2. 「常用命令」段：无变化（命令不变）。

3. 「目标目录与依赖规则」段：`internal/{...account...}` 清单中删除 `account`；`migrations/` 说明改为「migrations 内嵌在 `internal/storage`（当前单一 v1）」。

- [ ] **Step 7: README 更新**

`README.md` 首次使用流程补「渠道配置」小节与升级说明（措辞可微调，要点必须齐）：

```markdown
## 渠道配置

1. 打开「渠道」页 → 表单选渠道类型（Anthropic 官方 / Anthropic 兼容中转 / OpenAI 官方 / OpenAI 兼容），类型决定协议与默认地址。
2. 填名称与 Base URL（兼容类必填），保存渠道。
3. 在渠道详情导入 API Key（每行一枚，可批量）；密钥进系统钥匙串，库里只存引用与尾 4 位。
4. 为渠道添加模型：公开模型名（Agent 请求名）→ 上游模型名；同渠道公开名唯一，跨渠道可重名。
5. 用「测试连通 / 测试所选密钥」验证后启动网关；`/v1/models` 输出所有渠道启用模型的并集。

### 从旧版本升级（0.1.0 之前）

渠道化重构采用删库重建：删除一次 `~/Library/Application Support/AgentSwitch/agentswitch.db` 后重启应用即可（供应商/账号/别名需按新模型重新配置）。
```

- [ ] **Step 8: 最终验证 + Commit**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: 全绿

```bash
git add -A
git commit -m "refactor: 删旧收尾——migration 合并单 v1，providers/accounts/全局别名退役，文档同步

channels 聚合根为唯一渠道模型；bridge 三服务（Gateway/Channels/ModelsAgg）；
桌面三页（概览/渠道/模型）。用户需删库重建一次（README 有说明）。

Co-Authored-By: Claude Code <noreply@anthropic.com>"
```

- [ ] **Step 9: 人工验收（不在 CI）**

1. `rm ~/Library/Application\ Support/AgentSwitch/agentswitch.db`（或对应平台 UserConfigDir 路径）
2. `go tool mygo dev` 启动，浅/深两种外观过一遍三页
3. 建一个 anthropic-compatible 渠道指向本地 httptest 或真实中转，导入密钥、加模型、测试连通
4. Claude Code 指向网关跑一轮真实对话（含 tool use）
5. 概览确认池状态条在密钥被 429 冷却时出现琥珀点

---

## 完成定义

- [ ] 全部任务 checkbox 完成；`go build ./... && go vet ./... && go test -race ./...` 全绿
- [ ] spec §1 七项决策逐条对得上实现（渠道聚合根、双协议、渠道级模型、删库重建、三页 IA、聚合页只读、类型预设）
- [ ] docx §6.3/§7 不变量测试全部落位（Task 5/6）
- [ ] CLAUDE.md / README 与终态一致；偏离凭证 = spec §12

