// Package desktop 是 MyGo 桌面壳：窗口、托盘、关窗拦截、退出钩子与状态桥接。
// UI 页面（设置/供应商/账号/模型）由 Task 13-15 在本包视图上扩展。
package desktop

import (
	"context"
	_ "embed"
	"log"
	"sync/atomic"
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
	// win 经 atomic 读写：WhenReady/托盘重开（主线程）写，OnState/OnRecord
	// 回调（网关 goroutine）读——消除裸指针的数据竞争。
	var win atomic.Pointer[mygo.Window]

	mygo.App.WhenReady(func() {
		if tray, err := mygo.NewTray(mygo.TrayOptions{
			Icon:    trayIcon,
			ToolTip: "AgentSwitch",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: "打开主窗口", Click: func(*mygo.MenuItem, *mygo.Window) {
					if w := win.Load(); !needsReopen(w) {
						w.Restore()
						w.Focus()
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
		a.OnState(func(si app.StateInfo) {
			if w := win.Load(); w != nil {
				w.Update(func() { u.status = si })
			}
		})
		a.OnRecord(func(rec telemetry.RequestRecord) {
			if w := win.Load(); w != nil {
				w.Update(func() {
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
		// 首关教育只针对交互关闭（点 X）。mygo prepareQuit 先跑本钩子
		// （app.go:184-187），随后 a.quitting=true 并逐窗 w.close()
		// （app.go:189-194）——任一 OnClose PreventDefault 即复位 quitting
		// 中止退出。若不在第一行放行，托盘 RoleQuit/Ctrl+Q 在"窗口开着且
		// 未确认过教育提示"（最常见态）下：清理已做、退出被拦、UI 停在
		// 已关闭 DB 的尸体上。
		u.closeConfirmed = true
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

// needsReopen 报告托盘"打开主窗口"是否应重建窗口：无窗口，或窗口已销毁
// （mygo destroy 后 native=nil/destroyed=true，window.go:1605-1606，此时
// Restore/Focus 是 no-op，重建分支才可达）。
func needsReopen(w *mygo.Window) bool { return w == nil || w.IsDestroyed() }

// openWindow 建主窗、登记进 win 并挂首关拦截。OnClose 挂在这里而非仅挂
// 首窗：托盘重开的每个窗口都自带拦截，不依赖"销毁过 ⇒ closeConfirmed"
// 这条跨模块不变式。
func openWindow(u *uiState, win *atomic.Pointer[mygo.Window]) *mygo.Window {
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: "AgentSwitch", Width: 1080, Height: 720, StateKey: "main",
		Content: ui.View(u.view),
	})
	win.Store(w)
	w.OnClose(func(e *mygo.CloseEvent) {
		if !u.closeConfirmed {
			e.PreventDefault()
			u.closeNotice = true
			w.Update(func() {}) // 触发重绘显示提示
		}
	})
	return w
}

func graceSeconds(a *app.App) int {
	st, err := a.CurrentSettings()
	if err != nil || st.ShutdownGraceSeconds <= 0 {
		return 15
	}
	return st.ShutdownGraceSeconds
}
