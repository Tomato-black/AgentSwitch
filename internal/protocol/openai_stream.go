// 本文件是 OpenAI 上游方向的流式响应侧：OpenAI chat.completion.chunk SSE →
// Anthropic Messages SSE 的有状态转换器（工具参数聚合与终止事件映射）。
package protocol

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// ---------- 流式转换：OpenAI chunk SSE → Anthropic SSE ----------

type oaixChunk struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Delta struct {
			Role      string `json:"role"`
			Content   string `json:"content"`
			ToolCalls []struct {
				Index int    `json:"index"`
				ID    string `json:"id"`
				// 标准形态把 name/arguments 嵌在 function 下；顶层同名键只作
				// 非标准扁平形态的兼容（2026-10-22 mimo 空名字排查）。
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
				Function  struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type toolAcc struct {
	id, name string
	args     strings.Builder
}

// OpenAIStreamTransformer 把 OpenAI chunk 流逐步转换为 Anthropic SSE 字节
// （spec §5.3.3）。text 增量即时出 content_block_delta；tool_call 按上游
// index 累积、收口时一次性发完整 tool_use 块（input 为解析后的对象）。
// Feed 接受任意切分的上游字节（内部缓冲到完整 SSE 事件），逐事件 flush——
// 每字节恰好转发一次；Finish 收口（截断流按 end_turn）。
// 非并发安全：单一 goroutine 驱动。
type OpenAIStreamTransformer struct {
	raw          []byte // 不完整事件的字节缓冲
	out          bytes.Buffer
	started      bool
	finished     bool
	msgID        string
	model        string
	inTok        int
	outTok       int
	blockIdx     int              // 下一个 content block 的 index
	textOpen     bool             // 当前有打开的 text 块
	tools        map[int]*toolAcc // 按上游 index 累积中的 tool_call
	sawTerminal  bool             // 已收到 finish_reason 或 [DONE]
	droppedTools int              // 参数 JSON 不完整而丢弃的工具块数
}

func NewOpenAIStreamTransformer() *OpenAIStreamTransformer {
	return &OpenAIStreamTransformer{tools: map[int]*toolAcc{}}
}

// SawTerminal 报告上游是否给出正常终止标记（finish_reason 或 [DONE]）。
func (t *OpenAIStreamTransformer) SawTerminal() bool { return t.sawTerminal }

// DroppedTools 报告因参数 JSON 不完整而丢弃的工具块数。
func (t *OpenAIStreamTransformer) DroppedTools() int { return t.droppedTools }

func (t *OpenAIStreamTransformer) emit(event string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	t.out.WriteString("event: " + event + "\ndata: ")
	t.out.Write(b)
	t.out.WriteString("\n\n")
}

// startMessage 发 message_start（首个上游 chunk 触发；截断流由 Finish 兜底）。
func (t *OpenAIStreamTransformer) startMessage() {
	if t.started {
		return
	}
	t.started = true
	if t.msgID == "" {
		t.msgID = "msg_openai"
	}
	t.emit("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": t.msgID, "type": "message", "role": "assistant", "model": t.model,
			"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": t.inTok, "output_tokens": 0},
		},
	})
}

func (t *OpenAIStreamTransformer) closeText() {
	if !t.textOpen {
		return
	}
	t.textOpen = false
	t.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": t.blockIdx - 1})
}

// emitTools 收口一次性发累积中的 tool_use 块（spec §5.3.3：按 index 累积、
// 收口整块发出），返回实际发出的块数。按上游 index 升序，Anthropic 块 index
// 用 t.blockIdx 单调分配；参数 JSON 解析失败的块整块丢弃并计入 DroppedTools。
func (t *OpenAIStreamTransformer) emitTools() int {
	if len(t.tools) == 0 {
		return 0
	}
	idxs := make([]int, 0, len(t.tools))
	for i := range t.tools {
		idxs = append(idxs, i)
	}
	sort.Ints(idxs)
	emitted := 0
	for _, i := range idxs {
		acc := t.tools[i]
		args := acc.args.String()
		if args == "" {
			// 空 arguments 是「无参工具」的合法表示，按 {} 下发。
			args = "{}"
		}
		// 参数 JSON 不完整的块（上游 bug 或截断）不能下发：退化成 {} 会让
		// 客户端以空参数执行工具，丢弃并计数由上层告警。
		var input map[string]any
		if err := json.Unmarshal([]byte(args), &input); err != nil {
			t.droppedTools++
			continue
		}
		// 真实 Anthropic 流的工具参数走 input_json_delta 分片；客户端只从分片
		// 累积参数、忽略 start.input。塞进 start 会被当成 {}（2026-10-22 排查）。
		m, _ := json.Marshal(input)
		t.emit("content_block_start", map[string]any{
			"type": "content_block_start", "index": t.blockIdx,
			"content_block": map[string]any{"type": "tool_use", "id": acc.id, "name": acc.name, "input": map[string]any{}},
		})
		t.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": t.blockIdx,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": string(m)},
		})
		t.emit("content_block_stop", map[string]any{"type": "content_block_stop", "index": t.blockIdx})
		t.blockIdx++
		emitted++
	}
	t.tools = map[int]*toolAcc{}
	return emitted
}

