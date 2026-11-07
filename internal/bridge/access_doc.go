// access_doc.go 是接入文档管道：接入 settings JSON 的解析/归一化/补丁与
// 预览合成（Claude env 合成、Codex config.toml + 模型目录预览），供 CRUD/
// 应用与桌面编辑器共用。
package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"AgentSwitch/internal/storage"
)

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
	global, err := globalEnv(s.A, c.AppType) // claude 恒空（全局配置下线，2026-11-07）
	if err != nil {
		return nil, nil, "", err
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
			if k != "env" && k != "models" && k != "codex" && k != "model_meta" {
				return nil, nil, fmt.Errorf("Codex 配置仅支持 env、models、codex、model_meta 键")
			}
		}
		if _, err := parseCodexSettings(doc); err != nil {
			return nil, nil, err
		}
		// model_meta 结构 + 取值早校验（与目录生成同一套规则）：坏值不能
		// 静默入库，等到「应用」才失败。
		metas, err := codexModelMetaFromDoc(doc)
		if err != nil {
			return nil, nil, err
		}
		for name, o := range metas {
			stub := codexCatalogModel{Name: name}
			if err := applyCodexMetaOverride(&stub, o); err != nil {
				return nil, nil, err
			}
		}
	}
	return doc, env, nil
}
