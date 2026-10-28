package bridge

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/presets"
)

func channelsSvc(t *testing.T) (Channels, credential.Store) {
	t.Helper()
	a := tempApp(t)
	// 凭据走 app 的加密存储（库内 AES-256-GCM），不注入外部后端。
	return Channels{A: a}, a.Creds()
}

// TestCreateChannelStoresOneKey：Create 后 HasKey、keychain 可 Get。
func TestCreateChannelStoresOneKey(t *testing.T) {
	cs, creds := channelsSvc(t)
	c, err := cs.Create("", "main", "https://api.example.com", "anthropic", "sk-abc12345", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if !c.HasKey {
		t.Fatalf("HasKey after Create: %+v", c)
	}
	got, err := creds.Get(credential.ChannelKey(c.ID))
	if err != nil || string(got) != "sk-abc12345" {
		t.Fatalf("keychain Get: %q %v", got, err)
	}
	d, err := cs.Get(c.ID)
	if err != nil || !d.HasKey || d.SuggestionCount != 0 {
		t.Fatalf("detail %+v %v", d, err)
	}
}

// TestUpdateEmptyKeyKeepsOld：Update(apiKey="") 后旧 Key 仍在。
func TestUpdateEmptyKeyKeepsOld(t *testing.T) {
	cs, creds := channelsSvc(t)
	c, err := cs.Create("", "main", "https://api.example.com", "anthropic", "sk-old12345", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.Update(c.ID, "main2", "https://api2.example.com", "", false, false); err != nil {
		t.Fatal(err)
	}
	got, err := creds.Get(credential.ChannelKey(c.ID))
	if err != nil || string(got) != "sk-old12345" {
		t.Fatalf("old key must remain: %q %v", got, err)
	}
	d, err := cs.Get(c.ID)
	if err != nil || !d.HasKey || d.Name != "main2" {
		t.Fatalf("detail %+v %v", d, err)
	}
	// 非空 apiKey 覆盖旧 Key
	if err := cs.Update(c.ID, "main2", "https://api2.example.com", "sk-new67890", false, false); err != nil {
		t.Fatal(err)
	}
	got, err = creds.Get(credential.ChannelKey(c.ID))
	if err != nil || string(got) != "sk-new67890" {
		t.Fatalf("key must be replaced: %q %v", got, err)
	}
}

// TestRemoveDeletesKeyAndSuggestions：Remove 后 keychain Delete 被调（fallback
// file store 断言）且建议列表清空。
func TestRemoveDeletesKeyAndSuggestions(t *testing.T) {
	cs, creds := channelsSvc(t)
	c, err := cs.Create("", "main", "https://api.example.com", "anthropic", "sk-rm123456", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.SetSuggestions(c.ID, []string{"m-a", "m-b"}); err != nil {
		t.Fatal(err)
	}
	cs.Remove(c.ID)
	if _, err := creds.Get(credential.ChannelKey(c.ID)); !errors.Is(err, credential.ErrNotFound) {
		t.Fatalf("key must be deleted from fallback file store: %v", err)
	}
	if got := cs.Suggestions(c.ID); len(got) != 0 {
		t.Fatalf("suggestions must be gone: %v", got)
	}
	if _, err := cs.Get(c.ID); err == nil {
		t.Fatal("channel must be gone")
	}
}

// TestSetSuggestionsReplaces：两次 SetSuggestions 以第二次为准。
func TestSetSuggestionsReplaces(t *testing.T) {
	cs, _ := channelsSvc(t)
	c, err := cs.Create("", "main", "https://api.example.com", "anthropic", "sk-abc12345", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := cs.SetSuggestions(c.ID, []string{"m-a", "m-b"}); err != nil {
		t.Fatal(err)
	}
	if err := cs.SetSuggestions(c.ID, []string{"m-c"}); err != nil {
		t.Fatal(err)
	}
	got := cs.Suggestions(c.ID)
	if len(got) != 1 || got[0] != "m-c" {
		t.Fatalf("suggestions: %v", got)
	}
	d, err := cs.Get(c.ID)
	if err != nil || d.SuggestionCount != 1 || len(d.Suggestions) != 1 || d.Suggestions[0] != "m-c" {
		t.Fatalf("detail %+v %v", d, err)
	}
}

func TestChannelsCreateValidatesURLAndProtocol(t *testing.T) {
	cs, _ := channelsSvc(t)
	if _, err := cs.Create("", "x", "", "anthropic", "sk-a", true, false); err == nil ||
		!strings.Contains(err.Error(), "Base URL") {
		t.Fatalf("empty baseURL: %v", err)
	}
	if _, err := cs.Create("", "x", "https://x.example.com", "gopher", "sk-a", true, false); err == nil {
		t.Fatal("unknown protocol must error")
	}
	if _, err := cs.Create("", "x", "http://relay.example.com", "anthropic", "sk-a", true, false); err == nil {
		t.Fatal("non-loopback http must be rejected")
	}
	c, err := cs.Create("anthropic-official", "ok", "https://api.anthropic.com", "anthropic", "sk-a", true, false)
	if err != nil || c.PresetID != "anthropic-official" {
		t.Fatalf("create: %+v %v", c, err)
	}
}

func TestChannelsTestAndFetchModels(t *testing.T) {
	var gotOpenAIBearer bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			if r.Header.Get("Authorization") == "" && r.Header.Get("X-Api-Key") == "" {
				t.Error("no auth header")
			}
			if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk-") {
				gotOpenAIBearer = true
			}
			w.Write([]byte(`{"data":[{"id":"m-a"},{"id":"m-b"}]}`))
		default: // probe（anthropic→/v1/messages，openai→/v1/chat/completions）
			w.Write([]byte(`{}`))
		}
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("anthropic-compatible", "m", up.URL, "anthropic", "sk-abc12345", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if res := cs.Test(c.ID); !res.OK {
		t.Fatalf("channel test: %+v", res)
	}
	ids, err := cs.FetchModels(c.ID)
	if err != nil || len(ids) != 2 || ids[1] != "m-b" {
		t.Fatalf("fetch: %v %v", ids, err)
	}
	// openai 渠道：断言上游收到 Authorization: Bearer
	c2, err := cs.Create("openai-compatible", "o", up.URL, "openai", "sk-openai6789", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.FetchModels(c2.ID); err != nil {
		t.Fatal(err)
	}
	if !gotOpenAIBearer {
		t.Fatal("openai fetch must send Authorization: Bearer")
	}
	// 无密钥渠道报错
	c3, err := cs.Create("openai-compatible", "o3", up.URL, "openai", "", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if c3.HasKey {
		t.Fatal("empty apiKey must leave HasKey false")
	}
	if _, err := cs.FetchModels(c3.ID); err == nil {
		t.Fatal("fetch without key must error")
	}
	if res := cs.Test(c3.ID); res.OK {
		t.Fatalf("test without key must fail: %+v", res)
	}
}

// TestFetchModelsFollowsCursorPagination：上游返回 has_more + next_cursor 时
// 按 cursor 续页；跨页按 id 去重且保序（重复的 m-b 只保留首个出现位）。
func TestFetchModelsFollowsCursorPagination(t *testing.T) {
	var queries []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.RawQuery == "" {
			w.Write([]byte(`{"data":[{"id":"m-a"},{"id":"m-b"}],"has_more":true,"next_cursor":"abc"}`))
			return
		}
		if r.URL.Query().Get("cursor") != "abc" {
			t.Errorf("cursor query = %q", r.URL.RawQuery)
		}
		w.Write([]byte(`{"data":[{"id":"m-b"},{"id":"m-c"}],"has_more":false}`))
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("", "p", up.URL, "openai", "sk-page-1", true, true)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := cs.FetchModels(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"m-a", "m-b", "m-c"}
	if len(ids) != len(want) || ids[0] != want[0] || ids[1] != want[1] || ids[2] != want[2] {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %d, want 2: %v", len(queries), queries)
	}
}

// TestFetchModelsFallsBackToAfterID：无 next_cursor 但 has_more=true 时用
// Anthropic 风格 after_id=<最后一条 id> 续页。
func TestFetchModelsFallsBackToAfterID(t *testing.T) {
	var queries []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		if r.URL.RawQuery == "" {
			w.Write([]byte(`{"data":[{"id":"m-a"},{"id":"m-b"}],"has_more":true}`))
			return
		}
		if r.URL.Query().Get("after_id") != "m-b" {
			t.Errorf("after_id query = %q", r.URL.RawQuery)
		}
		w.Write([]byte(`{"data":[{"id":"m-c"}],"has_more":false}`))
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("", "a", up.URL, "anthropic", "sk-after-1", true, true)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := cs.FetchModels(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[2] != "m-c" {
		t.Fatalf("ids = %v", ids)
	}
	if len(queries) != 2 {
		t.Fatalf("requests = %d", len(queries))
	}
}

// TestFetchModelsTotalCap：单页返回 500 条且 has_more=true 时停止续页
// （达到 fetchModelsMaxTotal）。
func TestFetchModelsTotalCap(t *testing.T) {
	var requests int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		items := make([]string, 0, 500)
		for i := 0; i < 500; i++ {
			if i > 0 {
				items = append(items, ",")
			}
			items = append(items, `{"id":"m-`)
			items = append(items, string(rune('a'+i%26)))
			items = append(items, ``)
			items = append(items, string(rune('a'+i/26)))
			items = append(items, `"}`)
		}
		w.Write([]byte(`{"data":[` + strings.Join(items, "") + `],"has_more":true,"next_cursor":"next"}`))
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("", "cap", up.URL, "openai", "sk-cap", true, true)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := cs.FetchModels(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 500 {
		t.Fatalf("ids = %d, want 500", len(ids))
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1 (总条数上限后不再续页)", requests)
	}
}

// TestFetchModelsPageCapStops：上游无限 has_more 时最多 fetchModelsMaxPages 页。
func TestFetchModelsPageCapStops(t *testing.T) {
	var requests int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.Write([]byte(`{"data":[{"id":"m-` + string(rune('a'+requests)) + `"}],"has_more":true,"next_cursor":"n"}`))
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("", "loop", up.URL, "openai", "sk-loop", true, true)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := cs.FetchModels(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != fetchModelsMaxPages {
		t.Fatalf("ids = %d, want %d", len(ids), fetchModelsMaxPages)
	}
	if requests != fetchModelsMaxPages {
		t.Fatalf("requests = %d, want %d", requests, fetchModelsMaxPages)
	}
}

// TestFetchModelsPartialOnError：第一页成功、第二页非 200 → 返回已收集的
// 部分结果 + 错误。
func TestFetchModelsPartialOnError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery == "" {
			w.Write([]byte(`{"data":[{"id":"m-a"},{"id":"m-b"}],"has_more":true,"next_cursor":"n"}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("", "partial", up.URL, "openai", "sk-partial", true, true)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := cs.FetchModels(c.ID)
	if err == nil {
		t.Fatal("第二页 500 应报错")
	}
	if len(ids) != 2 || ids[0] != "m-a" || ids[1] != "m-b" {
		t.Fatalf("ids = %v, want [m-a m-b]", ids)
	}
	if !strings.Contains(err.Error(), "上游状态") {
		t.Fatalf("err = %v", err)
	}
}

// TestFetchModelsFromDraftValidates：协议非法、缺 Key、非法 Base URL 依次拒绝。
func TestFetchModelsFromDraftValidates(t *testing.T) {
	cs, _ := channelsSvc(t)
	if _, err := cs.FetchModelsFromDraft("https://api.example.com", "bogus", "sk-1", false); err == nil {
		t.Fatal("非法协议应报错")
	}
	if _, err := cs.FetchModelsFromDraft("https://api.example.com", "openai", "", false); err == nil {
		t.Fatal("缺 Key 应报错")
	}
	if _, err := cs.FetchModelsFromDraft("ftp://api.example.com", "openai", "sk-1", false); err == nil {
		t.Fatal("非法 scheme 应报错")
	}
	if _, err := cs.FetchModelsFromDraft("http://127.0.0.1:9", "openai", "sk-1", false); err == nil {
		t.Fatal("私网上游未放行时应报错")
	}
}

// TestFetchModelsFromDraftSendsProtocolHeaders：openai 用 Bearer，
// anthropic 用 X-Api-Key + Anthropic-Version。
func TestFetchModelsFromDraftSendsProtocolHeaders(t *testing.T) {
	var gotBearer, gotAnthropic bool
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.Header.Get("Authorization"), "Bearer sk-draft-") {
			gotBearer = true
		}
		if r.Header.Get("X-Api-Key") == "sk-draft-anthropic" && r.Header.Get("Anthropic-Version") == "2023-06-01" {
			gotAnthropic = true
		}
		w.Write([]byte(`{"data":[{"id":"m-1"}]}`))
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	ids, err := cs.FetchModelsFromDraft(up.URL, "openai", "sk-draft-openai", true)
	if err != nil || len(ids) != 1 || ids[0] != "m-1" {
		t.Fatalf("draft openai: %v %v", ids, err)
	}
	if !gotBearer {
		t.Fatal("openai draft 应发 Authorization: Bearer")
	}
	ids, err = cs.FetchModelsFromDraft(up.URL, "anthropic", "sk-draft-anthropic", true)
	if err != nil || len(ids) != 1 {
		t.Fatalf("draft anthropic: %v %v", ids, err)
	}
	if !gotAnthropic {
		t.Fatal("anthropic draft 应发 X-Api-Key + Anthropic-Version")
	}
}

// TestModelsURLCandidates：候选 URL 构造对齐 cc-switch model_fetch.rs——
// base 已含版本段 /v{N} 时不再补 /v1（否则 .../v1/v1/models → 404），
// Anthropic 兼容子路径剥离子路径后拼 /v1/models 与 /models 兜底。
func TestModelsURLCandidates(t *testing.T) {
	cases := []struct {
		base string
		want []string
	}{
		{"https://api.siliconflow.cn/v1", []string{"https://api.siliconflow.cn/v1/models"}},
		{"https://api.siliconflow.cn/v1/", []string{"https://api.siliconflow.cn/v1/models"}},
		{"https://api.deepseek.com", []string{"https://api.deepseek.com/v1/models"}},
		{"https://open.bigmodel.cn/api/coding/paas/v4", []string{
			"https://open.bigmodel.cn/api/coding/paas/v4/models",
			"https://open.bigmodel.cn/api/coding/paas/v4/v1/models",
		}},
		{"https://dashscope.aliyuncs.com/apps/anthropic", []string{
			"https://dashscope.aliyuncs.com/apps/anthropic/v1/models",
			"https://dashscope.aliyuncs.com/v1/models",
			"https://dashscope.aliyuncs.com/models",
		}},
		{"https://api.example.com/anthropic", []string{
			"https://api.example.com/anthropic/v1/models",
			"https://api.example.com/v1/models",
			"https://api.example.com/models",
		}},
	}
	for _, c := range cases {
		got := modelsURLCandidates(c.base)
		if len(got) != len(c.want) {
			t.Fatalf("modelsURLCandidates(%q) = %v, want %v", c.base, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("modelsURLCandidates(%q)[%d] = %q, want %q", c.base, i, got[i], c.want[i])
			}
		}
	}
}

// TestFetchModelsVersionSegmentNotDoubled：base 已以 /v1 结尾时拼出
// {base}/models（= /v1/models），绝不出现 /v1/v1/models。这是本次 404 的
// 根因——20 个 openai 预设里 13 个 base 以 /v1 结尾。
func TestFetchModelsVersionSegmentNotDoubled(t *testing.T) {
	var tried []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tried = append(tried, r.URL.Path)
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"m-1"},{"id":"m-2"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("", "seg", up.URL+"/v1", "openai", "sk-seg-12345", true, true)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := cs.FetchModels(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "m-1" {
		t.Fatalf("ids = %v", ids)
	}
	for _, p := range tried {
		if strings.Contains(p, "/v1/v1/") {
			t.Fatalf("拼出了重复版本段: %v", tried)
		}
	}
	if tried[0] != "/v1/models" {
		t.Fatalf("first tried = %v, want /v1/models", tried[0])
	}
}

// TestFetchModels404FallsBackToNextCandidate：Anthropic 兼容子路径命中
// （base 以 /anthropic 结尾）且该子路径下无模型端点时，自动回退到剥离
// 子路径后的候选。
func TestFetchModels404FallsBackToNextCandidate(t *testing.T) {
	var tried []string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tried = append(tried, r.URL.Path)
		if r.URL.Path == "/v1/models" {
			w.Write([]byte(`{"data":[{"id":"m-1"},{"id":"m-2"}]}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("", "compat", up.URL+"/anthropic", "anthropic", "sk-fb-12345", true, true)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := cs.FetchModels(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != "m-1" {
		t.Fatalf("ids = %v", ids)
	}
	if tried[len(tried)-1] != "/v1/models" {
		t.Fatalf("last tried = %v, want /v1/models", tried)
	}
}

// TestFetchModelsAllCandidates404：所有候选都 404 → 报错，且错误带最后
// 一次 404 信息，不返回空成功。
func TestFetchModelsAllCandidates404(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("", "all404", up.URL, "openai", "sk-all404", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cs.FetchModels(c.ID); err == nil {
		t.Fatal("全部候选 404 应报错")
	}
}

// TestFetchModelsAcceptsCatalogStyleResponse：智谱 /api/v1 形态
// （{"models":[{"slug":...}]}）也能解析。
func TestFetchModelsAcceptsCatalogStyleResponse(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"models":[{"slug":"glm-4"},{"slug":"glm-4-air"},{"id":"glm-4-plus"}]}`))
	}))
	defer up.Close()
	cs, _ := channelsSvc(t)
	c, err := cs.Create("", "glm", up.URL, "openai", "sk-glm-12345", true, true)
	if err != nil {
		t.Fatal(err)
	}
	ids, err := cs.FetchModels(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 || ids[0] != "glm-4" || ids[2] != "glm-4-plus" {
		t.Fatalf("ids = %v", ids)
	}
}

// TestPresetModelURLsNeverDoubleVersionSegment：把全部内置预设的每个协议
// base 都过一遍候选构造，断言绝不出现 .../v1/v1/...。这条锁死本次 404 的
// 根因（预设里 13/20 个 openai base 以 /v1 结尾，裸拼 /v1/models 必 404），
// 也防后续预置数据变更再踩同一个坑。
func TestPresetModelURLsNeverDoubleVersionSegment(t *testing.T) {
	for _, p := range presets.All() {
		for proto, ep := range p.Protocols {
			for _, c := range modelsURLCandidates(ep.BaseURL) {
				if strings.Contains(c, "/v1/v1/") {
					t.Errorf("preset %s %s base=%q 生成了重复版本段: %s",
						p.ID, proto, ep.BaseURL, c)
				}
			}
		}
	}
}
