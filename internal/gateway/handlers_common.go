// handlers_common.go 是各入站共用的编排件：鉴权门（并发/体积限额）、
// 就绪与模型列表端点、流拷贝（空闲超时/回调）、诊断日志与工具函数。
package gateway

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
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

// responseHeaderWhitelist：回传给客户端的上游响应头。
var responseHeaderWhitelist = []string{"Content-Type", "Retry-After", "Anthropic-Request-Id"}

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

func subtleConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// telemetryID 生成 8 字节随机 hex 的请求 ID。
func telemetryID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
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
