package storage

import (
	"context"
	"database/sql"
	"errors"
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

// TestMigrationCreatesAccessConfigs 断言单一 v1 首发迁移即含 access_configs
// （接入配置表随建库落地，不再有独立 v3 迁移）。
func TestMigrationCreatesAccessConfigs(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "access.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if s.AccessConfigs() == nil {
		t.Fatal("AccessConfigs() = nil")
	}
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
	// 重开时已记录版本按序跳过，不得重放报错，schema_migrations 必须恰为 [1]。
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

// TestOpenAcceptsFinalSchemaRecordedV1 覆盖删库重建守卫的放行面：手建最终
// schema（执行当前 migrationV1）且已记 v1 的库，Open 必须放行（不误拒）、
// 渠道数据原样保留、迁移不重放。
func TestOpenAcceptsFinalSchemaRecordedV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "final.db")
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
		t.Fatalf("已记 v1 的最终 schema 库应放行: %v", err)
	}
	defer s.Close()
	ds, ok := s.(*dbStore)
	if !ok {
		t.Fatalf("Store = %T", s)
	}
	var n int
	if err := ds.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='channel_secrets'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("channel_secrets 缺失 (n=%d err=%v)", n, err)
	}
	// 已有数据必须原样保留（守卫放行 + 迁移不重放）。
	ch, err := s.Channels().Get("c1")
	if err != nil || ch.Name != "旧渠道" || ch.CredentialRef != "channel:c1" {
		t.Fatalf("渠道数据丢失: %+v err=%v", ch, err)
	}
}

// TestMigrationFinalSchema 断言删库重建后的最终表结构：logical_models /
// logical_model_targets / access_configs 必须存在、channel_keys 必须不存在；
// channels 单 Key 列（credential_ref/preset_id/allow_private_upstream）就位，
// 旧列 priority/type 退役；access_configs 含 settings 列、无 overrides 列
// （spec 2026-10-19 §7）。
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
	for _, want := range []string{"channels", "channel_models", "logical_models", "logical_model_targets", "settings", "channel_secrets", "access_configs"} {
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
	// access_configs：settings 整份文档列就位、旧 overrides 列退役（spec §7）。
	acols := map[string]bool{}
	acrows, err := ds.db.Query(`SELECT name FROM pragma_table_info('access_configs')`)
	if err != nil {
		t.Fatal(err)
	}
	for acrows.Next() {
		var name string
		if err := acrows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		acols[name] = true
	}
	acrows.Close()
	if err := acrows.Err(); err != nil {
		t.Fatal(err)
	}
	if !acols["settings"] {
		t.Fatalf("access_configs missing column settings (have %v)", acols)
	}
	if acols["overrides"] {
		t.Fatalf("access_configs must not have column overrides")
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

func TestAccessConfigRepoSaveGetListDelete(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "a.db"))
	defer s.Close()
	r := s.AccessConfigs()
	saved, err := r.Save(AccessConfig{AppType: "claude", Name: "工作", LogicalModelID: "lm1",
		Settings: `{"env":{"HTTPS_PROXY":"http://127.0.0.1:7890"}}`})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" || saved.CreatedAt == 0 {
		t.Fatal("ID/CreatedAt 未填充")
	}
	got, err := r.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings != `{"env":{"HTTPS_PROXY":"http://127.0.0.1:7890"}}` {
		t.Fatalf("settings 未回读: %s", got.Settings)
	}
	if _, err := r.Save(AccessConfig{AppType: "claude", Name: "工作", LogicalModelID: "lm2"}); err == nil {
		t.Fatal("同 app 同名应 UNIQUE 冲突")
	}
	list, err := r.ListByApp("claude")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListByApp = %v, %v", list, err)
	}
	if err := r.Delete(saved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(saved.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after delete = %v", err)
	}
}

