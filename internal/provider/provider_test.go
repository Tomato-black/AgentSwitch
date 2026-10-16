package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateBaseURL(t *testing.T) {
	cases := []struct {
		url     string
		private bool
		wantErr bool
	}{
		{"https://api.anthropic.com", false, false},
		{"http://api.anthropic.com", false, true},
		{"http://127.0.0.1:9999", false, false}, // 回环显式放行（测试 Fake Provider 需要）
		{"http://192.168.1.5:8080", false, true},
		{"http://192.168.1.5:8080", true, false},
		{"ftp://x.example.com", false, true},
		{"https://user:pass@x.example.com", false, true},
	}
	for _, c := range cases {
		err := ValidateBaseURL(c.url, c.private)
		if (err != nil) != c.wantErr {
			t.Errorf("%s private=%v err=%v wantErr=%v", c.url, c.private, err, c.wantErr)
		}
	}
}

func TestClassifyConservative(t *testing.T) {
	if Classify(429, 7, nil) != ClassRateLimit {
		t.Fatal("429")
	}
	if Classify(429, 0, []byte(`{"error":{"type":"quota_exceeded"}}`)) != ClassQuota {
		t.Fatal("429+quota marker")
	}
	if Classify(403, 0, []byte(`insufficient credit`)) != ClassQuota {
		t.Fatal("403+quota marker")
	}
	if Classify(401, 0, nil) != ClassAuth {
		t.Fatal("401")
	}
	if Classify(500, 0, nil) != ClassServer {
		t.Fatal("500")
	}
	if Classify(400, 0, nil) != ClassParam {
		t.Fatal("400")
	}
	if Classify(404, 0, nil) != ClassParam {
		t.Fatal("404→param（保守，不转移）")
	}
}

func TestAnthropicAdapterHeadersAndNoLocalTokenLeak(t *testing.T) {
	var gotKey, gotVer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		gotVer = r.Header.Get("Anthropic-Version")
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1"}`))
	}))
	defer srv.Close()
	a := NewAnthropicAdapter()
	res, err := a.Do(context.Background(), DoRequest{
		BaseURL: srv.URL, AllowPrivate: true, APIKey: "sk-upstream",
		UpstreamModel: "up-opus", RawBody: []byte(`{"model":"public-alias","messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if string(b) != `{"id":"msg_1"}` {
		t.Fatalf("body %s", b)
	}
	if gotKey != "sk-upstream" || gotVer == "" {
		t.Fatalf("auth headers wrong: key=%q ver=%q", gotKey, gotVer)
	}
}

func TestAdapterDeniesRedirect(t *testing.T) {
	// 重定向目标：独立本地服务器，计数收到的请求数以直接证明“未跟随”。
	var targetHits int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits++
		w.Write([]byte(`followed`))
	}))
	defer target.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/elsewhere", http.StatusFound)
	}))
	defer srv.Close()
	a := NewAnthropicAdapter()
	res, err := a.Do(context.Background(), DoRequest{BaseURL: srv.URL, AllowPrivate: true, APIKey: "k", RawBody: []byte(`{}`)})
	if err == nil {
		t.Fatal("redirect must be denied")
	}
	// 拒绝跟随时 3xx 必须原样浮出为 302；CheckRedirect 回归（跟随）会让 Do
	// 在目标处拿到 200 或在 DNS/connect 处失败而非返回 302，断言必然红。
	if res.StatusCode != http.StatusFound {
		t.Fatalf("denied redirect must surface %d, got status %d (err=%v)", http.StatusFound, res.StatusCode, err)
	}
	if targetHits != 0 {
		t.Fatalf("redirect target must not be followed, got %d hits", targetHits)
	}
}

func TestProbeStages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"type":"authentication_error"}}`))
	}))
	defer srv.Close()
	res := Probe(context.Background(), "anthropic", srv.URL, "bad-key", true)
	if res.OK || res.Stage != "auth" {
		t.Fatalf("probe: %+v", res)
	}
	if strings.Contains(res.Error, "bad-key") {
		t.Fatalf("probe leaked key: %q", res.Error)
	}
}

