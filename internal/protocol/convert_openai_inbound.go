package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// 本文件是 OpenAI 入站方向：OpenAI 客户端（chat.completions）↔ Anthropic 上游
// （/v1/messages）非流式互转。与 openai.go 的反方向（Anthropic 客户端 → OpenAI
// 上游）对称；两侧共用 oaixToolCall / anthxMessage / anthxTool 形状，互不改语义。

// ---------- OpenAI 入站请求 ----------

// OpenAIChatRequest 是 chat.completions 请求的解析视图：Model/Stream/Messages
// 为路由与转换所需字段；其余未知字段收入 Extra，Marshal 时原样合并回去（透传）。
type OpenAIChatRequest struct {
	Model               string                     `json:"model"`
	Stream              bool                       `json:"stream"`
	Messages            []json.RawMessage          `json:"messages"`
	Temperature         *float64                   `json:"temperature,omitempty"`
	TopP                *float64                   `json:"top_p,omitempty"`
	MaxTokens           *int                       `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int                       `json:"max_completion_tokens,omitempty"`
	Stop                json.RawMessage            `json:"stop,omitempty"`  // string | []string
	Tools               json.RawMessage            `json:"tools,omitempty"` // []{type:function,function:{...}}
	ToolChoice          json.RawMessage            `json:"tool_choice,omitempty"`
	Extra               map[string]json.RawMessage `json:"-"` // 其余透传字段
}

var openAIChatKnownFields = map[string]bool{
	"model": true, "stream": true, "messages": true, "temperature": true, "top_p": true,
	"max_tokens": true, "max_completion_tokens": true, "stop": true,
	"tools": true, "tool_choice": true,
}

func (r *OpenAIChatRequest) UnmarshalJSON(data []byte) error {
	type known OpenAIChatRequest
	var k known
	if err := json.Unmarshal(data, &k); err != nil {
		return err
	}
	*r = OpenAIChatRequest(k)
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return err
	}
	for key, val := range top {
		if openAIChatKnownFields[key] {
			continue
		}
		if r.Extra == nil {
			r.Extra = map[string]json.RawMessage{}
		}
		r.Extra[key] = val
	}
	return nil
}

func (r OpenAIChatRequest) MarshalJSON() ([]byte, error) {
	type known OpenAIChatRequest
	b, err := json.Marshal(known(r))
	if err != nil {
		return nil, err
	}
	if len(r.Extra) == 0 {
		return b, nil
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(b, &top); err != nil {
		return nil, err
	}
	for k, v := range r.Extra {
		top[k] = v
	}
	return json.Marshal(top)
}

// ParseOpenAIChatRequest 解析 OpenAI 入站请求体；缺 model 与 JSON 非法均报
// invalid_request_error（与 ParseChatRequest 同风格）。
func ParseOpenAIChatRequest(body []byte) (OpenAIChatRequest, error) {
	var req OpenAIChatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return OpenAIChatRequest{}, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
	}
	if req.Model == "" {
		return OpenAIChatRequest{}, errors.New(ErrTypeInvalidRequest + ": model is required")
	}
	if len(req.Messages) == 0 {
		return OpenAIChatRequest{}, errors.New(ErrTypeInvalidRequest + ": messages is required")
	}
	return req, nil
}

// ---------- OpenAI 请求 → Anthropic 请求 ----------

type oaiInboundMessage struct {
	Role       string          `json:"role"`
	Content    json.RawMessage `json:"content"` // string | []part | null
	ToolCalls  []oaixToolCall  `json:"tool_calls"`
	ToolCallID string          `json:"tool_call_id"`
}

type oaiInboundTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// anthropicOutRequest 是 /v1/messages 请求输出形状（字段名同 Anthropic 官方；
// 可选字段 omitempty，避免 null 污染请求体）。
type anthropicOutRequest struct {
	Model         string          `json:"model"`
	MaxTokens     int             `json:"max_tokens"`
	System        string          `json:"system,omitempty"`
	Messages      []anthxMessage  `json:"messages"`
	Tools         []anthxTool     `json:"tools,omitempty"`
	ToolChoice    json.RawMessage `json:"tool_choice,omitempty"`
	Temperature   *float64        `json:"temperature,omitempty"`
	TopP          *float64        `json:"top_p,omitempty"`
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Stream        bool            `json:"stream,omitempty"`
}

// anthropicOutBlock 是请求侧 content block（text/tool_use/tool_result），带
// omitempty；不复用 openai.go 的 anthxBlock（解析用、无 omitempty）。
type anthropicOutBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// openAIContentText 把 OpenAI 消息 content（string | []part | null）归并为纯
// 文本（text-only：image_url 等多模态 part 丢弃，与包内既有纯文本归并一致）。
func openAIContentText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var out []string
	for _, p := range parts {
		if p.Type == "text" && p.Text != "" {
			out = append(out, p.Text)
		}
	}
	return strings.Join(out, "\n")
}

// parseOpenAIStop 兼容 OpenAI stop 的 string | []string 两种形状。
func parseOpenAIStop(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}
	}
	var arr []string
	if json.Unmarshal(raw, &arr) == nil {
		return arr
	}
	return nil
}

// toolChoiceToAnthropic 映射 tool_choice："none" → nil（并由调用方丢弃 tools，
// Anthropic 无 none）；"auto"/"required" → {type:auto|any}；function 指定 →
// {type:tool,name}。其余形状退化为 nil（默认 auto 行为）。
func toolChoiceToAnthropic(raw json.RawMessage) (json.RawMessage, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		switch s {
		case "auto":
			return json.RawMessage(`{"type":"auto"}`), false
		case "required":
			return json.RawMessage(`{"type":"any"}`), false
		case "none":
			return nil, true
		}
		return nil, false
	}
	var obj struct {
		Type     string `json:"type"`
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Function.Name != "" {
		b, _ := json.Marshal(map[string]string{"type": "tool", "name": obj.Function.Name})
		return b, false
	}
	return nil, false
}

// OpenAIRequestToAnthropic 把入站 chat.completions 请求转为 /v1/messages body；
// model 替换为 anthropicModel（上游真实模型名）。映射：system 升顶层 system，
// user/assistant/tool 按序入 messages（连续 tool 消息合并为同一条 user 的
// tool_result 块），temperature/top_p/stop_sequences 同名映射，tools/tool_choice
// 对换。max_tokens 缺省 4096——Anthropic 侧必填而 OpenAI 客户端常省略。
// text-only：多模态 content part 只取 text。
func OpenAIRequestToAnthropic(oa OpenAIChatRequest, anthropicModel string) ([]byte, error) {
	out := anthropicOutRequest{
		Model:         anthropicModel,
		Temperature:   oa.Temperature,
		TopP:          oa.TopP,
		Stream:        oa.Stream,
		StopSequences: parseOpenAIStop(oa.Stop),
	}
	out.MaxTokens = 4096
	if oa.MaxCompletionTokens != nil && *oa.MaxCompletionTokens > 0 {
		out.MaxTokens = *oa.MaxCompletionTokens
	} else if oa.MaxTokens != nil && *oa.MaxTokens > 0 {
		out.MaxTokens = *oa.MaxTokens
	}

	var systemParts []string
	var pendingTools []anthropicOutBlock // 累积的 tool_result，合并进同一条 user 消息
	flushTools := func() {
		if len(pendingTools) == 0 {
			return
		}
		content, err := json.Marshal(pendingTools)
		if err != nil {
			return
		}
		out.Messages = append(out.Messages, anthxMessage{Role: "user", Content: content})
		pendingTools = nil
	}

	for _, raw := range oa.Messages {
		var m oaiInboundMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
		}
		switch m.Role {
		case "system", "developer":
			if txt := openAIContentText(m.Content); txt != "" {
				systemParts = append(systemParts, txt)
			}
		case "user":
			flushTools()
			txt := openAIContentText(m.Content)
			if txt == "" {
				continue // text-only：纯多模态/空消息无文本可映射
			}
			content, _ := json.Marshal(txt)
			out.Messages = append(out.Messages, anthxMessage{Role: "user", Content: content})
		case "assistant":
			flushTools()
			var blocks []anthropicOutBlock
			if txt := openAIContentText(m.Content); txt != "" {
				blocks = append(blocks, anthropicOutBlock{Type: "text", Text: txt})
			}
			for _, tc := range m.ToolCalls {
				input := map[string]any{}
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
				if input == nil {
					input = map[string]any{}
				}
				rawIn, _ := json.Marshal(input)
				blocks = append(blocks, anthropicOutBlock{
					Type: "tool_use", ID: tc.ID, Name: tc.Function.Name, Input: rawIn,
				})
			}
			if len(blocks) == 0 {
				continue
			}
			content, _ := json.Marshal(blocks)
			out.Messages = append(out.Messages, anthxMessage{Role: "assistant", Content: content})
		case "tool":
			txt := openAIContentText(m.Content)
			content, _ := json.Marshal(txt)
			pendingTools = append(pendingTools, anthropicOutBlock{
				Type: "tool_result", ToolUseID: m.ToolCallID, Content: content,
			})
		}
	}
	flushTools()

	if len(systemParts) > 0 {
		out.System = strings.Join(systemParts, "\n")
	}

	if len(oa.Tools) > 0 {
		var tools []oaiInboundTool
		if err := json.Unmarshal(oa.Tools, &tools); err != nil {
			return nil, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
		}
		tc, disable := toolChoiceToAnthropic(oa.ToolChoice)
		if !disable {
			for _, tl := range tools {
				var t anthxTool
				t.Name, t.Description, t.InputSchema = tl.Function.Name, tl.Function.Description, tl.Function.Parameters
				out.Tools = append(out.Tools, t)
			}
			out.ToolChoice = tc
		}
	}

	return json.Marshal(out)
}

// ---------- Anthropic 响应 → OpenAI 响应（非流式）----------

type oaiInboundOutMessage struct {
	Role      string         `json:"role"`
	Content   *string        `json:"content"`
	ToolCalls []oaixToolCall `json:"tool_calls,omitempty"`
}

type oaiInboundOutChoice struct {
	Index        int                  `json:"index"`
	Message      oaiInboundOutMessage `json:"message"`
	FinishReason string               `json:"finish_reason"`
}

type oaiInboundOutResponse struct {
	ID      string                `json:"id"`
	Object  string                `json:"object"`
	Created int64                 `json:"created"`
	Model   string                `json:"model"`
	Choices []oaiInboundOutChoice `json:"choices"`
	Usage   struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage"`
}

