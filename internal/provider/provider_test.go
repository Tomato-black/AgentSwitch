package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"AgentSwitch/internal/protocol"
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

func TestAnthropicAdapterStreamInterrupted(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"par\"}}\n\n"))
		f.Flush()
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer up.Close()
	res, err := NewAnthropicAdapter().Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "up-1", Stream: true,
		RawBody: []byte(`{"model":"up-1","stream":true,"messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "event: error") {
		t.Fatalf("missing error event:\n%s", b)
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

func TestOpenAIAdapterStreamInterruptedEmitsError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte(`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","function":{"name":"Write","arguments":"{\"file_path\":\"/tmp/a\""}}]}}]}` + "\n\n"))
		f.Flush()
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, _, _ := hj.Hijack()
		conn.Close() // 上游中断：无 finish_reason/[DONE]
	}))
	defer up.Close()
	res, err := NewOpenAIAdapter(nil).Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "up-1", Stream: true,
		RawBody: []byte(`{"model":"up-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	out := string(b)
	if !strings.Contains(out, "event: error") || !strings.Contains(out, "upstream stream interrupted") {
		t.Fatalf("missing error event:\n%s", out)
	}
	if strings.Contains(out, "message_stop") {
		t.Fatalf("truncated stream must not emit message_stop:\n%s", out)
	}
	if strings.Contains(out, `"name":"Write"`) {
		t.Fatalf("incomplete tool block leaked:\n%s", out)
	}
}

func TestOpenAIAdapterStreamInterruptedLogsDroppedTools(t *testing.T) {
	var logs bytes.Buffer
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte(`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","function":{"name":"Write","arguments":"{\"file_path\":\"/tmp/a\""}}]}}]}` + "\n\n"))
		f.Flush()
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer up.Close()
	ad := NewOpenAIAdapter(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	res, err := ad.Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "up-1", Stream: true,
		RawBody: []byte(`{"model":"up-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	got := logs.String()
	if !strings.Contains(got, "upstream stream interrupted") {
		t.Fatalf("missing interrupted warning:\n%s", got)
	}
	if !strings.Contains(got, "dropped incomplete tool call") || !strings.Contains(got, "count=1") {
		t.Fatalf("missing dropped-tool warning with count:\n%s", got)
	}
}

func TestOpenAIAdapterStreamCancelSilent(t *testing.T) {
	var logs bytes.Buffer
	started := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte(`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","function":{"name":"Write","arguments":"{\"file_path\"/tmp/a\""}}]}}]}` + "\n\n"))
		f.Flush()
		// 工具块本身要收口才下发；追加一个 text delta 让客户端有可观测信号，
		// 读到 text_delta 即证明其前面的工具块已被转换器累积。
		w.Write([]byte(`data: {"choices":[{"delta":{"content":"x"}}]}` + "\n\n"))
		f.Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer up.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ad := NewOpenAIAdapter(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
	res, err := ad.Do(ctx, DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "up-1", Stream: true,
		RawBody: []byte(`{"model":"up-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	br := bufio.NewReader(res.Body)
	var seen strings.Builder
	for !strings.Contains(seen.String(), "text_delta") {
		line, rerr := br.ReadString('\n')
		if rerr != nil {
			t.Fatalf("read before cancel: %v\n%s", rerr, seen.String())
		}
		seen.WriteString(line)
	}
	cancel()
	rest, _ := io.ReadAll(br)
	res.Body.Close()
	out := seen.String() + string(rest)
	if strings.Contains(out, "event: error") {
		t.Fatalf("canceled stream must not emit error event:\n%s", out)
	}
	if strings.Contains(logs.String(), "dropped incomplete tool call") {
		t.Fatalf("cancel must be silent, got logs:\n%s", logs.String())
	}
}

