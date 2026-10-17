package gateway

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"AgentSwitch/internal/protocol"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

// responseHeaderWhitelist：回传给客户端的上游响应头。
var responseHeaderWhitelist = []string{"Content-Type", "Retry-After", "Anthropic-Request-Id"}

func writeErr(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(protocol.ErrorEnvelope(errType, msg))
}

// writeErrOpenAI 同 writeErr，但错误体为 OpenAI error 形状（chat.completions
// 入站）——错误 envelope 形状随入站协议而异。
func writeErrOpenAI(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(protocol.OpenAIErrorBody(errType, msg))
}

// gate 是 /v1/messages 与 /v1/chat/completions 共用的请求检查（简报 6B「同一套」）：
// 鉴权 → 并发上限 → body 上限并读体。ok=false 时 status/errType/msg 已定，由调用方
// 按入站协议的错误形状写出；release 非 nil 时调用方 defer 释放并发额度。
func (s *Server) gate(r *http.Request, rec *telemetry.RequestRecord) (
	body []byte, release func(), status int, errType, msg string, ok bool) {
	if !s.authorized(r) {
		rec.Status, rec.ErrorClass = 401, "auth"
		return nil, nil, 401, protocol.ErrTypeAuth, "invalid gateway token", false
	}
	lim := s.deps.Limits()
	if lim.MaxConcurrent > 0 && s.sem != nil {
		select {
		case s.sem <- struct{}{}:
			release = func() { <-s.sem }
		default:
			rec.Status, rec.ErrorClass = 503, "busy"
			return nil, nil, 503, protocol.ErrTypeAPI, "gateway busy, retry later", false
		}
	}
	b, err := io.ReadAll(io.LimitReader(r.Body, lim.MaxBodyBytes+1))
	if err != nil {
		if release != nil {
			release()
		}
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		return nil, nil, 400, protocol.ErrTypeInvalidRequest, "read body failed", false
	}
	if int64(len(b)) > lim.MaxBodyBytes {
		if release != nil {
			release()
		}
		rec.Status, rec.ErrorClass = 413, protocol.ErrTypeInvalidRequest
		return nil, nil, 413, protocol.ErrTypeInvalidRequest, "request body too large", false
	}
	return b, release, 0, "", "", true
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !s.deps.HasUsableChannel() || s.deps.Token() == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]any{"status": "degraded",
			"reason": "no usable channel/key or token"})
		return
	}
	w.WriteHeader(200)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, 401, protocol.ErrTypeAuth, "invalid gateway token")
		return
	}
	type model struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		DisplayName string `json:"display_name"`
	}
	data := []model{}
	for _, m := range s.deps.ListPublicModels() {
		data = append(data, model{ID: m, Type: "model", DisplayName: m})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false})
}

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
		writeErr(w, 400, protocol.ErrTypeInvalidRequest, "invalid body: model and valid JSON required")
		return
	}
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
			"unknown model "+cr.Model+"; models: "+strings.Join(s.deps.ListPublicModels(), ", "))
		return
	}
	decision := s.deps.Router.Resolve(cr.Model, cands)

	ctx := r.Context()
	if lim.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lim.RequestTimeout)
		defer cancel()
	}

	lastClass := provider.ClassUnknown
	out, ferr := s.failover(ctx, decision, func(c router.Candidate) (bool, error) {
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
			writeErr(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
			return true, nil // 已写终态响应，不换候选
		}
		res, err := adapter.Do(ctx, provider.DoRequest{
			BaseURL: c.BaseURL, AllowPrivate: c.AllowPrivate, APIKey: string(apiKey),
			UpstreamModel: c.UpstreamModel, RawBody: rewritten, Stream: cr.Stream,
		})
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
		w.WriteHeader(res.StatusCode)
		if cr.Stream {
			copyStream(w, res.Body)
		} else {
			io.Copy(w, res.Body)
		}
		res.Body.Close()
		return true, nil
	})
	switches = out.Switches
	if ferr != nil || out.Committed {
		return // 客户端已断 / 响应已写：不再写终局错误
	}
	// 耗尽且可区分（简报 Task 6）：全部仅因冷却不可用 → 503 all targets cooling；
	// 至少一个目标实际失败过 → 502 all targets failed。
	if !out.Attempted && (out.CoolSkips > 0 || len(decision.Filtered) > 0) && allCooling(decision.Filtered) {
		rec.Status, rec.ErrorClass = 503, "no_candidate"
		writeErr(w, 503, protocol.ErrTypeAPI, coolingExhaustedMsg(decision.Filtered))
		return
	}
	rec.Status, rec.ErrorClass = 502, string(lastClass)
	writeErr(w, 502, protocol.ErrTypeAPI, "all targets failed")
}

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

	ctx := r.Context()
	if lim := s.deps.Limits(); lim.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lim.RequestTimeout)
		defer cancel()
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
		switch {
		case c.Protocol == "openai":
			// 直通：响应已是 OpenAI 形状（流式为 chunk SSE），原样转发。
			if oar.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(res.StatusCode)
				copyStream(w, res.Body)
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
			copyStream(w, protocol.AnthropicStreamToOpenAIStream(res.Body, oar.Model))
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

// openaiPassthrough 是 OpenAI 入站 → OpenAI 上游的直通出站（简报 6B：RewriteModel
// 后直接送 /v1/chat/completions，请求与响应均不做协议转换）。与
// provider.OpenAIAdapter 不同——后者假定 Anthropic 形状 RawBody 并双向转换。
type openaiPassthrough struct{ client *http.Client }

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
	return provider.ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body, RetryAfterSec: ra}, nil
}

