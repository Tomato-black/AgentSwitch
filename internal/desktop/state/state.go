// Package state 是桌面 UI 的共享状态：组合根（internal/desktop）与各页
// 子包（pages/*）共同消费。叶子数据包——只依赖 bridge/telemetry，
// 不 import uikit 或任何页面，避免环依赖。
package state

import (
	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/telemetry"
)

// Services 是桌面壳持有的桥接服务（mygo.Bind 与各页共用同一实例；
// 由组合根填充）。
type Services struct {
	Gateway  bridge.Gateway
	Channels bridge.Channels
	Logical  *bridge.LogicalModels
}

// State 是桌面 UI 的全局状态：壳层字段 + 各页状态（随页面搬迁逐步填充）。
type State struct {
	Svc    Services
	Recent []telemetry.RequestRecord // 最近请求记录（渠道页健康统计；壳层 OnRecord 头插）

	Page           string       // 当前页；"logical" 默认（经 normalizedPage 归一）
	CloseNotice    bool         // 首关驻留模态 open 位
	CloseConfirmed bool         // 用户已确认驻留；之后的关窗直接隐藏
	Quitting       bool         // 退出流程中：放行窗口关闭（OnBeforeQuit 置位）
	HideWindow     func()       // 隐藏当前主窗（openWindow 注入；测试可为 nil）
	Schedule       func(func()) // 把窗口操作推迟出 GTK 事件回调（默认 afterEvent）
}

// New 构造初始状态：默认落在逻辑模型页，并带上网关已有的最近请求记录。
func New(svc Services) *State {
	return &State{Svc: svc, Recent: svc.Gateway.Recent(), Page: "logical"}
}
