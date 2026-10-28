// icons.go 是边栏导航的 16px 内联线性图标（逻辑模型=分支链、渠道=插头、
// 接入=钥匙、设置=齿轮），ui.ParseSVG 内联定义，色随主题文字
// （spec §4/§6.1；§8.1 待验证：currentColor 若不着色，退化为固定中性色）。
package uikit

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
	IconLogical = mustSVG(svgHead +
		`<circle cx="4" cy="4" r="2"/><circle cx="12" cy="8" r="2"/><circle cx="4" cy="12" r="2"/>` +
		`<path d="M6 4.8l4 2.4M6 11.2l4-2.4"/></svg>`)
	IconChannels = mustSVG(svgHead +
		`<path d="M5.5 1.5v4M10.5 1.5v4M4 5.5h8v2.5a4 4 0 0 1-8 0V5.5zM8 12v2.5"/></svg>`)
	IconAccess = mustSVG(svgHead +
		`<circle cx="5.5" cy="8" r="3"/><path d="M8.5 8H15M12.5 8v2.5M14.5 8v2"/></svg>`)
	IconSettings = mustSVG(svgHead +
		`<circle cx="8" cy="8" r="2.2"/><path d="M8 1.5v2.2M8 12.3v2.2M1.5 8h2.2M12.3 8h2.2M3.4 3.4l1.6 1.6M11 11l1.6 1.6M12.6 3.4L11 5M5 11l-1.6 1.6"/></svg>`)
	// IconTray 是「收进托盘」提示图形：向下箭头落到托盘横线。
	IconTray = mustSVG(svgHead +
		`<path d="M8 1.5v8M4.8 6.3L8 9.5l3.2-3.2M2 12.8h12"/></svg>`)
)