// failoverOutcome 报告目标链转移循环的结局（docx §6.3 可解释记录）。
type failoverOutcome struct {
	Committed bool // 已向客户端写出响应（2xx 或不可转移错误）
	Attempted bool // 至少一个目标真正进入 run
	Switches  int  // 切换/跳过次数（telemetry FailoverCount）
	CoolSkips int  // 发送前二次校验被冷却跳过的次数
}

// failover 是 Anthropic/OpenAI 入站共用的目标链转移循环骨架（6B 复用）：
// 发送前二次校验冷却；仅可转移失败才换下一目标；提交后绝不转移；客户端
// 取消即停且零状态副作用。run 对单个目标出站并回报：
//
//	(true,  _)    committed——响应已写客户端，停止
//	(false, nil)  可转移失败（run 内已分类/冷却），换下一目标
//	(false, err)  中止——停止并上抛 err（如 context.Canceled），不写 502/503
//
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

// writeUpstreamError 透传白名单头 + 原样/包装的错误体（原 handleMessages 内
// 内联逻辑提取；upstreamErrorBody 语义不变）。
func writeUpstreamError(w http.ResponseWriter, status int, snippet []byte, header http.Header) {
	for _, h := range responseHeaderWhitelist {
		if v := header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(upstreamErrorBody(snippet, status))
}

// writeUpstreamErrorOpenAI 同 writeUpstreamError，但错误体为 OpenAI error 形状
// （chat.completions 入站）。
func writeUpstreamErrorOpenAI(w http.ResponseWriter, status int, snippet []byte, header http.Header) {
	for _, h := range responseHeaderWhitelist {
		if v := header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(upstreamErrorBodyOpenAI(snippet, status))
}

// upstreamErrorBodyOpenAI 保证回传给 OpenAI 客户端的错误体是 OpenAI error 形状：
// 已是 OpenAI 形状（{"error":{...}} 且非 Anthropic envelope）原样透传；Anthropic
// envelope（anthropic 渠道上游）提取 error.type/message 转换后回传；其余尽力提
// 取 message 包装一层。状态码始终原样透传。
func upstreamErrorBodyOpenAI(snippet []byte, status int) []byte {
	var probe struct {
		Type  string          `json:"type"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(snippet, &probe); err == nil && len(probe.Error) > 0 {
		if probe.Type == "error" {
			// Anthropic envelope（含嵌套 error.type/message）→ 转 OpenAI 形状。
			var eo struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if json.Unmarshal(snippet, &eo) == nil && eo.Error.Message != "" {
				et := eo.Error.Type
				if et == "" {
					et = upstreamErrType(status)
				}
				return protocol.OpenAIErrorBody(et, eo.Error.Message)
			}
		}
		return snippet // 已是 OpenAI error 形状
	}
	var eo struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	msg := ""
	if err := json.Unmarshal(snippet, &eo); err == nil {
		msg = eo.Error.Message
		if msg == "" {
			msg = eo.Message
		}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return protocol.OpenAIErrorBody(upstreamErrType(status), msg)
}

// copyStream 逐块转发并 flush；客户端断开由上游 ctx 取消收尾。
func copyStream(w http.ResponseWriter, src io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// upstreamErrorBody 保证回传给客户端的上游错误体是 Anthropic envelope 形状：
// 已是 {"type":"error",...} 时原样透传（保留上游 error.type/message），
// 否则尽力提取 message 后包装一层；状态码本身始终原样透传。
func upstreamErrorBody(snippet []byte, status int) []byte {
	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(snippet, &probe); err == nil && probe.Type == "error" {
		return snippet
	}
	var eo struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	msg := ""
	if err := json.Unmarshal(snippet, &eo); err == nil {
		msg = eo.Error.Message
		if msg == "" {
			msg = eo.Message
		}
	}
	if msg == "" {
		msg = http.StatusText(status)
	}
	return protocol.ErrorEnvelope(upstreamErrType(status), msg)
}

// upstreamErrType 把上游状态码映射为 Anthropic envelope 的 error.type。
func upstreamErrType(status int) string {
	switch {
	case status == http.StatusBadRequest:
		return protocol.ErrTypeInvalidRequest
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return protocol.ErrTypeAuth
	case status == http.StatusNotFound:
		return protocol.ErrTypeNotFound
	case status == http.StatusTooManyRequests:
		return "rate_limit_error"
	default:
		return protocol.ErrTypeAPI
	}
}

func subtleConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// telemetryID 生成 8 字节随机 hex 的请求 ID。
func telemetryID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}
