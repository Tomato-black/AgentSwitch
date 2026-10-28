// channels_fetchmodels.go 是渠道「拉取模型」能力：按 baseURL/协议/Key 探测
// 上游模型列表端点（版本段与 anthropic 兼容后缀的候选拼接、分页、总数与
// 页数上限、OpenAI/智谱两种响应形态），供详情页与新建草稿使用。
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

	"AgentSwitch/internal/provider"
)

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
