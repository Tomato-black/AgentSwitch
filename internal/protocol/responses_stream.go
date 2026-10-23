package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
)

// responsesSSEEvent 生成一条 Responses SSE 帧（event: 行 + data: 行 + 空行）。
func responsesSSEEvent(event string, payload map[string]any) []byte {
	b, _ := json.Marshal(payload)
	var out bytes.Buffer
	out.WriteString("event: ")
	out.WriteString(event)
	out.WriteString("\ndata: ")
	out.Write(b)
	out.WriteString("\n\n")
	return out.Bytes()
}

// ResponsesFailedEvent 生成流中失败事件。供无法访问转换器状态的调用方使用
// （例如网关空闲超时后在收尾阶段补发失败事件）。
func ResponsesFailedEvent(message string) []byte {
	resp := responsesResponseObject("resp_local", "", "failed", 0, nil, chatUsageToResponsesUsage(nil))
	resp["error"] = map[string]any{"message": message}
	return responsesSSEEvent("response.failed", map[string]any{"type": "response.failed", "response": resp})
}

// responsesTextState 记录流式文本消息项的进度：一旦输出过文本就开启一个
// message 项，收口时补齐 done 事件。
type responsesTextState struct {
	index int
	id    string
	text  strings.Builder
	open  bool
	done  bool
}

// chatToResponsesStream 是 chat.completion.chunk → Responses SSE 的状态机。
// 本任务只覆盖文本生命周期；工具与 reasoning 分支由后续任务填充。
type chatToResponsesStream struct {
	out         bytes.Buffer
	raw         []byte
	started     bool
	finished    bool
	failed      bool
	respID      string
	model       string
	createdAt   int64
	nextIndex   int
	text        responsesTextState
	finish      string
	sawTerminal bool
	usage       *chatUsage
	tc          *ResponseToolContext
}

// ChatStreamToResponsesStream 把 chat.completion.chunk SSE 泵成 Responses SSE。
// 调用方必须关闭返回的 ReadCloser；上游 body 由调用方负责关闭
// （同 AnthropicStreamToOpenAIStream）。
func ChatStreamToResponsesStream(ctx context.Context, r io.Reader, requestedModel string, tc *ResponseToolContext) io.ReadCloser {
	pr, pw := io.Pipe()
	t := &chatToResponsesStream{
		respID: "resp_local", model: requestedModel, tc: tc,
	}
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, rerr := r.Read(buf)
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
			switch {
			case ctx.Err() != nil:
				// 客户端/网关取消：静默收尾，不再写事件。
			case rerr == io.EOF && t.sawTerminal:
				out = t.Finish()
			default:
				t.Fail("upstream stream interrupted")
				out = t.drain()
			}
			if len(out) > 0 {
				_, _ = pw.Write(out)
			}
			pw.Close()
			return
		}
	}()
	return pr
}

func (t *chatToResponsesStream) baseResponse(status string) map[string]any {
	return responsesResponseObject(t.respID, t.model, status, t.createdAt, nil, chatUsageToResponsesUsage(t.usage))
}

// ensureStarted 在首个有效 chunk 到来时补发 created/in_progress 事件。
// 此刻才能拿到上游响应 ID 与创建时间，避免先发占位对象再改 ID。
func (t *chatToResponsesStream) ensureStarted() {
	if t.started {
		return
	}
	t.started = true
	resp := t.baseResponse("in_progress")
	t.out.Write(responsesSSEEvent("response.created", map[string]any{"type": "response.created", "response": resp}))
	t.out.Write(responsesSSEEvent("response.in_progress", map[string]any{"type": "response.in_progress", "response": resp}))
}

// pushText 追加文本增量；首个增量触发 message 项与 content_part 的开启事件。
func (t *chatToResponsesStream) pushText(delta string) {
	t.ensureStarted()
	if !t.text.open {
		t.text.index = t.nextIndex
		t.nextIndex++
		t.text.id = t.respID + "_msg"
		t.text.open = true
		t.out.Write(responsesSSEEvent("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": t.text.index,
			"item": map[string]any{"id": t.text.id, "type": "message", "status": "in_progress",
				"role": "assistant", "content": []any{}},
		}))
		t.out.Write(responsesSSEEvent("response.content_part.added", map[string]any{
			"type": "response.content_part.added", "item_id": t.text.id,
			"output_index": t.text.index, "content_index": 0,
			"part": map[string]any{"type": "output_text", "text": "", "annotations": []any{}},
		}))
	}
	t.text.text.WriteString(delta)
	t.out.Write(responsesSSEEvent("response.output_text.delta", map[string]any{
		"type": "response.output_text.delta", "item_id": t.text.id,
		"output_index": t.text.index, "content_index": 0, "delta": delta,
	}))
}

