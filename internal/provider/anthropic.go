package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"AgentSwitch/internal/protocol"
)

// anthropicEndpoint 按 Anthropic 生态约定拼接端点：厂商文档的 base_url 是 API 根
// （官方 SDK / Claude Code 的 ANTHROPIC_BASE_URL 语义），请求路径固定为
// /v1/messages。base 路径为空时由 JoinAPIPath 补默认 /v1；已带 /v1 时不重复
// （2026-10-22 小米 /anthropic/messages 404 修复）。
func anthropicEndpoint(baseURL string) (string, error) {
	suffix := "/v1/messages"
	if u, err := url.Parse(baseURL); err == nil {
		p := strings.TrimRight(u.Path, "/")
		if p == "" || strings.HasSuffix(p, "/v1") {
			suffix = "/messages"
		}
	}
	return JoinAPIPath(baseURL, suffix)
}

// ValidateBaseURL 校验上游 base_url：仅 https（回环 http 或显式 allowPrivate 放行），
// 拒绝 userinfo 与非 http(s) scheme。
func ValidateBaseURL(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid base_url: %w", err)
	}
	if u.User != nil {
		return errors.New("base_url must not contain userinfo")
	}
	loopback := isLoopbackHost(u.Hostname())
	switch u.Scheme {
	case "https":
	case "http":
		if !loopback && !allowPrivate {
			return errors.New("http base_url requires loopback or allow_private_upstream")
		}
	default:
		return fmt.Errorf("base_url scheme %q not allowed", u.Scheme)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type AnthropicAdapter struct{ Client *http.Client }

func NewAnthropicAdapter() *AnthropicAdapter {
	return &AnthropicAdapter{Client: &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随：3xx 原样交给分类
		},
	}}
}

func (a *AnthropicAdapter) Do(ctx context.Context, r DoRequest) (ChatResult, error) {
	if err := ValidateBaseURL(r.BaseURL, r.AllowPrivate); err != nil {
		return ChatResult{}, err
	}
	endpoint, err := anthropicEndpoint(r.BaseURL)
	if err != nil {
		return ChatResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(r.RawBody))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", r.APIKey)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	if r.UserAgent != "" {
		req.Header.Set("User-Agent", r.UserAgent)
	}
	if r.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	res, err := sendUpstream(ctx, a.Client, req)
	if err != nil {
		return res, err
	}
	if r.Stream && res.StatusCode >= 200 && res.StatusCode < 300 {
		res.Body = WrapPassthroughStream(ctx, res.Body,
			protocol.AnthropicErrorEvent("upstream stream interrupted"),
			"event: message_stop", "event: error")
	}
	return res, nil
}

// sendUpstream 执行请求并归一化结果：3xx 拒绝为 error（CheckRedirect 返回
// ErrUseLastResponse 后 3xx 以"正常响应"浮出，必须显式拒绝且不跟随）；
// 4xx/5xx 限长读体后作为正常结果返回（分类交给调用方）；2xx 透传 Body。
func sendUpstream(ctx context.Context, client *http.Client, req *http.Request) (ChatResult, error) {
	resp, err := client.Do(req)
	if err != nil {
		return ChatResult{}, fmt.Errorf("connect: %w", err)
	}
	ra := 0
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil {
			ra = n
		}
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, RetryAfterSec: ra,
				Body: io.NopCloser(bytes.NewReader(snippet))},
			fmt.Errorf("redirect denied: upstream status %d", resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, RetryAfterSec: ra,
			Body: io.NopCloser(bytes.NewReader(snippet))}, nil
	}
	return ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body, RetryAfterSec: ra}, nil
}

type ProbeResult struct {
	Stage string `json:"stage"`
	Error string `json:"error,omitempty"`
	OK    bool   `json:"ok"`
}

// Probe 分级探测：dns → connect → auth（发一条最小请求，只报告分级与脱敏错误）。
// protocol 决定探测端点与鉴权头（anthropic→/v1/messages，openai→/v1/chat/completions）。
func Probe(ctx context.Context, protocol, baseURL, apiKey string, allowPrivate bool) ProbeResult {
	if err := ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return ProbeResult{Stage: "validate", Error: err.Error()}
	}
	u, _ := url.Parse(baseURL)
	host := u.Hostname()
	if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil && !isLoopbackHost(host) {
		return ProbeResult{Stage: "dns", Error: fmt.Sprintf("resolve %s failed", host)}
	}
	body := []byte(`{"model":"__probe__","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`)
	var ad Adapter = NewAnthropicAdapter()
	if protocol == "openai" {
		ad = NewOpenAIAdapter(nil)
	}
	res, err := ad.Do(ctx, DoRequest{BaseURL: baseURL, AllowPrivate: allowPrivate, APIKey: apiKey, RawBody: body})
	if err != nil {
		return ProbeResult{Stage: "connect", Error: MaskErr(err)}
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4<<10))
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return ProbeResult{Stage: "auth", Error: fmt.Sprintf("upstream status %d", res.StatusCode)}
	}
	return ProbeResult{Stage: "auth", OK: true}
}

// MaskErr 去掉错误文本中可能出现的密钥片段。
func MaskErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "sk-"); i >= 0 {
		return s[:i] + "sk-***"
	}
	return s
}
