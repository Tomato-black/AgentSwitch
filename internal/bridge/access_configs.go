package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"slices"
	"strings"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/storage"
)

// accessPlatform 是接入相关平台分支的注入点（默认当前平台）：测试在
// 非 Windows 主机上替换它，覆盖 Windows 路径/启动器/终端分派。
var accessPlatform = runtime.GOOS

// AccessConfigs 是接入配置管理面（agent-access-config spec）：把
// 「逻辑模型 × Agent」的配置合成与投影整理为桌面可调用的服务。
// 本文件只落合成纯函数 composeAccessEnv（Task 5/10 的 Apply/Export
// 复用）；Home 为 "" 时业务方法回落 os.UserHomeDir()（路径方法属后续任务）。
type AccessConfigs struct {
	A    *app.App
	Home string
}

// composeFor 解析 c 绑定的逻辑模型名与全局配置，合成应写入 Agent 的 env，
// 并返回解析后的整份配置文档（doc 顶层键由 writeClaudeLive/writePersistEnv
// 投影）。返回 model 为逻辑模型对外名（codex 写 config.toml 的 model 键与
// 启动命令用；claude 的模型名已在 env 内）。绑定的逻辑模型不存在时报中文
// 错误提示改绑；全局配置读取失败/文档非法直接返回错误。本方法不写任何含
// env/令牌的日志。
func (s AccessConfigs) composeFor(c storage.AccessConfig) (map[string]string, map[string]any, string, error) {
	lm, err := s.A.Store().LogicalModels().Get(c.LogicalModelID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, nil, "", fmt.Errorf("绑定的逻辑模型不存在，请改绑")
		}
		return nil, nil, "", err
	}
	raw, err := s.A.Store().Settings().Get("access.global." + c.AppType)
	if err != nil {
		return nil, nil, "", err
	}
	var global map[string]string
	if raw != "" { // Settings Get 缺失返回 ("", nil) → 视为无全局配置
		if err := json.Unmarshal([]byte(raw), &global); err != nil {
			return nil, nil, "", err
		}
	}
	doc, docEnv, err := parseAccessDoc(c.AppType, c.Settings)
	if err != nil {
		return nil, nil, "", err
	}
	env, err := composeAccessEnv(c.AppType, Gateway{A: s.A}.Status(), lm.Name, global, docEnv)
	if err != nil {
		return nil, nil, "", err
	}
	return env, doc, lm.Name, nil
}

// PreviewDoc 返回「应用该文档后 Agent env 的样子」：base → global → 文档 env →
// 锁定回写；文档顶层键原样保留。编辑器打开与主模型切换时调用。逻辑模型必须
// 存在。令牌键渲染真实网关令牌（2026-10-21 用户裁定，取代 spec 2026-10-19
// §3.4 的遮蔽占位符）；保存路径仍剔除锁定键，令牌永不落库。
func (s AccessConfigs) PreviewDoc(appType, logicalModelID, settingsJSON string) (string, error) {
	lm, err := s.A.Store().LogicalModels().Get(logicalModelID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "", fmt.Errorf("逻辑模型不存在")
		}
		return "", err
	}
	doc, env, err := parseAccessDoc(appType, settingsJSON)
	if err != nil {
		return "", err
	}
	global, err := s.GlobalConfig(appType)
	if err != nil {
		return "", err
	}
	st := Gateway{A: s.A}.Status()
	composed, err := composeAccessEnv(appType, st, lm.Name, global, env)
	if err != nil {
		return "", err
	}
	envAny := make(map[string]any, len(composed))
	for k, v := range composed {
		envAny[k] = v
	}
	doc["env"] = envAny
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// PreviewCodexConfig 返回「应用该文档后 Codex config.toml 的样子」：Get 逻辑
// 模型（不存在报中文错误）→ parseAccessDoc("codex") → GlobalConfig →
// composeAccessEnv（含真实网关令牌）→ projectCodex。projectCodex 会读取并合并
// 现有 <home>/.codex/config.toml 的用户键，但本方法**不写任何文件**（不建目录、
// 不落 config.toml、不落模型目录），仅供 UI 只读预览。保存路径仍由 Apply
// 负责写入。
func (s AccessConfigs) PreviewCodexConfig(logicalModelID, settingsJSON string) (string, error) {
	lm, err := s.A.Store().LogicalModels().Get(logicalModelID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "", fmt.Errorf("逻辑模型不存在")
		}
		return "", err
	}
	doc, env, err := parseAccessDoc("codex", settingsJSON)
	if err != nil {
		return "", err
	}
	global, err := s.GlobalConfig("codex")
	if err != nil {
		return "", err
	}
	composed, err := composeAccessEnv("codex", Gateway{A: s.A}.Status(), lm.Name, global, env)
	if err != nil {
		return "", err
	}
	cfgBytes, _, err := s.projectCodex(composed, lm.Name, doc)
	if err != nil {
		return "", err
	}
	return string(cfgBytes), nil
}

