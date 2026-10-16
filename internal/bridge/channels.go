package bridge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/storage"
)

// Channels 是渠道聚合根管理面（spec §7）：渠道字段 + 密钥子表 + 模型子表。
type Channels struct {
	A                 *app.App
	Creds             credential.Store
	KeychainAvailable func() bool
}

func (s Channels) creds() credential.Store {
	if s.Creds != nil {
		return s.Creds
	}
	return s.A.Creds()
}

func (s Channels) keychainAvailable() bool {
	if s.KeychainAvailable != nil {
		return s.KeychainAvailable()
	}
	return credential.OSKeychainAvailable()
}

type ChannelView struct {
	ID, Name, Type, Protocol, BaseURL  string
	Priority                           int
	Enabled, AllowPrivateUpstream      bool
	KeyCount, ModelCount, CoolingCount int
}

type KeyView struct {
	ID, ChannelID, Name, KeyTail, SecretBackend string
	Priority                                    int
	Enabled                                     bool
	Cooling                                     bool
	CoolingRemainSec                            int
	CoolingReason                               string
}

type ChannelModelRow struct {
	ID, ChannelID, PublicModel, UpstreamModel string
	Enabled                                   bool
}

type ChannelDetail struct {
	Channel ChannelView
	Keys    []KeyView
	Models  []ChannelModelRow
}

// coolingMap 取当前冷却快照（key id → 剩余秒）。
func (s Channels) coolingMap() map[string]int {
	now := time.Now()
	out := map[string]int{}
	for _, e := range s.A.Cooldowns().Snapshot(now) {
		out[e.KeyID] = int(e.Until.Sub(now).Seconds()) + 1 // 钳为正（Review Focus 5）
	}
	return out
}

func (s Channels) view(c storage.Channel) ChannelView {
	v := ChannelView{ID: c.ID, Name: c.Name, Type: c.Type, Protocol: c.Protocol,
		BaseURL: c.BaseURL, Priority: c.Priority, Enabled: c.Enabled,
		AllowPrivateUpstream: c.AllowPrivateUpstream}
	v.KeyCount, _ = s.A.Store().ChannelKeys().CountByChannel(c.ID)
	v.ModelCount, _ = s.A.Store().ChannelModels().CountByChannel(c.ID)
	cool := s.coolingMap()
	keys, _ := s.A.Store().ChannelKeys().ListByChannel(c.ID)
	for _, k := range keys {
		if _, ok := cool[k.ID]; ok {
			v.CoolingCount++
		}
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
	d := ChannelDetail{Channel: s.view(c)}
	cool := s.coolingMap()
	keys, _ := s.A.Store().ChannelKeys().ListByChannel(id)
	for _, k := range keys {
		kv := KeyView{ID: k.ID, ChannelID: k.ChannelID, Name: k.Name, KeyTail: k.KeyTail,
			SecretBackend: k.SecretBackend, Priority: k.Priority, Enabled: k.Enabled}
		if sec, ok := cool[k.ID]; ok {
			kv.Cooling, kv.CoolingRemainSec = true, sec
			for _, e := range s.A.Cooldowns().Snapshot(time.Now()) {
				if e.KeyID == k.ID {
					kv.CoolingReason = e.Reason
				}
			}
		}
		d.Keys = append(d.Keys, kv)
	}
	models, _ := s.A.Store().ChannelModels().ListByChannel(id)
	for _, m := range models {
		d.Models = append(d.Models, ChannelModelRow{ID: m.ID, ChannelID: m.ChannelID,
			PublicModel: m.PublicModel, UpstreamModel: m.UpstreamModel, Enabled: m.Enabled})
	}
	return d, nil
}

func (s Channels) Create(typeID, name, baseURL string, priority int, enabled, allowPrivate bool) (ChannelView, error) {
	preset, ok := provider.LookupPreset(typeID)
	if !ok {
		return ChannelView{}, fmt.Errorf("未知渠道类型: %s", typeID)
	}
	if baseURL == "" {
		baseURL = preset.DefaultBaseURL
	}
	if baseURL == "" {
		return ChannelView{}, fmt.Errorf("该类型需要填写 Base URL")
	}
	if err := provider.ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return ChannelView{}, err
	}
	c := storage.Channel{ID: storage.NewID(), Name: name, Type: preset.ID,
		Protocol: preset.Protocol, BaseURL: baseURL, Priority: priority,
		Enabled: enabled, AllowPrivateUpstream: allowPrivate}
	if err := s.A.Store().Channels().Save(c); err != nil {
		return ChannelView{}, err
	}
	s.A.Log().Info("channel created", "id", c.ID, "type", preset.ID, "actor", "ui", "result", "ok")
	return s.view(c), nil
}

