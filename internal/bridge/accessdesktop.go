package bridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"AgentSwitch/internal/storage"
)

// Claude Desktop 接入（2026-10-22 用户决策，参照 cc-switch 的 inferenceGateway
// profile 方案）：Desktop 配置文件写入网关 profile 三键 + inferenceModels，
// 请求打到 AgentSwitch 网关的 Anthropic 入站；Desktop 只接受 claude-* 角色
// 路由 ID，网关侧按别名表把角色名解析回逻辑模型（settings 键
// access.desktop_aliases，last-applied-wins）。
const (
	desktopConfigFileName = "claude_desktop_config.json"
)

// desktopRoleRouteIDs 是接入文档角色名 → Desktop 路由 ID 的固定映射。
// 白名单取自 Desktop 1.12603.1+ fail-all 校验（["sonnet","opus","haiku",
// "fable","mythos"]，参照 cc-switch；mythos 未公开发布不暴露）。
var desktopRoleRouteIDs = map[string]string{
	"sonnet": "claude-sonnet-5",
	"opus":   "claude-opus-5",
	"haiku":  "claude-haiku-4-5",
	"fable":  "claude-fable-5",
}

// desktopRoleOrder 是 UI/写入的角色固定顺序。
var desktopRoleOrder = []string{"sonnet", "opus", "haiku", "fable"}

// DesktopRoute 是一条已解析的角色映射（Apply 与 UI 摘要共用）。
type DesktopRoute struct {
	Role    string
	RouteID string
	Model   string
	OneM    bool
}

// DesktopRoutesOf 解析角色映射文档为路由表（结构校验同 Apply 路径）。
func DesktopRoutesOf(docJSON string) ([]DesktopRoute, error) {
	routes, _, err := composeDesktopRoutes(docJSON)
	return routes, err
}

// parseDesktopAccessDoc 解析 Claude Desktop 接入文档（角色映射，无 env 层）：
// 根必须是 JSON 对象（空串/null 视为空对象）；键只允许 desktopRoleOrder 中的
// 角色；值必须是对象 {"model": 非空字符串, "oneM"?: bool}——空对象 = 未启用。
// 其余键（含 env）一律拒绝：Desktop profile 由 Apply 生成，不接受自由键。
func parseDesktopAccessDoc(raw string) (map[string]any, error) {
	doc := map[string]any{}
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		dec := json.NewDecoder(strings.NewReader(trimmed))
		dec.UseNumber()
		var root any
		if err := dec.Decode(&root); err != nil {
			return nil, fmt.Errorf("配置 JSON 格式错误，请检查语法")
		}
		var extra any
		if err := dec.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("配置 JSON 格式错误，请检查语法")
		}
		m, ok := root.(map[string]any)
		if !ok {
			if root == nil { // 合法 null → 视为空对象
				return map[string]any{}, nil
			}
			return nil, fmt.Errorf("配置必须是 JSON 对象")
		}
		doc = m
	}
	for k, v := range doc {
		if _, isRole := desktopRoleRouteIDs[k]; !isRole {
			return nil, fmt.Errorf("未知角色 %q（仅支持 sonnet/opus/haiku/fable）", k)
		}
		if v == nil { // null 视为未启用，允许
			continue
		}
		role, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("角色 %s 的配置必须是对象", k)
		}
		for rk, rv := range role {
			switch rk {
			case "model":
				s, ok := rv.(string)
				if !ok || strings.TrimSpace(s) == "" {
					return nil, fmt.Errorf("角色 %s 的 model 必须是非空字符串", k)
				}
			case "oneM":
				if _, ok := rv.(bool); !ok {
					return nil, fmt.Errorf("角色 %s 的 oneM 必须是布尔值", k)
				}
			default:
				return nil, fmt.Errorf("角色 %s 含未知键 %q", k, rk)
			}
		}
	}
	return doc, nil
}

// composeDesktopRoutes 解析文档为角色映射与别名表：空对象/null 视为未启用
// 跳过；model 为空字符串拒绝（结构校验层不依赖存储）。顺序按 desktopRoleOrder。
func composeDesktopRoutes(docJSON string) ([]DesktopRoute, map[string]string, error) {
	doc, err := parseDesktopAccessDoc(docJSON)
	if err != nil {
		return nil, nil, err
	}
	var routes []DesktopRoute
	aliases := map[string]string{}
	for _, role := range desktopRoleOrder {
		v, ok := doc[role]
		if !ok || v == nil {
			continue
		}
		m := v.(map[string]any)
		rawModel, hasModel := m["model"]
		if !hasModel {
			continue // 空对象 = 未启用
		}
		model, _ := rawModel.(string)
		model = strings.TrimSpace(model)
		if model == "" {
			return nil, nil, fmt.Errorf("角色 %s 的 model 必须是非空字符串", role)
		}
		oneM, _ := m["oneM"].(bool)
		routeID := desktopRoleRouteIDs[role]
		routes = append(routes, DesktopRoute{Role: role, RouteID: routeID, Model: model, OneM: oneM})
		aliases[routeID] = model
	}
	return routes, aliases, nil
}

