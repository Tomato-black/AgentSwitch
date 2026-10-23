package bridge

import "AgentSwitch/internal/presets"

// Presets 是内置预设列表桥接（logical-models spec §8 / 预置配置 spec
// 2026-10-23）：桌面预设网格的只读数据源。mygo.Bind 注册由 desktop 侧完成。
type Presets struct{}

// PresetView 是预设卡片视图：双协议端点表（键 openai|anthropic），透传
// presets.ChannelPreset；警告透传见 Warning。
type PresetView struct {
	ID          string                              `json:"id"`
	DisplayName string                              `json:"displayName"`
	DocURL      string                              `json:"docUrl"`
	Protocols   map[string]presets.ProtocolEndpoint `json:"protocols"`
}

// List 返回全部内置预设（顺序即 UI 展示序）。
func (Presets) List() []PresetView {
	all := presets.All()
	out := make([]PresetView, 0, len(all))
	for _, p := range all {
		out = append(out, PresetView{
			ID: p.ID, DisplayName: p.DisplayName, DocURL: p.DocURL,
			Protocols: p.Protocols,
		})
	}
	return out
}

// Warning 透传最近一次预置加载的降级/跳过警告（空 = 无异常），供 UI 提示。
func (Presets) Warning() string { return presets.Warning() }
