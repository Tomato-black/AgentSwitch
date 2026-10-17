package bridge

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"AgentSwitch/internal/credential"
)

func channelsSvc(t *testing.T) (Channels, *credential.Manager) {
	t.Helper()
	a := tempApp(t)
	creds := &credential.Manager{
		Primary:       credential.NewFallbackFile(filepath.Join(t.TempDir(), "s.json")),
		AllowFallback: true,
	}
	return Channels{A: a, Creds: creds}, creds
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
