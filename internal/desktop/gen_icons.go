//go:build ignore

// gen_icons.go 生成 AgentSwitch 的图标资产（4×/2× 超采样后盒式降采样）：
//
//	internal/desktop/tray.png    64px  透明底 + 品牌橙双向箭头（系统托盘）
//	internal/desktop/appicon.png 256px 橙色圆角方块 + 白色箭头（窗口图标）
//	resources/icon.png           256px 同上（mygo build 打包用应用图标）
//
// 应用图标 256 而非 1024：桌面环境按 96px 渲染应用网格，256 已足够；
// GNOME Shell 42 经 St 的 GFileIcon 管线加载超大源图时会静默回退成
// application-x-executable（灰色齿轮）。
//
// 用法（仓库根目录）：
//
//	go run internal/desktop/gen_icons.go
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	must(writePNG("internal/desktop/tray.png", render(64, 4, false)))
	must(writePNG("internal/desktop/appicon.png", render(256, 4, true)))
	must(writePNG("resources/icon.png", render(256, 4, true)))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

var (
	orangeLight = [3]float64{0xFB, 0x92, 0x3C}
	orangeDark  = [3]float64{0xEA, 0x62, 0x0A}
	white       = [3]float64{0xFF, 0xFF, 0xFF}
	glyphOrange = [3]float64{0xF9, 0x73, 0x16}
)

// render 画一枚图标。tile=true 时是应用图标（橙色圆角方块 + 白箭头），
// false 时是透明底托盘图形（橙色箭头）。size 为输出边长，ss 为超采样倍数。
func render(size, ss int, tile bool) *image.NRGBA {
	w := size * ss
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))

	// 箭头在 64 单位坐标系里作画；应用图标把图形缩进到中间的 68%。
	gx, gy, gw := 0.0, 0.0, float64(w)
	if tile {
		gx, gy, gw = 0.16*float64(w), 0.16*float64(w), 0.68*float64(w)
	}

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var ar, ag, ab, aa float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					px := float64(x*ss+sx) + 0.5
					py := float64(y*ss+sy) + 0.5
					r, g, b, a, ok := pixel(px, py, float64(w), tile, gx, gy, gw)
					if !ok {
						continue
					}
					ar += r * a
					ag += g * a
					ab += b * a
					aa += a
				}
			}
			if aa == 0 {
				continue
			}
			n := float64(ss * ss)
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(ar / aa),
				G: uint8(ag / aa),
				B: uint8(ab / aa),
				A: uint8(aa / n * 255),
			})
		}
	}
	return dst
}

// pixel 返回一个超采样点的颜色（0..255）与覆盖 alpha（0 或 1）。
func pixel(px, py, w float64, tile bool, gx, gy, gw float64) (r, g, b, a float64, ok bool) {
	col := [3]float64{}
	covered := false
	if tile {
		if !inRoundedSquare(px, py, w, 0.22*w) {
			return 0, 0, 0, 0, false
		}
		t := (px + py) / (2 * w)
		col = mix(orangeLight, orangeDark, t)
		covered = true
	}
	// 箭头：画布坐标映射回 64 单位图形坐标。
	bx := (px - gx) / gw * 64
	by := (py - gy) / gw * 64
	if inGlyph(bx, by) {
		if tile {
			col = white
		} else {
			col = glyphOrange
		}
		covered = true
	}
	if !covered {
		return 0, 0, 0, 0, false
	}
	return col[0], col[1], col[2], 1, true
}

func inRoundedSquare(x, y, w, r float64) bool {
	if x < 0 || y < 0 || x > w || y > w {
		return false
	}
	cx := math.Min(math.Max(x, r), w-r)
	cy := math.Min(math.Max(y, r), w-r)
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

// inGlyph 判断 64 单位坐标系里的点是否落在双向箭头上。
func inGlyph(x, y float64) bool {
	// 上箭头（向右）：杆、圆头、三角箭头。
	return inRect(x, y, 10, 21, 38, 31) ||
		inCircle(x, y, 10, 26, 5) ||
		inTri(x, y, 36, 14, 59, 26, 36, 38) ||
		// 下箭头（向左）。
		inRect(x, y, 26, 33, 54, 43) ||
		inCircle(x, y, 54, 38, 5) ||
		inTri(x, y, 28, 31, 5, 38, 28, 45)
}

func inRect(x, y, x0, y0, x1, y1 float64) bool {
	return x >= x0 && x <= x1 && y >= y0 && y <= y1
}

func inCircle(x, y, cx, cy, r float64) bool {
	dx, dy := x-cx, y-cy
	return dx*dx+dy*dy <= r*r
}

func inTri(x, y, ax, ay, bx, by, cx, cy float64) bool {
	sign := func(px, py, qx, qy, rx, ry float64) float64 {
		return (px-rx)*(qy-ry) - (qx-rx)*(py-ry)
	}
	orient := sign(ax, ay, bx, by, cx, cy)
	e1 := sign(x, y, ax, ay, bx, by)
	e2 := sign(x, y, bx, by, cx, cy)
	e3 := sign(x, y, cx, cy, ax, ay)
	if orient < 0 {
		return e1 <= 0 && e2 <= 0 && e3 <= 0
	}
	return e1 >= 0 && e2 >= 0 && e3 >= 0
}

func mix(a, b [3]float64, t float64) [3]float64 {
	return [3]float64{
		a[0] + (b[0]-a[0])*t,
		a[1] + (b[1]-a[1])*t,
		a[2] + (b[2]-a[2])*t,
	}
}
