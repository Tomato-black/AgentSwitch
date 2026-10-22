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
	endpoint, err := JoinAPIPath(r.BaseURL, "/chat/completions")
	if err != nil {
		return ChatResult{}, err
	}
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
	res.Body = a.streamBody(ctx, res.Body)
	return res, nil
}

// streamBody 把 OpenAI chunk 流泵过转换器变成 Anthropic SSE 流（逐事件 flush）。
// src 由泵 goroutine 关闭：EOF/写失败/读错所有路径都经 defer 收口。
// 收尾区分三种情况：ctx 取消静默关闭；EOF 且见过终止标记才 Finish；
// 其余（EOF 无终止标记、非 EOF 读错）一律 Abort 以 error 事件收口，绝不伪装正常结束。
func (a *OpenAIAdapter) streamBody(ctx context.Context, src io.ReadCloser) io.ReadCloser {
	pr, pw := io.Pipe()
	t := protocol.NewOpenAIStreamTransformer()
	go func() {
		defer src.Close()
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
			if rerr == nil {
				continue
			}
			var out []byte
			canceled := ctx.Err() != nil
			switch {
			case canceled:
				// 客户端/网关取消：静默收尾，不记录任何告警
			case rerr == io.EOF && t.SawTerminal():
				out = t.Finish()
			default:
				if a.Log != nil {
					a.Log.Warn("upstream stream interrupted", "error", rerr.Error())
				}
				out = t.Abort("upstream stream interrupted")
			}
			if len(out) > 0 {
				_, _ = pw.Write(out)
			}
			if a.Log != nil && !canceled && t.DroppedTools() > 0 {
				a.Log.Warn("dropped incomplete tool call", "count", t.DroppedTools())
			}
			pw.Close()
			return
		}
	}()
	return pr
}
