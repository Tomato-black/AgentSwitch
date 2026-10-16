package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

// defaultSnippetPort 是配置片段端口的兜底（config.Default().Port）；正常
// 路径取 bridge.Status.Port（Gateway.Status 从 config.Load 填充）。
const defaultSnippetPort = 17890

// overviewView 是概览页（面板头形态，spec §5.1）：LED+状态词+等宽地址的
// 面板头、操作行、设置折叠面板、令牌行、配置片段、P0-B 槽位注释与最近
// 请求表。首关横条在 view 层（desktop.go），不在本页。
func (u *uiState) overviewView(c *ui.Context) {
	t := c.Theme()
	gs := u.svc.Gateway.Status() // 每帧取一次：设置回显与片段端口共用
	state := string(u.status.State)
	ui.Column(c).Gap(12).Children(func() {
		// 面板头：LED + 大号状态词 + 右对齐等宽地址
		ui.Row(c).Gap(10).Children(func() {
			ledDot(c, t, state)
			ui.Text(c, stateWord(state)).Bold().FontSize(t.FontSize + 4)
			ui.Box(c).Grow(1)
			if u.status.Addr != "" {
				ui.Text(c, u.status.Addr).Font(monoFont).TextColor(t.TextMuted)
			}
		})
		if u.status.DegradedReason != "" {
			ui.Textf(c, "降级原因: %s", u.status.DegradedReason).TextColor(t.Warning)
		}
		// 日志路径提示（spec §17 可诊断性）：恒显
		ui.Textf(c, "日志: %s", u.logPath).TextColor(t.TextMuted)
		ui.Divider(c)

		// 操作行：启动/停止按状态守卫（语义与放行集合不变）
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, "启动网关").Clicked() &&
				(state == "stopped" || state == "failed") {
				go func() { _ = u.svc.Gateway.Start() }()
			}
			if ui.Button(c, "停止网关").Clicked() &&
				(state == "running" || state == "degraded") {
				go func() { _ = u.svc.Gateway.Stop() }()
			}
		})

		// 设置折叠面板：open 即 settingsOpen；打开瞬间回显当前值——原
		// 「设置」按钮的行为改为 prev/cur 帧内检测（Collapsible 无打开钩子）。
		if u.settingsOpen && !u.prevSettingsOpen {
			u.portInput = strconv.Itoa(gs.Port)
			u.timeoutInput = strconv.Itoa(gs.TimeoutSeconds)
			u.settingsMsg = ""
		}
		u.prevSettingsOpen = u.settingsOpen
		ui.Collapsible(c, "设置", &u.settingsOpen, func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "端口:").Width(96).TextColor(t.TextMuted)
					ui.TextInput(c, &u.portInput)
				})
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "请求超时（秒）:").Width(96).TextColor(t.TextMuted)
					ui.TextInput(c, &u.timeoutInput)
				})
				ui.Row(c).Gap(8).Children(func() {
					if ui.PrimaryButton(c, "保存设置").Clicked() {
						port, err1 := strconv.Atoi(u.portInput)
						to, err2 := strconv.Atoi(u.timeoutInput)
						if err1 != nil || err2 != nil {
							u.settingsMsg = "端口/超时必须是整数"
						} else {
							restart, err := u.svc.Gateway.SaveSettings(port, to)
							if err != nil {
								u.settingsMsg = err.Error()
							} else if restart {
								u.settingsMsg = "已保存；重启网关后生效"
							} else {
								u.settingsMsg = "已保存"
							}
						}
					}
					if u.settingsMsg != "" {
						ui.Text(c, u.settingsMsg)
					}
				})
			})
		})

		// 访问令牌行：掩码/显示切换、复制、二次确认重新生成（语义不变）。
		// 显示/隐藏是同一个按钮换 label（原代码写法）——不能双按钮同帧
		// 都构建，否则 Tester 的 Click("显示") 在 showToken=true 时仍命中。
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "访问令牌:")
			masked := "***"
			if u.showToken {
				masked = u.token
			}
			ui.Text(c, masked).Font(monoFont).Grow(1)
			label := "显示"
			if u.showToken {
				label = "隐藏"
			}
			if ui.Button(c, label).Clicked() {
				u.showToken = !u.showToken
			}
			if ui.Button(c, "复制令牌").Clicked() {
				mygo.Clipboard.Write(transfer.TextData(u.token))
			}
			if ui.Button(c, "重新生成令牌").Clicked() {
				u.confirmRegen = !u.confirmRegen
			}
			if u.confirmRegen {
				if ui.Button(c, "确认重新生成").Clicked() {
					if tok, err := u.svc.Gateway.RegenerateToken(); err == nil {
						u.token = tok
						u.showToken = false
					}
					u.confirmRegen = false
				}
			}
		})

		// Claude Code 配置片段：端口用当前实际值；展示与复制分离、未点
		// 「显示」时掩码不变量照旧（Review Focus 1——掩码逻辑逐字保留）。
		ui.Text(c, "Claude Code 配置片段:")
		port := gs.Port
		if port <= 0 {
			port = defaultSnippetPort
		}
		base := fmt.Sprintf("export ANTHROPIC_BASE_URL=http://127.0.0.1:%d", port)
		snippet := base + "\nexport ANTHROPIC_AUTH_TOKEN=" + u.token // 复制用：真实片段
		shownTok := "***"                                            // 展示默认掩码
		if u.showToken {
			shownTok = u.token
		}
		display := base + "\nexport ANTHROPIC_AUTH_TOKEN=" + shownTok
		for _, line := range strings.Split(display, "\n") {
			ui.Text(c, line).Font(monoFont)
		}
		if ui.Button(c, "复制配置片段").Clicked() {
			mygo.Clipboard.Write(transfer.TextData(snippet))
		}

		// P0-B 槽位（spec §9）：账号池状态条预留位——每账号一枚迷你状态点
		// +名称。本期只留位不实现，接入点在此 Divider 之后。
		ui.Divider(c)

		// 最近请求：六列不变；时间/状态/耗时等宽化，非 2xx 状态染语义色
		if len(u.recent) == 0 {
			ui.Text(c, "暂无请求——Agent 接入后此处显示最近 100 条").TextColor(t.TextMuted)
		}
		ui.Text(c, "最近请求:")
		cols := []ui.TableColumn{
			{Title: "时间"}, {Title: "模型"}, {Title: "账号"},
			{Title: "状态"}, {Title: "耗时(ms)"}, {Title: "错误"},
		}
		ui.Table(c, &u.table, cols, len(u.recent), func(row, col int) {
			rec := u.recent[row]
			switch col {
			case 0:
				ui.Text(c, rec.StartedAt.Format("15:04:05")).SingleLine().Font(monoFont)
			case 1:
				ui.Text(c, rec.Model).SingleLine()
			case 2:
				ui.Text(c, rec.Account).SingleLine()
			case 3:
				e := ui.Text(c, strconv.Itoa(rec.Status)).SingleLine().Font(monoFont)
				switch {
				case rec.Status == 0 || rec.Status >= 500:
					e.TextColor(t.Danger)
				case rec.Status >= 400:
					e.TextColor(t.Warning)
				}
			case 4:
				ui.Text(c, strconv.FormatInt(rec.DurationMs, 10)).SingleLine().Font(monoFont)
			case 5:
				ui.Text(c, rec.ErrorClass).SingleLine()
			}
		}).Grow(1)
	})
}
