package telemetry

import (
	"net/http"
	"strings"
)

var sensitiveHeaders = []string{"Authorization", "X-Api-Key", "Cookie", "Set-Cookie", "Proxy-Authorization"}

// MaskToken 对非空值打码并以 "***" 结尾：首词是 scheme 形态（纯字母数字且不超过 16 个字符，
// 如 Bearer、Basic）时保留 "scheme + 空格 + 余下部分的前 4 个字符"；否则只保留前 4 个字符。
// 两种情况都丢弃其余全部内容（含后续空格之后的任何字符）。
func MaskToken(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 4 {
		return "***"
	}
	if head, rest, ok := strings.Cut(v, " "); ok && rest != "" && isScheme(head) {
		return head + " " + rest[:min(4, len(rest))] + "***"
	}
	return v[:4] + "***"
}

// isScheme 判断 s 是否为 scheme 形态：非空、不超过 16 个字符且全部为字母数字。
func isScheme(s string) bool {
	if s == "" || len(s) > 16 {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// RedactHeaders 返回拷贝，敏感头的值被掩码；输入不被修改。
func RedactHeaders(h http.Header) http.Header {
	out := h.Clone()
	for _, k := range sensitiveHeaders {
		if v := out.Get(k); v != "" {
			out.Set(k, MaskToken(v))
		}
	}
	return out
}
