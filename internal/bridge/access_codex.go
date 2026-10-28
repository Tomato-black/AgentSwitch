package bridge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/pelletier/go-toml/v2"
)

// auth.json 不读也不写：它是官方 ChatGPT 登录文件，接入配置只把第三方 Key
// 写进 config.toml 的 provider 表（Codex 0.149 起自定义 provider 不再从
// auth.json 继承凭据；桌面版从 Dock 启动也不继承 shell env，故不能用
// env_key）。模型目录模板取不到时整次投影中止。本方法不写任何日志——写入
// 值含网关令牌（spec §9 脱敏）。
func (s AccessConfigs) projectCodex(env map[string]string, model string, doc map[string]any) (cfgBytes, catalogBytes []byte, err error) {
	home := s.Home
	if home == "" {
		if home, err = os.UserHomeDir(); err != nil {
			return nil, nil, err
		}
	}
	cfgPath := filepath.Join(home, ".codex", "config.toml")

	cfg := map[string]any{}
	if b, err := os.ReadFile(cfgPath); err == nil {
		// 解析失败必须在模型解析/目录生成前返回，原文件保持不动。
		if err := toml.Unmarshal(b, &cfg); err != nil {
			return nil, nil, fmt.Errorf("config.toml 解析失败，请修复后重试: %w", err)
		}
		if cfg == nil { // 空文档 → 视为空表
			cfg = map[string]any{}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}

	settings := codexSettingsFromDoc(doc)
	names, err := s.resolveCodexModels(model, settings)
	if err != nil {
		return nil, nil, err
	}
	models, err := s.codexCatalogModels(names)
	if err != nil {
		return nil, nil, err
	}
	template, err := codexModelCatalogTemplateLoader()
	if err != nil {
		return nil, nil, err
	}
	catalogBytes, err = buildCodexModelCatalog(template, models, settings.AutoReviewModel)
	if err != nil {
		return nil, nil, err
	}

	cfg["model"] = names[0] // models[0] 即绑定的默认模型
	cfg["model_provider"] = "agentswitch"
	cfg["model_catalog_json"] = codexModelCatalogFileName
	cfg["disable_response_storage"] = true
	cfg["web_search"] = "disabled"
	// 1M 上下文与自动压缩阈值（spec §4.5）：默认模型声明了窗口才写；0 表示
	// 未声明——不写也不删，保留用户已有设置。
	if len(models) > 0 && models[0].ContextWindow > 0 {
		cfg["model_context_window"] = models[0].ContextWindow
		cfg["model_auto_compact_token_limit"] = models[0].ContextWindow * 9 / 10
	}
	// [agents] 子代理受管键（spec §4.3）：非空写、空值删；表内其他用户键
	// 保留，删空后整表删除。投影是唯一 live 写路径且可能读到旧库文档，
	// 写前按解析后的目录模型名/思考等级白名单二次校验——无效值必须报错，
	// 不能作为 Codex 不认识的毒值落进 config.toml。
	agents := map[string]any{}
	if v, ok := cfg["agents"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("config.toml 的 agents 不是表，请修复后重试")
		}
		agents = m
	}
	if settings.SubagentModel != "" {
		if !slices.ContainsFunc(models, func(m codexCatalogModel) bool { return m.Name == settings.SubagentModel }) {
			return nil, nil, fmt.Errorf("子代理模型 %s 不在模型列表中，请重新选择", settings.SubagentModel)
		}
		agents["default_subagent_model"] = settings.SubagentModel
	} else {
		delete(agents, "default_subagent_model")
	}
	if settings.SubagentReasoningEffort != "" {
		if !reasoningLevelWhitelist[settings.SubagentReasoningEffort] {
			return nil, nil, fmt.Errorf("未知思考等级：%s，请重新选择子代理思考等级", settings.SubagentReasoningEffort)
		}
		agents["default_subagent_reasoning_effort"] = settings.SubagentReasoningEffort
	} else {
		delete(agents, "default_subagent_reasoning_effort")
	}
	if len(agents) == 0 {
		delete(cfg, "agents")
	} else {
		cfg["agents"] = agents
	}
	// model_providers 子表：已存在但非表则报错（不猜测修复）。
	providers := map[string]any{}
	if v, ok := cfg["model_providers"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("config.toml 的 model_providers 不是表")
		}
		providers = m
	}
	agent := map[string]any{}
	if v, ok := providers["agentswitch"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("config.toml 的 model_providers.agentswitch 不是表")
		}
		agent = m
	}
	agent["name"] = "agentswitch"
	agent["base_url"] = env["OPENAI_BASE_URL"]
	agent["wire_api"] = "responses"
	agent["requires_openai_auth"] = false
	agent["experimental_bearer_token"] = env["OPENAI_API_KEY"]
	providers["agentswitch"] = agent
	cfg["model_providers"] = providers

	if cfgBytes, err = toml.Marshal(cfg); err != nil {
		return nil, nil, err
	}
	return cfgBytes, catalogBytes, nil
}

// writeCodexLive 把投影结果落到 <home>/.codex/config.toml 与模型目录
// （Codex CLI / 桌面版 / IDE 插件共用这一份文件）。语义：先「读-解析-投影」
// 全部成功后再落盘，解析/校验失败绝不改动原文件（不猜测修复）；目录先写、
// config.toml 后写（配置指向目录，目录写失败时配置保持原样）；两者均 0600
// 原子写。
func (s AccessConfigs) writeCodexLive(env map[string]string, model string, doc map[string]any) error {
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
	cfgBytes, catalogBytes, err := s.projectCodex(env, model, doc)
	if err != nil {
		return err
	}
	// 目录先写：config.toml 指向它；目录写失败时配置保持原样。
	if err := atomicWriteFile0600(filepath.Join(dir, codexModelCatalogFileName), catalogBytes); err != nil {
		return err
	}
	return atomicWriteFile0600(filepath.Join(dir, "config.toml"), cfgBytes)
}
