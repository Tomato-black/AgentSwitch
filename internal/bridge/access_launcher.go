package bridge

import (
	"fmt"
	"strings"
)

// LaunchCommand 返回展示/复制用的启动命令：每次调用都重写持久文件
// （保证令牌与模型最新——令牌轮换联动的单配置版）。命令拼装：
// macOS/Linux：claude `claude --settings "<json>"`（--settings 未给的键回落
// ~/.claude/settings.json）；codex `sh -c 'set -a; . "<env>"; set +a;
// exec codex -m "<model>"'`（env 文件内容保持命令跨令牌轮换有效）。
// Windows：`cmd /c call "<launcher>.cmd"`——cmd 与 PowerShell 均可直接粘贴，
// 令牌只存在 0600 的启动器文件里，不出现在命令文本中。
func (s AccessConfigs) LaunchCommand(id string) (string, error) {
	c, err := s.A.Store().AccessConfigs().Get(id)
	if err != nil {
		return "", err
	}
	env, doc, model, err := s.composeFor(c)
	if err != nil {
		return "", err
	}
	files, err := s.writePersistEnv(c, env, doc, model)
	if err != nil {
		return "", err
	}
	return launchCommandFor(accessPlatform, c.AppType, files, model)
}

// launchCommandFor 按平台拼装启动命令（纯函数，便于在任意主机覆盖平台）。
func launchCommandFor(platform, appType string, f persistFiles, model string) (string, error) {
	if platform == "windows" {
		if f.launcher == "" {
			return "", fmt.Errorf("生成 Windows 启动器失败")
		}
		return `cmd /c call "` + f.launcher + `"`, nil
	}
	switch appType {
	case "claude":
		return `claude --settings "` + shellDQ(f.claude) + `"`, nil
	case "codex":
		inner := `set -a; . "` + shellDQ(f.codex) + `"; set +a; exec codex -m "` + shellDQ(model) + `"`
		return `sh -c '` + shellSQ(inner) + `'`, nil
	default:
		return "", fmt.Errorf("不支持的接入类型: %q", appType)
	}
}

// OpenTerminal 用指定配置拉起一个终端（spec「打开终端」，macOS/Windows）：
// 先刷新持久文件。macOS 组装 AppleScript do script；Windows 用
// `cmd /c start "" "<launcher>.cmd"`（start 的空标题参数不可省）。其他
// 平台明确报不支持。不存在的配置报错。
func (s AccessConfigs) OpenTerminal(id string) error {
	c, err := s.A.Store().AccessConfigs().Get(id)
	if err != nil {
		return err
	}
	env, doc, model, err := s.composeFor(c)
	if err != nil {
		return err
	}
	files, err := s.writePersistEnv(c, env, doc, model)
	if err != nil {
		return err
	}
	var target string
	switch accessPlatform {
	case "windows":
		target = files.launcher
	case "darwin":
		if target, err = launchCommandFor(accessPlatform, c.AppType, files, model); err != nil {
			return err
		}
	default:
		return fmt.Errorf("当前平台不支持打开终端")
	}
	name, args, err := openTerminalInvocation(accessPlatform, target)
	if err != nil {
		return err
	}
	return openTerminalExec(name, args...)
}

// openTerminalInvocation 按平台组装终端拉起命令（纯函数，便于跨平台测试）。
// target：macOS 为启动命令文本，Windows 为 .cmd 启动器路径。
func openTerminalInvocation(platform, target string) (string, []string, error) {
	switch platform {
	case "darwin":
		escaped := strings.ReplaceAll(target, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		script := `tell application "Terminal" to do script "` + escaped + `"`
		return "osascript", []string{"-e", script}, nil
	case "windows":
		if target == "" {
			return "", nil, fmt.Errorf("Windows 启动器路径为空")
		}
		return "cmd", []string{"/c", "start", "", target}, nil
	default:
		return "", nil, fmt.Errorf("当前平台不支持打开终端")
	}
}
