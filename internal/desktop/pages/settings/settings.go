// settings.go 是设置页（spec §6.5 / Task 15）：网关端口、请求超时、
// 透传未登记模型开关（预留——SaveSettings 不消费，spec 裁定 1「首版开关
// 仅预留」）、关于版本。保存走 bridge.Gateway.SaveSettings；重启提示沿用
// 既有文案（「已保存；重启网关后生效」）。
package settings

import (
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/config"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
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
					ui.Switch(c, &st.Settings.PassthroughOn)
					uikit.SoftBadge(c, t, s, "gray", "预留")
				})
				ui.Text(c, "首版预留：开启后未登记模型仍返回 404").TextColor(t.TextMuted)
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
