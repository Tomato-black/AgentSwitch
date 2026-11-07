// Package desktop 是 MyGo 桌面壳（组合根）：窗口、托盘、关窗拦截、退出钩子
// 与状态桥接；UI 页面在 pages/* 子包，本包负责组合与分发。
package desktop

import (
	"context"
	_ "embed"
	"log"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/telemetry"
)

//go:embed tray.png
var trayIcon []byte

//go:embed appicon.png
var appIcon []byte

// appTray 持有托盘对象的强引用，必须活到进程结束。
//
// MyGo 的菜单项注册表（menu.go menuItems.byID）只存 weak.Pointer：*Tray
// 一旦被 GC，Tray.menu → Menu.items → MenuItem 全部不可达，而 native GTK
// 菜单仍挂在 appindicator 上照常显示；此时点击会在 menuItemClicked 的
// lookupMenuItem 处拿到 nil 并静默返回——托盘所有菜单项（含「退出」）
// 点了都没反应。NewTray 不会在框架内部保留引用，所有权在调用方。
var appTray *mygo.Tray

// keepTray 记录托盘强引用（见 appTray）。
func keepTray(t *mygo.Tray) { appTray = t }

// menuIcon 把 uikit SVG 转菜单图标（mygo v0.8.1 NewMenuIconSVG：ui.SVG 天然
// 满足 SVGImage 接口；template=true 单色模板随明暗主题）。失败返回 nil 降级
// 纯文字——图标是装饰，不阻塞托盘创建。
func menuIcon(svg *ui.SVG) *mygo.MenuIcon {
	ic, err := mygo.NewMenuIconSVG(svg, true)
	if err != nil {
		return nil
	}
	return ic
}

// uiState 是根包的 UI 状态适配器：嵌入共享 state（壳层 + 各页状态），
// 下列过渡字段在 T4–T12 迁移中逐页迁出（页面搬走后删除）。
type uiState struct {
	*state.State
	alerts *alertState
	Zoom   int // 缩放档位下标（zoom.go；随窗口会话，不持久化）
}

func newUIState(svc state.Services) *uiState {
	u := &uiState{State: state.New(svc), alerts: newAlertState(), Zoom: zoomOne}
	u.Schedule = afterEvent
	return u
}

// navItems、navItem、gatewayStatusBar 与 pageView 分发在 shell.go
// （cc-switch 式侧栏壳 + 底条网关状态）。

// view 是窗口根：按系统外观选品牌主题，左列自绘侧栏（品牌行 + 导航 +
// 底条网关状态）、右内容列（首关横条 + 当前页）。SetTheme 必须在任何
// 子件构建前调用（api_gen.go：SetTheme makes the frame use t）。导航点击
// 直接 u.Page = id（四个 id 与 pageView 分发一致），点选即切页，无需
// Changed 处理。
// 侧栏弃用 ui.Sidebar：其选中项硬编码 Accent 底（sidebar.go:188），与
// cc-switch 的中性灰选中冲突（spec §3.4），自绘列底色用 sidebarColor。
func (u *uiState) view(c *ui.Context) {
	c.SetTheme(uikit.BuildTheme(c.Theme().Dark, c.Preferences().TextScale))
	// 全局浏览器式缩放（mygo v0.7.0）：Cmd/Ctrl +=/-/0（Shift+= 即 +）；
	// 文字/矢量按缩放档位清晰重绘。每帧重设（档位是唯一状态）。
	switch {
	case c.Shortcut(ui.Cmd, ui.KeyEqual) || c.Shortcut(ui.Cmd|ui.Shift, ui.KeyEqual):
		u.Zoom = zoomStep(u.Zoom, 1)
	case c.Shortcut(ui.Cmd, ui.KeyMinus):
		u.Zoom = zoomStep(u.Zoom, -1)
	case c.Shortcut(ui.Cmd, ui.Key0):
		u.Zoom = zoomOne
	}
	c.SetZoom(zoomAt(u.Zoom))
	t := c.Theme()
	// 根 Row 必须 Fill()（宽高 100%）：仅 Grow(1) 只撑主轴（宽），高度仍是
	// 内容自然高——整窗内容居中悬空、上下留白。侧栏固定宽 + FillHeight 撑满
	// 高，内容列 Grow(1) 吃掉剩余宽（cc-switch 全高侧栏布局）。
	ui.Row(c).Gap(0).Fill().Children(func() {
		// 左列：自绘侧栏（spec §6.1）——品牌行、导航、底条网关状态
		// （状态是主角：任何页可见）；spacer 把底条钉在栏底。
		ui.Column(c).Gap(0).Width(200).FillHeight().Background(uikit.SidebarColor(t)).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Padding(t.Space(3), t.Space(2.5)).
				Children(func() { ui.Text(c, "AgentSwitch").Bold().FontSize(14) })
			ui.Column(c).Gap(2).Padding(t.Space(1), t.Space(2)).Children(func() {
				for _, it := range navItems {
					u.navItem(c, t, it.id, it.label, it.icon)
				}
			})
			ui.Box(c).Grow(1)
			gatewayStatusBar(c, u)
		})
		// 右列：当前页
		ui.Column(c).Gap(12).Padding(16).Grow(1).FillHeight().Children(func() {
			setupChecklist(c, u)
			u.pageView(c)
		})
	})
	// 首关驻留模态挂在根层：任意页点 X 都可见，且遮罩挡住页面交互。
	u.closeModal(c)
	// 停止网关确认同理挂在根层（底条在任何页可见）。
	stopConfirm(c, u)
	// 未保存编辑的切页确认（侧栏导航 / 页面返回共用）。
	u.navConfirm(c)
}

