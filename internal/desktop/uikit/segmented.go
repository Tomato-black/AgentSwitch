// segmented.go 是 uikit 的强选中态分段控件：基于 ui.SegmentedBase 自绘
// （框架主题版 Segmented 的选中面在浅色模式下取 t.Background，与本主题
// t.Surface 同为 #FFFFFF——白上加白不可见，2026-11-07 用户实测反馈）。
// 选中段 = 软橙底（SoftPalette act 组）+ 粗体 + 深字；轨道 = SurfaceHover。
package uikit

import (
	"github.com/egoist/mygo/ui"
)

// Segmented 渲染等宽分段选择器（radio 语义与 ui.Segmented 一致：点击/方向键
// 选择，Changed 在选中变化的那帧为真）。选中段视觉明显强于未选中段。
func Segmented(c *ui.Context, t *ui.Theme, s SoftPalette, selected *int, labels ...string) ui.Element {
	parts := ui.SegmentedBase(c, selected, len(labels))
	track := parts.Track
	track.AlignItems(ui.Stretch).Padding(t.Space(0.5)).Gap(t.Space(0.5)).
		Radius(t.Radius).Background(t.SurfaceHover).Border(1, t.Border)
	track.Children(func() {
		for i, label := range labels {
			seg := parts.Segment(i).Grow(1).Center()
			if i == *selected {
				seg.Radius(t.Radius - 2).Background(s.Act).TextColor(s.ActText)
				seg.Children(func() { ui.Text(c, label).FontWeight(600).SingleLine() })
			} else {
				seg.TextColor(t.TextMuted)
				seg.Children(func() { ui.Text(c, label).SingleLine() })
			}
		}
	})
	return track
}
