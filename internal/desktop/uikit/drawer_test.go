package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestDrawerOpenRenders 覆盖右侧抽屉的打开态：标题/副标题/关闭钮与 body
// 内容都进入可访问树（ui.Overlay 元素对 Tester 可见）。
func TestDrawerOpenRenders(t *testing.T) {
	open := true
	tt := ui.NewTester(func(c *ui.Context) {
		Drawer(c, c.Theme(), &open, "接入配置", "写入受管键", func() { ui.Text(c, "抽屉正文") })
	}, 1000, 1600)
	for _, want := range []string{"接入配置", "写入受管键", "关闭", "抽屉正文"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

// TestDrawerClosedRendersNothing：关闭态零渲染，正文与标题都不出现。
func TestDrawerClosedRendersNothing(t *testing.T) {
	open := false
	tt := ui.NewTester(func(c *ui.Context) {
		Drawer(c, c.Theme(), &open, "接入配置", "写入受管键", func() { ui.Text(c, "抽屉正文") })
	}, 1000, 1600)
	if tt.HasText("接入配置") || tt.HasText("抽屉正文") {
		t.Fatalf("closed drawer must render nothing: %v", tt.Texts())
	}
}

// TestDrawerCloseButtonFlipsOpen：点「关闭」只翻 open 位，不触碰调用方状态。
func TestDrawerCloseButtonFlipsOpen(t *testing.T) {
	open := true
	tt := ui.NewTester(func(c *ui.Context) {
		Drawer(c, c.Theme(), &open, "接入配置", "写入受管键", func() { ui.Text(c, "抽屉正文") })
	}, 1000, 1600)
	if err := tt.Click("关闭"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if open {
		t.Fatal("close button must flip *open to false")
	}
}
