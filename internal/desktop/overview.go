package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/telemetry"
)

// defaultSnippetPort 是配置片段端口的兜底（config.Default().Port）；正常
// 路径取 bridge.Status.Port（Gateway.Status 从 config.Load 填充）。
const defaultSnippetPort = 17890

// recentStats 统计 hero 徽章「近 N 条 · M 失败」：数据只读自内存遥测环
// （重启清零、不落库不扩 schema，spec §8.7）；失败 = Status==0（未收到
// 响应即中断）或 >=400，2xx/3xx 都不算失败。
func recentStats(recs []telemetry.RequestRecord) (total, failed int) {
	total = len(recs)
	for _, r := range recs {
		if r.Status == 0 || r.Status >= 400 {
			failed++
		}
	}
	return total, failed
}

// snippetBg 是接入卡片段代码块底色（spec §5.3）：浅色 #FAFAFA、深色 #232327。
func snippetBg(t *ui.Theme) ui.Color {
	if t.Dark {
		return ui.Hex("#232327")
	}
	return ui.Hex("#FAFAFA")
}

// overviewView 是概览页（四卡形态，spec §5.3）：状态 hero、接入 Claude Code、
// 密钥池+设置并排两小卡、最近请求卡；hero 卡下降级横幅条件出现。首关横条
// 在 view 层（desktop.go），不在本页。掩码/二次确认/启停守卫等不变量逐条
// 对拍 spec §8。
func (u *uiState) overviewView(c *ui.Context) {
	t := c.Theme()
	s := softs(t)
	gs := u.svc.Gateway.Status() // 每帧取一次：设置回显、地址行与片段端口共用
	state := string(u.status.State)
	total, failed := recentStats(u.recent)

	ui.Column(c).Gap(12).Children(func() {
		// 卡 1 状态 hero：LED+状态词+右侧统计徽章（total>0 才显）与启停钮；
		// 等宽地址行 + 恒显日志路径行（spec §17 可诊断性照旧；不做「日志」
		// 按钮——无既有打开日志能力，按非目标不新增）。
		card(c, t, func() {
			ui.Column(c).Gap(6).Children(func() {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ledDot(c, t, state)
					ui.Text(c, stateWord(state)).Bold().FontSize(16)
					ui.Box(c).Grow(1)
					if total > 0 {
						softBadge(c, t, s, "gray", fmt.Sprintf("近 %d 条 · %d 失败", total, failed))
					}
					// 启停按钮按实际状态守卫（spec §8.3：stopped/failed 才可
					// 启动、running/degraded 才可停止——渲染与点击双重限定，
					// 语义与放行集合同旧版）。
					if state == "stopped" || state == "failed" {
						if pillButton(c, t, "启动网关").Clicked() {
							go func() { _ = u.svc.Gateway.Start() }()
						}
					}
					if state == "running" || state == "degraded" {
						if softButton(c, t, s, "danger", "停止网关").Clicked() {
							go func() { _ = u.svc.Gateway.Stop() }()
						}
					}
				})
				// 地址行：优先实际监听地址（StateInfo.Addr，host:port——状态
				// 真实性不变量），未启动过回退配置端口拼 127.0.0.1。
				addr := u.status.Addr
				if addr == "" {
					port := gs.Port
					if port <= 0 {
						port = defaultSnippetPort
					}
					addr = fmt.Sprintf("127.0.0.1:%d", port)
				}
				ui.Textf(c, "http://%s · 超时 %ds", addr, gs.TimeoutSeconds).Font(monoFont).TextColor(t.TextMuted)
				ui.Textf(c, "日志: %s", u.logPath).Font(monoFont).TextColor(t.TextMuted)
			})
		})

		// 降级横幅：DegradedReason 非空时 hero 卡下的琥珀文字条（语义照旧）。
		if r := u.status.DegradedReason; r != "" {
			ui.Box(c).Radius(8).Background(s.Warn).Padding(8, 12).Children(func() {
				ui.Textf(c, "降级原因: %s", r).TextColor(s.WarnText)
			})
		}

		// 卡 2 接入 Claude Code：片段代码块 + 显示/复制片段/重新生成。
		u.accessCard(c, t, s, gs.Port)

		// 卡 3：密钥池与设置并排两小卡——外层 Grow(1) Box 各占一半，
		// 卡 Box 在其内横向撑满（Box 默认纵向，子件交叉轴 stretch）。
		ui.Row(c).Gap(12).Children(func() {
			ui.Box(c).Grow(1).Children(func() { u.poolCard(c, t, s) })
			ui.Box(c).Grow(1).Children(func() { u.settingsCard(c, t, gs.Port, gs.TimeoutSeconds) })
		})

		// 卡 4 最近请求：Row 包裹让卡吃掉页面剩余高度——表是虚拟滚动列表，
		// 需要受界高度才滚动（与旧布局表尾 Grow(1) 同效）。
		ui.Row(c).Grow(1).Children(func() {
			u.recentCard(c, t)
		})
	})

	// 重新生成令牌的 AlertDialog 二次确认（spec §8.2）：每帧无条件调用
	// （open=false 时它是 no-op，弹窗自身负责开合）。按钮序刻意把「取消」
	// 放末位——框架把最后一个按钮染 Accent 并绑定 Enter，取消占位让回车
	// 永远落在安全侧；i==0 才是「确认重新生成」。确认后 AlertDialog 自行
	// 关闭（open=false），无需手工清位。
	if i := ui.AlertDialog(c, &u.confirmRegen, "重新生成访问令牌",
		"旧令牌立即失效，接入方需更新配置。", "确认重新生成", "取消"); i == 0 {
		// 与旧两段按钮同款收尾：换令牌并掩回（旧令牌曾明文展示过）。
		if tok, err := u.svc.Gateway.RegenerateToken(); err == nil {
			u.token = tok
			u.showToken = false
		}
	}
}

