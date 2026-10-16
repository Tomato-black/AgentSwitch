package provider

import "strings"

type ErrorClass string

const (
	ClassQuota     ErrorClass = "quota_exhausted"
	ClassRateLimit ErrorClass = "rate_limited"
	ClassAuth      ErrorClass = "auth"
	ClassServer    ErrorClass = "server"
	ClassParam     ErrorClass = "param"
	ClassConnect   ErrorClass = "connect"
	ClassUnknown   ErrorClass = "unknown"
)

// Classify 保守分类：只有状态码与响应体标记同时支持时才判额度耗尽。
// retryAfterSec 保留供调用方读取 Retry-After 后使用，分类本身不依赖。
func Classify(status, retryAfterSec int, body []byte) ErrorClass {
	_ = retryAfterSec
	lower := strings.ToLower(string(body))
	quotaMarked := strings.Contains(lower, "quota") || strings.Contains(lower, "insufficient credit")
	switch {
	case status == 429:
		if quotaMarked {
			return ClassQuota
		}
		return ClassRateLimit
	case status == 401 || status == 403:
		if quotaMarked {
			return ClassQuota
		}
		return ClassAuth
	case status >= 500:
		return ClassServer
	case status >= 400:
		return ClassParam
	default:
		return ClassUnknown
	}
}