func (s Channels) Update(id, name, baseURL string, priority int, enabled, allowPrivate bool) error {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return err
	}
	if err := provider.ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return err
	}
	c.Name, c.BaseURL, c.Priority, c.Enabled, c.AllowPrivateUpstream = name, baseURL, priority, enabled, allowPrivate
	if err := s.A.Store().Channels().Save(c); err != nil {
		return err
	}
	s.A.Log().Info("channel updated", "id", id, "actor", "ui", "result", "ok")
	return nil
}

// Remove 删渠道并级联清理密钥本体（channel_keys 行由 FK CASCADE）。
func (s Channels) Remove(id string) error {
	keys, _ := s.A.Store().ChannelKeys().ListByChannel(id)
	for _, k := range keys {
		s.creds().Delete(k.SecretKey)
	}
	if err := s.A.Store().Channels().Delete(id); err != nil {
		return err
	}
	s.A.Log().Info("channel removed", "id", id, "actor", "ui", "result", "ok")
	return nil
}

func (s Channels) SetEnabled(id string, on bool) error {
	err := s.A.Store().Channels().SetEnabled(id, on)
	if err == nil {
		s.A.Log().Info("channel enabled", "id", id, "on", on, "actor", "ui", "result", "ok")
	}
	return err
}

func (s Channels) Test(id string) provider.ProbeResult {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	apiKey := ""
	keys, _ := s.A.Store().ChannelKeys().ListByChannel(id)
	for _, k := range keys {
		if k.Enabled {
			if v, err := s.creds().Get(k.SecretKey); err == nil {
				apiKey = string(v)
			}
			break
		}
	}
	return provider.Probe(context15(), c.Protocol, c.BaseURL, apiKey, c.AllowPrivateUpstream)
}

// FetchModels 拉上游 GET /v1/models（两种协议同形，鉴权头按协议区分）。
func (s Channels) FetchModels(id string) ([]string, error) {
	c, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return nil, err
	}
	key := ""
	keys, _ := s.A.Store().ChannelKeys().ListByChannel(id)
	for _, k := range keys {
		if k.Enabled {
			if v, gerr := s.creds().Get(k.SecretKey); gerr == nil {
				key = string(v)
			}
			break
		}
	}
	if key == "" {
		return nil, fmt.Errorf("渠道无可用密钥，无法从上游获取模型")
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
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("上游请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("上游状态 %d", resp.StatusCode)
	}
	var out struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("响应解析失败: %w", err)
	}
	ids := make([]string, 0, len(out.Data))
	for _, d := range out.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

