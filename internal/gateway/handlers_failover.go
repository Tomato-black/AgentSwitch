// handlers_failover.go 是目标链转移循环：按 decision 顺序逐个尝试候选、
// 发送前后冷却校验、切换/跳过计数与可解释结局（docx §6.3）。
package gateway

import (
	"context"
	"strings"
	"time"

	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
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

// coolingExhaustedMsg 构造 503 终局正文：中文冷却说明 + 渠道/原因清单
// （docx §6.3 可解释记录）。
func coolingExhaustedMsg(filtered []router.FilteredCandidate) string {
	if len(filtered) == 0 {
		return "所有目标渠道都在冷却中，请稍后重试"
	}
	reasons := make([]string, 0, len(filtered))
	for _, f := range filtered {
		reason := strings.TrimPrefix(f.Reason, "cooling:")
		reasons = append(reasons, f.Candidate.ChannelName+"（"+cooldownReasonWord(reason)+"）")
	}
	return "所有目标渠道都在冷却中：" + strings.Join(reasons, "；") + "。请稍后重试"
}

// cooldownReasonWord 把内部错误分类译成 Agent 可读中文（错误正文用）。
func cooldownReasonWord(reason string) string {
	switch reason {
	case "rate_limited":
		return "上游限流"
	case "quota_exhausted":
		return "额度耗尽"
	case "server":
		return "上游 5xx"
	case "connect":
		return "连接失败"
	case "auth":
		return "鉴权失败"
	case "endpoint":
		return "端点错误"
	default:
		return reason
	}
}

// 终局 502/503 由调用方据 outcome 写出（错误 envelope 形状随入站协议而异）。
func (s *Server) failover(ctx context.Context, decision router.Decision, rec *telemetry.RequestRecord,
	run func(c router.Candidate) (bool, error)) (failoverOutcome, error) {
	var out failoverOutcome
	record := func(a telemetry.AttemptRecord) {
		if rec != nil {
			rec.Attempts = append(rec.Attempts, a)
		}
	}
	// 先记 Router 已过滤的候选（原因如 cooling / disabled / no-key）。
	for _, f := range decision.Filtered {
		record(telemetry.AttemptRecord{
			Channel: f.Candidate.ChannelName, Model: f.Candidate.UpstreamModel,
			Outcome: "filtered", Reason: f.Reason,
		})
	}
	for _, c := range decision.Ordered {
		if ctx.Err() != nil { // 客户端已走：不再发任何上游请求（docx §6.3 取消不转移）
			record(telemetry.AttemptRecord{Channel: c.ChannelName, Model: c.UpstreamModel, Outcome: "canceled"})
			return out, ctx.Err()
		}
		// 发送前二次校验（docx §7；TryAcquire 锁内原子，不写状态）
		if s.deps.Cooldowns != nil {
			if ce, ok := s.deps.Cooldowns.TryAcquire(c.ChannelID, time.Now()); !ok {
				out.CoolSkips++
				out.Switches++
				record(telemetry.AttemptRecord{
					Channel: c.ChannelName, Model: c.UpstreamModel,
					Outcome: "cooling_skip", Reason: "cooling:" + ce.Reason,
				})
				continue
			}
		}
		out.Attempted = true
		start := time.Now()
		committed, err := run(c)
		outcome := "failed"
		if committed {
			outcome = "ok"
		}
		record(telemetry.AttemptRecord{
			Channel: c.ChannelName, Model: c.UpstreamModel,
			Outcome: outcome, DurationMs: time.Since(start).Milliseconds(),
		})
		if err != nil {
			return out, err
		}
		if committed {
			out.Committed = true
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkSuccess(c.ChannelID)
			}
			return out, nil
		}
		out.Switches++
	}
	return out, nil
}