// poolCard 是密钥池小卡（spec §5.3）：Success 静态点「全部就绪」，或每枚
// 冷却密钥一枚琥珀 chips（渠道/密钥+剩余秒；PoolStatus 只回冷却中的密钥，
// 正常密钥不出现）。
func (u *uiState) poolCard(c *ui.Context, t *ui.Theme, s softPalette) {
	card(c, t, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "密钥池").Bold()
			if pool := u.svc.Gateway.PoolStatus(); len(pool) > 0 {
				ui.Row(c).Gap(6).Children(func() {
					for _, p := range pool {
						softBadge(c, t, s, "warn",
							fmt.Sprintf("%s/%s 冷却 %ds", p.ChannelName, p.KeyName, p.RemainingSec))
					}
				})
			} else {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					// 就绪点用静态 Success 绿，不走 ledDot("running")——那会
					// 带上呼吸动画，就绪态不该动。
					ui.Box(c).Width(8).Height(8).Radius(4).Background(t.Success)
					ui.Text(c, "全部就绪").TextColor(t.TextMuted)
				})
			}
		})
	})
}

// settingsCard 是设置小卡（spec §5.3：原 Collapsible 改常开）：端口/超时
// 输入与保存。回显只在输入框为空时发生——首帧或上次保存成功清空后按当前
// 生效值回填（原「打开瞬间回显」的等价替换，不与输入中的用户打架）；
// SaveSettings 语义与提示文案一字不动。
func (u *uiState) settingsCard(c *ui.Context, t *ui.Theme, port, timeout int) {
	card(c, t, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "设置").Bold()
			if u.portInput == "" {
				u.portInput = strconv.Itoa(port)
			}
			if u.timeoutInput == "" {
				u.timeoutInput = strconv.Itoa(timeout)
			}
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "端口:").Width(96).TextColor(t.TextMuted)
				ui.TextInput(c, &u.portInput)
			})
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "请求超时（秒）:").Width(96).TextColor(t.TextMuted)
				ui.TextInput(c, &u.timeoutInput)
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if pillButton(c, t, "保存设置").Clicked() {
					p, err1 := strconv.Atoi(u.portInput)
					to, err2 := strconv.Atoi(u.timeoutInput)
					if err1 != nil || err2 != nil {
						u.settingsMsg = "端口/超时必须是整数"
					} else if restart, err := u.svc.Gateway.SaveSettings(p, to); err != nil {
						u.settingsMsg = err.Error()
					} else {
						if restart {
							u.settingsMsg = "已保存；重启网关后生效"
						} else {
							u.settingsMsg = "已保存"
						}
						// 保存成功（含需重启）即清空输入，下一帧按新生效值
						// 回显；校验/落库失败不清，留用户手改。
						u.portInput, u.timeoutInput = "", ""
					}
				}
				if u.settingsMsg != "" {
					ui.Text(c, u.settingsMsg)
				}
			})
		})
	})
}

