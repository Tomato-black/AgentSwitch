package config

import (
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	s := Default()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Port != 17890 || s.RequestTimeoutSeconds != 120 || s.ShutdownGraceSeconds != 15 {
		t.Fatalf("defaults drifted: %+v", s)
	}
	if s.MaxBodyBytes != 10<<20 || s.MaxConcurrent != 32 {
		t.Fatalf("limits drifted: %+v", s)
	}
}

func TestValidateRejectsBadPort(t *testing.T) {
	s := Default()
	s.Port = 0
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("want port error, got %v", err)
	}
}

func TestLoadUsesDefaultsAndOverrides(t *testing.T) {
	vals := map[string]string{KeyPort: "18000", KeyCredFallback: "true"}
	get := func(k string) (string, error) { return vals[k], nil }
	s, err := Load(get)
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 18000 || !s.CredentialFallback || s.RequestTimeoutSeconds != 120 {
		t.Fatalf("load wrong: %+v", s)
	}
}
