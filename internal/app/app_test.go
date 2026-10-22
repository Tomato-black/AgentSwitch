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

	"AgentSwitch/internal/storage"
)

// openApp 开空库应用（无渠道/模型夹具）；凭据走库内 AES-256-GCM 加密存储，
// 主密钥在临时库目录，无外部依赖。
func openApp(t *testing.T) *App {
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
	// 探测空闲端口注入：测试不与本机任何在跑实例（dev/正式版）抢 17890。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cur, err := a.CurrentSettings()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveSettings(port, cur.RequestTimeoutSeconds); err != nil {
		t.Fatal(err)
	}
	return a
}

// newTestApp 夹具：渠道 c1（withKey 时带 credential_ref=account:a1）+
// enabled 逻辑模型 lm1"alias" + 目标 t1→c1/model-v1。withKey=false 即
// 无可用凭据（HasUsableKey 假）。
func newTestApp(t *testing.T, upstreamURL string, withKey bool) *App {
	t.Helper()
	a := openApp(t)
	ref := ""
	if withKey {
		ref = "account:a1"
	}
	if _, err := a.Store().Channels().Save(storage.Channel{
		ID: "c1", Name: "up", Protocol: "anthropic",
		BaseURL: upstreamURL, CredentialRef: ref,
		Enabled: true, AllowPrivateUpstream: true,
	}); err != nil {
		t.Fatal(err)
	}
	if withKey {
		if err := a.Creds().Set("account:a1", []byte("sk-test")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{
		ID: "lm1", Name: "alias", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store().LogicalModelTargets().Save(storage.LogicalModelTarget{
		ID: "t1", LogicalModelID: "lm1", ChannelID: "c1", ModelName: "model-v1", Priority: 10,
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

// TestCandidatesFromLogicalModel：两渠道两目标，candidates 长度=2 且按 Priority
// 排序、UpstreamModel=目标 model_name、SecretKey=渠道 CredentialRef。
func TestCandidatesFromLogicalModel(t *testing.T) {
	a := openApp(t)
	for _, ch := range []storage.Channel{
		{ID: "c1", Name: "up1", Protocol: "anthropic", BaseURL: "http://u1", CredentialRef: "ref-1", Enabled: true, AllowPrivateUpstream: true},
		{ID: "c2", Name: "up2", Protocol: "openai", BaseURL: "http://u2", CredentialRef: "ref-2", Enabled: true},
	} {
		if _, err := a.Store().Channels().Save(ch); err != nil {
			t.Fatal(err)
		}
	}
	lm, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	// 故意先插 priority=2 再插 1——断言输出按 Priority 升序而非插入序。
	if _, err := a.Store().LogicalModelTargets().Save(storage.LogicalModelTarget{
		ID: "t2", LogicalModelID: lm.ID, ChannelID: "c2", ModelName: "model-b", Priority: 2,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store().LogicalModelTargets().Save(storage.LogicalModelTarget{
		ID: "t1", LogicalModelID: lm.ID, ChannelID: "c1", ModelName: "model-a", Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}
	cands, err := a.candidates("m")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 2 {
		t.Fatalf("len=%d want 2 (%+v)", len(cands), cands)
	}
	first, second := cands[0], cands[1]
	if first.Priority != 1 || second.Priority != 2 {
		t.Fatalf("priority order wrong: %d,%d", first.Priority, second.Priority)
	}
	if first.UpstreamModel != "model-a" || second.UpstreamModel != "model-b" {
		t.Fatalf("upstream models: %q,%q", first.UpstreamModel, second.UpstreamModel)
	}
	if first.SecretKey != "ref-1" || second.SecretKey != "ref-2" {
		t.Fatalf("secret keys: %q,%q", first.SecretKey, second.SecretKey)
	}
	// 装配映射契约（Task 6 消费字段）：TargetID/LogicalModel/ChannelID 随目标链。
	if first.TargetID != "t1" || first.LogicalModel != "m" || first.ChannelID != "c1" {
		t.Fatalf("first mapping: %+v", first)
	}
	if second.TargetID != "t2" || second.ChannelID != "c2" {
		t.Fatalf("second mapping: %+v", second)
	}
	if first.ChannelName != "up1" || first.Protocol != "anthropic" || first.BaseURL != "http://u1" || !first.AllowPrivate {
		t.Fatalf("channel fields: %+v", first)
	}
}

// TestCandidatesUnknownModelEmpty：未登记逻辑模型名 → 空切片（gateway 走「无候选」路径）。
func TestCandidatesUnknownModelEmpty(t *testing.T) {
	a := openApp(t)
	cands, err := a.candidates("nope")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 0 {
		t.Fatalf("unknown model must yield empty, got %+v", cands)
	}
}

// TestPublicModelsListsLogicalNames：两个 enabled + 一个 disabled → 仅前两个。
func TestPublicModelsListsLogicalNames(t *testing.T) {
	a := openApp(t)
	for _, m := range []storage.LogicalModel{
		{ID: "lm1", Name: "m1", Enabled: true},
		{ID: "lm2", Name: "m2", Enabled: true},
		{ID: "lm3", Name: "m3", Enabled: false},
	} {
		if _, err := a.Store().LogicalModels().Save(m); err != nil {
			t.Fatal(err)
		}
	}
	got := a.publicModels()
	if len(got) != 2 {
		t.Fatalf("publicModels=%v want 2 names", got)
	}
	seen := map[string]bool{}
	for _, n := range got {
		seen[n] = true
	}
	if !seen["m1"] || !seen["m2"] || seen["m3"] {
		t.Fatalf("publicModels=%v want {m1,m2}", got)
	}
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
		t.Fatalf("no usable channel → degraded, got %s", a.State().State)
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

	// 渠道禁用 → 可用渠道为零 → Degraded（spec §5；与 readyz/Router 过滤口径一致）。
	a3 := newTestApp(t, up.URL, true)
	if err := a3.Store().Channels().SetEnabled("c1", false); err != nil {
		t.Fatal(err)
	}
	if err := a3.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if a3.State().State != StateDegraded || a3.State().DegradedReason == "" {
		t.Fatalf("disabled channel → degraded, got %s (%q)", a3.State().State, a3.State().DegradedReason)
	}
	// Degraded 仍占端口——停掉再起 a4（默认端口同一 17890）。
	if err := a3.StopGateway(context.Background()); err != nil {
		t.Fatal(err)
	}

	// 无 enabled 逻辑模型 → 同守卫 Degraded（「至少一个 enabled 逻辑模型且至少一个可用渠道」）。
	a4 := newTestApp(t, up.URL, true)
	if err := a4.Store().LogicalModels().SetEnabled("lm1", false); err != nil {
		t.Fatal(err)
	}
	if err := a4.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if a4.State().State != StateDegraded || a4.State().DegradedReason == "" {
		t.Fatalf("no enabled logical model → degraded, got %s (%q)", a4.State().State, a4.State().DegradedReason)
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
	a := newTestApp(t, up.URL, true) // 启用渠道+逻辑模型在库 → 恢复后应为 Running
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
// load→mutate→save 临界区不得互相覆盖（config.Save 写全部键含 GatewayToken）。
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

// TestCandidatesResolveDesktopAlias：入站名命中桌面别名表时解析回逻辑模型
// （Claude Desktop 接入只接受 claude-* 角色路由 ID，网关侧按
// storage.DesktopAliasesSettingsKey 别名表转回逻辑模型）。
func TestCandidatesResolveDesktopAlias(t *testing.T) {
	a := newTestApp(t, "http://u", true) // 逻辑模型 "alias"
	if err := a.Store().Settings().Set(storage.DesktopAliasesSettingsKey,
		`{"claude-sonnet-5":"alias"}`); err != nil {
		t.Fatal(err)
	}
	cands, err := a.candidates("claude-sonnet-5")
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || cands[0].LogicalModel != "alias" || cands[0].UpstreamModel != "model-v1" {
		t.Fatalf("别名未解析: %+v", cands)
	}
	// 未命中别名的逻辑模型直查不受影响。
	direct, err := a.candidates("alias")
	if err != nil || len(direct) != 1 {
		t.Fatalf("直查被影响: %v %v", direct, err)
	}
	// 别名指向已删模型 → 空候选（404 unknown model）。
	a.Store().Settings().Set(storage.DesktopAliasesSettingsKey, `{"claude-sonnet-5":"gone"}`)
	gone, err := a.candidates("claude-sonnet-5")
	if err != nil || len(gone) != 0 {
		t.Fatalf("失效别名应空候选: %v %v", gone, err)
	}
}

// TestPublicModelsIncludeDesktopAliases：/v1/models 公开列表在逻辑模型名之外
// 追加别名（Desktop 靠模型列表校验角色路由 ID）。
func TestPublicModelsIncludeDesktopAliases(t *testing.T) {
	a := newTestApp(t, "http://u", true)
	a.Store().Settings().Set(storage.DesktopAliasesSettingsKey, `{"claude-sonnet-5":"alias"}`)
	got := map[string]bool{}
	for _, m := range a.publicModels() {
		got[m] = true
	}
	if !got["alias"] || !got["claude-sonnet-5"] {
		t.Fatalf("公开模型列表缺别名: %v", got)
	}
	// 禁用的逻辑模型不出现，但其别名也不出现（别名跟随逻辑模型启用态）。
	a.Store().LogicalModels().SetEnabled("lm1", false)
	got = map[string]bool{}
	for _, m := range a.publicModels() {
		got[m] = true
	}
	if got["alias"] || got["claude-sonnet-5"] {
		t.Fatalf("禁用模型与其别名不得出现: %v", got)
	}
}
