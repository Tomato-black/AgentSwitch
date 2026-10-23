package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/protocol"
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
// mapStore 是测试用内存凭据后端（生产为库内 AES-256-GCM 存储，见 storage.SecretRepo）。
type mapStore struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMapStore() *mapStore { return &mapStore{m: map[string][]byte{}} }

func (s *mapStore) Set(k string, v []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}

func (s *mapStore) Get(k string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	if !ok {
		return nil, credential.ErrNotFound
	}
	return v, nil
}

func (s *mapStore) Delete(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, k)
	return nil
}

func newGW(t *testing.T, baseURL string, tweak func(d *Deps)) *Server {
	t.Helper()
	creds := newMapStore()
	_ = creds.Set("account:k1", []byte("sk-test"))
	cand := router.Candidate{TargetID: "t1", LogicalModel: "pub-1",
		ChannelID: "c1", ChannelName: "main", SecretKey: "account:k1",
		Protocol: "anthropic", BaseURL: baseURL,
		AllowPrivate: true, UpstreamModel: "up-1", Priority: 10}
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
		Cred:   creds,
		Router: router.PriorityRouter{},
		AdapterFor: func(p string) (provider.Adapter, error) {
			switch p {
			case "anthropic":
				return provider.NewAnthropicAdapter(), nil
			case "openai":
				return provider.NewOpenAIAdapter(nil), nil
			default:
				return nil, fmt.Errorf("unknown protocol %q", p)
			}
		},
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
	resp := post(t, s, `{"model":"nope","messages":[{"role":"user","content":"hi"}]}`, "tok")
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
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "wrong")
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
		// 单 Key 模型下无密钥名：Account 语义收敛为渠道名（Task 5 注记，此处 pin）。
		if m["request_id"] == "" || m["model"] != "pub-1" || m["upstream_model"] != "up-1" ||
			m["account"] != "main" || m["channel"] != "main" {
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

// TestUpstreamErrorLogCarriesBody 覆盖 400 诊断：上游回 400 时，除 request 行外
// 还必须落一行 "upstream error"，带上截断后的上游错误体——否则 param 类透传
// 在日志里只剩分类，无法知道上游到底拒了什么（2026-10-22 mimo 400 排查需求）。
func TestUpstreamErrorLogCarriesBody(t *testing.T) {
	const upstreamBody = `{"error":{"message":"context_length_exceeded: too many tokens","type":"invalid_request_error"}}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write([]byte(upstreamBody))
	}))
	defer up.Close()
	var buf bytes.Buffer // handler 同步跑在测试 goroutine，无需加锁
	s := newGW(t, up.URL, func(d *Deps) {
		d.Log = slog.New(slog.NewJSONHandler(&buf, nil))
	})
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	found := false
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line not JSON: %q", line)
		}
		if m["msg"] != "upstream error" {
			continue
		}
		found = true
		if m["request_id"] == "" || m["channel"] != "main" || m["upstream_model"] != "up-1" {
			t.Errorf("upstream error line identity: %v", m)
		}
		if st, ok := m["status"].(float64); !ok || int(st) != 400 {
			t.Errorf("status = %v, want 400", m["status"])
		}
		if m["error_class"] != "param" {
			t.Errorf("error_class = %v, want param", m["error_class"])
		}
		if body, _ := m["upstream_body"].(string); !strings.Contains(body, "context_length_exceeded") {
			t.Errorf("upstream_body = %q, want it to contain upstream message", body)
		}
		if strings.Contains(line, "sk-test") {
			t.Fatalf("secret leaked into upstream error log: %s", line)
		}
	}
	if !found {
		t.Fatalf("no \"upstream error\" log line emitted: %q", buf.String())
	}
}

// TestUpstreamErrorLogTruncatesBody：错误体可能回显请求内容，诊断日志只留前
// upstreamErrorLogMaxBytes 字节，且截断点不得切坏多字节字符（JSON 日志要合法）。
func TestUpstreamErrorLogTruncatesBody(t *testing.T) {
	bigMsg := strings.Repeat("上下文超限", 500) // 5 个字符 ×3 字节 ×500 = 7500 字节
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"message":"` + bigMsg + `"}}`))
	}))
	defer up.Close()
	var buf bytes.Buffer
	s := newGW(t, up.URL, func(d *Deps) {
		d.Log = slog.New(slog.NewJSONHandler(&buf, nil))
	})
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line not JSON: %q", line)
		}
		if m["msg"] != "upstream error" {
			continue
		}
		body, _ := m["upstream_body"].(string)
		if len(body) > upstreamErrorLogMaxBytes {
			t.Errorf("upstream_body = %d bytes, want <= %d", len(body), upstreamErrorLogMaxBytes)
		}
		if !utf8.ValidString(body) {
			t.Errorf("upstream_body truncated mid-rune: %q", body)
		}
		return
	}
	t.Fatalf("no \"upstream error\" log line emitted: %q", buf.String())
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
			post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok").Body.Close()
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for inFlight.Load() < maxConcurrent {
		if time.Now().After(deadline) {
			t.Fatalf("inFlight=%d want %d", inFlight.Load(), maxConcurrent)
		}
		time.Sleep(5 * time.Millisecond)
	}
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
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
	req, _ := http.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"pub-1","stream":true,"messages":[{"role":"user","content":"hi"}]}`))
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
	req, _ := http.NewRequest("POST", "/v1/messages", strings.NewReader(`{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`))
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
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
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
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 400 || string(b) != upstreamBody {
		t.Fatalf("envelope must pass through verbatim: %d %s", resp.StatusCode, b)
	}
}

// TestAllCandidatesExhaustedReturns502：唯一候选 429 → 冷却后耗尽 → 502 envelope，
// body 含 all targets failed（与全冷却 503 可区分）。
func TestAllCandidatesExhaustedReturns502(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 502 {
		t.Fatalf("exhausted candidates must 502, got %d: %s", resp.StatusCode, b)
	}
	if !strings.Contains(string(b), "all targets failed") {
		t.Fatalf("502 body must say all targets failed: %s", b)
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
		// 冷却键=ChannelID（单 Key 模型）：死/活密钥必须分属不同渠道。
		dead := router.Candidate{TargetID: "t-dead", LogicalModel: "pub-1",
			ChannelID: "c-dead", ChannelName: "dead", SecretKey: "account:gone",
			Protocol: "anthropic", BaseURL: up.URL,
			AllowPrivate: true, UpstreamModel: "up-1", Priority: 5}
		live := router.Candidate{TargetID: "t-live", LogicalModel: "pub-1",
			ChannelID: "c-live", ChannelName: "live", SecretKey: "account:k1",
			Protocol: "anthropic", BaseURL: up.URL,
			AllowPrivate: true, UpstreamModel: "up-1", Priority: 10}
		d.Candidates = func(publicModel string) ([]router.Candidate, error) {
			if publicModel != "pub-1" {
				return nil, nil
			}
			return []router.Candidate{dead, live}, nil
		}
	})
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 || calls != 1 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("must fail over to live key: code=%d calls=%d body=%s", resp.StatusCode, calls, b)
	}
	snap := cd.Snapshot(time.Now())
	if len(snap) != 1 || snap[0].ChannelID != "c-dead" || snap[0].Reason != "auth" {
		t.Fatalf("dead channel must be cooled as auth: %+v", snap)
	}
	// 第二请求：c-dead 已冷却被过滤，仅 live 命中——死钥不每请求重试。
	resp2 := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
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
		// 协议=渠道属性：坏协议与好协议分属不同渠道。
		badProto := router.Candidate{TargetID: "t-bad", LogicalModel: "pub-1",
			ChannelID: "c-bad", ChannelName: "bad", SecretKey: "account:k1",
			Protocol: "grpc", BaseURL: up.URL,
			AllowPrivate: true, UpstreamModel: "up-1", Priority: 5}
		good := router.Candidate{TargetID: "t-good", LogicalModel: "pub-1",
			ChannelID: "c-good", ChannelName: "good", SecretKey: "account:k1",
			Protocol: "anthropic", BaseURL: up.URL,
			AllowPrivate: true, UpstreamModel: "up-1", Priority: 10}
		d.Candidates = func(publicModel string) ([]router.Candidate, error) {
			if publicModel != "pub-1" {
				return nil, nil
			}
			return []router.Candidate{badProto, good}, nil
		}
	})
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 || calls != 1 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("must fail over past bad protocol: code=%d calls=%d body=%s", resp.StatusCode, calls, b)
	}
	if snap := cd.Snapshot(time.Now()); len(snap) != 0 {
		t.Fatalf("bad protocol must not cool the key: %+v", snap)
	}
}

// --- Task 6：透传拒绝 + 耗尽错误可区分（2026-10-17-logical-models 简报）---

// TestUnknownModelRejected：Candidates 空（未登记模型）→ 404 envelope，且不进
// failover 循环——上游 0 调用。
func TestUnknownModelRejected(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"id":"ok"}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, nil) // Candidates 对非 pub-1 返回空
	resp := post(t, s, `{"model":"nope","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 {
		t.Fatalf("unknown model must 404, got %d: %s", resp.StatusCode, b)
	}
	var env struct {
		Type  string `json:"type"`
		Error struct {
			Type string `json:"type"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Type != "error" ||
		env.Error.Type != "not_found_error" {
		t.Fatalf("404 must be not_found_error envelope: %s", b)
	}
	if calls != 0 {
		t.Fatalf("unknown model must not enter failover, upstream calls=%d", calls)
	}
}

// TestPassthroughReservedStill404：passthrough 开 + candidates 空 → 仍 404。
// 设置项仅预留（后续接单渠道直连），避免半成品语义。
func TestPassthroughReservedStill404(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"id":"ok"}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Passthrough = func() bool { return true }
	})
	resp := post(t, s, `{"model":"nope","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 || calls != 0 {
		t.Fatalf("passthrough reserved: must still 404 with 0 upstream calls, got %d calls=%d: %s",
			resp.StatusCode, calls, b)
	}
}

// TestAllCooling503：候选全 cooling（Resolve 滤光且 Filtered 非空）→ 503，
// body 含 all targets cooling，上游 0 调用。
func TestAllCooling503(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"id":"ok"}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		cool := router.Candidate{TargetID: "t1", LogicalModel: "pub-1",
			ChannelID: "c1", ChannelName: "ch1", SecretKey: "account:k1",
			Protocol: "anthropic", BaseURL: up.URL, AllowPrivate: true,
			UpstreamModel: "u1", Priority: 10,
			CoolingUntil: time.Now().Add(time.Minute), CoolingReason: "rate_limited"}
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{cool}, nil
		}
	})
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 503 {
		t.Fatalf("all-cooling must 503, got %d: %s", resp.StatusCode, b)
	}
	if !strings.Contains(string(b), "all targets cooling") {
		t.Fatalf("503 body must say all targets cooling: %s", b)
	}
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Type != "error" {
		t.Fatalf("503 must be envelope: %s", b)
	}
	if calls != 0 {
		t.Fatalf("cooled targets must not be attempted, calls=%d", calls)
	}
}

