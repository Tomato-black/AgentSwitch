package provider

import "strings"

type ErrorClass string

const (
	ClassQuota     ErrorClass = "quota_exhausted"
	ClassRateLimit ErrorClass = "rate_limited"
	ClassAuth      ErrorClass = "auth"
	ClassServer    ErrorClass = "server"
	ClassParam     ErrorClass = "param"
	ClassEndpoint  ErrorClass = "endpoint"
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
	case status == 404 || status == 405:
		// 路径不存在/方法不对 = 渠道 base_url 或协议配错（不是请求参数问题）：
		// 允许冷却并转移到下一目标（2026-10-22 小米 /anthropic/messages 404 决策）。
		return ClassEndpoint
	case status >= 500:
		return ClassServer
	case status >= 400:
		return ClassParam
	default:
		return ClassUnknown
	}
}
