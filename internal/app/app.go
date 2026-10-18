// Package app 是应用组装层：组合 storage/credential/telemetry/config 与
// gateway 数据面，持有六态状态机、令牌与设置管理，并管理网关生命周期。
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"AgentSwitch/internal/config"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/gateway"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/storage"
	"AgentSwitch/internal/telemetry"
)

// State 是网关生命周期的六态之一。
type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateDegraded State = "degraded"
	StateStopping State = "stopping"
	StateFailed   State = "failed"
)

// StateInfo 是对外广播的完整状态快照。
type StateInfo struct {
	State          State     `json:"state"`
	Addr           string    `json:"addr,omitempty"`
	DegradedReason string    `json:"degradedReason,omitempty"`
	Since          time.Time `json:"since"`
	Version        string    `json:"version,omitempty"`
}

// Options 是 Open 的入参；Host 缺省 127.0.0.1，DBPath 必填。
type Options struct {
	DBPath, LogPath, Version, Host string
}

// App 是单实例应用核心：库、日志、环形缓冲、凭据存储与网关句柄。
type App struct {
	mu sync.Mutex
	// settingsMu 串行化 settings 的 load→mutate→save 临界区（config.Save 写全部键，
	// 交错会把旧令牌重新持久化）。锁序：settingsMu 可嵌套取 a.mu（SaveSettings→State），
	// 绝不在持 a.mu 时取 settingsMu。纯读路径（currentToken/Limits）不加此锁。
	settingsMu sync.Mutex
	// lifecycleMu 串行化 StartGateway/StopGateway 全函数，关闭双启动与 Stop 竞态窗口。
	// 锁序最外层：可取 a.mu / settingsMu，反向禁止。
	lifecycleMu sync.Mutex
	opts        Options
	store       storage.Store
	logClose    io.Closer
	log         *slog.Logger
	ring        *telemetry.Ring
	creds       credential.Store
	gw          *gateway.Server
	cooldowns   *router.Cooldowns
	adapters    map[string]provider.Adapter
	state       StateInfo
	onState     []func(StateInfo)
	onRecord    []func(telemetry.RequestRecord)
}

// Open 开库、迁移、建日志与加密凭据存储，确保网关令牌存在（空则生成并持久化）。
func Open(opts Options) (*App, error) {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	if opts.DBPath == "" {
		return nil, fmt.Errorf("DBPath required")
	}
	// 首启（C1）：全新机器上配置目录父链不存在，sql.Open+Ping 直接失败
	// "unable to open database file (14)"——先建目录（0700）再开库。
	// 主入口用 os.UserConfigDir() 拼路径，父目录何时缺失不可预期。
	if err := os.MkdirAll(filepath.Dir(opts.DBPath), 0o700); err != nil {
		return nil, err
	}
	st, err := storage.Open(opts.DBPath)
	if err != nil {
		return nil, err
	}
	logger, closer, err := telemetry.NewLogger(opts.LogPath)
	if err != nil {
		st.Close()
		return nil, err
	}
	// 旧明文降级文件清场（2026-10-18 存储改造）：凭据已 AES-256-GCM 加密落库，
	// 磁盘上不得残留 secrets-fallback.json 明文。尽力而为，删不掉不阻塞启动。
	_ = os.Remove(filepath.Join(filepath.Dir(opts.DBPath), "secrets-fallback.json"))
	a := &App{
		opts:     opts,
		store:    st,
		log:      logger,
		logClose: closer,
		ring:     telemetry.NewRing(100),
		creds:    st.Secrets(),
	}
	a.cooldowns = router.NewCooldowns()
	a.adapters = map[string]provider.Adapter{
		"anthropic": provider.NewAnthropicAdapter(),
		"openai":    provider.NewOpenAIAdapter(logger),
	}
	// 确保令牌存在（settingsMu 临界区，与 RegenerateToken/SaveSettings 同口径）
	if err := func() error {
		a.settingsMu.Lock()
		defer a.settingsMu.Unlock()
		s, err := a.settings()
		if err != nil {
			return err
		}
		if s.GatewayToken == "" {
			_, err = a.regenerateToken()
			return err
		}
		return nil
	}(); err != nil {
		a.Close()
		return nil, err
	}
	a.setState(StateInfo{State: StateStopped, Since: time.Now(), Version: opts.Version})
	return a, nil
}

func (a *App) Store() storage.Store  { return a.store }
func (a *App) Ring() *telemetry.Ring { return a.ring }
func (a *App) Log() *slog.Logger     { return a.log }

// LogPath 返回日志文件路径（opts.LogPath；可为空=写 stderr）。概览页用它做
// 降级/失败时的日志位置提示（spec §17 可诊断性）。
func (a *App) LogPath() string { return a.opts.LogPath }

// Creds 暴露凭据存储（库内 AES-256-GCM 加密；bridge 直接使用）。
func (a *App) Creds() credential.Store { return a.creds }

// CurrentSettings 返回当前生效设置（Task 12 desktop 直接使用）。
func (a *App) CurrentSettings() (config.Settings, error) { return a.settings() }

