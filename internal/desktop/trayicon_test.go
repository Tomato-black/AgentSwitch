package desktop

import (
	"bytes"
	"image/png"
	"testing"
)

// TestTrayIconIsGlyphNotBlock 防回归：托盘图标必须是透明底的品牌橙图形，
// 而不是整块不透明色块（旧 tray.png 是 16×16 纯深灰方块，在顶栏等于隐形）。
func TestTrayIconIsGlyphNotBlock(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(trayIcon))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() < 32 || b.Dy() < 32 {
		t.Fatalf("tray icon %dx%d too small for HiDPI panels", b.Dx(), b.Dy())
	}
	transparent, orange := false, false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a == 0 {
				transparent = true
				continue
			}
			if r > 0xc800 && g > 0x5000 && g < 0xa000 && bl < 0x4000 {
				orange = true
			}
		}
	}
	if !transparent {
		t.Fatal("tray icon has no transparent pixels: it is a filled block")
	}
	if !orange {
		t.Fatal("tray icon has no brand-orange pixels")
	}
}

// TestAppIconIsBrandTile 防回归：窗口/启动器图标必须是方形、透明圆角、
// 橙色底 + 白色箭头，而不是空白或纯色块。
func TestAppIconIsBrandTile(t *testing.T) {
	img, err := png.Decode(bytes.NewReader(appIcon))
	if err != nil {
		t.Fatal(err)
	}
	b := img.Bounds()
	if b.Dx() != b.Dy() || b.Dx() < 128 {
		t.Fatalf("app icon %dx%d: must be a square >= 128px", b.Dx(), b.Dy())
	}
	transparent, orange, white := false, false, false
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			switch {
			case a == 0:
				transparent = true
			case r > 0xe000 && g > 0xe000 && bl > 0xe000:
				white = true
			case r > 0xc800 && g > 0x5000 && g < 0xa000 && bl < 0x4000:
				orange = true
			}
		}
	}
	if !transparent || !orange || !white {
		t.Fatalf("app icon: transparent=%v orange=%v white=%v", transparent, orange, white)
	}
}
