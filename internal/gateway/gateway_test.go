package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"AgentSwitch/internal/account"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

type memCred map[string][]byte

func (m memCred) Set(k string, v []byte) error { m[k] = v; return nil }
func (m memCred) Get(k string) ([]byte, error) {
	v, ok := m[k]
	if !ok {
		return nil, credential.ErrNotFound
	}
	return v, nil
}
func (m memCred) Delete(k string) error { delete(m, k); return nil }

func testServer(t *testing.T, upstream http.HandlerFunc, tok string, accounts []account.Account, alias map[string]string) *Server {
	t.Helper()
	return testServerLog(t, upstream, tok, accounts, alias, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

// testServerLog 同 testServer 但注入给定 Logger（供日志断言测试用）。
func testServerLog(t *testing.T, upstream http.HandlerFunc, tok string, accounts []account.Account, alias map[string]string, log *slog.Logger) *Server {
	t.Helper()
	var upBase string
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	upBase = srv.URL
	ring := telemetry.NewRing(10)
	d := Deps{
		Token:    func() string { return tok },
		Limits:   func() Limits { return Limits{MaxBodyBytes: 1 << 20, MaxConcurrent: 8, RequestTimeout: 5 * time.Second} },
		Accounts: func() ([]account.Account, error) { return accounts, nil },
		Alias: func(public string) (string, bool, error) {
			u, ok := alias[public]
			return u, ok, nil
		},
		ListAliases: func() []string {
			out := make([]string, 0, len(alias))
			for k := range alias {
				out = append(out, k)
			}
			sort.Strings(out)
			return out
		},
		Cred:           memCred{"account:a1": []byte("sk-upstream")},
		Router:         router.Single{},
		Adapter:        provider.NewAnthropicAdapter(),
		ProviderConfig: func() (string, bool, error) { return upBase, true, nil },
		Log:            log,
		Ring:           ring,
	}
	return New(d)
}

// TestRequestLogLineCarriesMetadata 覆盖 I5：handleMessages 的 defer 必须落一行
// slog "request"（request_id/model/upstream_model/account/status/duration_ms/
// error_class/streaming——零密钥、零请求体、零 Authorization）。JSON buffer 断言。
func TestRequestLogLineCarriesMetadata(t *testing.T) {
	up := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","content":[]}`))
	}
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	var buf bytes.Buffer // handler 同步跑在测试 goroutine，无需加锁
	ts := testServerLog(t, up, "local-tok", accts, map[string]string{"claude-opus": "up-opus"},
		slog.New(slog.NewJSONHandler(&buf, nil)))
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok",
		`{"model":"claude-opus","messages":[{"role":"user","content":"hi"}]}`))
	if rec.Code != 200 {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
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
		for _, k := range []string{"request_id", "model", "upstream_model", "account",
			"status", "duration_ms", "error_class", "streaming"} {
			if _, ok := m[k]; !ok {
				t.Errorf("request line missing %q: %v", k, m)
			}
		}
		if m["request_id"] == "" || m["model"] != "claude-opus" ||
			m["upstream_model"] != "up-opus" || m["account"] != "a1" {
			t.Errorf("request line values: %v", m)
		}
		if st, ok := m["status"].(float64); !ok || int(st) != 200 {
			t.Errorf("status = %v, want 200", m["status"])
		}
		// 零密钥不变量：上游密钥与网关令牌绝不进日志行。
		if strings.Contains(line, "sk-upstream") || strings.Contains(line, "local-tok") {
			t.Fatalf("secret leaked into request log: %s", line)
		}
	}
	if !found {
		t.Fatalf("no \"request\" log line emitted: %q", buf.String())
	}
}

func authReq(ts *Server, method, path, tok, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	return r
}

func TestNonStreamPassThroughAndModelRewrite(t *testing.T) {
	var sawModel, sawKey, sawAuth string
	up := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		sawModel, _ = m["model"].(string)
		sawKey = r.Header.Get("X-Api-Key")
		sawAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","content":[]}`))
	}
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"claude-opus": "up-opus"})
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok",
		`{"model":"claude-opus","messages":[{"role":"user","content":"hi"}]}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "msg_1") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if sawModel != "up-opus" {
		t.Fatalf("upstream model %q", sawModel)
	}
	if sawKey != "sk-upstream" {
		t.Fatalf("upstream key %q", sawKey)
	}
	// 本地网关令牌绝不发给上游：上游只见 X-Api-Key（由 Adapter 注入）。
	if sawAuth != "" {
		t.Fatalf("local gateway token leaked upstream: Authorization=%q", sawAuth)
	}
}

func TestStreamPassThrough(t *testing.T) {
	up := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message_start\ndata: {}\n\n"))
		w.Write([]byte("event: message_stop\ndata: {}\n\n"))
	}
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"m": "u"})
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok",
		`{"model":"m","stream":true,"messages":[]}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "message_stop") {
		t.Fatalf("stream code=%d body=%s", rec.Code, rec.Body.String())
	}
	if !rec.Flushed {
		t.Fatal("stream chunks were not flushed per-read")
	}
}

