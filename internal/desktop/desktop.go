// Package desktop 是 MyGo 桌面壳：窗口、托盘、关窗拦截、退出钩子与状态桥接。
// UI 页面（设置/供应商/账号/模型）由 Task 13-15 在本包视图上扩展。
package desktop

import (
	"context"
	_ "embed"
	"log"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/telemetry"
)

//go:embed tray.png
var trayIcon []byte

type Services struct {
	Gateway   bridge.Gateway
	Providers bridge.Providers
	Accounts  bridge.Accounts
	Models    bridge.Models
}

type uiState struct {
	svc            Services
	status         app.StateInfo
	recent         []telemetry.RequestRecord
	token          string
	closeNotice    bool // 首关教育提示
	closeConfirmed bool // 用户已知晓，允许关闭
}

func newUIState(svc Services, a *app.App) *uiState {
	si := a.State()
	st, _ := a.CurrentSettings()
	return &uiState{svc: svc, status: si, recent: a.Recent(), token: st.GatewayToken}
}

func (u *uiState) view(c *ui.Context) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Textf(c, "网关状态: %s", u.status.State)
		if u.status.Addr != "" {
			ui.Textf(c, "监听: %s", u.status.Addr)
		}
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
		if ui.PrimaryButton(c, "启动网关").Clicked() && u.status.State == "stopped" {
			go func() { _ = u.svc.Gateway.Start() }()
		}
		if ui.Button(c, "停止网关").Clicked() && (u.status.State == "running" || u.status.State == "degraded") {
			go func() { _ = u.svc.Gateway.Stop() }()
		}
	})
}

func Run(a *app.App) error {
	svc := Services{
		Gateway:   bridge.Gateway{A: a},
		Providers: bridge.Providers{A: a, Creds: a.Creds()},
		Accounts:  bridge.Accounts{A: a, Creds: a.Creds()},
		Models:    bridge.Models{A: a},
	}
	mygo.Bind(svc.Gateway)
	mygo.Bind(svc.Providers)
	mygo.Bind(svc.Accounts)
	mygo.Bind(svc.Models)

	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}
	u := newUIState(svc, a)
	var win *mygo.Window

	mygo.App.WhenReady(func() {
		if tray, err := mygo.NewTray(mygo.TrayOptions{
			Icon:    trayIcon,
			ToolTip: "AgentSwitch",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: "打开主窗口", Click: func(*mygo.MenuItem, *mygo.Window) {
					if win != nil {
						win.Restore()
						win.Focus()
					} else {
						openWindow(u, &win)
					}
				}},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}),
		}); err != nil {
			a.Log().Info("tray unavailable (继续运行)", "err", err.Error())
		} else {
			_ = tray
		}
		openWindow(u, &win)
		win.OnClose(func(e *mygo.CloseEvent) {
			if !u.closeConfirmed {
				e.PreventDefault()
				u.closeNotice = true
				win.Update(func() {}) // 触发重绘显示提示
			}
		})
		a.OnState(func(si app.StateInfo) {
			if win != nil {
				win.Update(func() { u.status = si })
			}
		})
		a.OnRecord(func(rec telemetry.RequestRecord) {
			if win != nil {
				win.Update(func() {
					u.recent = append([]telemetry.RequestRecord{rec}, u.recent...)
					if len(u.recent) > 100 {
						u.recent = u.recent[:100]
					}
				})
			}
		})
	})

	// 关窗不退出：最后一个窗口关闭时保持运行（托盘驻留）
	mygo.App.OnWindowAllClosed(func() { /* 故意为空 */ })

	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(graceSeconds(a))*time.Second)
		defer cancel()
		if err := a.StopGateway(ctx); err != nil {
			log.Println("stop gateway:", err)
		}
		if err := a.Close(); err != nil {
			log.Println("close app:", err)
		}
	})
	return mygo.App.Run()
}

func openWindow(u *uiState, win **mygo.Window) {
	*win = mygo.NewWindow(mygo.WindowOptions{
		Title: "AgentSwitch", Width: 1080, Height: 720, StateKey: "main",
		Content: ui.View(u.view),
	})
}

func graceSeconds(a *app.App) int {
	st, err := a.CurrentSettings()
	if err != nil || st.ShutdownGraceSeconds <= 0 {
		return 15
	}
	return st.ShutdownGraceSeconds
}