func TestAccessConfigRepoSetCurrentExclusive(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "a.db"))
	defer s.Close()
	r := s.AccessConfigs()
	a, _ := r.Save(AccessConfig{AppType: "claude", Name: "A", LogicalModelID: "lm1"})
	b, _ := r.Save(AccessConfig{AppType: "claude", Name: "B", LogicalModelID: "lm1"})
	c, _ := r.Save(AccessConfig{AppType: "codex", Name: "C", LogicalModelID: "lm1"})
	// codex 需有 current 才能验证「不被 claude 操作波及」（is_current 由 SetCurrent 置位，Save 不自动置）。
	if err := r.SetCurrent("codex", c.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCurrent("claude", a.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCurrent("claude", b.ID); err != nil {
		t.Fatal(err)
	}
	cur, err := r.Current("claude")
	if err != nil || cur.ID != b.ID {
		t.Fatalf("Current = %v, %v", cur, err)
	}
	all, _ := r.ListByApp("claude")
	n := 0
	for _, x := range all {
		if x.IsCurrent {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("is_current 行数 = %d，应为 1", n)
	}
	if cur, _ := r.Current("codex"); cur.ID != c.ID {
		t.Fatal("codex current 不应被 claude 操作波及")
	}
	if err := r.SetCurrent("claude", b.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.SetCurrent("claude", "nonexistent"); err == nil {
		t.Fatal("不存在的 id 应报错")
	}
	if cur, _ := r.Current("claude"); cur.ID != b.ID {
		t.Fatal("失败应回滚，既有 current 应保持 b")
	}
	if err := r.SetCurrent("claude", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Current("claude"); !errors.Is(err, ErrNotFound) {
		t.Fatal("清空后 Current 应 ErrNotFound")
	}
	if err := r.SetCurrent("claude", "nonexistent"); err == nil {
		t.Fatal("不存在的 id 应报错")
	}
}

// TestOpenRejectsV3RecordedDB 覆盖删库重建守卫的「v2/v3 记录」分支：
// schema_migrations 含 2/3 的库一律拒绝。channels/channel_secrets/
// access_configs(settings) 均按最终形态建齐，使既有表形状检查全部通过、
// 拒绝原因唯一落在版本检查上。
func TestOpenRejectsV3RecordedDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentswitch.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
		INSERT INTO schema_migrations(version) VALUES (1),(2),(3);
		CREATE TABLE channels (id TEXT PRIMARY KEY, credential_ref TEXT NOT NULL);
		CREATE TABLE channel_secrets (credential_ref TEXT PRIMARY KEY, ciphertext BLOB NOT NULL);
		CREATE TABLE access_configs (id TEXT PRIMARY KEY, settings TEXT NOT NULL DEFAULT '{}')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); !errors.Is(err, errLegacyDB) {
		t.Fatalf("v2/v3 记录库应拒绝，got %v", err)
	}
}

// TestOpenRejectsAccessConfigsMissingSettingsColumn 覆盖「v3 时代旧形态」：
// 已记 v1、channels/channel_secrets 形状合法，但 access_configs 仍是
// overrides 列——守卫必须拒绝（settings 列检查），不得当最终 schema 放行。
func TestOpenRejectsAccessConfigsMissingSettingsColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentswitch.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟 v3 时代库：schema_migrations 只记 1，但 access_configs 是旧 overrides 形态
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
		INSERT INTO schema_migrations(version) VALUES (1);
		CREATE TABLE channels (id TEXT PRIMARY KEY, credential_ref TEXT NOT NULL);
		CREATE TABLE channel_secrets (credential_ref TEXT PRIMARY KEY, ciphertext BLOB NOT NULL);
		CREATE TABLE access_configs (id TEXT PRIMARY KEY, overrides TEXT NOT NULL DEFAULT '{}')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); !errors.Is(err, errLegacyDB) {
		t.Fatalf("无 settings 列的 access_configs 应拒绝，got %v", err)
	}
}

// TestAccessConfigSettingsRoundTrip 覆盖 settings 整份文档列的存取：写入
// 什么回读什么（JSON 文本直通、不重排）；空 Settings 读写两侧都归一化 "{}"。
func TestAccessConfigSettingsRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "agentswitch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	doc := `{"env":{"A":"1"},"mcpServers":{"x":{"command":"c"}}}`
	saved, err := s.AccessConfigs().Save(AccessConfig{AppType: "claude", Name: "n", LogicalModelID: "lm", Settings: doc})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.AccessConfigs().Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings != doc {
		t.Fatalf("settings 应原样回读: %s", got.Settings)
	}
	// 空 Settings 存取归一化为 "{}"
	empty, err := s.AccessConfigs().Save(AccessConfig{AppType: "claude", Name: "e", LogicalModelID: "lm"})
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := s.AccessConfigs().Get(empty.ID)
	if got2.Settings != "{}" {
		t.Fatalf("空 settings 应为 {}: %s", got2.Settings)
	}
}

