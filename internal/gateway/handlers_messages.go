package gateway

import (
	"AgentSwitch/internal/modelid"
	"AgentSwitch/internal/provider"
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"AgentSwitch/internal/protocol"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	rec := telemetry.RequestRecord{ID: telemetryID(), StartedAt: started}
	switches := 0
	defer func() {
		rec.DurationMs = time.Since(started).Milliseconds()
		rec.FailoverCount = switches
		// 请求日志行（I5/spec §10 审计）：只记脱敏元数据；零密钥、零请求体、零 Authorization。
		if s.deps.Log != nil {
			s.deps.Log.Info("request", "request_id", rec.ID, "model", rec.Model,
				"upstream_model", rec.UpstreamModel, "channel", rec.Channel,
				"account", rec.Account, "status", rec.Status,
				"duration_ms", rec.DurationMs, "error_class", rec.ErrorClass,
				"streaming", rec.Streaming, "switches", switches)
		}
		if s.deps.Ring != nil {
			s.deps.Ring.Add(rec)
		}
		if s.deps.OnRecord != nil {
			s.deps.OnRecord(rec)
		}
	}()

	body, release, status, errType, msg, ok := s.gate(r, &rec)
	if release != nil {
		defer release()
	}
	if !ok {
		writeErr(w, status, errType, msg)
		return
	}
	lim := s.deps.Limits()
	// 安全顺序：必须先 Parse（字面 null body 会死在 "model is required"）。
	cr, err := protocol.ParseChatRequest(body)
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErr(w, 400, protocol.ErrTypeInvalidRequest, "请求体无效：需要 model 与合法 JSON")
		return
	}
	cr.Model, _ = modelid.SplitOneM(cr.Model)
	rec.Model, rec.Streaming = cr.Model, cr.Stream

	cands, err := s.deps.Candidates(cr.Model)
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErr(w, 500, protocol.ErrTypeAPI, "candidates unavailable")
		return
	}
	if len(cands) == 0 {
		// 未登记模型：passthrough 开亦 404——设置项仅预留（后续接单渠道直连），
		// 避免半成品语义（简报 Task 6）。
		if s.deps.Passthrough != nil && s.deps.Passthrough() && s.deps.Log != nil {
			s.deps.Log.Debug("passthrough reserved: rejecting unregistered model", "model", cr.Model)
		}
		rec.Status, rec.ErrorClass = 404, protocol.ErrTypeNotFound
		writeErr(w, 404, protocol.ErrTypeNotFound,
			"模型「"+cr.Model+"」未在 AgentSwitch 登记；可用模型："+strings.Join(s.deps.ListPublicModels(), ", "))
		return
	}
	decision := s.deps.Router.Resolve(cr.Model, cands)

	// 超时语义（spec 2026-10-22）：非流式为总时长上限；流式的 RequestTimeout 只
	// 约束等待响应头，2xx 提交点即停表，其后由 copyStream 的空闲 watchdog 接管。
	ctx := r.Context()
	var cancel context.CancelFunc
	var headerTimer *time.Timer
	var headerTimedOut atomic.Bool
	if lim.RequestTimeout > 0 {
		if cr.Stream {
			ctx, cancel = context.WithCancel(ctx)
			headerTimer = time.AfterFunc(lim.RequestTimeout, func() {
				headerTimedOut.Store(true)
				cancel()
			})
		} else {
			ctx, cancel = context.WithTimeout(ctx, lim.RequestTimeout)
		}
		defer cancel()
		defer func() {
			if headerTimer != nil {
				headerTimer.Stop()
			}
		}()
	}

	lastClass := provider.ClassUnknown
	out, ferr := s.failover(ctx, decision, &rec, func(c router.Candidate) (bool, error) {
		apiKey, err := s.deps.Cred.Get(c.SecretKey)
		if err != nil {
			// 凭据失效（被吊销/钥匙环删除）= 死密钥：按上游 401 同口径冷却
			// （ClassAuth→1h）再换下一候选——单枚死钥不得瘫痪整个池，也不得
			// 每请求重试。此刻零字节已写客户端，属可转移窗口。
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.ChannelID, provider.ClassAuth, 0, time.Now())
			}
			lastClass = "credential"
			return false, nil
		}
		adapter, err := s.deps.AdapterFor(c.Protocol)
		if err != nil {
			// 渠道协议配置错，密钥无辜：不冷却，换下一候选。
			lastClass = provider.ClassUnknown
			return false, nil
		}
		rewritten, err := protocol.RewriteModel(body, c.UpstreamModel)
		if err != nil {
			rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
			writeErr(w, 400, protocol.ErrTypeInvalidRequest, "请求体无效")
			return true, nil // 已写终态响应，不换候选
		}
		res, err := adapter.Do(ctx, provider.DoRequest{
			BaseURL: c.BaseURL, AllowPrivate: c.AllowPrivate, APIKey: string(apiKey),
			UpstreamModel: c.UpstreamModel, RawBody: rewritten, Stream: cr.Stream,
			UserAgent: r.Header.Get("User-Agent"),
		})
		// 兼容部分 Anthropic 兼容上游：thinking 模式历史不完整时返回 400
		// 「content[].thinking must be passed back」。去掉本次 thinking 声明与
		// thinking 块后重试一次（响应尚未提交，仍属可转移窗口），避免整次会话卡死。
		if err == nil && res.StatusCode == http.StatusBadRequest && c.Protocol == "anthropic" {
			snippet, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
			res.Body.Close()
			res.Body = io.NopCloser(bytes.NewReader(snippet))
			if strings.Contains(string(snippet), "content[].thinking") {
				if stripped, changed, serr := protocol.StripThinking(rewritten); serr == nil && changed {
					if s.deps.Log != nil {
						s.deps.Log.Info("retry without thinking", "request_id", rec.ID, "channel", c.ChannelName)
					}
					res2, err2 := adapter.Do(ctx, provider.DoRequest{
						BaseURL: c.BaseURL, AllowPrivate: c.AllowPrivate, APIKey: string(apiKey),
						UpstreamModel: c.UpstreamModel, RawBody: stripped, Stream: cr.Stream,
						UserAgent: r.Header.Get("User-Agent"),
					})
					if err2 == nil {
						res, err = res2, nil
					} else {
						err = err2
					}
				}
			}
		}
		if err != nil {
			// 客户端已取消：对候选零状态副作用，直接收尾（docx §6.3 取消即停）。
			// 循环顶的守卫只挡「下一候选」，挡不住本分支的标记动作——取消引发的
			// Do 错误是客户端断开而非上游故障，不得冷却、不得计 switches。
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			// Do 错误（连接层/3xx/请求转换失败）：冷却后换下一候选——此刻未向
			// 客户端写过任何字节，属可转移窗口。
			lastClass = provider.ClassConnect
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.ChannelID, provider.ClassConnect, 0, time.Now())
			}
			return false, nil
		}
		if res.StatusCode >= 400 {
			snippet, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
			res.Body.Close()
			class := provider.Classify(res.StatusCode, res.RetryAfterSec, snippet)
			s.logUpstreamError(rec.ID, c, res.StatusCode, class, snippet)
			if class == provider.ClassParam {
				// 400 类=客户端自身问题：不冷却、不转移，原样回传（docx §6.3）
				rec.Status, rec.ErrorClass = res.StatusCode, string(class)
				rec.Account, rec.Channel = c.ChannelName, c.ChannelName
				writeUpstreamError(w, res.StatusCode, snippet, res.Header)
				return true, nil // 不可转移错误已写出，停止
			}
			lastClass = class
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.ChannelID, class, res.RetryAfterSec, time.Now())
			}
			return false, nil
		}
		// 2xx：提交点——写响应头之后绝不转移（首字节后不重放，docx §6.3）
		rec.Status = res.StatusCode
		rec.UpstreamModel, rec.Account, rec.Channel = c.UpstreamModel, c.ChannelName, c.ChannelName
		for _, h := range responseHeaderWhitelist {
			if v := res.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		// 提交点：响应头即将下发，头部等待计时器必须停表——否则活跃长流会被
		// 总超时切断（首字节后绝不转移/重放，故此处只能停止，不能换候选）。
		if cr.Stream && headerTimer != nil {
			headerTimer.Stop()
		}
		w.WriteHeader(res.StatusCode)
		if cr.Stream {
			onIdle := protocol.AnthropicErrorEvent("upstream stream idle timeout")
			uw, ut := trackedWriter(w)
			if copyStream(uw, res.Body, lim.RequestTimeout, onIdle) {
				rec.ErrorClass = "stream"
				s.logIdleTimeout(rec.ID, lim.RequestTimeout)
			}
			ut.Flush()
			rec.InputTokens, rec.OutputTokens = ut.Usage()
		} else {
			full, _ := io.ReadAll(io.LimitReader(res.Body, 64<<20))
			w.Write(full)
			if in, out, ok := protocol.ExtractUsage(full); ok {
				rec.InputTokens, rec.OutputTokens = in, out
			}
		}
		res.Body.Close()
		return true, nil
	})
	switches = out.Switches
	if ferr != nil || out.Committed {
		// 头部等待超时（区别于客户端断开）：计时器取消 ctx 且响应未提交时，客户端
		// 必须收到明确的 504，而不是 200 + 空 body；客户端取消保持静默。
		if ferr != nil && !out.Committed && headerTimedOut.Load() {
			rec.Status, rec.ErrorClass = http.StatusGatewayTimeout, "timeout"
			writeErr(w, http.StatusGatewayTimeout, protocol.ErrTypeAPI, "upstream response header timeout")
		}
		return // 客户端已断 / 响应已写：不再写终局错误
	}
	// 耗尽且可区分（简报 Task 6）：全部仅因冷却不可用 → 503 中文冷却说明；
	// 至少一个目标实际失败过 → 502 all targets failed。
	if !out.Attempted && (out.CoolSkips > 0 || len(decision.Filtered) > 0) && allCooling(decision.Filtered) {
		rec.Status, rec.ErrorClass = 503, "no_candidate"
		writeErr(w, 503, protocol.ErrTypeAPI, coolingExhaustedMsg(decision.Filtered))
		return
	}
	rec.Status, rec.ErrorClass = 502, string(lastClass)
	writeErr(w, 502, protocol.ErrTypeAPI, "all targets failed")
}
