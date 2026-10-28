package gateway

import (
	"AgentSwitch/internal/protocol"
	"AgentSwitch/internal/provider"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

// upstreamErrorLogMaxBytes 限制诊断日志里上游错误体的长度。400 类错误的真实原因
// 只在响应体里（param 分类不足以定位），但错误体可能回显请求内容——按 docx §8/§10
// 只记截断片段，绝不记完整请求体。
const upstreamErrorLogMaxBytes = 512

// failoverOutcome 报告目标链转移循环的结局（docx §6.3 可解释记录）。
type failoverOutcome struct {
	Committed bool // 已向客户端写出响应（2xx 或不可转移错误）
	Attempted bool // 至少一个目标真正进入 run
	Switches  int  // 切换/跳过次数（telemetry FailoverCount）
	CoolSkips int  // 发送前二次校验被冷却跳过的次数
}

// responseHeaderWhitelist：回传给客户端的上游响应头。
var responseHeaderWhitelist = []string{"Content-Type", "Retry-After", "Anthropic-Request-Id"}

// stripOneM 剥离模型名尾部 "[1M]"（大小写不敏感，Claude Code 1M 上下文声明
// 字面量，spec 2026-10-19 §3.3）：接入配置角色行可带该标记，网关路由匹配前
// 剥离，使 my-code[1M] 命中逻辑模型 my-code。
func stripOneM(model string) string {
	if len(model) >= 4 && strings.EqualFold(model[len(model)-4:], "[1M]") {
		return model[:len(model)-4]
	}
	return model
}

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

// truncateForLog 截断错误体到 upstreamErrorLogMaxBytes 并回退到合法 UTF-8 边界。
func truncateForLog(b []byte) string {
	b = bytes.TrimSpace(b)
	if len(b) > upstreamErrorLogMaxBytes {
		b = b[:upstreamErrorLogMaxBytes]
	}
	for len(b) > 0 && !utf8.Valid(b) {
		b = b[:len(b)-1]
	}
	return string(b)
}

// copyStream 逐块转发并 flush；idle>0 时监控客户端可见输出的空闲间隔，
// 超时关闭上游并写出 onIdle。返回是否因空闲超时结束。src 由本函数拥有并在
// 所有退出路径关闭：客户端写失败时必须关闭转换器 pipe 的读端，否则其泵
// goroutine 会永久阻塞在写端（重复 Close 安全）。
func copyStream(w http.ResponseWriter, src io.ReadCloser, idle time.Duration, onIdle []byte) bool {
	defer src.Close()
	flusher, _ := w.(http.Flusher)
	var timedOut atomic.Bool
	var timer *time.Timer
	if idle > 0 {
		timer = time.AfterFunc(idle, func() {
			timedOut.Store(true)
			src.Close()
		})
		defer timer.Stop()
	}
	// tail 保留最后 4 个字节，用于判断已转发内容是否停在 SSE 事件边界；
	// 中途截断时先补 \n\n，避免错误事件与残缺事件粘连成非法 SSE。
	var tail []byte
	atEventBoundary := func() bool {
		s := string(tail)
		return strings.HasSuffix(s, "\n\n") || strings.HasSuffix(s, "\r\n\r\n")
	}
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if timer != nil && !timedOut.Load() {
				timer.Reset(idle)
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return timedOut.Load()
			}
			if len(buf[:n]) >= 4 {
				tail = append(tail[:0], buf[n-4:n]...)
			} else {
				tail = append(tail, buf[:n]...)
				if len(tail) > 4 {
					tail = tail[len(tail)-4:]
				}
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			// 单次读取超时状态：写入 onIdle 与返回值必须基于同一快照，
			// 避免两次 Load 之间 watchdog 翻转造成「写了事件却报未超时」。
			to := timedOut.Load()
			if to && len(onIdle) > 0 {
				if !atEventBoundary() {
					w.Write([]byte("\n\n"))
				}
				w.Write(onIdle)
				if flusher != nil {
					flusher.Flush()
				}
			}
			return to
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

// logIdleTimeout 记录客户端可见输出空闲超时（spec §3.7：WARN，含 request_id
// 与 idle_ms；HTTP 状态已提交 200，无法回改，故以 error_class=stream 遥测）。
func (s *Server) logIdleTimeout(requestID string, idle time.Duration) {
	if s.deps.Log == nil {
		return
	}
	s.deps.Log.Warn("stream idle timeout", "request_id", requestID, "idle_ms", idle.Milliseconds())
}

// logUpstreamError 为每次被上游拒绝的尝试落一行 "upstream error" 诊断日志。
func (s *Server) logUpstreamError(requestID string, c router.Candidate, status int,
	class provider.ErrorClass, snippet []byte) {
	if s.deps.Log == nil {
		return
	}
	s.deps.Log.Info("upstream error", "request_id", requestID,
		"upstream_model", c.UpstreamModel, "channel", c.ChannelName,
		"status", status, "error_class", string(class),
		"upstream_body", truncateForLog(snippet))
}
