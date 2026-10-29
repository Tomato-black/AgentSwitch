package protocol

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
)

// StreamUsageTracker 从客户端可见的 SSE 数据行中提取 usage。协议转换器与
// 直通流都会把 usage 写在 data: JSON 里（Anthropic message_start/message_delta、
// OpenAI 尾块、Responses completed），因此统一在写入侧扫描即可覆盖四路流式。
type StreamUsageTracker struct {
	mu     sync.Mutex
	buf    []byte
	inTok  int
	outTok int
}

type streamUsageFields struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type streamUsageEnvelope struct {
	Usage   *streamUsageFields `json:"usage"`
	Message *struct {
		Usage *streamUsageFields `json:"usage"`
	} `json:"message"`
}

// Write 累积 SSE 数据并扫描完整行；返回写入长度（不改变数据）。
func (t *StreamUsageTracker) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	for {
		idx := bytes.IndexByte(t.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(t.buf[:idx])
		t.buf = t.buf[idx+1:]
		t.scanLine(line)
	}
	// 防止无换行长行无限增长；SSE 行正常远小于此。
	if len(t.buf) > 64<<10 {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-(64<<10):]...)
	}
	return len(p), nil
}

// Flush 扫描残余缓冲（流结束时最后一行可能没有换行）。
func (t *StreamUsageTracker) Flush() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.buf) > 0 {
		t.scanLine(string(t.buf))
		t.buf = nil
	}
}

// Usage 返回已观察到的最大输入/输出 token（0 表示未出现/未知）。
func (t *StreamUsageTracker) Usage() (in, out int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.inTok, t.outTok
}

func (t *StreamUsageTracker) scanLine(line string) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "data:") {
		return
	}
	data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if data == "" || data == "[DONE]" {
		return
	}
	var env streamUsageEnvelope
	if json.Unmarshal([]byte(data), &env) != nil {
		return
	}
	t.apply(env.Usage)
	if env.Message != nil {
		t.apply(env.Message.Usage)
	}
}

func (t *StreamUsageTracker) apply(u *streamUsageFields) {
	if u == nil {
		return
	}
	in, out := u.InputTokens, u.OutputTokens
	if u.PromptTokens > in {
		in = u.PromptTokens
	}
	if u.CompletionTokens > out {
		out = u.CompletionTokens
	}
	if in > t.inTok {
		t.inTok = in
	}
	if out > t.outTok {
		t.outTok = out
	}
}

// EnableChatStreamUsage 给 OpenAI Chat 上游请求加 stream_options.include_usage，
// 让上游在流末尾返回 usage 尾块；解析失败原样返回。
func EnableChatStreamUsage(body []byte) []byte {
	var m map[string]any
	if json.Unmarshal(body, &m) != nil || m == nil {
		return body
	}
	opts, _ := m["stream_options"].(map[string]any)
	if opts == nil {
		opts = map[string]any{}
		m["stream_options"] = opts
	}
	opts["include_usage"] = true
	b, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return b
}
