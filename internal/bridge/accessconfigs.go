package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/storage"
)

// AccessConfigs 是接入配置管理面（agent-access-config spec）：把
// 「逻辑模型 × Agent」的配置合成与投影整理为桌面可调用的服务。
// 本文件只落合成纯函数 composeAccessEnv（Task 5/10 的 Apply/Export
// 复用）；Home 为 "" 时业务方法回落 os.UserHomeDir()（路径方法属后续任务）。
type AccessConfigs struct {
	A    *app.App
	Home string
}

// composeAccessEnv 合成应写入 Agent 的 env（spec「配置合成规则」）：
// 先置网关派生基础键，再依次合并 global 与 overrides（后者优先），
// 最后重新写回不可篡改基础键，保证路由关键键（网关地址、令牌、模型名）
// 无法被用户配置篡改。注意：claude 的 ANTHROPIC_SMALL_FAST_MODEL 只作
// 默认值，允许被 overrides 覆盖（简报测试钉死）；不可篡改集为
// BASE_URL / AUTH_TOKEN / MODEL 三键（codex 为两键全锁定）。
func composeAccessEnv(appType string, st Status, modelName string, global, overrides map[string]string) (map[string]string, error) {
	if modelName == "" {
		return nil, fmt.Errorf("请先选择逻辑模型")
	}
	if err := validateAccessAppType(appType); err != nil {
		return nil, err
	}
	var base, locked map[string]string
	switch appType {
	case "claude":
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", st.Port)
		base = map[string]string{
			"ANTHROPIC_BASE_URL":         baseURL,
			"ANTHROPIC_AUTH_TOKEN":       st.Token,
			"ANTHROPIC_MODEL":            modelName,
			"ANTHROPIC_SMALL_FAST_MODEL": modelName,
		}
		locked = map[string]string{
			"ANTHROPIC_BASE_URL":   baseURL,
			"ANTHROPIC_AUTH_TOKEN": st.Token,
			"ANTHROPIC_MODEL":      modelName,
		}
	case "codex":
		baseURL := fmt.Sprintf("http://127.0.0.1:%d/v1", st.Port)
		base = map[string]string{
			"OPENAI_BASE_URL": baseURL,
			"OPENAI_API_KEY":  st.Token,
		}
		locked = map[string]string{
			"OPENAI_BASE_URL": baseURL,
			"OPENAI_API_KEY":  st.Token,
		}
	}
	env := make(map[string]string, len(base)+len(global)+len(overrides))
	for k, v := range base { // 1. 网关派生基础键
		env[k] = v
	}
	for k, v := range global { // 2. 全局 env（access.global.<app_type>）
		env[k] = v
	}
	for k, v := range overrides { // 3. 配置级覆盖（最高优先，但动不了锁定键）
		env[k] = v
	}
	for k, v := range locked { // 4. 重新写回不可篡改基础键（恒定）
		env[k] = v
	}
	return env, nil
}

// composeFor 解析 c 绑定的逻辑模型名与全局配置，合成应写入 Agent 的 env。
// 返回 model 为逻辑模型对外名（codex 写 config.toml 的 model 键与启动命令用；
// claude 的模型名已在 env 内）。绑定的逻辑模型不存在时报中文错误提示改绑；
// 全局配置读取失败/JSON 非法直接返回错误。本方法不写任何含 env/令牌的日志。
func (s AccessConfigs) composeFor(c storage.AccessConfig) (map[string]string, string, error) {
	lm, err := s.A.Store().LogicalModels().Get(c.LogicalModelID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, "", fmt.Errorf("绑定的逻辑模型不存在，请改绑")
		}
		return nil, "", err
	}
	raw, err := s.A.Store().Settings().Get("access.global." + c.AppType)
	if err != nil {
		return nil, "", err
	}
	var global map[string]string
	if raw != "" { // Settings Get 缺失返回 ("", nil) → 视为无全局配置
		if err := json.Unmarshal([]byte(raw), &global); err != nil {
			return nil, "", err
		}
	}
	overrides, err := accessDocEnvMap(c.Settings)
	if err != nil {
		return nil, "", err
	}
	env, err := composeAccessEnv(c.AppType, Gateway{A: s.A}.Status(), lm.Name, global, overrides)
	if err != nil {
		return nil, "", err
	}
	return env, lm.Name, nil
}