func TestPresetsCatalogAndLookup(t *testing.T) {
	ps := Presets()
	if len(ps) != 4 {
		t.Fatalf("presets %d", len(ps))
	}
	p, ok := LookupPreset("openai-compatible")
	if !ok || p.Protocol != "openai" || p.DefaultBaseURL != "" {
		t.Fatalf("lookup: %+v %v", p, ok)
	}
	if _, ok := LookupPreset("nope"); ok {
		t.Fatal("unknown preset must miss")
	}
	// 每个 preset 的 protocol 合法（anthropic|openai）且 official 有默认 URL
	for _, p := range ps {
		if p.Protocol != "anthropic" && p.Protocol != "openai" {
			t.Fatalf("bad protocol %q", p.Protocol)
		}
		if strings.HasSuffix(p.ID, "-official") && p.DefaultBaseURL == "" {
			t.Fatalf("%s missing default url", p.ID)
		}
	}
}

func TestAdapterForProtocolDispatch(t *testing.T) {
	a, err := AdapterForProtocol("anthropic", nil)
	if err != nil || a == nil {
		t.Fatalf("anthropic: %v %v", a, err)
	}
	o, err := AdapterForProtocol("openai", nil)
	if err != nil || o == nil {
		t.Fatalf("openai: %v %v", o, err)
	}
	if _, err := AdapterForProtocol("grpc", nil); err == nil {
		t.Fatal("unknown protocol must error")
	}
}

func TestOpenAIAdapterNonStreamEndToEnd(t *testing.T) {
	var gotAuth, gotPath string
	var gotBody map[string]any
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"cmpl-1","model":"up-1","choices":[{"message":{"content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`))
	}))
	defer up.Close()
	ad := NewOpenAIAdapter(nil)
	res, err := ad.Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "up-1",
		RawBody: []byte(`{"model":"up-1","max_tokens":8,"system":"sys","messages":[{"role":"user","content":"q"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("status %d", res.StatusCode)
	}
	if gotAuth != "Bearer sk-x" || gotPath != "/v1/chat/completions" {
		t.Fatalf("auth=%q path=%q", gotAuth, gotPath)
	}
	if gotBody["messages"] == nil {
		t.Fatalf("body not converted: %v", gotBody)
	}
	out, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(out), `"stop_reason":"end_turn"`) {
		t.Fatalf("response not converted: %s", out)
	}
}

func TestOpenAIAdapterStreamEndToEnd(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, s := range []string{
			"data: {\"id\":\"c\",\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":\"ok\"}}]}\n\n",
			"data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n",
			"data: [DONE]\n\n",
		} {
			w.Write([]byte(s))
			f.Flush()
		}
	}))
	defer up.Close()
	ad := NewOpenAIAdapter(nil)
	res, err := ad.Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "up-1", Stream: true,
		RawBody: []byte(`{"model":"up-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	for _, want := range []string{"message_start", "text_delta", "message_stop"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("stream missing %q:\n%s", want, b)
		}
	}
}

func TestOpenAIAdapter429Passthrough(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "9")
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"rate limited","type":"rate_limit_error"}}`))
	}))
	defer up.Close()
	res, err := NewOpenAIAdapter(nil).Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "m",
		RawBody: []byte(`{"model":"m","max_tokens":1,"messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	// 4xx 正常返回（err==nil），状态码与 Retry-After 保留供分类与冷却
	if res.StatusCode != 429 || res.RetryAfterSec != 9 {
		t.Fatalf("%d %d", res.StatusCode, res.RetryAfterSec)
	}
}

func TestProbeBothProtocols(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("openai probe path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"choices":[{"message":{"content":"x"},"finish_reason":"stop"}]}`))
	}))
	defer up.Close()
	if res := Probe(context.Background(), "openai", up.URL, "sk-x", true); !res.OK {
		t.Fatalf("openai probe: %+v", res)
	}
	if res := Probe(context.Background(), "anthropic", "http://127.0.0.1:1", "k", true); res.OK {
		t.Fatal("anthropic probe against dead port must fail")
	}
}
