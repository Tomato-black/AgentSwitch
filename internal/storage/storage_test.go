package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
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

// TestMigrationFinalSchema 断言删库重建后的最终表结构：logical_models /
// logical_model_targets 必须存在、channel_keys 必须不存在；channels 单 Key
// 列（credential_ref/preset_id/allow_private_upstream）就位，旧列
// priority/type 退役。
func TestMigrationFinalSchema(t *testing.T) {
	s := tempStore(t)
	ds, ok := s.(*dbStore)
	if !ok {
		t.Fatalf("Store = %T, want *dbStore", s)
	}
	tables := map[string]bool{}
	rows, err := ds.db.Query(`SELECT name FROM sqlite_master WHERE type='table'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"channels", "channel_models", "logical_models", "logical_model_targets", "settings"} {
		if !tables[want] {
			t.Fatalf("missing table %s (have %v)", want, tables)
		}
	}
	if tables["channel_keys"] {
		t.Fatal("channel_keys must not exist in final schema")
	}
	cols := map[string]bool{}
	crows, err := ds.db.Query(`SELECT name FROM pragma_table_info('channels')`)
	if err != nil {
		t.Fatal(err)
	}
	for crows.Next() {
		var name string
		if err := crows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		cols[name] = true
	}
	crows.Close()
	for _, want := range []string{"credential_ref", "preset_id", "allow_private_upstream"} {
		if !cols[want] {
			t.Fatalf("channels missing column %s (have %v)", want, cols)
		}
	}
	for _, gone := range []string{"priority", "type"} {
		if cols[gone] {
			t.Fatalf("channels must not have column %s", gone)
		}
	}
}

// TestOpenRejectsLegacySchema 覆盖删库重建的旧库守卫：DB 里存在 channel_keys
// （多 Key 旧 schema）时 Open 必须拒绝并提示删除，不得静默清库或就地迁移。
func TestOpenRejectsLegacySchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	// 手工造一个带 channel_keys 的旧库（不经过 Open，避免旧 schema 干扰）。
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE channel_keys (id TEXT PRIMARY KEY, channel_id TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("Open must reject legacy schema containing channel_keys")
	}
	if !strings.Contains(err.Error(), "删除") {
		t.Fatalf("error must mention 删除: %v", err)
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

// ---- 逻辑模型重构（migration v1，spec 2026-10-17-logical-models §5）----

func channelFixture(t *testing.T, s Store) (Channel, Channel) {
	t.Helper()
	a := Channel{ID: NewID(), Name: "beta", Protocol: "anthropic", BaseURL: "https://b.example.com",
		CredentialRef: "account:b1", PresetID: "custom", Enabled: true}
	z := Channel{ID: NewID(), Name: "alpha", Protocol: "anthropic", BaseURL: "https://api.anthropic.com",
		CredentialRef: "account:a1", PresetID: "anthropic", Enabled: true}
	for _, c := range []Channel{a, z} {
		if _, err := s.Channels().Save(c); err != nil {
			t.Fatal(err)
		}
	}
	return a, z
}

func TestChannelSaveRoundTrip(t *testing.T) {
	s := tempStore(t)
	in := Channel{ID: NewID(), Name: "solo", Protocol: "openai", BaseURL: "https://x.example.com",
		CredentialRef: "account:x1", PresetID: "openai-compatible", Enabled: true, AllowPrivateUpstream: true}
	saved, err := s.Channels().Save(in)
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID != in.ID || saved.CreatedAt == 0 || saved.UpdatedAt == 0 {
		t.Fatalf("Save returned: %+v", saved)
	}
	got, err := s.Channels().Get(in.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := in
	want.CreatedAt, want.UpdatedAt = got.CreatedAt, got.UpdatedAt
	if got != want {
		t.Fatalf("Get = %+v, want %+v", got, want)
	}
}

func TestChannelLifecycle(t *testing.T) {
	s := tempStore(t)
	a, z := channelFixture(t, s)
	list, err := s.Channels().List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("List = %+v", list)
	}
	if err := s.Channels().SetEnabled(z.ID, false); err != nil {
		t.Fatal(err)
	}
	got, err := s.Channels().Get(z.ID)
	if err != nil || got.Enabled {
		t.Fatalf("SetEnabled(false): %+v %v", got, err)
	}
	if err := s.Channels().Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Channels().Get(a.ID); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestChannelDeleteCascadeModels(t *testing.T) {
	s := tempStore(t)
	a, _ := channelFixture(t, s)
	for _, name := range []string{"m1", "m2"} {
		if _, err := s.ChannelModels().Save(ChannelModel{ID: NewID(), ChannelID: a.ID, ModelName: name}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Channels().Delete(a.ID); err != nil {
		t.Fatal(err)
	}
	ms, err := s.ChannelModels().ListByChannel(a.ID)
	if err != nil || len(ms) != 0 {
		t.Fatalf("cascade: %v %v", ms, err)
	}
}

func TestChannelModelSuggestions(t *testing.T) {
	s := tempStore(t)
	a, z := channelFixture(t, s)
	for _, name := range []string{"claude-x", "claude-a", "claude-m"} {
		if _, err := s.ChannelModels().Save(ChannelModel{ID: NewID(), ChannelID: a.ID, ModelName: name}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.ChannelModels().Save(ChannelModel{ID: NewID(), ChannelID: z.ID, ModelName: "other"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.ChannelModels().Suggestions(a.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"claude-a", "claude-m", "claude-x"}
	if len(got) != len(want) {
		t.Fatalf("Suggestions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Suggestions = %v, want %v", got, want)
		}
	}
}

func TestChannelModelUniquePerChannel(t *testing.T) {
	s := tempStore(t)
	a, z := channelFixture(t, s)
	if _, err := s.ChannelModels().Save(ChannelModel{ID: NewID(), ChannelID: a.ID, ModelName: "claude-x"}); err != nil {
		t.Fatal(err)
	}
	// 同渠道同模型名 → UNIQUE(channel_id, model_name) 冲突
	if _, err := s.ChannelModels().Save(ChannelModel{ID: NewID(), ChannelID: a.ID, ModelName: "claude-x"}); err == nil {
		t.Fatal("duplicate (channel,model_name) must fail")
	}
	// 跨渠道同名 → 允许
	if _, err := s.ChannelModels().Save(ChannelModel{ID: NewID(), ChannelID: z.ID, ModelName: "claude-x"}); err != nil {
		t.Fatal(err)
	}
	ms, _ := s.ChannelModels().ListByChannel(z.ID)
	if len(ms) != 1 || ms[0].ModelName != "claude-x" {
		t.Fatalf("ListByChannel = %+v", ms)
	}
}

func TestHasUsableKey(t *testing.T) {
	s := tempStore(t)
	ok, err := s.Channels().HasUsableKey()
	if err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v", ok, err)
	}
	a, z := channelFixture(t, s)
	ok, err = s.Channels().HasUsableKey()
	if err != nil || !ok {
		t.Fatalf("usable: ok=%v err=%v", ok, err)
	}
	for _, id := range []string{a.ID, z.ID} {
		if err := s.Channels().SetEnabled(id, false); err != nil {
			t.Fatal(err)
		}
	}
	ok, err = s.Channels().HasUsableKey()
	if err != nil || ok {
		t.Fatalf("all disabled: ok=%v err=%v", ok, err)
	}
}
