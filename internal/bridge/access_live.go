package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"AgentSwitch/internal/storage"
)

// writeClaudeLive 把配置文档顶层键与**合成 env 的全部键**写进
// <home>/.claude/settings.json（受管集=合成 env 全部键——2026-10-18 裁定：
// Apply 传入的是合成 env（基础键+全局配置+覆盖项），spec 验收场景④要求全局
// env 如 HTTPS_PROXY 可达 live 文件；原简报「只四个受管键」表述过窄，以设计
// 为准）：目录不存在则 MkdirAll 0o755；文件不存在建空对象，存在则整体解析为
// map[string]any，**解析失败直接返回错误且绝不改动原文件**（先读后写，
// 不猜测修复）；文档顶层键逐键写入（env 键除外——env 走合成结果；同键替换，
// live 中不在文档的键保留，spec 2026-10-19 §4.1 只增改不删除）；确保 env
// 子对象存在，同键替换写入合成 env 全部键；文件里不在本次合成 env 中的键
// （用户其他 env）与非受管顶层键原样保留；0600 原子写回（同目录 CreateTemp
// → chmod → write → close → rename）。本方法不写任何日志——写入值含网关
// 令牌（spec §9 脱敏）。
func (s AccessConfigs) writeClaudeLive(doc map[string]any, env map[string]string) error {
	home := s.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return err
		}
	}
	dir := filepath.Join(home, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.json")
	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		// 解析失败必须在任何写动作前返回，原文件保持不动。
		// UseNumber：大整数保留原始数字文本，避免 float64 往返丢精度
		//（评审 Minor#9）。
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&root); err != nil {
			return err
		}
		if root == nil { // 合法 JSON null → 视为空对象
			root = map[string]any{}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// 文档顶层键逐键写入（同键替换；live 中不在文档的键保留——只增改不删除，
	// spec 2026-10-19 §4.1）。
	for k, v := range doc {
		if k == "env" {
			continue
		}
		root[k] = v
	}
	// 确保 env 子对象存在；已有但非对象则报错（不猜测修复）。
	envObj := map[string]any{}
	if v, ok := root["env"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("settings.json 的 env 不是对象")
		}
		envObj = m
	}
	for k, v := range env { // 合成 env 全部键写入（同键替换）
		envObj[k] = v
	}
	root["env"] = envObj
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile0600(path, b)
}

// liveBackupFiles 返回 appType 对应的 live 文件相对路径列表（简报钉死）：
// claude 仅 settings.json；codex 仅 config.toml（auth.json 由 Codex 自己
// 维护官方登录，接入配置不读写它）。
func liveBackupFiles(appType string) []string {
	switch appType {
	case "claude":
		return []string{filepath.Join(".claude", "settings.json")}
	case "codex":
		return []string{filepath.Join(".codex", "config.toml")}
	case "claude_desktop":
		// Desktop 配置在 config 目录而非 home 直下，由 backupLive 以
		// desktopPathsFor(home) 的 4 个文件由 backupLive 单独补入，这里返回空。
		return nil
	}
	return nil
}

