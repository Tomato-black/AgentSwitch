// Package app 是应用组装层：组合 storage/credential/telemetry/config 与
// gateway 数据面，持有六态状态机、令牌与设置管理，并管理网关生命周期。
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// App 是单实例应用核心：库、日志、环形缓冲、凭据管理器与网关句柄。
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
	creds       credential.Manager
	gw          *gateway.Server
	cooldowns   *router.Cooldowns
	adapters    map[string]provider.Adapter
	state       StateInfo
	onState     []func(StateInfo)
	onRecord    []func(telemetry.RequestRecord)
}

// Open 开库、迁移、建日志与凭据管理器，确保网关令牌存在（空则生成并持久化）。
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
	fb := credential.NewFallbackFile(filepath.Join(filepath.Dir(opts.DBPath), "secrets-fallback.json"))
	a := &App{
		opts:     opts,
		store:    st,
		log:      logger,
		logClose: closer,
		ring:     telemetry.NewRing(100),
		// AllowFallback：显式确认闸在 bridge 的 pickBackend(allowFallback)——无密钥环且
		// 用户未确认时在 Set 之前就报错；Manager 层放行是其配套，否则确认后仍失败。
		// settings 的 credential.fallback_enabled 留作后续 UI 记忆偏好（P0-A 不接）。
		creds: credential.Manager{Primary: credential.OSKeychain{}, Fallback: fb, AllowFallback: true},
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

// Creds 暴露凭据管理器（Task 11 bridge 直接使用）。
func (a *App) Creds() credential.Store { return &a.creds }

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

// candidates 装配公开模型的候选集：enabled 渠道 × enabled 密钥 × enabled
// channel_models(public=X)（spec §6.1），并标注冷却快照。SQL 已按渠道/密钥
// 优先级有序；router 内再排序一次是双保险。
func (a *App) candidates(public string) ([]router.Candidate, error) {
	chans, err := a.store.Channels().List()
	if err != nil {
		return nil, err
	}
	cool := map[string]router.CooldownEntry{}
	for _, e := range a.cooldowns.Snapshot(time.Now()) {
		cool[e.KeyID] = e
	}
	var out []router.Candidate
	for _, ch := range chans {
		if !ch.Enabled {
			continue
		}
		models, err := a.store.ChannelModels().ListByChannel(ch.ID)
		if err != nil {
			return nil, err
		}
		upstream, found := "", false
		for _, m := range models {
			if m.Enabled && m.PublicModel == public {
				upstream, found = m.UpstreamModel, true
				break
			}
		}
		if !found {
			continue
		}
		keys, err := a.store.ChannelKeys().ListByChannel(ch.ID)
		if err != nil {
			return nil, err
		}
		for _, k := range keys {
			if !k.Enabled {
				continue
			}
			c := router.Candidate{ChannelID: ch.ID, ChannelName: ch.Name,
				KeyID: k.ID, KeyName: k.Name, SecretKey: k.SecretKey,
				Protocol: ch.Protocol, BaseURL: ch.BaseURL,
				AllowPrivate: ch.AllowPrivateUpstream, UpstreamModel: upstream,
				ChannelPriority: ch.Priority, KeyPriority: k.Priority}
			if e, ok := cool[k.ID]; ok {
				c.CoolingUntil, c.CoolingReason = e.Until, e.Reason
			}
			out = append(out, c)
		}
	}
	return out, nil
}

func (a *App) publicModels() []string {
	ms, err := a.store.ChannelModels().PublicModels()
	if err != nil {
		return nil
	}
	return ms
}

func (a *App) hasUsableChannel() bool {
	ok, err := a.store.Channels().HasUsableKey()
	return err == nil && ok
}

// StartGateway 组装 gateway.Deps 并启动：Starting → Running/Degraded
// （零可用渠道/密钥 → Degraded 带 reason）；任一步失败 → Failed（reason=err）。
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
			}
		},
		Candidates: a.candidates,
		Cooldowns:  a.cooldowns,
		Cred:       &a.creds,
		Router:     router.PriorityRouter{},
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
	if !a.hasUsableChannel() {
		si.State = StateDegraded
		si.DegradedReason = "no usable channel/key"
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
// load→mutate→save 整段持 settingsMu（config.Save 写全部 7 键，交错会覆盖他人写入）。
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
