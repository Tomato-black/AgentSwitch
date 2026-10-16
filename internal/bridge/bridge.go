// Package bridge 是管理面桥接层：把 app.App 的能力整理为经 mygo.Bind
// 暴露给 desktop 的四个服务——Gateway（网关控制）、Providers（供应商）、
// Accounts（账号与密钥）、Models（模型映射）。
package bridge

import (
	"context"
	"fmt"
	"time"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/config"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/storage"
	"AgentSwitch/internal/telemetry"
)

// context15 返回 15 秒超时上下文（StopGateway 与 Probe 共用）。
// goroutine 等到 ctx 结束后再调 cancel（简报约定的 cancel 回收方式），
// 定时器与 goroutine 最多存活 15 秒，vet lostcancel 亦不报。
func context15() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	go func() {
		<-ctx.Done()
		cancel()
	}()
	return ctx
}

type Gateway struct{ A *app.App }

type Status struct {
	State          string `json:"state"`
	Addr           string `json:"addr"`
	DegradedReason string `json:"degradedReason,omitempty"`
	Since          int64  `json:"sinceMs"`
	Token          string `json:"token"`
	Version        string `json:"version"`
	// Port 与 TimeoutSeconds 是当前生效配置（config.Load 填充），供 UI 设置
	// 面板回显与概览配置片段拼实际端口（Task 13 实现时修正条款 1）。
	Port           int                       `json:"port"`
	TimeoutSeconds int                       `json:"timeoutSeconds"`
	Recent         []telemetry.RequestRecord `json:"recent"`
}

func (g Gateway) Status() Status {
	si := g.A.State()
	s, _ := config.Load(g.A.Store().Settings().Get)
	return Status{
		State: string(si.State), Addr: si.Addr, DegradedReason: si.DegradedReason,
		Since: si.Since.UnixMilli(), Token: s.GatewayToken, Version: si.Version,
		Port: s.Port, TimeoutSeconds: s.RequestTimeoutSeconds,
		Recent: g.A.Recent(),
	}
}

func (g Gateway) Start() error { return g.A.StartGateway() }
func (g Gateway) Stop() error  { return g.A.StopGateway(context15()) }
func (g Gateway) RegenerateToken() (string, error) {
	tok, err := g.A.RegenerateToken()
	if err != nil {
		return "", err
	}
	g.A.Log().Info("token regenerated") // 审计只记事件，绝不记令牌值
	return tok, nil
}

func (g Gateway) SaveSettings(port, timeoutSeconds int) (bool, error) {
	restart, err := g.A.SaveSettings(port, timeoutSeconds)
	if err != nil {
		return false, err
	}
	g.A.Log().Info("settings saved", "port", port, "timeoutSeconds", timeoutSeconds, "restartRequired", restart)
	return restart, nil
}
func (g Gateway) Recent() []telemetry.RequestRecord { return g.A.Recent() }

type ProviderView struct {
	ID, Name, Protocol, BaseURL   string
	Enabled, AllowPrivateUpstream bool
}

// Providers 是供应商管理面；Creds 与 Accounts.Creds 同源注入（Task 12 传
// a.Creds()），Remove 级联删密钥与 Test 读密钥走同一 store。
type Providers struct {
	A     *app.App
	Creds credential.Store
}

func providerView(p storage.Provider) ProviderView {
	return ProviderView{p.ID, p.Name, p.Protocol, p.BaseURL, p.Enabled, p.AllowPrivateUpstream}
}

func (s Providers) List() []ProviderView {
	ps, _ := s.A.Store().Providers().List()
	out := make([]ProviderView, 0, len(ps))
	for _, p := range ps {
		out = append(out, providerView(p))
	}
	return out
}

func (s Providers) Create(name, protocol, baseURL string, enabled, allowPrivate bool) (ProviderView, error) {
	if protocol != "anthropic" {
		return ProviderView{}, fmt.Errorf("P0-A 仅支持协议 anthropic，收到 %q", protocol)
	}
	if err := provider.ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return ProviderView{}, err
	}
	p := storage.Provider{ID: storage.NewID(), Name: name, Protocol: protocol,
		BaseURL: baseURL, Enabled: enabled, AllowPrivateUpstream: allowPrivate}
	if err := s.A.Store().Providers().Save(p); err != nil {
		return ProviderView{}, err
	}
	s.A.Log().Info("provider created", "id", p.ID, "name", name)
	return providerView(p), nil
}

