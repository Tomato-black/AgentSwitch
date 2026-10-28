// Package bridge 是管理面桥接层：把 app.App 的能力整理为经 mygo.Bind
// 暴露给 desktop 的服务——Gateway（网关控制）与 Channels（渠道聚合根，
// 每渠道单 Key + 模型建议列表）。后者在 channels.go。
package bridge

import (
	"context"
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
func (g Gateway) SaveSettings(port, timeoutSeconds int) (bool, error) {
	restart, err := g.A.SaveSettings(port, timeoutSeconds)
	if err != nil {
		return false, err
	}
	g.A.Log().Info("settings saved", "port", port, "timeoutSeconds", timeoutSeconds, "restartRequired", restart,
		"actor", "ui", "result", "ok")
	if restart { // 端口变更：base_url 派生的接入文件需刷新（无配置时零写入）
		AccessConfigs{A: g.A}.RegenerateDerived()
	}
	return restart, nil
}
func (g Gateway) Recent() []telemetry.RequestRecord { return g.A.Recent() }