// TestSecondCheckAllCooling503：候选未带冷却快照但发送前二次校验全部命中冷却
// （TryAcquire 失败）→ 亦503 all targets cooling（「全部仅因冷却」含此路径）。
func TestSecondCheckAllCooling503(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"id":"ok"}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		cd := router.NewCooldowns()
		d.Cooldowns = cd
		cd.MarkClassified("c1", provider.ClassRateLimit, 30, time.Now())
		cand := router.Candidate{TargetID: "t1", LogicalModel: "pub-1",
			ChannelID: "c1", ChannelName: "ch1", SecretKey: "account:k1",
			Protocol: "anthropic", BaseURL: up.URL, AllowPrivate: true,
			UpstreamModel: "u1", Priority: 10}
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{cand}, nil
		}
	})
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 503 || !strings.Contains(string(b), "all targets cooling") {
		t.Fatalf("second-check all-cooling must 503 all targets cooling, got %d: %s",
			resp.StatusCode, b)
	}
	if calls != 0 {
		t.Fatalf("must not attempt cooled channel, calls=%d", calls)
	}
}

// --- Task 6：docx §13 转移边界落位（简报 Step 1/2 + OpenAI 端到端 + Task 5 pointer pin）---

// twoCands 构造两个候选共享密钥 store：c1/account:k1→upstream1、c2/account:k2→upstream2。
// 返回 (Deps tweak, 共享 Cooldowns)——冷却断言用同一实例。
func twoCands(t *testing.T, up1, up2 string) (func(d *Deps), *router.Cooldowns) {
	t.Helper()
	creds := newMapStore()
	_ = creds.Set("account:k1", []byte("sk-1"))
	_ = creds.Set("account:k2", []byte("sk-2"))
	cds := router.NewCooldowns()
	return func(d *Deps) {
		d.Cred = creds
		c1 := router.Candidate{TargetID: "t1", LogicalModel: "pub-1",
			ChannelID: "c1", ChannelName: "ch1", SecretKey: "account:k1",
			Protocol: "anthropic", BaseURL: up1, AllowPrivate: true,
			UpstreamModel: "u1", Priority: 10}
		c2 := router.Candidate{TargetID: "t2", LogicalModel: "pub-1",
			ChannelID: "c2", ChannelName: "ch2", SecretKey: "account:k2",
			Protocol: "anthropic", BaseURL: up2, AllowPrivate: true,
			UpstreamModel: "u2", Priority: 20}
		d.Candidates = func(string) ([]router.Candidate, error) { return []router.Candidate{c1, c2}, nil }
		d.Cooldowns = cds
	}, cds
}

