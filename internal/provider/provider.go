package provider

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// DoRequest 是一次上游聊天请求的完整描述（由 gateway/router 组装）。
type DoRequest struct {
	BaseURL       string
	AllowPrivate  bool
	APIKey        string
	UpstreamModel string
	RawBody       []byte
	Stream        bool
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

// AdapterForProtocol 按渠道协议分派 adapter（spec §5.1）；未知协议报错。
// log 仅 openai adapter 使用（丢弃字段的提醒行），可为 nil。
func AdapterForProtocol(p string, log *slog.Logger) (Adapter, error) {
	switch p {
	case "anthropic":
		return NewAnthropicAdapter(), nil
	case "openai":
		return NewOpenAIAdapter(log), nil
	default:
		return nil, fmt.Errorf("unknown protocol %q", p)
	}
}