func (s Providers) Update(id, name, baseURL string, enabled, allowPrivate bool) error {
	p, err := s.A.Store().Providers().Get(id)
	if err != nil {
		return err
	}
	if err := provider.ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return err
	}
	p.Name, p.BaseURL, p.Enabled, p.AllowPrivateUpstream = name, baseURL, enabled, allowPrivate
	if err := s.A.Store().Providers().Save(p); err != nil {
		return err
	}
	s.A.Log().Info("provider updated", "id", id)
	return nil
}

// Remove 删除供应商并级联清理账号与密钥（库层 FK 级联删账号，密钥经注入的
// Creds 逐个删除——与 Accounts 同一 store，密钥不残留）。
func (s Providers) Remove(id string) error {
	accounts, _ := s.A.Store().Accounts().ListByProvider(id)
	for _, a := range accounts {
		s.Creds.Delete(a.SecretKey)
	}
	if err := s.A.Store().Providers().Delete(id); err != nil {
		return err
	}
	s.A.Log().Info("provider removed", "id", id)
	return nil
}

func (s Providers) Test(id string) provider.ProbeResult {
	p, err := s.A.Store().Providers().Get(id)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	accounts, _ := s.A.Store().Accounts().ListByProvider(id)
	apiKey := ""
	for _, a := range accounts {
		if a.Enabled {
			if v, err := s.A.Creds().Get(a.SecretKey); err == nil {
				apiKey = string(v)
			}
			break
		}
	}
	return provider.Probe(context15(), p.BaseURL, apiKey, p.AllowPrivateUpstream)
}

type AccountView struct {
	ID, ProviderID, Name, KeyTail, SecretBackend string
	Priority                                     int
	Enabled                                      bool
}

// Accounts 是账号与密钥管理面。KeychainAvailable 注入系统密钥环可用性
// （nil 时回退 credential.OSKeychainAvailable）：测试注入 false 以不触真实
// 钥匙环并断言文件降级后端。
type Accounts struct {
	A                 *app.App
	Creds             credential.Store
	KeychainAvailable func() bool
}

func accountView(a storage.Account) AccountView {
	return AccountView{a.ID, a.ProviderID, a.Name, a.KeyTail, a.SecretBackend, a.Priority, a.Enabled}
}

func (s Accounts) List() []AccountView {
	as, _ := s.A.Store().Accounts().List()
	out := make([]AccountView, 0, len(as))
	for _, a := range as {
		out = append(out, accountView(a))
	}
	return out
}

func (s Accounts) Create(name, providerID, apiKey string, priority int, enabled, allowFallback bool) (AccountView, error) {
	if apiKey == "" {
		return AccountView{}, fmt.Errorf("apiKey required")
	}
	if _, err := s.A.Store().Providers().Get(providerID); err != nil {
		return AccountView{}, fmt.Errorf("provider: %w", err)
	}
	backend, err := s.pickBackend(allowFallback)
	if err != nil {
		return AccountView{}, err
	}
	id := storage.NewID()
	key := credential.AccountKey(id)
	if err := s.Creds.Set(key, []byte(apiKey)); err != nil {
		return AccountView{}, err
	}
	// KeyTail（spec §12.4）：建号时随行写入尾 4——非秘密、供 List/Get 回读。
	row := storage.Account{ID: id, ProviderID: providerID, Name: name, SecretKey: key,
		SecretBackend: backend, KeyTail: tail4(apiKey), Priority: priority, Enabled: enabled}
	if err := s.A.Store().Accounts().Save(row); err != nil {
		s.Creds.Delete(key)
		return AccountView{}, err
	}
	s.A.Log().Info("account created", "id", id, "provider", providerID, "backend", backend)
	return accountView(row), nil
}

// pickBackend 选密钥后端：系统密钥环可用则用之；否则仅在显式允许时降级
// 到 0600 文件——确认闸在这里，Manager 层的 AllowFallback 是其配套。
func (s Accounts) pickBackend(allowFallback bool) (string, error) {
	if s.keychainAvailable() {
		return "os-keychain", nil
	}
	if allowFallback {
		return "fallback-file", nil
	}
	return "", fmt.Errorf("系统密钥存储不可用；需显式允许文件降级（allowFallback）")
}

// keychainAvailable 用注入的探测函数；未注入时探测真实系统密钥环。
func (s Accounts) keychainAvailable() bool {
	if s.KeychainAvailable != nil {
		return s.KeychainAvailable()
	}
	return credential.OSKeychainAvailable()
}

func (s Accounts) Update(id, name string, priority int) error {
	a, err := s.A.Store().Accounts().Get(id)
	if err != nil {
		return err
	}
	a.Name, a.Priority = name, priority
	if err := s.A.Store().Accounts().Save(a); err != nil {
		return err
	}
	s.A.Log().Info("account updated", "id", id, "name", name, "priority", priority)
	return nil
}