// desktopLivePath 返回 Claude Desktop 配置文件路径（相对注入的 home，
// 与 backupLive 的基准一致）：Linux <home>/.config/Claude、macOS
// <home>/Library/Application Support/Claude（与 cc-switch 平台映射一致；
// Windows 未实现，随当前平台支持范围）。
func desktopLivePath(home string) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "Claude", desktopConfigFileName)
	}
	return filepath.Join(home, ".config", "Claude", desktopConfigFileName)
}

// desktopPlan 是 Apply 的合成产物。
type desktopPlan struct {
	Routes  []DesktopRoute
	Aliases map[string]string
}

// composeDesktop 按配置文档合成 Desktop 接入计划：解析角色映射，校验每个
// 启用角色引用的逻辑模型存在（与 composeFor 的「改绑」语义一致）。
func (s AccessConfigs) composeDesktop(c storage.AccessConfig) (desktopPlan, error) {
	routes, aliases, err := composeDesktopRoutes(c.Settings)
	if err != nil {
		return desktopPlan{}, err
	}
	if len(routes) == 0 {
		return desktopPlan{}, fmt.Errorf("至少启用一个角色（sonnet/opus/haiku/fable）")
	}
	lms := s.A.Store().LogicalModels()
	for _, r := range routes {
		if _, err := lms.GetByName(r.Model); err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				return desktopPlan{}, fmt.Errorf("角色 %s 绑定的逻辑模型 %q 不存在，请改绑", r.Role, r.Model)
			}
			return desktopPlan{}, err
		}
	}
	return desktopPlan{Routes: routes, Aliases: aliases}, nil
}

// writeClaudeDesktopLive 把网关 profile patch 合并进 Desktop 配置：受管五键
// （inferenceProvider/inferenceGatewayBaseUrl/inferenceGatewayApiKey/
// inferenceGatewayAuthScheme/inferenceModels）同键替换，其余键（MCP 等）
// 原样保留；文件不存在建新、存在但非法 JSON 直接报错且绝不改动原文件；
// 0600 原子写回。inferenceModels 按 cc-switch 形状生成：默认纯字符串名；
// oneM 角色输出 {"name","supports1m":true,"prefer1m":true}（prefer1m 让
// Desktop 只列原名、交内嵌 CLI 时自动补 [1m]）。本方法不写任何日志（含令牌）。
func (s AccessConfigs) writeClaudeDesktopLive(plan desktopPlan, baseURL, token string) error {
	home := s.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return err
		}
	}
	path := desktopLivePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	root := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&root); err != nil {
			return err
		}
		if root == nil {
			root = map[string]any{}
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	models := make([]any, 0, len(plan.Routes))
	for _, r := range plan.Routes {
		if r.OneM {
			models = append(models, map[string]any{
				"name": r.RouteID, "supports1m": true, "prefer1m": true,
			})
		} else {
			models = append(models, r.RouteID)
		}
	}
	root["inferenceProvider"] = "gateway"
	root["inferenceGatewayBaseUrl"] = baseURL
	root["inferenceGatewayApiKey"] = token
	root["inferenceGatewayAuthScheme"] = "bearer"
	root["inferenceModels"] = models
	b, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile0600(path, b)
}

// applyDesktop 执行 Desktop 接入的应用：合成计划 → patch 写入 Desktop 配置
// → 持久化别名表（网关 candidates 解析用）。备份由 Apply 调用方先行完成。
func (s AccessConfigs) applyDesktop(c storage.AccessConfig) error {
	plan, err := s.composeDesktop(c)
	if err != nil {
		return err
	}
	st := Gateway{A: s.A}.Status()
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", st.Port)
	if err := s.writeClaudeDesktopLive(plan, baseURL, st.Token); err != nil {
		return err
	}
	return s.saveDesktopAliases(plan.Aliases)
}

// saveDesktopAliases 把 last-applied 别名表持久化（settings 键，网关
// candidates 解析用）；传空表清空。
func (s AccessConfigs) saveDesktopAliases(aliases map[string]string) error {
	if aliases == nil {
		aliases = map[string]string{}
	}
	b, err := json.Marshal(aliases)
	if err != nil {
		return err
	}
	return s.A.Store().Settings().Set(storage.DesktopAliasesSettingsKey, string(b))
}

// desktopConfigDirNote 给 UI 提示的实际写入路径（展示用）。
func desktopConfigDirNote(home string) string {
	if home == "" {
		if h, err := os.UserHomeDir(); err == nil {
			home = h
		}
	}
	return desktopLivePath(home)
}
