package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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

// responsesToolState 记录一个流式工具调用的进度。
// kind/itemType/idPrefix 的零值即 function / function_call / fc_；custom 与
// tool_search 的 kind 解析与收口分支由后续任务填充。
type responsesToolState struct {
	index     int
	callID    string
	name      string
	args      strings.Builder
	open      bool
	done      bool
	kind      responseToolKind
	itemType  string
	idPrefix  string
	completed map[string]any
}

// chatToResponsesStream 是 chat.completion.chunk → Responses SSE 的状态机。
// 本任务覆盖文本与 function 工具生命周期；reasoning 分支由后续任务填充。
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
	tools       map[int]*responsesToolState
	toolOrder   []int
	finish      string
	sawTerminal bool
	doneSeen    bool // 已收到 data: [DONE]，可立即收口而不等连接关闭
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

// pushToolDelta 累积一个工具调用分片。与 cc-switch 一致：必须等 call_id 与 name
// 都到齐才发 output_item.added（部分上游把 name 与 id 分在不同分片）；此前到达的
// 参数先缓冲，added 时一次性补发。
func (t *chatToResponsesStream) pushToolDelta(index int, id, name, args string) {
	t.ensureStarted()
	if t.tools == nil {
		t.tools = map[int]*responsesToolState{}
	}
	st := t.tools[index]
	if st == nil {
		st = &responsesToolState{index: t.nextIndex, idPrefix: "fc_", itemType: "function_call"}
		t.nextIndex++
		t.tools[index] = st
		t.toolOrder = append(t.toolOrder, index)
	}
	if id != "" {
		st.callID = id
	}
	if name != "" {
		st.name = name
	}
	if args != "" {
		st.args.WriteString(args)
	}

	if !st.open && st.callID != "" && st.name != "" {
		t.openTool(st)
		return
	}
	if st.open && args != "" && st.kind == responseToolFunction {
		t.out.Write(responsesSSEEvent("response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": st.idPrefix + st.callID,
			"output_index": st.index, "delta": args,
		}))
	}
}

// openTool 发出 output_item.added；参数若在 call_id/name 到齐前已缓冲，此处一次性补发。
func (t *chatToResponsesStream) openTool(st *responsesToolState) {
	st.open = true
	item := map[string]any{
		"id": st.idPrefix + st.callID, "type": st.itemType, "status": "in_progress",
		"call_id": st.callID,
	}
	if st.kind == responseToolSearch {
		item["execution"] = "client"
		item["arguments"] = map[string]any{}
	} else {
		item["name"] = st.name
		if st.kind == responseToolCustom {
			item["input"] = ""
		} else {
			item["arguments"] = ""
		}
	}
	t.out.Write(responsesSSEEvent("response.output_item.added", map[string]any{
		"type": "response.output_item.added", "output_index": st.index, "item": item,
	}))
	if st.args.Len() > 0 && st.kind == responseToolFunction {
		t.out.Write(responsesSSEEvent("response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": st.idPrefix + st.callID,
			"output_index": st.index, "delta": st.args.String(),
		}))
	}
}

// closeTools 依次收口工具调用并返回完成态 item。
// 未拿到工具名的调用无法还原语义，直接丢弃；id 缺失时合成非空 call_id，
// 并补发 added，保证客户端看到的事件序列完整（不会出现只有 done 的孤儿 item）。
func (t *chatToResponsesStream) closeTools() []map[string]any {
	var output []map[string]any
	for _, idx := range t.toolOrder {
		st := t.tools[idx]
		if st == nil || st.done || st.name == "" {
			continue
		}
		st.done = true
		callID := st.callID
		if callID == "" {
			callID = fmt.Sprintf("call_%d", st.index)
			st.callID = callID
		}
		if !st.open {
			t.openTool(st)
		}
		// 工具上下文缺失（防御性分支）时按普通 function 透传，避免流 goroutine panic。
		spec := responseToolSpec{Kind: responseToolFunction, Name: st.name}
		if t.tc != nil {
			if s, ok := t.tc.Spec(st.name); ok {
				spec = s
			}
		}
		switch st.kind {
		default:
			// function（含未登记工具的透传）收口；custom/tool_search 分支由后续任务补齐。
			args := st.args.String()
			if args == "" {
				args = "{}"
			}
			item := map[string]any{
				"id": st.idPrefix + callID, "type": st.itemType, "status": "completed",
				"call_id": callID, "name": spec.Name, "arguments": args,
			}
			if spec.Namespace != "" {
				item["namespace"] = spec.Namespace
			}
			t.out.Write(responsesSSEEvent("response.function_call_arguments.done", map[string]any{
				"type": "response.function_call_arguments.done", "item_id": st.idPrefix + callID,
				"output_index": st.index, "arguments": args,
			}))
			t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done", "output_index": st.index, "item": item,
			}))
			st.completed = item
			output = append(output, item)
		}
	}
	return output
}

// completedToolItems 返回已收口的工具 item；未完成的不下发（用于失败收尾）。
func (t *chatToResponsesStream) completedToolItems() []map[string]any {
	var output []map[string]any
	for _, idx := range t.toolOrder {
		if st := t.tools[idx]; st != nil && st.done && st.completed != nil {
			output = append(output, st.completed)
		}
	}
	return output
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
				Content   string `json:"content"`
				ToolCalls []struct {
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
	if c.Choices[0].Delta.Content != "" {
		t.pushText(c.Choices[0].Delta.Content)
	}
	for _, call := range c.Choices[0].Delta.ToolCalls {
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

// Finish 正常收口：补齐文本 done 事件并发出 response.completed。
func (t *chatToResponsesStream) Finish() []byte {
	if t.finished {
		return nil
	}
	t.finished = true
	t.ensureStarted()
	output := t.closeText()
	output = append(output, t.closeTools()...)
	if output == nil {
		output = []map[string]any{}
	}
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
	t.ensureStarted()
	output := t.closeText()
	// 失败收尾只带已收口的工具 item；仍在流式中的工具下发会破坏客户端状态机。
	output = append(output, t.completedToolItems()...)
	if output == nil {
		output = []map[string]any{}
	}
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