func Run(a *app.App) error {
	svc := state.Services{
		Gateway:  bridge.Gateway{A: a},
		Channels: bridge.Channels{A: a, Creds: a.Creds()},
		Logical:  &bridge.LogicalModels{A: a},
	}
	mygo.Bind(svc.Gateway)
	mygo.Bind(svc.Channels)
	mygo.Bind(svc.Logical)

	// 单实例锁已在入口（cmd/agentswitch/main.go）取——那里的抢锁失败在
	// app.Open 之前返回，不泄漏库句柄、不留并发迁移窗口；此处不得再取锁。
	u := newUIState(svc)
	// win 经 atomic 读写：WhenReady/托盘重开（主线程）写，OnState/OnRecord
	// 回调（网关 goroutine）读——消除裸指针的数据竞争。
	var win atomic.Pointer[mygo.Window]
	// 冷却倒计时刷新：仅当存在冷却时每秒重绘一次（空闲零开销），退出时停止。
	coolStop := make(chan struct{})
	var coolStopOnce sync.Once

	// 第二实例唤回：隐藏到托盘后用户再次启动应用（常见于托盘不可见的
	// Linux 桌面），不能静默退出——把已有窗口 Show/Restore/Focus，窗口已
	// 销毁则重建（2026-10-29 P1-15）。
	mygo.App.OnSecondInstance(func([]string, string) {
		if w := win.Load(); !needsReopen(w) {
			afterEvent(func() {
				w.Show()
				w.Restore()
				w.Focus()
			})
			return
		}
		openWindow(u, &win)
	})
	// 点击系统通知唤回主窗（告警只在托盘驻留时最有价值）。
	mygo.App.OnNotificationClick(func(string) {
		if w := win.Load(); !needsReopen(w) {
			afterEvent(func() {
				w.Show()
				w.Restore()
				w.Focus()
			})
			return
		}
		openWindow(u, &win)
	})

	mygo.App.WhenReady(func() {
		// 必须在建窗前：MyGo 在窗口宿主初始化时读一次 GTK 的字体渲染设置。
		// 关闭子像素抗锯齿以绕开其 Linux 字形着色缺陷（见 fontrender_linux.go）。
		disableSubpixelAA()
		if tray, err := newTray(mygo.TrayOptions{
			Icon:           trayIcon,
			IconIsTemplate: runtime.GOOS == "darwin",
			ToolTip:        "AgentSwitch",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: "打开主窗口", Icon: menuIcon(uikit.IconAccess), Click: func(*mygo.MenuItem, *mygo.Window) {
					if w := win.Load(); !needsReopen(w) {
						// 隐藏型窗口要先 Show 才会回来；Restore 兼顾最小化态。
						// 必须推迟出菜单点击事件，否则 Show 触发的帧会提交到
						// 隐藏时已失效的 GL 表面（实测 SIGABRT）。
						afterEvent(func() {
							w.Show()
							w.Restore()
							w.Focus()
						})
					} else {
						openWindow(u, &win)
					}
				}},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}),
		}); err != nil {
			u.TrayUnavailable = true
			a.Log().Info("tray unavailable (继续运行)", "err", err.Error())
		} else {
			// 必须保留强引用：见 appTray。
			keepTray(tray)
		}
		openWindow(u, &win)
		go func() {
			t := time.NewTicker(time.Second)
			defer t.Stop()
			for {
				select {
				case <-coolStop:
					return
				case now := <-t.C:
					if len(a.Cooldowns().Snapshot(now)) == 0 {
						continue // 无冷却不重绘，保持空闲 0%
					}
					if w := win.Load(); w != nil {
						w.Update(func() {})
					}
				}
			}
		}()
		// 异步任务完成后回主线程重绘：Window.Update 在校验窗口存活后把
		// 回调排进 GTK 主循环；无窗口时直接执行（测试/退出边缘）。
		u.Dispatch = func(fn func()) {
			if w := win.Load(); w != nil {
				w.Update(fn)
			} else {
				fn()
			}
		}
		a.OnRecord(func(rec telemetry.RequestRecord) {
			alertID, alertTitle, alertBody := "", "", ""
			if u.Svc.Gateway.AlertsEnabled() {
				alertID, alertTitle, alertBody = u.alerts.evaluate(rec, time.Now())
			}
			if alertID != "" && mygo.NotificationsSupported() {
				go func() {
					_ = mygo.NewNotification(mygo.NotificationOptions{
						ID: "agentswitch-" + alertID, Title: alertTitle, Body: alertBody, Group: "AgentSwitch",
					}).Show()
				}()
			}
			if w := win.Load(); w != nil {
				w.Update(func() {
					if alertID != "" {
						u.AlertMsg = alertTitle + "：" + alertBody
					}
					// 新请求到达后让「用量」卡片下一帧重新拉取；否则用户先打开
					// 过设置页、再发请求时，图表会一直停留在旧（可能为空）数据。
					u.Settings.UsageLoaded = false
					u.Recent = append([]telemetry.RequestRecord{rec}, u.Recent...)
					if len(u.Recent) > 100 {
						u.Recent = u.Recent[:100]
					}
				})
			}
		})
		// 网关自动启动（I6 裁定，spec §5 字面：应用启动即运行）：注册完状态/
		// 记录回调再启动，首帧状态变化不丢。失败只记日志——状态机已进 Failed，
		// 由概览页（组 4 已放行 failed 态启动按钮）呈现与重试；端口被占等场景
		// 不必静默死在后台。
		if err := svc.Gateway.Start(); err != nil {
			a.Log().Info("gateway auto-start failed", "err", err.Error())
		}
	})

	// 关窗不退出：最后一个窗口关闭时保持运行（托盘驻留）。正常关窗走
	// Hide，不走销毁；本监听仍保留作兜底（例如 WM 强制销毁时）。
	mygo.App.OnWindowAllClosed(func() { /* 故意为空 */ })

	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		// 首关教育只针对交互关闭（点 X）。mygo prepareQuit 先跑本钩子
		// （app.go:184-187），随后 a.quitting=true 并逐窗 w.close()
		// （app.go:189-194）——任一 OnClose PreventDefault 即复位 quitting
		// 中止退出。若不在第一行放行，托盘 RoleQuit/Ctrl+Q 在"窗口开着且
		// 未确认过教育提示"（最常见态）下：清理已做、退出被拦、UI 停在
		// 已关闭 DB 的尸体上。
		u.Quitting = true
		u.CloseConfirmed = true
		coolStopOnce.Do(func() { close(coolStop) })
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

