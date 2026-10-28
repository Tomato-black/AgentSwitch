package bridge

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/storage"
)

// Channels 是渠道聚合根管理面（logical-models spec §5）：每渠道单 Key
// （credential_ref → 库内 AES-256-GCM 密文，键为 credential.ChannelKey(id)）+
// 模型建议列表（SetSuggestions 全量替换，目标编辑下拉数据源）。
type Channels struct {
	A     *app.App
	Creds credential.Store
}

func (s Channels) creds() credential.Store {
	if s.Creds != nil {
		return s.Creds
	}
	return s.A.Creds()
}

// ChannelView 是渠道卡片视图；HasKey 即 credential_ref 非空（有可用凭据）。
type ChannelView struct {
	ID, Name, PresetID, Protocol, BaseURL string
	Enabled, AllowPrivate                 bool
	HasKey                                bool
	SuggestionCount                       int
}

// ChannelDetail 是渠道详情：视图 + 模型建议列表。
type ChannelDetail struct {
	ChannelView
	Suggestions []string
}

func (s Channels) view(c storage.Channel) ChannelView {
	v := ChannelView{ID: c.ID, Name: c.Name, PresetID: c.PresetID, Protocol: c.Protocol,
		BaseURL: c.BaseURL, Enabled: c.Enabled, AllowPrivate: c.AllowPrivateUpstream,
		HasKey: c.CredentialRef != ""}
	if names, err := s.A.Store().ChannelModels().Suggestions(c.ID); err == nil {
		v.SuggestionCount = len(names)
	}
	return v
}

func (s Channels) List() []ChannelView {
	cs, err := s.A.Store().Channels().List()
	if err != nil {
		return nil
	}
	out := make([]ChannelView, 0, len(cs))
	for _, c := range cs {
		out = append(out, s.view(c))
	}
	return out
}

func (s Channels) Get(id string) (ChannelDetail, error) {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return ChannelDetail{}, err
	}
	d := ChannelDetail{ChannelView: s.view(c)}
	d.Suggestions, _ = s.A.Store().ChannelModels().Suggestions(id)
	return d, nil
}

// Create 建渠道，apiKey 非空则同时写入单 Key。presetID 仅作预设回显（可空），
// protocol 与 baseURL 由调用方（预设表单）显式给出。
func (s Channels) Create(presetID, name, baseURL, protocol, apiKey string, enabled, allowPrivate bool) (ChannelView, error) {
	if err := validateChannelURL(baseURL, allowPrivate); err != nil {
		return ChannelView{}, err
	}
	if protocol != "anthropic" && protocol != "openai" {
		return ChannelView{}, fmt.Errorf("协议必须是 anthropic 或 openai")
	}
	id := storage.NewID()
	c := storage.Channel{ID: id, Name: name, Protocol: protocol, BaseURL: baseURL,
		PresetID: presetID, Enabled: enabled, AllowPrivateUpstream: allowPrivate}
	if apiKey != "" {
		ref := credential.ChannelKey(id)
		if err := s.creds().Set(ref, []byte(apiKey)); err != nil {
			return ChannelView{}, fmt.Errorf("密钥写入加密存储失败: %w", err)
		}
		c.CredentialRef = ref
	}
	saved, err := s.A.Store().Channels().Save(c)
	if err != nil {
		if c.CredentialRef != "" {
			s.creds().Delete(c.CredentialRef)
		}
		return ChannelView{}, friendlySaveErr(err)
	}
	s.A.Log().Info("channel created", "id", id, "preset", presetID, "protocol", protocol, "actor", "ui", "result", "ok")
	if c.CredentialRef != "" {
		s.A.Log().Info("channel key set", "id", id, "actor", "ui", "result", "ok")
	}
	return s.view(saved), nil
}

// Update 改渠道字段；apiKey 非空=覆盖单 Key，空=保持原 Key 不变。
// protocol 可编辑（2026-10-28 修订，偏离 2026-10-23 spec §5「创建后不可变」），
// 校验口径与 Create 一致。
func (s Channels) Update(id, name, baseURL, protocol, apiKey string, enabled, allowPrivate bool) error {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return err
	}
	if err := validateChannelURL(baseURL, allowPrivate); err != nil {
		return err
	}
	if protocol != "anthropic" && protocol != "openai" {
		return fmt.Errorf("协议必须是 anthropic 或 openai")
	}
	c.Name, c.BaseURL, c.Protocol, c.Enabled, c.AllowPrivateUpstream = name, baseURL, protocol, enabled, allowPrivate
	keySet := false
	if apiKey != "" {
		ref := credential.ChannelKey(id)
		if err := s.creds().Set(ref, []byte(apiKey)); err != nil {
			return fmt.Errorf("密钥写入加密存储失败: %w", err)
		}
		c.CredentialRef = ref
		keySet = true
	}
	if _, err := s.A.Store().Channels().Save(c); err != nil {
		return friendlySaveErr(err)
	}
	s.A.Log().Info("channel updated", "id", id, "protocol", protocol, "actor", "ui", "result", "ok")
	if keySet {
		s.A.Log().Info("channel key set", "id", id, "actor", "ui", "result", "ok")
	}
	return nil
}

