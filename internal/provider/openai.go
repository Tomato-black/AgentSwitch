package provider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"AgentSwitch/internal/protocol"
)

// respLimit 是 openai 非流响应转换前的整体读取上限。
const respLimit = 64 << 20

// OpenAIAdapter 把 Anthropic Messages 请求转换为 ChatCompletions 发送，并把
// 响应/流转换回 Anthropic 形状（spec §5.3）。4xx/5xx 错误体保持上游原样——
// gateway 的 upstreamErrorBody 会兜底包装成 Anthropic envelope。
type OpenAIAdapter struct {
	Client *http.Client
	Log    *slog.Logger
}

func NewOpenAIAdapter(log *slog.Logger) *OpenAIAdapter {
	return &OpenAIAdapter{Client: &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随：3xx 原样交给分类
		},
	}, Log: log}
}

func (a *OpenAIAdapter) Do(ctx context.Context, r DoRequest) (ChatResult, error) {
	if err := ValidateBaseURL(r.BaseURL, r.AllowPrivate); err != nil {
		return ChatResult{}, err
	}
	converted, dropped, err := protocol.AnthropicRequestToOpenAI(r.RawBody)
	if err != nil {
		return ChatResult{}, err
	}
	if len(dropped) > 0 && a.Log != nil {
		a.Log.Info("openai adapter dropped anthropic-only fields", "fields", strings.Join(dropped, ","))
	}
	endpoint := strings.TrimRight(r.BaseURL, "/") + "/v1/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(converted))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.APIKey)
	if r.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	res, err := sendUpstream(ctx, a.Client, req)
	if err != nil || res.StatusCode >= 400 {
		return res, err
	}
	if !r.Stream {
		full, err := io.ReadAll(io.LimitReader(res.Body, respLimit))
		res.Body.Close()
		if err != nil {
			return ChatResult{}, fmt.Errorf("read upstream: %w", err)
		}
		out, err := protocol.OpenAIResponseToAnthropic(full)
		if err != nil {
			return ChatResult{}, fmt.Errorf("convert response: %w", err)
		}
		res.Body = io.NopCloser(bytes.NewReader(out))
		return res, nil
	}
	res.Body = a.streamBody(res.Body)
	return res, nil
}

// streamBody 把 OpenAI chunk 流泵过转换器变成 Anthropic SSE 流（逐事件 flush）。
func (a *OpenAIAdapter) streamBody(src io.Reader) io.ReadCloser {
	pr, pw := io.Pipe()
	t := protocol.NewOpenAIStreamTransformer()
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, rerr := src.Read(buf)
			if n > 0 {
				if out := t.Feed(buf[:n]); len(out) > 0 {
					if _, werr := pw.Write(out); werr != nil {
						return
					}
				}
			}
			if rerr != nil {
				if out := t.Finish(); len(out) > 0 {
					_, _ = pw.Write(out)
				}
				pw.Close()
				return
			}
		}
	}()
	return pr
}
