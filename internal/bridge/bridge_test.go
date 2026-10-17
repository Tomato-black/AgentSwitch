package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	c, err := cs.Create("anthropic-compatible", "main", "https://api.example.com", 10, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Update(c.ID, "main2", "https://api2.example.com", 10, true, false); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetEnabled(c.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AddKeys(c.ID, "pool", "sk-ant-1234", true); err != nil {
		t.Fatal(err)
	}
	d, err := cs.Get(c.ID)
	if err != nil || len(d.Keys) != 1 {
		t.Fatalf("keys %+v err %v", d.Keys, err)
	}
	if err := cs.SetKeyEnabled(d.Keys[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.AddModel(c.ID, "claude-opus", "up-opus"); err != nil {
		t.Fatal(err)
	}
	mods, _ := cs.A.Store().ChannelModels().ListByChannel(c.ID)
	if len(mods) != 1 {
		t.Fatalf("models %+v", mods)
	}
	if err := cs.UpdateModel(mods[0].ID, "claude-opus-2", "up-opus"); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetModelEnabled(mods[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if err := cs.RemoveModel(mods[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := cs.RemoveKey(d.Keys[0].ID); err != nil {
		t.Fatal(err)
	}
	g := Gateway{A: a}
	if _, err := g.RegenerateToken(); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SaveSettings(17891, 60); err != nil {
		t.Fatal(err)
	}
	if err := cs.Remove(c.ID); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(a.LogPath())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"token regenerated": false, "settings saved": false,
		"channel created": false, "channel updated": false, "channel enabled": false,
		"channel keys added": false, "channel key enabled": false, "channel key removed": false,
		"channel model added": false, "channel model updated": false,
		"channel model enabled": false, "channel model removed": false,
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
	tok, err := g.RegenerateToken()
	if err != nil || tok == st.Token {
		t.Fatalf("%q %v", tok, err)
	}
	_ = storage.NewID
}