// Remove 删渠道：先清密钥本体，再删行（channel_models 由 FK CASCADE）。
func (s Channels) Remove(id string) {
	if c, err := s.A.Store().Channels().Get(id); err == nil && c.CredentialRef != "" {
		s.creds().Delete(c.CredentialRef)
	}
	if err := s.A.Store().Channels().Delete(id); err != nil {
		s.A.Log().Error("channel remove failed", "id", id, "err", err)
		return
	}
	s.A.Log().Info("channel removed", "id", id, "actor", "ui", "result", "ok")
}

func (s Channels) SetEnabled(id string, on bool) {
	if err := s.A.Store().Channels().SetEnabled(id, on); err != nil {
		s.A.Log().Error("channel enable failed", "id", id, "on", on, "err", err)
		return
	}
	s.A.Log().Info("channel enabled", "id", id, "on", on, "actor", "ui", "result", "ok")
}

// secret 取渠道单 Key 本体；未配置或取不到返回中文错误。
func (s Channels) secret(c storage.Channel) (string, error) {
	if c.CredentialRef == "" {
		return "", fmt.Errorf("渠道未配置密钥")
	}
	v, err := s.creds().Get(c.CredentialRef)
	if err != nil {
		return "", fmt.Errorf("读取密钥失败: %w", err)
	}
	return string(v), nil
}

func (s Channels) Test(id string) provider.ProbeResult {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	key, err := s.secret(c)
	if err != nil {
		return provider.ProbeResult{Stage: "credential", Error: err.Error()}
	}
	return provider.Probe(context15(), c.Protocol, c.BaseURL, key, c.AllowPrivateUpstream)
}

// fetchModelsClient 是 FetchModels 专用的包级私有 client：不跟随重定向
// （3xx 原样返回，交由状态检查报错），与数据面 adapter 的出站姿态一致。
var fetchModelsClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// FetchModels 拉上游 GET /v1/models（两种协议同形，鉴权头按协议区分）。
func (s Channels) FetchModels(id string) ([]string, error) {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return nil, err
	}
	key, err := s.secret(c)
	if err != nil {
		return nil, fmt.Errorf("渠道无可用密钥，无法从上游获取模型: %w", err)
	}
	return fetchModelsHTTP(c.BaseURL, c.Protocol, key, c.AllowPrivateUpstream)
}

// FetchModelsFromDraft 用未保存渠道的表单值直接拉模型（新建表单「拉取模型」
// 用；渠道尚未入库，无 Key 可读）。协议与 Key 在此校验——Create/Update 侧
// 对 Key 只在 Create 时必填，草稿拉取则必须显式提供。
func (s Channels) FetchModelsFromDraft(baseURL, protocol, apiKey string, allowPrivate bool) ([]string, error) {
	if protocol != "anthropic" && protocol != "openai" {
		return nil, fmt.Errorf("协议必须是 anthropic 或 openai")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, fmt.Errorf("需要填写 API Key 后才能拉取模型")
	}
	return fetchModelsHTTP(baseURL, protocol, apiKey, allowPrivate)
}

// 分页上限：页数与总条数都封顶，防止上游无限 has_more 把 UI 卡死
// （fetchModelsHTTP 单次调用阻塞 UI 线程，与「测试连通」同一姿态）。
const (
	fetchModelsMaxPages = 10
	fetchModelsMaxTotal = 500
)

