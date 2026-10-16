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

// overviewView 是概览页：状态/操作、设置面板、访问令牌、Claude Code 配置
// 片段、首关教育与最近请求表。
func (u *uiState) overviewView(c *ui.Context) {
	gs := u.svc.Gateway.Status() // 每帧取一次：设置回显与片段端口共用
	ui.Column(c).Gap(8).Children(func() {
		ui.Textf(c, "网关状态: %s", u.status.State)
		if u.status.Addr != "" {
			ui.Textf(c, "监听地址: %s", u.status.Addr)
		}
		if u.status.DegradedReason != "" {
			ui.Textf(c, "降级原因: %s", u.status.DegradedReason)
		}
		// 日志路径提示（spec §17 可诊断性）：恒显——降级/失败时是定位入口，
		// 正常态给一个可发现的位置也不碍事（选恒显，免条件分支）。
		ui.Textf(c, "日志: %s", u.logPath)

		// 操作行：启动/停止按状态守卫；设置切换面板并回显当前值
		ui.Row(c).Gap(8).Children(func() {
			// 启动放行 stopped 与 failed：Failed 是可恢复态（spec §5），
			// StopGateway 对 gw==nil 幂等，二次 Start 不被旧句柄挡住。
			if ui.PrimaryButton(c, "启动网关").Clicked() &&
				(u.status.State == "stopped" || u.status.State == "failed") {
				go func() { _ = u.svc.Gateway.Start() }()
			}
			if ui.Button(c, "停止网关").Clicked() &&
				(u.status.State == "running" || u.status.State == "degraded") {
				go func() { _ = u.svc.Gateway.Stop() }()
			}
			if ui.Button(c, "设置").Clicked() {
				u.settingsOpen = !u.settingsOpen
				if u.settingsOpen {
					u.portInput = strconv.Itoa(gs.Port)
					u.timeoutInput = strconv.Itoa(gs.TimeoutSeconds)
					u.settingsMsg = ""
				}
			}
		})

		// 设置面板
		if u.settingsOpen {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "端口:")
				ui.TextInput(c, &u.portInput)
				ui.Text(c, "请求超时（秒）:")
				ui.TextInput(c, &u.timeoutInput)
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
		}

		// 访问令牌：掩码/显示切换、复制、二次确认后重新生成（同步）
		ui.Text(c, "访问令牌:")
		masked := "***"
		if u.showToken {
			masked = u.token
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, masked)
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

		// Claude Code 配置片段：端口用当前实际值，不硬编码。
		// 降级说明（简报修正条款 3）：TextArea.ReadOnly 编译通过，但 Tester
		// 只收集 labels，TextArea 的值不进 labels——HasText("ANTHROPIC_BASE_URL")
		// 拿不到，故按条款降级为逐行只读 ui.Text 展示（复制仍走复制按钮）。
		//
		// 展示与复制分离（评审 Important：显示前掩码不变量）：展示行在未点
		// "显示"时令牌掩码为 ***，复制按钮始终写真实片段——可复制但不可见。
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
			ui.Text(c, line)
		}
		if ui.Button(c, "复制配置片段").Clicked() {
			mygo.Clipboard.Write(transfer.TextData(snippet))
		}

		// 首关教育（从 T12 的 view 迁入）
		if u.closeNotice && !u.closeConfirmed {
			ui.Text(c, "窗口关闭后应用将驻留托盘，网关继续运行。")
			if ui.PrimaryButton(c, "继续关闭窗口").Clicked() {
				u.closeConfirmed = true
				if w := mygo.FocusedWindow(); w != nil {
					w.Close()
				}
			}
			if ui.Button(c, "取消").Clicked() {
				u.closeNotice = false
			}
		}

		// 最近请求：六列（时间/模型/账号/状态/耗时/错误）
		ui.Text(c, "最近请求:")
		cols := []ui.TableColumn{
			{Title: "时间"}, {Title: "模型"}, {Title: "账号"},
			{Title: "状态"}, {Title: "耗时(ms)"}, {Title: "错误"},
		}
		ui.Table(c, &u.table, cols, len(u.recent), func(row, col int) {
			rec := u.recent[row]
			switch col {
			case 0:
				ui.Text(c, rec.StartedAt.Format("15:04:05")).SingleLine()
			case 1:
				ui.Text(c, rec.Model).SingleLine()
			case 2:
				ui.Text(c, rec.Account).SingleLine()
			case 3:
				ui.Text(c, strconv.Itoa(rec.Status)).SingleLine()
			case 4:
				ui.Text(c, strconv.FormatInt(rec.DurationMs, 10)).SingleLine()
			case 5:
				ui.Text(c, rec.ErrorClass).SingleLine()
			}
		}).Grow(1)
	})
}
