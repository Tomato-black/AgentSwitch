package gateway

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"AgentSwitch/internal/protocol"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

// handleChatCompletions 是 OpenAI 入站（POST /v1/chat/completions，简报 6B）。
// 与 handleMessages 同构：同一套鉴权/并发/body 检查（gate）、同一 failover 循环、
// 同一 404/502/503 语义；差异仅在（a）按候选 Protocol 互转请求/响应（b）错误
// envelope 为 OpenAI 形状。提交点=向客户端写响应头：其后绝不换候选重放。
func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
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
		writeErrOpenAI(w, status, errType, msg)
		return
	}
	oar, err := protocol.ParseOpenAIChatRequest(body)
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, "invalid body: model and valid JSON required")
		return
	}
	oar.Model = stripOneM(oar.Model)
	rec.Model, rec.Streaming = oar.Model, oar.Stream

	cands, err := s.deps.Candidates(oar.Model)
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErrOpenAI(w, 500, protocol.ErrTypeAPI, "candidates unavailable")
		return
	}
	if len(cands) == 0 {
		// 未登记模型：passthrough 开亦 404（设置项仅预留，同 handleMessages）。
		if s.deps.Passthrough != nil && s.deps.Passthrough() && s.deps.Log != nil {
			s.deps.Log.Debug("passthrough reserved: rejecting unregistered model", "model", oar.Model)
		}
		rec.Status, rec.ErrorClass = 404, protocol.ErrTypeNotFound
		writeErrOpenAI(w, 404, protocol.ErrTypeNotFound,
			"unknown model "+oar.Model+"; models: "+strings.Join(s.deps.ListPublicModels(), ", "))
		return
	}
	decision := s.deps.Router.Resolve(oar.Model, cands)

	lim := s.deps.Limits()
	// 超时语义同 handleMessages：非流式为总时长上限；流式的 RequestTimeout 只
	// 约束等待响应头，2xx 提交点即停表，其后由 copyStream 的空闲 watchdog 接管。
	ctx := r.Context()
	var cancel context.CancelFunc
	var headerTimer *time.Timer
	var headerTimedOut atomic.Bool
	if lim.RequestTimeout > 0 {
		if oar.Stream {
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
			// 死密钥：按上游 401 同口径冷却后换下一候选（同 handleMessages）。
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.ChannelID, provider.ClassAuth, 0, time.Now())
			}
			lastClass = "credential"
			return false, nil
		}
		// 按候选协议准备出站（简报 6B）：
		//   openai   → RewriteModel 后直通（body 已是 chat.completions 形状）；
		//   anthropic → OpenAIRequestToAnthropic 后走 Anthropic adapter，响应回转。
		var rawBody []byte
		var adapter provider.Adapter
		switch c.Protocol {
		case "openai":
			rawBody, err = protocol.RewriteModel(body, c.UpstreamModel)
			if err != nil {
				rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
				writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
				return true, nil // 已写终态响应，不换候选
			}
			adapter = newOpenAIPassthrough()
		case "anthropic":
			rawBody, err = protocol.OpenAIRequestToAnthropic(oar, c.UpstreamModel)
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
			UpstreamModel: c.UpstreamModel, RawBody: rawBody, Stream: oar.Stream,
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
		if oar.Stream && headerTimer != nil {
			headerTimer.Stop()
		}
		switch {
		case c.Protocol == "openai":
			// 直通：响应已是 OpenAI 形状（流式为 chunk SSE），原样转发。
			if oar.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(res.StatusCode)
				onIdle := protocol.OpenAIStreamErrorChunk("upstream stream idle timeout")
				if copyStream(w, res.Body, lim.RequestTimeout, onIdle) {
					rec.ErrorClass = "stream"
					s.logIdleTimeout(rec.ID, lim.RequestTimeout)
				}
			} else {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(res.StatusCode)
				io.Copy(w, res.Body)
			}
			res.Body.Close()
		case oar.Stream:
			// anthropic 流式：SSE 泵过 AnthropicStreamToOpenAIStream。
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(res.StatusCode)
			onIdle := protocol.OpenAIStreamErrorChunk("upstream stream idle timeout")
			if copyStream(w, protocol.AnthropicStreamToOpenAIStream(ctx, res.Body, oar.Model), lim.RequestTimeout, onIdle) {
				rec.ErrorClass = "stream"
				s.logIdleTimeout(rec.ID, lim.RequestTimeout)
			}
			res.Body.Close()
		default:
			// anthropic 非流：整体读出转 chat.completion。转换失败仍可转移——
			// 尚未向客户端写字节；上游 200 坏体不冷却、不计协议无罪。
			full, rerr := io.ReadAll(io.LimitReader(res.Body, 64<<20))
			res.Body.Close()
			if rerr != nil {
				lastClass = provider.ClassConnect
				return false, nil
			}
			converted, cerr := protocol.AnthropicResponseToOpenAI(full, oar.Model)
			if cerr != nil {
				lastClass = provider.ClassUnknown
				return false, nil
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(res.StatusCode)
			w.Write(converted)
		}
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
	if !out.Attempted && (out.CoolSkips > 0 || len(decision.Filtered) > 0) && allCooling(decision.Filtered) {
		rec.Status, rec.ErrorClass = 503, "no_candidate"
		writeErrOpenAI(w, 503, protocol.ErrTypeAPI, coolingExhaustedMsg(decision.Filtered))
		return
	}
	rec.Status, rec.ErrorClass = 502, string(lastClass)
	writeErrOpenAI(w, 502, protocol.ErrTypeAPI, "all targets failed")
}

