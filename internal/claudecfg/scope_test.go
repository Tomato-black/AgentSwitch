package claudecfg

import "testing"

func findViolation(vs []Violation, path, reason string) *Violation {
	for i := range vs {
		if vs[i].Path == path && vs[i].Reason == reason {
			return &vs[i]
		}
	}
	return nil
}

func TestValidateProjectSharedForbidden(t *testing.T) {
	doc := map[string]any{"skipDangerousModePermissionPrompt": false}
	vs := Validate(doc, ScopeProject, map[string]any{}, ManagedSet{})
	v := findViolation(vs, "skipDangerousModePermissionPrompt", "not-in-project-shared")
	if v == nil {
		t.Fatalf("violations = %#v", vs)
	}
	if v.Severity != "error" || !v.Changed || !v.Present {
		t.Fatalf("violation = %#v", v)
	}
}

func TestValidateLocalAllowsSkipDangerous(t *testing.T) {
	doc := map[string]any{"skipDangerousModePermissionPrompt": false}
	vs := Validate(doc, ScopeLocal, map[string]any{}, ManagedSet{})
	if len(vs) != 0 {
		t.Fatalf("violations = %#v", vs)
	}
}

func TestValidateUserOnlyAtProject(t *testing.T) {
	doc := map[string]any{"autoMode": map[string]any{}}
	vs := Validate(doc, ScopeProject, map[string]any{}, ManagedSet{})
	if findViolation(vs, "autoMode", "user-only") == nil {
		t.Fatalf("violations = %#v", vs)
	}
}

func TestValidateProjectDefaultModeWarns(t *testing.T) {
	doc := map[string]any{"permissions": map[string]any{"defaultMode": "auto"}}
	vs := Validate(doc, ScopeProject, map[string]any{}, ManagedSet{})
	v := findViolation(vs, "permissions.defaultMode", "ineffective-at-project")
	if v == nil || v.Severity != "warn" {
		t.Fatalf("violations = %#v", vs)
	}
}

func TestValidateManagedKeys(t *testing.T) {
	managed := ManagedSet{
		StrongEnv:      []string{"ANTHROPIC_AUTH_TOKEN"},
		StrongTopLevel: []string{"permissions"},
		WeakEnv:        []string{"env.HTTPS_PROXY"},
	}
	doc := map[string]any{
		"env":         map[string]any{"ANTHROPIC_AUTH_TOKEN": "new", "HTTPS_PROXY": "new"},
		"permissions": map[string]any{"allow": []any{"Bash"}},
	}
	base := map[string]any{
		"env":         map[string]any{"ANTHROPIC_AUTH_TOKEN": "old", "HTTPS_PROXY": "old"},
		"permissions": map[string]any{"allow": []any{}},
	}
	vs := Validate(doc, ScopeUser, base, managed)
	if findViolation(vs, "env.ANTHROPIC_AUTH_TOKEN", "managed") == nil {
		t.Fatalf("violations = %#v", vs)
	}
	if findViolation(vs, "permissions.allow", "managed") == nil {
		t.Fatalf("violations = %#v", vs)
	}
	if v := findViolation(vs, "env.HTTPS_PROXY", "weak-managed"); v == nil || v.Severity != "warn" {
		t.Fatalf("violations = %#v", vs)
	}
}

func TestValidateSkillOverrides(t *testing.T) {
	doc := map[string]any{"skillOverrides": map[string]any{
		"ok":        "off",
		"bad":       "maybe",
		"__proto__": "on",
	}}
	vs := Validate(doc, ScopeUser, map[string]any{}, ManagedSet{})
	if findViolation(vs, "skillOverrides.bad", "invalid-value") == nil {
		t.Fatalf("violations = %#v", vs)
	}
	if findViolation(vs, "skillOverrides.__proto__", "reserved-name") == nil {
		t.Fatalf("violations = %#v", vs)
	}
}

func TestValidateHistoryPreservedIsUnchanged(t *testing.T) {
	doc := map[string]any{"autoMode": map[string]any{"enabled": true}}
	base := map[string]any{"autoMode": map[string]any{"enabled": true}}
	vs := Validate(doc, ScopeProject, base, ManagedSet{})
	v := findViolation(vs, "autoMode", "user-only")
	if v == nil || v.Changed {
		t.Fatalf("violations = %#v", vs)
	}
}
