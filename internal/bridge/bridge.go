// Package bridge 是管理面桥接层：把 app.App 的能力整理为经 mygo.Bind
// 暴露给 desktop 的服务——Gateway（网关控制）与 Channels（渠道聚合根，
// 每渠道单 Key + 模型建议列表）。后者在 channels.go。
package bridge

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/config"
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

// ModelProbeTarget/ModelProbeResult 是逻辑模型测试结果（app 类型别名，
// 避免桌面层依赖 app 包）。
type ModelProbeTarget = app.ModelProbeTarget
type ModelProbeResult = app.ModelProbeResult

// TestLogicalModel 对逻辑模型目标链发最小请求，返回逐目标结果。
func (g Gateway) TestLogicalModel(ctx context.Context, logicalName string) (ModelProbeResult, error) {
	return g.A.ProbeLogicalModel(ctx, logicalName)
}

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

// Start 启动网关；成功后刷新接入派生文件（端口可能已在停止期间改过，
// 只有真正监听新端口后才让 Agent 配置指向它，2026-10-29 P2-4）。
func (g Gateway) Start() error {
	if err := g.A.StartGateway(); err != nil {
		return err
	}
	AccessConfigs{A: g.A}.RegenerateDerived()
	return nil
}
func (g Gateway) Stop() error { return g.A.StopGateway(context15()) }
func (g Gateway) SaveSettings(port, timeoutSeconds int) (bool, error) {
	restart, err := g.A.SaveSettings(port, timeoutSeconds)
	if err != nil {
		return false, err
	}
	g.A.Log().Info("settings saved", "port", port, "timeoutSeconds", timeoutSeconds, "restartRequired", restart,
		"actor", "ui", "result", "ok")
	// 端口变更后不立即重写派生文件：网关仍在旧端口监听，提前改写会让
	// Agent 指向空端口；等下一次 Start 成功后再刷新（P2-4）。
	return restart, nil
}
func (g Gateway) Recent() []telemetry.RequestRecord { return g.A.Recent() }

// alertsEnabledKey 是故障/冷却告警开关（默认开）。
const alertsEnabledKey = "alerts.enabled"

// AlertsEnabled 返回是否启用故障/冷却告警；未设置默认开启。
func (g Gateway) AlertsEnabled() bool {
	raw, err := g.A.Store().Settings().Get(alertsEnabledKey)
	if err != nil {
		return true
	}
	return raw != "false"
}

// SetAlertsEnabled 持久化告警开关。
func (g Gateway) SetAlertsEnabled(on bool) error {
	v := "false"
	if on {
		v = "true"
	}
	return g.A.Store().Settings().Set(alertsEnabledKey, v)
}

// SetupStatus 是首次上手三步清单的完成计数。
type SetupStatus struct {
	Channels, LogicalModels, AppliedAccess int
}

// Setup 统计渠道/逻辑模型/已应用接入配置数量，供三步清单判断。
func (g Gateway) Setup() SetupStatus {
	var s SetupStatus
	if chs, err := g.A.Store().Channels().List(); err == nil {
		s.Channels = len(chs)
	}
	if lms, err := g.A.Store().LogicalModels().List(); err == nil {
		s.LogicalModels = len(lms)
	}
	for _, app := range []string{"claude", "codex", "claude_desktop"} {
		if _, err := g.A.Store().AccessConfigs().Current(app); err == nil {
			s.AppliedAccess++
		}
	}
	return s
}

// History 返回持久化的最近请求（新→旧）；since 为零值表示不限时间，
// 供设置页筛选与诊断。
func (g Gateway) History(limit int, since time.Time) []telemetry.RequestRecord {
	return g.A.History(limit, since)
}

// ExportHistory 把请求历史导出为脱敏文件（format "json"/"csv"）到
// ~/.agentswitch/exports，返回路径。
func (g Gateway) ExportHistory(format string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return g.A.ExportHistory(filepath.Join(home, ".agentswitch", "exports"), format)
}

// CooldownView 是渠道冷却的 UI 视图：渠道名解析失败时回退 ID。
type CooldownView struct {
	ChannelID        string
	ChannelName      string
	Reason           string
	RemainingSeconds int
}

// Cooldowns 返回当前未过期的渠道冷却快照（内存态，重启清零）。渠道名从
// 库中解析，供渠道卡/逻辑模型卡显示冷却倒计时与原因。
func (g Gateway) Cooldowns() []CooldownView {
	now := time.Now()
	entries := g.A.Cooldowns().Snapshot(now)
	if len(entries) == 0 {
		return nil
	}
	names := map[string]string{}
	if cs, err := g.A.Store().Channels().List(); err == nil {
		for _, c := range cs {
			names[c.ID] = c.Name
		}
	}
	out := make([]CooldownView, 0, len(entries))
	for _, e := range entries {
		remaining := int(e.Until.Sub(now).Round(time.Second) / time.Second)
		if remaining < 0 {
			remaining = 0
		}
		name := names[e.ChannelID]
		if name == "" {
			name = e.ChannelID
		}
		out = append(out, CooldownView{ChannelID: e.ChannelID, ChannelName: name,
			Reason: e.Reason, RemainingSeconds: remaining})
	}
	return out
}
