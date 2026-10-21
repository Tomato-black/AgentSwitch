//go:build !linux

package desktop

import "github.com/egoist/mygo"

// newTray 在非 Linux 平台直接建托盘；-panel 命名是 Ubuntu GNOME 专属约定。
func newTray(opts mygo.TrayOptions) (*mygo.Tray, error) {
	return mygo.NewTray(opts)
}