// modelsURLCandidates 构造「模型列表端点」候选 URL，对齐 cc-switch 的
// services/model_fetch.rs::build_models_url_candidates。之所以不能无脑拼
// /v1/models：base 已以版本段 /v{N} 结尾时（预设里 20 个 openai 端点有 13 个
// 以 /v1 结尾），版本号已在路径中，再补 /v1 得到 .../v1/v1/models → 404——
// 数据面 JoinAPIPath/anthropicEndpoint 早已处理同一坑（2026-10-22 小米
// /anthropic/messages 404 修复），拉取模型此前没跟上。
//
// 候选顺序：
//  1. base 以版本段 /v{N} 结尾 → {base}/models；版本段非 /v1（智谱
//     /api/coding/paas/v4 等）时再补 {base}/v1/models 兜底
//  2. 否则 → {base}/v1/models
//  3. base 命中已知 Anthropic 兼容子路径 → 再补剥离后的 {root}/v1/models
//     与 {root}/models（部分聚合站把两种协议挂在不同子路径下）
//
// 结果去重且保持首次出现顺序；空 base 返回空。
func modelsURLCandidates(baseURL string) []string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return nil
	}
	var out []string
	if hasVersionSegment(trimmed) {
		out = append(out, trimmed+"/models")
		if !strings.HasSuffix(trimmed, "/v1") {
			out = append(out, trimmed+"/v1/models")
		}
	} else {
		out = append(out, trimmed+"/v1/models")
	}
	if root := stripAnthropicCompatSuffix(trimmed); root != "" {
		out = append(out, root+"/v1/models", root+"/models")
	}
	unique := make([]string, 0, len(out))
	for _, u := range out {
		if !slices.Contains(unique, u) {
			unique = append(unique, u)
		}
	}
	return unique
}

// hasVersionSegment 报告 URL 路径是否以版本段 /v{N} 结尾（/v1、/v4 等）。
func hasVersionSegment(u string) bool {
	last := u
	if i := strings.LastIndex(u, "/"); i >= 0 {
		last = u[i+1:]
	}
	if len(last) < 2 || last[0] != 'v' {
		return false
	}
	for _, b := range []byte(last[1:]) {
		if b < '0' || b > '9' {
			return false
		}
	}
	return true
}

// anthropicCompatSuffixes 是已知的「Anthropic 协议兼容子路径」后缀，按最长
// 前缀优先排列（/api/anthropic 必须先于 /anthropic，否则剥离子路径会错位）。
var anthropicCompatSuffixes = []string{
	"/api/claudecode",
	"/api/anthropic",
	"/apps/anthropic",
	"/api/coding",
	"/claudecode",
	"/anthropic",
	"/step_plan",
	"/coding",
	"/claude",
}

// stripAnthropicCompatSuffix 若 base 以任一已知兼容子路径结尾，返回剥离后的
// 根（如 https://x/apps/anthropic → https://x）；否则返回空串。
func stripAnthropicCompatSuffix(baseURL string) string {
	for _, suffix := range anthropicCompatSuffixes {
		if strings.HasSuffix(baseURL, suffix) {
			root := strings.TrimRight(baseURL[:len(baseURL)-len(suffix)], "/")
			if root == "" || !strings.Contains(root, "://") {
				return ""
			}
			return root
		}
	}
	return ""
}

// errModelsEndpointMissing 标记「该候选端点 404/405」，供 fetchModelsHTTP
// 决定是否切换下一个候选 URL。
var errModelsEndpointMissing = errors.New("模型列表端点不存在")

// fetchModelsHTTP 按候选端点列表逐个拉取：任一候选成功即返回，全部候选
// 404/405 才报「全部候选端点均不可用」。非 404/405 的错误（网络、鉴权、
// 解析等）立刻返回——换 URL 对它们没有意义。
//
// 每个候选内部沿用分页语义（响应形如 {"data":[{"id":...}],
// "has_more"?:bool, "next_cursor"?:string}）：
//   - has_more=true 且有 next_cursor → 下一页带 cursor=<next_cursor>；
//   - has_more=true 且无 next_cursor → 下一页带 after_id=<最后一条 id>
//     （Anthropic 官方 /v1/models 的续页参数）；
//   - 任一页非 200 即中止，返回已收集的部分 + 错误。
//
// 响应同时兼容 OpenAI 形态 data[].id 与智谱目录形态 models[].slug|id。
// 跨页按 id 去重保序；单页超过 fetchModelsMaxTotal 条或页数达到
// fetchModelsMaxPages 即停。不跟随 3xx。
func fetchModelsHTTP(baseURL, protocol, apiKey string, allowPrivate bool) ([]string, error) {
	if err := provider.ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return nil, fmt.Errorf("Base URL 校验失败: %w", err)
	}
	key := strings.TrimSpace(apiKey)
	candidates := modelsURLCandidates(baseURL)
	if len(candidates) == 0 {
		return nil, fmt.Errorf("Base URL 无法构造模型列表端点")
	}
	var lastErr error
	for _, endpoint := range candidates {
		ids, err := fetchModelsFromEndpoint(endpoint, protocol, key)
		if err == nil {
			return ids, nil
		}
		lastErr = err
		if !errors.Is(err, errModelsEndpointMissing) {
			return ids, err
		}
	}
	return nil, fmt.Errorf("该地址下所有候选模型端点均返回 404（最后错误：%v）", lastErr)
}