func TestRejectsBadTokenWithoutUpstreamCall(t *testing.T) {
	var calls atomic.Int32
	up := func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"m": "u"})
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "wrong", `{"model":"m","messages":[]}`))
	if rec.Code != 401 || calls.Load() != 0 {
		t.Fatalf("code=%d calls=%d", rec.Code, calls.Load())
	}
}

func TestErrorClassesPropagateAsEnvelope(t *testing.T) {
	for _, c := range []struct {
		status int
		want   string
	}{{401, "auth"}, {429, "rate_limited"}, {500, "server"}} {
		up := func(w http.ResponseWriter, r *http.Request) {
			if c.status == 429 {
				w.Header().Set("Retry-After", "7")
			}
			w.WriteHeader(c.status)
			w.Write([]byte(`{"error":{"message":"boom"}}`))
		}
		accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
		ts := testServer(t, up, "local-tok", accts, map[string]string{"m": "u"})
		rec := httptest.NewRecorder()
		ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok", `{"model":"m","messages":[]}`))
		if rec.Code != c.status {
			t.Fatalf("%d: code=%d", c.status, rec.Code)
		}
		var env map[string]any
		json.Unmarshal(rec.Body.Bytes(), &env)
		if env["type"] != "error" {
			t.Fatalf("%d: not envelope: %s", c.status, rec.Body.String())
		}
		// Retry-After 透传
		if c.status == 429 && rec.Header().Get("Retry-After") != "7" {
			t.Fatal("Retry-After not forwarded")
		}
	}
}

func TestUnknownModelListsAliases(t *testing.T) {
	up := func(w http.ResponseWriter, r *http.Request) { t.Fatal("must not reach upstream") }
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"known": "u"})
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok", `{"model":"nope","messages":[]}`))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "known") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHealthAndReadyz(t *testing.T) {
	up := func(w http.ResponseWriter, r *http.Request) {}
	ts := testServer(t, up, "local-tok", nil, nil)
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("healthz %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 {
		t.Fatalf("readyz without accounts=%d want 503", rec.Code)
	}
	ts2 := testServer(t, up, "local-tok",
		[]account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}, nil)
	rec = httptest.NewRecorder()
	ts2.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 200 {
		t.Fatalf("readyz with accounts=%d", rec.Code)
	}
}

func TestModelsEndpointRequiresToken(t *testing.T) {
	up := func(w http.ResponseWriter, r *http.Request) {}
	ts := testServer(t, up, "local-tok", nil, map[string]string{"alias-a": "u"})
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "GET", "/models", "local-tok", ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "alias-a") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "GET", "/models", "", ""))
	if rec.Code != 401 {
		t.Fatalf("unauth models=%d", rec.Code)
	}
}

func TestClientCancelPropagatesUpstream(t *testing.T) {
	started := make(chan struct{}, 1)
	upstreamCtxDone := make(chan struct{}, 1)
	up := func(w http.ResponseWriter, r *http.Request) {
		// net/http 服务端只在请求体读完后才 arm 断连检测（server.go:
		// "When the request has a body, we start the background read only
		// after the entire body has been consumed"）——不读 body 则 r.Context()
		// 永不因客户端断开而取消。真实上游都会读 body，这里同样先读完。
		io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
			upstreamCtxDone <- struct{}{}
		case <-time.After(3 * time.Second):
		}
	}
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"m": "u"})
	ctx, cancel := context.WithCancel(context.Background())
	req := authReq(ts, "POST", "/v1/messages", "local-tok", `{"model":"m","messages":[]}`).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		ts.Handler().ServeHTTP(httptest.NewRecorder(), req)
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

// TestBodyTooLargeRejected：超 MaxBodyBytes（helper 固定 1<<20）→ 413，且不触达上游。
func TestBodyTooLargeRejected(t *testing.T) {
	var calls atomic.Int32
	up := func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"m": "u"})
	rec := httptest.NewRecorder()
	big := strings.Repeat("a", (1<<20)+1)
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok", big))
	if rec.Code != 413 || calls.Load() != 0 {
		t.Fatalf("code=%d calls=%d", rec.Code, calls.Load())
	}
}

// TestConcurrencyLimitRejectsOverflow：占满 MaxConcurrent（helper 固定 8）后第 9 个请求 503。
func TestConcurrencyLimitRejectsOverflow(t *testing.T) {
	const maxConcurrent = 8
	release := make(chan struct{})
	var once sync.Once
	rel := func() { once.Do(func() { close(release) }) }
	t.Cleanup(rel)
	var inFlight atomic.Int32
	up := func(w http.ResponseWriter, r *http.Request) {
		inFlight.Add(1)
		<-release
	}
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"m": "u"})
	var wg sync.WaitGroup
	for i := 0; i < maxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok",
				`{"model":"m","messages":[]}`))
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for inFlight.Load() < maxConcurrent {
		if time.Now().After(deadline) {
			t.Fatalf("inFlight=%d want %d", inFlight.Load(), maxConcurrent)
		}
		time.Sleep(5 * time.Millisecond)
	}
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok",
		`{"model":"m","messages":[]}`))
	if rec.Code != 503 {
		t.Fatalf("overflow code=%d want 503", rec.Code)
	}
	rel()
	wg.Wait()
}
