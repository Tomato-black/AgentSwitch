package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
	env, err := composeAccessEnv(c.AppType, Gateway{A: s.A}.Status(), lm.Name, global, c.Overrides)
	if err != nil {
		return nil, "", err
	}
	return env, lm.Name, nil
}

// validateAccessAppType 校验接入类型白名单 {claude, codex}。
func validateAccessAppType(appType string) error {
	if appType != "claude" && appType != "codex" {
		return fmt.Errorf("不支持的接入类型: %q（仅支持 claude、codex）", appType)
	}
	return nil
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
		if err := json.Unmarshal(b, &root); err != nil {
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
		LogicalModelID: c.LogicalModelID, Overrides: c.Overrides, IsCurrent: c.IsCurrent}
	// 逐条解析逻辑模型名；Get 失败（如已删模型）留空串即可。
	if lm, err := s.A.Store().LogicalModels().Get(c.LogicalModelID); err == nil {
		v.LogicalModelName = lm.Name
	}
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
		ID: id, AppType: appType, Name: name, LogicalModelID: logicalModelID, Overrides: overrides,
	})
	if err != nil {
		return AccessConfigView{}, friendlyAccessSaveErr(err)
	}
	s.A.Log().Info("access config created", "id", id, "app", appType, "actor", "ui", "result", "ok")
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
	c.Name, c.LogicalModelID, c.Overrides = name, logicalModelID, overrides
	if _, err := s.A.Store().AccessConfigs().Save(c); err != nil {
		return friendlyAccessSaveErr(err)
	}
	s.A.Log().Info("access config updated", "id", id, "app", c.AppType, "actor", "ui", "result", "ok")
	return nil
}

// Remove 直接删行（不触碰任何文件——那是后续任务语义）；is_current 行删除后
// current 自然消失（部分唯一索引允许）。
func (s AccessConfigs) Remove(id string) {
	if err := s.A.Store().AccessConfigs().Delete(id); err != nil {
		s.A.Log().Error("access config remove failed", "id", id, "err", err)
		return
	}
	s.A.Log().Info("access config removed", "id", id, "actor", "ui", "result", "ok")
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
	return nil
}
