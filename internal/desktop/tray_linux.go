//go:build linux

package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
)

// newTray 建托盘并修好 Linux 上的图标命名。
//
// MyGo 经 libayatana-appindicator 注册 StatusNotifierItem：图标写成
// <tmp>/mygo-tray-*/icon-N.png，再以 IconName=icon-N + IconThemePath 暴露。
// Ubuntu GNOME 的 ubuntu-appindicators 扩展查主题前会把名字追加 "-panel"
// （appIndicator.js `name += '-panel'`），只写 icon-N.png 会得到
// "unable to update icon"，图标被丢弃、顶栏什么都不显示。
//
// 修法：补写 icon-1-panel.png 与下一次 SetIcon 会写到的 icon-2-panel.png，
// 再调一次 SetIcon 产生 icon-2 并发出 NewIcon——扩展这次按 icon-2-panel
// 就能查到文件。其它宿主（KDE 等）不认 -panel，多出的文件无害。
func newTray(opts mygo.TrayOptions) (*mygo.Tray, error) {
	base := os.TempDir()
	before := trayTempDirs(base)
	tray, err := mygo.NewTray(opts)
	if err != nil {
		return nil, err
	}
	if len(opts.Icon) > 0 {
		if dir := findNewTrayDir(base, before, opts.Icon); dir != "" {
			if mirrorPanelIcon(dir, opts.Icon) == nil {
				_ = tray.SetIcon(opts.Icon, opts.IconIsTemplate)
			}
		}
	}
	return tray, nil
}

// trayTempDirs 记录 base 下已存在的 mygo 托盘临时目录名（NewTray 前调用）。
func trayTempDirs(base string) map[string]bool {
	m := map[string]bool{}
	entries, err := os.ReadDir(base)
	if err != nil {
		return m
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "mygo-tray-") {
			m[e.Name()] = true
		}
	}
	return m
}

// findNewTrayDir 找出 NewTray 新建、且 icon-1.png 与传入图标逐字节一致的
// 目录；找不到返回 ""（调用方静默降级）。比对内容是为了不误认并发运行
// 的其它 mygo 应用留下的目录。
func findNewTrayDir(base string, before map[string]bool, icon []byte) string {
	entries, err := os.ReadDir(base)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "mygo-tray-") || before[e.Name()] {
			continue
		}
		dir := filepath.Join(base, e.Name())
		got, err := os.ReadFile(filepath.Join(dir, "icon-1.png"))
		if err != nil || !bytes.Equal(got, icon) {
			continue
		}
		return dir
	}
	return ""
}

// mirrorPanelIcon 写入 Ubuntu 扩展要求的 -panel 文件名副本。
func mirrorPanelIcon(dir string, icon []byte) error {
	for _, name := range []string{"icon-1-panel.png", "icon-2-panel.png"} {
		if err := os.WriteFile(filepath.Join(dir, name), icon, 0o644); err != nil {
			return err
		}
	}
	return nil
}
