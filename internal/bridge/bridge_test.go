package bridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"AgentSwitch/internal/app"
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

// TestAuditLinesCarryActorAndResult 覆盖 I5 的审计字段（spec §9）：bridge 全部
// 审计行带 actor="ui"（发起方=桌面管理面）与 result="ok"（这些行只在成功后记）。
// 逐条解析日志文件 JSON 行，对已知审计 msg 断言两字段齐全（旧供应商/账号/
// 全局别名服务退役后，覆盖存活服务 Gateway 与 Channels 的写路径）。
func TestAuditLinesCarryActorAndResult(t *testing.T) {
	cs, _ := channelsSvc(t)
	a := cs.A
	c, err := cs.Create("anthropic-compatible", "main", "https://api.example.com", "anthropic", "sk-ant-1234", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Update(c.ID, "main2", "https://api2.example.com", "anthropic", "", true, false); err != nil {
		t.Fatal(err)
	}
	cs.SetEnabled(c.ID, false)
	if err := cs.Update(c.ID, "main2", "https://api2.example.com", "anthropic", "sk-ant-5678", true, false); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetSuggestions(c.ID, []string{"claude-opus", "claude-sonnet"}); err != nil {
		t.Fatal(err)
	}
	g := Gateway{A: a}
	if _, err := g.SaveSettings(17891, 60); err != nil {
		t.Fatal(err)
	}
	cs.Remove(c.ID)
	data, err := os.ReadFile(a.LogPath())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"settings saved":  false,
		"channel created": false, "channel updated": false, "channel enabled": false,
		"channel key set": false, "channel suggestions set": false,
		"channel removed": false,
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line not JSON: %q", line)
		}
		msg, _ := m["msg"].(string)
		if _, known := want[msg]; !known {
			continue
		}
		if m["actor"] != "ui" || m["result"] != "ok" {
			t.Errorf("audit %q: actor=%v result=%v, want actor=ui result=ok", msg, m["actor"], m["result"])
		}
		want[msg] = true
	}
	for msg, ok := range want {
		if !ok {
			t.Errorf("audit line %q not found in log", msg)
		}
	}
}

func TestGatewayStatusShape(t *testing.T) {
	a := tempApp(t)
	g := Gateway{A: a}
	st := g.Status()
	if st.State != "stopped" || st.Token == "" {
		t.Fatalf("%+v", st)
	}
	// Task 13：Status 回显当前配置（供 UI 设置面板与概览片段）
	if st.Port != 17890 || st.TimeoutSeconds != 120 {
		t.Fatalf("settings echo: port=%d timeout=%d", st.Port, st.TimeoutSeconds)
	}
	_ = storage.NewID
}

func TestGatewayHistoryAndExport(t *testing.T) {
	a := tempApp(t)
	g := Gateway{A: a}
	if err := a.Store().RequestHistory().Add(storage.RequestHistoryRecord{
		ID: "r1", StartedAt: 123, Model: "m", Channel: "c", Status: 200,
	}); err != nil {
		t.Fatal(err)
	}
	got := g.History(10, time.Time{})
	if len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("History = %+v", got)
	}
	if got := g.History(10, time.Now()); len(got) != 0 {
		t.Fatalf("History(since=now) = %+v", got)
	}
	t.Setenv("HOME", t.TempDir())
	path, err := g.ExportHistory()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"r1"`) {
		t.Fatalf("export = %s", b)
	}
	if strings.Contains(string(b), "Authorization") || strings.Contains(string(b), "sk-") {
		t.Fatal("export must not contain secrets")
	}
}

func TestGatewayTestLogicalModel(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer up.Close()
	a := tempApp(t)
	if _, err := a.Store().Channels().Save(storage.Channel{
		ID: "c1", Name: "up", Protocol: "anthropic", BaseURL: up.URL,
		CredentialRef: "ref", Enabled: true, AllowPrivateUpstream: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.Creds().Set("ref", []byte("sk-test")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store().LogicalModelTargets().Save(storage.LogicalModelTarget{
		ID: "t1", LogicalModelID: "lm1", ChannelID: "c1", ModelName: "m1", Priority: 1,
	}); err != nil {
		t.Fatal(err)
	}
	res, err := (Gateway{A: a}).TestLogicalModel(context.Background(), "my-code")
	if err != nil || !res.OK || len(res.Targets) != 1 {
		t.Fatalf("probe = %+v, %v", res, err)
	}
}

func TestGatewaySetup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	a := tempApp(t)
	g := Gateway{A: a}
	if s := g.Setup(); s.Channels != 0 || s.LogicalModels != 0 || s.AppliedAccess != 0 {
		t.Fatalf("empty setup = %+v", s)
	}
	ch, err := (Channels{A: a}).Create("", "主渠道", "https://api.example.com", "anthropic", "", true, false)
	if err != nil {
		t.Fatal(err)
	}
	lm, err := (LogicalModels{A: a}).Create("my-code", "", ModelMeta{}, []TargetSpec{{ChannelID: ch.ID, ModelName: "m1"}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := (AccessConfigs{A: a}).Create("claude", "工作", lm.ID, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := (AccessConfigs{A: a}).Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	s := g.Setup()
	if s.Channels != 1 || s.LogicalModels != 1 || s.AppliedAccess != 1 {
		t.Fatalf("setup = %+v", s)
	}
}

func TestGatewayAlertsEnabled(t *testing.T) {
	g := Gateway{A: tempApp(t)}
	if !g.AlertsEnabled() {
		t.Fatal("alerts must default on")
	}
	if err := g.SetAlertsEnabled(false); err != nil {
		t.Fatal(err)
	}
	if g.AlertsEnabled() {
		t.Fatal("alerts must be off after disable")
	}
	if err := g.SetAlertsEnabled(true); err != nil {
		t.Fatal(err)
	}
	if !g.AlertsEnabled() {
		t.Fatal("alerts must be on after enable")
	}
}

func TestOpenLogDir(t *testing.T) {
	old := openPath
	var got string
	openPath = func(path string) error { got = path; return nil }
	defer func() { openPath = old }()
	a := tempApp(t)
	if err := (Gateway{A: a}).OpenLogDir(); err != nil {
		t.Fatal(err)
	}
	if got != filepath.Dir(a.LogPath()) {
		t.Fatalf("opened %q, want %q", got, filepath.Dir(a.LogPath()))
	}
}
