package settings

import (
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/desktop/uitest"
)

func TestAggregateChannelTokens(t *testing.T) {
	rows := []bridge.UsageRow{
		{Channel: "a", InputTokens: 100, OutputTokens: 20},
		{Channel: "b", InputTokens: 300, OutputTokens: 30},
		{Channel: "a", InputTokens: 10, OutputTokens: 5},
	}
	got := aggregateChannelTokens(rows)
	if len(got) != 2 || got[0].Name != "b" || got[0].Tokens != 330 || got[1].Tokens != 135 {
		t.Fatalf("aggregate = %+v", got)
	}
}

func TestDurationSeriesByChannel(t *testing.T) {
	now := time.Now()
	points := []bridge.DurationPoint{
		{Channel: "a", StartedAt: now, DurationMs: 10},
		{Channel: "b", StartedAt: now.Add(time.Second), DurationMs: 20},
		{Channel: "b", StartedAt: now.Add(2 * time.Second), DurationMs: 30},
	}
	got := durationSeriesByChannel(points, 5)
	if len(got) != 2 || got[0].Name != "b" || len(got[0].Points) != 2 {
		t.Fatalf("series = %+v", got)
	}
}

func TestChartsRender(t *testing.T) {
	now := time.Now()
	rows := []bridge.UsageRow{{Channel: "main", InputTokens: 1000, OutputTokens: 200}}
	points := []bridge.DurationPoint{{Channel: "main", StartedAt: now, DurationMs: 120}}
	_ = ui.NewTester(func(c *ui.Context) {
		tokenBarChart(c, c.Theme(), rows, nil)
		durationLineChart(c, c.Theme(), points, nil)
	}, 800, 700)
}

func TestChartsEmptyRender(t *testing.T) {
	st, _ := uitest.New(t)
	tt := ui.NewTester(func(c *ui.Context) {
		tokenBarChart(c, c.Theme(), nil, nil)
		durationLineChart(c, c.Theme(), nil, nil)
	}, 800, 500)
	if !tt.HasText("还没有 token 数据") || !tt.HasText("还没有请求耗时数据") {
		t.Fatalf("empty chart text missing: %v", tt.Texts())
	}
	_ = st
}

func countNearColor(img interface{ At(x, y int) color.Color }, b image.Rectangle, target ui.Color, tol int) int {
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := img.At(x, y).RGBA()
			dr, dg, db := int(r>>8)-int(target.R), int(g>>8)-int(target.G), int(bl>>8)-int(target.B)
			if abs(dr) <= tol && abs(dg) <= tol && abs(db) <= tol {
				n++
			}
		}
	}
	return n
}

func nearColor(c color.Color, target ui.Color, tol int) bool {
	r, g, bl, _ := c.RGBA()
	return abs(int(r>>8)-int(target.R)) <= tol &&
		abs(int(g>>8)-int(target.G)) <= tol &&
		abs(int(bl>>8)-int(target.B)) <= tol
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func TestTokenBarChartDrawsBars(t *testing.T) {
	theme := uikit.BuildTheme(true, 1)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		tokenBarChart(c, c.Theme(), []bridge.UsageRow{{Channel: "ds", InputTokens: 1000, OutputTokens: 500}}, nil)
	}, 600, 260)
	if n := countNearColor(tt.Image(), tt.Image().Bounds(), uikit.ChartColors(theme)[0], 8); n < 100 {
		t.Fatalf("bar pixels = %d", n)
	}
}

func TestDurationLineChartDraws(t *testing.T) {
	theme := uikit.BuildTheme(true, 1)
	now := time.Now()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		durationLineChart(c, c.Theme(), []bridge.DurationPoint{
			{Channel: "ds", StartedAt: now, DurationMs: 100},
			{Channel: "ds", StartedAt: now.Add(time.Second), DurationMs: 200},
		}, nil)
	}, 600, 280)
	if n := countNearColor(tt.Image(), tt.Image().Bounds(), uikit.ChartColors(theme)[0], 8); n < 5 {
		t.Fatalf("line pixels = %d", n)
	}
}

func TestTokenBarChartUsesAbsoluteElementOffset(t *testing.T) {
	theme := uikit.BuildTheme(true, 1)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		ui.Column(c).Padding(100, 200).Children(func() {
			tokenBarChart(c, c.Theme(), []bridge.UsageRow{{Channel: "ds", InputTokens: 1000, OutputTokens: 500}}, nil)
		})
	}, 700, 500)
	img := tt.Image()
	inOffset := 0
	atOrigin := 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			if !nearColor(img.At(x, y), uikit.ChartColors(theme)[0], 8) {
				continue
			}
			if x >= 200 && y >= 100 {
				inOffset++
			} else {
				atOrigin++
			}
		}
	}
	if inOffset < 100 || atOrigin != 0 {
		t.Fatalf("offset pixels=%d origin pixels=%d", inOffset, atOrigin)
	}
}

func TestDailyUsageChartDraws(t *testing.T) {
	theme := uikit.BuildTheme(true, 1)
	rows := []bridge.DailyUsageRow{
		{Day: "2026-10-27", InputTokens: 100, OutputTokens: 20},
		{Day: "2026-10-28", InputTokens: 300, OutputTokens: 80},
		{Day: "2026-10-29", InputTokens: 200, OutputTokens: 50},
	}
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		dailyUsageChart(c, c.Theme(), rows)
	}, 600, 300)
	if !tt.HasText("每日 Token（近 7 天）") {
		t.Fatalf("title missing")
	}
}
