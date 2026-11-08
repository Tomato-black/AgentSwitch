package desktop

import (
	"sync"
	"time"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/telemetry"
)

// alertState 是桌面告警评估器：纯逻辑 + 去重时间戳，便于测试。
// 规则：
//   - 全渠道冷却（ErrorClass=no_candidate）5 分钟内一次；
//   - 连续 3 次发生故障转移（FailoverCount>0）5 分钟内一次；
//   - 额度耗尽按渠道 10 分钟内一次；
//   - 本地配额估算用量 ≥80% / ≥100% 按渠道 24 小时内各一次（evaluateQuota）。
type alertState struct {
	mu              sync.Mutex
	lastNoCandidate time.Time
	lastFailover    time.Time
	consecutive     int
	lastQuota       map[string]time.Time
	// lastQuotaRatio 按渠道记阈值告警（"80:渠道"/"100:渠道"→时间）。
	lastQuotaRatio map[string]time.Time
	lastQuotaCheck time.Time
}

func newAlertState() *alertState {
	return &alertState{lastQuota: map[string]time.Time{}}
}

// evaluate 返回告警 id/标题/正文；id 为空表示不告警。
func (a *alertState) evaluate(rec telemetry.RequestRecord, now time.Time) (id, title, body string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if rec.FailoverCount > 0 {
		a.consecutive++
	} else {
		a.consecutive = 0
	}
	if rec.ErrorClass == "no_candidate" {
		if now.Sub(a.lastNoCandidate) >= 5*time.Minute {
			a.lastNoCandidate = now
			return "no-candidate", "所有目标渠道冷却中",
				"AgentSwitch：当前所有目标都在冷却，请求可能失败。打开应用查看渠道与冷却原因。"
		}
	}
	if a.consecutive >= 3 && now.Sub(a.lastFailover) >= 5*time.Minute {
		a.lastFailover = now
		a.consecutive = 0
		return "failover", "连续故障转移",
			"AgentSwitch：最近多次请求发生目标切换，请检查主渠道与上游状态。"
	}
	if rec.ErrorClass == "quota_exhausted" {
		ch := rec.Channel
		if ch == "" {
			ch = "-"
		}
		if a.lastQuota == nil {
			a.lastQuota = map[string]time.Time{}
		}
		if now.Sub(a.lastQuota[ch]) >= 10*time.Minute {
			a.lastQuota[ch] = now
			return "quota", "上游额度耗尽",
				"AgentSwitch：渠道「" + ch + "」返回额度耗尽，请更换 Key/渠道或调整目标链。"
		}
	}
	return "", "", ""
}

// evaluateQuota 基于本地渠道配额（P1-2c 估算）做阈值告警：任一渠道用量
// ≥100% 或 ≥80% 各告警一次（按渠道 24 小时去重；100% 优先）。Token 配额
// 优先于 USD 配额判定。
func (a *alertState) evaluateQuota(quotas []bridge.QuotaStatus, now time.Time) (id, title, body string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.lastQuotaRatio == nil {
		a.lastQuotaRatio = map[string]time.Time{}
	}
	var hit80 string
	for _, q := range quotas {
		ratio, ok := quotaRatio(q)
		if !ok {
			continue
		}
		if ratio >= 1 {
			if now.Sub(a.lastQuotaRatio["100:"+q.Channel]) >= 24*time.Hour {
				a.lastQuotaRatio["100:"+q.Channel] = now
				return "quota-100", "渠道额度已用尽",
					"AgentSwitch：渠道「" + q.Channel + "」本地估算用量已达配额 100%，请求可能持续失败。"
			}
			continue
		}
		if ratio >= 0.8 && hit80 == "" && now.Sub(a.lastQuotaRatio["80:"+q.Channel]) >= 24*time.Hour {
			hit80 = q.Channel
		}
	}
	if hit80 != "" {
		a.lastQuotaRatio["80:"+hit80] = now
		return "quota-80", "渠道额度即将用尽",
			"AgentSwitch：渠道「" + hit80 + "」本地估算用量已达配额 80%，请留意剩余额度。"
	}
	return "", "", ""
}

// quotaCheckDue 报告距上次配额阈值检查是否已过 10 分钟（顺带记账），把
// QuotaReport 的 SQL 聚合从每请求降到低频。
func (a *alertState) quotaCheckDue(now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if now.Sub(a.lastQuotaCheck) < 10*time.Minute {
		return false
	}
	a.lastQuotaCheck = now
	return true
}

// quotaRatio 取渠道用量比例：Token 配额优先，其次 USD；未配置返回 false。
func quotaRatio(q bridge.QuotaStatus) (float64, bool) {
	if q.TokenQuota > 0 {
		return float64(q.TokenUsed) / float64(q.TokenQuota), true
	}
	if q.USDQuota > 0 {
		return q.USDUsed / q.USDQuota, true
	}
	return 0, false
}
