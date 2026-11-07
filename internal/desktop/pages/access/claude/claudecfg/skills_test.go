package claudecfg

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/state"
)

func renderSkills(st *state.State) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		SkillsCard(c, st)
	}, 1000, 1200)
}

func TestSkillsCardRendersStatesLocksAndUnknown(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.Skills = []bridge.SkillView{
		{Name: "claude-api", Source: corecfg.SkillSourceBuiltIn, Effective: corecfg.SkillOn},
		{Name: "plug:deploy", Source: corecfg.SkillSourcePlugin, Effective: corecfg.SkillOn, LockedBy: "plugin"},
		{Name: "gone", Source: corecfg.SkillSourceUnknown, Effective: corecfg.SkillOff},
	}
	cf.SkillStates = map[string]string{"gone": "off"}
	cf.Doc = map[string]any{"skillOverrides": map[string]any{"gone": "off"}}
	cf.Baseline = cloneDoc(cf.Doc)

	tt := renderSkills(st)
	for _, want := range []string{"Skills", "claude-api", "由插件管理", "未知来源", "gone", "四态"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

func TestSetSkillStateWritesOverride(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.SkillStates = map[string]string{}

	setSkillState(cf, "claude-api", "off")
	m, ok := cf.Doc["skillOverrides"].(map[string]any)
	if !ok || m["claude-api"] != "off" {
		t.Fatalf("skillOverrides = %v", cf.Doc["skillOverrides"])
	}
	if cf.SkillStates["claude-api"] != "off" {
		t.Fatalf("skill state cache = %v", cf.SkillStates)
	}
	if !cf.Dirty {
		t.Fatal("skill edit must dirty document")
	}

	setSkillState(cf, "claude-api", "")
	if _, ok := cf.Doc["skillOverrides"]; ok {
		t.Fatalf("empty skillOverrides must be removed: %v", cf.Doc)
	}
	if _, ok := cf.SkillStates["claude-api"]; ok {
		t.Fatalf("skill state cache must drop entry: %v", cf.SkillStates)
	}
	if cf.Dirty {
		t.Fatal("deleting back to baseline must clear dirty")
	}
}

func TestSetSkillStateKeepsUnknownOverride(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.SkillStates = map[string]string{"gone": "off"}
	cf.Doc = map[string]any{"skillOverrides": map[string]any{"gone": "off", "claude-api": "name-only"}}

	setSkillState(cf, "gone", "")
	m, _ := cf.Doc["skillOverrides"].(map[string]any)
	if len(m) != 1 || m["claude-api"] != "name-only" {
		t.Fatalf("unrelated overrides must be preserved: %v", m)
	}
}
