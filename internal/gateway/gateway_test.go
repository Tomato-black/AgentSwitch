package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

// fakeUpstream 起一个 anthropic 形状的假上游，记录被调次数与最后请求体。
func fakeUpstream(t *testing.T, status int, body string, calls *int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		b, _ := io.ReadAll(r.Body)
		t.Logf("upstream got: %s", b)
		w.WriteHeader(status)
		w.Write([]byte(body))
	}))
}

// newGW 组装最小 Deps：一个 anthropic 渠道 + 一枚密钥；tweak 里加更多候选。
func newGW(t *testing.T, baseURL string, tweak func(d *Deps)) *Server {
	t.Helper()
	creds := credential.NewFallbackFile(t.TempDir() + "/s.json")
	_ = creds.Set("account:k1", []byte("sk-test"))
	cand := router.Candidate{ChannelID: "c1", ChannelName: "main", KeyID: "k1", KeyName: "key1",
		SecretKey: "account:k1", Protocol: "anthropic", BaseURL: baseURL,
		AllowPrivate: true, UpstreamModel: "up-1", ChannelPriority: 10, KeyPriority: 10}
	d := Deps{
		Token:  func() string { return "tok" },
		Limits: func() Limits { return Limits{MaxBodyBytes: 1 << 20} },
		// 模型感知：pub-1 才有候选，其余公开名返回空（驱动 404 路径）
		Candidates: func(publicModel string) ([]router.Candidate, error) {
			if publicModel != "pub-1" {
				return nil, nil
			}
			return []router.Candidate{cand}, nil
		},
		Cred:             creds,
		Router:           router.PriorityRouter{},
		AdapterFor:       func(p string) (provider.Adapter, error) { return provider.AdapterForProtocol(p, nil) },
		ListPublicModels: func() []string { return []string{"pub-1"} },
		HasUsableChannel: func() bool { return true },
		Ring:             telemetry.NewRing(10),
	}
	if tweak != nil {
		tweak(&d)
	}
	return New(d)
}

func post(t *testing.T, s *Server, body, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w.Result()
}

func TestMessagesHappyPathRewritesModel(t *testing.T) {
	var calls int
	var lastModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		m := map[string]any{}
		json.Unmarshal(b, &m)
		lastModel, _ = m["model"].(string)
		w.Write([]byte(`{"id":"ok","type":"message"}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	if calls != 1 || lastModel != "up-1" {
		t.Fatalf("calls %d model %q", calls, lastModel)
	}
}

func TestUnknownModelListsPublic(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", nil)
	resp := post(t, s, `{"model":"nope","messages":[]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 || !strings.Contains(string(b), "pub-1") {
		t.Fatalf("404 must list models: %d %s", resp.StatusCode, b)
	}
}

func TestMessagesAuthRequired(t *testing.T) {
	var calls int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","messages":[]}`, "wrong")
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if calls != 0 {
		t.Fatalf("bad token must not reach upstream, calls=%d", calls)
	}
}

func TestModelsEndpointUnion(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", func(d *Deps) {
		d.ListPublicModels = func() []string { return []string{"a", "b"} }
	})
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(w.Result().Body).Decode(&out)
	if len(out.Data) != 2 || out.Data[1].ID != "b" {
		t.Fatalf("models: %+v", out)
	}
}

func TestReadyzDegradedWithoutUsableChannel(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", func(d *Deps) { d.HasUsableChannel = func() bool { return false } })
	req, _ := http.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if w.Result().StatusCode != 503 {
		t.Fatalf("readyz status %d", w.Result().StatusCode)
	}
}

// --- 回归 pin 回补（自 c48c728 版 gateway_test.go 恢复并适配新 Deps）---

// TestRequestLogLineCarriesMetadata 覆盖 I5：handleMessages 的 defer 必须落一行
// slog "request"（request_id/model/upstream_model/channel/account/status/
// duration_ms/error_class/streaming/switches——零密钥、零请求体、零 Authorization）。
func TestRequestLogLineCarriesMetadata(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","content":[]}`))
	}))
	defer up.Close()
	var buf bytes.Buffer // handler 同步跑在测试 goroutine，无需加锁
	s := newGW(t, up.URL, func(d *Deps) {
		d.Log = slog.New(slog.NewJSONHandler(&buf, nil))
	})
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line not JSON: %q", line)
		}
		if m["msg"] != "request" {
			continue
		}
		found = true
		for _, k := range []string{"request_id", "model", "upstream_model", "channel",
			"account", "status", "duration_ms", "error_class", "streaming", "switches"} {
			if _, ok := m[k]; !ok {
				t.Errorf("request line missing %q: %v", k, m)
			}
		}
		if m["request_id"] == "" || m["model"] != "pub-1" || m["upstream_model"] != "up-1" ||
			m["account"] != "key1" || m["channel"] != "main" {
			t.Errorf("request line values: %v", m)
		}
		if st, ok := m["status"].(float64); !ok || int(st) != 200 {
			t.Errorf("status = %v, want 200", m["status"])
		}
		if sw, ok := m["switches"].(float64); !ok || int(sw) != 0 {
			t.Errorf("switches = %v, want 0", m["switches"])
		}
		// 零密钥不变量：上游密钥与网关令牌绝不进日志行。
		if strings.Contains(line, "sk-test") || strings.Contains(line, "tok") {
			t.Fatalf("secret leaked into request log: %s", line)
		}
	}
	if !found {
		t.Fatalf("no \"request\" log line emitted: %q", buf.String())
	}
}

