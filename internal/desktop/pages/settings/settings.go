// Package settings 是设置页：网关端口、请求超时、网关诊断、最近请求与
// 关于版本；保存走 bridge.Gateway.SaveSettings。
//
// 透传未登记模型仍是预留功能（Settings.PassthroughUnregistered），首版
// 不提供 UI 开关，避免用户误以为已生效。
package settings

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/config"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/telemetry"
)

// settingsToForm 把生效 Settings 编成表单（Settings→表单）：端口/超时进
// 输入框串，透传进开关。
func settingsToForm(s config.Settings) (portInput, timeoutInput string, passthrough bool) {
	return strconv.Itoa(s.Port), strconv.Itoa(s.RequestTimeoutSeconds), s.PassthroughUnregistered
}

// formToSettings 把表单解回 Settings（表单→Settings）：仅覆盖端口/超时/
// 透传；非整数输入报「端口/超时必须是整数」（旧设置卡文案沿用）。
func formToSettings(portInput, timeoutInput string, passthrough bool) (config.Settings, error) {
	port, err1 := strconv.Atoi(strings.TrimSpace(portInput))
	timeout, err2 := strconv.Atoi(strings.TrimSpace(timeoutInput))
	if err1 != nil || err2 != nil {
		return config.Settings{}, fmt.Errorf("端口/超时必须是整数")
	}
	if port < 1 || port > 65535 {
		return config.Settings{}, fmt.Errorf("端口需在 1–65535 之间")
	}
	if timeout < 1 || timeout > 86400 {
		return config.Settings{}, fmt.Errorf("请求超时需在 1–86400 秒之间")
	}
	return config.Settings{
		Port: port, RequestTimeoutSeconds: timeout, PassthroughUnregistered: passthrough,
	}, nil
}

