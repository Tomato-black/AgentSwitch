package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"AgentSwitch/internal/credential"
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
	// 测试不得写真实 OS 钥匙环：包内把 Manager.Primary 换成本地 0600 文件
	// （Open 的生产装配不动；AllowFallback 本已为 true）。
	a.creds.Primary = credential.NewFallbackFile(filepath.Join(t.TempDir(), "secrets.json"))
	// best-effort 清理历史测试可能已写入真实钥匙环的同名条目（无钥匙环时忽略错误）。
	t.Cleanup(func() { _ = credential.OSKeychain{}.Delete("account:a1") })
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
		if err := a.Creds().Set("account:a1", []byte("sk-test")); err != nil {
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

// TestDoubleStartRejected：运行中再 Start 必须拒绝且不改变状态（状态必须来自实际服务状态，spec §5）。
func TestDoubleStartRejected(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	a := newTestApp(t, up.URL, true)
	if err := a.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if a.State().State != StateRunning {
		t.Fatalf("first start: %s", a.State().State)
	}
	if err := a.StartGateway(); err == nil {
		t.Fatal("second start must be rejected")
	}
	if a.State().State != StateRunning {
		t.Fatalf("rejected start must not change state, got %s", a.State().State)
	}
	if err := a.StopGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.State().State != StateStopped {
		t.Fatalf("after stop %s", a.State().State)
	}
}

// TestSettingsConcurrencyConsistency：RegenerateToken 与 SaveSettings 并发交错下
// load→mutate→save 临界区不得互相覆盖（config.Save 写全部 7 键含 GatewayToken）。
// 断言强度说明：令牌回滚窗口由 settingsMu 临界区构造闭合，无法在单测里直接观测中间态；
// 本测试是行为烟测——无 panic、最终 token 非空、端口值已持久化，纳入 -race 运行。
func TestSettingsConcurrencyConsistency(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	a := newTestApp(t, up.URL, false)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			if _, err := a.SaveSettings(17890, 120); err != nil {
				t.Errorf("save settings: %v", err)
			}
		}()
		go func() {
			defer wg.Done()
			if _, err := a.RegenerateToken(); err != nil {
				t.Errorf("regenerate: %v", err)
			}
		}()
	}
	wg.Wait()
	s, err := a.settings()
	if err != nil {
		t.Fatal(err)
	}
	if s.GatewayToken == "" {
		t.Fatal("token must stay non-empty under concurrent saves")
	}
	if s.Port != 17890 || s.RequestTimeoutSeconds != 120 {
		t.Fatalf("settings not persisted: port=%d timeout=%d", s.Port, s.RequestTimeoutSeconds)
	}
}
