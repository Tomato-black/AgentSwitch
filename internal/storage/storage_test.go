package storage

import (
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
	if err := s.Providers().Save(got); err != nil {
		t.Fatal(err)
	}
	if g2, _ := s.Providers().Get(p.ID); g2.BaseURL != "https://api2.example.com" {
		t.Fatal("update not persisted")
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