// accessDocEnvMap 从整份 settings 文档提取 env 字符串 map（写入侧保证 env 值为
// 字符串；解析失败向上返回错误）。文档缺 env 视为空 map。
func accessDocEnvMap(settings string) (map[string]string, error) {
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if strings.TrimSpace(settings) == "" {
		return map[string]string{}, nil
	}
	if err := json.Unmarshal([]byte(settings), &doc); err != nil {
		return nil, err
	}
	if doc.Env == nil {
		return map[string]string{}, nil
	}
	return doc.Env, nil
}

// accessEnvDoc 把 env map 包装为最小文档 JSON（Task 7 前的过渡存法）。
func accessEnvDoc(env map[string]string) string {
	if env == nil {
		env = map[string]string{}
	}
	b, err := json.Marshal(struct {
		Env map[string]string `json:"env"`
	}{Env: env})
	if err != nil {
		return "{}" // map[string]string 不可能 marshal 失败；防御
	}
	return string(b)
}

// validateAccessAppType 校验接入类型白名单 {claude, codex}。
func validateAccessAppType(appType string) error {
	if appType != "claude" && appType != "codex" {
		return fmt.Errorf("不支持的接入类型: %q（仅支持 claude、codex）", appType)
	}
	return nil
}

// parseAccessDoc 解析接入配置文档（spec 2026-10-19 §5.2）：根必须是 JSON 对象
// （空串视为空对象）；env 缺省/null 视为空对象、必须是字符串值对象；Codex 仅
// 允许 env 键（其 live 是 TOML，顶层文档编辑仅 Claude 享受）。返回解析后的
// 文档与提取的 env 字符串 map。
func parseAccessDoc(appType, raw string) (map[string]any, map[string]string, error) {
	if err := validateAccessAppType(appType); err != nil {
		return nil, nil, err
	}
	doc := map[string]any{}
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
			return nil, nil, fmt.Errorf("配置必须是 JSON 对象：%v", err)
		}
		if doc == nil { // 合法 "null" 也拒绝：根必须是对象
			return nil, nil, fmt.Errorf("配置必须是 JSON 对象")
		}
	}
	env := map[string]string{}
	if v, ok := doc["env"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("env 必须是 JSON 对象")
		}
		for k, val := range m {
			s, ok := val.(string)
			if !ok {
				return nil, nil, fmt.Errorf("env 值必须是字符串: %s", k)
			}
			env[k] = s
		}
	}
	if appType == "codex" {
		for k := range doc {
			if k != "env" {
				return nil, nil, fmt.Errorf("Codex 配置仅支持 env 键")
			}
		}
	}
	return doc, env, nil
}

