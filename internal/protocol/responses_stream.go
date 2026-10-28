package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync/atomic"
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

// chatToResponsesStream 是 chat.completion.chunk → Responses SSE 的状态机。
// 覆盖 reasoning、文本与工具调用的生命周期。
type chatToResponsesStream struct {
	out         bytes.Buffer
	raw         []byte
	started     bool
	finished    bool
	failed      atomic.Bool // pump goroutine 写，网关读（Failed()）
	respID      string
	model       string
	createdAt   int64
	nextIndex   int
	reasoning   responsesReasoningState
	text        responsesTextState
	tools       map[int]*responsesToolState
	toolOrder   []int
	finish      string
	sawTerminal bool
	doneSeen    bool // 已收到 data: [DONE]，可立即收口而不等连接关闭
	usage       *chatUsage
	tc          *ResponseToolContext
}

// ResponsesStream 包装转换器的读端，并把转换器内部的失败状态暴露给调用方
// （网关据此区分「正常结束」与「提交后流中断」，补齐遥测与告警）。
type ResponsesStream struct {
	io.ReadCloser
	state *chatToResponsesStream
}

// Failed 报告流是否以失败（response.failed）收口。可在读端 EOF/Close 后安全调用。
func (s *ResponsesStream) Failed() bool { return s.state.failed.Load() }

// ChatStreamToResponsesStream 把 chat.completion.chunk SSE 泵成 Responses SSE。
// 调用方必须关闭返回的 ReadCloser；上游 body 由调用方负责关闭
// （同 AnthropicStreamToOpenAIStream）。
func ChatStreamToResponsesStream(ctx context.Context, r io.Reader, requestedModel string, tc *ResponseToolContext) *ResponsesStream {
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
	return &ResponsesStream{ReadCloser: pr, state: t}
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

// handleChunk 解析单个 chat.completion.chunk 并推进状态。
// 只读取 id/created/usage 用于填充响应信封；上游 model 一律忽略，
// 响应中的 model 始终回显客户端请求的逻辑模型名。
func (t *chatToResponsesStream) handleChunk(data []byte) {
	if t.finished {
		return
	}
	// 上游在流中回 error 对象：立即失败收口并把错误信息透给客户端，
	// 绝不再把后续 [DONE] 当成正常完成。
	var probe struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.Error != nil && probe.Error.Message != "" {
		t.Fail(probe.Error.Message)
		return
	}
	var c struct {
		ID      string     `json:"id"`
		Created int64      `json:"created"`
		Model   string     `json:"model"`
		Usage   *chatUsage `json:"usage"`
		Choices []struct {
			Delta struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				Reasoning        string `json:"reasoning"`
				ToolCalls        []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
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
	d := c.Choices[0].Delta
	if r := d.ReasoningContent; r != "" {
		t.pushReasoning(r)
	} else if r := d.Reasoning; r != "" {
		t.pushReasoning(r)
	}
	if d.Content != "" {
		// reasoning 项在正文开始前收口，两段 item 各自独立。
		t.closeReasoning()
		t.pushText(d.Content)
	}
	for _, call := range d.ToolCalls {
		t.pushToolDelta(call.Index, call.ID, call.Function.Name, call.Function.Arguments)
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
			t.doneSeen = true
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
	out := t.drain()
	// [DONE] 即协议终止：立即收口，避免上游保持连接时空闲看门狗把正常完成
	// 误判为 response.failed。EOF 路径仍作为无 [DONE] 的兜底。
	if t.doneSeen && !t.finished {
		out = append(out, t.Finish()...)
	}
	return out
}

// Finish 正常收口：补齐文本 done 事件后按 finish_reason 发出终态事件。
// finish_reason=length 表示上游因输出上限截断，必须收口为 response.incomplete
// （与非流式 ChatCompletionToResponses 一致），绝不伪装成 completed——否则客户端
// 会把半截工具参数当成正常结果执行。其余 finish_reason 维持 completed。
func (t *chatToResponsesStream) Finish() []byte {
	if t.finished {
		return nil
	}
	t.finished = true
	t.ensureStarted()
	items := t.closeText()
	items = append(items, t.closeReasoning()...)
	items = append(items, t.closeTools()...)
	status, event := "completed", "response.completed"
	if t.finish == "length" {
		status, event = "incomplete", "response.incomplete"
	}
	resp := t.baseResponse(status)
	resp["output"] = sortedOutputItems(items)
	if status == "incomplete" {
		resp["incomplete_details"] = map[string]any{"reason": "max_output_tokens"}
	}
	t.out.Write(responsesSSEEvent(event, map[string]any{"type": event, "response": resp}))
	return t.drain()
}

// Fail 只把失败事件写进内部缓冲，不 drain——既可由 EOF 路径调用后统一 drain，
// 也可由 handleChunk（上游 error chunk）调用，由 Feed 正常取走。
func (t *chatToResponsesStream) Fail(message string) {
	if t.finished {
		return
	}
	t.finished = true
	t.failed.Store(true)
	t.ensureStarted()
	items := t.closeText()
	// reasoning 内容已完整到达，失败收尾时正常收口并保留。
	items = append(items, t.closeReasoning()...)
	// 失败收尾只带已收口的工具 item；仍在流式中的工具下发会破坏客户端状态机。
	items = append(items, t.completedToolItems()...)
	resp := t.baseResponse("failed")
	resp["output"] = sortedOutputItems(items)
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
