package telemetry

import (
	"net/http"
	"strings"
	"testing"
)

func TestMaskToken(t *testing.T) {
	got := MaskToken("Bearer sk-ant-abcdefghijklmnop")
	if strings.Contains(got, "efghijklmnop") {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.HasPrefix(got, "Bearer sk-a") || !strings.HasSuffix(got, "***") {
		t.Fatalf("unexpected mask: %q", got)
	}
	if MaskToken("") != "" {
		t.Fatal("empty stays empty")
	}
}

func TestRedactHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret-token-value")
	h.Set("X-Api-Key", "sk-ant-topsecret")
	h.Set("Content-Type", "application/json")
	out := RedactHeaders(h)
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