// Page 是设置页（组合根 pageView 分发）。
// 输入框为空时按当前生效值回显（不与输入中的用户打架）；保存成功（含需
// 重启）即清空输入，下一帧按新生效值回显——校验/落库失败不清，留用户手改。
func Page(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	gs := st.Svc.Gateway.Status()
	if st.Settings.PortInput == "" || st.Settings.TimeoutInput == "" {
		p, to, _ := settingsToForm(config.Settings{
			Port: gs.Port, RequestTimeoutSeconds: gs.TimeoutSeconds,
		})
		if st.Settings.PortInput == "" {
			st.Settings.PortInput = p
		}
		if st.Settings.TimeoutInput == "" {
			st.Settings.TimeoutInput = to
		}
	}
	if !st.Settings.UsageLoaded {
		loadUsage(st)
	}
	if !st.Settings.RecentLoaded {
		loadRecent(st)
	}
	if !st.Settings.AlertsLoaded {
		st.Settings.AlertsOn = st.Svc.Gateway.AlertsEnabled()
		st.Settings.AlertsLoaded = true
	}
	ui.Column(c).Gap(12).Grow(1).Children(func() {
		uikit.PageHeader(c, t, uikit.PageHeaderOpts{
			Title: "设置", Subtitle: "网关、诊断与关于",
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				uikit.Card(c, t, func() {
					ui.Column(c).Gap(8).Children(func() {
						ui.Text(c, "网关").Bold()
						uikit.FormRow(c, t, "端口", func() ui.Element {
							return ui.TextInput(c, &st.Settings.PortInput).Width(160)
						})
						uikit.FormRow(c, t, "请求超时（秒）", func() ui.Element {
							return ui.TextInput(c, &st.Settings.TimeoutInput).Width(160)
						})
						if cb := ui.Checkbox(c, &st.Settings.AlertsOn, "启用冷却/故障告警"); cb.Changed() {
							if err := st.Svc.Gateway.SetAlertsEnabled(st.Settings.AlertsOn); err != nil {
								st.Settings.Msg = err.Error()
							} else {
								st.Settings.Msg = "告警设置已保存"
							}
						}
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							if uikit.PillButton(c, t, "保存设置").Clicked() {
								cfg, err := formToSettings(st.Settings.PortInput, st.Settings.TimeoutInput, st.Settings.PassthroughOn)
								if err != nil {
									st.Settings.Msg = err.Error()
								} else if restart, err := st.Svc.Gateway.SaveSettings(cfg.Port, cfg.RequestTimeoutSeconds); err != nil {
									st.Settings.Msg = err.Error()
								} else {
									if restart {
										st.Settings.Msg = "已保存；停止并重新启动网关后生效（重启前仍使用旧端口）"
									} else {
										st.Settings.Msg = "已保存"
									}
									st.Settings.PortInput, st.Settings.TimeoutInput = "", ""
								}
							}
						})
						uikit.Feedback(c, t, uikit.InferFeedback(st.Settings.Msg), st.Settings.Msg)
					})
				})
				uikit.Card(c, t, func() {
					ui.Column(c).Gap(8).Children(func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "用量（近 30 天）").Bold()
							ui.Box(c).Grow(1)
							if uikit.SoftButton(c, t, s, "gray", "刷新").Clicked() {
								loadUsage(st)
							}
						})
						ui.Text(c, "流式与非流式请求均统计（上游返回 usage 时）；成本为估算，不代表账单").TextColor(t.TextMuted)
						rep := st.Settings.UsageReport
						if rep.Error != "" {
							uikit.Feedback(c, t, uikit.FeedbackError, "读取用量失败："+rep.Error)
						} else {
							ui.Textf(c, "输入 %d token · 输出 %d token", rep.TotalInput, rep.TotalOutput)
							if rep.Priced {
								ui.Textf(c, "估算成本 $%.4f", rep.TotalCostUSD)
							} else {
								ui.Text(c, "未配置价格表，成本不可用").TextColor(t.TextMuted)
							}
							chartSlots := uikit.ChartSlots(t, usageChartNames(rep.Rows, st.Settings.DurationSeries))
							dailyUsageChart(c, t, st.Settings.DailyUsage)
							tokenBarChart(c, t, rep.Rows, chartSlots)
							durationLineChart(c, t, st.Settings.DurationSeries, chartSlots)
							quotaSection(c, t, st)
							shown := len(rep.Rows)
							if shown > 20 {
								shown = 20
							}
							for _, row := range rep.Rows[:shown] {
								line := fmt.Sprintf("%s · %s · %d 次 · 输入 %d / 输出 %d",
									row.Channel, row.Model, row.Requests, row.InputTokens, row.OutputTokens)
								if row.Priced {
									line += fmt.Sprintf(" · $%.4f", row.CostUSD)
								}
								ui.Text(c, line).Font(uikit.MonoFont).TextColor(t.TextMuted).SingleLine()
							}
							if len(rep.Rows) == 0 {
								ui.Text(c, "还没有非流式请求记录").TextColor(t.TextMuted)
							}
						}
						ui.Text(c, "价格表（美元 / 1M token，JSON）").Bold()
						ui.TextArea(c, &st.Settings.UsagePrices).Height(110).Font(uikit.MonoFont)
						ui.Row(c).Gap(8).Children(func() {
							if uikit.SoftButton(c, t, s, "act", "保存价格表").Clicked() {
								if err := st.Svc.Gateway.SavePricesJSON(st.Settings.UsagePrices); err != nil {
									st.Settings.UsageMsg = err.Error()
								} else {
									st.Settings.UsageMsg = "价格表已保存"
									loadUsage(st)
								}
							}
						})
						uikit.Feedback(c, t, uikit.InferFeedback(st.Settings.UsageMsg), st.Settings.UsageMsg)
					})
				})
				uikit.Card(c, t, func() {
					ui.Column(c).Gap(8).Children(func() {
						ui.Text(c, "数据").Bold()
						ui.Text(c, "导出的配置默认不含渠道 Key；导入只新增，不覆盖同名项。").
							TextColor(t.TextMuted)
						ui.Row(c).Gap(8).Children(func() {
							if uikit.SoftButton(c, t, s, "gray", "导出配置").Clicked() {
								home, _ := os.UserHomeDir()
								path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
									Title:       "导出配置",
									DefaultPath: filepath.Join(home, ".agentswitch", "exports", "agentswitch-config.json"),
									Filters:     []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
								})
								if err != nil {
									st.Settings.DataMsg = "导出失败：" + err.Error()
								} else if path != "" {
									sum, err := st.Svc.Gateway.ExportConfig(path)
									if err != nil {
										st.Settings.DataMsg = "导出失败：" + err.Error()
									} else {
										st.Settings.DataMsg = fmt.Sprintf("已导出 %d 个渠道、%d 个逻辑模型、%d 个接入配置：%s",
											sum.Channels, sum.LogicalModels, sum.AccessConfigs, sum.Path)
									}
								}
							}
							if uikit.SoftButton(c, t, s, "gray", "导入配置").Clicked() {
								paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
									Title:   "导入配置",
									Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
								})
								if err == nil && len(paths) > 0 {
									pre, err := st.Svc.Gateway.PreviewImport(paths[0])
									if err != nil {
										st.Settings.DataMsg = "导入预览失败：" + err.Error()
									} else {
										st.Settings.ImportPreview = &pre
										st.Settings.ImportPath = paths[0]
										st.Settings.ImportArm = true
										st.Settings.DataMsg = ""
									}
								}
							}
						})
						uikit.Feedback(c, t, uikit.InferFeedback(st.Settings.DataMsg), st.Settings.DataMsg)
						if st.Settings.ImportArm && st.Settings.ImportPreview != nil {
							p := st.Settings.ImportPreview
							ui.Text(c, "导入预览").Bold()
							ui.Textf(c, "渠道 %d、逻辑模型 %d、接入配置 %d",
								p.Channels, p.LogicalModels, p.AccessConfigs)
							for _, conflict := range p.Conflicts {
								ui.Text(c, "冲突："+conflict).TextColor(t.Warning)
							}
							for _, warning := range p.Warnings {
								ui.Text(c, warning).TextColor(t.TextMuted)
							}
							ui.Row(c).Gap(8).Children(func() {
								if uikit.SoftButton(c, t, s, "gray", "取消导入").Clicked() {
									st.Settings.ImportArm = false
									st.Settings.ImportPreview = nil
									st.Settings.ImportPath = ""
								}
								if uikit.SoftButton(c, t, s, "act", "确认导入").Clicked() {
									res, err := st.Svc.Gateway.ApplyImport(st.Settings.ImportPath)
									if err != nil {
										st.Settings.DataMsg = "导入失败：" + err.Error()
									} else {
										st.Settings.DataMsg = fmt.Sprintf("已导入 %d 个渠道、%d 个逻辑模型、%d 个接入配置；渠道 Key 需重新填写",
											res.AddedChannels, res.AddedLogicalModels, res.AddedAccessConfigs)
										if len(res.Errors) > 0 {
											st.Settings.DataMsg += "；部分失败：" + res.Errors[0]
										}
									}
									st.Settings.ImportArm = false
									st.Settings.ImportPreview = nil
									st.Settings.ImportPath = ""
								}
							})
						}
					})
				})
				uikit.Card(c, t, func() {
					ui.Column(c).Gap(6).Children(func() {
						ui.Text(c, "关于").Bold()
						v := gs.Version
						if v == "" {
							v = "未知"
						}
						ui.Textf(c, "版本 %s", v).TextColor(t.TextMuted)
					})
				})
				uikit.Card(c, t, func() {
					ui.Column(c).Gap(8).Children(func() {
						ui.Text(c, "网关诊断").Bold()
						ui.Row(c).Gap(12).Children(func() {
							ui.Text(c, "状态").Width(uikit.LabelWidth).TextColor(t.TextMuted)
							ui.Text(c, uikit.StateWord(gs.State))
							if reason := uikit.ReasonWord(gs.DegradedReason); reason != "" {
								ui.Text(c, reason).TextColor(t.Warning).Grow(1).SingleLine()
							}
						})
						ui.Row(c).Gap(12).Children(func() {
							ui.Text(c, "地址").Width(uikit.LabelWidth).TextColor(t.TextMuted)
							addr := gs.Addr
							if addr == "" {
								addr = "未监听"
							}
							ui.Text(c, addr).Font(uikit.MonoFont)
						})
						if logPath := st.Svc.Gateway.A.LogPath(); logPath != "" {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "日志").Width(uikit.LabelWidth).TextColor(t.TextMuted)
								ui.Text(c, logPath).Font(uikit.MonoFont).Grow(1).SingleLine()
								if uikit.SoftButton(c, t, s, "gray", "复制").Clicked() {
									mygo.Clipboard.Write(transfer.TextData(logPath))
									st.Settings.Msg = "日志路径已复制"
								}
								if uikit.SoftButton(c, t, s, "gray", "打开目录").Clicked() {
									if err := st.Svc.Gateway.OpenLogDir(); err != nil {
										st.Settings.Msg = "打开日志目录失败：" + err.Error()
									} else {
										st.Settings.Msg = "已打开日志目录"
									}
								}
							})
						}
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, "最近请求").Bold()
							if uikit.Segmented(c, t, s, &st.Settings.RecentWindow, "全部", "今天", "24小时", "7天").Changed() {
								loadRecent(st)
							}
							ui.TextInput(c, &st.Settings.HistoryFilter).Width(260).
								Placeholder("筛选：模型 / 渠道 / 状态")
							ui.Box(c).Grow(1)
							if uikit.SoftButton(c, t, s, "gray", "导出诊断").Clicked() {
								path, err := st.Svc.Gateway.ExportHistory()
								if err != nil {
									st.Settings.HistoryMsg = "导出诊断失败：" + err.Error()
								} else {
									mygo.Clipboard.Write(transfer.TextData(path))
									st.Settings.HistoryMsg = "诊断已导出（路径已复制）：" + path
								}
							}
						})
						uikit.Feedback(c, t, uikit.InferFeedback(st.Settings.HistoryMsg), st.Settings.HistoryMsg)
						if len(st.Settings.Recent) == 0 {
							ui.Text(c, "还没有请求记录——Agent 发起请求后会显示在这里").TextColor(t.TextMuted)
						}
						ui.Text(c, "点击一条记录展开尝试链（渠道/模型/跳过原因）").TextColor(t.TextMuted)
						filter := strings.TrimSpace(st.Settings.HistoryFilter)
						filtered := make([]telemetry.RequestRecord, 0, len(st.Settings.Recent))
						for _, r := range st.Settings.Recent {
							if filter == "" || strings.Contains(r.Model, filter) ||
								strings.Contains(r.Channel, filter) ||
								strings.Contains(fmt.Sprint(r.Status), filter) {
								filtered = append(filtered, r)
							}
						}
						if filter != "" && len(filtered) == 0 {
							ui.Text(c, "没有匹配的请求记录").TextColor(t.TextMuted)
						}
						shown := len(filtered)
						if shown > 20 {
							shown = 20
						}
						for _, r := range filtered[:shown] {
							rec := r
							open := st.Settings.RecentOpen == rec.ID
							arrow := "▸"
							if open {
								arrow = "▾"
							}
							row := ui.ButtonBase(c).Grow(1).Padding(4, 6).Radius(6)
							if row.Hovered() {
								row.Background(t.SurfaceHover)
							}
							row.Children(func() {
								ui.Text(c, arrow+" "+recentLine(rec)).Font(uikit.MonoFont).
									TextColor(t.TextMuted).SingleLine()
							})
							if row.Clicked() {
								if open {
									st.Settings.RecentOpen = ""
								} else {
									st.Settings.RecentOpen = rec.ID
								}
							}
							if open {
								if len(rec.Attempts) == 0 {
									ui.Text(c, "  无尝试记录（旧版本请求）").TextColor(t.TextMuted)
								}
								for i, a := range rec.Attempts {
									ui.Text(c, fmt.Sprintf("  %d. %s", i+1, attemptLine(a))).
										Font(uikit.MonoFont).TextColor(t.TextMuted).SingleLine()
								}
							}
						}
					})
				})
			})
		})
	})
}