// writeClaudeLive 把传入**合成 env 的全部键**写进 <home>/.claude/settings.json
// 的 env 子对象（受管集=合成 env 全部键——2026-10-18 裁定：Apply 传入的是
// 合成 env（基础键+全局配置+覆盖项），spec 验收场景④要求全局 env 如
// HTTPS_PROXY 可达 live 文件；原简报「只四个受管键」表述过窄，以设计为准）：
// 目录不存在则 MkdirAll 0o755；文件不存在建空对象，存在则整体解析为
// map[string]any，**解析失败直接返回错误且绝不改动原文件**（先读后写，
// 不猜测修复）；确保 env 子对象存在，同键替换写入合成 env 全部键；文件里
// 不在本次合成 env 中的键（用户其他 env）与非 env 顶层键原样保留；
// 0600 原子写回（同目录 CreateTemp → chmod → write → close → rename）。
// 本方法不写任何日志——写入值含网关令牌（spec §9 脱敏）。
func (s AccessConfigs) writeClaudeLive(env map[string]string) error {
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

// writeCodexLive 把合成 env 与逻辑模型名投影到 <home>/.codex/ 的两个 live
// 文件（config.toml + auth.json）。语义：两个文件都各自「读-解析-合并」成功
// 后才开始落盘——**任一解析失败则两个文件都不写**（一败俱不写），解析失败
// 绝不改动原文件（先读后写，不猜测修复）：
//   - config.toml：不存在建空；解析为 map[string]any（go-toml/v2：TOML 表
//     对应嵌套 map[string]any）；受管键 model=<model>、
//     model_provider="agentswitch"、model_providers.agentswitch 子表
//     {base_url: env["OPENAI_BASE_URL"], wire_api: "chat",
//     requires_openai_auth: false}（同键替换，子表内非受管键保留）；其余
//     顶层键/表语义保留（TOML 注释允许丢失）；0600 原子写。
//   - auth.json：不存在建空对象；JSON 解析失败中止；受管键 OPENAI_API_KEY
//     = env["OPENAI_API_KEY"]；其余键保留；0600 原子写。
//
// 落盘顺序：任一写失败即中止、不写第二个。本方法不写任何日志——写入值
// 含网关令牌（spec §9 脱敏）。
func (s AccessConfigs) writeCodexLive(env map[string]string, model string) error {
	home := s.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return err
		}
	}
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	cfgPath := filepath.Join(dir, "config.toml")
	authPath := filepath.Join(dir, "auth.json")

	// —— 1. config.toml 读-解析-合并（失败即返回，尚未写任何文件）——
	cfg := map[string]any{}
	if b, err := os.ReadFile(cfgPath); err == nil {
		// 解析失败必须在任何写动作前返回，原文件保持不动。
		if err := toml.Unmarshal(b, &cfg); err != nil {
			return err
		}
		if cfg == nil { // 空文档 → 视为空表
			cfg = map[string]any{}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	cfg["model"] = model
	cfg["model_provider"] = "agentswitch"
	// model_providers 子表：已存在但非表则报错（不猜测修复）。
	providers := map[string]any{}
	if v, ok := cfg["model_providers"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("config.toml 的 model_providers 不是表")
		}
		providers = m
	}
	agent := map[string]any{}
	if v, ok := providers["agentswitch"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("config.toml 的 model_providers.agentswitch 不是表")
		}
		agent = m
	}
	agent["base_url"] = env["OPENAI_BASE_URL"]
	agent["wire_api"] = "chat"
	agent["requires_openai_auth"] = false
	providers["agentswitch"] = agent
	cfg["model_providers"] = providers
	cfgBytes, err := toml.Marshal(cfg)
	if err != nil {
		return err
	}

	// —— 2. auth.json 读-解析-合并（失败即返回，两个文件都还没写）——
	auth := map[string]any{}
	if b, err := os.ReadFile(authPath); err == nil {
		if err := json.Unmarshal(b, &auth); err != nil {
			return err
		}
		if auth == nil { // 合法 JSON null → 视为空对象
			auth = map[string]any{}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	auth["OPENAI_API_KEY"] = env["OPENAI_API_KEY"]
	authBytes, err := json.MarshalIndent(auth, "", "  ")
	if err != nil {
		return err
	}

	// —— 3. 两个文件都解析成功后才落盘；任一写失败即中止、不写第二个 ——
	if err := atomicWriteFile0600(cfgPath, cfgBytes); err != nil {
		return err
	}
	return atomicWriteFile0600(authPath, authBytes)
}

// liveBackupFiles 返回 appType 对应的 live 文件相对路径列表（简报钉死）：
// claude 仅 settings.json；codex 为 config.toml 与 auth.json。
func liveBackupFiles(appType string) []string {
	switch appType {
	case "claude":
		return []string{filepath.Join(".claude", "settings.json")}
	case "codex":
		return []string{filepath.Join(".codex", "config.toml"), filepath.Join(".codex", "auth.json")}
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
	for _, rel := range liveBackupFiles(appType) {
		path := filepath.Join(home, rel)
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

// friendlyAccessSaveErr 把 UNIQUE(app_type, name) 冲突译成中文友好错误
// （repo 约定由 bridge 友好化）。
func friendlyAccessSaveErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("配置名已存在")
	}
	return err
}

// AccessConfigView 是接入配置卡片视图（UI 接线契约，字段名勿改）；
// LogicalModelName 为绑定逻辑模型的对外名（解析失败留空串）。
type AccessConfigView struct {
	ID, Name, AppType, LogicalModelID, LogicalModelName string
	Overrides                                           map[string]string
	IsCurrent                                           bool
}

func (s AccessConfigs) view(c storage.AccessConfig) AccessConfigView {
	v := AccessConfigView{ID: c.ID, Name: c.Name, AppType: c.AppType,
		LogicalModelID: c.LogicalModelID, IsCurrent: c.IsCurrent}
	// 逐条解析逻辑模型名；Get 失败（如已删模型）留空串即可。
	if lm, err := s.A.Store().LogicalModels().Get(c.LogicalModelID); err == nil {
		v.LogicalModelName = lm.Name
	}
	// 文档提取失败留空 map：view 只读展示，不因单份脏数据整体失败。
	v.Overrides, _ = accessDocEnvMap(c.Settings)
	return v
}

// List 返回该 app 的接入配置列表（含 LogicalModelName 解析与 IsCurrent 标记）。
func (s AccessConfigs) List(appType string) []AccessConfigView {
	if validateAccessAppType(appType) != nil {
		return nil
	}
	cs, err := s.A.Store().AccessConfigs().ListByApp(appType)
	if err != nil {
		return nil
	}
	out := make([]AccessConfigView, 0, len(cs))
	for _, c := range cs {
		out = append(out, s.view(c))
	}
	return out
}

// Create 建接入配置：appType 白名单、name 非空（TrimSpace）、logicalModelID
// 必须存在；同 app 重名译「配置名已存在」。审计行绝不记录 env/overrides 值
// 或 token（spec §9 脱敏）。
func (s AccessConfigs) Create(appType, name, logicalModelID string, overrides map[string]string) (AccessConfigView, error) {
	if err := validateAccessAppType(appType); err != nil {
		return AccessConfigView{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return AccessConfigView{}, fmt.Errorf("配置名不能为空")
	}
	if _, err := s.A.Store().LogicalModels().Get(logicalModelID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return AccessConfigView{}, fmt.Errorf("逻辑模型不存在")
		}
		return AccessConfigView{}, err
	}
	id := storage.NewID()
	saved, err := s.A.Store().AccessConfigs().Save(storage.AccessConfig{
		ID: id, AppType: appType, Name: name, LogicalModelID: logicalModelID, Settings: accessEnvDoc(overrides),
	})
	if err != nil {
		return AccessConfigView{}, friendlyAccessSaveErr(err)
	}
	s.A.Log().Info("access config created", "id", id, "app", appType, "actor", "ui", "result", "ok")
	s.RegenerateDerived() // 新配置即落持久文件，其余派生文件同步刷新（spec「保存/更新时同步生成」）
	return s.view(saved), nil
}

// Update 改配置字段并保 ID（非 Remove+Create）：直接 repo.Save 覆盖，
// 保留原 app_type / is_current / sort_order。
func (s AccessConfigs) Update(id, name, logicalModelID string, overrides map[string]string) error {
	c, err := s.A.Store().AccessConfigs().Get(id)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("配置名不能为空")
	}
	if _, err := s.A.Store().LogicalModels().Get(logicalModelID); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("逻辑模型不存在")
		}
		return err
	}
	c.Name, c.LogicalModelID = name, logicalModelID
	c.Settings = accessEnvDoc(overrides)
	if _, err := s.A.Store().AccessConfigs().Save(c); err != nil {
		return friendlyAccessSaveErr(err)
	}
	s.A.Log().Info("access config updated", "id", id, "app", c.AppType, "actor", "ui", "result", "ok")
	s.RegenerateDerived() // 覆盖项/模型变了，全部派生文件同步刷新（spec「保存/更新时同步生成」）
	return nil
}

