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
	"sort"
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
	// Desktop 3P 网关模式（参照 cc-switch）：两个配置文件设
	// deploymentMode=3p，网关 profile 落 configLibrary/<id>.json，
	// _meta.json 以 appliedId 激活。PROFILE_ID 与 cc-switch 一致
	// （Desktop 按 id 匹配 _meta；同机并用两家工具时 last-wins）。
	desktopProfileID        = "00000000-0000-4000-8000-000000157210"
	desktopProfileName      = "AgentSwitch"
	desktopConfigLibraryDir = "configLibrary"
)

// desktopManagedProfileKeys 是网关 profile 的受管键（写入 profile 文件、
// 从旧版普通配置中清理——首次实现误写进 Claude/ 配置的遗留键）。
var desktopManagedProfileKeys = []string{
	"coworkEgressAllowedHosts", "disableDeploymentModeChooser",
	"inferenceGatewayApiKey", "inferenceGatewayAuthScheme",
	"inferenceGatewayBaseUrl", "inferenceProvider", "inferenceModels",
}

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

// desktopPaths 是 Desktop 3P 网关模式涉及的 4 个文件路径（相对注入的
// home，与 backupLive 基准一致）：Linux <home>/.config、macOS
// <home>/Library/Application Support、Windows %LOCALAPPDATA%
// （cc-switch 平台映射，2026-10-26 补齐 Windows）。
type desktopPaths struct {
	normalConfig string // Claude/claude_desktop_config.json（官方模式配置）
	threepConfig string // Claude-3p/claude_desktop_config.json（3P 模式配置）
	profile      string // Claude-3p/configLibrary/<PROFILE_ID>.json（网关 profile）
	meta         string // Claude-3p/configLibrary/_meta.json（应用登记）
}

func desktopPathsFor(home string) desktopPaths {
	if accessPlatform == "windows" {
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return windowsDesktopPaths(localAppData)
	}
	base := filepath.Join(home, ".config")
	if accessPlatform == "darwin" {
		base = filepath.Join(home, "Library", "Application Support")
	}
	return desktopPathsFromDirs(filepath.Join(base, "Claude"), filepath.Join(base, "Claude-3p"))
}

func desktopPathsFromDirs(normalDir, threepDir string) desktopPaths {
	lib := filepath.Join(threepDir, desktopConfigLibraryDir)
	return desktopPaths{
		normalConfig: filepath.Join(normalDir, desktopConfigFileName),
		threepConfig: filepath.Join(threepDir, desktopConfigFileName),
		profile:      filepath.Join(lib, desktopProfileID+".json"),
		meta:         filepath.Join(lib, "_meta.json"),
	}
}

// windowsDesktopPaths 镜像 cc-switch 的 Windows 解析：Claude Desktop 的
// 配置在 %LOCALAPPDATA%（不是 Roaming）。目录名带版本/渠道后缀时按
// pickWindowsClaudeDir 的规则挑选。
func windowsDesktopPaths(localAppData string) desktopPaths {
	normal := pickWindowsClaudeDir(localAppData, false)
	if normal == "" {
		normal = filepath.Join(localAppData, "Claude")
	}
	threep := pickWindowsClaudeDir(localAppData, true)
	if threep == "" {
		threep = filepath.Join(localAppData, "Claude-3p")
	}
	return desktopPathsFromDirs(normal, threep)
}

// pickWindowsClaudeDir 挑选 LOCALAPPDATA 下的 Claude 配置目录（cc-switch
// 规则）：先精确匹配 Claude / Claude-3p；否则取以 Claude 开头、且
// 「是否含 -3p」匹配的目录中字典序第一个；都没有返回空串。
func pickWindowsClaudeDir(localAppData string, threep bool) string {
	exactName := "Claude"
	if threep {
		exactName = "Claude-3p"
	}
	exact := filepath.Join(localAppData, exactName)
	if fi, err := os.Stat(exact); err == nil && fi.IsDir() {
		return exact
	}
	entries, err := os.ReadDir(localAppData)
	if err != nil {
		return ""
	}
	var candidates []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "Claude") {
			continue
		}
		if strings.Contains(e.Name(), "-3p") == threep {
			candidates = append(candidates, filepath.Join(localAppData, e.Name()))
		}
	}
	sort.Strings(candidates)
	if len(candidates) == 0 {
		return ""
	}
	return candidates[0]
}

