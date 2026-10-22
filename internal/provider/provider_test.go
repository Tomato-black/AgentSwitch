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
	// 404/405 = 端点路径不存在/方法不对（渠道 base_url 或协议配错），不是客户端
	// 参数问题：归 endpoint，允许冷却并转移到下一目标（2026-10-22 决策）。
	if Classify(404, 0, []byte("<html>404 Not Found</html>")) != ClassEndpoint {
		t.Fatal("404→endpoint（允许转移）")
	}
	if Classify(405, 0, nil) != ClassEndpoint {
		t.Fatal("405→endpoint")
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

// Anthropic 协议的 base_url 按厂商文档语义解释：文档给的是 API 根
// （如 https://token-plan-cn.xiaomimimo.com/anthropic），端点路径是 /v1/messages
// ——与官方 Anthropic SDK / Claude Code 的 ANTHROPIC_BASE_URL 约定一致。
// base 已带 /v1 时不得重复（/v1/v1/messages），裸主机仍补默认 /v1。
func TestAnthropicAdapterDocumentedBaseURLPath(t *testing.T) {
	cases := []struct{ name, basePath, wantPath string }{
		{"documented-base", "/anthropic", "/anthropic/v1/messages"},
		{"base-already-v1", "/anthropic/v1", "/anthropic/v1/messages"},
		{"bare-host", "", "/v1/messages"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			a := NewAnthropicAdapter()
			res, err := a.Do(context.Background(), DoRequest{
				BaseURL: srv.URL + tc.basePath, AllowPrivate: true, APIKey: "k",
				RawBody: []byte(`{}`),
			})
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if gotPath != tc.wantPath {
				t.Fatalf("path = %q, want %q", gotPath, tc.wantPath)
			}
		})
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

func TestAdapterConstructors(t *testing.T) {
	if NewAnthropicAdapter() == nil {
		t.Fatal("anthropic adapter nil")
	}
	if NewOpenAIAdapter(nil) == nil {
		t.Fatal("openai adapter nil")
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

func TestJoinAPIPath(t *testing.T) {
	cases := []struct {
		base, suffix, want string
	}{
		// 无路径或仅 "/"：插入 /v1（OpenAI/Anthropic 官方形态）
		{"https://api.openai.com", "/chat/completions", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/", "/chat/completions", "https://api.openai.com/v1/chat/completions"},
		{"https://api.anthropic.com", "/messages", "https://api.anthropic.com/v1/messages"},
		// 已以 /v1（或 /v1/）结尾：直接拼后缀，避免双 /v1
		{"https://api.openai.com/v1", "/chat/completions", "https://api.openai.com/v1/chat/completions"},
		{"https://api.openai.com/v1/", "/chat/completions", "https://api.openai.com/v1/chat/completions"},
		{"https://api.anthropic.com/v1", "/messages", "https://api.anthropic.com/v1/messages"},
		{"https://api.anthropic.com/v1/", "/messages", "https://api.anthropic.com/v1/messages"},
		// 供应商完整 API 前缀（非平凡路径且不以 /v1 结尾）：只拼后缀，不插 /v1
		{"https://open.bigmodel.cn/api/paas/v4", "/chat/completions", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"https://open.bigmodel.cn/api/paas/v4/", "/chat/completions", "https://open.bigmodel.cn/api/paas/v4/chat/completions"},
		{"https://ark.cn-beijing.volces.com/api/v3", "/chat/completions", "https://ark.cn-beijing.volces.com/api/v3/chat/completions"},
		{"https://generativelanguage.googleapis.com/v1beta/openai", "/chat/completions", "https://generativelanguage.googleapis.com/v1beta/openai/chat/completions"},
	}
	for _, c := range cases {
		got, err := JoinAPIPath(c.base, c.suffix)
		if err != nil {
			t.Errorf("JoinAPIPath(%q,%q) err=%v", c.base, c.suffix, err)
			continue
		}
		if got != c.want {
			t.Errorf("JoinAPIPath(%q,%q)=%q want %q", c.base, c.suffix, got, c.want)
		}
	}
	if _, err := JoinAPIPath("not-a-url", "/messages"); err == nil {
		t.Error("invalid base must error")
	}
	if _, err := JoinAPIPath("https://x.example.com", ""); err == nil {
		t.Error("empty suffix must error")
	}
}

// TestOpenAIAdapterVendorPrefixPath：base_url 自带供应商前缀（智谱形态）时
// 适配器不得再插入 /v1（旧拼接得到 …/api/paas/v4/v1/chat/completions 必 404）。
func TestOpenAIAdapterVendorPrefixPath(t *testing.T) {
	var gotPath string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"cmpl-1","model":"m","choices":[{"message":{"content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer up.Close()
	res, err := NewOpenAIAdapter(nil).Do(context.Background(), DoRequest{
		BaseURL: up.URL + "/api/paas/v4", AllowPrivate: true, APIKey: "sk-x",
		RawBody: []byte(`{"model":"m","max_tokens":1,"messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if gotPath != "/api/paas/v4/chat/completions" {
		t.Fatalf("path=%q want /api/paas/v4/chat/completions", gotPath)
	}
}

// TestAnthropicAdapterNoDoubleV1：base_url 已以 /v1 结尾时不再插 /v1。
func TestAnthropicAdapterNoDoubleV1(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"id":"msg_1"}`))
	}))
	defer srv.Close()
	res, err := NewAnthropicAdapter().Do(context.Background(), DoRequest{
		BaseURL: srv.URL + "/v1", AllowPrivate: true, APIKey: "k", RawBody: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if gotPath != "/v1/messages" {
		t.Fatalf("path=%q want /v1/messages", gotPath)
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
