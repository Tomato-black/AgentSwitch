package bridge

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

// openPath 是平台文件管理器打开目录的注入点（测试替换）。
var openPath = func(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("explorer", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// OpenLogDir 打开日志文件所在目录；失败返回错误，不阻塞 UI。
func (g Gateway) OpenLogDir() error {
	p := g.A.LogPath()
	if p == "" {
		return fmt.Errorf("日志路径未配置")
	}
	return openPath(filepath.Dir(p))
}