// AddKeys 批量导入：keysText 每行一枚（空行/首尾空白容忍）；多枚共享名称，
// 以尾4区分。逐枚落密钥存储+建行；中途失败返回已存数量与错误（部分成功明示）。
func (s Channels) AddKeys(channelID, name, keysText string, allowFallback bool) (int, error) {
	if _, err := s.A.Store().Channels().Get(channelID); err != nil {
		return 0, fmt.Errorf("渠道不存在: %w", err)
	}
	if name == "" {
		name = "key"
	}
	var lines []string
	for _, ln := range strings.Split(keysText, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			lines = append(lines, ln)
		}
	}
	if len(lines) == 0 {
		return 0, fmt.Errorf("至少需要一行密钥")
	}
	stored := 0
	for _, apiKey := range lines {
		backend, err := pickBackendFor(s, allowFallback)
		if err != nil {
			return stored, fmt.Errorf("第 %d 枚密钥: %w", stored+1, err)
		}
		id := storage.NewID()
		ref := credential.AccountKey(id)
		if err := s.creds().Set(ref, []byte(apiKey)); err != nil {
			return stored, fmt.Errorf("第 %d 枚密钥写入密钥存储失败: %w", stored+1, err)
		}
		row := storage.ChannelKey{ID: id, ChannelID: channelID, Name: name,
			SecretKey: ref, SecretBackend: backend, KeyTail: tail4(apiKey),
			Priority: 10, Enabled: true}
		if err := s.A.Store().ChannelKeys().Save(row); err != nil {
			s.creds().Delete(ref)
			return stored, fmt.Errorf("第 %d 枚密钥入库失败: %w", stored+1, err)
		}
		stored++
	}
	s.A.Log().Info("channel keys added", "channel", channelID, "count", stored, "actor", "ui", "result", "ok")
	return stored, nil
}

// pickBackendFor 复用 bridge.go 里 Accounts 的确认闸逻辑（该服务退役前两者并存）。
func pickBackendFor(s Channels, allowFallback bool) (string, error) {
	if s.keychainAvailable() {
		return "os-keychain", nil
	}
	if allowFallback {
		return "fallback-file", nil
	}
	return "", fmt.Errorf("系统密钥存储不可用；需显式允许文件降级（allowFallback）")
}

func (s Channels) SetKeyEnabled(id string, on bool) error {
	err := s.A.Store().ChannelKeys().SetEnabled(id, on)
	if err == nil {
		s.A.Log().Info("channel key enabled", "id", id, "on", on, "actor", "ui", "result", "ok")
	}
	return err
}

func (s Channels) RemoveKey(id string) error {
	k, err := s.A.Store().ChannelKeys().Get(id)
	if err != nil {
		return err
	}
	s.creds().Delete(k.SecretKey)
	if err := s.A.Store().ChannelKeys().Delete(id); err != nil {
		return err
	}
	s.A.Log().Info("channel key removed", "id", id, "actor", "ui", "result", "ok")
	return nil
}

func (s Channels) TestKey(id string) provider.ProbeResult {
	k, err := s.A.Store().ChannelKeys().Get(id)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	c, err := s.A.Store().Channels().Get(k.ChannelID)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	key, err := s.creds().Get(k.SecretKey)
	if err != nil {
		return provider.ProbeResult{Stage: "credential", Error: err.Error()}
	}
	return provider.Probe(context15(), c.Protocol, c.BaseURL, string(key), c.AllowPrivateUpstream)
}

func (s Channels) AddModel(channelID, public, upstream string) (ChannelModelRow, error) {
	if public == "" || upstream == "" {
		return ChannelModelRow{}, fmt.Errorf("公开模型名与上游模型名均不能为空")
	}
	if _, err := s.A.Store().Channels().Get(channelID); err != nil {
		return ChannelModelRow{}, fmt.Errorf("渠道不存在: %w", err)
	}
	m := storage.ChannelModel{ID: storage.NewID(), ChannelID: channelID,
		PublicModel: public, UpstreamModel: upstream, Enabled: true}
	if err := s.A.Store().ChannelModels().Save(m); err != nil {
		return ChannelModelRow{}, fmt.Errorf("该渠道已存在公开模型 %s", public)
	}
	s.A.Log().Info("channel model added", "channel", channelID, "public", public,
		"upstream", upstream, "actor", "ui", "result", "ok")
	return ChannelModelRow{ID: m.ID, ChannelID: channelID, PublicModel: public,
		UpstreamModel: upstream, Enabled: true}, nil
}