// close 收口全部打开块（text 即时块 + 累积中的 tool 块）并发
// message_delta+message_stop；幂等。
func (t *OpenAIStreamTransformer) close(reason string) {
	if t.finished {
		return
	}
	t.finished = true
	t.closeText()
	emitted := t.emitTools()
	// 全部工具块因参数残缺被丢弃时，声明 tool_use 会让客户端等待一个不存在的
	// 工具块；降级为 end_turn（droppedTools 已计数，由上层告警）。
	if reason == "tool_use" && emitted == 0 {
		reason = "end_turn"
	}
	t.emit("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": reason, "stop_sequence": nil},
		// usage 同时带 input/output：usage-only 尾块晚于 message_start 到达时不丢
		"usage": map[string]any{"input_tokens": t.inTok, "output_tokens": t.outTok},
	})
	t.emit("message_stop", map[string]any{"type": "message_stop"})
}

// handleChunk 处理一个已解析的 OpenAI chunk（spec §5.3.3 的事件编排）。
func (t *OpenAIStreamTransformer) handleChunk(data []byte) {
	var c oaixChunk
	if json.Unmarshal(data, &c) != nil {
		return
	}
	// usage 先于 startMessage 落账：首 chunk 携带 usage 时 message_start 直接带真实 input_tokens
	if c.Usage != nil {
		t.inTok, t.outTok = c.Usage.PromptTokens, c.Usage.CompletionTokens
	}
	if !t.started {
		t.msgID, t.model = c.ID, c.Model
		if t.msgID != "" {
			t.msgID = "msg_" + t.msgID
		}
		t.startMessage()
	}
	if len(c.Choices) == 0 { // usage-only 尾块或心跳
		return
	}
	if t.finished { // finish_reason/[DONE] 之后的迟到 chunk：不再产生事件
		return
	}
	ch := c.Choices[0]
	d := ch.Delta
	if d.Content != "" {
		if !t.textOpen {
			t.emit("content_block_start", map[string]any{
				"type": "content_block_start", "index": t.blockIdx,
				"content_block": map[string]any{"type": "text", "text": ""},
			})
			t.textOpen = true
			t.blockIdx++
		}
		t.emit("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": t.blockIdx - 1,
			"delta": map[string]any{"type": "text_delta", "text": d.Content},
		})
	}
	for _, tc := range d.ToolCalls { // 仅累积；收口时 emitTools 一次性发块
		acc := t.tools[tc.Index]
		if acc == nil {
			acc = &toolAcc{}
			t.tools[tc.Index] = acc
		}
		if tc.ID != "" {
			acc.id = tc.ID
		}
		name, args := tc.Function.Name, tc.Function.Arguments
		if name == "" {
			name = tc.Name // 非标准扁平形态兼容
		}
		if args == "" {
			args = tc.Arguments
		}
		if name != "" {
			acc.name = name
		}
		if args != "" {
			acc.args.WriteString(args)
		}
	}
	if ch.FinishReason != nil && *ch.FinishReason != "" {
		t.sawTerminal = true
		t.close(mapFinishReason(*ch.FinishReason))
	}
}

// handleEvent 处理一个完整 SSE 事件字节（取全部 data: 行，通常一行）。
func (t *OpenAIStreamTransformer) handleEvent(event []byte) {
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
			t.startMessage()
			t.close("end_turn")
			return
		}
		t.handleChunk([]byte(payload))
	}
}

// findEvent 定位完整 SSE 事件（\n\n 或 \r\n\r\n 结尾），返回事件字节与消耗长度。
func findEvent(raw []byte) (event []byte, consumed int, ok bool) {
	i1 := bytes.Index(raw, []byte("\n\n"))
	i2 := bytes.Index(raw, []byte("\r\n\r\n"))
	switch {
	case i1 >= 0 && (i2 < 0 || i1 < i2):
		return raw[:i1], i1 + 2, true
	case i2 >= 0:
		return raw[:i2], i2 + 4, true
	default:
		return nil, 0, false
	}
}

func (t *OpenAIStreamTransformer) Feed(p []byte) []byte {
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

// Finish 收口：已 started 但缺 finish_reason → end_turn；完全空流 → 最小完整消息。
func (t *OpenAIStreamTransformer) Finish() []byte {
	t.startMessage()
	t.close("end_turn")
	out := t.drain()
	t.raw = nil
	return out
}

// Abort 以 error 事件收口截断流：关闭已打开的 text 块、丢弃累积中的工具块，
// 不写 message_delta/message_stop。幂等。
func (t *OpenAIStreamTransformer) Abort(msg string) []byte {
	if t.finished {
		return nil
	}
	t.finished = true
	t.startMessage() // 空流截断也保证 message_start 先于 error（幂等）
	t.closeText()
	t.droppedTools += len(t.tools) // 未完成工具块随截断一并丢弃，计数供上层告警
	t.tools = map[int]*toolAcc{}
	t.out.Write(AnthropicErrorEvent(msg))
	return t.drain()
}

func (t *OpenAIStreamTransformer) drain() []byte {
	if t.out.Len() == 0 {
		return nil
	}
	out := make([]byte, t.out.Len())
	copy(out, t.out.Bytes())
	t.out.Reset()
	return out
}
