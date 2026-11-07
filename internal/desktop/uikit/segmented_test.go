package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestSegmentedStrongSelection：uikit.Segmented 渲染全部段文本，点击未选中段
// 改变选择（radio 语义与 ui.Segmented 一致）；选中段样式为软橙底+粗体
// （视觉项，由用户实机验收，测试钉住语义与构建）。
func TestSegmentedStrongSelection(t *testing.T) {
	sel := 0
	tt := ui.NewTester(func(c *ui.Context) {
		Segmented(c, c.Theme(), Softs(c.Theme()), &sel, "用户级", "项目级", "企业托管")
	}, 600, 200)
	for _, want := range []string{"用户级", "项目级", "企业托管"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	if err := tt.Click("项目级"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if sel != 1 {
		t.Fatalf("click must choose the segment: %d", sel)
	}
}
