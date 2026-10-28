// 本文件是 OpenAI 入站方向的流式响应侧：Anthropic SSE → OpenAI
// chat.completion.chunk SSE 的有状态转换器（含工具调用与终止事件映射）。
package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"time"
)

// anthxStreamPayload 解析 Anthropic SSE 的 data 载荷（按 type 分派；message_start
// 的 usage 在 message 内，message_delta 的 usage 在顶层）。
type anthxStreamPayload struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	ContentBlock struct {
		Type  string          `json:"type"`
		ID    string          `json:"id"`
		Name  string          `json:"name"`
		Input json.RawMessage `json:"input"`
	} `json:"content_block"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

type oaiInboundChunkToolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaiInboundChunkDelta struct {
	Role      string                    `json:"role,omitempty"`
	Content   string                    `json:"content,omitempty"`
	ToolCalls []oaiInboundChunkToolCall `json:"tool_calls,omitempty"`
}

type oaiInboundChunkChoice struct {
	Index        int                  `json:"index"`
	Delta        oaiInboundChunkDelta `json:"delta"`
	FinishReason *string              `json:"finish_reason"`
}

type oaiInboundChunk struct {
	ID      string                  `json:"id"`
	Object  string                  `json:"object"`
	Created int64                   `json:"created"`
	Model   string                  `json:"model"`
	Choices []oaiInboundChunkChoice `json:"choices"`
}

// anthropicToOpenAIStream 把 Anthropic SSE 逐步转换为 OpenAI chat.completion.chunk
// 字节（AnthropicStreamToOpenAIStream 的内核；与 OpenAIStreamTransformer 方向相反、
// 风格对齐：Feed 逐事件 flush，Finish 收口）。model 恒为 requestedModel（逻辑模型
// 名，不泄露上游名）；text 增量即时出 content 帧，tool_use 出 tool_calls 帧
// （index 单调分配，input_json_delta 原样分片），message_delta 的 stop_reason 映射
// 为末帧 finish_reason，收口补 [DONE]。截断/上游错误由 Abort 出 error chunk
// （不补 [DONE]）；Finish 只用于已见终止标记的正常收口。
// 非并发安全：单一 goroutine 驱动。
type anthropicToOpenAIStream struct {
	raw            []byte
	out            bytes.Buffer
	requestedModel string
	started        bool
	finished       bool
	id             string
	created        int64
	toolIdx        map[int]int // anthropic block index → OpenAI tool_calls index
	nextTool       int
	stopReason     string
	sawTerminal    bool
}

func (t *anthropicToOpenAIStream) emitChunk(delta oaiInboundChunkDelta, finish string) {
	ch := oaiInboundChunk{
		ID: t.id, Object: "chat.completion.chunk", Created: t.created, Model: t.requestedModel,
		Choices: []oaiInboundChunkChoice{{Index: 0, Delta: delta}},
	}
	if finish != "" {
		f := finish
		ch.Choices[0].FinishReason = &f
	}
	b, err := json.Marshal(ch)
	if err != nil {
		return
	}
	t.out.WriteString("data: ")
	t.out.Write(b)
	t.out.WriteString("\n\n")
}

// start 发首帧（role=assistant）；message_start 或首个内容事件触发。id 映射同
// AnthropicResponseToOpenAI：chatcmpl-<msg id>。
func (t *anthropicToOpenAIStream) start() {
	if t.started {
		return
	}
	t.started = true
	if t.id == "" {
		t.id = "chatcmpl-0"
	} else if !strings.HasPrefix(t.id, "chatcmpl-") {
		t.id = "chatcmpl-" + t.id
	}
	if t.created == 0 {
		t.created = time.Now().Unix()
	}
	t.emitChunk(oaiInboundChunkDelta{Role: "assistant"}, "")
}

// close 收口：末帧 finish_reason + [DONE]；幂等。
func (t *anthropicToOpenAIStream) close() {
	if t.finished {
		return
	}
	t.finished = true
	t.start()
	t.emitChunk(oaiInboundChunkDelta{}, mapOpenAIFinishReason(t.stopReason))
	t.out.WriteString("data: [DONE]\n\n")
}

// SawTerminal 报告上游是否给出 message_stop 或 error。
func (t *anthropicToOpenAIStream) SawTerminal() bool { return t.sawTerminal }

// Abort 以 error chunk 终止截断流：不写 finish_reason、不写 [DONE]。幂等。
func (t *anthropicToOpenAIStream) Abort(msg string) []byte {
	t.abortCore(msg)
	return t.drain()
}

// abortCore 标记流已终止并写入 error chunk（不写 finish_reason/[DONE]）。不 drain：
// 由 Abort 或后续 Feed 取回，保证 handleData 内的错误事件仍能流到客户端。幂等。
func (t *anthropicToOpenAIStream) abortCore(msg string) {
	if t.finished {
		return
	}
	t.finished = true
	t.out.Write(OpenAIStreamErrorChunk(msg))
}

func (t *anthropicToOpenAIStream) handleData(payload []byte) {
	if t.finished {
		// 终止事件（message_stop / error / Abort）之后的帧一律忽略：
		// 不得在 [DONE] 或 error chunk 之后再造出任何 chunk。
		return
	}
	var p anthxStreamPayload
	if json.Unmarshal(payload, &p) != nil {
		return
	}
	switch p.Type {
	case "message_start":
		if p.Message.ID != "" {
			t.id = "chatcmpl-" + p.Message.ID
		}
		t.start()
	case "content_block_start":
		t.start()
		if p.ContentBlock.Type == "tool_use" {
			idx := t.nextTool
			t.nextTool++
			t.toolIdx[p.Index] = idx
			var tc oaiInboundChunkToolCall
			tc.Index = idx
			tc.ID = p.ContentBlock.ID
			tc.Type = "function"
			tc.Function.Name = p.ContentBlock.Name
			tc.Function.Arguments = ""
			t.emitChunk(oaiInboundChunkDelta{ToolCalls: []oaiInboundChunkToolCall{tc}}, "")
		}
	case "content_block_delta":
		t.start()
		switch p.Delta.Type {
		case "text_delta":
			if p.Delta.Text != "" {
				t.emitChunk(oaiInboundChunkDelta{Content: p.Delta.Text}, "")
			}
		case "input_json_delta":
			if idx, ok := t.toolIdx[p.Index]; ok && p.Delta.PartialJSON != "" {
				var tc oaiInboundChunkToolCall
				tc.Index = idx
				tc.Function.Arguments = p.Delta.PartialJSON
				t.emitChunk(oaiInboundChunkDelta{ToolCalls: []oaiInboundChunkToolCall{tc}}, "")
			}
		}
	case "message_delta":
		if p.Delta.StopReason != "" {
			t.stopReason = p.Delta.StopReason
		}
	case "message_stop":
		t.sawTerminal = true
		t.close()
	case "error":
		// 上游错误事件：不伪装成正常结束（spec：任何上游错误都必须对客户端
		// 可见）。error chunk 留在缓冲区由 Feed 取回，不转发上游错误正文。
		t.sawTerminal = true
		t.abortCore("upstream stream error")
	}
}

// handleEvent 处理一个完整 SSE 事件字节（取全部 data: 行，同 OpenAIStreamTransformer）。
func (t *anthropicToOpenAIStream) handleEvent(event []byte) {
	for _, line := range strings.Split(string(event), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(line[len("data:"):])
		if payload == "" {
			continue
		}
		t.handleData([]byte(payload))
	}
}

func (t *anthropicToOpenAIStream) Feed(p []byte) []byte {
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

// Finish 收口：已开始但缺 message_stop → end_turn 末帧；完全空流 → 最小完整流。
func (t *anthropicToOpenAIStream) Finish() []byte {
	t.close()
	out := t.drain()
	t.raw = nil
	return out
}

func (t *anthropicToOpenAIStream) drain() []byte {
	if t.out.Len() == 0 {
		return nil
	}
	out := make([]byte, t.out.Len())
	copy(out, t.out.Bytes())
	t.out.Reset()
	return out
}

// AnthropicStreamToOpenAIStream 把 Anthropic SSE 流泵成 OpenAI chat.completion.chunk
// SSE 流（与 openai.go 的 streamBody 同构反向）。model 回显 requestedModel（逻辑
// 模型名）。调用方必须关闭 r（上游 body）**以及返回的 ReadCloser**——关闭读端会
// 解除泵在 io.Pipe 写端的阻塞，客户端断开后泵才能退出。收口区分三种结束：ctx
// 取消 → 静默关流；EOF 且上游已给终止标记 → Finish（finish_reason + [DONE]）；
// EOF 缺终止标记或读错 → Abort（error chunk，不补 [DONE]）。
func AnthropicStreamToOpenAIStream(ctx context.Context, r io.Reader, requestedModel string) io.ReadCloser {
	pr, pw := io.Pipe()
	t := &anthropicToOpenAIStream{
		requestedModel: requestedModel,
		toolIdx:        map[int]int{},
		created:        time.Now().Unix(),
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
				// 客户端/网关取消：静默收尾
			case rerr == io.EOF && t.SawTerminal():
				out = t.Finish()
			default:
				out = t.Abort("upstream stream interrupted")
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
