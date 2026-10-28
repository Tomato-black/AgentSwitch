// handlers_errors.go 是错误响应构造：三协议的错误体（Anthropic/OpenAI/
// Responses 风格）、上游错误透传与诊断日志截断（不回显完整请求体）。
package gateway

import (
	"bytes"
	"encoding/json"
	"net/http"
	"unicode/utf8"

	"AgentSwitch/internal/protocol"
)

// upstreamErrorLogMaxBytes 限制诊断日志里上游错误体的长度。400 类错误的真实原因
// 只在响应体里（param 分类不足以定位），但错误体可能回显请求内容——按 docx §8/§10
// 只记截断片段，绝不记完整请求体。
const upstreamErrorLogMaxBytes = 512

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