func TestWrapPassthroughStream(t *testing.T) {
	t.Run("normal terminal passes through unchanged", func(t *testing.T) {
		src := io.NopCloser(strings.NewReader("data: a\n\ndata: [DONE]\n\n"))
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(), src, []byte("ERR\n\n"), "[DONE]"))
		if string(out) != "data: a\n\ndata: [DONE]\n\n" {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("clean EOF without terminal appends error", func(t *testing.T) {
		src := io.NopCloser(strings.NewReader("data: a\n\n"))
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(), src, []byte("ERR\n\n"), "[DONE]"))
		if string(out) != "data: a\n\nERR\n\n" {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("read error appends error", func(t *testing.T) {
		er := &errAfterReader{data: "data: a\n\n"}
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(), er, []byte("ERR\n\n"), "[DONE]"))
		if string(out) != "data: a\n\nERR\n\n" {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("canceled ctx does not append", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		src := io.NopCloser(strings.NewReader("data: a\n\n"))
		out, _ := io.ReadAll(WrapPassthroughStream(ctx, src, []byte("ERR\n\n"), "[DONE]"))
		if string(out) != "data: a\n\n" {
			t.Fatalf("out = %q", out)
		}
	})
}

type errAfterReader struct {
	data string
	done bool
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	if e.done {
		return 0, errors.New("boom")
	}
	e.done = true
	n := copy(p, e.data)
	return n, nil
}
func (e *errAfterReader) Close() error { return nil }

// oneByteReader 每次 Read 只交出 1 字节，用于验证终止标记跨 chunk 边界时
// 的尾部窗口逻辑（完整数据一次交出无法暴露窗口实现错误）。
type oneByteReader struct{ data string }

func (r *oneByteReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, io.EOF
	}
	p[0] = r.data[0]
	r.data = r.data[1:]
	return 1, nil
}

func (r *oneByteReader) Close() error { return nil }

func TestWrapPassthroughStreamTerminalAcrossChunks(t *testing.T) {
	t.Run("[DONE] split into single-byte chunks", func(t *testing.T) {
		const stream = "data: a\n\ndata: [DONE]\n\n"
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(),
			&oneByteReader{data: stream}, []byte("ERR\n\n"), "[DONE]"))
		if string(out) != stream {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("anthropic message_stop split into single-byte chunks", func(t *testing.T) {
		const stream = "event: content_block_delta\ndata: {}\n\nevent: message_stop\ndata: {}\n\n"
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(),
			&oneByteReader{data: stream}, []byte("ERR\n\n"), "event: message_stop", "event: error"))
		if string(out) != stream {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("anthropic error terminal split into single-byte chunks", func(t *testing.T) {
		const stream = "event: error\ndata: {\"type\":\"error\"}\n\n"
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(),
			&oneByteReader{data: stream}, []byte("ERR\n\n"), "event: message_stop", "event: error"))
		if string(out) != stream {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("empty terminal marker is ignored", func(t *testing.T) {
		const stream = "data: a\n\n"
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(),
			io.NopCloser(strings.NewReader(stream)), []byte("ERR\n\n"), ""))
		if string(out) != stream+"ERR\n\n" {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("truncated marker still appends error on fresh event boundary", func(t *testing.T) {
		const stream = "data: a\n\ndata: [DON"
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(),
			&oneByteReader{data: stream}, []byte("ERR\n\n"), "[DONE]"))
		// 残块停在事件中途：error 事件前必须补 \n\n，不能与残缺事件粘连。
		if string(out) != stream+"\n\nERR\n\n" {
			t.Fatalf("out = %q", out)
		}
	})
}

// TestWrapPassthroughStreamEventAware 锁定两件事：终止标记只在完整 SSE 事件内
// 匹配（正文中的同名字面量不算正常结束）；追加错误事件前补齐事件边界。
func TestWrapPassthroughStreamEventAware(t *testing.T) {
	t.Run("openai truncation mid-event gets fresh boundary", func(t *testing.T) {
		const prefix = `data: {"choices":[{"delta":{"content":"par"}`
		errChunk := protocol.OpenAIStreamErrorChunk("upstream stream interrupted")
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(),
			io.NopCloser(strings.NewReader(prefix)), errChunk, "[DONE]"))
		if !strings.HasPrefix(string(out), prefix) {
			t.Fatalf("prefix must be unchanged:\n%s", out)
		}
		if !strings.Contains(string(out), "\n\ndata: {\"error\"") {
			t.Fatalf("error event must start on a fresh SSE boundary:\n%s", out)
		}
	})

	t.Run("anthropic truncation mid-event gets fresh boundary", func(t *testing.T) {
		const prefix = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"par\""
		errEvent := protocol.AnthropicErrorEvent("upstream stream interrupted")
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(),
			io.NopCloser(strings.NewReader(prefix)), errEvent,
			"event: message_stop", "event: error"))
		if !strings.HasPrefix(string(out), prefix) {
			t.Fatalf("prefix must be unchanged:\n%s", out)
		}
		if !strings.Contains(string(out), "\n\nevent: error") {
			t.Fatalf("error event must start on a fresh SSE boundary:\n%s", out)
		}
	})

	t.Run("terminal literals inside JSON content are not terminal", func(t *testing.T) {
		const stream = `data: {"choices":[{"delta":{"content":"discuss [DONE] and event: message_stop"}}]}` + "\n\n"
		errChunk := protocol.OpenAIStreamErrorChunk("upstream stream interrupted")
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(),
			io.NopCloser(strings.NewReader(stream)), errChunk, "[DONE]", "event: message_stop"))
		if string(out) != stream+string(errChunk) {
			t.Fatalf("literal terminal text in content must not suppress truncation:\n%s", out)
		}
	})

	t.Run("read error after terminal marker appends nothing", func(t *testing.T) {
		const stream = "data: [DONE]\n\n"
		src := &errAfterReader{data: stream}
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(), src, []byte("ERR\n\n"), "[DONE]"))
		if string(out) != stream {
			t.Fatalf("terminal seen: later read error must not append a second error:\n%s", out)
		}
	})

	t.Run("finish_reason-only stream is normal", func(t *testing.T) {
		const stream = `data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(),
			io.NopCloser(strings.NewReader(stream)), []byte("ERR\n\n"),
			"[DONE]", `"finish_reason":"`))
		if string(out) != stream {
			t.Fatalf("finish_reason-only stream must pass through unchanged:\n%s", out)
		}
	})

	t.Run("null finish_reason is not terminal", func(t *testing.T) {
		const stream = `data: {"choices":[{"delta":{},"finish_reason":null}]}` + "\n\n"
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(),
			io.NopCloser(strings.NewReader(stream)), []byte("ERR\n\n"),
			"[DONE]", `"finish_reason":"`))
		if string(out) != stream+"ERR\n\n" {
			t.Fatalf("null finish_reason must still count as truncation:\n%s", out)
		}
	})
}