// Review Focus 1 + docx §13：首候选连接失败 → 冷却 connect → 次候选服务，200。
func TestFailoverSkipsBadChannel(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	dead := "http://127.0.0.1:1" // 必然连接失败
	tw, _ := twoCands(t, dead, good.URL)
	s := newGW(t, dead, tw)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 || calls2 != 1 {
		t.Fatalf("failover: status %d calls2 %d", resp.StatusCode, calls2)
	}
}

// docx §13：429 + 无 Retry-After → 默认 30s 冷却 rate_limited → 次候选服务 200。
func Test429SwitchesAndMarksCooldown(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	busy := fakeUpstream(t, 429, `{"type":"error"}`, nil)
	defer busy.Close()
	tw, cds := twoCands(t, busy.URL, good.URL)
	s := newGW(t, busy.URL, tw)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	// c1 已被冷却（429 无 Retry-After → 默认 30s）；10s 后仍未解冻——
	// TryAcquire 必须失败（ok=false = 冷却中）。
	if _, ok := cds.TryAcquire("c1", time.Now().Add(10*time.Second)); ok {
		t.Fatal("c1 must still be cooling 10s after 429 (default 30s)")
	}
	// 只有失败的那条渠道冷却：接管的 c2 不得被连坐。
	if _, ok := cds.TryAcquire("c2", time.Now()); !ok {
		t.Fatal("c2 served fine and must not be cooled")
	}
}

// docx §13：400 参数错误 → 不转移、不冷却，客户端拿到 400。
func Test400NoSwitchNoCooldown(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	bad := fakeUpstream(t, 400, `{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`, nil)
	defer bad.Close()
	tw, cds := twoCands(t, bad.URL, good.URL)
	s := newGW(t, bad.URL, tw)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if _, ok := cds.TryAcquire("c1", time.Now()); !ok {
		t.Fatal("400 must not cool the channel")
	}
	if resp.StatusCode != 400 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "invalid_request_error") {
		t.Fatalf("body %s", b)
	}
	if calls2 != 0 {
		t.Fatal("400 must not switch")
	}
}

// 404/405 = 端点不存在/方法不对（渠道 base_url 或协议配错），不是客户端参数问题：
// 冷却该渠道并转移到下一目标（2026-10-22 小米 /anthropic/messages openresty 404）。
func Test404EndpointFailureFailsOver(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	notFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(404)
		w.Write([]byte("<html><head><title>404 Not Found</title></head><body><center>openresty</center></body></html>"))
	}))
	defer notFound.Close()
	tw, cds := twoCands(t, notFound.URL, good.URL)
	s := newGW(t, notFound.URL, tw)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 || calls2 != 1 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("404 must fail over: status %d calls2 %d body %s", resp.StatusCode, calls2, b)
	}
	// 端点配错的渠道必须被冷却，避免后续请求继续撞同一个 404。
	if _, ok := cds.TryAcquire("c1", time.Now()); ok {
		t.Fatal("404 endpoint failure must cool c1")
	}
	// 接管的 c2 不被连坐。
	if _, ok := cds.TryAcquire("c2", time.Now()); !ok {
		t.Fatal("c2 served fine and must not be cooled")
	}
}

// docx §13：流式/非流首字节已写（提交点已过）→ 上游中断只断流，绝不换候选重放。
// （计划名 TestFirstByteThenUpstreamErrorNoReplay，2026-10-17-logical-models）
func TestFirstByteThenUpstreamErrorNoReplay(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200) // 响应头已写=提交点已过
		w.(http.Flusher).Flush()
		w.Write([]byte(`{"partial":`))
		panic(http.ErrAbortHandler) // 模拟流中断（静默断连，不打日志）
	}))
	defer flaky.Close()
	tw, _ := twoCands(t, flaky.URL, good.URL)
	s := newGW(t, flaky.URL, tw)
	resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if calls2 != 0 {
		t.Fatalf("after commit must not switch, calls2=%d", calls2)
	}
}