// TestOpenRejectsV1MissingChannelSecrets 覆盖 2026-10-17 时代 v1 库：已记 1、
// channels 形状合法，但 channel_secrets/access_configs 皆无（spec §2.3 评审
// 补充）——守卫必须拒绝，不得放行后在首次读密钥/接入配置时报裸 SQL 错。
func TestOpenRejectsV1MissingChannelSecrets(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pre-secrets-v1.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE channels (id TEXT PRIMARY KEY, credential_ref TEXT NOT NULL);
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
		INSERT INTO schema_migrations(version) VALUES (1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, errLegacyDB) {
		t.Fatalf("缺 channel_secrets/access_configs 的已记 v1 库应拒绝，got %v", err)
	}
}

// TestOpenRejectsV1MissingAccessConfigs 覆盖守卫 OR 分支的另一侧：已有
// channel_secrets 但缺 access_configs（中间态残库）同样拒绝。
func TestOpenRejectsV1MissingAccessConfigs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "no-access-v1.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE channels (id TEXT PRIMARY KEY, credential_ref TEXT NOT NULL);
		CREATE TABLE channel_secrets (credential_ref TEXT PRIMARY KEY, ciphertext BLOB NOT NULL);
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
		INSERT INTO schema_migrations(version) VALUES (1);
	`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); !errors.Is(err, errLegacyDB) {
		t.Fatalf("缺 access_configs 的已记 v1 库应拒绝，got %v", err)
	}
}

// TestRouteVersionBumpsOnRouteWrites：路由相关四表（channels / channel_models /
// logical_models / logical_model_targets）的任何写操作都必须推进 RouteVersion——
// app 层路由缓存靠它判断失效，漏一个写入口就会静默走旧配置。只读不推进。
func TestRouteVersionBumpsOnRouteWrites(t *testing.T) {
	s := tempStore(t)
	last := s.RouteVersion()
	bump := func(name string, fn func() error) {
		t.Helper()
		if err := fn(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if v := s.RouteVersion(); v <= last {
			t.Fatalf("%s: RouteVersion 未推进（%d → %d）", name, last, v)
		} else {
			last = v
		}
	}

	var ch Channel
	bump("channel save", func() error {
		c, err := s.Channels().Save(Channel{Name: "c1", Protocol: "anthropic", BaseURL: "http://u1"})
		ch = c
		return err
	})
	var lm LogicalModel
	bump("logical model save", func() error {
		m, err := s.LogicalModels().Save(LogicalModel{Name: "m1", Enabled: true})
		lm = m
		return err
	})
	bump("target save", func() error {
		_, err := s.LogicalModelTargets().Save(LogicalModelTarget{
			LogicalModelID: lm.ID, ChannelID: ch.ID, ModelName: "model-a", Priority: 1,
		})
		return err
	})
	bump("channel model save", func() error {
		_, err := s.ChannelModels().Save(ChannelModel{ChannelID: ch.ID, ModelName: "model-a"})
		return err
	})
	bump("channel setEnabled", func() error { return s.Channels().SetEnabled(ch.ID, false) })
	bump("logical model setEnabled", func() error { return s.LogicalModels().SetEnabled(lm.ID, false) })
	bump("targets deleteByModel", func() error { return s.LogicalModelTargets().DeleteByModel(lm.ID) })

	// 只读查询不得推进版本号（否则缓存永不命中）。
	before := s.RouteVersion()
	if _, err := s.Channels().List(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LogicalModels().Get(lm.ID); err != nil {
		t.Fatal(err)
	}
	if got := s.RouteVersion(); got != before {
		t.Fatalf("只读操作推进了 RouteVersion：%d → %d", before, got)
	}

	bump("logical model delete", func() error { return s.LogicalModels().Delete(lm.ID) })
	bump("channel model delete", func() error {
		ms, err := s.ChannelModels().ListByChannel(ch.ID)
		if err != nil || len(ms) == 0 {
			return err
		}
		return s.ChannelModels().Delete(ms[0].ID)
	})
	bump("channel delete", func() error { return s.Channels().Delete(ch.ID) })
}