// TestBodyTooLargeRejected：超 MaxBodyBytes（fixture 固定 1<<20）→ 413，且不触达上游。
func TestBodyTooLargeRejected(t *testing.T) {
	var calls int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	big := strings.Repeat("a", (1<<20)+1)
	resp := post(t, s, big, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 413 || calls != 0 {
		t.Fatalf("code=%d calls=%d", resp.StatusCode, calls)
	}
}

// TestConcurrencyLimitRejectsOverflow：占满 MaxConcurrent 后超额请求 503 busy。
func TestConcurrencyLimitRejectsOverflow(t *testing.T) {
	const maxConcurrent = 8
	release := make(chan struct{})
	var once sync.Once
	rel := func() { once.Do(func() { close(release) }) }
	t.Cleanup(rel)
	var inFlight atomic.Int32
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		<-release
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Limits = func() Limits { return Limits{MaxBodyBytes: 1 << 20, MaxConcurrent: maxConcurrent} }
	})
	var wg sync.WaitGroup
	for i := 0; i < maxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			post(t, s, `{"model":"pub-1","messages":[]}`, "tok").Body.Close()
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for inFlight.Load() < maxConcurrent {
		if time.Now().After(deadline) {
			t.Fatalf("inFlight=%d want %d", inFlight.Load(), maxConcurrent)
		}
		time.Sleep(5 * time.Millisecond)
	}
	resp := post(t, s, `{"model":"pub-1","messages":[]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("overflow code=%d want 503", resp.StatusCode)
	}
	rel()
	wg.Wait()
}

// TestModelsEndpointRequiresToken：/v1/models 带令牌 200（含公开模型名），无令牌 401。
func TestModelsEndpointRequiresToken(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", nil)
	req, _ := http.NewRequest("GET", "/v1/models", nil)
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	body, _ := io.ReadAll(w.Result().Body)
	if w.Result().StatusCode != 200 || !strings.Contains(string(body), "pub-1") {
		t.Fatalf("models with token: %d %s", w.Result().StatusCode, body)
	}
	req2, _ := http.NewRequest("GET", "/v1/models", nil)
	w2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(w2, req2)
	if w2.Result().StatusCode != 401 {
		t.Fatalf("unauth models=%d", w2.Result().StatusCode)
	}
}

// TestHealthAndReadyz：healthz 恒 200；readyz 正常路径（有可用渠道/密钥+令牌）200。
func TestHealthAndReadyz(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/healthz", nil))
	if w.Result().StatusCode != 200 {
		t.Fatalf("healthz %d", w.Result().StatusCode)
	}
	w2 := httptest.NewRecorder()
	s.Handler().ServeHTTP(w2, httptest.NewRequest("GET", "/readyz", nil))
	if w2.Result().StatusCode != 200 {
		t.Fatalf("readyz=%d want 200", w2.Result().StatusCode)
	}
}

// TestStreamPassThroughFlushesPerChunk：fake SSE 上游逐块透传并逐读 flush。
func TestStreamPassThroughFlushesPerChunk(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message_start\ndata: {}\n\n"))
		w.Write([]byte("event: message_stop\ndata: {}\n\n"))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	req, _ := http.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"pub-1","stream":true,"messages":[]}`))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	res := w.Result()
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(b), "message_stop") {
		t.Fatalf("stream code=%d body=%s", res.StatusCode, b)
	}
	if !w.Flushed {
		t.Fatal("stream chunks were not flushed per-read")
	}
}

