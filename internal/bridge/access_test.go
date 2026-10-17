package bridge

import (
	"os"
	"strings"
	"testing"
)

// joinLines 把片段行拼成可 Contains 断言的大字符串。
func joinLines(lines []string) string { return strings.Join(lines, "\n") }

// TestClaudeSnippetContainsModelEnv 覆盖简报点名用例：Claude Code 片段四行
// 齐全（BASE_URL/AUTH_TOKEN/MODEL/SMALL_FAST_MODEL），模型两行=入参逻辑模型。
// 另附两条安全面断言：token 完整展示（供用户复制）、绝不写入日志。
func TestClaudeSnippetContainsModelEnv(t *testing.T) {
	a := tempApp(t)
	st := Gateway{A: a}.Status()
	sn := Access{A: a}.ClaudeCodeSnippet("my-logical-model")

	body := joinLines(sn.Lines)
	for _, want := range []string{
		"ANTHROPIC_BASE_URL=http://127.0.0.1:",
		"ANTHROPIC_AUTH_TOKEN=" + st.Token,
		"ANTHROPIC_MODEL=my-logical-model",
		"ANTHROPIC_SMALL_FAST_MODEL=my-logical-model",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("snippet missing %q in:\n%s", want, body)
		}
	}
	if len(sn.Lines) != 4 {
		t.Errorf("lines=%d, want 4: %q", len(sn.Lines), sn.Lines)
	}
	if sn.Hint == "" {
		t.Error("empty hint")
	}
	// token 完整展示且不进日志（安全默认 §8）
	if !strings.Contains(body, st.Token) {
		t.Error("token not fully shown in snippet")
	}
	data, err := os.ReadFile(a.LogPath())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), st.Token) {
		t.Error("token leaked into log")
	}
}

// TestCodexSnippetContainsModelAndToken 覆盖简报点名用例：Codex 片段含
// OPENAI_BASE_URL（带 /v1）/OPENAI_API_KEY（=gateway token）与指向逻辑模型的
// 模型键（Codex 官方 config.toml 等价写法），Hint 说明粘贴位置。
func TestCodexSnippetContainsModelAndToken(t *testing.T) {
	a := tempApp(t)
	st := Gateway{A: a}.Status()
	sn := Access{A: a}.CodexSnippet("my-logical-model")

	body := joinLines(sn.Lines)
	for _, want := range []string{
		"OPENAI_BASE_URL=http://127.0.0.1:",
		"/v1",
		"OPENAI_API_KEY=" + st.Token,
		"my-logical-model",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("snippet missing %q in:\n%s", want, body)
		}
	}
	// 模型键须指向入参（config.toml 的 model = "..." 或等价 env 行）
	if !strings.Contains(body, "my-logical-model") {
		t.Error("model not pointed at logical model")
	}
	if sn.Hint == "" {
		t.Error("empty hint")
	}
	if !strings.Contains(sn.Hint, "config.toml") && !strings.Contains(sn.Hint, "env") {
		t.Errorf("hint should say where to paste: %q", sn.Hint)
	}
}

// TestSnippetEmptyModelHint 覆盖简报点名用例：logicalModel 为空时返回空 Lines
// 与「请先创建并选择逻辑模型」提示，不 panic。
func TestSnippetEmptyModelHint(t *testing.T) {
	a := tempApp(t)
	svc := Access{A: a}
	for _, sn := range []AccessSnippet{svc.ClaudeCodeSnippet(""), svc.CodexSnippet("")} {
		if len(sn.Lines) != 0 {
			t.Errorf("lines=%q, want empty", sn.Lines)
		}
		if !strings.Contains(sn.Hint, "请先创建并选择逻辑模型") {
			t.Errorf("hint=%q, want 创建并选择逻辑模型提示", sn.Hint)
		}
	}
}