// prepareDoc 解析+归一化接入文档并序列化（Create/Update 共用）：parseAccessDoc
// 校验 → （Codex）模型列表语义校验与回退固化、子代理/自动审批归属校验 →
// normalizeAccessEnv 剔除锁定键与未定制键 → env 写回文档 → 2 空格缩进。
func (s AccessConfigs) prepareDoc(appType, modelName, settingsJSON string) (string, error) {
	doc, env, err := parseAccessDoc(appType, settingsJSON)
	if err != nil {
		return "", err
	}
	if appType == "claude_desktop" {
		// Desktop 无 env 合成/归一化层：结构校验通过即原样规范化入库
		//（引用的逻辑模型存在性在 Apply 时校验，与 composeFor 口径一致）。
		b, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	if appType == "codex" {
		// Codex 模型列表语义校验（spec 2026-10-27 §4.2）：显式列表须存在/
		// 启用且首项为绑定模型；缺省则在此固化回退列表，旧 env-only 配置
		// 保存后即拥有显式列表。子代理/自动审批模型必须落在列表内。
		settings, err := parseCodexSettings(doc)
		if err != nil {
			return "", err
		}
		models, err := s.resolveCodexModels(modelName, settings)
		if err != nil {
			return "", err
		}
		if settings.SubagentModel != "" && !slices.Contains(models, settings.SubagentModel) {
			return "", fmt.Errorf("子代理模型 %s 不在模型列表中", settings.SubagentModel)
		}
		if settings.AutoReviewModel != "" && !slices.Contains(models, settings.AutoReviewModel) {
			return "", fmt.Errorf("自动审批模型 %s 不在模型列表中", settings.AutoReviewModel)
		}
		doc["models"] = toAnyStrings(models)
		// codex 字段去空白后写回；未知键已在 parseAccessDoc 拒绝。
		if v, ok := doc["codex"]; ok && v != nil {
			doc["codex"] = map[string]any{
				"subagent_model":            settings.SubagentModel,
				"subagent_reasoning_effort": settings.SubagentReasoningEffort,
				"auto_review_model":         settings.AutoReviewModel,
			}
		}
	}
	global, err := s.GlobalConfig(appType)
	if err != nil {
		return "", err
	}
	norm, err := normalizeAccessEnv(appType, Gateway{A: s.A}.Status(), modelName, global, env)
	if err != nil {
		return "", err
	}
	doc["env"] = norm
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// RebaseAccessDoc 在合成基线变化（切换主模型/更新全局配置）时，从文档 env 中
// 删除「值等于旧基线」的继承键，避免旧派生值在新基线的保存归一化中被误判为
// 定制项而冻结。解析失败返回 error（调用方保留原文）。
func RebaseAccessDoc(appType, settingsJSON string, baseline map[string]string) (string, error) {
	doc, env, err := parseAccessDoc(appType, settingsJSON)
	if err != nil {
		return "", err
	}
	for k, v := range baseline {
		if ev, ok := env[k]; ok && ev == v {
			delete(env, k)
		}
	}
	doc["env"] = env
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// validateAccessAppType 校验接入类型白名单 {claude, codex}。
func validateAccessAppType(appType string) error {
	if appType != "claude" && appType != "codex" && appType != "claude_desktop" {
		return fmt.Errorf("不支持的接入类型: %q（仅支持 claude、codex、claude_desktop）", appType)
	}
	return nil
}

// parseAccessDoc 解析接入配置文档（spec 2026-10-19 §5.2、2026-10-27 §4.2）：
// 根必须是 JSON 对象（空串视为空对象）；env 缺省/null 视为空对象、必须是
// 字符串值对象；Codex 仅允许 env、models、codex 三个顶层键，models/codex 的
// 结构校验见 parseCodexSettings。UseNumber 保留大整数原文（避免 float64 往返
// 丢精度）；尾随垃圾/多余 JSON 值拒绝。返回解析后的文档与提取的 env 字符串 map。
func parseAccessDoc(appType, raw string) (map[string]any, map[string]string, error) {
	if err := validateAccessAppType(appType); err != nil {
		return nil, nil, err
	}
	if appType == "claude_desktop" {
		// Desktop 文档是角色映射（无 env 层、无自由键），解析与校验见
		// parseDesktopAccessDoc；env 恒空。
		doc, err := parseDesktopAccessDoc(raw)
		return doc, nil, err
	}
	doc := map[string]any{}
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.UseNumber()
		var root any
		if err := dec.Decode(&root); err != nil {
			return nil, nil, fmt.Errorf("配置 JSON 格式错误，请检查语法")
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, nil, fmt.Errorf("配置 JSON 格式错误，请检查语法")
		}
		m, ok := root.(map[string]any)
		if !ok { // 合法 "null"/"[]" 也拒绝：根必须是对象
			return nil, nil, fmt.Errorf("配置必须是 JSON 对象")
		}
		doc = m
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
			if k != "env" && k != "models" && k != "codex" {
				return nil, nil, fmt.Errorf("Codex 配置仅支持 env、models、codex 键")
			}
		}
		if _, err := parseCodexSettings(doc); err != nil {
			return nil, nil, err
		}
	}
	return doc, env, nil
}

// projectCodex 把合成 env、绑定的默认模型与接入文档投影为 Codex 的
// config.toml 与模型目录字节（不落盘，供 Apply 写入与 UI 预览共用）。
// 投影顺序（spec 2026-10-27 §4.3、§5）：
//   - 先读取并解析现有 <home>/.codex/config.toml：解析失败在模型列表/目录
//     生成之前中止，失败路径不产生半状态；文件不存在视为空表；未知用户
//     键/表语义保留（TOML 注释允许丢失）。
//   - 模型列表取文档显式 models；缺省回退「绑定模型 + 全部启用模型」并按名
//     排序（resolveCodexModels），每项必须存在且启用——目录只为可用模型
//     生成条目。目录条目取这些模型的元数据（codexCatalogModels），自动审批
//     覆盖取 codex.auto_review_model（空则不写，Codex 回落主模型）。
//   - 受管键：model=models[0]、model_provider="agentswitch"、
//     model_catalog_json=agentswitch-model-catalog.json（桌面版模型选择器
//     只认该文件，不读网关 /v1/models）、disable_response_storage=true、
//     web_search="disabled"、model_context_window/models[0] 的
//     ContextWindow 与 model_auto_compact_token_limit=窗口的 90%（窗口未
//     声明即 0 时两个键都不写也不删，保留用户设置）、
//     model_providers.agentswitch 子表
//     {name: "agentswitch"（Codex 0.143+ 必填且非空）,
//     base_url: env["OPENAI_BASE_URL"], wire_api: "responses"
//     （Codex 0.143+ 仅支持 Responses 线路）, requires_openai_auth: false,
//     experimental_bearer_token: env["OPENAI_API_KEY"]}（同键替换，子表内
//     非受管键保留）。
//
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
// Settings 为整份配置文档原文（含归一化后的 env）。
type AccessConfigView struct {
	ID, Name, AppType, LogicalModelID, LogicalModelName string
	Settings                                            string
	IsCurrent                                           bool
}

func (s AccessConfigs) view(c storage.AccessConfig) AccessConfigView {
	v := AccessConfigView{ID: c.ID, Name: c.Name, AppType: c.AppType,
		LogicalModelID: c.LogicalModelID, Settings: c.Settings, IsCurrent: c.IsCurrent}
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
// 必须存在；settingsJSON 经 prepareDoc 归一化后入库（锁定键剔除、未定制键
// 剔除、文档顶层键保留）；同 app 重名译「配置名已存在」。审计行绝不记录
// env/settings 值或 token（spec §9 脱敏）。
func (s AccessConfigs) Create(appType, name, logicalModelID, settingsJSON string) (AccessConfigView, error) {
	if err := validateAccessAppType(appType); err != nil {
		return AccessConfigView{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return AccessConfigView{}, fmt.Errorf("配置名不能为空")
	}
	lm, err := s.A.Store().LogicalModels().Get(logicalModelID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return AccessConfigView{}, fmt.Errorf("逻辑模型不存在")
		}
		return AccessConfigView{}, err
	}
	raw, err := s.prepareDoc(appType, lm.Name, settingsJSON)
	if err != nil {
		return AccessConfigView{}, err
	}
	id := storage.NewID()
	saved, err := s.A.Store().AccessConfigs().Save(storage.AccessConfig{
		ID: id, AppType: appType, Name: name, LogicalModelID: logicalModelID, Settings: raw,
	})
	if err != nil {
		return AccessConfigView{}, friendlyAccessSaveErr(err)
	}
	s.A.Log().Info("access config created", "id", id, "app", appType, "actor", "ui", "result", "ok")
	s.RegenerateDerived() // 新配置即落持久文件，其余派生文件同步刷新（spec「保存/更新时同步生成」）
	return s.view(saved), nil
}

// Update 改配置字段并保 ID（非 Remove+Create）：直接 repo.Save 覆盖，
// 保留原 app_type / is_current / sort_order；settingsJSON 经 prepareDoc
// 归一化后覆盖 Settings（语义同 Create）。
func (s AccessConfigs) Update(id, name, logicalModelID, settingsJSON string) error {
	c, err := s.A.Store().AccessConfigs().Get(id)
	if err != nil {
		return err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("配置名不能为空")
	}
	lm, err := s.A.Store().LogicalModels().Get(logicalModelID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return fmt.Errorf("逻辑模型不存在")
		}
		return err
	}
	raw, err := s.prepareDoc(c.AppType, lm.Name, settingsJSON)
	if err != nil {
		return err
	}
	c.Name, c.LogicalModelID = name, logicalModelID
	c.Settings = raw
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
// writeClaudeLive(doc, env)；codex→writeCodexLive(env, model, doc)）→ SetCurrent
// (appType, id)。任一步失败即停——含备份失败（不写 live、不置 current），
// 失败路径不产生半状态；composeFor 的逻辑模型缺失错误直接透传。
// 审计行绝不记录 env/overrides 值或 token（spec §9 脱敏）。
func (s AccessConfigs) Apply(id string) error {
	c, err := s.A.Store().AccessConfigs().Get(id)
	if err != nil {
		return err
	}
	if c.AppType == "claude_desktop" {
		// Desktop 无 env 合成层，独立分流（composeFor 不适用）。
		if err := s.backupLive(c.AppType); err != nil {
			return err
		}
		if err := s.applyDesktop(c); err != nil {
			return err
		}
		if err := s.A.Store().AccessConfigs().SetCurrent(c.AppType, id); err != nil {
			return err
		}
		s.A.Log().Info("access config applied", "id", id, "app", c.AppType, "actor", "ui", "result", "ok")
		return nil
	}
	env, doc, model, err := s.composeFor(c)
	if err != nil {
		return err
	}
	if err := s.backupLive(c.AppType); err != nil {
		return err
	}
	switch c.AppType {
	case "claude":
		err = s.writeClaudeLive(doc, env)
	case "codex":
		err = s.writeCodexLive(env, model, doc)
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

// persistFiles 是 writePersistEnv 的产物：claude/codex 为对应平台持久文件，
// launcher 为 Windows .cmd 启动器（非 Windows 为空）。按 appType 只生成对应
// 一项持久文件，其余为空串。
type persistFiles struct {
	claude   string
	codex    string
	launcher string
}

// writeWindowsLauncher 生成 Windows .cmd 启动器（CRLF）：
//
//	claude → claude --settings "<json>" %*
//	codex  → set "KEY=VALUE"（字典序）+ codex -m "<model>" %*
//
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
			env, doc, model, err := s.composeFor(c)
			if err != nil {
				s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "compose", "err", err)
				continue
			}
			// 持久文件失败不跳过 current 的 live 重写——令牌轮换后 live
			// 仍是 Agent 实际使用的文件，陈旧令牌比持久文件缺失更糟
			// （评审 Minor#8）。
			if _, perr := s.writePersistEnv(c, env, doc, model); perr != nil {
				s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "persist", "err", perr)
			}
			if cur.ID != c.ID {
				continue
			}
			if appType == "claude" {
				err = s.writeClaudeLive(doc, env)
			} else {
				err = s.writeCodexLive(env, model, doc)
			}
			if err != nil {
				s.A.Log().Error("access derived refresh failed", "app", appType, "stage", "live", "err", err)
			}
		}
	}
}

// openTerminalExec 是 OpenTerminal 的终端拉起注入点（测试替换断言参数，
// 不真开终端）；默认执行平台终端命令。
var openTerminalExec = func(name string, args ...string) error {
	return exec.Command(name, args...).Run()
}
