package claudecfg

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/state"
)

func renderMerge(st *state.State) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		mergedView(c, c.Theme(), st)
	}, 1400, 1200)
}

func mergeTestState(t *testing.T, entries map[string]corecfg.MergedEntry) *state.State {
	t.Helper()
	st := formTestState(t)
	st.ClaudeCfg.MergeEntries = entries
	return st
}

func TestMergeKindLabels(t *testing.T) {
	cases := map[corecfg.MergeKind]string{
		corecfg.MergeOverride: "覆盖",
		corecfg.MergeUnion:    "并集",
		corecfg.MergeEnv:      "逐变量",
		corecfg.MergeHooks:    "累加",
		corecfg.MergePlugins:  "逐键",
		corecfg.MergeSkill:    "逐键",
		corecfg.MergeReplace:  "整值替换",
		corecfg.MergeOr:       "任一层 true",
		corecfg.MergeLeaf:     "逐键",
	}
	for kind, want := range cases {
		if got := mergeKindLabel(kind); got != want {
			t.Fatalf("mergeKindLabel(%s) = %q, want %q", kind, got, want)
		}
	}
}

func TestMergedViewShowsSourcesKindsAndMaskedValues(t *testing.T) {
	entries := map[string]corecfg.MergedEntry{
		"model": {
			Path: "model", Value: "opus", Kind: corecfg.MergeOverride, Source: corecfg.ScopeUser,
			Contributions: []corecfg.Contribution{
				{Layer: corecfg.ScopeUser, File: "/home/u/.claude/settings.json", Value: "opus", Effective: true},
			},
		},
		"permissions.allow": {
			Path: "permissions.allow", Value: []any{"Read"}, Kind: corecfg.MergeUnion, Source: corecfg.ScopeProject,
			Contributions: []corecfg.Contribution{
				{Layer: corecfg.ScopeUser, File: "/home/u/.claude/settings.json", Value: []any{"Read"}, Effective: true},
			},
		},
		"skipDangerousModePermissionPrompt": {
			Path: "skipDangerousModePermissionPrompt", Value: true, Kind: corecfg.MergeOr, Source: corecfg.ScopeUser,
		},
		"skillOverrides.deploy": {
			Path: "skillOverrides.deploy", Value: "off", Kind: corecfg.MergeSkill, Source: corecfg.ScopeUser,
		},
		"env.ANTHROPIC_AUTH_TOKEN": {
			Path: "env.ANTHROPIC_AUTH_TOKEN", Value: "secret-token-value", Kind: corecfg.MergeEnv, Source: corecfg.ScopeUser,
		},
	}
	st := mergeTestState(t, entries)
	st.ClaudeCfg.Skills = []bridge.SkillView{
		{Name: "deploy", Source: corecfg.SkillSourcePlugin, Effective: corecfg.SkillOn, LockedBy: "plugin"},
	}

	tt := renderMerge(st)
	for _, want := range []string{
		"合并预览", "model", "来源：用户级", "覆盖", "并集", "任一层 true", "逐键",
		"skillOverrides.deploy", "env.ANTHROPIC_AUTH_TOKEN",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if !tt.HasText("••") {
		t.Fatalf("sensitive value must be masked: %v", tt.Texts())
	}
	if tt.HasText("secret-token-value") {
		t.Fatal("sensitive value must not render in clear text")
	}
}

func TestPermissionCardOrderAndIneffectiveProjectMode(t *testing.T) {
	entries := map[string]corecfg.MergedEntry{
		"permissions.deny": {
			Path: "permissions.deny", Value: []any{"Bash(rm:*)"}, Kind: corecfg.MergeUnion, Source: corecfg.ScopeUser,
		},
		"permissions.ask": {
			Path: "permissions.ask", Value: []any{"Write"}, Kind: corecfg.MergeUnion, Source: corecfg.ScopeUser,
		},
		"permissions.allow": {
			Path: "permissions.allow", Value: []any{"Read"}, Kind: corecfg.MergeUnion, Source: corecfg.ScopeUser,
		},
		"permissions.defaultMode": {
			Path: "permissions.defaultMode", Value: "default", Kind: corecfg.MergeOverride, Source: corecfg.ScopeUser,
			Contributions: []corecfg.Contribution{
				{Layer: corecfg.ScopeProject, File: "/p/.claude/settings.json", Value: "auto", Effective: false},
				{Layer: corecfg.ScopeUser, File: "/home/u/.claude/settings.json", Value: "default", Effective: true},
			},
		},
	}
	st := mergeTestState(t, entries)
	tt := renderMerge(st)

	texts := tt.Texts()
	index := func(s string) int {
		for i, text := range texts {
			if text == s {
				return i
			}
		}
		return -1
	}
	deny, ask, allow := index("1 拒绝"), index("2 询问"), index("3 允许")
	if deny < 0 || ask < 0 || allow < 0 {
		t.Fatalf("permission segments missing: %v", texts)
	}
	if !(deny < ask && ask < allow) {
		t.Fatalf("permission order must be deny → ask → allow: %d %d %d", deny, ask, allow)
	}
	if !tt.HasText("项目级") || !tt.HasText("不生效") {
		t.Fatalf("project-level auto/bypass must be marked ineffective: %v", texts)
	}
}

func TestMergedContributionsExpandable(t *testing.T) {
	entries := map[string]corecfg.MergedEntry{
		"model": {
			Path: "model", Value: "opus", Kind: corecfg.MergeOverride, Source: corecfg.ScopeUser,
			Contributions: []corecfg.Contribution{
				{Layer: corecfg.ScopeUser, File: "/home/u/.claude/settings.json", Value: "opus", Effective: true},
			},
		},
	}
	st := mergeTestState(t, entries)
	if tt := renderMerge(st); tt.HasText("/home/u/.claude/settings.json") {
		t.Fatalf("collapsed row must hide contributions: %v", tt.Texts())
	}

	st.ClaudeCfg.MergeOpen = map[string]bool{"model": true}
	tt := renderMerge(st)
	if !tt.HasText("/home/u/.claude/settings.json") {
		t.Fatalf("expanded row must show contribution file: %v", tt.Texts())
	}
	if !tt.HasText("生效") {
		t.Fatalf("expanded row must mark effective contribution: %v", tt.Texts())
	}
}

func TestMergedSkillsPluginRowsMarkedLocked(t *testing.T) {
	st := mergeTestState(t, map[string]corecfg.MergedEntry{})
	st.ClaudeCfg.Skills = []bridge.SkillView{
		{Name: "deploy", Source: corecfg.SkillSourcePlugin, Effective: corecfg.SkillOn, LockedBy: "plugin"},
	}
	tt := renderMerge(st)
	if !tt.HasText("插件锁定") {
		t.Fatalf("plugin skill must be marked locked: %v", tt.Texts())
	}
	if !strings.Contains(strings.Join(tt.Texts(), " "), "deploy") {
		t.Fatalf("skill row missing: %v", tt.Texts())
	}
}

// TestPermissionEvalThreeSegmentCard（2026-11-07 原型对齐）：权限求值卡为
// 三段式——「1 拒绝」→「2 询问」→「3 允许」，每段显示条数与固定说明。
func TestPermissionEvalThreeSegmentCard(t *testing.T) {
	entries := map[string]corecfg.MergedEntry{
		"permissions.deny": {
			Path: "permissions.deny", Value: []any{"Bash(rm:*)"}, Kind: corecfg.MergeUnion, Source: corecfg.ScopeUser,
		},
		"permissions.ask": {
			Path: "permissions.ask", Value: []any{"Write"}, Kind: corecfg.MergeUnion, Source: corecfg.ScopeUser,
		},
		"permissions.allow": {
			Path: "permissions.allow", Value: []any{"Read"}, Kind: corecfg.MergeUnion, Source: corecfg.ScopeUser,
		},
	}
	st := mergeTestState(t, entries)
	tt := renderMerge(st)
	texts := tt.Texts()
	index := func(s string) int {
		for i, text := range texts {
			if text == s {
				return i
			}
		}
		return -1
	}
	s1, s2, s3 := index("1 拒绝"), index("2 询问"), index("3 允许")
	if s1 < 0 || s2 < 0 || s3 < 0 {
		t.Fatalf("three-segment labels missing: %v", texts)
	}
	if !(s1 < s2 && s2 < s3) {
		t.Fatalf("segment order must be 1→2→3: %d %d %d", s1, s2, s3)
	}
	if !tt.HasText("1 条") {
		t.Fatalf("segment count missing: %v", texts)
	}
	if !tt.HasText("deny 任意层压过 allow 任意层") {
		t.Fatalf("fixed note missing: %v", texts)
	}
}

// TestMergedViewQueryFilterPinned（2026-11-07 组件升级钉住）：键路径筛选框换
// ui.SearchField 后过滤语义不变——只显示含查询串的键行。
func TestMergedViewQueryFilterPinned(t *testing.T) {
	entries := map[string]corecfg.MergedEntry{
		"model":       {Path: "model", Value: "sonnet", Kind: corecfg.MergeOverride, Source: corecfg.ScopeUser},
		"permissions": {Path: "permissions", Value: map[string]any{}, Kind: corecfg.MergeOverride, Source: corecfg.ScopeUser},
	}
	st := mergeTestState(t, entries)
	st.ClaudeCfg.MergeQuery = "model"
	tt := renderMerge(st)
	if !tt.HasText("model") {
		t.Fatalf("命中键行缺失: %v", tt.Texts())
	}
	if tt.HasText("permissions") {
		t.Fatalf("未命中键行应被过滤: %v", tt.Texts())
	}
}