// backupLive 在写入前把 Agent 的 live 文件备份到
// <home>/.agentswitch/backups/<appType>/<UnixMilli毫秒时间戳>-<原文件名>：
// 源文件不存在则跳过该文件（全部跳过时不建备份目录、返回 nil）；复制保留
// 原权限；同毫秒撞名时加 -1/-2 递增后缀去重（不覆盖丢份）；每 appType
// 目录只保留最新 10 份（文件名时间戳前缀等长，字典序即时序，从旧到新删到
// 剩 10）。任何备份失败返回 error（调用方中止后续写入）。本方法不写任何
// 日志——备份内容可能含令牌（spec §9 脱敏）。
func (s AccessConfigs) backupLive(appType string) error {
	home := s.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return err
		}
	}
	// 先收集全部存在的源文件；全部不存在则直接返回（不建备份目录）。
	type srcFile struct {
		name string
		data []byte
		mode os.FileMode
	}
	var srcs []srcFile
	paths := make([]string, 0, len(liveBackupFiles(appType))+1)
	for _, rel := range liveBackupFiles(appType) {
		paths = append(paths, filepath.Join(home, rel))
	}
	if appType == "claude_desktop" {
		// Desktop 的 4 个文件在 config 目录（绝对路径），不随 home 拼接。
		paths = append(paths, desktopPathsFor(home).all()...)
	}
	for _, path := range paths {
		b, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return err
		}
		fi, err := os.Stat(path)
		if err != nil {
			return err
		}
		srcs = append(srcs, srcFile{name: filepath.Base(path), data: b, mode: fi.Mode().Perm()})
	}
	if len(srcs) == 0 {
		return nil
	}
	bdir := filepath.Join(home, ".agentswitch", "backups", appType)
	if err := os.MkdirAll(bdir, 0o755); err != nil {
		return err
	}
	ts := strconv.FormatInt(time.Now().UnixMilli(), 10)
	for _, src := range srcs {
		dst := filepath.Join(bdir, ts+"-"+src.name)
		// 同毫秒撞名去重：目标已存在则加 -1/-2 递增后缀，绝不覆盖丢份。
		for i := 1; ; i++ {
			if _, err := os.Stat(dst); errors.Is(err, fs.ErrNotExist) {
				break
			} else if err != nil {
				return err
			}
			dst = filepath.Join(bdir, fmt.Sprintf("%s-%d-%s", ts, i, src.name))
		}
		// 备份含令牌：强制 0600（源文件若是 0644 也收紧——评审 Minor#7）。
		if err := os.WriteFile(dst, src.data, 0o600); err != nil {
			return err
		}
		if err := os.Chmod(dst, 0o600); err != nil {
			return err
		}
	}
	// 滚动清理：按文件名排序（时间戳前缀等长 → 字典序即时序）从旧到新删到剩 10。
	ents, err := os.ReadDir(bdir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(ents))
	for _, e := range ents {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	if excess := len(names) - 10; excess > 0 { // 不足 10 份时无需删除
		for _, n := range names[:excess] {
			if err := os.Remove(filepath.Join(bdir, n)); err != nil {
				return err
			}
		}
	}
	return nil
}

// atomicWriteFile0600 以 0600 原子写回文件：同目录 CreateTemp → chmod 0600
// → write → close → rename；任一步失败清理临时文件（rename 成功后清理为
// 无操作），目标文件要么保持旧内容要么被完整替换。
func atomicWriteFile0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// writePersistEnv 把合成 env 落成该配置的持久文件（spec「持久文件」）：
// claude → <home>/.agentswitch/access/claude/<id>.json（**整份合成文档**：
// doc 顶层键 + env 替换为合成结果，供 `claude --settings` 引用）；codex →
// <home>/.agentswitch/access/codex/<id>.env（KEY=VALUE 行，键按字典序，供
// shell source 后启动 codex；doc 参数忽略——codex 持久文件是行格式无文档
// 语义，且 codex 配置本身仅允许 env 键）。Windows 改用 .cmd 启动器
// （见 writeWindowsLauncher）：claude 仍写 .json，另加一份 .cmd 供
// 「复制启动命令/打开终端」使用；codex 只写 .cmd（Windows 无 sh source）。
// 文件 0600（含网关令牌，不写日志）。
func (s AccessConfigs) writePersistEnv(c storage.AccessConfig, env map[string]string, doc map[string]any, model string) (persistFiles, error) {
	home := s.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return persistFiles{}, err
		}
	}
	switch c.AppType {
	case "claude":
		docFull := make(map[string]any, len(doc)+1)
		for k, v := range doc {
			docFull[k] = v
		}
		envAny := make(map[string]any, len(env))
		for k, v := range env {
			envAny[k] = v
		}
		docFull["env"] = envAny
		b, err := json.MarshalIndent(docFull, "", "  ")
		if err != nil {
			return persistFiles{}, err
		}
		p := filepath.Join(home, ".agentswitch", "access", "claude", c.ID+".json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return persistFiles{}, err
		}
		if err := atomicWriteFile0600(p, b); err != nil {
			return persistFiles{}, err
		}
		f := persistFiles{claude: p}
		if accessPlatform == "windows" {
			lp := filepath.Join(filepath.Dir(p), c.ID+".cmd")
			if err := s.writeWindowsLauncherFile(lp, "claude", env, model, p); err != nil {
				return persistFiles{}, err
			}
			f.launcher = lp
		}
		return f, nil
	case "codex":
		keys, err := codexEnvKeys(env)
		if err != nil {
			return persistFiles{}, err
		}
		dir := filepath.Join(home, ".agentswitch", "access", "codex")
		if accessPlatform == "windows" {
			lp := filepath.Join(dir, c.ID+".cmd")
			if err := s.writeWindowsLauncherFile(lp, "codex", env, model, ""); err != nil {
				return persistFiles{}, err
			}
			return persistFiles{launcher: lp}, nil
		}
		var sb strings.Builder
		for _, k := range keys {
			// 值用单引号包裹（' 换成 '\''）：source 时空格/元字符不再被
			// shell 解释——「复制出来的命令在干净 shell 里可直接运行」。
			sb.WriteString(k)
			sb.WriteString("='")
			sb.WriteString(strings.ReplaceAll(env[k], "'", `'\''`))
			sb.WriteString("'\n")
		}
		p := filepath.Join(dir, c.ID+".env")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return persistFiles{}, err
		}
		if err := atomicWriteFile0600(p, []byte(sb.String())); err != nil {
			return persistFiles{}, err
		}
		return persistFiles{codex: p}, nil
	default:
		return persistFiles{}, fmt.Errorf("不支持的接入类型: %q", c.AppType)
	}
}