// docx §13：客户端取消 → 直接取消上游、不转移下一候选，且对候选零状态副作用
// （不冷却、switches 不计）。
func TestClientCancelNoSwitch(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// net/http 服务端读完请求体后才 arm 断连检测——先读完 body 再挂住，
		// 否则 r.Context().Done() 永不触发、Close() 永久阻塞（同 TestClientCancelPropagatesUpstream）。
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done() // 挂住直到网关取消
	}))
	defer slow.Close()
	tw, cds := twoCands(t, slow.URL, good.URL)
	var ring *telemetry.Ring
	s := newGW(t, slow.URL, func(d *Deps) {
		tw(d)
		ring = d.Ring
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "POST", "/v1/messages",
		strings.NewReader(`{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer tok")
	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req) // 客户端已断，写响应无害
	if calls2 != 0 {
		t.Fatalf("cancel must not switch, calls2=%d", calls2)
	}
	// 取消零副作用：密钥不得留冷却状态（Do 返回的 context.Canceled 是客户端
	// 断开，不是上游故障）。
	if snap := cds.Snapshot(time.Now()); len(snap) != 0 {
		t.Fatalf("cancel must leave no cooldown state: %+v", snap)
	}
	// switches 不计（Ring 记录 FailoverCount==0）。
	recs := ring.List()
	if len(recs) == 0 {
		t.Fatal("ring must hold the request record")
	}
	if recs[0].FailoverCount != 0 {
		t.Fatalf("cancel must not count switches: FailoverCount=%d", recs[0].FailoverCount)
	}
}

// docx §13 + -race：并发 429 下同一密钥冷却原子（prolong 不回退），次候选接管。
func TestConcurrent429CooldownAtomic(t *testing.T) {
	var mu sync.Mutex
	calls2 := 0
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls2++
		mu.Unlock()
		w.Write([]byte(`{"id":"ok"}`))
	}))
	defer good.Close()
	busy := fakeUpstream(t, 429, `{"type":"error"}`, nil)
	defer busy.Close()
	tw, cds := twoCands(t, busy.URL, good.URL)
	s := newGW(t, busy.URL, tw)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp := post(t, s, `{"model":"pub-1","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
			resp.Body.Close()
		}()
	}
	wg.Wait()
	// 首轮 429 后 k1 冷却：此后请求应全部直达 good（不再打 busy 由
	// Candidates 快照过滤保证——app 层职责，网关层断言冷却标记成立即可）。
	// 断言点必须在冷却窗口内（429 默认冷却 30s——简报的 now+1h 观察点已过期），
	// TryAcquire 必须失败（ok=false = 冷却中）。
	if _, ok := cds.TryAcquire("c1", time.Now()); ok {
		t.Fatal("c1 must be cooling after concurrent 429s")
	}
	if calls2 == 0 {
		t.Fatal("good upstream must have served")
	}
}

// spec §10：OpenAI 协议候选端到端——Anthropic 入站 → 转换 → OpenAI 假上游
// （ChatCompletions）→ 逆转换 → 客户端收到 Anthropic envelope。
func TestOpenAIProtocolCandidateEndToEnd(t *testing.T) {
	var gotAuth string
	var gotBody map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"cmpl-1","model":"u-oai","choices":[{"message":{"content":"hello from openai"},"finish_reason":"stop"}],"usage":{"prompt_tokens":4,"completion_tokens":3}}`))
	}))
	defer up.Close()
	creds := newMapStore()
	_ = creds.Set("account:ko", []byte("sk-oai"))
	cand := router.Candidate{TargetID: "to", LogicalModel: "pub-1",
		ChannelID: "co", ChannelName: "oai", SecretKey: "account:ko",
		Protocol: "openai", BaseURL: up.URL, AllowPrivate: true,
		UpstreamModel: "u-oai", Priority: 10}
	s := newGW(t, up.URL, func(d *Deps) {
		d.Cred = creds
		d.Candidates = func(string) ([]router.Candidate, error) { return []router.Candidate{cand}, nil }
	})
	resp := post(t, s, `{"model":"pub-1","max_tokens":16,"system":"sys","messages":[{"role":"user","content":"q"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	if !strings.Contains(string(b), `"stop_reason":"end_turn"`) || !strings.Contains(string(b), "hello from openai") {
		t.Fatalf("anthropic envelope expected: %s", b)
	}
	if gotAuth != "Bearer sk-oai" {
		t.Fatalf("bearer auth: %q", gotAuth)
	}
	// system 已转换
	if gotBody["messages"] == nil {
		t.Fatalf("converted body: %v", gotBody)
	}
}

// --- Task 5 遗留 pointer 补 pin ---

// Pointer 1：全候选凭据失效 → 耗尽 502 envelope，Ring 记录 error_class="credential"。
func TestAllCredentialsDeadReturns502CredentialClass(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"ok"}`))
	}))
	defer up.Close()
	var ring *telemetry.Ring
	s := newGW(t, up.URL, func(d *Deps) {
		ring = d.Ring
		dead1 := router.Candidate{TargetID: "td1", LogicalModel: "pub-1",
			ChannelID: "c1", ChannelName: "ch1", SecretKey: "account:gone1",
			Protocol: "anthropic", BaseURL: up.URL,
			AllowPrivate: true, UpstreamModel: "u1", Priority: 10}
		dead2 := router.Candidate{TargetID: "td2", LogicalModel: "pub-1",
			ChannelID: "c2", ChannelName: "ch2", SecretKey: "account:gone2",
			Protocol: "anthropic", BaseURL: up.URL,
			AllowPrivate: true, UpstreamModel: "u2", Priority: 20}
		d.Candidates = func(string) ([]router.Candidate, error) { return []router.Candidate{dead1, dead2}, nil }
	})
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 502 {
		t.Fatalf("all-credential-dead must 502, got %d: %s", resp.StatusCode, b)
	}
	var env struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Type != "error" {
		t.Fatalf("502 must be envelope: %s", b)
	}
	recs := ring.List()
	if len(recs) == 0 {
		t.Fatal("ring must hold the request record")
	}
	if recs[0].ErrorClass != "credential" || recs[0].Status != 502 {
		t.Fatalf("ring record must carry error_class=credential status=502: %+v", recs[0])
	}
}

// Pointer 2：anthropic 渠道上游应见 X-Api-Key=凭据本体；本地网关令牌不出站。
func TestUpstreamSeesAPIKeyNotLocalToken(t *testing.T) {
	var gotKey, gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{"id":"ok"}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if gotKey != "sk-test" {
		t.Fatalf("upstream must see X-Api-Key=sk-test, got %q", gotKey)
	}
	if gotAuth != "" {
		t.Fatalf("local gateway token must not leave: Authorization=%q", gotAuth)
	}
}

// Pointer 3a：Retry-After 白名单透传——2xx 成功路径。
func TestRetryAfterPassthroughOnSuccess(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.Write([]byte(`{"id":"ok"}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Retry-After") != "5" {
		t.Fatalf("Retry-After must pass through on 2xx: status=%d ra=%q",
			resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

// Pointer 3b：Retry-After 白名单透传——4xx param 原状态码回传路径。
func TestRetryAfterPassthroughOnParamError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "9")
		w.WriteHeader(400)
		w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad"}}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 400 || resp.Header.Get("Retry-After") != "9" {
		t.Fatalf("Retry-After must pass through on 400: status=%d ra=%q",
			resp.StatusCode, resp.Header.Get("Retry-After"))
	}
}

// --- Task 6B：OpenAI 入站 /v1/chat/completions ---

// postChat 向 OpenAI 入站端点发请求（与 post 同构，路径不同）。
func postChat(t *testing.T, s *Server, body, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w.Result()
}

// TestOpenAIInboundFailoverAcrossChannels：twoCands——第一目标 429，第二目标 200；
// 客户端收到 OpenAI 形状 200（chat.completion + 逻辑模型名），第一目标只调一次。
func TestOpenAIInboundFailoverAcrossChannels(t *testing.T) {
	var calls1, calls2 int
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls1++
		w.WriteHeader(429)
		w.Write([]byte(`{"type":"error"}`))
	}))
	defer busy.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls2++
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"u2","content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn","usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	defer good.Close()
	tw, _ := twoCands(t, busy.URL, good.URL)
	s := newGW(t, busy.URL, tw)
	resp := postChat(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	if calls1 != 1 || calls2 != 1 {
		t.Fatalf("first target once then second target: calls1=%d calls2=%d", calls1, calls2)
	}
	var out struct {
		Object  string `json:"object"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("client must get OpenAI shape: %s", b)
	}
	if out.Object != "chat.completion" || out.Model != "pub-1" {
		t.Fatalf("envelope must echo logical model: %s", b)
	}
	if len(out.Choices) != 1 || out.Choices[0].Message.Content != "pong" || out.Choices[0].FinishReason != "stop" {
		t.Fatalf("choices: %s", b)
	}
}

// TestOpenAIInboundUnknownModel404：未登记模型 → 404（列出公开模型），上游 0 调用。
func TestOpenAIInboundUnknownModel404(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"id":"ok"}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := postChat(t, s, `{"model":"nope","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 || calls != 0 {
		t.Fatalf("unknown model must 404 with 0 upstream calls: %d calls=%d %s", resp.StatusCode, calls, b)
	}
	if !strings.Contains(string(b), "pub-1") {
		t.Fatalf("404 must list models: %s", b)
	}
}

// TestOpenAIInboundCommitNoReplay：首 chunk 写出后上游再错——提交点已过，绝不
// 调第二目标（docx §6.3 首字节后不重放）。
func TestOpenAIInboundCommitNoReplay(t *testing.T) {
	var calls2 int
	good := fakeUpstream(t, 200, `{"id":"ok"}`, &calls2)
	defer good.Close()
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"role\":\"assistant\",\"model\":\"u1\",\"content\":[]}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // 首 chunk 后断流
	}))
	defer flaky.Close()
	tw, _ := twoCands(t, flaky.URL, good.URL)
	s := newGW(t, flaky.URL, tw)
	resp := postChat(t, s, `{"model":"pub-1","stream":true,"messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	if !strings.Contains(string(b), `"content":"hi"`) {
		t.Fatalf("first chunk must reach client before abort: %s", b)
	}
	if calls2 != 0 {
		t.Fatalf("after first byte must not switch, calls2=%d", calls2)
	}
}

// TestOpenAIInboundOpenAIProtocolPassthrough：上游 openai 协议——RewriteModel 后
// 直通（无 Anthropic 互转），响应原样 OpenAI 形状回传。
func TestOpenAIInboundOpenAIProtocolPassthrough(t *testing.T) {
	var gotModel, gotAuth string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		m := map[string]any{}
		json.Unmarshal(b, &m)
		gotModel, _ = m["model"].(string)
		w.Write([]byte(`{"id":"cmpl-1","object":"chat.completion","model":"u-oai","choices":[{"message":{"role":"assistant","content":"raw"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer up.Close()
	creds := newMapStore()
	_ = creds.Set("account:ko", []byte("sk-oai"))
	cand := router.Candidate{TargetID: "to", LogicalModel: "pub-1",
		ChannelID: "co", ChannelName: "oai", SecretKey: "account:ko",
		Protocol: "openai", BaseURL: up.URL, AllowPrivate: true,
		UpstreamModel: "u-oai", Priority: 10}
	s := newGW(t, up.URL, func(d *Deps) {
		d.Cred = creds
		d.Candidates = func(string) ([]router.Candidate, error) { return []router.Candidate{cand}, nil }
	})
	resp := postChat(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	if gotModel != "u-oai" || gotAuth != "Bearer sk-oai" {
		t.Fatalf("passthrough must rewrite model and use channel key: model=%q auth=%q", gotModel, gotAuth)
	}
	if !strings.Contains(string(b), `"raw"`) {
		t.Fatalf("response must pass through unconverted: %s", b)
	}
}

// 回归 pin：OpenAI 入站 → openai 渠道是纯透传，工具调用体原样到达上游——
// 既不经过 Anthropic↔OpenAI 转换器，也不做空名清洗（2026-10-22 协议修复
// 排查后确认「OpenAI 入站不受影响」）。
func TestOpenAIInboundOpenAIProtocolPassthroughPreservesToolCalls(t *testing.T) {
	var gotBody map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		w.Write([]byte(`{"id":"cmpl-1","object":"chat.completion","choices":[],"usage":{}}`))
	}))
	defer up.Close()
	creds := newMapStore()
	_ = creds.Set("account:ko", []byte("sk-oai"))
	cand := router.Candidate{TargetID: "to", LogicalModel: "pub-1",
		ChannelID: "co", ChannelName: "oai", SecretKey: "account:ko",
		Protocol: "openai", BaseURL: up.URL, AllowPrivate: true,
		UpstreamModel: "u-oai", Priority: 10}
	s := newGW(t, up.URL, func(d *Deps) {
		d.Cred = creds
		d.Candidates = func(string) ([]router.Candidate, error) { return []router.Candidate{cand}, nil }
	})
	body := `{"model":"pub-1","messages":[{"role":"assistant","content":null,
	  "tool_calls":[{"id":"call_1","type":"function","function":{"name":"Bash","arguments":"{\"command\":\"ls\"}"}}]}]}`
	resp := postChat(t, s, body, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	if gotBody["model"] != "u-oai" {
		t.Fatalf("model not rewritten: %v", gotBody["model"])
	}
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages must pass through untouched: %v", gotBody["messages"])
	}
	m0, _ := msgs[0].(map[string]any)
	calls, _ := m0["tool_calls"].([]any)
	if len(calls) != 1 {
		t.Fatalf("tool_calls must pass through untouched: %v", m0["tool_calls"])
	}
	fn, _ := calls[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "Bash" || fn["arguments"] != `{"command":"ls"}` {
		t.Fatalf("tool call mangled: %v", fn)
	}
}

// TestOpenAIInboundUpstreamErrorOpenAIShape：anthropic 上游 400 的 Anthropic
// envelope 须转成 OpenAI error 形状回给 chat.completions 客户端（不原样透传）。
func TestOpenAIInboundUpstreamErrorOpenAIShape(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		w.Write([]byte(`{"type":"error","error":{"type":"invalid_request_error","message":"bad param"}}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := postChat(t, s, `{"model":"pub-1","messages":[{"role":"user","content":"hi"}]}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 400 {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	var env struct {
		Type  string `json:"type"` // Anthropic envelope 的顶层 type 不得出现在 OpenAI 形状
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(b, &env); err != nil || env.Type != "" ||
		env.Error.Message != "bad param" || env.Error.Type != "invalid_request_error" {
		t.Fatalf("must be OpenAI error shape: %s", b)
	}
}

// TestOpenAIInboundPassthroughStreamInterrupted：OpenAI 直通流在上游未发 [DONE]
// 即断流时，须向客户端补一条 error chunk，且不得伪造 [DONE]。
func TestOpenAIInboundPassthroughStreamInterrupted(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte(`data: {"id":"c","choices":[{"delta":{"content":"par"}}]}` + "\n\n"))
		f.Flush()
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1",
				ChannelName: "main", SecretKey: "account:k1", Protocol: "openai",
				BaseURL: up.URL, AllowPrivate: true, UpstreamModel: "up-1", Priority: 10}}, nil
		}
	})
	body := `{"model":"pub-1","stream":true,"messages":[{"role":"user","content":"q"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	out := w.Body.String()
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, "upstream stream interrupted") {
		t.Fatalf("missing error chunk:\n%s", out)
	}
	if strings.Contains(out, "[DONE]") {
		t.Fatalf("truncated stream must not emit [DONE]:\n%s", out)
	}
}

// —— 破坏性测试回归（2026-10-18）：畸形 messages 不得出网关 ——

func TestMessagesMissingRejectedBeforeUpstream(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"content":[{"type":"text","text":"x"}]}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","max_tokens":16}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("missing messages must 400 at gateway, got %d: %s", resp.StatusCode, b)
	}
	if calls != 0 {
		t.Fatalf("gateway must not forward missing-messages body to upstream, calls=%d", calls)
	}
}

func TestMessagesEmptyArrayRejectedBeforeUpstream(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"content":[{"type":"text","text":"x"}]}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := post(t, s, `{"model":"pub-1","max_tokens":16,"messages":[]}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("empty messages must 400 at gateway, got %d: %s", resp.StatusCode, b)
	}
	if calls != 0 {
		t.Fatalf("gateway must not forward empty-messages body to upstream, calls=%d", calls)
	}
}