func newOpenAIPassthrough() *openaiPassthrough {
	return &openaiPassthrough{client: &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随：3xx 原样交给分类（同 provider）
		},
	}}
}

func (a *openaiPassthrough) Do(ctx context.Context, r provider.DoRequest) (provider.ChatResult, error) {
	if err := provider.ValidateBaseURL(r.BaseURL, r.AllowPrivate); err != nil {
		return provider.ChatResult{}, err
	}
	endpoint, err := provider.JoinAPIPath(r.BaseURL, "/chat/completions")
	if err != nil {
		return provider.ChatResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(r.RawBody))
	if err != nil {
		return provider.ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	if r.UserAgent != "" {
		req.Header.Set("User-Agent", r.UserAgent)
	}
	if r.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return provider.ChatResult{}, fmt.Errorf("connect: %w", err)
	}
	ra := 0
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil {
			ra = n
		}
	}
	// 与 provider.sendUpstream 同归一化（该函数未导出，此处并同构）：3xx 拒绝为
	// error；4xx/5xx 限长读体返回给调用方分类；2xx 透传 Body。
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return provider.ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, RetryAfterSec: ra,
				Body: io.NopCloser(bytes.NewReader(snippet))},
			fmt.Errorf("redirect denied: upstream status %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return provider.ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, RetryAfterSec: ra,
			Body: io.NopCloser(bytes.NewReader(snippet))}, nil
	}
	// 2xx 直通流：上游未给出正常终止标记即断流时补 error chunk（不伪造 [DONE]）。
	// 正常终止判据同时接受 [DONE] 与非空 finish_reason——部分 OpenAI 兼容上游
	// 只发 finish_reason 后直接关流（spec 2026-10-22 §3.3 通用规则）。
	// 非流式与 4xx/5xx 响应体保持原样。
	if r.Stream {
		resp.Body = provider.WrapPassthroughStream(ctx, resp.Body,
			protocol.OpenAIStreamErrorChunk("upstream stream interrupted"),
			"[DONE]", `"finish_reason":"`)
	}
	return provider.ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body, RetryAfterSec: ra}, nil
}

type openaiPassthrough struct{ client *http.Client }
