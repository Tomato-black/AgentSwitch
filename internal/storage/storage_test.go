package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

func tempStore(t *testing.T) Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	// 渠道化重构后 schema 只剩单一 v1（删库重建，spec 决策 5）；重开时已记录
	// 版本按序跳过，不得重放报错，schema_migrations 也必须恰有一条 v1。
	ds, ok := s2.(*dbStore)
	if !ok {
		t.Fatalf("Store = %T", s2)
	}
	rows, err := ds.db.Query(`SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		got = append(got, v)
	}
	if rows.Err() != nil || len(got) != 1 || got[0] != 1 {
		t.Fatalf("schema_migrations = %v, want [1]", got)
	}
}

func TestSettingsGetSet(t *testing.T) {
	s := tempStore(t)
	if err := s.Settings().Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Settings().Get("k"); err != nil || v != "v" {
		t.Fatalf("settings: %q %v", v, err)
	}
	// 缺失键返回空串而非错误（config.Load 视为默认值）。
	if v, err := s.Settings().Get("missing"); err != nil || v != "" {
		t.Fatalf("missing key: %q %v", v, err)
	}
}

// TestForeignKeysOnEveryConnection 覆盖 PRAGMA 必须逐连接生效：
// 从池里取出两条连接（第二条必为新建），各自断言 foreign_keys=1、busy_timeout=5000。
func TestForeignKeysOnEveryConnection(t *testing.T) {
	s := tempStore(t)
	ds, ok := s.(*dbStore)
	if !ok {
		t.Fatalf("Store = %T, want *dbStore", s)
	}
	ctx := context.Background()
	c1, err := ds.db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c2, err := ds.db.Conn(ctx) // c1 仍占用池中连接，此调用强制新开一条
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	for i, c := range []*sql.Conn{c1, c2} {
		var fk int
		if err := c.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
			t.Fatalf("conn%d foreign_keys: %v", i+1, err)
		}
		if fk != 1 {
			t.Fatalf("conn%d foreign_keys = %d, want 1", i+1, fk)
		}
	}
	var bt int
	if err := c2.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&bt); err != nil {
		t.Fatalf("conn2 busy_timeout: %v", err)
	}
	if bt != 5000 {
		t.Fatalf("conn2 busy_timeout = %d, want 5000", bt)
	}
	var jm string
	if err := c2.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&jm); err != nil {
		t.Fatalf("conn2 journal_mode: %v", err)
	}
	if jm != "wal" {
		t.Fatalf("conn2 journal_mode = %q, want wal", jm)
	}
}

// ---- 渠道化三表（migration v1，spec 2026-10-17 §3.1）----

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
	s := tempStore(t)
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
	s := tempStore(t)
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
	s := tempStore(t)
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
	byPub, _ := s.ChannelModels().ListByPublic("claude-x")
	if len(byPub) != 2 {
		t.Fatalf("ListByPublic = %d", len(byPub))
	}
	// enabled 过滤必须可观察（Task 1 审查加固）：原先两行共用公开名
	// "claude-x"，DISTINCT 去重把禁用掩盖——补一枚不同公开名的行并禁用，
	// PublicModels 只能剩 "claude-x"。
	if err := s.ChannelModels().Save(m(z.ID, "claude-y", "up-y")); err != nil {
		t.Fatal(err)
	}
	y, _ := s.ChannelModels().ListByPublic("claude-y")
	if err := s.ChannelModels().SetEnabled(y[0].ID, false); err != nil {
		t.Fatal(err)
	}
	pubs, _ := s.ChannelModels().PublicModels()
	if len(pubs) != 1 || pubs[0] != "claude-x" {
		t.Fatalf("PublicModels = %v (distinct+enabled)", pubs)
	}
}

func TestHasUsableKey(t *testing.T) {
	s := tempStore(t)
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