func TestSlowBodyUploadTimesOut(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"content":[]}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		orig := d.Limits
		d.Limits = func() Limits {
			lim := orig()
			lim.ReadTimeout = 300 * time.Millisecond
			return lim
		}
	})
	addr, err := s.Start("127.0.0.1", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// 声明 1MB body 但一个字节不发：读体阶段无 deadline 时会无限挂起。
	fmt.Fprintf(conn, "POST /v1/messages HTTP/1.1\r\nHost: %s\r\nAuthorization: Bearer tok\r\nContent-Type: application/json\r\nContent-Length: 1048576\r\n\r\n", addr)
	// 客户端 deadline 远大于服务端 ReadTimeout(300ms)：服务端必须先主动结束
	// （408 后关闭 / 直接 EOF），而不是客户端等到自己的 deadline 才放弃。
	start := time.Now()
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var got strings.Builder
	buf := make([]byte, 256)
	for {
		n, rerr := conn.Read(buf)
		got.Write(buf[:n])
		if rerr != nil {
			if nerr, ok := rerr.(net.Error); ok && nerr.Timeout() {
				t.Fatalf("server must close slow-body connection at ReadTimeout, client waited full deadline (%v), got: %q", time.Since(start), got.String())
			}
			break // EOF / connection reset：服务端已关闭
		}
	}
	if calls != 0 {
		t.Fatalf("slow body must never reach upstream, calls=%d", calls)
	}
	// 若服务端先回了响应，只允许 4xx（如 408），绝不能是成功状态。
	if s := got.String(); s != "" && !strings.HasPrefix(s, "HTTP/1.1 4") {
		t.Fatalf("only 4xx allowed for slow body, got: %q", s)
	}
}

