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
// 网关运行中修改目标上游模型，下一次请求即走新配置（agent 无感）。
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

	// newTestApp 夹具：逻辑模型 "alias" → t1 → c1/model-v1。
	a := newTestApp(t, up.URL, true)
	if err := a.StartGateway(); err != nil {
		t.Fatal(err)
	}
	call := func() string {
		req, _ := http.NewRequest("POST", "http://"+a.State().Addr+"/v1/messages",
			strings.NewReader(`{"model":"alias","messages":[{"role":"user","content":"hi"}]}`))
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
	// 运行中改目标上游模型 → 下一请求生效（无需重启网关/agent）。
	if _, err := a.Store().LogicalModelTargets().Save(storage.LogicalModelTarget{
		ID: "t1", LogicalModelID: "lm1", ChannelID: "c1", ModelName: "model-v2", Priority: 10,
	}); err != nil {
		t.Fatal(err)
	}
	if got := call(); got != "model-v2" {
		t.Fatalf("after target change upstream model %q", got)
	}
}
