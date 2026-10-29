package desktop

import (
	"sync"
	"time"

	"AgentSwitch/internal/telemetry"
)

// alertState 是桌面告警评估器：纯逻辑 + 去重时间戳，便于测试。
// 规则：
//   - 全渠道冷却（ErrorClass=no_candidate）5 分钟内一次；
//   - 连续 3 次发生故障转移（FailoverCount>0）5 分钟内一次；
//   - 额度耗尽按渠道 10 分钟内一次。
type alertState struct {
	mu              sync.Mutex
	lastNoCandidate time.Time
	lastFailover    time.Time
	consecutive     int
	lastQuota       map[string]time.Time
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
