//go:build !linux

package desktop

// disableSubpixelAA 在非 Linux 平台无操作：子像素字形着色缺陷只在
// Linux（Pango）路径上存在。见 fontrender_linux.go。
func disableSubpixelAA() {}
