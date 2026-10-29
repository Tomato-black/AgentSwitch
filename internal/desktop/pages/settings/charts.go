package settings

import (
	"fmt"
	"sort"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/uikit"
)

type channelTokens struct {
	Name   string
	Tokens int64
}

// aggregateChannelTokens 把按渠道·模型的行聚合成按渠道，降序；最多 8 个。
func aggregateChannelTokens(rows []bridge.UsageRow) []channelTokens {
	by := map[string]int64{}
	for _, r := range rows {
		by[r.Channel] += r.InputTokens + r.OutputTokens
	}
	out := make([]channelTokens, 0, len(by))
	for name, n := range by {
		out = append(out, channelTokens{Name: name, Tokens: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tokens != out[j].Tokens {
			return out[i].Tokens > out[j].Tokens
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > 8 {
		out = out[:8]
	}
	return out
}

type durationSeries struct {
	Name   string
	Points []bridge.DurationPoint
}

// durationSeriesByChannel 按渠道分组并按点数降序，最多 maxSeries 条。
func durationSeriesByChannel(points []bridge.DurationPoint, maxSeries int) []durationSeries {
	by := map[string][]bridge.DurationPoint{}
	for _, p := range points {
		by[p.Channel] = append(by[p.Channel], p)
	}
	out := make([]durationSeries, 0, len(by))
	for name, ps := range by {
		sort.Slice(ps, func(i, j int) bool { return ps[i].StartedAt.Before(ps[j].StartedAt) })
		out = append(out, durationSeries{Name: name, Points: ps})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Points) != len(out[j].Points) {
			return len(out[i].Points) > len(out[j].Points)
		}
		return out[i].Name < out[j].Name
	})
	if len(out) > maxSeries {
		out = out[:maxSeries]
	}
	return out
}

// usageChartNames 收集两张图出现的全部渠道名，供共享配色；顺序无关
// （uikit.ChartSlots 会按名称排序），同一渠道在两图中因此同色。
func usageChartNames(rows []bridge.UsageRow, points []bridge.DurationPoint) []string {
	names := make([]string, 0, len(rows)+len(points))
	for _, r := range rows {
		names = append(names, r.Channel)
	}
	for _, p := range points {
		names = append(names, p.Channel)
	}
	return names
}

// seriesColor 取渠道对应的分类色；slots 缺项时回落首槽（正常不会发生）。
func seriesColor(t *ui.Theme, slots map[string]ui.Color, name string) ui.Color {
	if c, ok := slots[name]; ok {
		return c
	}
	return uikit.ChartColors(t)[0]
}

func shortTokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		return fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}

// tokenBarChart 画按渠道的 token 柱状图（输入+输出）。slots 为空时按本图
// 渠道自建配色（测试直接调用时走这条路径）。
func tokenBarChart(c *ui.Context, t *ui.Theme, rows []bridge.UsageRow, slots map[string]ui.Color) {
	ui.Text(c, "Token 用量（按渠道）").Bold()
	data := aggregateChannelTokens(rows)
	if len(data) == 0 {
		ui.Text(c, "还没有 token 数据").TextColor(t.TextMuted)
		return
	}
	max := data[0].Tokens
	if max <= 0 {
		ui.Text(c, "还没有 token 数据").TextColor(t.TextMuted)
		return
	}
	if slots == nil {
		names := make([]string, len(data))
		for i, d := range data {
			names[i] = d.Name
		}
		slots = uikit.ChartSlots(t, names)
	}
	axis, grid, baseline := uikit.ChartAxis(t), uikit.ChartGrid(t), uikit.ChartBaseline(t)
	ui.Box(c).Height(160).FillWidth().Draw(func(p *ui.Painter, r ui.Rect) {
		left, right, top, bottom := float32(8), float32(8), float32(14), float32(32)
		plotW, plotH := r.W-left-right, r.H-top-bottom
		if plotW <= 0 || plotH <= 0 {
			return
		}
		p.Clip(r, 0, func() {
			x0, y0 := r.X+left, r.Y+top
			for i := 0; i <= 4; i++ {
				y := y0 + plotH*float32(i)/4
				p.Line(x0, y, x0+plotW, y, 1, grid)
			}
			p.Line(x0, y0+plotH, x0+plotW, y0+plotH, 1, baseline)
			bw := plotW / float32(len(data))
			for i, d := range data {
				h := plotH * float32(d.Tokens) / float32(max)
				w := bw * 0.56
				if w > 64 {
					w = 64
				}
				if w < 4 {
					w = 4
				}
				x := x0 + bw*float32(i) + (bw-w)/2
				barTop := y0 + plotH - h
				col := seriesColor(t, slots, d.Name)
				p.Fill(ui.Rect{X: x, Y: barTop, W: w, H: h}, col, 4)

				name := truncateRunes(d.Name, 7)
				nameW, _ := p.MeasureText(0, ui.Span{Text: name, Size: 9})
				p.Text(x+w/2-nameW/2, r.Y+r.H-24, name, 9, axis)

				valueY := barTop - 14
				valueColor := axis
				if valueY < y0 {
					valueY = barTop + 4
					valueColor = uikit.ChartInkOn(col)
				}
				value := shortTokens(d.Tokens)
				valueW, _ := p.MeasureText(0, ui.Span{Text: value, Size: 9})
				p.Text(x+w/2-valueW/2, valueY, value, 9, valueColor)
			}
		})
	})
}

// durationLineChart 画按渠道的响应时长折线图（最近 1 天，Top 5 渠道）。
// slots 为空时按本图系列自建配色（测试直接调用时走这条路径）。
func durationLineChart(c *ui.Context, t *ui.Theme, points []bridge.DurationPoint, slots map[string]ui.Color) {
	ui.Text(c, "响应时长（按渠道）").Bold()
	series := durationSeriesByChannel(points, 5)
	if len(series) == 0 {
		ui.Text(c, "还没有请求耗时数据").TextColor(t.TextMuted)
		return
	}
	if slots == nil {
		names := make([]string, len(series))
		for i, s := range series {
			names[i] = s.Name
		}
		slots = uikit.ChartSlots(t, names)
	}
	axis, grid, baseline := uikit.ChartAxis(t), uikit.ChartGrid(t), uikit.ChartBaseline(t)
	// 图例：色块承载身份，文字保持中性墨色（不让文字染系列色）。
	ui.Row(c).Gap(14).Wrap().AlignItems(ui.Center).Children(func() {
		for _, s := range series {
			col := seriesColor(t, slots, s.Name)
			ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Width(12).Height(3).Radius(1.5).Background(col).Shrink(0)
				ui.Text(c, s.Name).FontSize(t.FontSize - 2).TextColor(axis).SingleLine()
			})
		}
	})
	var minT, maxT time.Time
	var maxDur int64 = 1
	for _, s := range series {
		for _, p := range s.Points {
			if minT.IsZero() || p.StartedAt.Before(minT) {
				minT = p.StartedAt
			}
			if maxT.IsZero() || p.StartedAt.After(maxT) {
				maxT = p.StartedAt
			}
			if p.DurationMs > maxDur {
				maxDur = p.DurationMs
			}
		}
	}
	ui.Box(c).Height(170).FillWidth().Draw(func(p *ui.Painter, r ui.Rect) {
		left, right, top, bottom := float32(44), float32(12), float32(12), float32(26)
		plotW, plotH := r.W-left-right, r.H-top-bottom
		if plotW <= 0 || plotH <= 0 {
			return
		}
		p.Clip(r, 0, func() {
			x0, y0 := r.X+left, r.Y+top
			for i := 0; i <= 4; i++ {
				y := y0 + plotH*float32(i)/4
				p.Line(x0, y, x0+plotW, y, 1, grid)
				label := fmt.Sprintf("%dms", maxDur*int64(4-i)/4)
				p.Text(r.X+2, y+4, label, 9, axis)
			}
			p.Line(x0, y0+plotH, x0+plotW, y0+plotH, 1, baseline)
			span := float32(maxT.Sub(minT).Milliseconds())
			if span <= 0 {
				span = 1
			}
			xOf := func(at time.Time) float32 {
				if maxT.Equal(minT) {
					return x0 + plotW/2
				}
				return x0 + plotW*float32(at.Sub(minT).Milliseconds())/span
			}
			yOf := func(ms int64) float32 {
				return y0 + plotH*(1-float32(ms)/float32(maxDur))
			}
			p.Text(x0, r.Y+r.H-18, minT.Format("15:04"), 9, axis)
			p.Text(x0+plotW-34, r.Y+r.H-18, maxT.Format("15:04"), 9, axis)
			// 先画全部折线，再画数据点：点带卡片底色描边，压在线上更清晰。
			for _, s := range series {
				col := seriesColor(t, slots, s.Name)
				for j := 1; j < len(s.Points); j++ {
					a, b := s.Points[j-1], s.Points[j]
					p.Line(xOf(a.StartedAt), yOf(a.DurationMs), xOf(b.StartedAt), yOf(b.DurationMs), 2, col)
				}
			}
			for _, s := range series {
				col := seriesColor(t, slots, s.Name)
				for _, pt := range s.Points {
					x, y := xOf(pt.StartedAt), yOf(pt.DurationMs)
					p.Fill(ui.Rect{X: x - 3.5, Y: y - 3.5, W: 7, H: 7}, t.Surface, 4)
					p.Fill(ui.Rect{X: x - 2.5, Y: y - 2.5, W: 5, H: 5}, col, 3)
				}
			}
		})
	})
}