// Remove 直接删行，**不触碰任何 live 文件**（删除使用中配置时 live 保持原样，
// 由用户自行决定后续处理）；is_current 行删除后 current 自然消失（部分唯一
// 索引允许）。删除后重写其余配置的持久文件（被删配置的持久文件保留——
// 用户可能仍持有其启动命令）。
func (s AccessConfigs) Remove(id string) error {
	if err := s.A.Store().AccessConfigs().Delete(id); err != nil {
		s.A.Log().Error("access config remove failed", "id", id, "err", err)
		return err
	}
	s.A.Log().Info("access config removed", "id", id, "actor", "ui", "result", "ok")
	s.RegenerateDerived()
	return nil
}

// Apply 把配置合成后投影到 Agent live 文件并置为 current：Get(id) →
// composeFor → backupLive(appType) → 按 appType 写 live（claude→
// writeClaudeLive(env)；codex→writeCodexLive(env, model)）→ SetCurrent
// (appType, id)。任一步失败即停——含备份失败（不写 live、不置 current），
// 失败路径不产生半状态；composeFor 的逻辑模型缺失错误直接透传。
// 审计行绝不记录 env/overrides 值或 token（spec §9 脱敏）。
func (s AccessConfigs) Apply(id string) error {
	c, err := s.A.Store().AccessConfigs().Get(id)
	if err != nil {
		return err
	}
	env, model, err := s.composeFor(c)
	if err != nil {
		return err
	}
	if err := s.backupLive(c.AppType); err != nil {
		return err
	}
	switch c.AppType {
	case "claude":
		err = s.writeClaudeLive(env)
	case "codex":
		err = s.writeCodexLive(env, model)
	}
	if err != nil {
		return err
	}
	if err := s.A.Store().AccessConfigs().SetCurrent(c.AppType, id); err != nil {
		return err
	}
	s.A.Log().Info("access config applied", "id", id, "app", c.AppType, "actor", "ui", "result", "ok")
	return nil
}

