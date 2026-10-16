package provider

import (
	"context"
	"io"
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
