// drawer.go 是 uikit 的右侧抽屉容器（原型 docs/prototypes/
// claude-code-config-page.html §接入抽屉：右侧 560、带遮罩、Esc/遮罩点击
// 关闭、正文滚动）。基于 ui.Overlay + 绝对定位实现；关闭只翻 *open，不触碰
// 调用方状态——「关闭保留草稿」等语义由调用方承载。
package uikit

import (
	"github.com/egoist/mygo/ui"
)

// Drawer 渲染右侧抽屉：*open 为假时零渲染。标题行固定（title + 可选
// subtitle + 「关闭」软底钮），body 在 Scroll 内滚动；窗口宽不足时抽屉
// 收窄到窗宽-48（不小于 280）。Esc、遮罩点击、「关闭」三者等价关闭。
func Drawer(c *ui.Context, t *ui.Theme, open *bool, title, subtitle string, body func()) {
	if open == nil || !*open {
		return
	}
	s := Softs(t)
	w, _ := c.Size()
	width := w - 48
	if width > 560 {
		width = 560
	}
	if width < 280 {
		width = 280
	}
	ui.Overlay(c, func() {
		// 遮罩铺满窗口，点击即关。
		back := ui.ButtonBase(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
			Background(ui.RGBA(24, 24, 27, 0.28))
		if back.Clicked() {
			*open = false
		}
		// 面板贴右缘、全高；Esc 关闭与 DialogBase 同款快捷键通道。
		panel := ui.Box(c).Absolute().Top(0).Right(0).Bottom(0).Width(width).
			Background(t.Surface).Border(1, t.Border)
		if panel.OverlayShortcut(0, ui.KeyEscape) {
			*open = false
		}
		panel.Children(func() {
			ui.Column(c).Gap(t.Space(3)).Padding(t.Space(4)).Fill().Children(func() {
				ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Center).Children(func() {
					ui.Text(c, title).Bold().FontSize(t.FontSize + 2)
					ui.Box(c).Grow(1)
					if SoftButton(c, t, s, "gray", "关闭").Clicked() {
						*open = false
					}
				})
				if subtitle != "" {
					ui.Text(c, subtitle).TextColor(t.TextMuted)
				}
				ui.Scroll(c).Grow(1).Children(body)
			})
		})
	})
}