// openWindow 建主窗、登记进 win 并挂关窗拦截。OnClose 挂在这里而非仅挂
// 首窗：托盘重开的每个窗口都自带拦截，不依赖"销毁过 ⇒ closeConfirmed"
// 这条跨模块不变式。
//
// 交互关窗一律 PreventDefault 后 Hide：Linux 上在 WebView 点击回调里同步
// 销毁窗口会让 GTK 在事件派发未结束时释放窗口对象（实测 SIGSEGV：
// gdk_window_get_display assertion + Backend.Run 中崩溃），整个进程与网关
// 一起消失。隐藏既绕开析构路径，也让托盘唤回保留界面状态。
func openWindow(u *uiState, win *atomic.Pointer[mygo.Window]) *mygo.Window {
	w := mygo.NewWindow(mygo.WindowOptions{
		Title: "AgentSwitch", Width: 1080, Height: 720, MinWidth: 900, MinHeight: 600, StateKey: "main",
		Content: ui.View(u.view),
	})
	// 窗口/任务栏图标（Linux、Windows）；macOS 窗口无自身图标，由打包
	// 的 .car 决定。失败只影响观感，不阻断窗口。
	_ = w.SetIcon(appIcon)
	win.Store(w)
	u.HideWindow = w.Hide
	w.OnClose(func(e *mygo.CloseEvent) {
		if u.Quitting {
			return
		}
		e.PreventDefault()
		u.requestClose(w.Hide, func() { w.Update(func() {}) })
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
