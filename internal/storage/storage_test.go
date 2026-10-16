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
