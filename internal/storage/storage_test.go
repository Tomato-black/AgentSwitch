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
	// 重开时已记录版本按序跳过，不得重放报错，schema_migrations 必须恰为 [1 2]。
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
	if rows.Err() != nil || len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("schema_migrations = %v, want [1 2]", got)
	}
}

// TestMigrateV1ToV2：既有 v1 库（渠道化重构产物）经 Open 自动升级到 v2
// （channel_secrets 加密凭据表），免删库重建。
func TestMigrateV1ToV2(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(migrationV1); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO schema_migrations(version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO channels(id,name,protocol,base_url,credential_ref,enabled,created_at,updated_at)
		VALUES ('c1','旧渠道','anthropic','http://x','channel:c1',1,1,1)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("v1->v2 open: %v", err)
	}
	defer s.Close()
	ds, ok := s.(*dbStore)
	if !ok {
		t.Fatalf("Store = %T", s)
	}
	var n int
	if err := ds.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='channel_secrets'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("channel_secrets missing after migrate (n=%d err=%v)", n, err)
	}
	// 旧渠道数据必须原样保留。
	ch, err := s.Channels().Get("c1")
	if err != nil || ch.Name != "旧渠道" || ch.CredentialRef != "channel:c1" {
		t.Fatalf("v1 row lost: %+v err=%v", ch, err)
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
	for _, want := range []string{"channels", "channel_models", "logical_models", "logical_model_targets", "settings", "channel_secrets"} {
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

// TestOpenRejectsPreChannelsV1 覆盖 providers 时代旧库：schema_migrations 已记
// v1 且无 channel_keys，但也没有最终形态的 channels——migrate 会跳过 v1，
// 守卫必须拒绝，否则启动后保存渠道报「no such table: channels」。
func TestOpenRejectsPreChannelsV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-channels.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// 复刻 8a7228a 时代：migration001（providers/accounts）+ 已记 v1。
	if _, err := db.Exec(`
		CREATE TABLE providers (id TEXT PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE accounts (id TEXT PRIMARY KEY, provider_id TEXT NOT NULL);
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
		INSERT INTO schema_migrations(version) VALUES (1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("Open must reject pre-channels DB with schema_migrations v1 already recorded")
	}
	if !strings.Contains(err.Error(), "删除") {
		t.Fatalf("error must mention 删除: %v", err)
	}
}

// TestOpenRejectsV1WithoutCredentialRef 覆盖「v1 已记 + 有 channels 但缺
// credential_ref」的残缺形态：不得静默当新库用。
func TestOpenRejectsV1WithoutCredentialRef(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stale-v1.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE channels (id TEXT PRIMARY KEY, name TEXT NOT NULL);
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
		INSERT INTO schema_migrations(version) VALUES (1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err == nil {
		s.Close()
		t.Fatal("Open must reject v1 DB whose channels lack credential_ref")
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

// ---- LogicalModel / LogicalModelTarget 仓储（Task 3）----

func logicalFixture(t *testing.T, s Store) LogicalModel {
	t.Helper()
	m, err := s.LogicalModels().Save(LogicalModel{ID: NewID(), Name: "claude-sonnet", Description: "default", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestLogicalModelNameUnique：name UNIQUE——不同 ID 同名第二次 Save 必须报错。
func TestLogicalModelNameUnique(t *testing.T) {
	s := tempStore(t)
	logicalFixture(t, s)
	if _, err := s.LogicalModels().Save(LogicalModel{ID: NewID(), Name: "claude-sonnet", Enabled: true}); err == nil {
		t.Fatal("duplicate logical model name must fail")
	}
}

// TestLogicalModelGetByName：Save 后 GetByName 命中并回读字段；未知名返回 ErrNotFound。
func TestLogicalModelGetByName(t *testing.T) {
	s := tempStore(t)
	in := logicalFixture(t, s)
	got, err := s.LogicalModels().GetByName("claude-sonnet")
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != in.ID || got.Name != in.Name || got.Description != "default" || !got.Enabled {
		t.Fatalf("GetByName = %+v, want %+v", got, in)
	}
	if got.CreatedAt == 0 || got.UpdatedAt == 0 {
		t.Fatalf("timestamps unset: %+v", got)
	}
	if _, err := s.LogicalModels().GetByName("unknown-model"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

// TestLogicalModelDeleteCascadeTargets：删逻辑模型后其目标链必须清空（FK ON DELETE CASCADE）。
func TestLogicalModelDeleteCascadeTargets(t *testing.T) {
	s := tempStore(t)
	a, z := channelFixture(t, s)
	m := logicalFixture(t, s)
	for _, tg := range []LogicalModelTarget{
		{ID: NewID(), LogicalModelID: m.ID, ChannelID: a.ID, ModelName: "up-a", Priority: 1},
		{ID: NewID(), LogicalModelID: m.ID, ChannelID: z.ID, ModelName: "up-b", Priority: 2},
	} {
		if _, err := s.LogicalModelTargets().Save(tg); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.LogicalModels().Delete(m.ID); err != nil {
		t.Fatal(err)
	}
	ts, err := s.LogicalModelTargets().ListByModel(m.ID)
	if err != nil || len(ts) != 0 {
		t.Fatalf("cascade: %v %v", ts, err)
	}
}

// TestTargetListOrdered：ListByModel 按 priority 升序（priority 越小越优先）。
func TestTargetListOrdered(t *testing.T) {
	s := tempStore(t)
	a, z := channelFixture(t, s)
	m := logicalFixture(t, s)
	// 插入顺序故意乱序：30 → 10 → 20。
	for _, tg := range []LogicalModelTarget{
		{ID: NewID(), LogicalModelID: m.ID, ChannelID: a.ID, ModelName: "up-a", Priority: 30},
		{ID: NewID(), LogicalModelID: m.ID, ChannelID: z.ID, ModelName: "up-b", Priority: 10},
		{ID: NewID(), LogicalModelID: m.ID, ChannelID: a.ID, ModelName: "up-c", Priority: 20},
	} {
		if _, err := s.LogicalModelTargets().Save(tg); err != nil {
			t.Fatal(err)
		}
	}
	ts, err := s.LogicalModelTargets().ListByModel(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []int{10, 20, 30}
	if len(ts) != len(want) {
		t.Fatalf("ListByModel = %+v", ts)
	}
	for i, p := range want {
		if ts[i].Priority != p {
			t.Fatalf("ListByModel priorities = %+v, want %v", ts, want)
		}
	}
}
