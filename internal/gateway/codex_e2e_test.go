//go:build e2e

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"AgentSwitch/internal/router"
)

// TestCodexCLIResponsesToolRoundTrip 用真实 codex 二进制跑一次工具调用往返。
// 运行：CODEX_E2E=1 go test -tags e2e ./internal/gateway -run TestCodexCLIResponsesToolRoundTrip -v
//
// 上游假渠道固定返回两轮：第一轮吐 exec_command 工具调用，第二轮吐文本 done。
// 若 codex → 网关（/v1/responses）→ chat.completions 的形状有不兼容，往返会中断。
func TestCodexCLIResponsesToolRoundTrip(t *testing.T) {
	if os.Getenv("CODEX_E2E") != "1" {
		t.Skip("set CODEX_E2E=1 to run")
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not installed")
	}
	var turns int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		turns++
		w.Header().Set("Content-Type", "text/event-stream")
		if turns == 1 {
			w.Write([]byte(`data: {"id":"c1","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"echo hi\"}"}}]}}]}` + "\n\n"))
			w.Write([]byte(`data: {"id":"c1","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		w.Write([]byte(`data: {"id":"c2","choices":[{"delta":{"content":"done"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"c2","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer up.Close()

	s := newGW(t, up.URL, func(d *Deps) {
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{
				TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1", ChannelName: "fake",
				SecretKey: "account:k1", Protocol: "openai", BaseURL: up.URL,
				AllowPrivate: true, UpstreamModel: "up-1", Priority: 10,
			}}, nil
		}
	})
	gw := httptest.NewServer(s.Handler())
	defer gw.Close()

	home := t.TempDir()
	work := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".codex"), 0o755)
	// 与接入投影的受管键保持一致（model_catalog_json 未写入：本夹具不生成
	// 模型目录文件，e2e 只验证协议往返，不验证模型选择器）。
	os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(`
model = "pub-1"
model_provider = "agentswitch"
disable_response_storage = true
web_search = "disabled"

[model_providers.agentswitch]
name = "agentswitch"
base_url = "`+gw.URL+`/v1"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "tok"
`), 0o600)

	cmd := exec.Command(codexPath, "exec", "--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox", "run echo hi")
	cmd.Dir = work
	cmd.Stdin = strings.NewReader("") // 避免 codex exec 在非 TTY 下阻塞于读取 stdin
	cmd.Env = append(os.Environ(), "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex failed: %v\n%s", err, out)
	}
	if turns < 2 {
		t.Fatalf("expected tool round trip, turns=%d\n%s", turns, out)
	}
}
