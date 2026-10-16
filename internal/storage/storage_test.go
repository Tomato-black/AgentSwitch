package storage

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
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
	s2.Close()
}

// TestAccountKeyTailPersistsAcrossReopen 覆盖 spec §12.4 的 key_tail 列
// （migration 002）：Save 落列、Get/List/ListByProvider 回读、轮换（改写 Save）
// 更新尾 4；重开（migrate 幂等重放 v1+v2 不得报错）后仍可读；schema_migrations
// 记录 v1、v2 各一次。
func TestAccountKeyTailPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kt.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	p := Provider{ID: NewID(), Name: "p", Protocol: "anthropic", BaseURL: "https://x.example.com", Enabled: true}
	if err := s1.Providers().Save(p); err != nil {
		t.Fatal(err)
	}
	a := Account{ID: NewID(), ProviderID: p.ID, Name: "a1", SecretKey: "account:k1",
		SecretBackend: "fallback-file", KeyTail: "1234", Priority: 10, Enabled: true}
	if err := s1.Accounts().Save(a); err != nil {
		t.Fatal(err)
	}
	assertKeyTail := func(s Store, want string, when string) {
		t.Helper()
		if g, err := s.Accounts().Get(a.ID); err != nil || g.KeyTail != want {
			t.Fatalf("%s Get: key_tail=%q err=%v", when, g.KeyTail, err)
		}
		if l, err := s.Accounts().List(); err != nil || len(l) != 1 || l[0].KeyTail != want {
			t.Fatalf("%s List: %+v err=%v", when, l, err)
		}
		if l, err := s.Accounts().ListByProvider(p.ID); err != nil || len(l) != 1 || l[0].KeyTail != want {
			t.Fatalf("%s ListByProvider: %+v err=%v", when, l, err)
		}
	}
	assertKeyTail(s1, "1234", "after save")
	// 轮换语义（bridge RotateSecret 的存储侧）：改写 KeyTail 再 Save → 回读更新。
	a.KeyTail = "5678"
	if err := s1.Accounts().Save(a); err != nil {
		t.Fatal(err)
	}
	assertKeyTail(s1, "5678", "after rotate")
	// 幂等重开：v1+v2 已记录时按版本跳过，数据不丢。
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { s2.Close() })
	assertKeyTail(s2, "5678", "after reopen")
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

// TestMigration002UpgradesLegacyV1Database 覆盖升级路径：只有 v1 记录的库
// （migration 001 建的 accounts 无 key_tail 列）再次 Open 必须按版本序补跑
// 002（ALTER TABLE ADD COLUMN），随后 key_tail 可读写。
func TestMigration002UpgradesLegacyV1Database(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(migration001); err != nil {
		t.Fatalf("seed v1 schema: %v", err)
	}
	if _, err := raw.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO schema_migrations(version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	s, err := Open(path)
	if err != nil {
		t.Fatalf("upgrade open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	p := Provider{ID: NewID(), Name: "p", Protocol: "anthropic", BaseURL: "https://x.example.com", Enabled: true}
	if err := s.Providers().Save(p); err != nil {
		t.Fatal(err)
	}
	a := Account{ID: NewID(), ProviderID: p.ID, Name: "a1", SecretKey: "account:k1",
		SecretBackend: "fallback-file", KeyTail: "9999", Priority: 10, Enabled: true}
	if err := s.Accounts().Save(a); err != nil {
		t.Fatalf("save after upgrade: %v", err)
	}
	if g, err := s.Accounts().Get(a.ID); err != nil || g.KeyTail != "9999" {
		t.Fatalf("get after upgrade: key_tail=%q err=%v", g.KeyTail, err)
	}
}

func TestProviderCRUD(t *testing.T) {
	s := tempStore(t)
	p := Provider{ID: NewID(), Name: "p1", Protocol: "anthropic", BaseURL: "https://api.example.com", Enabled: true}
	if err := s.Providers().Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Providers().Get(p.ID)
	if err != nil || got.BaseURL != p.BaseURL {
		t.Fatalf("get: %+v %v", got, err)
	}
	got.BaseURL = "https://api2.example.com"
	got.Protocol = "openai"
	if err := s.Providers().Save(got); err != nil {
		t.Fatal(err)
	}
	if g2, _ := s.Providers().Get(p.ID); g2.BaseURL != "https://api2.example.com" {
		t.Fatal("update not persisted")
	}
	if g2, _ := s.Providers().Get(p.ID); g2.Protocol != "openai" {
		t.Fatalf("protocol update not persisted: %q", g2.Protocol)
	}
	if err := s.Providers().Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Providers().Get(p.ID); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAccountCascadeAndSettings(t *testing.T) {
	s := tempStore(t)
	p := Provider{ID: NewID(), Name: "p", Protocol: "anthropic", BaseURL: "https://x.example.com", Enabled: true}
	s.Providers().Save(p)
	a := Account{ID: NewID(), ProviderID: p.ID, Name: "a1", SecretKey: "account:k1", SecretBackend: "os-keychain", Priority: 10, Enabled: true, CreatedAt: time.Now()}
	if err := s.Accounts().Save(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts().SetEnabled(a.ID, false); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Accounts().Get(a.ID); g.Enabled {
		t.Fatal("SetEnabled failed")
	}
	if err := s.Providers().Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts().Get(a.ID); err != ErrNotFound {
		t.Fatalf("cascade delete missing: %v", err)
	}
	if err := s.Settings().Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Settings().Get("k"); err != nil || v != "v" {
		t.Fatalf("settings: %q %v", v, err)
	}
}

func TestModelUniquePublic(t *testing.T) {
	s := tempStore(t)
	m := ModelMapping{ID: NewID(), PublicModel: "claude-opus", UpstreamModel: "up-opus", Enabled: true}
	if err := s.Models().Save(m); err != nil {
		t.Fatal(err)
	}
	m2 := ModelMapping{ID: NewID(), PublicModel: "claude-opus", UpstreamModel: "other", Enabled: true}
	if err := s.Models().Save(m2); err == nil {
		t.Fatal("duplicate public_model must fail")
	}
	if g, err := s.Models().GetByPublic("claude-opus"); err != nil || g.UpstreamModel != "up-opus" {
		t.Fatalf("getByPublic: %+v %v", g, err)
	}
	if _, err := s.Models().GetByPublic("nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
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
