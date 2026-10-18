package bridge

import (
	"fmt"

	"AgentSwitch/internal/app"
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
	default:
		return nil, fmt.Errorf("不支持的接入类型: %q（仅支持 claude、codex）", appType)
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