func TestMessagesStripsOneMSuffix(t *testing.T) {
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
	// pub-1[1M] 应命中公开模型 pub-1（Candidates 只对 pub-1 返回候选）
	for _, model := range []string{"pub-1[1M]", "pub-1[1m]"} {
		calls = 0
		resp := post(t, s, `{"model":"`+model+`","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("model=%s status %d body %s", model, resp.StatusCode, b)
		}
		if calls != 1 || lastModel != "up-1" {
			t.Fatalf("model=%s calls=%d lastModel=%s", model, calls, lastModel)
		}
	}
}

func TestOpenAIChatStripsOneMSuffix(t *testing.T) {
	var calls int
	var lastModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		m := map[string]any{}
		json.Unmarshal(b, &m)
		lastModel, _ = m["model"].(string)
		w.Write([]byte(`{"id":"chatcmpl-ok","object":"chat.completion","choices":[]}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"pub-1[1M]","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	resp := w.Result()
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	if calls != 1 || lastModel != "up-1" {
		t.Fatalf("calls=%d lastModel=%s", calls, lastModel)
	}
}

func TestStripOneM(t *testing.T) {
	cases := map[string]string{
		"pub-1[1M]": "pub-1",
		"pub-1[1m]": "pub-1",
		"pub-1":     "pub-1",
		"[]":        "[]",
		"[1M]":      "",       // 纯后缀也剥（随后未登记模型 404，行为明确）
		"a[1M]x":    "a[1M]x", // 非尾部不动
	}
	for in, want := range cases {
		if got := stripOneM(in); got != want {
			t.Fatalf("stripOneM(%q)=%q want %q", in, got, want)
		}
	}
}

func TestCopyStreamIdleTimeout(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	w := httptest.NewRecorder()
	start := time.Now()
	timedOut := copyStream(w, pr, 50*time.Millisecond, []byte("EVENT\n\n"))
	if !timedOut {
		t.Fatal("expected idle timeout")
	}
	if time.Since(start) > time.Second {
		t.Fatal("watchdog took too long")
	}
	if !strings.Contains(w.Body.String(), "EVENT") {
		t.Fatalf("idle payload missing: %q", w.Body.String())
	}
}

func TestCopyStreamPassesData(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("hello"))
		pw.Close()
	}()
	w := httptest.NewRecorder()
	if copyStream(w, pr, time.Second, []byte("EVENT")) {
		t.Fatal("must not report timeout")
	}
	if w.Body.String() != "hello" {
		t.Fatalf("body = %q", w.Body.String())
	}
}

