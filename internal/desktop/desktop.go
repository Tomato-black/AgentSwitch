// Package desktop 是 MyGo 桌面壳：窗口、托盘、关窗拦截、退出钩子与状态桥接。
// UI 页面（概览/渠道/模型）挂在本包视图上。
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
	Gateway  bridge.Gateway
	Channels bridge.Channels
}

type uiState struct {
	svc            Services
	status         app.StateInfo
	recent         []telemetry.RequestRecord
	token          string
	logPath        string       // 日志文件路径（a.LogPath()；概览恒显的"日志:"提示）
	page           string       // 当前页；"overview" 默认（自绘侧栏选中 id，经 navSel 归一）
	showToken      bool         // 令牌明文显示开关
	portInput      string       // 设置卡端口输入（为空时回显 Status.Port）
	timeoutInput   string       // 设置卡超时输入（为空时回显 Status.TimeoutSeconds）
	settingsMsg    string       // 设置保存结果提示
	confirmRegen   bool         // 重新生成令牌的 AlertDialog open 位（选定后框架自关）
	table          ui.ListState // 最近请求表状态
	closeNotice    bool         // 首关教育提示
	closeConfirmed bool         // 用户已知晓，允许关闭
	// 模型聚合页（Task 9 重写：跨渠道只读 + 行内启停）：
	modelList ui.ListState // 聚合表状态；Selected 经 modelSel 指向当前行
	modelSel  int          // 聚合表当前行（-1=无）
	// 渠道页（Task 5 卡片列表；Task 6/7 接详情/新建）：
	chList             ui.ListState // 渠道表状态（旧表态；Task 6/7 迁移期保留）
	chSel              int          // 渠道表当前行（-1=无）；详情三段面板锚定行选中
	chDetailID         string       // 详情页锚定渠道 id（openChannelDetail 落值）
	chDetailSeg        int          // 详情页分段选中（0=连接配置 1=密钥池 2=渠道模型）
	chDeleteArm        bool         // 删除渠道 AlertDialog open 位
	chDeleteID         string       // 删除武装对象——弹窗仅对匹配卡触发
	chForm             chFormData   // 连接配置表单；editID 非空 = 编辑该渠道
	chConfirmDelete    bool         // 渠道删除二次确认（两段式，旧表单态）
	chTestMsg          string       // 渠道/密钥连通测试结果展示
	keyForm            keyFormData  // 密钥批量导入表单
	keyFormOpen        bool         // 密钥池段「＋ 批量导入」表单展开位
	keyConfirmDelete   bool         // 密钥删除二次确认（两段式）
	chModelForm        channelModelFormData
	chModelConfirmDel  bool   // 渠道模型删除二次确认（两段式，审查 Finding 1）
	chTypeResetConfirm bool   // 详情态换类型确认（AlertDialog open 位：Base URL 将重置）
	prevType           string // 类型 Select 变更检测（预填 BaseURL / 详情态换类型确认）
	confirmOwnerID     string // 确认位武装时的渠道 id——确认钮仅对本渠道详情渲染（审查 Finding 2）
	prevDetailID       string // 详情渠道变更检测：变更即回显表单并清子选中与确认位（审查 Finding 2）
	chKeyList          ui.ListState
	chModelList        ui.ListState
	chKeySel           int
	chModelSel         int
}

// chFormData 是渠道页连接配置表单；editID 非空 = 编辑该渠道。
// typeID 存预设 id（初始化/载入/测试直赋），Select 交互后短暂持有 label
// ——提交/预填/描述统一经 resolveTypeID 解析回 id。
type chFormData struct {
	typeID, name, baseURL, priorityInput string
	enabled, allowPrivate                bool
	msg, editID                          string
}

// keyFormData 是密钥批量导入表单；keysText 每行一枚（TextArea 多行输入）。
type keyFormData struct {
	name, keysText string
	allowFallback  bool
	msg            string
}

// channelModelFormData 是渠道模型添加表单。
type channelModelFormData struct {
	public, upstream, msg string
}

func newUIState(svc Services, a *app.App) *uiState {
	si := a.State()
	st, _ := a.CurrentSettings()
	u := &uiState{svc: svc, status: si, recent: a.Recent(), token: st.GatewayToken,
		logPath:  a.LogPath(),
		page:     "overview",
		chForm:   chFormData{typeID: "anthropic-official", baseURL: "https://api.anthropic.com", enabled: true, priorityInput: "10"},
		keyForm:  keyFormData{},
		modelSel: -1,
		chSel:    -1, chKeySel: -1, chModelSel: -1}
	// 表格行选择：Selected 必须非 nil 点击才选行（mygo ui.ListState.Selected
	// 注释：*Selected 是所选行，-1 为无），这里指向状态字段（-1=无），
	// "载入所选/停用·启用/测试"才落得到所选行。
	u.modelList.Selected = &u.modelSel
	u.chList.Selected = &u.chSel
	u.chKeyList.Selected = &u.chKeySel
	u.chModelList.Selected = &u.chModelSel
	return u
}

// navItems 是侧栏导航的三项：id 即 u.page 的分发 id（pageView 消费），
// label 供渲染与测试点击。
var navItems = []struct {
	id, label string
	icon      *ui.SVG
}{
	{"overview", "概览", iconOverview},
	{"channels", "渠道", iconChannels},
	{"models", "模型", iconModels},
}