// attemptLine 把一次尝试/跳过压成诊断行：渠道·模型 + 结果/原因 + 耗时。
func attemptLine(a telemetry.AttemptRecord) string {
	target := a.Channel
	if a.Model != "" {
		target += "·" + a.Model
	}
	detail := a.Outcome
	switch a.Outcome {
	case "ok":
		detail = "成功"
	case "failed":
		detail = "失败"
	case "canceled":
		detail = "客户端取消"
	case "cooling_skip":
		detail = "跳过（冷却：" + uikit.CooldownReasonWord(strings.TrimPrefix(a.Reason, "cooling:")) + "）"
	case "filtered":
		if strings.HasPrefix(a.Reason, "cooling:") {
			detail = "过滤（冷却：" + uikit.CooldownReasonWord(strings.TrimPrefix(a.Reason, "cooling:")) + "）"
		} else {
			detail = "过滤（" + a.Reason + "）"
		}
	}
	line := target + " " + detail
	if a.DurationMs > 0 {
		line += fmt.Sprintf(" %dms", a.DurationMs)
	}
	return line
}

// recentLine 把一条请求记录压成诊断行（时间 · 模型 · 渠道 · 状态 · 耗时），
// 含故障转移次数与流式标记；只含脱敏元数据（无请求体/密钥）。
func recentLine(r telemetry.RequestRecord) string {
	status := "失败"
	switch {
	case r.Status >= 200 && r.Status < 300:
		status = strconv.Itoa(r.Status) + " ✓"
	case r.Status != 0:
		status = strconv.Itoa(r.Status) + " ✗"
	}
	channel := r.Channel
	if channel == "" {
		channel = "-"
	}
	line := fmt.Sprintf("%s · %s · %s · %s · %dms", r.StartedAt.Format("01-02 15:04:05"), r.Model, channel, status, r.DurationMs)
	if r.FailoverCount > 0 {
		line += fmt.Sprintf(" · 切换%d", r.FailoverCount)
	}
	if r.Streaming {
		line += " · 流"
	}
	return line
}

// loadRecent 按当前时间范围窗口加载持久化请求历史（含重启前记录）。
func loadRecent(st *state.State) {
	var since time.Time
	switch st.Settings.RecentWindow {
	case 1: // 今天：当地零点起
		now := time.Now()
		since = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	case 2:
		since = time.Now().Add(-24 * time.Hour)
	case 3:
		since = time.Now().Add(-7 * 24 * time.Hour)
	}
	st.Settings.Recent = st.Svc.Gateway.History(100, since)
	st.Settings.RecentLoaded = true
}

// loadUsage 刷新 30 天用量报告与价格表原文。
func loadUsage(st *state.State) {
	st.Settings.UsageReport = st.Svc.Gateway.UsageReport(30)
	st.Settings.DailyUsage = st.Svc.Gateway.DailyUsage(7)
	st.Settings.DurationSeries = st.Svc.Gateway.DurationSeries(1, 500)
	st.Settings.QuotaReport = st.Svc.Gateway.QuotaReport()
	st.Settings.QuotasJSON = st.Svc.Gateway.QuotasJSON()
	st.Settings.UsagePrices = st.Svc.Gateway.PricesJSON()
	st.Settings.UsageLoaded = true
}