// OnState 注册状态回调；每次 setState 全量广播快照。
func (a *App) OnState(fn func(StateInfo)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onState = append(a.onState, fn)
}

// OnRecord 注册请求记录回调；网关每条 RequestRecord 都会广播。
func (a *App) OnRecord(fn func(telemetry.RequestRecord)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onRecord = append(a.onRecord, fn)
}

// State 返回当前状态快照。
func (a *App) State() StateInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

// setState 在锁内更新状态并快照回调列表，锁外调用（回调线程安全）。
func (a *App) setState(si StateInfo) {
	a.mu.Lock()
	a.state = si
	cbs := append([]func(StateInfo){}, a.onState...)
	a.mu.Unlock()
	for _, fn := range cbs {
		fn(si)
	}
}

func (a *App) settings() (config.Settings, error) {
	return config.Load(a.store.Settings().Get)
}

// currentToken 读当前网关令牌；读取失败按空串处理（authorized 会拒绝）。
func (a *App) currentToken() string {
	s, err := a.settings()
	if err != nil {
		return ""
	}
	return s.GatewayToken
}

// Cooldowns 暴露冷却存储（bridge PoolStatus 读取用）。
func (a *App) Cooldowns() *router.Cooldowns { return a.cooldowns }

// candidates 按逻辑模型名装配目标链候选（spec 2026-10-17-logical-models）：
// GetByName → enabled 检查 → ListByModel（priority 升序）→ join channel。
// 不可用目标（渠道 disabled / 缺 credential_ref）跳过并记日志——candidates
// 只返回可用候选。未登记或 disabled：返回空切片，gateway 走「无候选」路径。
// 冷却快照按 ChannelID 注入（冷却跨逻辑模型共享）。
func (a *App) candidates(logicalName string) ([]router.Candidate, error) {
	lm, err := a.store.LogicalModels().GetByName(logicalName)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return []router.Candidate{}, nil
		}
		return nil, err
	}
	if !lm.Enabled {
		return []router.Candidate{}, nil
	}
	targets, err := a.store.LogicalModelTargets().ListByModel(lm.ID)
	if err != nil {
		return nil, err
	}
	cool := map[string]router.CooldownEntry{}
	for _, e := range a.cooldowns.Snapshot(time.Now()) {
		cool[e.ChannelID] = e
	}
	out := make([]router.Candidate, 0, len(targets))
	for _, t := range targets {
		ch, err := a.store.Channels().Get(t.ChannelID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) {
				a.log.Debug("skip target: channel gone", "target", t.ID, "channel", t.ChannelID)
				continue
			}
			return nil, err
		}
		if !ch.Enabled || ch.CredentialRef == "" {
			a.log.Debug("skip target: channel unusable", "target", t.ID, "channel", t.ChannelID,
				"enabled", ch.Enabled, "hasCredential", ch.CredentialRef != "")
			continue
		}
		c := router.Candidate{
			TargetID: t.ID, LogicalModel: logicalName,
			ChannelID: ch.ID, ChannelName: ch.Name,
			Protocol: ch.Protocol, BaseURL: ch.BaseURL,
			SecretKey: ch.CredentialRef, AllowPrivate: ch.AllowPrivateUpstream,
			UpstreamModel: t.ModelName, Priority: t.Priority,
		}
		if e, ok := cool[ch.ID]; ok {
			c.CoolingUntil, c.CoolingReason = e.Until, e.Reason
		}
		out = append(out, c)
	}
	return out, nil
}

// publicModels 返回 enabled 逻辑模型名（网关对外模型清单）。
func (a *App) publicModels() []string {
	ms, err := a.store.LogicalModels().List()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ms))
	for _, m := range ms {
		if m.Enabled {
			out = append(out, m.Name)
		}
	}
	return out
}

// hasUsableChannel 报告是否存在可用渠道（enabled 且 credential_ref 非空）。
func (a *App) hasUsableChannel() bool {
	ok, err := a.store.Channels().HasUsableKey()
	return err == nil && ok
}

// hasRouteReady 是网关启动守卫：至少一个 enabled 逻辑模型且至少一个可用渠道。
func (a *App) hasRouteReady() bool {
	if !a.hasUsableChannel() {
		return false
	}
	ms, err := a.store.LogicalModels().List()
	if err != nil {
		return false
	}
	for _, m := range ms {
		if m.Enabled {
			return true
		}
	}
	return false
}