// mapOpenAIFinishReason 把 Anthropic stop_reason 映射回 OpenAI finish_reason。
func mapOpenAIFinishReason(s string) string {
	switch s {
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default: // end_turn / stop_sequence / 空
		return "stop"
	}
}

// AnthropicResponseToOpenAI 转换 Anthropic 非流响应为 chat.completions 形状
// （spec：id/choices/content/usage 映射）。model 用 requestedModel（逻辑模型名），
// 不泄露上游模型名；usage 落到 prompt_tokens/completion_tokens/total_tokens。
func AnthropicResponseToOpenAI(anthropicBody []byte, requestedModel string) ([]byte, error) {
	var ar anthxResponse
	if err := json.Unmarshal(anthropicBody, &ar); err != nil {
		return nil, fmt.Errorf("decode upstream body: %w", err)
	}
	id := "chatcmpl-0"
	if ar.ID != "" {
		id = "chatcmpl-" + ar.ID
	}
	resp := oaiInboundOutResponse{ID: id, Object: "chat.completion", Created: time.Now().Unix(), Model: requestedModel}
	resp.Usage.PromptTokens = ar.Usage.InputTokens
	resp.Usage.CompletionTokens = ar.Usage.OutputTokens
	resp.Usage.TotalTokens = ar.Usage.InputTokens + ar.Usage.OutputTokens

	var texts []string
	var calls []oaixToolCall
	for _, b := range ar.Content {
		switch b.Type {
		case "text":
			if b.Text != "" {
				texts = append(texts, b.Text)
			}
		case "tool_use":
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			tc := oaixToolCall{ID: b.ID, Type: "function"}
			tc.Function.Name, tc.Function.Arguments = b.Name, args
			calls = append(calls, tc)
		}
	}
	var content *string
	switch {
	case len(texts) > 0:
		j := strings.Join(texts, "\n")
		content = &j
	case len(calls) == 0:
		j := ""
		content = &j
	default: // 仅 tool_calls：OpenAI content 为 null
	}

	resp.Choices = []oaiInboundOutChoice{{
		Message: oaiInboundOutMessage{
			Role:      "assistant",
			Content:   content,
			ToolCalls: calls,
		},
		FinishReason: mapOpenAIFinishReason(ar.StopReason),
	}}
	return json.Marshal(resp)
}

