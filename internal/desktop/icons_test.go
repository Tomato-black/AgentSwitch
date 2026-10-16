package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestIconsParsed 冒烟：包级 mustSVG 在 init 解析失败即 panic，测试能跑
// 到这里即四枚图标解析成功；再显式断言非 nil 防将来改为惰性构造时漏检。
func TestIconsParsed(t *testing.T) {
	for name, ic := range map[string]*ui.SVG{
		"overview": iconOverview, "channels": iconChannels,
		"accounts": iconAccounts, "models": iconModels,
	} {
		if ic == nil {
			t.Fatalf("icon %s is nil", name)
		}
	}
}
