package telemetry

import (
	"net/http"
	"strings"
	"testing"
)

func TestMaskToken(t *testing.T) {
	got := maskToken("Bearer sk-ant-abcdefghijklmnop")
	if strings.Contains(got, "efghijklmnop") {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.HasPrefix(got, "Bearer sk-a") || !strings.HasSuffix(got, "***") {
		t.Fatalf("unexpected mask: %q", got)
	}
	if maskToken("") != "" {
		t.Fatal("empty stays empty")
	}
}

func TestMaskTokenNonSchemeValues(t *testing.T) {
	got := maskToken("session=topsecret; theme=dark")
	if strings.Contains(got, "topsecret") || strings.Contains(got, "theme") {
		t.Fatalf("cookie value leaked: %q", got)
	}
	if !strings.HasSuffix(got, "***") {
		t.Fatalf("missing mask: %q", got)
	}
	sc := maskToken("session=topsecret; Path=/; HttpOnly")
	if strings.Contains(sc, "topsecret") || !strings.HasSuffix(sc, "***") {
		t.Fatalf("set-cookie value leaked: %q", sc)
	}
}

func TestRedactHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret-token-value")
	h.Set("X-Api-Key", "sk-ant-topsecret")
	h.Set("Content-Type", "application/json")
	out := redactHeaders(h)
	if strings.Contains(out.Get("Authorization"), "secret-token-value") ||
		strings.Contains(out.Get("X-Api-Key"), "topsecret") {
		t.Fatalf("leak: %v", out)
	}
	if out.Get("Content-Type") != "application/json" {
		t.Fatal("non-sensitive header must pass through")
	}
	if h.Get("Authorization") != "Bearer secret-token-value" {
		t.Fatal("must not mutate the input")
	}
}