// GlobalConfig 读全局 env（settings 键 access.global.<app_type>）；缺失视为
// 空 map。审计/返回不涉及 token。
func (s AccessConfigs) GlobalConfig(appType string) (map[string]string, error) {
	if err := validateAccessAppType(appType); err != nil {
		return nil, err
	}
	raw, err := s.A.Store().Settings().Get("access.global." + appType)
	if err != nil {
		return nil, err
	}
	if raw == "" { // Settings Get 缺失返回 ("", nil) → 无全局配置
		return map[string]string{}, nil
	}
	var env map[string]string
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, err
	}
	if env == nil {
		env = map[string]string{}
	}
	return env, nil
}

// SaveGlobalConfig 写全局 env（JSON 编码存 settings 键 access.global.<app_type>）。
// 审计行绝不记录 env 值或 token。
func (s AccessConfigs) SaveGlobalConfig(appType string, env map[string]string) error {
	if err := validateAccessAppType(appType); err != nil {
		return err
	}
	if env == nil {
		env = map[string]string{}
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	if err := s.A.Store().Settings().Set("access.global."+appType, string(raw)); err != nil {
		return err
	}
	s.A.Log().Info("access global config saved", "app", appType, "actor", "ui", "result", "ok")
	s.RegenerateDerived() // 全局配置变了，全部派生文件同步刷新（spec 验收场景④）
	return nil
}

// writePersistEnv 把合成 env 落成该配置的持久文件（spec「持久文件」）：
// claude → <home>/.agentswitch/access/claude/<id>.json（{"env":{...}}，
// 供 `claude --settings` 引用）；codex → <home>/.agentswitch/access/codex/
// <id>.env（KEY=VALUE 行，键按字典序，供 shell source 后启动 codex）。
// 按 appType 只生成对应一种，另一种路径返回空串。.env 任一值含换行符即
// 报错中止（行格式无法安全表达）。文件 0600（含网关令牌，不写日志）。
func (s AccessConfigs) writePersistEnv(c storage.AccessConfig, env map[string]string) (string, string, error) {
	home := s.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return "", "", err
		}
	}
	switch c.AppType {
	case "claude":
		payload := struct {
			Env map[string]string `json:"env"`
		}{Env: env}
		b, err := json.MarshalIndent(payload, "", "  ")
		if err != nil {
			return "", "", err
		}
		p := filepath.Join(home, ".agentswitch", "access", "claude", c.ID+".json")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", "", err
		}
		if err := atomicWriteFile0600(p, b); err != nil {
			return "", "", err
		}
		return p, "", nil
	case "codex":
		keys := make([]string, 0, len(env))
		for k := range env {
			if strings.ContainsAny(env[k], "\r\n") {
				return "", "", fmt.Errorf("配置值不能包含换行符: %s", k)
			}
			// 键不得含 = / 换行（KEY=VALUE 行格式无法安全表达）。
			if k == "" || strings.ContainsAny(k, "=\r\n") {
				return "", "", fmt.Errorf("配置键含非法字符: %q", k)
			}
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var sb strings.Builder
		for _, k := range keys {
			// 值用单引号包裹（' 换成 '\''）：source 时空格/元字符不再被
			// shell 解释——「复制出来的命令在干净 shell 里可直接运行」。
			sb.WriteString(k)
			sb.WriteString("='")
			sb.WriteString(strings.ReplaceAll(env[k], "'", `'\''`))
			sb.WriteString("'\n")
		}
		p := filepath.Join(home, ".agentswitch", "access", "codex", c.ID+".env")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return "", "", err
		}
		if err := atomicWriteFile0600(p, []byte(sb.String())); err != nil {
			return "", "", err
		}
		return "", p, nil
	default:
		return "", "", fmt.Errorf("不支持的接入类型: %q", c.AppType)
	}
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

