package claudecfg

import "testing"

func TestIsSensitivePath(t *testing.T) {
	for _, p := range []string{
		"env.ANTHROPIC_AUTH_TOKEN",
		"env.API_KEY",
		"env.CLIENT_SECRET",
		"env.PASSWORD",
		"env.AUTHORIZATION",
	} {
		if !IsSensitivePath(p) {
			t.Fatalf("%s should be sensitive", p)
		}
	}
	if IsSensitivePath("env.FOO") {
		t.Fatal("FOO should not be sensitive")
	}
}

func TestMaskSecret(t *testing.T) {
	if got := MaskSecret("sk-1234567890"); got == "sk-1234567890" || got == "" {
		t.Fatalf("mask = %q", got)
	}
}

func TestContainsSecretLike(t *testing.T) {
	doc := map[string]any{"env": map[string]any{"FOO": "sk-abcdefghijklmnopqrstuvwxyz"}}
	if !ContainsSecretLike(doc) {
		t.Fatal("expected high-entropy secret")
	}
	if ContainsSecretLike(map[string]any{"env": map[string]any{"FOO": "bar"}}) {
		t.Fatal("bar should not look like a secret")
	}
}
