package claudecfg

import (
	"reflect"
	"testing"
)

func TestAccessManagedEnvIsThirteen(t *testing.T) {
	if len(AccessManagedEnv) != 13 {
		t.Fatalf("len = %d, want 13: %v", len(AccessManagedEnv), AccessManagedEnv)
	}
	want := []string{
		"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL",
		"ANTHROPIC_SMALL_FAST_MODEL",
		"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME",
		"ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL_NAME",
		"ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL_NAME",
		"ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME",
		"CLAUDE_CODE_SUBAGENT_MODEL",
	}
	if !reflect.DeepEqual(AccessManagedEnv, want) {
		t.Fatalf("AccessManagedEnv = %v", AccessManagedEnv)
	}
}

func TestSkipDangerousModeKeySpec(t *testing.T) {
	spec, ok := Lookup("skipDangerousModePermissionPrompt")
	if !ok {
		t.Fatal("key not found")
	}
	if spec.Merge != MergeOr {
		t.Fatalf("merge = %q", spec.Merge)
	}
	if spec.Default != true {
		t.Fatalf("default = %v", spec.Default)
	}
	if spec.Scopes.Has(ScopeProject) {
		t.Fatal("project shared must not be allowed")
	}
	if !spec.Scopes.Has(ScopeUser) || !spec.Scopes.Has(ScopeLocal) || !spec.Scopes.Has(ScopeManaged) {
		t.Fatalf("scopes = %v", spec.Scopes)
	}
}

func TestSkillOverridesKeySpec(t *testing.T) {
	spec, ok := Lookup("skillOverrides")
	if !ok {
		t.Fatal("key not found")
	}
	if spec.Merge != MergeSkill {
		t.Fatalf("merge = %q", spec.Merge)
	}
	if spec.Kind != KindObject {
		t.Fatalf("kind = %q", spec.Kind)
	}
	for _, scope := range []ScopeKind{ScopeUser, ScopeProject, ScopeLocal, ScopeManaged} {
		if !spec.Scopes.Has(scope) {
			t.Fatalf("scope %s not allowed", scope)
		}
	}
}

func TestScopeLists(t *testing.T) {
	for _, key := range []string{"skipDangerousModePermissionPrompt", "syncClaudeAiPlugins", "syncClaudeAiSkills", "useAutoModeDuringPlan"} {
		found := false
		for _, v := range NoProjectShared {
			if v == key {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s missing from NoProjectShared", key)
		}
	}
	for _, key := range []string{"autoMode", "modelPicker"} {
		found := false
		for _, v := range UserOnly {
			if v == key {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s missing from UserOnly", key)
		}
	}
}

func TestUnknownKey(t *testing.T) {
	if _, ok := Lookup("unknown.key"); ok {
		t.Fatal("unknown key should not resolve")
	}
}
