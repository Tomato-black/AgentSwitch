package main

import (
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/desktop"
	"AgentSwitch/internal/presets"
)

func main() {
	// 单实例锁前移到 app.Open 之前（审查 minor 升级）：抢锁失败直接返回——
	// 若先开库再抢锁，失败路径会带着已打开的库句柄与可能已跑的迁移退出
	// （句柄泄漏 + 与持锁实例的并发迁移竞态）。
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	base, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	dir := filepath.Join(base, "AgentSwitch")
	a, err := app.Open(app.Options{
		DBPath:  filepath.Join(dir, "agentswitch.db"),
		LogPath: filepath.Join(dir, "logs", "app.log"),
		Version: "0.1.0",
		Host:    "127.0.0.1",
	})
	if err != nil {
		log.Fatal(err)
	}
	// 渠道预置表启动加载一次（spec 2026-10-23 §4：内嵌默认 + 用户文件覆盖，
	// 此后只读）。用户文件放 ~/.agentswitch/presets.json（项目用户编辑文件
	// 惯例，同 access/backups）。
	userPresets := ""
	if home, err := os.UserHomeDir(); err == nil {
		userPresets = filepath.Join(home, ".agentswitch", "presets.json")
	}
	presets.Load(presets.DefaultJSON(), userPresets)
	if err := desktop.Run(a); err != nil {
		log.Fatal(err)
	}
}