// dailyUsageChart 画最近 7 天的每日 token 柱状图（单色，避免与渠道分类色混淆）。
func dailyUsageChart(c *ui.Context, t *ui.Theme, rows []bridge.DailyUsageRow) {
	ui.Text(c, "每日 Token（近 7 天）").Bold()
	if len(rows) == 0 {
		ui.Text(c, "还没有每日用量数据").TextColor(t.TextMuted)
		return
	}
	var max int64
	for _, r := range rows {
		if n := r.InputTokens + r.OutputTokens; n > max {
			max = n
		}
	}
	if max <= 0 {
		ui.Text(c, "近 7 天暂无 token 记录").TextColor(t.TextMuted)
		return
	}
	col := uikit.ChartColors(t)[2]
	axis, grid, base := uikit.ChartAxis(t), uikit.ChartGrid(t), uikit.ChartBaseline(t)
	ui.Box(c).Height(140).FillWidth().Draw(func(p *ui.Painter, r ui.Rect) {
		left, right, top, bottom := float32(8), float32(8), float32(12), float32(26)
		plotW, plotH := r.W-left-right, r.H-top-bottom
		if plotW <= 0 || plotH <= 0 {
			return
		}
		p.Clip(r, 0, func() {
			x0, y0 := r.X+left, r.Y+top
			for i := 0; i <= 4; i++ {
				y := y0 + plotH*float32(i)/4
				p.Line(x0, y, x0+plotW, y, 1, grid)
			}
			p.Line(x0, y0+plotH, x0+plotW, y0+plotH, 1, base)
			bw := plotW / float32(len(rows))
			for i, row := range rows {
				total := row.InputTokens + row.OutputTokens
				h := plotH * float32(total) / float32(max)
				w := bw * 0.5
				if w > 28 {
					w = 28
				}
				if w < 4 {
					w = 4
				}
				x := x0 + bw*float32(i) + (bw-w)/2
				barTop := y0 + plotH - h
				p.Fill(ui.Rect{X: x, Y: barTop, W: w, H: h}, col, 3)
				label := row.Day
				if len(label) >= 5 {
					label = label[5:]
				}
				lw, _ := p.MeasureText(0, ui.Span{Text: label, Size: 9})
				p.Text(x+w/2-lw/2, r.Y+r.H-22, label, 9, axis)
				if total > 0 {
					value := shortTokens(total)
					vw, _ := p.MeasureText(0, ui.Span{Text: value, Size: 9})
					vy := barTop - 13
					if vy < y0 {
						vy = barTop + 3
					}
					p.Text(x+w/2-vw/2, vy, value, 9, axis)
				}
			}
		})
	})
}
