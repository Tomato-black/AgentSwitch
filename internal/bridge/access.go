package bridge

import (
	"fmt"

	"AgentSwitch/internal/app"
)

// Access 是接入片段桥接（Task 10）：为 Claude Code / Codex CLI 生成指向本地
// 网关的接入配置片段（logicalModel 必选一个 enabled 逻辑模型，由 UI 传入）。
// 片段含 gateway token 完整值供用户复制——绝不写入日志。
type Access struct{ A *app.App }

// AccessSnippet 是可复制的接入片段：Lines 为配置行，Hint 说明粘贴位置。
type AccessSnippet struct {
	Title string   `json:"title"`
	Lines []string `json:"lines"`
	Hint  string   `json:"hint"`
}

// status 取网关状态——端口与 token 同源（Gateway.Status）。
func (s Access) status() Status { return Gateway{A: s.A}.Status() }

// emptySnippet 返回 logicalModel 为空时的空 Lines 片段（不 panic）。
func emptySnippet(title string) AccessSnippet {
	return AccessSnippet{Title: title, Hint: "请先创建并选择逻辑模型"}
}

// ClaudeCodeSnippet 生成 Claude Code 接入片段：env 四行均指向逻辑模型。
// Hint 说明写入 shell 环境或 ~/.claude/settings.json 的 env。
func (s Access) ClaudeCodeSnippet(logicalModel string) AccessSnippet {
	if logicalModel == "" {
		return emptySnippet("Claude Code")
	}
	st := s.status()
	base := fmt.Sprintf("http://127.0.0.1:%d", st.Port)
	return AccessSnippet{
		Title: "Claude Code",
		Lines: []string{
			"ANTHROPIC_BASE_URL=" + base,
			"ANTHROPIC_AUTH_TOKEN=" + st.Token,
			"ANTHROPIC_MODEL=" + logicalModel,
			"ANTHROPIC_SMALL_FAST_MODEL=" + logicalModel,
		},
		Hint: "写入 shell 环境变量，或 ~/.claude/settings.json 的 env 字段",
	}
}

// CodexSnippet 生成 Codex CLI 接入片段：OPENAI_BASE_URL（含 /v1）与
// OPENAI_API_KEY 走环境变量，模型键用 Codex 官方 config.toml 等价写法
// model = "<logicalModel>"。Hint 说明各自粘贴位置。
func (s Access) CodexSnippet(logicalModel string) AccessSnippet {
	if logicalModel == "" {
		return emptySnippet("Codex CLI")
	}
	st := s.status()
	base := fmt.Sprintf("http://127.0.0.1:%d/v1", st.Port)
	return AccessSnippet{
		Title: "Codex CLI",
		Lines: []string{
			"OPENAI_BASE_URL=" + base,
			"OPENAI_API_KEY=" + st.Token,
			`model = "` + logicalModel + `"`,
		},
		Hint: "前两行写入 shell 环境变量；model 行写入 ~/.codex/config.toml",
	}
}