func TestMessagesStreamLongerThanTimeoutNotCut(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.WriteHeader(200)
		for i := 0; i < 6; i++ {
			w.Write([]byte("event: message_start\ndata: {}\n\n"))
			f.Flush()
			time.Sleep(40 * time.Millisecond)
		}
		w.Write([]byte("event: message_stop\ndata: {}\n\n"))
		f.Flush()
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Limits = func() Limits { return Limits{MaxBodyBytes: 1 << 20, RequestTimeout: 100 * time.Millisecond} }
	})
	body := `{"model":"pub-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`
	resp := post(t, s, body, "tok")
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "message_stop") {
		t.Fatalf("long active stream was cut at total timeout:\n%s", b)
	}
}

func TestOpenAIInboundStreamIdleTimeoutEmitsError(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.WriteHeader(200)
		w.Write([]byte(`data: {"id":"c","choices":[{"delta":{"content":"x"}}]}` + "\n\n"))
		f.Flush()
		<-release
	}))
	defer up.Close()
	defer close(release)
	var logs bytes.Buffer
	s := newGW(t, up.URL, func(d *Deps) {
		d.Limits = func() Limits { return Limits{MaxBodyBytes: 1 << 20, RequestTimeout: 50 * time.Millisecond} }
		d.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1",
				ChannelName: "main", SecretKey: "account:k1", Protocol: "openai",
				BaseURL: up.URL, AllowPrivate: true, UpstreamModel: "up-1", Priority: 10}}, nil
		}
	})
	body := `{"model":"pub-1","stream":true,"messages":[{"role":"user","content":"q"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	out := w.Body.String()
	if !strings.Contains(out, "upstream stream idle timeout") {
		t.Fatalf("missing idle error chunk:\n%s", out)
	}
	recs := s.deps.Ring.List()
	if len(recs) == 0 || recs[len(recs)-1].ErrorClass != "stream" {
		t.Fatalf("request record must carry error_class=stream: %+v", recs)
	}
	assertIdleTimeoutWarn(t, logs.String())
}

func TestMessagesStreamIdleTimeoutEmitsError(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.WriteHeader(200)
		w.Write([]byte("event: message_start\ndata: {}\n\n"))
		f.Flush()
		<-release // 此后不再输出
	}))
	defer up.Close()
	defer close(release)
	var logs bytes.Buffer
	s := newGW(t, up.URL, func(d *Deps) {
		d.Limits = func() Limits { return Limits{MaxBodyBytes: 1 << 20, RequestTimeout: 50 * time.Millisecond} }
		d.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))
	})
	body := `{"model":"pub-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`
	resp := post(t, s, body, "tok")
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "upstream stream idle timeout") {
		t.Fatalf("missing idle error event:\n%s", b)
	}
	recs := s.deps.Ring.List()
	if len(recs) == 0 || recs[len(recs)-1].ErrorClass != "stream" {
		t.Fatalf("request record must carry error_class=stream: %+v", recs)
	}
	assertIdleTimeoutWarn(t, logs.String())
}

// assertIdleTimeoutWarn 断言 spec §3.7 要求的 WARN 日志（消息 + request_id + idle_ms）。
func assertIdleTimeoutWarn(t *testing.T, logs string) {
	t.Helper()
	if !strings.Contains(logs, "stream idle timeout") {
		t.Fatalf("missing idle timeout warning:\n%s", logs)
	}
	if !strings.Contains(logs, "request_id=") || !strings.Contains(logs, "idle_ms=50") {
		t.Fatalf("idle timeout warning must carry request_id and idle_ms:\n%s", logs)
	}
}

// failingWriter 模拟客户端断开：Write 永久失败（httptest.ResponseRecorder 不会）。
type failingWriter struct{ h http.Header }

func (f *failingWriter) Header() http.Header {
	if f.h == nil {
		f.h = http.Header{}
	}
	return f.h
}
func (f *failingWriter) Write([]byte) (int, error) { return 0, errors.New("client gone") }
func (f *failingWriter) WriteHeader(int)           {}

// TestCopyStreamClosesSrcOnClientWriteFailure：#1 回归——copyStream 拥有 src，
// 客户端写失败时必须关闭它，解除转换器泵在 pipe 写端的永久阻塞。
func TestCopyStreamClosesSrcOnClientWriteFailure(t *testing.T) {
	pr, pw := io.Pipe()
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		for {
			// 模拟 AnthropicStreamToOpenAIStream 的泵：持续向 pipe 写转换结果；
			// 读端被关闭后 Write 返回错误，泵退出。
			if _, err := pw.Write([]byte("data: x\n\n")); err != nil {
				return
			}
		}
	}()
	w := &failingWriter{}
	if copyStream(w, pr, 0, nil) {
		t.Fatal("client write failure must not report idle timeout")
	}
	select {
	case <-pumpDone:
	case <-time.After(2 * time.Second):
		t.Fatal("pump goroutine still blocked after client write failure (src not closed)")
	}
}

// TestCopyStreamIdleAddsEventBoundary：空闲超时恰好发生在事件中途时，onIdle
// 必须另起一条 SSE 事件，不能与残缺事件粘连。
func TestCopyStreamIdleAddsEventBoundary(t *testing.T) {
	const prefix = `data: {"choices":[{"delta":{"content":"par"}`
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte(prefix)) // 不写事件分隔符、不关闭：驱动空闲 watchdog
	}()
	w := httptest.NewRecorder()
	onIdle := protocol.OpenAIStreamErrorChunk("upstream stream idle timeout")
	if !copyStream(w, pr, 50*time.Millisecond, onIdle) {
		t.Fatal("expected idle timeout")
	}
	out := w.Body.String()
	if !strings.HasPrefix(out, prefix) {
		t.Fatalf("prefix must be unchanged: %q", out)
	}
	if !strings.Contains(out, "\n\ndata: {\"error\"") {
		t.Fatalf("idle error must start on a fresh SSE boundary: %q", out)
	}
}