// accessCard 是接入卡（spec §5.3）：片段代码块 + 显示/复制片段/重新生成。
// 展示行令牌恒掩码、复制经 mygo.Clipboard 写真实片段——展示与复制分离
// （Review Focus 1 语义原样）；显示/隐藏是同一按钮换 label，不能双按钮
// 同帧并存（否则 Tester 的 Click("显示") 在 showToken=true 时仍命中）。
func (u *uiState) accessCard(c *ui.Context, t *ui.Theme, s softPalette, port int) {
	card(c, t, func() {
		ui.Column(c).Gap(10).Children(func() {
			ui.Text(c, "接入 Claude Code").Bold()
			if port <= 0 {
				port = defaultSnippetPort
			}
			base := fmt.Sprintf("export ANTHROPIC_BASE_URL=http://127.0.0.1:%d", port)
			snippet := base + "\nexport ANTHROPIC_AUTH_TOKEN=" + u.token // 复制用：真实片段
			shownTok := "***"                                            // 展示默认掩码
			if u.showToken {
				shownTok = u.token
			}
			// 代码块：Radius 9 专属圆角、mono 小一号（scale 1 时 11.5，随
			// textScale 缩放不丢可访问性）。
			ui.Box(c).Radius(9).Background(snippetBg(t)).Padding(10, 12).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					for _, line := range strings.Split(base+"\nexport ANTHROPIC_AUTH_TOKEN="+shownTok, "\n") {
						ui.Text(c, line).Font(monoFont).FontSize(t.FontSize - 1.5).SingleLine()
					}
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				label := "显示"
				if u.showToken {
					label = "隐藏"
				}
				if softButton(c, t, s, "gray", label).Clicked() {
					u.showToken = !u.showToken
				}
				if softButton(c, t, s, "gray", "复制片段").Clicked() {
					mygo.Clipboard.Write(transfer.TextData(snippet))
				}
				if softButton(c, t, s, "gray", "重新生成").Clicked() {
					u.confirmRegen = true
				}
			})
		})
	})
}

// recentCard 是最近请求卡（spec §5.3）：七列 时间/模型/渠道/密钥/状态/耗时/
// 错误；「渠道/密钥」列 = Channel/Account 等宽两段；状态码 2xx=Success、
// 4xx=Warning、5xx/0=Danger 文本色；空态是邀请不是空白。
func (u *uiState) recentCard(c *ui.Context, t *ui.Theme) {
	card(c, t, func() {
		ui.Column(c).Gap(8).Grow(1).Children(func() {
			ui.Text(c, "最近请求").Bold()
			if len(u.recent) == 0 {
				ui.Text(c, "暂无请求——Agent 接入后此处显示最近 100 条").TextColor(t.TextMuted)
			}
			cols := []ui.TableColumn{
				{Title: "时间"}, {Title: "模型"}, {Title: "渠道/密钥"},
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
					ui.Textf(c, "%s/%s", rec.Channel, rec.Account).SingleLine().Font(monoFont)
				case 3:
					e := ui.Text(c, strconv.Itoa(rec.Status)).SingleLine().Font(monoFont)
					switch {
					case rec.Status == 0 || rec.Status >= 500:
						e.TextColor(t.Danger)
					case rec.Status >= 400:
						e.TextColor(t.Warning)
					case rec.Status >= 200 && rec.Status < 300:
						e.TextColor(t.Success)
					}
				case 4:
					ui.Text(c, strconv.FormatInt(rec.DurationMs, 10)).SingleLine().Font(monoFont)
				case 5:
					ui.Text(c, rec.ErrorClass).SingleLine()
				}
			}).Grow(1)
		})
	})
}