// TestClientCancelPropagatesUpstream：客户端取消即停——上游 ctx 收到取消，
// handler 快速返回（取消不转移，docx §6.3）。
func TestClientCancelPropagatesUpstream(t *testing.T) {
	started := make(chan struct{}, 1)
	upstreamCtxDone := make(chan struct{}, 1)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// net/http 服务端读完请求体后才 arm 断连检测——先读完 body。
		io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
			upstreamCtxDone <- struct{}{}
		case <-time.After(3 * time.Second):
		}
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"pub-1","messages":[]}`))
	req.Header.Set("Authorization", "Bearer tok")
	req = req.WithContext(ctx)
	done := make(chan struct{})
	go func() {
		s.Handler().ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after client cancel")
	}
	select {
	case <-upstreamCtxDone:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream request context not cancelled")
	}
}

// TestUpstreamErrorEnvelopeWrapped：上游非 envelope 错误体被包装成
// {"type":"error"} 形状；400=param 路径原状态码回传、不转移不冷却。
func TestUpstreamErrorEnvelopeWrapped(t *testing.T) {
	var calls int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Cooldowns = router.NewCooldowns()
	})
	resp := post(t, s, `{"model":"pub-1","messages":[]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 400 || calls != 1 {
		t.Fatalf("code=%d calls=%d body=%s", resp.StatusCode, calls, b)
	}
	var env struct {
		Type  string `json:"type"`
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Type != "error" ||
		env.Error.Message != "boom" || env.Error.Type != "invalid_request_error" {
		t.Fatalf("not wrapped envelope: %s", b)
	}
}

// TestUpstreamErrorEnvelopePreserved：上游已是 envelope 形状则原样透传。
func TestUpstreamErrorEnvelopePreserved(t *testing.T) {
	const upstreamBody = `{"type":"error","error":{"type":"invalid_request_error","message":"up says"}}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(upstreamBody))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","messages":[]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 400 || string(b) != upstreamBody {
		t.Fatalf("envelope must pass through verbatim: %d %s", resp.StatusCode, b)
	}
}

// TestAllCandidatesExhaustedReturns502：唯一候选 429 → 冷却后耗尽 → 502 envelope。
func TestAllCandidatesExhaustedReturns502(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","messages":[]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 502 {
		t.Fatalf("exhausted candidates must 502, got %d: %s", resp.StatusCode, b)
	}
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Type != "error" {
		t.Fatalf("502 must be envelope: %s", b)
	}
}

// --- Finding 1 行为 pin：凭据/协议失效候选转移 ---

// TestCredentialFailureCoolsAndFailsOver：死密钥（凭据缺失）按 ClassAuth 冷却
// 并转移下一候选；后续请求冷却过滤，死钥不再触达。
func TestCredentialFailureCoolsAndFailsOver(t *testing.T) {
	var calls int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"id":"ok"}`))
	}))
	defer up.Close()
	var cd *router.Cooldowns
	s := newGW(t, up.URL, func(d *Deps) {
		cd = router.NewCooldowns()
		d.Cooldowns = cd
		dead := router.Candidate{ChannelID: "c1", ChannelName: "main", KeyID: "kdead", KeyName: "dead",
			SecretKey: "account:gone", Protocol: "anthropic", BaseURL: up.URL,
			AllowPrivate: true, UpstreamModel: "up-1", ChannelPriority: 5, KeyPriority: 10}
		live := router.Candidate{ChannelID: "c1", ChannelName: "main", KeyID: "klive", KeyName: "live",
			SecretKey: "account:k1", Protocol: "anthropic", BaseURL: up.URL,
			AllowPrivate: true, UpstreamModel: "up-1", ChannelPriority: 10, KeyPriority: 10}
		d.Candidates = func(publicModel string) ([]router.Candidate, error) {
			if publicModel != "pub-1" {
				return nil, nil
			}
			return []router.Candidate{dead, live}, nil
		}
	})
	resp := post(t, s, `{"model":"pub-1","messages":[]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 || calls != 1 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("must fail over to live key: code=%d calls=%d body=%s", resp.StatusCode, calls, b)
	}
	snap := cd.Snapshot(time.Now())
	if len(snap) != 1 || snap[0].KeyID != "kdead" || snap[0].Reason != "auth" {
		t.Fatalf("dead key must be cooled as auth: %+v", snap)
	}
	// 第二请求：kdead 已冷却被过滤，仅 live 命中——死钥不每请求重试。
	resp2 := post(t, s, `{"model":"pub-1","messages":[]}`, "tok")
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 || calls != 2 {
		t.Fatalf("second request must hit live only: code=%d calls=%d", resp2.StatusCode, calls)
	}
}

// TestAdapterFailureFailsOverWithoutCooldown：协议配置错=渠道侧问题，密钥无辜
// 不冷却，转移下一候选后成功。
func TestAdapterFailureFailsOverWithoutCooldown(t *testing.T) {
	var calls int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"id":"ok"}`))
	}))
	defer up.Close()
	var cd *router.Cooldowns
	s := newGW(t, up.URL, func(d *Deps) {
		cd = router.NewCooldowns()
		d.Cooldowns = cd
		badProto := router.Candidate{ChannelID: "c1", ChannelName: "main", KeyID: "kbad", KeyName: "bad",
			SecretKey: "account:k1", Protocol: "grpc", BaseURL: up.URL,
			AllowPrivate: true, UpstreamModel: "up-1", ChannelPriority: 5, KeyPriority: 10}
		good := router.Candidate{ChannelID: "c1", ChannelName: "main", KeyID: "kgood", KeyName: "good",
			SecretKey: "account:k1", Protocol: "anthropic", BaseURL: up.URL,
			AllowPrivate: true, UpstreamModel: "up-1", ChannelPriority: 10, KeyPriority: 10}
		d.Candidates = func(publicModel string) ([]router.Candidate, error) {
			if publicModel != "pub-1" {
				return nil, nil
			}
			return []router.Candidate{badProto, good}, nil
		}
	})
	resp := post(t, s, `{"model":"pub-1","messages":[]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 || calls != 1 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("must fail over past bad protocol: code=%d calls=%d body=%s", resp.StatusCode, calls, b)
	}
	if snap := cd.Snapshot(time.Now()); len(snap) != 0 {
		t.Fatalf("bad protocol must not cool the key: %+v", snap)
	}
}
