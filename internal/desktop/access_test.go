package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// TestAccessCardTitles 覆盖两卡标题字符串快照：「Claude Code」/「Codex CLI」，
// 且与 bridge 空片段 Title 一字不差（UI 卡头与片段标题同源对齐，spec §6.4）。
func TestAccessCardTitles(t *testing.T) {
	claude, codex := accessCardTitles()
	if claude != "Claude Code" || codex != "Codex CLI" {
		t.Fatalf("titles = %q %q, want Claude Code / Codex CLI", claude, codex)
	}
	if got := (bridge.Access{}).ClaudeCodeSnippet("").Title; got != claude {
		t.Fatalf("claude card title %q != bridge snippet title %q", claude, got)
	}
	if got := (bridge.Access{}).CodexSnippet("").Title; got != codex {
		t.Fatalf("codex card title %q != bridge snippet title %q", codex, got)
	}
}

// TestAccessDefaultModelSelection 覆盖接入页选中纯函数：无记忆键默认第一个
// enabled；记忆命中沿用；记忆失效（已删/已停用）回落第一个 enabled；
// 跳过 disabled；全无 enabled 返回空串。
func TestAccessDefaultModelSelection(t *testing.T) {
	models := []bridge.ModelView{
		{Name: "off-1", Enabled: false},
		{Name: "gpt-5", Enabled: true},
		{Name: "claude-x", Enabled: true},
	}
	if got := pickAccessModel(models, ""); got != "gpt-5" {
		t.Fatalf("no memory = %q, want gpt-5 (first enabled)", got)
	}
	if got := pickAccessModel(models, "claude-x"); got != "claude-x" {
		t.Fatalf("remembered = %q, want claude-x", got)
	}
	if got := pickAccessModel(models, "gone"); got != "gpt-5" {
		t.Fatalf("stale memory = %q, want gpt-5", got)
	}
	if got := pickAccessModel(models, "off-1"); got != "gpt-5" {
		t.Fatalf("disabled memory = %q, want gpt-5", got)
	}
	if got := pickAccessModel([]bridge.ModelView{{Name: "x", Enabled: false}}, ""); got != "" {
		t.Fatalf("no enabled = %q, want empty", got)
	}
}

// TestMaskSnippetLines 覆盖展示侧令牌掩码（展示与复制分离，spec §6.4/§9）：
// 展示行不得出现网关令牌明文。
func TestMaskSnippetLines(t *testing.T) {
	lines := []string{"ANTHROPIC_AUTH_TOKEN=tok123", "ANTHROPIC_MODEL=m"}
	got := maskSnippetLines(lines, "tok123")
	if got[0] != "ANTHROPIC_AUTH_TOKEN=***" || got[1] != "ANTHROPIC_MODEL=m" {
		t.Fatalf("masked = %v", got)
	}
	// 原切片不被改动（复制路径仍拿真实行）。
	if lines[0] != "ANTHROPIC_AUTH_TOKEN=tok123" {
		t.Fatal("mask must not mutate input")
	}
}

// TestAccessPageRenders 覆盖接入页替换占位：选择器标题与两卡标题在场，
// 无逻辑模型时给出引导 Hint；不再出现 TODO 占位。
func TestAccessPageRenders(t *testing.T) {
	u, _ := testUI(t)
	u.page = "access"
	tt := ui.NewTester(u.view, 900, 600)
	for _, want := range []string{"当前逻辑模型", "Claude Code", "Codex CLI", "请先创建并选择逻辑模型"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("TODO: 接入") {
		t.Fatalf("placeholder leaked: %v", tt.Texts())
	}
}
