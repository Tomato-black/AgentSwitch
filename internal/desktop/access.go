// access.go 是接入页（spec §6.4 / Task 15）：顶部「当前逻辑模型」选择器
// （数据来自 Logical.List() 的 enabled 项，记忆 settings 键
// access.last_logical_model）+ Claude Code / Codex 两卡（片段代码块 +
// 复制按钮）。片段来自 bridge.Access；展示令牌掩码、复制写真实片段
// （展示与复制分离，spec §6.4/§9）。片段样式 snippetBg 定义于本文件。
package desktop

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// accessModelKey 是接入页记忆上次选择的 settings 键（简报 Task 15）。
const accessModelKey = "access.last_logical_model"

// snippetBg 是接入卡片段代码块底色（spec §5.3）：浅色 #FAFAFA、深色 #232327。
func snippetBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#232327")
	}
	return ui.Hex("#FAFAFA")
}

// accessCardTitles 返回两卡标题（字符串快照锚点）：与 bridge.AccessSnippet
// 的 Title 一字不差（TestAccessCardTitles 对拍空片段标题）。
func accessCardTitles() (claude, codex string) { return "Claude Code", "Codex CLI" }

// pickAccessModel 解析接入页选中的逻辑模型名：lastSaved 仍命中某 enabled
// 项则沿用，否则取第一个 enabled；无 enabled 返回 ""。选择器只展示 enabled
// 项，故 disabled 即便名字命中记忆也回落。
func pickAccessModel(models []bridge.ModelView, lastSaved string) string {
	first := ""
	for _, m := range models {
		if !m.Enabled {
			continue
		}
		if m.Name == lastSaved {
			return m.Name
		}
		if first == "" {
			first = m.Name
		}
	}
	return first
}

// maskSnippetLines 返回展示用行：令牌子串一律掩成 ***（不改原切片——
// 复制路径仍拿真实行，展示与复制分离）。
func maskSnippetLines(lines []string, token string) []string {
	if token == "" {
		return lines
	}
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.ReplaceAll(l, token, "***")
	}
	return out
}

// accessPage 是接入页（shell.go pageView 分发；签名即接线契约，不得改名）。
// 每帧取一次片段（端口/令牌同源 Gateway.Status），选择器变更即记忆落库。
func (u *uiState) accessPage(c *ui.Context) {
	t := c.Theme()
	s := softs(t)
	gs := u.svc.Gateway.Status()

	var models []bridge.ModelView
	if u.svc.Logical != nil {
		models = u.svc.Logical.List()
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		if m.Enabled {
			names = append(names, m.Name)
		}
	}

	// 记忆上次选择（settings 键）；当前值失效/缺省时回落 pickAccessModel。
	store := u.svc.Gateway.A.Store().Settings()
	last, _ := store.Get(accessModelKey)
	valid := false
	for _, n := range names {
		if n == u.accessModel {
			valid = true
			break
		}
	}
	if !valid {
		u.accessModel = pickAccessModel(models, last)
	}
	if u.accessModel != "" && u.accessModel != last {
		_ = store.Set(accessModelKey, u.accessModel)
	}

	acc := bridge.Access{A: u.svc.Gateway.A}
	claudeTitle, codexTitle := accessCardTitles()
	ui.Column(c).Gap(12).Children(func() {
		card(c, t, func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, "当前逻辑模型").Bold()
				if len(names) == 0 {
					ui.Text(c, "还没有启用的逻辑模型——先在「逻辑模型」页创建并启用").TextColor(t.TextMuted)
				} else {
					ui.Select(c, &u.accessModel, names)
				}
			})
		})
		u.accessSnippetCard(c, t, s, claudeTitle, acc.ClaudeCodeSnippet(u.accessModel), gs.Token)
		u.accessSnippetCard(c, t, s, codexTitle, acc.CodexSnippet(u.accessModel), gs.Token)
	})
}

// accessSnippetCard 渲染一块接入卡：标题 + 片段代码块（snippetBg 等宽小
// 一号）+ 粘贴位置 Hint + 复制按钮（transfer 写真实片段到剪贴板）。
// 无片段（未选逻辑模型）时只显 Hint，不出空代码块。
func (u *uiState) accessSnippetCard(c *ui.Context, t *ui.Theme, s softPalette, title string, sn bridge.AccessSnippet, token string) {
	card(c, t, func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Text(c, title).Bold()
			if len(sn.Lines) == 0 {
				ui.Text(c, sn.Hint).TextColor(t.TextMuted)
				return
			}
			ui.Box(c).Radius(9).Background(snippetBg(t)).Padding(10, 12).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					for _, line := range maskSnippetLines(sn.Lines, token) {
						ui.Text(c, line).Font(monoFont).FontSize(t.FontSize - 1.5).SingleLine()
					}
				})
			})
			ui.Text(c, sn.Hint).TextColor(t.TextMuted)
			if softButton(c, t, s, "gray", "复制").Clicked() {
				mygo.Clipboard.Write(transfer.TextData(strings.Join(sn.Lines, "\n")))
			}
		})
	})
}
