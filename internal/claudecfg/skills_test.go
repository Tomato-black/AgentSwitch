package claudecfg

import "testing"

func skillLayer(kind ScopeKind, name, state string) Layer {
	return Layer{Kind: kind, Doc: map[string]any{
		"skillOverrides": map[string]any{name: state},
	}}
}

func TestResolveSkillDefaultOn(t *testing.T) {
	state, locked := ResolveSkill(Skill{Name: "commit", Source: SkillSourceUser}, nil, nil)
	if state != SkillOn || locked != "" {
		t.Fatalf("state=%q locked=%q", state, locked)
	}
}

func TestResolveSkillUserOff(t *testing.T) {
	layers := []Layer{skillLayer(ScopeUser, "commit", "off")}
	state, locked := ResolveSkill(Skill{Name: "commit", Source: SkillSourceUser}, layers, nil)
	if state != SkillOff || locked != "" {
		t.Fatalf("state=%q locked=%q", state, locked)
	}
}

func TestResolveSkillLocalOverridesProject(t *testing.T) {
	layers := []Layer{
		skillLayer(ScopeUser, "commit", "on"),
		skillLayer(ScopeProject, "commit", "on"),
		skillLayer(ScopeLocal, "commit", "off"),
	}
	state, _ := ResolveSkill(Skill{Name: "commit", Source: SkillSourceUser}, layers, nil)
	if state != SkillOff {
		t.Fatalf("state=%q", state)
	}
}

func TestResolveSkillPluginDisabled(t *testing.T) {
	skill := Skill{Name: "deploy", Source: SkillSourcePlugin, PluginID: "ops@market"}
	state, locked := ResolveSkill(skill, nil, map[string]bool{"ops@market": false})
	if state != SkillOff || locked != "plugin" {
		t.Fatalf("state=%q locked=%q", state, locked)
	}
}

func TestResolveSkillPluginOverrideDoesNotApply(t *testing.T) {
	skill := Skill{Name: "deploy", Source: SkillSourcePlugin, PluginID: "ops@market"}
	layers := []Layer{skillLayer(ScopeUser, "deploy", "off")}
	state, locked := ResolveSkill(skill, layers, map[string]bool{"ops@market": true})
	if state != SkillOn || locked != "plugin" {
		t.Fatalf("state=%q locked=%q", state, locked)
	}
}

func TestResolveSkillAuthorLockCapsState(t *testing.T) {
	skill := Skill{Name: "review", Source: SkillSourceUser, LockedBy: "author"}
	layers := []Layer{skillLayer(ScopeUser, "review", "on")}
	state, locked := ResolveSkill(skill, layers, nil)
	if state != SkillUserInvocableOnly || locked != "author" {
		t.Fatalf("state=%q locked=%q", state, locked)
	}
}

func TestResolveSkillManagedLock(t *testing.T) {
	layers := []Layer{skillLayer(ScopeManaged, "commit", "off")}
	state, locked := ResolveSkill(Skill{Name: "commit", Source: SkillSourceUser}, layers, nil)
	if state != SkillOff || locked != "managed" {
		t.Fatalf("state=%q locked=%q", state, locked)
	}
}

func TestResolveSkillReservedName(t *testing.T) {
	state, locked := ResolveSkill(Skill{Name: "__proto__", Source: SkillSourceUser}, nil, nil)
	if locked != "reserved-name" {
		t.Fatalf("state=%q locked=%q", state, locked)
	}
}

func TestCollectSkillOverridesPreservesUnknown(t *testing.T) {
	layers := []Layer{
		skillLayer(ScopeUser, "known", "on"),
		skillLayer(ScopeLocal, "removed-long-ago", "name-only"),
	}
	got := CollectSkillOverrides(layers)
	if got["removed-long-ago"] != SkillNameOnly {
		t.Fatalf("got=%#v", got)
	}
}
