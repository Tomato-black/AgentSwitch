package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestTileSoftDeterministic 覆盖 tileSoft 的预设映射与确定性：anthropic
// 系→act（软橙）、openai 系→ok（软绿），未知名走 fnv 轮转，但同一输入
// 必须恒同输出——tile 底色不允许逐帧漂移。
func TestTileSoftDeterministic(t *testing.T) {
	if TileSoft("anthropic-official") != "act" || TileSoft("openai-compatible") != "ok" {
		t.Fatal("preset 映射")
	}
	if TileSoft("GLM") != TileSoft("GLM") {
		t.Fatal("must be deterministic")
	}
}

// TestWidgetsRender 渲染全部视觉词汇 helper 并断言文字在场；两个按钮
// helper 以 ButtonBase 为底座，是真按钮——Tester 点击后 Clicked() 必须
// 送达（后续页面全部经 if xxx(...).Clicked() 消费，点不中即页面瘫痪）。
func TestWidgetsRender(t *testing.T) {

	var stopClicked, addClicked bool
	tt := ui.NewTester(func(c *ui.Context) {
		th := c.Theme()
		s := Softs(th)
		Card(c, th, func() {
			SoftBadge(c, th, s, "ok", "启用")
			SoftBadge(c, th, s, "warn", "冷却 1 把")
			SoftBadge(c, th, s, "nonsense", "中性")
			if SoftButton(c, th, s, "danger", "停止网关").Clicked() {
				stopClicked = true
			}
			if PillButton(c, th, "＋ 添加渠道").Clicked() {
				addClicked = true
			}
			Tile(c, th, s, "PackyCode", 40)
		})
	}, 600, 400)
	for _, want := range []string{"启用", "冷却 1 把", "中性", "停止网关", "＋ 添加渠道", "Pa"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if err := tt.Click("停止网关"); err != nil {
		t.Fatal(err)
	}
	if !stopClicked {
		t.Fatal("softButton Clicked() not delivered")
	}
	if err := tt.Click("＋ 添加渠道"); err != nil {
		t.Fatal(err)
	}
	if !addClicked {
		t.Fatal("pillButton Clicked() not delivered")
	}
}
