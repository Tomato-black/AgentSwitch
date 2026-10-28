// Package provider 是上游供应商适配层：按 anthropic/openai 协议直连上游并
// 归一化结果，另含测速探针（Probe）、上游错误分类与密钥脱敏助手。
package provider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DoRequest 是一次上游聊天请求的完整描述（由 gateway/router 组装）。
type DoRequest struct {
	BaseURL       string
	AllowPrivate  bool
	APIKey        string
	UpstreamModel string
	RawBody       []byte
	Stream        bool
	// UserAgent 透传客户端原始 UA（Kimi Code 等订阅条款要求；spec
	// 2026-10-23 §6 检查项 2）。空 = 不设置（保持 Go 默认 UA）。
	UserAgent string
}

// ChatResult 是上游响应：非 2xx 时 Body 为已限长读取的错误体（供 Classify 使用）。
type ChatResult struct {
	StatusCode    int
	Header        http.Header
	Body          io.ReadCloser
	RetryAfterSec int
}

// Adapter 抽象一次上游调用；实现负责协议头、路径与安全校验。
type Adapter interface {
	Do(ctx context.Context, r DoRequest) (ChatResult, error)
}

// JoinAPIPath 把 API 后缀（"/chat/completions" 或 "/messages"）拼到渠道 base_url
// 上，路径感知（两适配器与 gateway openaiPassthrough 共用）：
//   - base_url 无路径或仅 "/"：插入 /v1，得官方形态 …/v1/chat/completions；
//   - base_url 路径已以 /v1 或 /v1/ 结尾：直接拼后缀，避免双 /v1；
//   - 其余非平凡路径（智谱 /api/paas/v4、豆包 /api/v3、Google /v1beta/openai）：
//     视作供应商已给完整 API 前缀，只拼后缀，不再插入 /v1。
func JoinAPIPath(baseURL, suffix string) (string, error) {
	if suffix == "" {
		return "", fmt.Errorf("empty api suffix")
	}
	if !strings.HasPrefix(suffix, "/") {
		suffix = "/" + suffix
	}
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("invalid base_url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("invalid base_url %q", baseURL)
	}
	path := strings.TrimRight(u.Path, "/")
	if path == "" {
		path = "/v1"
	}
	u.Path = path + suffix
	return u.String(), nil
}
