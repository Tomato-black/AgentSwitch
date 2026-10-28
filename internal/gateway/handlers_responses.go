package gateway

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"AgentSwitch/internal/protocol"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

// handleResponses 是 OpenAI Responses 入站（POST /v1/responses）的 handler。
// 鉴权/并发/读体走共用 gate，随后解析请求并做模型解析；错误体为 OpenAI 形状
// （与 /v1/chat/completions 一致）。failover 循环与 handleChatCompletions 同构：
// 差异仅在（a）请求体先经 ResponsesRequestToChat 转成 chat 形状（b）2xx 响应
// 经 ChatCompletionToResponses / ChatStreamToResponsesStream 回转为 Responses。
// 提交点=向客户端写响应头：其后绝不换候选重放。
func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	rec := telemetry.RequestRecord{ID: telemetryID(), StartedAt: started}
	switches := 0
	defer func() {
		rec.DurationMs = time.Since(started).Milliseconds()
		rec.FailoverCount = switches
		// 请求日志行（spec §10 审计）：只记脱敏元数据；零密钥、零请求体、零 Authorization。
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
		writeErrOpenAI(w, status, errType, msg)
		return
	}
	req, err := protocol.ParseResponsesRequest(body)
	if err != nil {
		// previous_response_id 等不支持字段由 ParseResponsesRequest 显式拒为 400，
		// 避免静默丢失上下文。
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, err.Error())
		return
	}
	req.Model = stripOneM(req.Model)
	rec.Model, rec.Streaming = req.Model, req.Stream

	cands, err := s.deps.Candidates(req.Model)
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErrOpenAI(w, 500, protocol.ErrTypeAPI, "candidates unavailable")
		return
	}
	if len(cands) == 0 {
		// 未登记模型：列出公开模型名便于客户端纠错（错误体仍为 OpenAI 形状）。
		rec.Status, rec.ErrorClass = 404, protocol.ErrTypeNotFound
		writeErrOpenAI(w, 404, protocol.ErrTypeNotFound,
			"unknown model "+req.Model+"; models: "+strings.Join(s.deps.ListPublicModels(), ", "))
		return
	}
	decision := s.deps.Router.Resolve(req.Model, cands)
	// 工具上下文持有「chat 名 → Responses 工具语义」映射，请求期只读；
	// 无法映射的工具在转换时丢弃，此处记一条 debug 便于排查。
	tc := protocol.NewResponseToolContext(req)
	if tc.DroppedTools() > 0 && s.deps.Log != nil {
		s.deps.Log.Debug("responses tools dropped", "request_id", rec.ID, "count", tc.DroppedTools())
	}

	lim := s.deps.Limits()
	// 超时语义同 handleChatCompletions（spec 2026-10-22）：非流式为总时长上限；
	// 流式的 RequestTimeout 只约束等待响应头，2xx 提交点即停表，其后由 copyStream
	// 的空闲 watchdog 接管。
	ctx := r.Context()
	var cancel context.CancelFunc
	var headerTimer *time.Timer
	var headerTimedOut atomic.Bool
	if lim.RequestTimeout > 0 {
		if req.Stream {
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
	out, ferr := s.failover(ctx, decision, func(c router.Candidate) (bool, error) {
		apiKey, err := s.deps.Cred.Get(c.SecretKey)
		if err != nil {
			// 死密钥：按上游 401 同口径冷却后换下一候选（同 handleChatCompletions）。
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.ChannelID, provider.ClassAuth, 0, time.Now())
			}
			lastClass = "credential"
			return false, nil
		}
		// 按候选协议准备出站：Responses 请求先转 chat.completions 形状；
		//   openai    → 直通（转换后的 body 已是 chat.completions 形状）；
		//   anthropic → 再把 chat 形状转 Anthropic（双跳转换），响应侧反向中转。
		var rawBody []byte
		var adapter provider.Adapter
		chatBody, err := protocol.ResponsesRequestToChat(req, tc, c.UpstreamModel)
		if err != nil {
			rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
			writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
			return true, nil // 已写终态响应，不换候选
		}
		switch c.Protocol {
		case "openai":
			rawBody, adapter = chatBody, newOpenAIPassthrough()
		case "anthropic":
			oa, perr := protocol.ParseOpenAIChatRequest(chatBody)
			if perr != nil {
				rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
				writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
				return true, nil
			}
			rawBody, err = protocol.OpenAIRequestToAnthropic(oa, c.UpstreamModel)
			if err != nil {
				rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
				writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
				return true, nil
			}
			adapter, err = s.deps.AdapterFor("anthropic")
			if err != nil {
				lastClass = provider.ClassUnknown
				return false, nil
			}
		default:
			// 渠道协议配置错，密钥无辜：不冷却，换下一候选。
			lastClass = provider.ClassUnknown
			return false, nil
		}
		res, err := adapter.Do(ctx, provider.DoRequest{
			BaseURL: c.BaseURL, AllowPrivate: c.AllowPrivate, APIKey: string(apiKey),
			UpstreamModel: c.UpstreamModel, RawBody: rawBody, Stream: req.Stream,
			UserAgent: r.Header.Get("User-Agent"),
		})
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err() // 客户端已走：零状态副作用，不转移
			}
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
				// 400 类=客户端自身问题：不冷却、不转移，原样回传（docx §6.3）。
				rec.Status, rec.ErrorClass = res.StatusCode, string(class)
				rec.Account, rec.Channel = c.ChannelName, c.ChannelName
				writeUpstreamErrorOpenAI(w, res.StatusCode, snippet, res.Header)
				return true, nil
			}
			lastClass = class
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.ChannelID, class, res.RetryAfterSec, time.Now())
			}
			return false, nil
		}
		// 2xx：提交点——写响应头之后绝不转移（首字节后不重放，docx §6.3）。
		rec.Status = res.StatusCode
		rec.UpstreamModel, rec.Account, rec.Channel = c.UpstreamModel, c.ChannelName, c.ChannelName
		for _, h := range responseHeaderWhitelist {
			if v := res.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		// 提交点：响应头即将下发，头部等待计时器必须停表——否则活跃长流会被
		// 总超时切断（首字节后绝不转移/重放，故此处只能停止，不能换候选）。
		if req.Stream && headerTimer != nil {
			headerTimer.Stop()
		}
		if req.Stream {
			// 流式：chat.completion.chunk SSE 泵成 Responses SSE；空闲超时补
			// response.failed（不伪造 response.completed）。
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(res.StatusCode)
			var mid io.ReadCloser // anthropic 双跳的中间 chat 流，必须显式关闭
			var src io.ReadCloser
			if c.Protocol == "anthropic" {
				mid = protocol.AnthropicStreamToOpenAIStream(ctx, res.Body, req.Model)
				src = protocol.ChatStreamToResponsesStream(ctx, mid, req.Model, tc)
			} else {
				src = protocol.ChatStreamToResponsesStream(ctx, res.Body, req.Model, tc)
			}
			onIdle := protocol.ResponsesFailedEvent("upstream stream idle timeout")
			timedOut := copyStream(w, src, lim.RequestTimeout, onIdle)
			if timedOut {
				rec.ErrorClass = "stream"
				s.logIdleTimeout(rec.ID, lim.RequestTimeout)
			} else if rs, ok := src.(interface{ Failed() bool }); ok && rs.Failed() {
				// 提交后的流中断（上游截断/流内 error）：客户端已收到 response.failed，
				// 遥测同样必须标记；空闲超时路径已有自己的 WARN，这里不重复告警。
				rec.ErrorClass = "stream"
				s.logStreamFailed(rec.ID)
			}
			if mid != nil {
				mid.Close() // 解除中间泵写端阻塞，避免 goroutine 泄漏
			}
			res.Body.Close()
			return true, nil
		}
		// 非流式：整体读出转 Responses 响应对象。转换失败仍可转移——尚未向
		// 客户端写字节；上游 200 坏体不冷却、不计协议无罪。
		full, rerr := io.ReadAll(io.LimitReader(res.Body, 64<<20))
		res.Body.Close()
		if rerr != nil {
			lastClass = provider.ClassConnect
			return false, nil
		}
		chatJSON := full
		if c.Protocol == "anthropic" {
			var cerr error
			chatJSON, cerr = protocol.AnthropicResponseToOpenAI(full, req.Model)
			if cerr != nil {
				lastClass = provider.ClassUnknown
				return false, nil
			}
		}
		converted, cerr := protocol.ChatCompletionToResponses(chatJSON, req.Model, tc)
		if cerr != nil {
			lastClass = provider.ClassUnknown
			return false, nil
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.StatusCode)
		w.Write(converted)
		return true, nil
	})
	switches = out.Switches
	if ferr != nil || out.Committed {
		// 头部等待超时（区别于客户端断开）：计时器取消 ctx 且响应未提交时，客户端
		// 必须收到明确的 504，而不是 200 + 空 body；客户端取消保持静默。
		if ferr != nil && !out.Committed && headerTimedOut.Load() {
			rec.Status, rec.ErrorClass = http.StatusGatewayTimeout, "timeout"
			writeErrOpenAI(w, http.StatusGatewayTimeout, protocol.ErrTypeAPI, "upstream response header timeout")
		}
		return // 客户端已断 / 响应已写：不再写终局错误
	}
	// 耗尽且可区分（同 handleChatCompletions）：全部仅因冷却不可用 → 503；
	// 至少一个目标实际失败过 → 502。
	if !out.Attempted && (out.CoolSkips > 0 || len(decision.Filtered) > 0) && allCooling(decision.Filtered) {
		rec.Status, rec.ErrorClass = 503, "no_candidate"
		writeErrOpenAI(w, 503, protocol.ErrTypeAPI, coolingExhaustedMsg(decision.Filtered))
		return
	}
	rec.Status, rec.ErrorClass = 502, string(lastClass)
	writeErrOpenAI(w, 502, protocol.ErrTypeAPI, "all targets failed")
}

// logStreamFailed 为提交后的流中断（上游截断/流内 error）落一行 WARN。
// 空闲超时另有 logIdleTimeout，调用方须保证二者不重复触发。
func (s *Server) logStreamFailed(requestID string) {
	if s.deps.Log == nil {
		return
	}
	s.deps.Log.Warn("responses stream failed", "request_id", requestID)
}
