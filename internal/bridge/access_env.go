package bridge

import (
	"fmt"
	"sort"
	"strings"
)

// baseAccessEnv 返回网关派生基础键（claude 含 SMALL_FAST 默认值）。
func baseAccessEnv(appType string, st Status, modelName string) (map[string]string, error) {
	if modelName == "" {
		return nil, fmt.Errorf("请先选择逻辑模型")
	}
	if err := validateAccessAppType(appType); err != nil {
		return nil, err
	}
	switch appType {
	case "claude":
		return map[string]string{
			"ANTHROPIC_BASE_URL":         fmt.Sprintf("http://127.0.0.1:%d", st.Port),
			"ANTHROPIC_AUTH_TOKEN":       st.Token,
			"ANTHROPIC_MODEL":            modelName,
			"ANTHROPIC_SMALL_FAST_MODEL": modelName,
		}, nil
	case "codex":
		return map[string]string{
			"OPENAI_BASE_URL": fmt.Sprintf("http://127.0.0.1:%d/v1", st.Port),
			"OPENAI_API_KEY":  st.Token,
		}, nil
	}
	return nil, fmt.Errorf("不支持的接入类型: %q（仅支持 claude、codex）", appType)
}

// lockedAccessEnv 返回不可篡改基础键子集（composeAccessEnv 第 4 步回写、
// normalizeAccessEnv 保存剔除共用）。
func lockedAccessEnv(appType string, st Status, modelName string) map[string]string {
	base, err := baseAccessEnv(appType, st, modelName)
	if err != nil {
		return nil
	}
	switch appType {
	case "claude":
		return map[string]string{
			"ANTHROPIC_BASE_URL":   base["ANTHROPIC_BASE_URL"],
			"ANTHROPIC_AUTH_TOKEN": base["ANTHROPIC_AUTH_TOKEN"],
			"ANTHROPIC_MODEL":      base["ANTHROPIC_MODEL"],
		}
	case "codex":
		return base // codex 两键全锁
	}
	return nil
}

// accessSplitOneM 剥离值尾部 "[1M]"（大小写不敏感，返回基名与是否带标记）。
func accessSplitOneM(v string) (string, bool) {
	if len(v) >= 4 && strings.EqualFold(v[len(v)-4:], "[1M]") {
		return v[:len(v)-4], true
	}
	return v, false
}

// mainModelOneM 判断文档 env 的主模型是否为「当前绑定模型名[1M]」的合法
// 1M 声明（半锁定：模型名不可改，仅允许追加 [1M] 后缀）。
func mainModelOneM(modelName string, docEnv map[string]string) bool {
	base, oneM := accessSplitOneM(docEnv["ANTHROPIC_MODEL"])
	return oneM && base == modelName
}

// composeAccessEnv 合成应写入 Agent 的 env：base → global → docEnv → 锁定回写。
// 语义与拆分前完全一致（测试钉死）；claude 的 ANTHROPIC_SMALL_FAST_MODEL 只作
// 默认值，允许被 docEnv 覆盖。ANTHROPIC_MODEL 为半锁定键：文档中的
// 「模型名[1M]」声明在回写时保留后缀（2026-10-21 主模型 1M）。
func composeAccessEnv(appType string, st Status, modelName string, global, docEnv map[string]string) (map[string]string, error) {
	base, err := baseAccessEnv(appType, st, modelName)
	if err != nil {
		return nil, err
	}
	locked := lockedAccessEnv(appType, st, modelName)
	env := make(map[string]string, len(base)+len(global)+len(docEnv))
	for k, v := range base { // 1. 网关派生基础键
		env[k] = v
	}
	for k, v := range global { // 2. 全局 env（access.global.<app_type>）
		env[k] = v
	}
	for k, v := range docEnv { // 3. 配置文档 env（最高优先，但动不了锁定键）
		env[k] = v
	}
	for k, v := range locked { // 4. 重新写回不可篡改基础键（恒定）
		env[k] = v
	}
	if appType == "claude" && mainModelOneM(modelName, docEnv) { // 5. 半锁定主模型的 1M 声明
		env["ANTHROPIC_MODEL"] = modelName + "[1M]"
	}
	return env, nil
}

// normalizeAccessEnv 保存归一化（spec 2026-10-19 §5.2）：1) 锁定键恒剔除
// （网关自动生成，改了无效）——例外：claude 的 ANTHROPIC_MODEL 为半锁定，
// 「绑定模型名[1M]」合法保留（2026-10-21 主模型 1M）；2) 值与「无文档层合成结果」
// （基础键+全局）相同的键剔除——视为未定制、保持继承语义，防止编辑器展示的合成
// 结果在直接保存时把全局键与默认值冻结成覆盖项。
func normalizeAccessEnv(appType string, st Status, modelName string, global, docEnv map[string]string) (map[string]string, error) {
	base, err := baseAccessEnv(appType, st, modelName)
	if err != nil {
		return nil, err
	}
	baseline := make(map[string]string, len(base)+len(global))
	for k, v := range base {
		baseline[k] = v
	}
	for k, v := range global {
		baseline[k] = v
	}
	locked := lockedAccessEnv(appType, st, modelName)
	out := map[string]string{}
	for k, v := range docEnv {
		if _, isLocked := locked[k]; isLocked {
			// 半锁定主模型：仅保留「绑定模型名[1M]」的能力声明（规范为
			// [1M]）；纯名视为未定制、基名不匹配视为篡改，均剔除。
			if appType == "claude" && k == "ANTHROPIC_MODEL" && mainModelOneM(modelName, docEnv) {
				out[k] = modelName + "[1M]"
			}
			continue
		}
		if bv, ok := baseline[k]; ok && bv == v {
			continue
		}
		out[k] = v
	}
	return out, nil
}

// codexEnvKeys 校验并返回字典序的 env 键：键不得含 = / 换行，值不得含
// 换行（KEY=VALUE 行与批处理 set 都无法安全表达）。
func codexEnvKeys(env map[string]string) ([]string, error) {
	keys := make([]string, 0, len(env))
	for k, v := range env {
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("配置值不能包含换行符: %s", k)
		}
		if k == "" || strings.ContainsAny(k, "=\r\n") {
			return nil, fmt.Errorf("配置键含非法字符: %q", k)
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}