// TestAnthropicEndpointAllPresetBases 锁死 spec 2026-10-23 §6 全部含 anthropic
// 协议预置 base_url（按量 + Coding Plan 订阅）的端点拼接：base 无 /v1 拼
// /v1/messages（Anthropic SDK 约定），已含 /v1 只拼 /messages。数据变更须先改 spec。
func TestAnthropicEndpointAllPresetBases(t *testing.T) {
	cases := []struct{ base, want string }{
		// 按量（base 无 /v1 → 拼 /v1/messages）
		{"https://api.xiaomimimo.com/anthropic", "https://api.xiaomimimo.com/anthropic/v1/messages"},
		{"https://dashscope.aliyuncs.com/apps/anthropic", "https://dashscope.aliyuncs.com/apps/anthropic/v1/messages"},
		{"https://api.deepseek.com/anthropic", "https://api.deepseek.com/anthropic/v1/messages"},
		{"https://api.moonshot.cn/anthropic", "https://api.moonshot.cn/anthropic/v1/messages"},
		{"https://open.bigmodel.cn/api/anthropic", "https://open.bigmodel.cn/api/anthropic/v1/messages"},
		{"https://api.siliconflow.cn", "https://api.siliconflow.cn/v1/messages"},
		{"https://api.stepfun.com", "https://api.stepfun.com/v1/messages"},
		{"https://ark.cn-beijing.volces.com/api/compatible", "https://ark.cn-beijing.volces.com/api/compatible/v1/messages"},
		{"https://api.anthropic.com", "https://api.anthropic.com/v1/messages"},
		// base 已含 /v1 → 只拼 /messages
		{"https://openrouter.ai/api/v1", "https://openrouter.ai/api/v1/messages"},
		// 订阅（Coding Plan）
		{"https://ark.cn-beijing.volces.com/api/coding", "https://ark.cn-beijing.volces.com/api/coding/v1/messages"},
		{"https://api.kimi.com/coding/", "https://api.kimi.com/coding/v1/messages"}, // 官方原文带尾斜杠
		{"https://coding.dashscope.aliyuncs.com/apps/anthropic", "https://coding.dashscope.aliyuncs.com/apps/anthropic/v1/messages"},
		{"https://api.stepfun.com/step_plan", "https://api.stepfun.com/step_plan/v1/messages"},
		{"https://token-plan-cn.xiaomimimo.com/anthropic", "https://token-plan-cn.xiaomimimo.com/anthropic/v1/messages"},
		// 智谱订阅与按量 anthropic 同址，不重复列
	}
	for _, tc := range cases {
		got, err := anthropicEndpoint(tc.base)
		if err != nil {
			t.Fatalf("anthropicEndpoint(%q) error: %v", tc.base, err)
		}
		if got != tc.want {
			t.Errorf("anthropicEndpoint(%q) = %q, want %q", tc.base, got, tc.want)
		}
	}
}

// TestUserAgentPassthroughAdapters 覆盖 spec 2026-10-23 §6 检查项 2（Kimi
// Code 条款要求透传客户端 UA）：DoRequest.UserAgent 非空时两 adapter 出站
// 带同值；空时不设置（保持 Go 默认，不发明空 UA 头）。
func TestUserAgentPassthroughAdapters(t *testing.T) {
	const ua = "claude-cli/2.0.0 (test)"
	seen := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen[r.URL.Path] = r.Header.Get("User-Agent")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	anthropic := NewAnthropicAdapter()
	res, err := anthropic.Do(context.Background(), DoRequest{
		BaseURL: srv.URL, AllowPrivate: true, APIKey: "k", UserAgent: ua, RawBody: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if seen["/v1/messages"] != ua {
		t.Fatalf("anthropic UA = %q, want %q", seen["/v1/messages"], ua)
	}

	openai := NewOpenAIAdapter(nil)
	res, err = openai.Do(context.Background(), DoRequest{
		BaseURL: srv.URL, AllowPrivate: true, APIKey: "k", UserAgent: ua, RawBody: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if seen["/v1/chat/completions"] != ua {
		t.Fatalf("openai UA = %q, want %q", seen["/v1/chat/completions"], ua)
	}

	// 空 UA：不显式设置（Go 默认 Go-http-client/1.1，非空）。
	res, err = openai.Do(context.Background(), DoRequest{
		BaseURL: srv.URL, AllowPrivate: true, APIKey: "k", RawBody: []byte(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if seen["/v1/chat/completions"] == "" {
		t.Fatal("空 UserAgent 时上游收到空 UA 头，want Go 默认")
	}
}
