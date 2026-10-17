package bridge

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/storage"
)

// Channels 是渠道聚合根管理面（logical-models spec §5）：每渠道单 Key
// （credential_ref → OS 密钥存储，写在 credential.ChannelKey(id)）+
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
			return ChannelView{}, fmt.Errorf("密钥写入密钥存储失败: %w", err)
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
func (s Channels) Update(id, name, baseURL, apiKey string, enabled, allowPrivate bool) error {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return err
	}
	if err := validateChannelURL(baseURL, allowPrivate); err != nil {
		return err
	}
	c.Name, c.BaseURL, c.Enabled, c.AllowPrivateUpstream = name, baseURL, enabled, allowPrivate
	keySet := false
	if apiKey != "" {
		ref := credential.ChannelKey(id)
		if err := s.creds().Set(ref, []byte(apiKey)); err != nil {
			return fmt.Errorf("密钥写入密钥存储失败: %w", err)
		}
		c.CredentialRef = ref
		keySet = true
	}
	if _, err := s.A.Store().Channels().Save(c); err != nil {
		return friendlySaveErr(err)
	}
	s.A.Log().Info("channel updated", "id", id, "actor", "ui", "result", "ok")
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
	if err := provider.ValidateBaseURL(c.BaseURL, c.AllowPrivateUpstream); err != nil {
		return nil, fmt.Errorf("Base URL 校验失败: %w", err)
	}
	key, err := s.secret(c)
	if err != nil {
		return nil, fmt.Errorf("渠道无可用密钥，无法从上游获取模型: %w", err)
	}
	req, err := http.NewRequestWithContext(context15(), http.MethodGet,
		strings.TrimRight(c.BaseURL, "/")+"/v1/models", nil)
	if err != nil {
		return nil, err
	}
	if c.Protocol == "openai" {
		req.Header.Set("Authorization", "Bearer "+key)
	} else {
		req.Header.Set("X-Api-Key", key)
		req.Header.Set("Anthropic-Version", "2023-06-01")
	}
	resp, err := fetchModelsClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("上游请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		return nil, fmt.Errorf("上游重定向被拒绝: %d", resp.StatusCode)
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("上游状态 %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}
	ids := make([]string, 0, len(out.Data))
	for _, d := range out.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
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
