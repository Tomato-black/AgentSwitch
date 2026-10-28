// closemodal.go 是首关驻留模态：点窗口 X 不再弹出内容区横条，而是显示
// 居中的自绘对话框（DialogBase + 设计系统 token），确认后隐藏窗口而非
// 销毁——网关继续运行，托盘可随时唤回。
package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/uikit"
)

// closeModal 渲染首关驻留模态。DialogBase 自带 Esc/点遮罩关闭，会把
// u.CloseNotice 置 false（等同取消）。
func (u *uiState) closeModal(c *ui.Context) {
	t := c.Theme()
	s := uikit.Softs(t)
	ui.DialogBase(c, &u.CloseNotice, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.45))
		panel.Width(420).Radius(14).Background(t.Surface).Border(1, t.Border).
			Padding(t.Space(6)).Children(func() {
			ui.Column(c).Gap(t.Space(4)).Children(func() {
				ui.Row(c).Gap(t.Space(3.5)).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Size(40, 40).Radius(10).Background(s.Act).Center().Children(func() {
						ui.Icon(c, uikit.IconTray).FontSize(20).TextColor(s.ActText)
					})
					ui.Column(c).Gap(2).Grow(1).Children(func() {
						ui.Text(c, "网关继续运行").Bold().FontSize(t.FontSize + 2)
						ui.Text(c, "窗口将驻留系统托盘").TextColor(t.TextMuted).
							FontSize(t.FontSize - 1)
					})
				})
				// 拆成两行，避免 CJK 自动换行把「窗口」拆在行首行尾。
				ui.Column(c).Gap(2).Children(func() {
					ui.Text(c, "关闭窗口不会中断 Agent 请求。").TextColor(t.TextMuted)
					ui.Text(c, "可从系统托盘重新打开窗口，或在托盘中退出应用。").
						TextColor(t.TextMuted)
				})
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					ui.Box(c).Grow(1)
					if uikit.SoftButton(c, t, s, "gray", "取消").Clicked() {
						u.CloseNotice = false
					}
					if uikit.PillButton(c, t, "最小化到托盘").Clicked() {
						u.minimizeToTray()
					}
				})
			})
		})
	})
}

// minimizeToTray 是模态的确认动作：收起模态、记录已确认，然后隐藏窗口。
// 隐藏必须推迟出当前 GTK 事件回调（见 hideAfterFrame）：在按钮点击回调里
// 同步 Hide 会让 MyGo 的 GPU Presenter 在已失效的 GL 表面上提交帧，实测
// `epoxy_get_proc_address: Couldn't find current GLX or EGL context` → SIGABRT。
func (u *uiState) minimizeToTray() {
	u.CloseNotice = false
	u.CloseConfirmed = true
	if u.HideWindow != nil {
		u.Schedule(u.HideWindow)
	}
}

// afterEvent 把窗口操作投递到当前事件派发之后：timer goroutine 调
// Window.Hide/Show 时 MyGo 会把动作排队回主线程，主循环处理完当前 GTK
// 事件后才执行。在事件回调里同步 Hide/Show 会让 GPU Presenter 在失效的
// GL 表面上提交帧（实测 SIGABRT），Show 被托盘菜单点击触发时同理。
func afterEvent(fn func()) {
	time.AfterFunc(0, fn)
}

// requestClose 处理交互关窗：首次只弹模态；用户确认过（closeConfirmed）
// 之后的每次关窗直接隐藏。redraw 用于让首关模态立刻上屏。隐藏同样要
// 推迟出关窗事件（OnClose 在 GTK delete-event 派发中执行）。
func (u *uiState) requestClose(hide, redraw func()) {
	if u.CloseConfirmed {
		u.Schedule(hide)
		return
	}
	u.CloseNotice = true
	redraw()
}
