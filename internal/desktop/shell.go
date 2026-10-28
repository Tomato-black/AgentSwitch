// shell.go 是 cc-switch 式桌面壳：侧栏导航（逻辑模型/渠道/接入/设置）与
// 侧栏底条网关状态（LED + 状态词 + 启停钮）。spec：docs/superpowers/specs/
// 2026-10-17-logical-models-ccswitch-ui-design.md §6.1。
// 页面内容（逻辑模型列表/渠道/接入/设置）由后续 Task 填充——本文件只做壳，
// pageView 分发到各页占位视图（占位必须在 Task 12–16 清空）。
package desktop

import (
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/pages/channels"
	"AgentSwitch/internal/desktop/pages/logical"
	"AgentSwitch/internal/desktop/pages/settings"
	"AgentSwitch/internal/desktop/uikit"
)

// navItems 是侧栏导航的四项：id 即 uiState.page 的分发 id（pageView 消费），
// label 供渲染与测试点击。顺序即展示序（spec §6.1：逻辑模型/渠道/接入/设置）。
var navItems = []struct {
	id, label string
	icon      *ui.SVG
}{
	{"logical", "逻辑模型", uikit.IconLogical},
	{"channels", "渠道", uikit.IconChannels},
	{"access", "接入", uikit.IconAccess},
	{"settings", "设置", uikit.IconSettings},
}

// normalizedPage 把 u.Page 归一成当前页 id：零值（""）落逻辑模型——默认页 =
// 逻辑模型列表（spec §6.1）。已知 id 原样返回，未知 id 由 pageView 的
// default 分支兜底到逻辑模型页。
func (u *uiState) normalizedPage() string {
	if u.Page == "" {
		return "logical"
	}
	return u.Page
}

// navItem 是一枚自绘导航项：ButtonBase 底（可聚焦，指针/Enter/Space 报
// Clicked），高 30、Radius 8；选中底 = 应用级选中灰 selectedColor（与
// 品牌橙无关，且选中项 hover 不换底——灰底不漂移），未选中 hover 用
// SurfaceHover；激活项图标着 Accent，其余继承正文色（spec §3.4/§5.1）。
// 点击直接落 u.Page。
func (u *uiState) navItem(c *ui.Context, t *ui.Theme, id, label string, icon *ui.SVG) {
	b := ui.ButtonBase(c)
	on := u.normalizedPage() == id
	bg := ui.Color{}
	if on {
		bg = uikit.SelectedColor(t)
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
		u.Page = id
	}
}

// gatewayStatusBar 是侧栏底条的全局状态位（spec §6.1「侧栏底条：LED+状态+
// 启停」）：LED + 状态词 + 等宽地址 + 启停按钮。数据取 u.Svc.Gateway.Status()
// ——状态真实性不变量：渲染的是网关实际状态，不是桌面按钮状态。启停按钮
// 按实际状态守卫（stopped/failed 才可启动、running/degraded 才可停止，
// spec §8.3 放行集合不变）；点击经 go 异步执行，避免阻塞渲染帧。
func gatewayStatusBar(c *ui.Context, u *uiState) {
	t := c.Theme()
	s := uikit.Softs(t)
	gs := u.Svc.Gateway.Status()
	state := gs.State
	ui.Column(c).Gap(6).Padding(12).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			uikit.LedDot(c, t, state)
			ui.Text(c, uikit.StateWord(state))
		})
		addr := gs.Addr
		if addr == "" {
			addr = "未监听"
		}
		ui.Text(c, addr).Font(uikit.MonoFont).TextColor(t.TextMuted)
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if state == "stopped" || state == "failed" {
				if uikit.PillButton(c, t, "启动网关").Clicked() {
					go func() { _ = u.Svc.Gateway.Start() }()
				}
			}
			if state == "running" || state == "degraded" {
				if uikit.SoftButton(c, t, s, "danger", "停止网关").Clicked() {
					go func() { _ = u.Svc.Gateway.Stop() }()
				}
			}
		})
	})
}

// pageView 按归一化后的 u.Page 分发页面；未识别的 page 落到逻辑模型。
// 本 Task 各页是占位视图（ui.Text TODO），Task 12–15 填充真实内容、
// Task 16 清空占位痕迹。
func (u *uiState) pageView(c *ui.Context) {
	switch u.normalizedPage() {
	case "channels":
		channels.Page(c, u.State)
	case "access":
		u.accessPage(c)
	case "settings":
		settings.Page(c, u.State)
	default:
		logical.Page(c, u.State)
	}
}

// 以下四个 *Page 是页面占位视图（Task 11）：Task 12 填逻辑模型列表、
// Task 14 填渠道页、Task 15 填接入页与设置页。签名即接线契约，不得改名。

// accessPage 的真身在 access.go（接入页）；设置页已迁 pages/settings。