// TestMessagesHeaderTimeoutReturns504：#3——头部等待超时必须显式 504，
// 而不是 200 + 空 body；遥测记录 status=504、error_class=timeout。
func TestMessagesHeaderTimeoutReturns504(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // 永不写响应头
	}))
	defer up.Close()
	defer close(release)
	s := newGW(t, up.URL, func(d *Deps) {
		d.Limits = func() Limits { return Limits{MaxBodyBytes: 1 << 20, RequestTimeout: 50 * time.Millisecond} }
	})
	body := `{"model":"pub-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`
	resp := post(t, s, body, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504: %s", resp.StatusCode, b)
	}
	if !strings.Contains(string(b), "upstream response header timeout") {
		t.Fatalf("missing timeout message: %s", b)
	}
	recs := s.deps.Ring.List()
	if len(recs) == 0 || recs[len(recs)-1].Status != 504 || recs[len(recs)-1].ErrorClass != "timeout" {
		t.Fatalf("request record must carry status=504/error_class=timeout: %+v", recs)
	}
}

// TestOpenAIInboundHeaderTimeoutReturns504：OpenAI 入站同 #3，错误体为 OpenAI 形状。
func TestOpenAIInboundHeaderTimeoutReturns504(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer up.Close()
	defer close(release)
	s := newGW(t, up.URL, func(d *Deps) {
		d.Limits = func() Limits { return Limits{MaxBodyBytes: 1 << 20, RequestTimeout: 50 * time.Millisecond} }
	})
	body := `{"model":"pub-1","stream":true,"messages":[{"role":"user","content":"q"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	out := w.Body.String()
	if w.Code != http.StatusGatewayTimeout {
		t.Fatalf("status = %d, want 504: %s", w.Code, out)
	}
	if !strings.Contains(out, "upstream response header timeout") {
		t.Fatalf("missing timeout message: %s", out)
	}
	var env struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.Error.Message == "" {
		t.Fatalf("must be OpenAI error shape: %s", out)
	}
	recs := s.deps.Ring.List()
	if len(recs) == 0 || recs[len(recs)-1].Status != 504 || recs[len(recs)-1].ErrorClass != "timeout" {
		t.Fatalf("request record must carry status=504/error_class=timeout: %+v", recs)
	}
}

// TestOpenAIInboundPassthroughFinishReasonOnlyNormal：#4——有 finish_reason、
// 无 [DONE] 的正常 OpenAI 兼容流必须原样透传，不得追加 error chunk。
func TestOpenAIInboundPassthroughFinishReasonOnlyNormal(t *testing.T) {
	const stream = `data: {"id":"c","choices":[{"delta":{"content":"hi"},"finish_reason":null}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(stream))
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1",
				ChannelName: "main", SecretKey: "account:k1", Protocol: "openai",
				BaseURL: up.URL, AllowPrivate: true, UpstreamModel: "up-1", Priority: 10}}, nil
		}
	})
	body := `{"model":"pub-1","stream":true,"messages":[{"role":"user","content":"q"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	if got := w.Body.String(); got != stream {
		t.Fatalf("finish_reason-only stream must pass through unchanged:\n got %q\nwant %q", got, stream)
	}
}

// TestOpenAIInboundPassthroughTruncationMidEvent：#2 端到端——上游在事件中途
// 截断时，补的 error chunk 必须另起事件，前缀字节保持不变。
func TestOpenAIInboundPassthroughTruncationMidEvent(t *testing.T) {
	const prefix = `data: {"choices":[{"delta":{"content":"par"}`
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte(prefix))
		f.Flush()
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1",
				ChannelName: "main", SecretKey: "account:k1", Protocol: "openai",
				BaseURL: up.URL, AllowPrivate: true, UpstreamModel: "up-1", Priority: 10}}, nil
		}
	})
	body := `{"model":"pub-1","stream":true,"messages":[{"role":"user","content":"q"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	out := w.Body.String()
	if !strings.HasPrefix(out, prefix) {
		t.Fatalf("prefix must be unchanged: %q", out)
	}
	if !strings.Contains(out, "\n\ndata: {\"error\"") || !strings.Contains(out, "upstream stream interrupted") {
		t.Fatalf("error chunk must start on a fresh SSE boundary: %q", out)
	}
}

// postResponses 以指定令牌向 /v1/responses 发请求（Responses 入站骨架测试）。
func postResponses(t *testing.T, s *Server, body, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w.Result()
}

func TestResponsesAuthRequired(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"id":"ok"}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi"}`, "wrong")
	defer resp.Body.Close()
	if resp.StatusCode != 401 || calls != 0 {
		t.Fatalf("status=%d calls=%d", resp.StatusCode, calls)
	}
}

func TestResponsesUnknownModel404(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"id":"ok"}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := postResponses(t, s, `{"model":"nope","input":"hi"}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 || calls != 0 || !strings.Contains(string(b), "pub-1") {
		t.Fatalf("status=%d calls=%d body=%s", resp.StatusCode, calls, b)
	}
}

func TestResponsesPreviousResponseID400(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", nil)
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi","previous_response_id":"r"}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestResponsesOpenAIChannelNonStream(t *testing.T) {
	var gotModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotModel, _ = body["model"].(string)
		if _, ok := body["messages"]; !ok {
			t.Errorf("upstream must receive chat messages: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":1,
			"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{
				TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1", ChannelName: "oa",
				SecretKey: "account:k1", Protocol: "openai", BaseURL: up.URL,
				AllowPrivate: true, UpstreamModel: "up-1", Priority: 10,
			}}, nil
		}
	})
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi"}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || gotModel != "up-1" {
		t.Fatalf("status=%d model=%q body=%s", resp.StatusCode, gotModel, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil || out["object"] != "response" {
		t.Fatalf("must be Responses shape: %s", b)
	}
	if out["model"] != "pub-1" {
		t.Fatalf("logical model echoed: %s", b)
	}
}

func TestResponsesOpenAIChannelStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"id":"c1","choices":[{"delta":{"content":"hi"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"c1","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{
				TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1", ChannelName: "oa",
				SecretKey: "account:k1", Protocol: "openai", BaseURL: up.URL,
				AllowPrivate: true, UpstreamModel: "up-1", Priority: 10,
			}}, nil
		}
	})
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi","stream":true}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	out := string(b)
	for _, want := range []string{"event: response.created", `"delta":"hi"`, "event: response.completed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