func (s Accounts) SetEnabled(id string, on bool) error {
	if err := s.A.Store().Accounts().SetEnabled(id, on); err != nil {
		return err
	}
	s.A.Log().Info("account enabled", "id", id, "on", on)
	return nil
}

func (s Accounts) RotateSecret(id, apiKey string, allowFallback bool) error {
	a, err := s.A.Store().Accounts().Get(id)
	if err != nil {
		return err
	}
	if apiKey == "" {
		return fmt.Errorf("apiKey required")
	}
	backend, err := s.pickBackend(allowFallback)
	if err != nil {
		return err
	}
	if err := s.Creds.Set(a.SecretKey, []byte(apiKey)); err != nil {
		return err
	}
	a.SecretBackend = backend
	// 轮换同时刷新持久化的尾 4（List/Get 即刻回读新值）。历史行（migration 002
	// 之前建的）key_tail 留空直到这里——迁移不读钥匙环，绝不在升级时回填。
	a.KeyTail = tail4(apiKey)
	s.A.Log().Info("account secret rotated", "id", id)
	return s.A.Store().Accounts().Save(a)
}

func (s Accounts) Remove(id string) error {
	a, err := s.A.Store().Accounts().Get(id)
	if err != nil {
		return err
	}
	s.Creds.Delete(a.SecretKey)
	s.A.Log().Info("account removed", "id", id)
	return s.A.Store().Accounts().Delete(id)
}

// Test 分级探测单账号：lookup（账号/供应商定位）→ credential（密钥读取）→
// provider.Probe（15s 上下文，与 Providers.Test 同形态）。密钥经注入的
// s.Creds 读取（与 Create/Rotate 同一 store；Task 12 生产两侧同为 a.Creds()）。
func (s Accounts) Test(id string) provider.ProbeResult {
	a, err := s.A.Store().Accounts().Get(id)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	p, err := s.A.Store().Providers().Get(a.ProviderID)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	key, err := s.Creds.Get(a.SecretKey)
	if err != nil {
		return provider.ProbeResult{Stage: "credential", Error: err.Error()}
	}
	return provider.Probe(context15(), p.BaseURL, string(key), p.AllowPrivateUpstream)
}

// tail4 取密钥本体尾 4 位（不足 4 位时掩码为 "****"）。
func tail4(secret string) string {
	if len(secret) <= 4 {
		return "****"
	}
	return secret[len(secret)-4:]
}

type ModelView struct {
	ID, PublicModel, UpstreamModel string
	Enabled                        bool
}

type Models struct{ A *app.App }

func (s Models) List() []ModelView {
	ms, _ := s.A.Store().Models().List()
	out := make([]ModelView, 0, len(ms))
	for _, m := range ms {
		out = append(out, ModelView{m.ID, m.PublicModel, m.UpstreamModel, m.Enabled})
	}
	return out
}

func (s Models) Create(public, upstream string) (ModelView, error) {
	if public == "" || upstream == "" {
		return ModelView{}, fmt.Errorf("public 与 upstream 均不能为空")
	}
	m := storage.ModelMapping{ID: storage.NewID(), PublicModel: public, UpstreamModel: upstream, Enabled: true}
	if err := s.A.Store().Models().Save(m); err != nil {
		return ModelView{}, err
	}
	s.A.Log().Info("model created", "public", public, "upstream", upstream)
	return ModelView{m.ID, m.PublicModel, m.UpstreamModel, m.Enabled}, nil
}

func (s Models) Update(id, public, upstream string) error {
	m, err := s.A.Store().Models().GetByPublic(public)
	if err != nil && err != storage.ErrNotFound {
		return err
	}
	if err == nil && m.ID != id {
		return fmt.Errorf("公开模型名已存在: %s", public)
	}
	// 按 id 重新取（GetByPublic 只是查重）
	list, _ := s.A.Store().Models().List()
	for _, x := range list {
		if x.ID == id {
			m = x
			break
		}
	}
	if m.ID != id {
		return storage.ErrNotFound
	}
	m.PublicModel, m.UpstreamModel = public, upstream
	if err := s.A.Store().Models().Save(m); err != nil {
		return err
	}
	s.A.Log().Info("model updated", "id", id, "public", public, "upstream", upstream)
	return nil
}

func (s Models) Remove(id string) error {
	if err := s.A.Store().Models().Delete(id); err != nil {
		return err
	}
	s.A.Log().Info("model removed", "id", id)
	return nil
}
