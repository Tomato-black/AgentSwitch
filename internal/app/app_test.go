package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"AgentSwitch/internal/storage"
)

func newTestApp(t *testing.T, upstreamURL string, withAccount bool) *App {
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
	if err := a.Store().Providers().Save(storage.Provider{
		ID: "p1", Name: "up", Protocol: "anthropic", BaseURL: upstreamURL,
		Enabled: true, AllowPrivateUpstream: true,
	}); err != nil {
		t.Fatal(err)
	}
	if withAccount {
		if err := a.Store().Accounts().Save(storage.Account{
			ID: "a1", ProviderID: "p1", Name: "acc", SecretKey: "account:a1",
			SecretBackend: "fallback-file", Priority: 10, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		if err := a.creds.Set("account:a1", []byte("sk-test")); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func TestLifecycleDegradedAndRunning(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	a := newTestApp(t, up.URL, false)
	if a.State().State != StateStopped {
		t.Fatalf("initial %s", a.State().State)
	}
	if err := a.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if a.State().State != StateDegraded {
		t.Fatalf("no accounts → degraded, got %s", a.State().State)
	}
	if err := a.StopGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.State().State != StateStopped {
		t.Fatalf("after stop %s", a.State().State)
	}

	a2 := newTestApp(t, up.URL, true)
	if err := a2.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if a2.State().State != StateRunning {
		t.Fatalf("want running, got %s (%s)", a2.State().State, a2.State().DegradedReason)
	}
	if err := a2.StopGateway(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 有账号但全部禁用 → 可用启用账号为零 → Degraded（spec §5；与 readyz/Router 过滤口径一致）。
	a3 := newTestApp(t, up.URL, true)
	if err := a3.Store().Accounts().SetEnabled("a1", false); err != nil {
		t.Fatal(err)
	}
	if err := a3.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if a3.State().State != StateDegraded || a3.State().DegradedReason == "" {
		t.Fatalf("disabled account → degraded, got %s (%q)", a3.State().State, a3.State().DegradedReason)
	}
}

func TestOpenGeneratesTokenAndRegeneratePersists(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "t.db")
	a, err := Open(Options{DBPath: db, LogPath: filepath.Join(dir, "l.log"), Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	tok := a.currentToken()
	if tok == "" {
		t.Fatal("token must be generated")
	}
	newTok, err := a.RegenerateToken()
	if err != nil || newTok == "" || newTok == tok {
		t.Fatalf("regen: %q %v", newTok, err)
	}
	a.Close()
	a2, err := Open(Options{DBPath: db, LogPath: filepath.Join(dir, "l2.log"), Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	if a2.currentToken() != newTok {
		t.Fatal("regenerated token not persisted")
	}
}

func TestStateCallbacksFire(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	a := newTestApp(t, up.URL, true)
	var got []State
	a.OnState(func(si StateInfo) { got = append(got, si.State) })
	if err := a.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || got[0] != StateStarting || got[len(got)-1] != StateRunning {
		t.Fatalf("states %v", got)
	}
}