// LaunchCommand 返回展示/复制用的启动命令：每次调用都重写持久文件
// （保证令牌与模型最新——令牌轮换联动的单配置版）。命令拼装：
// claude：`claude --settings "<json>"`（--settings 未给的键回落
// ~/.claude/settings.json）；codex：`sh -c 'set -a; . "<env>"; set +a;
// exec codex -m "<model>"'`（env 文件内容保持命令跨令牌轮换有效）。
// 嵌入值经 shellDQ/shellSQ 转义——模型名含 ' $ ` " \ 时命令仍安全可执行。
func (s AccessConfigs) LaunchCommand(id string) (string, error) {
	c, err := s.A.Store().AccessConfigs().Get(id)
	if err != nil {
		return "", err
	}
	env, model, err := s.composeFor(c)
	if err != nil {
		return "", err
	}
	claudePath, codexPath, err := s.writePersistEnv(c, env)
	if err != nil {
		return "", err
	}
	switch c.AppType {
	case "claude":
		return `claude --settings "` + shellDQ(claudePath) + `"`, nil
	case "codex":
		inner := `set -a; . "` + shellDQ(codexPath) + `"; set +a; exec codex -m "` + shellDQ(model) + `"`
		return `sh -c '` + shellSQ(inner) + `'`, nil
	default:
		return "", fmt.Errorf("不支持的接入类型: %q", c.AppType)
	}
}

// RegenerateDerived 令牌/端口变更后的联动重写（spec「令牌/端口联动」）：
// 遍历两个 appType 的全部配置重写持久文件；对 Current(appType) 命中的
// 配置额外重写 live 文件。任一配置失败只记 Error 日志并继续其余
// （err 可能含路径，不回显 env/令牌值）；无任何配置时零写入、零日志
// （既有 Gateway 测试不触碰用户目录的前提）。由 Gateway.RegenerateToken
// 与 SaveSettings（端口变更）成功后调用。
func (s AccessConfigs) RegenerateDerived() {
	for _, appType := range []string{"claude", "codex"} {
		cfgs, err := s.A.Store().AccessConfigs().ListByApp(appType)
		if err != nil {
			s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "list", "err", err)
			continue
		}
		if len(cfgs) == 0 {
			continue
		}
		cur, _ := s.A.Store().AccessConfigs().Current(appType)
		for _, c := range cfgs {
			env, model, err := s.composeFor(c)
			if err != nil {
				s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "compose", "err", err)
				continue
			}
			// 持久文件失败不跳过 current 的 live 重写——令牌轮换后 live
			// 仍是 Agent 实际使用的文件，陈旧令牌比持久文件缺失更糟
			// （评审 Minor#8）。
			if _, _, perr := s.writePersistEnv(c, env); perr != nil {
				s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "persist", "err", perr)
			}
			if cur.ID != c.ID {
				continue
			}
			if appType == "claude" {
				err = s.writeClaudeLive(env)
			} else {
				err = s.writeCodexLive(env, model)
			}
			if err != nil {
				s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "live", "err", err)
			}
		}
	}
}

// openTerminalExec 是 OpenTerminal 的终端拉起注入点（测试替换断言脚本，
// 不真开终端）；默认经 osascript 让 Terminal.app 执行 do script。
var openTerminalExec = func(script string) error {
	return exec.Command("osascript", "-e", script).Run()
}

// OpenTerminal 用指定配置拉起一个终端（spec「打开终端」，仅 macOS）：
// 先经 LaunchCommand 刷新持久文件并取启动命令，再组装 AppleScript
// do script；命令内的双引号按 AppleScript 语义转义。不存在的配置报错。
func (s AccessConfigs) OpenTerminal(id string) error {
	cmd, err := s.LaunchCommand(id)
	if err != nil {
		return err
	}
	escaped := strings.ReplaceAll(cmd, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	script := `tell application "Terminal" to do script "` + escaped + `"`
	return openTerminalExec(script)
}
