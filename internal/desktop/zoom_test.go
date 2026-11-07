package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestZoomSteps 覆盖浏览器式缩放档位（mygo v0.7.0 SetZoom，gallery 同款）：
// 表含 1.0、两端夹取、步进与复位语义。
func TestZoomSteps(t *testing.T) {
	one := -1
	for i, z := range zoomSteps {
		if z == 1 {
			one = i
			break
		}
	}
	if one < 0 {
		t.Fatalf("档位表必须含 1.0: %v", zoomSteps)
	}
	// 漂移保护（终审 minor）：1.0 的下标必须与 zoomOne 常量一致——
	// newUIState 默认值与 Cmd+0 复位都指向 zoomOne，档位表调整挪位会静默错档。
	if one != zoomOne {
		t.Fatalf("1.0 档位下标 %d 与 zoomOne=%d 漂移", one, zoomOne)
	}
	if zoomAt(one) != 1 {
		t.Fatalf("zoomAt(基准位) = %v", zoomAt(one))
	}
	// 越界夹取：两端恒返回首/末档。
	if zoomAt(-10) != zoomSteps[0] || zoomAt(len(zoomSteps)+10) != zoomSteps[len(zoomSteps)-1] {
		t.Fatalf("越界应夹取: %v %v", zoomAt(-10), zoomAt(len(zoomSteps)+10))
	}
	// 步进：基准位 +1 放大、-1 缩小、两端不再移动。
	if zoomStep(one, 1) != one+1 || zoomStep(one, -1) != one-1 {
		t.Fatalf("基准步进错误: %d %d", zoomStep(one, 1), zoomStep(one, -1))
	}
	if zoomStep(0, -1) != 0 || zoomStep(len(zoomSteps)-1, 1) != len(zoomSteps)-1 {
		t.Fatal("两端步进必须钳制")
	}
	// 复位语义：zoomStep(i, 0) 不由本函数表达（复位=直接置 one），仅防御性
	// 断言 0 步进为恒等。
	if zoomStep(one, 0) != one {
		t.Fatalf("0 步进应恒等: %d", zoomStep(one, 0))
	}
}

// TestZoomShortcutWiring：根视图快捷键接线——Cmd+= 步进放大、Cmd+0 复位。
func TestZoomShortcutWiring(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 900, 600)
	tt.Key(ui.Cmd, ui.KeyEqual)
	tt.Frame()
	if u.Zoom != zoomOne+1 {
		t.Fatalf("Cmd+= 应放大一档: %d", u.Zoom)
	}
	tt.Key(ui.Cmd, ui.Key0)
	tt.Frame()
	if u.Zoom != zoomOne {
		t.Fatalf("Cmd+0 应复位: %d", u.Zoom)
	}
	tt.Key(ui.Cmd, ui.KeyMinus)
	tt.Frame()
	if u.Zoom != zoomOne-1 {
		t.Fatalf("Cmd+- 应缩小一档: %d", u.Zoom)
	}
}
