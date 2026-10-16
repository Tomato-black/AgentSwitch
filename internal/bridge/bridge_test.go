package bridge

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/storage"
)

func tempApp(t *testing.T) *app.App {
	t.Helper()
	a, err := app.Open(app.Options{
		DBPath: filepath.Join(t.TempDir(), "b.db"), LogPath: filepath.Join(t.TempDir(), "b.log"), Host: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func TestProviderCRUDAndValidation(t *testing.T) {
	a := tempApp(t)
	ps := Providers{A: a}
	p, err := ps.Create("main", "anthropic", "https://api.example.com", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.Create("bad", "openai", "https://x.example.com", true, false); err == nil {
		t.Fatal("non-anthropic protocol must be rejected in P0-A")
	}
	if _, err := ps.Create("badurl", "anthropic", "http://plain.example.com", true, false); err == nil {
		t.Fatal("non-loopback http must be rejected")
	}
	if err := ps.Update(p.ID, "main2", "https://api2.example.com", false, false); err != nil {
		t.Fatal(err)
	}
	if got := ps.List(); len(got) != 1 || got[0].Name != "main2" || got[0].Enabled {
		t.Fatalf("list %+v", got)
	}
	if err := ps.Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(ps.List()) != 0 {
		t.Fatal("remove failed")
	}
}

func TestAccountSecretLifecycle(t *testing.T) {
	a := tempApp(t)
	// 本地 manager 存密钥：Providers/Accounts 两侧注入同一 store（R-a），级联断言才成立。
	creds := credential.Manager{
		Primary:       credential.NewFallbackFile(filepath.Join(t.TempDir(), "s.json")),
		AllowFallback: true,
	}
	ps := Providers{A: a, Creds: &creds}
	p, _ := ps.Create("main", "anthropic", "https://api.example.com", true, false)
	// KeychainAvailable 注入 false（R-b）：不探测/不写真实钥匙环，backend 断言为 fallback-file。
	as := Accounts{A: a, Creds: &creds, KeychainAvailable: func() bool { return false }}
	av, err := as.Create("acc1", p.ID, "sk-ant-xyz7890", 10, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if av.KeyTail != "7890" || av.SecretBackend != "fallback-file" {
		t.Fatalf("view %+v", av)
	}
	got, err := creds.Get(credential.AccountKey(av.ID))
	if err != nil || string(got) != "sk-ant-xyz7890" {
		t.Fatalf("secret: %q %v", got, err)
	}
	if err := as.SetEnabled(av.ID, false); err != nil {
		t.Fatal(err)
	}
	if l := as.List(); len(l) != 1 || l[0].Enabled {
		t.Fatalf("setEnabled %+v", l)
	}
	if err := as.RotateSecret(av.ID, "sk-ant-newkey99", true); err != nil {
		t.Fatal(err)
	}
	if v, _ := creds.Get(credential.AccountKey(av.ID)); string(v) != "sk-ant-newkey99" {
		t.Fatal("rotate failed")
	}
	// 删除 provider 级联删除账号与密钥
	if err := ps.Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(as.List()) != 0 {
		t.Fatal("cascade failed")
	}
	if _, err := creds.Get(credential.AccountKey(av.ID)); err == nil {
		t.Fatal("cascade must delete secret")
	}
}

func TestModelsCRUDAndUnique(t *testing.T) {
	a := tempApp(t)
	ms := Models{A: a}
	m, err := ms.Create("claude-opus", "up-opus")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ms.Create("claude-opus", "other"); err == nil {
		t.Fatal("duplicate alias must fail")
	}
	if err := ms.Update(m.ID, "claude-opus-2", "up-opus"); err != nil {
		t.Fatal(err)
	}
	if got := ms.List(); len(got) != 1 || got[0].PublicModel != "claude-opus-2" {
		t.Fatalf("%+v", got)
	}
	if err := ms.Remove(m.ID); err != nil {
		t.Fatal(err)
	}
}

func TestAccountProbeStages(t *testing.T) {
	a := tempApp(t)
	// 本地 401 上游：httptest 走 127.0.0.1 回环，Probe dns 阶段对回环免解析，
	// 全离线（不解析/不访问任何真实主机）。
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(up.Close)
	creds := credential.Manager{
		Primary:       credential.NewFallbackFile(filepath.Join(t.TempDir(), "s.json")),
		AllowFallback: true,
	}
	ps := Providers{A: a, Creds: &creds}
	p, err := ps.Create("main", "anthropic", up.URL, true, false)
	if err != nil {
		t.Fatal(err)
	}
	as := Accounts{A: a, Creds: &creds, KeychainAvailable: func() bool { return false }}
	av, err := as.Create("acc1", p.ID, "sk-ant-xyz7890", 10, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if r := as.Test("missing"); r.Stage != "lookup" || r.OK {
		t.Fatalf("lookup miss %+v", r)
	}
	r := as.Test(av.ID)
	if r.Stage != "auth" || r.OK {
		t.Fatalf("want auth-stage 401 from local upstream, got %+v", r)
	}
	if err := creds.Delete(credential.AccountKey(av.ID)); err != nil {
		t.Fatal(err)
	}
	if r := as.Test(av.ID); r.Stage != "credential" || r.OK {
		t.Fatalf("credential miss %+v", r)
	}
}

func TestGatewayStatusShape(t *testing.T) {
	a := tempApp(t)
	g := Gateway{A: a}
	st := g.Status()
	if st.State != "stopped" || st.Token == "" {
		t.Fatalf("%+v", st)
	}
	tok, err := g.RegenerateToken()
	if err != nil || tok == st.Token {
		t.Fatalf("%q %v", tok, err)
	}
	_ = storage.NewID
}
