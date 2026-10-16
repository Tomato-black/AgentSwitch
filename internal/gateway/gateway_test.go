package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	s := newGW(t, "http://127.0.0.1:1", nil)
	if resp := post(t, s, `{"model":"pub-1","messages":[]}`, "wrong"); resp.StatusCode != 401 {
		t.Fatalf("status %d", resp.StatusCode)
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
