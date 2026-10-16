package app

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"AgentSwitch/internal/storage"
)

// TestConfigChangeTakesEffectWithoutRestart 覆盖 spec §15 验收项 2：
// 网关运行中修改模型别名，下一次请求即走新配置（agent 无感）。
func TestConfigChangeTakesEffectWithoutRestart(t *testing.T) {
	var lastModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastModel = strings.Split(string(b), `"model":"`)[1]
		lastModel = strings.Split(lastModel, `"`)[0]
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"ok"}`))
	}))
	defer up.Close()

	a := newTestApp(t, up.URL, true)
	if err := a.Store().Models().Save(storage.ModelMapping{
		ID: "m1", PublicModel: "alias", UpstreamModel: "model-v1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.StartGateway(); err != nil {
		t.Fatal(err)
	}
	call := func() string {
		req, _ := http.NewRequest("POST", "http://"+a.State().Addr+"/v1/messages",
			strings.NewReader(`{"model":"alias","messages":[]}`))
		req.Header.Set("Authorization", "Bearer "+a.currentToken())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d: %s", resp.StatusCode, b)
		}
		return lastModel
	}
	if got := call(); got != "model-v1" {
		t.Fatalf("first call upstream model %q", got)
	}
	// 运行中改别名 → 下一请求生效（无需重启网关/agent）
	m, err := a.Store().Models().GetByPublic("alias")
	if err != nil {
		t.Fatal(err)
	}
	m.UpstreamModel = "model-v2"
	if err := a.Store().Models().Save(m); err != nil {
		t.Fatal(err)
	}
	if got := call(); got != "model-v2" {
		t.Fatalf("after alias change upstream model %q", got)
	}
}
