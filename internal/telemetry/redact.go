package telemetry

import (
	"net/http"
	"strings"
)

var sensitiveHeaders = []string{"Authorization", "X-Api-Key", "Cookie", "Set-Cookie", "Proxy-Authorization"}

// MaskToken 保留非空前缀的前 4 个字符并追加 "***"，丢弃其余内容。
func MaskToken(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 4 {
		return "***"
	}
	head := v
	if i := strings.IndexByte(v, ' '); i > 0 && i+1 < len(v) {
		head = v[:i+1] + v[i+1:min(i+5, len(v))]
	} else {
		head = v[:4]
	}
	return head + "***"
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
