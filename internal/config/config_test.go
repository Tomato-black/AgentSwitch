package config

import (
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	s := defaultSettings()
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
	s := defaultSettings()
	s.Port = 0
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("want port error, got %v", err)
	}
}

func TestLoadUsesDefaultsAndOverrides(t *testing.T) {
	vals := map[string]string{KeyPort: "18000", KeyToken: "tok"}
	get := func(k string) (string, error) { return vals[k], nil }
	s, err := Load(get)
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 18000 || s.GatewayToken != "tok" || s.RequestTimeoutSeconds != 120 {
		t.Fatalf("load wrong: %+v", s)
	}
}

// TestPassthroughDefaultFalse：routing.passthrough_unregistered 缺省 false
// （defaultSettings() 同），Set "1" → true。
func TestPassthroughDefaultFalse(t *testing.T) {
	if defaultSettings().PassthroughUnregistered {
		t.Fatal("defaultSettings() must be false")
	}
	empty := func(string) (string, error) { return "", nil }
	s, err := Load(empty)
	if err != nil {
		t.Fatal(err)
	}
	if s.PassthroughUnregistered {
		t.Fatal("Load(空) must be false")
	}
	vals := map[string]string{KeyPassthrough: "1"}
	s2, err := Load(func(k string) (string, error) { return vals[k], nil })
	if err != nil {
		t.Fatal(err)
	}
	if !s2.PassthroughUnregistered {
		t.Fatal(`Set "1" must load true`)
	}
}
