package gateway

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"AgentSwitch/internal/protocol"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/telemetry"
)

// responseHeaderWhitelist：回传给客户端的上游响应头。
var responseHeaderWhitelist = []string{"Content-Type", "Retry-After", "Anthropic-Request-Id"}

func writeErr(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(protocol.ErrorEnvelope(errType, msg))
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

	if !s.authorized(r) {
		rec.Status, rec.ErrorClass = 401, "auth"
		writeErr(w, 401, protocol.ErrTypeAuth, "invalid gateway token")
		return
	}
	lim := s.deps.Limits()
	if lim.MaxConcurrent > 0 && s.sem != nil {
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		default:
			rec.Status, rec.ErrorClass = 503, "busy"
			writeErr(w, 503, protocol.ErrTypeAPI, "gateway busy, retry later")
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, lim.MaxBodyBytes+1))
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErr(w, 400, protocol.ErrTypeInvalidRequest, "read body failed")
		return
	}
	if int64(len(body)) > lim.MaxBodyBytes {
		rec.Status, rec.ErrorClass = 413, protocol.ErrTypeInvalidRequest
		writeErr(w, 413, protocol.ErrTypeInvalidRequest, "request body too large")
		return
	}
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
		rec.Status, rec.ErrorClass = 404, protocol.ErrTypeNotFound
		writeErr(w, 404, protocol.ErrTypeNotFound,
			"unknown model "+cr.Model+"; models: "+strings.Join(s.deps.ListPublicModels(), ", "))
		return
	}
	decision := s.deps.Router.Resolve(cr.Model, cands)
	if len(decision.Ordered) == 0 {
		rec.Status, rec.ErrorClass = 503, "no_candidate"
		reasons := make([]string, 0, len(decision.Filtered))
		for _, f := range decision.Filtered {
			reasons = append(reasons, f.Candidate.ChannelName+"/"+f.Candidate.KeyName+":"+f.Reason)
		}
		writeErr(w, 503, protocol.ErrTypeAPI, "no usable candidate (filtered: "+strings.Join(reasons, "; ")+")")
		return
	}

	ctx := r.Context()
	if lim.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lim.RequestTimeout)
		defer cancel()
	}

	committed := false
	lastClass := provider.ClassUnknown
	for _, c := range decision.Ordered {
		if ctx.Err() != nil { // 客户端已走：不再发任何上游请求（docx §6.3 取消不转移）
			return
		}
		// 发送前二次校验（docx §7；TryAcquire 锁内原子，不写状态）
		if s.deps.Cooldowns != nil {
			if _, ok := s.deps.Cooldowns.TryAcquire(c.KeyID, time.Now()); !ok {
				switches++
				continue
			}
		}
		apiKey, err := s.deps.Cred.Get(c.SecretKey)
		if err != nil {
			// 凭据失效（被吊销/钥匙环删除）= 死密钥：按上游 401 同口径冷却
			// （ClassAuth→1h）再换下一候选——单枚死钥不得瘫痪整个池，也不得
			// 每请求重试。此刻零字节已写客户端，属可转移窗口。
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.KeyID, provider.ClassAuth, 0, time.Now())
			}
			lastClass = "credential"
			switches++
			continue
		}
		adapter, err := s.deps.AdapterFor(c.Protocol)
		if err != nil {
			// 渠道协议配置错，密钥无辜：不冷却，换下一候选。
			lastClass = provider.ClassUnknown
			switches++
			continue
		}
		rewritten, err := protocol.RewriteModel(body, c.UpstreamModel)
		if err != nil {
			rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
			writeErr(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
			return
		}
		res, err := adapter.Do(ctx, provider.DoRequest{
			BaseURL: c.BaseURL, AllowPrivate: c.AllowPrivate, APIKey: string(apiKey),
			UpstreamModel: c.UpstreamModel, RawBody: rewritten, Stream: cr.Stream,
		})
		if err != nil {
			// Do 错误（连接层/3xx/请求转换失败）：冷却后换下一候选——此刻未向
			// 客户端写过任何字节，属可转移窗口。
			lastClass = provider.ClassConnect
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.KeyID, provider.ClassConnect, 0, time.Now())
			}
			switches++
			continue
		}
		if res.StatusCode >= 400 {
			snippet, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
			res.Body.Close()
			class := provider.Classify(res.StatusCode, res.RetryAfterSec, snippet)
			if class == provider.ClassParam {
				// 400 类=客户端自身问题：不冷却、不转移，原样回传（docx §6.3）
				rec.Status, rec.ErrorClass = res.StatusCode, string(class)
				rec.Account, rec.Channel = c.KeyName, c.ChannelName
				writeUpstreamError(w, res.StatusCode, snippet, res.Header)
				return
			}
			lastClass = class
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.KeyID, class, res.RetryAfterSec, time.Now())
			}
			switches++
			continue
		}
		// 2xx：提交点——写响应头之后绝不转移（首字节后不重放，docx §6.3）
		committed = true
		rec.Status = res.StatusCode
		rec.UpstreamModel, rec.Account, rec.Channel = c.UpstreamModel, c.KeyName, c.ChannelName
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
		break
	}
	if !committed {
		rec.Status, rec.ErrorClass = 502, string(lastClass)
		writeErr(w, 502, protocol.ErrTypeAPI, "all candidates failed")
	}
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
