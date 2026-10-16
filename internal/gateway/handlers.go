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
	accts, err := s.deps.Accounts()
	if err != nil || len(accts) == 0 || s.deps.Token() == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]any{"status": "degraded",
			"reason": "no usable account or token"})
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
	if _, err := s.deps.Accounts(); err != nil {
		writeErr(w, 500, protocol.ErrTypeAPI, "internal error")
		return
	}
	// 别名来自 Alias 闭包无法枚举——Deps.ListAliases 专门用于枚举。
	aliases := s.deps.ListAliases()
	type model struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		DisplayName string `json:"display_name"`
	}
	data := []model{}
	for _, a := range aliases {
		data = append(data, model{ID: a, Type: "model", DisplayName: a})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	rec := telemetry.RequestRecord{ID: telemetryID(), StartedAt: started}
	defer func() {
		rec.DurationMs = time.Since(started).Milliseconds()
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
	// 安全顺序：必须先 Parse（字面 null body 会死在 "model is required"），
	// 否则 RewriteModel 的 nil-map panic 可达。
	cr, err := protocol.ParseChatRequest(body)
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErr(w, 400, protocol.ErrTypeInvalidRequest, "invalid body: model and valid JSON required")
		return
	}
	rec.Model, rec.Streaming = cr.Model, cr.Stream

	upstream, ok, err := s.deps.Alias(cr.Model)
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErr(w, 500, protocol.ErrTypeAPI, "alias lookup failed")
		return
	}
	if !ok {
		rec.Status, rec.ErrorClass = 404, protocol.ErrTypeNotFound
		writeErr(w, 404, protocol.ErrTypeNotFound,
			"unknown model "+cr.Model+"; aliases: "+strings.Join(s.deps.ListAliases(), ", "))
		return
	}
	rec.UpstreamModel = upstream
	rewritten, err := protocol.RewriteModel(body, upstream)
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErr(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
		return
	}

	accts, err := s.deps.Accounts()
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErr(w, 500, protocol.ErrTypeAPI, "accounts unavailable")
		return
	}
	decision, err := s.deps.Router.Resolve(accts)
	if err != nil {
		rec.Status, rec.ErrorClass = 503, "no_candidate"
		writeErr(w, 503, protocol.ErrTypeAPI, "no usable account: "+err.Error())
		return
	}
	rec.Account = decision.Chosen.ID
	apiKey, err := s.deps.Cred.Get(decision.Chosen.SecretKey)
	if err != nil {
		rec.Status, rec.ErrorClass = 500, "credential"
		writeErr(w, 500, protocol.ErrTypeAPI, "credential unavailable for account "+decision.Chosen.Name)
		return
	}
	baseURL, allowPrivate, err := s.deps.ProviderConfig()
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErr(w, 500, protocol.ErrTypeAPI, "provider config error")
		return
	}

	ctx := r.Context()
	if lim.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lim.RequestTimeout)
		defer cancel()
	}
	res, err := s.deps.Adapter.Do(ctx, provider.DoRequest{
		BaseURL: baseURL, AllowPrivate: allowPrivate, APIKey: string(apiKey),
		UpstreamModel: upstream, RawBody: rewritten, Stream: cr.Stream,
	})
	if err != nil {
		// 含 3xx（Adapter 返回 "redirect denied"）在内的一切 Do 错误：保守 502。
		rec.Status, rec.ErrorClass = 502, string(provider.ClassConnect)
		writeErr(w, 502, protocol.ErrTypeAPI, "upstream connect failed")
		return
	}
	defer res.Body.Close()
	rec.Status = res.StatusCode
	if res.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		rec.ErrorClass = string(provider.Classify(res.StatusCode, res.RetryAfterSec, snippet))
		for _, h := range responseHeaderWhitelist {
			if v := res.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.StatusCode)
		// 上游已为 envelope 形状则原样透传，否则包装保证 {"type":"error"} 形状。
		w.Write(upstreamErrorBody(snippet, res.StatusCode))
		return
	}
	for _, h := range responseHeaderWhitelist {
		if v := res.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	if cr.Stream {
		copyStream(w, res.Body)
	} else {
		io.Copy(w, res.Body)
	}
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
