// zoom.go 是全局浏览器式缩放（mygo v0.7.0 Context.SetZoom + Shortcut）：档位
// 与 gallery 同款（0.5–3，浏览器习惯的渐进密度）；Cmd/Ctrl（macOS 上 Cmd）
// + =/+/-/0 步进与复位，文字/图标/矢量按缩放尺寸清晰重绘（非拉伸）。
// 为密集的设置页提供低视力可放大能力，随窗口会话存在（不持久化）。
package desktop

// zoomSteps 是缩放档位（升序）；zoomOne 是 1.0 的下标。
var zoomSteps = []float32{0.5, 0.67, 0.75, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2, 2.5, 3}

const zoomOne = 5

// zoomAt 返回档位下标的缩放值；越界夹取到首/末档。
func zoomAt(i int) float32 {
	if i < 0 {
		i = 0
	}
	if i >= len(zoomSteps) {
		i = len(zoomSteps) - 1
	}
	return zoomSteps[i]
}

// zoomStep 返回步进后的档位下标（两端钳制）；delta 为 0 恒等（复位由调用方
// 直接置 zoomOne，不经本函数）。
func zoomStep(i, delta int) int {
	i += delta
	if i < 0 {
		return 0
	}
	if i >= len(zoomSteps) {
		return len(zoomSteps) - 1
	}
	return i
}
