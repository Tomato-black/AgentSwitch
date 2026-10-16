// icons.go 是边栏导航的四枚 16px 内联线性图标（概览=面板格、供应商=插头、
// 账号=钥匙、模型=分支路由），ui.ParseSVG 内联定义，色随主题文字
//（spec §4；§8.1 待验证：currentColor 若不着色，退化为固定中性色）。
package desktop

import "github.com/egoist/mygo/ui"

func mustSVG(data string) *ui.SVG {
	s, err := ui.ParseSVG([]byte(data))
	if err != nil {
		panic("desktop: bad icon svg: " + err.Error())
	}
	return s
}

const svgHead = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">`

var (
	iconOverview = mustSVG(svgHead +
		`<rect x="1.5" y="1.5" width="5.5" height="5.5" rx="1"/><rect x="9" y="1.5" width="5.5" height="5.5" rx="1"/>` +
		`<rect x="1.5" y="9" width="5.5" height="5.5" rx="1"/><rect x="9" y="9" width="5.5" height="5.5" rx="1"/></svg>`)
	iconProviders = mustSVG(svgHead +
		`<path d="M5.5 1.5v4M10.5 1.5v4M4 5.5h8v2.5a4 4 0 0 1-8 0V5.5zM8 12v2.5"/></svg>`)
	iconAccounts = mustSVG(svgHead +
		`<circle cx="5" cy="11" r="3"/><path d="M7.3 8.7L14 2M10.5 5.5l2.2 2.2"/></svg>`)
	iconModels = mustSVG(svgHead +
		`<circle cx="8" cy="8" r="2.2"/><path d="M8 1.5v4.3M8 10.2v4.3M1.5 8h4.3M10.2 8h4.3"/></svg>`)
)
