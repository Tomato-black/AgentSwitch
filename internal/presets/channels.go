// Package presets 是渠道预设的加载与合并（spec 2026-10-23 §3/§4）：预置不再
// 硬编码于 Go 源码，而是 JSON 数据（embed 默认表 + ~/.agentswitch/presets.json
// 用户覆盖/追加），供 bridge 与桌面预设网格消费。每条预设按协议组织——
// openai/anthropic 各自独立的 base_url 与默认模型建议。
package presets

import (
	_ "embed"
	"os"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"AgentSwitch/internal/provider"
)

// defaultJSON 是内嵌默认预置表（spec 2026-10-23 §6 查证数据），开箱即用；
// 解析失败属构建期缺陷（TestDefaultJSONTwentyEntries 断言）。
//
//go:embed default.json
var defaultJSON []byte

// DefaultJSON 返回内嵌默认表字节（应用启动传给 Load；测试注入用）。
func DefaultJSON() []byte { return defaultJSON }

// ProtocolEndpoint 是某协议下的端点预设：base_url 与默认模型建议列表。
type ProtocolEndpoint struct {
	BaseURL       string   `json:"baseUrl"`
	DefaultModels []string `json:"defaultModels"`
}

// ChannelPreset 是一条渠道预设：BaseURL 语义按协议区分（openai 经
// provider.JoinAPIPath 拼后缀；anthropic 按 Anthropic SDK 约定拼 /v1/messages，
// 见 provider.anthropicEndpoint）。
type ChannelPreset struct {
	ID          string                      `json:"id"`
	DisplayName string                      `json:"displayName"`
	DocURL      string                      `json:"docUrl"`
	Protocols   map[string]ProtocolEndpoint `json:"protocols"` // 键: "openai"|"anthropic"
}

// 包内只读状态：应用启动时 Load 一次，此后 All/ById/Warning 只读（spec §4，
// 不做热重载、不加锁）。
var (
	table   []ChannelPreset
	warning string
)

// knownProtocols 是合法协议键（与渠道协议枚举一致）。
var knownProtocols = map[string]bool{"openai": true, "anthropic": true}

// Load 解析内嵌默认字节与用户文件（userFile 为空或不存在时只用内嵌），
// 校验后更新包内表与警告。内嵌条目 baseUrl 必须过 provider.ValidateBaseURL
// (_, false)；用户文件条目放宽（http/私网/localhost 放行，仅要求可解析——
// spec §4 用户决策 2026-10-23）。合并按 id：同 id 整体覆盖原位、新 id 追加
// 末尾。坏条目跳过并计入 Warning，用户文件整体解析失败降级用内嵌表——预置
// 是辅助功能，绝不阻塞启动（spec §4）。
func Load(embedded []byte, userFile string) {
	table, warning = nil, ""
	table = parse(embedded, true)
	if userFile == "" {
		return
	}
	data, err := os.ReadFile(userFile)
	if err != nil {
		if !os.IsNotExist(err) {
			warning += fmt.Sprintf("用户预置文件读取失败已忽略：%v；", err)
		}
		return
	}
	user := parse(data, false)
	merge(user)
}

// merge 把用户表并入 table：同 id 整体覆盖（原位），新 id 追加末尾。
func merge(user []ChannelPreset) {
	index := make(map[string]int, len(table))
	for i, p := range table {
		index[p.ID] = i
	}
	for _, p := range user {
		if i, ok := index[p.ID]; ok {
			table[i] = p
		} else {
			index[p.ID] = len(table)
			table = append(table, p)
		}
	}
}

// parse 解析一段 JSON 到预设表：单条校验失败跳过并拼进警告；strict=true 时
// baseUrl 跑 ValidateBaseURL（内嵌默认），false 时仅要求可解析（用户文件）。
func parse(data []byte, strict bool) []ChannelPreset {
	var raw []ChannelPreset
	if err := json.Unmarshal(data, &raw); err != nil {
		warning += fmt.Sprintf("预置数据解析失败已整体忽略：%v；", err)
		return nil
	}
	out := make([]ChannelPreset, 0, len(raw))
	for _, p := range raw {
		if msg := validateEntry(p, strict); msg != "" {
			warning += fmt.Sprintf("预置 %q 已跳过：%s；", p.ID, msg)
			continue
		}
		out = append(out, p)
	}
	return out
}

// validateEntry 返回空串表示合法；strict 语义见 parse。
func validateEntry(p ChannelPreset, strict bool) string {
	if strings.TrimSpace(p.ID) == "" {
		return "id 为空"
	}
	if len(p.Protocols) == 0 {
		return "protocols 为空"
	}
	for proto, ep := range p.Protocols {
		if !knownProtocols[proto] {
			return "未知协议键 " + proto
		}
		if strict {
			if err := provider.ValidateBaseURL(ep.BaseURL, false); err != nil {
				return fmt.Sprintf("baseUrl %q 未过安全校验: %v", ep.BaseURL, err)
			}
		} else if err := parseableURL(ep.BaseURL); err != nil {
			return fmt.Sprintf("baseUrl %q 不可解析: %v", ep.BaseURL, err)
		}
	}
	return ""
}

// parseableURL 是用户文件条目的放宽校验（spec §4 用户决策 2026-10-23）：
// 仅要求可解析且带 scheme/host，http/私网/localhost 放行。
func parseableURL(raw string) error {
	if raw == "" {
		return fmt.Errorf("空 URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("缺少 scheme/host")
	}
	return nil
}

// All 返回合并后的全部预设，顺序即 UI 网格展示序；未 Load 时为空表。
func All() []ChannelPreset { return table }

// ById 按 id 查预设（重复 id 时首个命中）。
func ById(id string) (ChannelPreset, bool) {
	for _, p := range table {
		if p.ID == id {
			return p, true
		}
	}
	return ChannelPreset{}, false
}

// Warning 返回最近一次 Load 的降级/跳过警告（空 = 无异常），供 UI 提示。
func Warning() string { return warning }

// protocolOrder 是「第一可用协议」的固定序（spec §5：openai 优先）。
var protocolOrder = []string{"openai", "anthropic"}

// FirstProtocol 返回预置的第一个可用协议与端点；无协议时 ok=false。
func FirstProtocol(p ChannelPreset) (string, ProtocolEndpoint, bool) {
	for _, proto := range protocolOrder {
		if ep, ok := p.Protocols[proto]; ok {
			return proto, ep, true
		}
	}
	return "", ProtocolEndpoint{}, false
}