// view 是窗口根：按系统外观选品牌主题，左列自绘侧栏（品牌行 + 导航 +
// 页脚全局状态位）、右内容列（首关横条 + 当前页）。SetTheme 必须在任何
// 子件构建前调用（api_gen.go：SetTheme makes the frame use t）。导航点击
// 直接 u.page = id（三个 id 与 pageView 分发一致），点选即切页，无需
// Changed 处理；选中项经 navSel 归一（渠道详情/新建页点亮「渠道」项）。
// 侧栏弃用 ui.Sidebar：其选中项硬编码 Accent 底（sidebar.go:188），与
// cc-switch 的中性灰选中冲突（spec §3.4），自绘列底色用 sidebarColor。
func (u *uiState) view(c *ui.Context) {
	c.SetTheme(buildTheme(c.Theme().Dark, c.Preferences().TextScale))
	t := c.Theme()
	ui.Row(c).Gap(0).Grow(1).Children(func() {
		// 左列：自绘侧栏（spec §5.1）——品牌行、导航、底部页脚状态位
		// （状态是主角：任何页可见）；spacer 把页脚钉在栏底（原
		// Sidebar.Grow(1) 的布局效果）。
		ui.Column(c).Gap(0).Width(200).Grow(1).Background(sidebarColor(t)).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Padding(t.Space(3), t.Space(2.5)).
				Children(func() { ui.Text(c, "AgentSwitch").Bold().FontSize(14) })
			ui.Column(c).Gap(2).Padding(t.Space(1), t.Space(2)).Children(func() {
				for _, it := range navItems {
					u.navItem(c, t, it.id, it.label, it.icon)
				}
			})
			ui.Box(c).Grow(1)
			u.sidebarFooter(c, t)
		})
		// 右列：首关横条（全页可见，I4）+ 当前页
		ui.Column(c).Gap(12).Padding(16).Grow(1).Children(func() {
			if u.closeNotice && !u.closeConfirmed {
				card(c, t, func() {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "窗口关闭后应用将驻留托盘，网关继续运行。").Grow(1)
						if pillButton(c, t, "继续关闭窗口").Clicked() {
							u.closeConfirmed = true
							if w := mygo.FocusedWindow(); w != nil {
								w.Close()
							}
						}
						if ui.Button(c, "取消").Clicked() {
							u.closeNotice = false
						}
					})
				})
			}
			u.pageView(c)
		})
	})
}

// navSel 把 u.page 归一成侧栏导航的选中项：渠道详情/新建两页（Task 6/7
// 各自追加进 pageView 分发）都点亮「渠道」项，其余页 id 原样（与三个
// 导航 id 一一对应）。
func (u *uiState) navSel() string {
	switch u.page {
	case "channel-detail", "channel-new":
		return "channels"
	default:
		return u.page
	}
}

// navItem 是一枚自绘导航项：ButtonBase 底（可聚焦，指针/Enter/Space 报
// Clicked），高 30、Radius 8；选中底 = 应用级选中灰 selectedColor（与
// 品牌橙无关，且选中项 hover 不换底——灰底不漂移），未选中 hover 用
// SurfaceHover；激活项图标着 Accent，其余继承正文色（spec §3.4/§5.1）。
// 点击直接落 u.page。
func (u *uiState) navItem(c *ui.Context, t *ui.Theme, id, label string, icon *ui.SVG) {
	b := ui.ButtonBase(c)
	on := u.navSel() == id
	bg := ui.Color{}
	if on {
		bg = selectedColor(t)
	} else if b.Hovered() {
		bg = t.SurfaceHover
	}
	b.Height(30).Radius(8).Background(bg).Gap(t.Space(2)).
		Padding(0, t.Space(2)).Children(func() {
		ic := ui.Icon(c, icon).FontSize(16)
		if on {
			ic.TextColor(t.Accent)
		}
		ui.Text(c, label).Grow(1).SingleLine()
	})
	if b.Clicked() {
		u.page = id
	}
}

// pageView 按 u.page 分发页面；未识别的 page 落到概览。
func (u *uiState) pageView(c *ui.Context) {
	switch u.page {
	case "channels":
		u.channelsView(c)
	case "channel-detail":
		u.channelDetailView(c)
	case "models":
		u.modelsView(c)
	default:
		u.overviewView(c)
	}
}

// sidebarFooter 是边栏页脚的全局状态位：LED + 状态词 + 等宽地址。
// 地址空（未启动过）显示「未监听」。
func (u *uiState) sidebarFooter(c *ui.Context, t *ui.Theme) {
	state := string(u.status.State)
	ui.Row(c).Gap(8).Padding(12).Children(func() {
		ledDot(c, t, state)
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, stateWord(state))
			addr := u.status.Addr
			if addr == "" {
				addr = "未监听"
			}
			ui.Text(c, addr).Font(monoFont).TextColor(t.TextMuted)
		})
	})
}

func Run(a *app.App) error {
	svc := Services{
		Gateway:  bridge.Gateway{A: a},
		Channels: bridge.Channels{A: a, Creds: a.Creds()},
	}
	mygo.Bind(svc.Gateway)
	mygo.Bind(svc.Channels)

	// 单实例锁已在入口（cmd/agentswitch/main.go）取——那里的抢锁失败在
	// app.Open 之前返回，不泄漏库句柄、不留并发迁移窗口；此处不得再取锁。
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
		// 网关自动启动（I6 裁定，spec §5 字面：应用启动即运行）：注册完状态/
		// 记录回调再启动，首帧状态变化不丢。失败只记日志——状态机已进 Failed，
		// 由概览页（组 4 已放行 failed 态启动按钮）呈现与重试；端口被占等场景
		// 不必静默死在后台。
		if err := a.StartGateway(); err != nil {
			a.Log().Info("gateway auto-start failed", "err", err.Error())
		}
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