// writeWindowsLauncherFile 把 Windows .cmd 启动器原子写入磁盘（0600）。
func (s AccessConfigs) writeWindowsLauncherFile(path, appType string, env map[string]string, model, settingsPath string) error {
	b, err := writeWindowsLauncher(appType, env, model, settingsPath)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicWriteFile0600(path, b)
}

// 批处理无 argv 式 env 注入，故用 set；% 是变量前缀，统一转义为 %%。
// 值/模型含双引号或换行时拒绝生成（行格式无法安全表达）。
func writeWindowsLauncher(appType string, env map[string]string, model, settingsPath string) ([]byte, error) {
	var sb strings.Builder
	sb.WriteString("@echo off\r\nsetlocal\r\n")
	switch appType {
	case "claude":
		sb.WriteString(`claude --settings "` + batchEscape(settingsPath) + `" %*` + "\r\n")
		return []byte(sb.String()), nil
	case "codex":
		keys, err := codexEnvKeys(env)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if strings.Contains(env[k], `"`) {
				return nil, fmt.Errorf("配置值不能包含双引号（Windows）: %s", k)
			}
			sb.WriteString(`set "` + k + `=` + batchEscape(env[k]) + `"` + "\r\n")
		}
		if strings.ContainsAny(model, "\"\r\n") {
			return nil, fmt.Errorf("模型名不能包含双引号或换行符（Windows）: %q", model)
		}
		sb.WriteString(`codex -m "` + batchEscape(model) + `" %*` + "\r\n")
		return []byte(sb.String()), nil
	default:
		return nil, fmt.Errorf("不支持的接入类型: %q", appType)
	}
}

// batchEscape 转义批处理中的 %（变量展开前缀）。
func batchEscape(v string) string {
	return strings.ReplaceAll(v, "%", "%%")
}

// shellDQ 转义值用于双引号 shell 片段内（\ " $ ` 五个有语义字符）；
// 模型名/路径均为用户可控输入，拼进贴给用户执行的命令必须转义
// （Task 11 评审 Important：Go %q 是字面量引用不是 shell 引用）。
func shellDQ(v string) string {
	return strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		`$`, `\$`,
		"`", "\\`",
	).Replace(v)
}

// shellSQ 转义字符串用于外层单引号 shell 片段内（单引号换成反斜杠转义序列）。
func shellSQ(v string) string {
	return strings.ReplaceAll(v, `'`, `'\''`)
}