// ---------- 流式转换：Anthropic SSE → OpenAI chunk SSE ----------

// OpenAIErrorBody 构造 chat.completions 入站的错误体（OpenAI error 形状：
// {"error":{"message","type"}}），与 ErrorEnvelope（Anthropic 形状）对称——
// 错误 envelope 形状随入站协议而异。
func OpenAIErrorBody(errType, message string) []byte {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]string{"message": message, "type": errType},
	})
	return b
}

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
// 为末帧 finish_reason，收口补 [DONE]。截断流由 Finish 兜底 finish=stop。
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

// start 发首帧（role=assistant）；message_start 或首个内容事件触发，截断流由
// close 兜底。id 映射同 AnthropicResponseToOpenAI：chatcmpl-<msg id>。
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
	if t.finished {
		return nil
	}
	t.finished = true
	t.out.Write(OpenAIStreamErrorChunk(msg))
	return t.drain()
}

func (t *anthropicToOpenAIStream) handleData(payload []byte) {
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
	case "message_stop", "error":
		t.sawTerminal = true
		// error 事件亦收口：客户端拿得到 finish 而非悬空流。
		t.close()
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
// 模型名）。src 由调用方关闭；EOF/写失败/读错所有路径都经 Finish 收口。
func AnthropicStreamToOpenAIStream(r io.Reader, requestedModel string) io.Reader {
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
