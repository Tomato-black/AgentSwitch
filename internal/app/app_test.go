package app

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/storage"
)

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
		t.Fatalf("no usable channel keys → degraded, got %s", a.State().State)
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

	// 有密钥但禁用 → 可用渠道/密钥为零 → Degraded（spec §5；与 readyz/Router 过滤口径一致）。
	a3 := newTestApp(t, up.URL, true)
	if err := a3.Store().ChannelKeys().SetEnabled("k1", false); err != nil {
		t.Fatal(err)
	}
	if err := a3.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if a3.State().State != StateDegraded || a3.State().DegradedReason == "" {
		t.Fatalf("disabled account → degraded, got %s (%q)", a3.State().State, a3.State().DegradedReason)
	}
}

// TestOpenCreatesNestedConfigDir 覆盖首启目录创建（C1）：全新机器上配置目录
// 父链不存在时，Open 必须先 MkdirAll 再开库——否则 sql.Open+Ping 直接失败
// "unable to open database file (14)"。同时断言目录权限收窄（0700 语义：
// 无 group/other 访问）。
func TestOpenCreatesNestedConfigDir(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "a", "b", "c")
	db := filepath.Join(nested, "x.db")
	if _, err := os.Stat(filepath.Join(root, "a")); !os.IsNotExist(err) {
		t.Fatalf("precondition: parent chain must not exist, stat=%v", err)
	}
	logPath := filepath.Join(nested, "logs", "app.log")
	a, err := Open(Options{DBPath: db, LogPath: logPath, Version: "test", Host: "127.0.0.1"})
	if err != nil {
		t.Fatalf("Open on fresh machine (missing parent dirs): %v", err)
	}
	t.Cleanup(func() { a.Close() })
	fi, err := os.Stat(nested)
	if err != nil || !fi.IsDir() {
		t.Fatalf("config dir not created: %v", err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("config dir perm = %o, want no group/other access (0700)", perm)
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatalf("db file not created: %v", err)
	}
	// 日志目录（telemetry.NewLogger 建）与配置目录同权：0700 语义一致。
	lf, err := os.Stat(filepath.Join(nested, "logs"))
	if err != nil || !lf.IsDir() {
		t.Fatalf("log dir not created: %v", err)
	}
	if perm := lf.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("log dir perm = %o, want no group/other access (0700)", perm)
	}
	// LogPath() 透传 opts（I3 概览日志提示的数据源）。
	if a.LogPath() != logPath {
		t.Fatalf("LogPath() = %q, want %q", a.LogPath(), logPath)
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

// TestStartGatewayFailsOnBusyPortThenRecovers 覆盖 I3（spec §5 六态）：端口被
// 占 → StartGateway 返回错误且状态 Failed（带 reason）；释放端口后再 Start
// 必须成功变 Running——Failed 是可恢复态（StopGateway 对 gw==nil 幂等，不会
// 挡住二次启动）。端口取 :0 临时占用，避免与并行测试包的 17890 撞车。
func TestStartGatewayFailsOnBusyPortThenRecovers(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	a := newTestApp(t, up.URL, true) // 启用账号在库 → 恢复后应为 Running
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	busy := ln.Addr().(*net.TCPAddr).Port
	if _, err := a.SaveSettings(busy, 120); err != nil {
		t.Fatal(err)
	}
	if err := a.StartGateway(); err == nil {
		t.Fatal("start on occupied port must fail")
	}
	si := a.State()
	if si.State != StateFailed || si.DegradedReason == "" {
		t.Fatalf("want failed with reason, got %+v", si)
	}
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.StartGateway(); err != nil {
		t.Fatalf("start after port released: %v", err)
	}
	si = a.State()
	if si.State != StateRunning {
		t.Fatalf("failed must be recoverable: got %s (%s)", si.State, si.DegradedReason)
	}
	if err := a.StopGateway(context.Background()); err != nil {
		t.Fatal(err)
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
