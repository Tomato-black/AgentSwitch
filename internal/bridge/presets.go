package bridge

import "AgentSwitch/internal/presets"

// Presets 是内置预设列表桥接（logical-models spec §8）：桌面预设网格的
// 只读数据源。mygo.Bind 注册由 desktop 侧完成。
type Presets struct{}

// PresetView 是预设卡片视图，字段透传 presets.ChannelPreset。
type PresetView struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"displayName"`
	Protocol      string   `json:"protocol"`
	BaseURL       string   `json:"baseUrl"`
	DefaultModels []string `json:"defaultModels"`
	DocURL        string   `json:"docUrl"`
}

// List 返回全部内置预设（顺序即 UI 展示序）。
func (Presets) List() []PresetView {
	all := presets.All()
	out := make([]PresetView, 0, len(all))
	for _, p := range all {
		out = append(out, PresetView{
			ID: p.ID, DisplayName: p.DisplayName, Protocol: p.Protocol,
			BaseURL: p.BaseURL, DefaultModels: p.DefaultModels, DocURL: p.DocURL,
		})
	}
	return out
}
