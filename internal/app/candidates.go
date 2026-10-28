package app

import (
	"errors"
	"time"

	"AgentSwitch/internal/router"
	"AgentSwitch/internal/storage"
)

// candidates.go 是路由候选装配：逻辑模型名 → 候选链（含冷却叠加、
// 桌面别名解析、路由缓存失效），与网关生命周期分开维护。
// 失效键是 Store.RouteVersion：缓存有效 = 版本号与装配时相同。版本号由存储层
// 在每次写后递增，所以任何写入口（桥接、测试夹具、未来 CLI）都无需手动清缓存，
// 「运行中改配置下一次请求即生效」不变量不受缓存影响。
func (a *App) candidates(logicalName string) ([]router.Candidate, error) {
	if target := a.resolveDesktopAlias(logicalName); target != "" {
		logicalName = target
	}
	ver := a.store.RouteVersion()
	a.routeCacheMu.Lock()
	e, ok := a.routeCache[logicalName]
	a.routeCacheMu.Unlock()
	if ok && e.ver == ver {
		return a.withCooldowns(e.cands), nil
	}
	base, err := a.buildCandidates(logicalName)
	if err != nil {
		return nil, err
	}
	a.routeCacheMu.Lock()
	if a.routeCache == nil || a.routeCacheVer != ver {
		// 版本变化即整表重建：残留条目（改名的旧模型）一并清掉，
		// 缓存大小随当前配置规模而非历史模型数增长。
		a.routeCache = make(map[string]cachedRoutes)
		a.routeCacheVer = ver
	}
	a.routeCache[logicalName] = cachedRoutes{ver: ver, cands: base}
	a.routeCacheMu.Unlock()
	return a.withCooldowns(base), nil
}

// withCooldowns 返回 base 的副本，并按当前冷却快照叠加 CoolingUntil/CoolingReason。
// 冷却在内存里、按渠道跨逻辑模型共享且随错误迁移实时变化，绝不能进缓存。
func (a *App) withCooldowns(base []router.Candidate) []router.Candidate {
	out := make([]router.Candidate, len(base))
	copy(out, base)
	if len(out) == 0 || a.cooldowns == nil {
		return out
	}
	cool := map[string]router.CooldownEntry{}
	for _, e := range a.cooldowns.Snapshot(time.Now()) {
		cool[e.ChannelID] = e
	}
	for i := range out {
		if e, ok := cool[out[i].ChannelID]; ok {
			out[i].CoolingUntil, out[i].CoolingReason = e.Until, e.Reason
		}
	}
	return out
}

// buildCandidates 按逻辑模型名装配目标链候选（spec 2026-10-17-logical-models）：
// GetByName → enabled 检查 → ListByModel（priority 升序）→ join channel。
// 不可用目标（渠道 disabled / 缺 credential_ref）跳过并记日志——candidates
// 只返回可用候选。未登记或 disabled：返回空切片，gateway 走「无候选」路径。
// 结果进缓存，因此这里只装静态字段；冷却由 withCooldowns 每次调用现叠。
// resolveDesktopAlias 把 Claude Desktop 入站名（claude-sonnet-5 等角色路由
// ID）解析回对应逻辑模型名；未命中返回空串。别名表每次请求现读（本地 SQLite
// 微秒级），保证 Apply 后即时生效；缓存按解析后的逻辑模型名，不受别名变更
// 污染。
func (a *App) resolveDesktopAlias(name string) string {
	if aliases := storage.DesktopAliases(a.store); aliases != nil {
		return aliases[name]
	}
	return ""
}

func (a *App) buildCandidates(logicalName string) ([]router.Candidate, error) {
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
	enabled := map[string]bool{}
	for _, m := range ms {
		if m.Enabled {
			out = append(out, m.Name)
			enabled[m.Name] = true
		}
	}
	// 桌面别名（claude-* 角色路由 ID）跟随目标逻辑模型的启用态追加，
	// Desktop 靠 /v1/models 校验路由 ID 白名单。
	for alias, target := range storage.DesktopAliases(a.store) {
		if enabled[target] {
			out = append(out, alias)
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