// fetchModelsFromEndpoint 对单个端点做分页拉取；404/405 包成
// errModelsEndpointMissing 交给上层切换候选，其余错误原样上抛。
func fetchModelsFromEndpoint(endpoint, protocol, key string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	cursor := ""
	for page := 0; page < fetchModelsMaxPages; page++ {
		req, err := http.NewRequestWithContext(context15(), http.MethodGet,
			endpoint+cursor, nil)
		if err != nil {
			return out, err
		}
		if protocol == "openai" {
			req.Header.Set("Authorization", "Bearer "+key)
		} else {
			req.Header.Set("X-Api-Key", key)
			req.Header.Set("Anthropic-Version", "2023-06-01")
		}
		resp, err := fetchModelsClient.Do(req)
		if err != nil {
			return out, fmt.Errorf("上游请求失败: %w", err)
		}
		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			resp.Body.Close()
			return out, fmt.Errorf("上游重定向被拒绝: %d", resp.StatusCode)
		}
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			resp.Body.Close()
			return out, fmt.Errorf("%w: 上游状态 %d", errModelsEndpointMissing, resp.StatusCode)
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return out, fmt.Errorf("上游状态 %d", resp.StatusCode)
		}
		body, err := decodeModelsPage(resp)
		resp.Body.Close()
		if err != nil {
			return out, err
		}
		for _, id := range body.ids {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
		if !body.hasMore || len(body.ids) == 0 || len(out) >= fetchModelsMaxTotal {
			break
		}
		// 无 cursor 时用最后一条 id；空 id 说明上游无法续页，停。
		next := body.nextCursor
		if next == "" {
			next = body.ids[len(body.ids)-1]
			if next == "" {
				break
			}
			cursor = "?after_id=" + url.QueryEscape(next)
		} else {
			cursor = "?cursor=" + url.QueryEscape(next)
		}
	}
	return out, nil
}

// modelsPage 是一页模型列表响应的归一化结果。
type modelsPage struct {
	ids        []string
	hasMore    bool
	nextCursor string
}

// decodeModelsPage 解析一页响应。`models` 字段用 RawMessage 拿下再宽松解析：
// 部分供应商会在 data 旁附带任意形状的 models，强类型反序列化一旦对不上
// 就会连带 data 一起解析失败（cc-switch #7593 同款问题）。
func decodeModelsPage(resp *http.Response) (modelsPage, error) {
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return modelsPage{}, fmt.Errorf("读取响应失败: %w", err)
	}
	var head struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Models     json.RawMessage `json:"models"`
		HasMore    bool            `json:"has_more"`
		NextCursor string          `json:"next_cursor"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return modelsPage{}, fmt.Errorf("响应解析失败: %w", err)
	}
	page := modelsPage{hasMore: head.HasMore, nextCursor: head.NextCursor}
	for _, d := range head.Data {
		page.ids = append(page.ids, d.ID)
	}
	page.ids = append(page.ids, catalogModelIDs(head.Models)...)
	return page, nil
}

// catalogModelIDs 解析智谱目录形态 models[].slug|id；非数组/非对象/空串一律
// 跳过，绝不报错。
func catalogModelIDs(raw json.RawMessage) []string {
	var entries []map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &entries) != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		for _, k := range []string{"slug", "id"} {
			if s, _ := e[k].(string); strings.TrimSpace(s) != "" {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

// SetSuggestions 全量替换渠道的模型建议列表（空白/空串/重复项剔除）。
func (s Channels) SetSuggestions(channelID string, models []string) error {
	if _, err := s.A.Store().Channels().Get(channelID); err != nil {
		return fmt.Errorf("渠道不存在: %w", err)
	}
	if err := s.A.Store().ChannelModels().DeleteByChannel(channelID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		if _, err := s.A.Store().ChannelModels().Save(storage.ChannelModel{
			ChannelID: channelID, ModelName: m}); err != nil {
			return err
		}
	}
	s.A.Log().Info("channel suggestions set", "channel", channelID, "count", len(seen), "actor", "ui", "result", "ok")
	return nil
}

// Suggestions 返回渠道的模型建议名（按名排序）。
func (s Channels) Suggestions(channelID string) []string {
	out, err := s.A.Store().ChannelModels().Suggestions(channelID)
	if err != nil {
		return nil
	}
	return out
}

func validateChannelURL(baseURL string, allowPrivate bool) error {
	if baseURL == "" {
		return fmt.Errorf("需要填写 Base URL")
	}
	return provider.ValidateBaseURL(baseURL, allowPrivate)
}

// friendlySaveErr 把 name UNIQUE 冲突译成中文友好错误（repo 约定由 bridge 友好化）。
func friendlySaveErr(err error) error {
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return fmt.Errorf("渠道名已存在")
	}
	return err
}