// StartGateway 组装 gateway.Deps 并启动：Starting → Running/Degraded
// （无 enabled 逻辑模型或无可用渠道 → Degraded 带 reason）；任一步失败 → Failed（reason=err）。
// lifecycleMu 串行化全函数；已在运行时拒绝且不改变状态（状态必须来自实际服务状态）。
func (a *App) StartGateway() error {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	a.mu.Lock()
	running := a.gw != nil
	a.mu.Unlock()
	if running {
		return fmt.Errorf("gateway already running")
	}
	a.setState(StateInfo{State: StateStarting, Since: time.Now(), Version: a.State().Version})
	s, err := a.settings()
	if err != nil {
		a.setState(StateInfo{State: StateFailed, DegradedReason: err.Error(), Since: time.Now(), Version: a.State().Version})
		return err
	}
	d := gateway.Deps{
		Token: a.currentToken,
		Limits: func() gateway.Limits {
			s, _ := a.settings()
			return gateway.Limits{
				MaxBodyBytes: s.MaxBodyBytes, MaxConcurrent: s.MaxConcurrent,
				RequestTimeout: time.Duration(s.RequestTimeoutSeconds) * time.Second,
				ReadTimeout:    30 * time.Second, // 读请求（含 body）上限；LLM 请求体在本机回环毫秒级到达，30s 足够
			}
		},
		Candidates: a.candidates,
		Passthrough: func() bool {
			s, err := a.settings()
			return err == nil && s.PassthroughUnregistered
		},
		Cooldowns: a.cooldowns,
		Cred:      a.creds,
		Router:    router.PriorityRouter{},
		AdapterFor: func(protocol string) (provider.Adapter, error) {
			ad, ok := a.adapters[protocol]
			if !ok {
				return nil, fmt.Errorf("unknown protocol %q", protocol)
			}
			return ad, nil
		},
		ListPublicModels: a.publicModels,
		HasUsableChannel: a.hasUsableChannel,
		Log:              a.log,
		Ring:             a.ring,
		OnRecord: func(rec telemetry.RequestRecord) {
			a.mu.Lock()
			cbs := append([]func(telemetry.RequestRecord){}, a.onRecord...)
			a.mu.Unlock()
			for _, fn := range cbs {
				fn(rec)
			}
		},
	}
	gw := gateway.New(d)
	addr, err := gw.Start(a.opts.Host, s.Port)
	if err != nil {
		a.setState(StateInfo{State: StateFailed, DegradedReason: err.Error(), Since: time.Now(), Version: a.State().Version})
		return err
	}
	a.mu.Lock()
	a.gw = gw
	a.mu.Unlock()
	si := StateInfo{State: StateRunning, Addr: addr, Since: time.Now(), Version: a.State().Version}
	if !a.hasRouteReady() {
		si.State = StateDegraded
		si.DegradedReason = "no enabled logical model or usable channel"
	}
	a.log.Info("gateway started", "addr", addr, "state", si.State)
	a.setState(si)
	return nil
}

// StopGateway 停网关：Stopping → Stopped；无网关时幂等直接返回。
// lifecycleMu 与 StartGateway 串行，关闭「刚停又被并发启动」的竞态窗口。
func (a *App) StopGateway(ctx context.Context) error {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()
	a.mu.Lock()
	gw := a.gw
	a.mu.Unlock()
	if gw == nil {
		return nil
	}
	a.setState(StateInfo{State: StateStopping, Addr: gw.Addr(), Since: time.Now(), Version: a.State().Version})
	err := gw.Shutdown(ctx)
	a.mu.Lock()
	a.gw = nil
	a.mu.Unlock()
	a.setState(StateInfo{State: StateStopped, Since: time.Now(), Version: a.State().Version})
	a.log.Info("gateway stopped")
	return err
}

// RegenerateToken 生成 32 字节 crypto/rand 令牌（hex）并持久化到 settings。
// settingsMu 包住完整 load→mutate→save，避免与 SaveSettings 交错把旧令牌重新写回。
func (a *App) RegenerateToken() (string, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	return a.regenerateToken()
}

// regenerateToken 是无锁实现，调用方必须已持 settingsMu。
func (a *App) regenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	s, err := a.settings()
	if err != nil {
		return "", err
	}
	s.GatewayToken = tok
	if err := s.Save(a.store.Settings().Set); err != nil {
		return "", err
	}
	return tok, nil
}

// SaveSettings 保存端口与请求超时；网关运行中且端口变化时要求重启。
// load→mutate→save 整段持 settingsMu（config.Save 写全部键，交错会覆盖他人写入）。
func (a *App) SaveSettings(port, timeoutSeconds int) (bool, error) {
	a.settingsMu.Lock()
	defer a.settingsMu.Unlock()
	s, err := a.settings()
	if err != nil {
		return false, err
	}
	restart := port != s.Port && a.State().State != StateStopped
	s.Port = port
	s.RequestTimeoutSeconds = timeoutSeconds
	if err := s.Save(a.store.Settings().Set); err != nil {
		return false, err
	}
	return restart, nil
}

// Recent 返回环形缓冲中的最近请求（新→旧）。
func (a *App) Recent() []telemetry.RequestRecord { return a.ring.List() }

// Close 先停网关，再关日志与库；可安全重复调用。
func (a *App) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = a.StopGateway(ctx)
	a.mu.Lock()
	closer := a.logClose
	st := a.store
	a.mu.Unlock()
	if closer != nil {
		closer.Close()
	}
	if st != nil {
		return st.Close()
	}
	return nil
}