func (s Channels) UpdateModel(id, public, upstream string) error {
	if public == "" || upstream == "" {
		return fmt.Errorf("公开模型名与上游模型名均不能为空")
	}
	m, err := s.A.Store().ChannelModels().Get(id)
	if err != nil {
		return err
	}
	m.PublicModel, m.UpstreamModel = public, upstream
	if err := s.A.Store().ChannelModels().Save(m); err != nil {
		return fmt.Errorf("该渠道已存在公开模型 %s", public)
	}
	s.A.Log().Info("channel model updated", "id", id, "actor", "ui", "result", "ok")
	return nil
}

func (s Channels) SetModelEnabled(id string, on bool) error {
	err := s.A.Store().ChannelModels().SetEnabled(id, on)
	if err == nil {
		s.A.Log().Info("channel model enabled", "id", id, "on", on, "actor", "ui", "result", "ok")
	}
	return err
}

func (s Channels) RemoveModel(id string) error {
	err := s.A.Store().ChannelModels().Delete(id)
	if err == nil {
		s.A.Log().Info("channel model removed", "id", id, "actor", "ui", "result", "ok")
	}
	return err
}

// ---------- Models 聚合（模型页数据源，spec §7） ----------

type ModelAggregateView struct {
	ID, ChannelID, ChannelName, PublicModel, UpstreamModel string
	Enabled                                                bool
}

type ModelsAgg struct{ A *app.App }

func (s ModelsAgg) ListAll() []ModelAggregateView {
	ms, err := s.A.Store().ChannelModels().List()
	if err != nil {
		return nil
	}
	out := make([]ModelAggregateView, 0, len(ms))
	for _, m := range ms {
		name := m.ChannelID
		if c, err := s.A.Store().Channels().Get(m.ChannelID); err == nil {
			name = c.Name
		}
		out = append(out, ModelAggregateView{ID: m.ID, ChannelID: m.ChannelID,
			ChannelName: name, PublicModel: m.PublicModel,
			UpstreamModel: m.UpstreamModel, Enabled: m.Enabled})
	}
	return out
}

func (s ModelsAgg) SetEnabled(id string, on bool) error {
	return s.A.Store().ChannelModels().SetEnabled(id, on)
}

// ---------- Gateway.PoolStatus（概览池状态条，spec §8） ----------

type PoolEntry struct {
	ChannelID, ChannelName, KeyID, KeyName string
	CoolingUntilMs                         int64
	RemainingSec                           int
	Reason                                 string
}

// PoolStatus 返回当前冷却中的密钥（附渠道/密钥名）。正常密钥不出现。
// 简报实现说明的合并形态：一次 keys 遍历同时取全 ChannelID/ChannelName/KeyName，
// 免去 chanIDOf 的二次全表扫（行为与简报逐字版一致）。
func (g Gateway) PoolStatus() []PoolEntry {
	now := time.Now()
	sn := g.A.Cooldowns().Snapshot(now)
	if len(sn) == 0 {
		return nil
	}
	name := map[string][3]string{} // key id → [渠道ID, 渠道名, 密钥名]
	cs, _ := g.A.Store().Channels().List()
	for _, c := range cs {
		keys, _ := g.A.Store().ChannelKeys().ListByChannel(c.ID)
		for _, k := range keys {
			name[k.ID] = [3]string{c.ID, c.Name, k.Name}
		}
	}
	out := make([]PoolEntry, 0, len(sn))
	for _, e := range sn {
		p := PoolEntry{KeyID: e.KeyID, CoolingUntilMs: e.Until.UnixMilli(),
			RemainingSec: int(e.Until.Sub(now).Seconds()) + 1, Reason: e.Reason}
		if n, ok := name[e.KeyID]; ok {
			p.ChannelID, p.ChannelName, p.KeyName = n[0], n[1], n[2]
		}
		out = append(out, p)
	}
	return out
}