// closeText 收口文本项并返回其完成态 item；未开启或已收口时返回 nil。
func (t *chatToResponsesStream) closeText() []map[string]any {
	if !t.text.open || t.text.done {
		return nil
	}
	t.text.done = true
	text := t.text.text.String()
	item := map[string]any{
		"id": t.text.id, "type": "message", "status": "completed", "role": "assistant",
		"content": []map[string]any{{"type": "output_text", "text": text, "annotations": []any{}}},
	}
	t.out.Write(responsesSSEEvent("response.output_text.done", map[string]any{
		"type": "response.output_text.done", "item_id": t.text.id,
		"output_index": t.text.index, "content_index": 0, "text": text,
	}))
	t.out.Write(responsesSSEEvent("response.content_part.done", map[string]any{
		"type": "response.content_part.done", "item_id": t.text.id,
		"output_index": t.text.index, "content_index": 0,
		"part": map[string]any{"type": "output_text", "text": text, "annotations": []any{}},
	}))
	t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": t.text.index, "item": item,
	}))
	return []map[string]any{item}
}

// handleChunk 解析单个 chat.completion.chunk 并推进状态。
// 只读取 id/created/usage 用于填充响应信封；上游 model 一律忽略，
// 响应中的 model 始终回显客户端请求的逻辑模型名。
func (t *chatToResponsesStream) handleChunk(data []byte) {
	var c struct {
		ID      string     `json:"id"`
		Created int64      `json:"created"`
		Model   string     `json:"model"`
		Usage   *chatUsage `json:"usage"`
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &c) != nil {
		return
	}
	if c.ID != "" {
		t.respID = responseIDFromChatID(c.ID)
	}
	if c.Usage != nil {
		t.usage = c.Usage
	}
	if c.Created != 0 {
		t.createdAt = c.Created
	}
	t.ensureStarted()
	if len(c.Choices) == 0 {
		return
	}
	if c.Choices[0].Delta.Content != "" {
		t.pushText(c.Choices[0].Delta.Content)
	}
	if c.Choices[0].FinishReason != nil && *c.Choices[0].FinishReason != "" {
		t.finish = *c.Choices[0].FinishReason
		t.sawTerminal = true
	}
}

// handleEvent 处理一个完整 SSE 事件字节（取全部 data: 行，通常一行）。
func (t *chatToResponsesStream) handleEvent(event []byte) {
	for _, line := range strings.Split(string(event), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[len("data:"):])
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			t.sawTerminal = true
			continue
		}
		t.handleChunk([]byte(payload))
	}
}

// Feed 摄入原始字节，返回本次可下发的 SSE 片段。
func (t *chatToResponsesStream) Feed(p []byte) []byte {
	t.raw = append(t.raw, p...)
	for {
		event, consumed, ok := findEvent(t.raw)
		if !ok {
			break
		}
		t.handleEvent(event)
		t.raw = t.raw[consumed:]
	}
	return t.drain()
}

// Finish 正常收口：补齐文本 done 事件并发出 response.completed。
func (t *chatToResponsesStream) Finish() []byte {
	if t.finished {
		return nil
	}
	t.finished = true
	output := t.closeText()
	resp := t.baseResponse("completed")
	resp["output"] = output
	t.out.Write(responsesSSEEvent("response.completed", map[string]any{"type": "response.completed", "response": resp}))
	return t.drain()
}

// Fail 只把失败事件写进内部缓冲，不 drain——既可由 EOF 路径调用后统一 drain，
// 也可由 handleChunk（上游 error chunk）调用，由 Feed 正常取走。
func (t *chatToResponsesStream) Fail(message string) {
	if t.finished {
		return
	}
	t.finished, t.failed = true, true
	output := t.closeText()
	resp := t.baseResponse("failed")
	resp["output"] = output
	resp["error"] = map[string]any{"message": message}
	t.out.Write(responsesSSEEvent("response.failed", map[string]any{"type": "response.failed", "response": resp}))
}

// drain 取走内部缓冲的全部字节。
func (t *chatToResponsesStream) drain() []byte {
	if t.out.Len() == 0 {
		return nil
	}
	out := make([]byte, t.out.Len())
	copy(out, t.out.Bytes())
	t.out.Reset()
	return out
}
