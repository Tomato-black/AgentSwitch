package bridge

import (
	"context"
	"fmt"
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

// BalanceResult 是渠道上游余额查询结果（provider 类型别名，桌面层无需 import provider）。
type BalanceResult = provider.BalanceResult

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
		return ChannelView{}, uniqueErr(err, "渠道名已存在")
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
		return uniqueErr(err, "渠道名已存在")
	}
	s.A.Log().Info("channel updated", "id", id, "protocol", protocol, "actor", "ui", "result", "ok")
	if keySet {
		s.A.Log().Info("channel key set", "id", id, "actor", "ui", "result", "ok")
	}
	return nil
}

// Copy 复制渠道的连接配置与建议模型；copyKey 为 false 时不复制 Key（默认
// 安全），新渠道需由用户重新填写 Key。名称冲突自动追加「2/3…」。
func (s Channels) Copy(id string, copyKey bool) (ChannelView, error) {
	src, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return ChannelView{}, fmt.Errorf("渠道不存在")
	}
	key := ""
	if copyKey {
		if k, err := s.secret(src); err == nil {
			key = k
		}
	}
	names := map[string]bool{}
	if chs, err := s.A.Store().Channels().List(); err == nil {
		for _, c := range chs {
			names[c.Name] = true
		}
	}
	base := src.Name + " 副本"
	name := base
	for i := 2; names[name]; i++ {
		name = fmt.Sprintf("%s %d", base, i)
	}
	v, err := s.Create(src.PresetID, name, src.BaseURL, src.Protocol, key, src.Enabled, src.AllowPrivateUpstream)
	if err != nil {
		return ChannelView{}, err
	}
	if sugg, err := s.A.Store().ChannelModels().Suggestions(src.ID); err == nil && len(sugg) > 0 {
		if err := s.SetSuggestions(v.ID, sugg); err != nil {
			return v, err
		}
	}
	s.A.Log().Info("channel copied", "from", id, "to", v.ID, "with_key", copyKey, "actor", "ui", "result", "ok")
	return v, nil
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
	return s.TestContext(context15(), id)
}

// TestContext 是 Test 的可取消版本（UI 异步任务用）：ctx 取消后立刻返回
// 取消错误，不再等待上游。
func (s Channels) TestContext(ctx context.Context, id string) provider.ProbeResult {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	key, err := s.secret(c)
	if err != nil {
		return provider.ProbeResult{Stage: "credential", Error: err.Error()}
	}
	res := provider.Probe(ctx, c.Protocol, c.BaseURL, key, c.AllowPrivateUpstream)
	if ctx.Err() != nil {
		return provider.ProbeResult{Stage: "canceled", Error: ctx.Err().Error()}
	}
	return res
}

// Balance 查询渠道上游余额（可取消版本见 BalanceContext）。
func (s Channels) Balance(id string) provider.BalanceResult {
	return s.BalanceContext(context15(), id)
}

// BalanceContext 查询渠道上游余额；只支持有公开余额 API 的供应商。
func (s Channels) BalanceContext(ctx context.Context, id string) provider.BalanceResult {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return provider.BalanceResult{Message: "渠道不存在"}
	}
	key, err := s.secret(c)
	if err != nil {
		return provider.BalanceResult{Message: err.Error()}
	}
	res := provider.ProbeBalance(ctx, c.PresetID, c.BaseURL, key, c.AllowPrivateUpstream)
	if ctx.Err() != nil {
		res.Message = "已取消"
	}
	return res
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
