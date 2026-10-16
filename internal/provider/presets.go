package provider

// Preset 是渠道类型预设（spec §4）：提供 protocol 推导与默认 BaseURL，
// BaseURL 可被显式覆盖（ValidateBaseURL 校验不变）。
type Preset struct {
	ID             string `json:"id"`
	Label          string `json:"label"`
	Protocol       string `json:"protocol"`
	DefaultBaseURL string `json:"defaultBaseUrl"`
	Description    string `json:"description"`
}

// Presets 渠道类型目录（顺序即 UI 展示序）。
func Presets() []Preset {
	return []Preset{
		{ID: "anthropic-official", Label: "Anthropic 官方", Protocol: "anthropic",
			DefaultBaseURL: "https://api.anthropic.com", Description: "Anthropic 官方 API"},
		{ID: "anthropic-compatible", Label: "Anthropic 兼容中转", Protocol: "anthropic",
			DefaultBaseURL: "", Description: "第三方 Anthropic 协议代理/中转站，Base URL 必填"},
		{ID: "openai-official", Label: "OpenAI 官方", Protocol: "openai",
			DefaultBaseURL: "https://api.openai.com", Description: "OpenAI 官方 API"},
		{ID: "openai-compatible", Label: "OpenAI 兼容", Protocol: "openai",
			DefaultBaseURL: "", Description: "OpenRouter/聚合站/自建网关等 OpenAI 协议端点，Base URL 必填"},
	}
}

// LookupPreset 按 id 查预设。
func LookupPreset(id string) (Preset, bool) {
	for _, p := range Presets() {
		if p.ID == id {
			return p, true
		}
	}
	return Preset{}, false
}
