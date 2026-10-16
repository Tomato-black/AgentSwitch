package provider

import (
	"context"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.example.com/", http.StatusFound)
	}))
	defer srv.Close()
	a := NewAnthropicAdapter()
	_, err := a.Do(context.Background(), DoRequest{BaseURL: srv.URL, AllowPrivate: true, APIKey: "k", RawBody: []byte(`{}`)})
	if err == nil {
		t.Fatal("redirect must be denied")
	}
}

func TestProbeStages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"type":"authentication_error"}}`))
	}))
	defer srv.Close()
	res := Probe(context.Background(), srv.URL, "bad-key", true)
	if res.OK || res.Stage != "auth" {
		t.Fatalf("probe: %+v", res)
	}
	if strings.Contains(res.Error, "bad-key") {
		t.Fatalf("probe leaked key: %q", res.Error)
	}
}
