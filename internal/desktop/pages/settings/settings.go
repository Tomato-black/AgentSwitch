// Package settings 是设置页：网关端口、请求超时、透传未登记模型开关
// （预留）与关于版本；保存走 bridge.Gateway.SaveSettings。
//
// settings.go 是设置页（spec §6.5 / Task 15）：网关端口、请求超时、
// 透传未登记模型开关（预留——SaveSettings 不消费，spec 裁定 1「首版开关
// 仅预留」）、关于版本。保存走 bridge.Gateway.SaveSettings；重启提示沿用
// 既有文案（「已保存；重启网关后生效」）。
package settings

import (
	"fmt"
	"strconv"

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
	port, err1 := strconv.Atoi(portInput)
	timeout, err2 := strconv.Atoi(timeoutInput)
	if err1 != nil || err2 != nil {
		return config.Settings{}, fmt.Errorf("端口/超时必须是整数")
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
	ui.Column(c).Gap(12).Children(func() {
		uikit.Card(c, t, func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, "网关").Bold()
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "端口").Width(120).TextColor(t.TextMuted)
					ui.TextInput(c, &st.Settings.PortInput)
				})
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "请求超时（秒）").Width(120).TextColor(t.TextMuted)
					ui.TextInput(c, &st.Settings.TimeoutInput)
				})
				ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "透传未登记模型").Width(120).TextColor(t.TextMuted)
					// 预留功能：首版不提供开关（避免用户误以为已生效）。
					ui.Text(c, "关闭").TextColor(t.TextMuted)
					uikit.SoftBadge(c, t, s, "gray", "预留")
				})
				ui.Text(c, "预留功能：未登记模型仍返回 404，首版不提供开关").TextColor(t.TextMuted)
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					if uikit.PillButton(c, t, "保存设置").Clicked() {
						cfg, err := formToSettings(st.Settings.PortInput, st.Settings.TimeoutInput, st.Settings.PassthroughOn)
						if err != nil {
							st.Settings.Msg = err.Error()
						} else if restart, err := st.Svc.Gateway.SaveSettings(cfg.Port, cfg.RequestTimeoutSeconds); err != nil {
							st.Settings.Msg = err.Error()
						} else {
							if restart {
								st.Settings.Msg = "已保存；重启网关后生效"
							} else {
								st.Settings.Msg = "已保存"
							}
							st.Settings.PortInput, st.Settings.TimeoutInput = "", ""
						}
					}
					if st.Settings.Msg != "" {
						ui.Text(c, st.Settings.Msg)
					}
				})
			})
		})
		uikit.Card(c, t, func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Text(c, "网关诊断").Bold()
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "状态").Width(120).TextColor(t.TextMuted)
					ui.Text(c, uikit.StateWord(gs.State))
					if reason := uikit.ReasonWord(gs.DegradedReason); reason != "" {
						ui.Text(c, reason).TextColor(t.Warning).Grow(1).SingleLine()
					}
				})
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "地址").Width(120).TextColor(t.TextMuted)
					addr := gs.Addr
					if addr == "" {
						addr = "未监听"
					}
					ui.Text(c, addr).Font(uikit.MonoFont)
				})
				if logPath := st.Svc.Gateway.A.LogPath(); logPath != "" {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "日志").Width(120).TextColor(t.TextMuted)
						ui.Text(c, logPath).Font(uikit.MonoFont).Grow(1).SingleLine()
						if uikit.SoftButton(c, t, s, "gray", "复制").Clicked() {
							mygo.Clipboard.Write(transfer.TextData(logPath))
							st.Settings.Msg = "日志路径已复制"
						}
					})
				}
				ui.Text(c, "最近请求").Bold()
				if len(st.Recent) == 0 {
					ui.Text(c, "还没有请求记录——Agent 发起请求后会显示在这里").TextColor(t.TextMuted)
				}
				shown := len(st.Recent)
				if shown > 8 {
					shown = 8
				}
				for _, r := range st.Recent[:shown] {
					ui.Text(c, recentLine(r)).Font(uikit.MonoFont).TextColor(t.TextMuted).SingleLine()
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
	})
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
	line := fmt.Sprintf("%s · %s · %s · %s · %dms", r.StartedAt.Format("15:04:05"), r.Model, channel, status, r.DurationMs)
	if r.FailoverCount > 0 {
		line += fmt.Sprintf(" · 切换%d", r.FailoverCount)
	}
	if r.Streaming {
		line += " · 流"
	}
	return line
}
