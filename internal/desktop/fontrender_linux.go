//go:build linux

package desktop

import (
	"github.com/ebitengine/purego"
)

// disableSubpixelAA 只对本进程的 GTK 关闭子像素抗锯齿
// （gtk-xft-rgba=none），让 MyGo 选灰度抗锯齿栅格化文字。
//
// 原因：MyGo v0.5.1 的 Linux 子像素字形路径着色有缺陷——Pango 以白色
// 栅格化字形、覆盖率存进 ColorAtlas，GPU 着色时文字颜色没有正确应用，
// 所有文字实际都画成白色。深色主题下正文本来就是近白色，看不出来；
// 亮色主题（深色文字）下整窗文字不可读（白底白字）。
//
// 灰度字形走 mask 路径，颜色正确。gtk_settings_set_string_property 只
// 改本进程的 GtkSettings，不影响系统设置与其他应用；必须在创建窗口前
// 调用——MyGo 在窗口宿主初始化时读一次 gtk-xft 设置。
func disableSubpixelAA() {
	lib, err := purego.Dlopen("libgtk-3.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	var getDefault func() uintptr
	var setString func(settings uintptr, name, value, origin *byte)
	if !bindGTK(&getDefault, lib, "gtk_settings_get_default") ||
		!bindGTK(&setString, lib, "gtk_settings_set_string_property") {
		return
	}
	settings := getDefault()
	if settings == 0 {
		return
	}
	setString(settings, cString("gtk-xft-rgba"), cString("none"), cString("agentswitch"))
}

// bindGTK 注册一个 GTK 符号；库版本缺该符号时返回 false 而不是 panic。
func bindGTK(fptr any, lib uintptr, name string) (ok bool) {
	defer func() { ok = recover() == nil }()
	purego.RegisterLibFunc(fptr, lib, name)
	return true
}

// cString 返回 s 的 NUL 结尾 C 字符串；切片逃逸到堆，调用期间地址有效。
func cString(s string) *byte {
	b := append([]byte(s), 0)
	return &b[0]
}
