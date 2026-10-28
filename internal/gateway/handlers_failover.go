// handlers_failover.go 是目标链转移循环：按 decision 顺序逐个尝试候选、
// 发送前后冷却校验、切换/跳过计数与可解释结局（docx §6.3）。
package gateway

import (
	"context"
	"strings"
	"time"

	"AgentSwitch/internal/router"
)

// failoverOutcome 报告目标链转移循环的结局（docx §6.3 可解释记录）。
type failoverOutcome struct {
	Committed bool // 已向客户端写出响应（2xx 或不可转移错误）
	Attempted bool // 至少一个目标真正进入 run
	Switches  int  // 切换/跳过次数（telemetry FailoverCount）
	CoolSkips int  // 发送前二次校验被冷却跳过的次数
}

// allCooling 报告 Resolve 滤掉的目标是否全部仅因冷却。
func allCooling(filtered []router.FilteredCandidate) bool {
	for _, f := range filtered {
		if !strings.HasPrefix(f.Reason, "cooling:") {
			return false
		}
	}
	return true
}

// coolingExhaustedMsg 构造 503 终局正文：含 all targets cooling 与滤因清单
// （docx §6.3 可解释记录）。
func coolingExhaustedMsg(filtered []router.FilteredCandidate) string {
	if len(filtered) == 0 {
		return "all targets cooling"
	}
	reasons := make([]string, 0, len(filtered))
	for _, f := range filtered {
		reasons = append(reasons, f.Candidate.ChannelName+":"+f.Reason)
	}
	return "all targets cooling (filtered: " + strings.Join(reasons, "; ") + ")"
}

// 终局 502/503 由调用方据 outcome 写出（错误 envelope 形状随入站协议而异）。
func (s *Server) failover(ctx context.Context, decision router.Decision,
	run func(c router.Candidate) (bool, error)) (failoverOutcome, error) {
	var out failoverOutcome
	for _, c := range decision.Ordered {
		if ctx.Err() != nil { // 客户端已走：不再发任何上游请求（docx §6.3 取消不转移）
			return out, ctx.Err()
		}
		// 发送前二次校验（docx §7；TryAcquire 锁内原子，不写状态）
		if s.deps.Cooldowns != nil {
			if _, ok := s.deps.Cooldowns.TryAcquire(c.ChannelID, time.Now()); !ok {
				out.CoolSkips++
				out.Switches++
				continue
			}
		}
		out.Attempted = true
		committed, err := run(c)
		if err != nil {
			return out, err
		}
		if committed {
			out.Committed = true
			return out, nil
		}
		out.Switches++
	}
	return out, nil
}