// desktopLiveFiles 返回全部 4 个路径（备份清单用）。
func (p desktopPaths) all() []string {
	return []string{p.normalConfig, p.threepConfig, p.profile, p.meta}
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

// writeClaudeDesktopLive 启用 Desktop 的 3P 网关模式（参照 cc-switch 的
// write_profile_at_paths，写 4 个文件）：
//  1. Claude/claude_desktop_config.json —— deploymentMode=3p，其余键
//     （MCP 等）原样保留，清理首次实现误写在顶层的网关 profile 遗留键；
//  2. Claude-3p/claude_desktop_config.json —— deploymentMode=3p；
//  3. configLibrary/<PROFILE_ID>.json —— 网关 profile 整文件（五键 +
//     coworkEgressAllowedHosts/disableDeploymentModeChooser）；
//  4. configLibrary/_meta.json —— entries 登记本 profile、appliedId 指向它
//     （patch 合并，其余键保留）。
//
// 文件不存在建新、存在但非法 JSON 直接报错且绝不改动原文件；0600 原子写回。
// 本方法不写任何日志（含令牌）。
func (s AccessConfigs) writeClaudeDesktopLive(plan desktopPlan, baseURL, token string) error {
	home := s.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return err
		}
	}
	p := desktopPathsFor(home)
	// 1+2: 两个配置文件设 deploymentMode（normal 顺带清理遗留 profile 键）。
	for _, cfg := range []struct {
		path     string
		cleanOld bool
	}{{p.normalConfig, true}, {p.threepConfig, false}} {
		if err := patchDesktopJSON(cfg.path, func(root map[string]any) {
			root["deploymentMode"] = "3p"
			if cfg.cleanOld {
				for _, k := range desktopManagedProfileKeys {
					delete(root, k)
				}
			}
		}); err != nil {
			return err
		}
	}
	// 3: profile 文件整体重建（本工具私有文件，等价 cc-switch clear+set）。
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
	profile := map[string]any{
		"coworkEgressAllowedHosts":     []any{"*"},
		"disableDeploymentModeChooser": true,
		"inferenceProvider":            "gateway",
		"inferenceGatewayBaseUrl":      baseURL,
		"inferenceGatewayApiKey":       token,
		"inferenceGatewayAuthScheme":   "bearer",
		"inferenceModels":              models,
	}
	if err := os.MkdirAll(filepath.Dir(p.profile), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return err
	}
	if err := atomicWriteFile0600(p.profile, b); err != nil {
		return err
	}
	// 4: _meta.json patch：entries 确保含本 profile、appliedId 指向它。
	return patchDesktopJSON(p.meta, func(root map[string]any) {
		entries, _ := root["entries"].([]any)
		found := false
		for _, e := range entries {
			if m, ok := e.(map[string]any); ok && m["id"] == desktopProfileID {
				m["name"] = desktopProfileName
				found = true
			}
		}
		if !found {
			root["entries"] = append(entries, map[string]any{
				"id": desktopProfileID, "name": desktopProfileName,
			})
		}
		root["appliedId"] = desktopProfileID
	})
}

// patchDesktopJSON 读-改-写单个 Desktop JSON 文件：不存在建空对象、存在但
// 非法 JSON 报错且不动原文件；mutate 后 0600 原子写回。
func patchDesktopJSON(path string, mutate func(root map[string]any)) error {
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
	mutate(root)
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile0600(path, out)
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
