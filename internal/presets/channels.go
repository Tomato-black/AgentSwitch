// Package presets 是内置渠道预设数据表（logical-models spec §7）：精选
// 15～25 家官方渠道的一键导入模板，Go 内嵌静态表，供 bridge 与桌面预设网格
// 消费。与 internal/provider.Preset（渠道类型/协议预设）不同：这里每条是
// 具体供应商的 base_url、协议与默认模型建议。
package presets

// ChannelPreset 是一条渠道预设（spec §7 字段：id、显示名、协议、base_url、
// 默认模型建议列表、官网/文档链接）。BaseURL 是渠道 base_url 语义——适配器
// 在其后拼 /v1/messages 或 /v1/chat/completions，故不含 /v1 后缀。
type ChannelPreset struct {
	ID            string   `json:"id"`
	DisplayName   string   `json:"displayName"`
	Protocol      string   `json:"protocol"`
	BaseURL       string   `json:"baseUrl"`
	DefaultModels []string `json:"defaultModels"`
	DocURL        string   `json:"docUrl"`
}

// All 返回全部预设，顺序即 UI 网格展示序（国内优先，再海外）。
// URL 以各官方控制台/文档为准，且必须能过 provider.ValidateBaseURL(_, false)。
func All() []ChannelPreset {
	return []ChannelPreset{
		{ID: "xiaomi", DisplayName: "小米 MiMo", Protocol: "openai",
			BaseURL:       "https://api.xiaomi.com",
			DefaultModels: []string{"mimo-7b-rl", "mimo-vl-7b"},
			DocURL:        "https://dev.mi.com/"},
		{ID: "qwen", DisplayName: "阿里千问", Protocol: "openai",
			BaseURL:       "https://dashscope.aliyuncs.com/compatible-mode",
			DefaultModels: []string{"qwen-max", "qwen-plus", "qwen-turbo"},
			DocURL:        "https://bailian.console.aliyun.com/"},
		{ID: "deepseek", DisplayName: "DeepSeek", Protocol: "openai",
			BaseURL:       "https://api.deepseek.com",
			DefaultModels: []string{"deepseek-chat", "deepseek-reasoner"},
			DocURL:        "https://platform.deepseek.com/"},
		{ID: "kimi", DisplayName: "Kimi 月之暗面", Protocol: "openai",
			BaseURL:       "https://api.moonshot.cn",
			DefaultModels: []string{"moonshot-v1-8k", "moonshot-v1-32k", "moonshot-v1-128k"},
			DocURL:        "https://platform.moonshot.cn/"},
		{ID: "zhipu", DisplayName: "智谱 GLM", Protocol: "openai",
			BaseURL:       "https://open.bigmodel.cn/api/paas/v4",
			DefaultModels: []string{"glm-4-plus", "glm-4-air", "glm-4-flash"},
			DocURL:        "https://open.bigmodel.cn/"},
		{ID: "siliconflow", DisplayName: "硅基流动", Protocol: "openai",
			BaseURL:       "https://api.siliconflow.cn",
			DefaultModels: []string{"deepseek-ai/DeepSeek-V3", "Qwen/Qwen2.5-72B-Instruct"},
			DocURL:        "https://cloud.siliconflow.cn/"},
		{ID: "stepfun", DisplayName: "阶跃星辰", Protocol: "openai",
			BaseURL:       "https://api.stepfun.com",
			DefaultModels: []string{"step-2-16k", "step-1-8k"},
			DocURL:        "https://platform.stepfun.com/"},
		{ID: "doubao", DisplayName: "豆包", Protocol: "openai",
			BaseURL:       "https://ark.cn-beijing.volces.com/api/v3",
			DefaultModels: []string{"doubao-seed-1-6", "doubao-1-5-pro-32k"},
			DocURL:        "https://console.volcengine.com/ark"},
		{ID: "anthropic", DisplayName: "Anthropic", Protocol: "anthropic",
			BaseURL:       "https://api.anthropic.com",
			DefaultModels: []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5"},
			DocURL:        "https://console.anthropic.com/"},
		{ID: "openai", DisplayName: "OpenAI", Protocol: "openai",
			BaseURL:       "https://api.openai.com",
			DefaultModels: []string{"gpt-4o", "gpt-4o-mini", "o3"},
			DocURL:        "https://platform.openai.com/"},
		{ID: "google", DisplayName: "Google AI Studio", Protocol: "openai",
			BaseURL:       "https://generativelanguage.googleapis.com/v1beta/openai",
			DefaultModels: []string{"gemini-2.5-pro", "gemini-2.5-flash"},
			DocURL:        "https://aistudio.google.com/"},
		{ID: "groq", DisplayName: "Groq", Protocol: "openai",
			BaseURL:       "https://api.groq.com/openai",
			DefaultModels: []string{"llama-3.3-70b-versatile", "deepseek-r1-distill-llama-70b"},
			DocURL:        "https://console.groq.com/"},
		{ID: "together", DisplayName: "Together AI", Protocol: "openai",
			BaseURL:       "https://api.together.xyz",
			DefaultModels: []string{"meta-llama/Llama-3.3-70B-Instruct-Turbo", "deepseek-ai/DeepSeek-V3"},
			DocURL:        "https://api.together.ai/"},
		{ID: "openrouter", DisplayName: "OpenRouter", Protocol: "openai",
			BaseURL:       "https://openrouter.ai/api",
			DefaultModels: []string{"openai/gpt-4o", "anthropic/claude-sonnet-4.5", "meta-llama/llama-3.3-70b-instruct"},
			DocURL:        "https://openrouter.ai/docs"},
	}
}

// ById 按 id 查预设。
func ById(id string) (ChannelPreset, bool) {
	for _, p := range All() {
		if p.ID == id {
			return p, true
		}
	}
	return ChannelPreset{}, false
}
