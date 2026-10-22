# 流式截断显式化与空闲超时 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让四条流式路径在上游中断/超时时向客户端发出明确 error 事件，绝不把残缺工具参数伪装成正常结束；并把流式请求的 120s 总超时改为响应头等待 + 输出空闲超时。

**Architecture:** `protocol` 层的两个流式转换器新增终止标记跟踪与 `Abort`（丢弃未完成工具块、写 error 事件、不写 `message_stop`/`[DONE]`）；`provider` 层区分 EOF/取消/非 EOF 错误，并为两条直通路径提供通用守卫；`gateway` 层把流式总超时改为响应头计时器 + `copyStream` 空闲 watchdog，并按入站协议写 error 事件。

**Tech Stack:** Go 1.27.2、`net/http`、`log/slog`、`httptest`（测试全部离线，无新依赖）。

**Spec:** `docs/superpowers/specs/2026-10-22-stream-truncation-idle-timeout-design.md`

## Global Constraints

- 不新增第三方依赖；测试全部离线（Fake Provider / httptest）。
- 首字节已写出后**绝不**转移候选或重放请求（docx §6.3）。
- 客户端取消：静默收尾，不写 error、不冷却、不重放。
- 日志/错误事件只含稳定描述；不得包含密钥、Authorization、请求体或上游原文。
- 错误事件形状：Anthropic 为 `event: error` + `{"type":"error","error":{"type":"api_error","message":...}}`；OpenAI 为 `data: {"error":{"message":...,"type":"api_error"}}`，截断后不发 `[DONE]`。
- 复用 `RequestTimeoutSeconds`：非流式=总时限；流式=响应头等待时限 + 客户端可见输出空闲时限。
- 涉及并发/计时的测试必须带 `-race`。
- 提交信息沿用仓库风格：`fix(protocol): ...`、`fix(provider): ...`、`fix(gateway): ...`。

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/protocol/openai.go` | OpenAI 上游 → Anthropic 入站转换器：终止标记、`Abort`、跳过残缺工具块 |
| `internal/protocol/convert_openai_inbound.go` | Anthropic 上游 → OpenAI 入站转换器：终止标记、`Abort`、泵错误区分 |
| `internal/protocol/errors.go`（新建） | 两种入站协议的流式 error 事件构造 helper |
| `internal/provider/stream.go`（新建） | 通用直通 SSE 守卫：原样转发、检测终止标记、截断时追加 error |
| `internal/provider/openai.go` | OpenAI adapter：泵区分 EOF/取消/错误，接 `Abort` |
| `internal/provider/anthropic.go` | Anthropic adapter：流式 2xx 接直通守卫 |
| `internal/gateway/handlers.go` | 流式超时改造、`copyStream` 空闲 watchdog、OpenAI 直通接守卫 |
| `docs/…/2026-10-22-stream-truncation-idle-timeout-design.md` | 设计依据（只读） |
| `CLAUDE.md` | 超时语义说明同步 |

---

### Task 1: OpenAI 转换器跟踪终止标记

**Files:**
- Modify: `internal/protocol/openai.go`（`OpenAIStreamTransformer` 结构体、`handleChunk`、`handleEvent`）
- Test: `internal/protocol/openai_test.go`

**Interfaces:**
- Produces: `func (t *OpenAIStreamTransformer) SawTerminal() bool`

- [ ] **Step 1: 写失败测试**

在 `internal/protocol/openai_test.go` 末尾追加：

```go
func TestStreamTransformerTerminalMarker(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	if tu.SawTerminal() {
		t.Fatal("fresh transformer must not report terminal")
	}
	tu.Feed([]byte(`data: {"id":"c","choices":[{"delta":{"content":"x"},"finish_reason":null}]}` + "\n\n"))
	if tu.SawTerminal() {
		t.Fatal("finish_reason:null must not count as terminal")
	}
	tu.Feed([]byte(`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
	if !tu.SawTerminal() {
		t.Fatal("finish_reason must mark terminal")
	}

	done := NewOpenAIStreamTransformer()
	done.Feed([]byte("data: [DONE]\n\n"))
	if !done.SawTerminal() {
		t.Fatal("[DONE] must mark terminal")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestStreamTransformerTerminalMarker -v`
Expected: 编译失败 `tu.SawTerminal undefined`

- [ ] **Step 3: 最小实现**

`OpenAIStreamTransformer` 结构体增加字段：

```go
	sawTerminal bool // 已收到 finish_reason 或 [DONE]
```

`handleChunk` 中 `finish_reason` 分支改为：

```go
	if ch.FinishReason != nil && *ch.FinishReason != "" {
		t.sawTerminal = true
		t.close(mapFinishReason(*ch.FinishReason))
	}
```

`handleEvent` 的 `[DONE]` 分支改为：

```go
		if payload == "[DONE]" {
			t.sawTerminal = true
			t.startMessage()
			t.close("end_turn")
			return
		}
```

新增方法：

```go
// SawTerminal 报告上游是否给出正常终止标记（finish_reason 或 [DONE]）。
func (t *OpenAIStreamTransformer) SawTerminal() bool { return t.sawTerminal }
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestStreamTransformer -v`
Expected: PASS（含既有 Transformer 用例）

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/openai.go internal/protocol/openai_test.go
git commit -m "fix(protocol): 跟踪 OpenAI 流终止标记"
```

---

### Task 2: 跳过参数残缺的工具块

**Files:**
- Modify: `internal/protocol/openai.go`（`OpenAIStreamTransformer` 字段、`emitTools`）
- Test: `internal/protocol/openai_test.go`

**Interfaces:**
- Produces: `func (t *OpenAIStreamTransformer) DroppedTools() int`

- [ ] **Step 1: 写失败测试**

```go
func TestStreamTransformerDropsIncompleteToolArgs(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	var acc []byte
	acc = append(acc, tu.Feed([]byte(
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","function":{"name":"Write","arguments":"{\"file_path\":\"/tmp/a\",\"content\":\"abc"}}]}}]}`+"\n\n"))...)
	// 正常 finish_reason 但参数 JSON 不完整（上游自身 bug）：该块必须丢弃。
	acc = append(acc, tu.Feed([]byte(
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`+"\n\n"))...)
	out := string(acc)
	if strings.Contains(out, `"name":"Write"`) {
		t.Fatalf("incomplete tool block must not be emitted:\n%s", out)
	}
	if tu.DroppedTools() != 1 {
		t.Fatalf("DroppedTools = %d, want 1", tu.DroppedTools())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestStreamTransformerDropsIncompleteToolArgs -v`
Expected: FAIL — 输出包含 `"name":"Write"`

- [ ] **Step 3: 最小实现**

结构体增加：

```go
	droppedTools int // 参数 JSON 不完整而丢弃的工具块数
```

`emitTools` 中替换参数解析段：

```go
		acc := t.tools[i]
		var input map[string]any
		if err := json.Unmarshal([]byte(acc.args.String()), &input); err != nil {
			t.droppedTools++
			continue
		}
		m, _ := json.Marshal(input)
```

新增：

```go
// DroppedTools 报告因参数 JSON 不完整而丢弃的工具块数。
func (t *OpenAIStreamTransformer) DroppedTools() int { return t.droppedTools }
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestStreamTransformer -v`
Expected: PASS（既有完整参数用例仍通过）

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/openai.go internal/protocol/openai_test.go
git commit -m "fix(protocol): 不再下发参数残缺的工具块"
```

---

### Task 3: OpenAI 转换器 Abort

**Files:**
- Modify: `internal/protocol/openai.go`
- Create: `internal/protocol/errors.go`
- Test: `internal/protocol/openai_test.go`

**Interfaces:**
- Produces: `func (t *OpenAIStreamTransformer) Abort(msg string) []byte`
- Produces: `func AnthropicErrorEvent(msg string) []byte`

- [ ] **Step 1: 写失败测试**

```go
func TestStreamTransformerAbort(t *testing.T) {
	tu := NewOpenAIStreamTransformer()
	var acc []byte
	acc = append(acc, tu.Feed([]byte(`data: {"id":"c","choices":[{"delta":{"content":"par"}}]}`+"\n\n"))...)
	acc = append(acc, tu.Abort("upstream stream interrupted")...)
	out := string(acc)
	for _, want := range []string{"event: message_start", "event: content_block_stop", "event: error",
		`"type":"api_error"`, "upstream stream interrupted"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Index(out, "message_start") > strings.Index(out, "event: error") {
		t.Fatalf("message_start must precede error:\n%s", out)
	}
	if strings.Contains(out, "message_stop") {
		t.Fatalf("Abort must not emit message_stop:\n%s", out)
	}
	if again := tu.Abort("again"); len(again) != 0 {
		t.Fatalf("Abort must be idempotent, got %q", again)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestStreamTransformerAbort -v`
Expected: 编译失败 `tu.Abort undefined`

- [ ] **Step 3: 最小实现**

新建 `internal/protocol/errors.go`：

```go
package protocol

import "encoding/json"

// AnthropicErrorEvent 构造 Anthropic SSE error 事件（流已提交后的唯一错误通道）。
func AnthropicErrorEvent(msg string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "api_error", "message": msg},
	})
	return []byte("event: error\ndata: " + string(b) + "\n\n")
}
```

`openai.go` 新增：

```go
// Abort 以 error 事件收口截断流：关闭已打开的 text 块、丢弃累积中的工具块，
// 不写 message_delta/message_stop。幂等。
func (t *OpenAIStreamTransformer) Abort(msg string) []byte {
	if t.finished {
		return nil
	}
	t.finished = true
	t.startMessage() // 空流截断也保证 message_start 先于 error（幂等）
	t.closeText()
	t.tools = map[int]*toolAcc{}
	t.out.Write(AnthropicErrorEvent(msg))
	return t.drain()
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run 'TestStreamTransformer' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/openai.go internal/protocol/errors.go internal/protocol/openai_test.go
git commit -m "fix(protocol): 截断流以 error 事件收口"
```

---

### Task 4: OpenAI adapter 区分 EOF / 取消 / 错误

**Files:**
- Modify: `internal/provider/openai.go`（`Do` 调用点、`streamBody`）
- Test: `internal/provider/provider_test.go`

**Interfaces:**
- Consumes: `SawTerminal()`、`Abort()`、`DroppedTools()`（Task 1–3）
- Produces: 无新导出 API；`streamBody` 签名改为 `func (a *OpenAIAdapter) streamBody(ctx context.Context, src io.ReadCloser) io.ReadCloser`

- [ ] **Step 1: 写失败测试**

在 `internal/provider/provider_test.go` 追加（需 `net/http/httptest`、`bufio` 已按需导入）：

```go
func TestOpenAIAdapterStreamInterruptedEmitsError(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte(`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"t1","function":{"name":"Write","arguments":"{\"file_path\":\"/tmp/a\""}}]}}]}` + "\n\n"))
		f.Flush()
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("no hijacker")
		}
		conn, _, _ := hj.Hijack()
		conn.Close() // 上游中断：无 finish_reason/[DONE]
	}))
	defer up.Close()
	res, err := NewOpenAIAdapter(nil).Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "up-1", Stream: true,
		RawBody: []byte(`{"model":"up-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	out := string(b)
	if !strings.Contains(out, "event: error") || !strings.Contains(out, "upstream stream interrupted") {
		t.Fatalf("missing error event:\n%s", out)
	}
	if strings.Contains(out, "message_stop") {
		t.Fatalf("truncated stream must not emit message_stop:\n%s", out)
	}
	if strings.Contains(out, `"name":"Write"`) {
		t.Fatalf("incomplete tool block leaked:\n%s", out)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/provider -run TestOpenAIAdapterStreamInterruptedEmitsError -v`
Expected: FAIL — 输出含 `message_stop`、无 `event: error`

- [ ] **Step 3: 最小实现**

`Do` 中调用点改为：

```go
	res.Body = a.streamBody(ctx, res.Body)
```

`streamBody` 改为：

```go
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
			switch {
			case ctx.Err() != nil:
				// 客户端/网关取消：静默收尾
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
			if a.Log != nil && t.DroppedTools() > 0 {
				a.Log.Warn("dropped incomplete tool call", "count", t.DroppedTools())
			}
			pw.Close()
			return
		}
	}()
	return pr
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test -race ./internal/provider -run 'TestOpenAIAdapter' -v`
Expected: PASS（含既有 `TestOpenAIAdapterStreamEndToEnd`）

- [ ] **Step 5: 提交**

```bash
git add internal/provider/openai.go internal/provider/provider_test.go
git commit -m "fix(provider): 上游流中断不再伪装正常结束"
```

---

### Task 5: Anthropic→OpenAI 转换器 Abort 与终止标记

**Files:**
- Modify: `internal/protocol/convert_openai_inbound.go`（`anthropicToOpenAIStream` 字段、`handleData`、新增方法）
- Modify: `internal/protocol/errors.go`（新增 OpenAI helper）
- Test: `internal/protocol/convert_openai_inbound_test.go`

**Interfaces:**
- Produces: `func (t *anthropicToOpenAIStream) SawTerminal() bool`
- Produces: `func (t *anthropicToOpenAIStream) Abort(msg string) []byte`
- Produces: `func OpenAIStreamErrorChunk(msg string) []byte`

- [ ] **Step 1: 写失败测试**

```go
func TestAnthropicToOpenAIStreamAbort(t *testing.T) {
	tu := &anthropicToOpenAIStream{requestedModel: "logical-1", toolIdx: map[int]int{}, created: 1}
	tu.Feed([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
	out := string(tu.Abort("upstream stream interrupted"))
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, "upstream stream interrupted") {
		t.Fatalf("missing error chunk:\n%s", out)
	}
	if strings.Contains(out, "[DONE]") {
		t.Fatalf("Abort must not emit [DONE]:\n%s", out)
	}
	if again := tu.Abort("again"); len(again) != 0 {
		t.Fatalf("Abort must be idempotent, got %q", again)
	}
}

func TestAnthropicToOpenAIStreamSawTerminal(t *testing.T) {
	tu := &anthropicToOpenAIStream{requestedModel: "m", toolIdx: map[int]int{}, created: 1}
	if tu.SawTerminal() {
		t.Fatal("fresh stream must not report terminal")
	}
	tu.Feed([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	if !tu.SawTerminal() {
		t.Fatal("message_stop must mark terminal")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run 'TestAnthropicToOpenAIStream(Abort|SawTerminal)' -v`
Expected: 编译失败 `Abort/SawTerminal undefined`

- [ ] **Step 3: 最小实现**

`errors.go` 追加：

```go
// OpenAIStreamErrorChunk 构造 OpenAI 流式 error chunk（截断后不补 [DONE]）。
func OpenAIStreamErrorChunk(msg string) []byte {
	b, _ := json.Marshal(map[string]any{
		"error": map[string]any{"message": msg, "type": "api_error"},
	})
	return []byte("data: " + string(b) + "\n\n")
}
```

`anthropicToOpenAIStream` 结构体增加：

```go
	sawTerminal bool
```

`handleData` 的 `message_stop`/`error` 分支改为：

```go
	case "message_stop", "error":
		t.sawTerminal = true
		// error 事件亦收口：客户端拿得到 finish 而非悬空流。
		t.close()
```

新增：

```go
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
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run 'TestAnthropicStreamToOpenAI|TestAnthropicToOpenAIStream' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/convert_openai_inbound.go internal/protocol/errors.go internal/protocol/convert_openai_inbound_test.go
git commit -m "fix(protocol): Anthropic→OpenAI 流支持截断收口"
```

---

### Task 6: Anthropic→OpenAI 泵区分 EOF / 取消 / 错误

**Files:**
- Modify: `internal/protocol/convert_openai_inbound.go`（`AnthropicStreamToOpenAIStream`）
- Modify: `internal/gateway/handlers.go:449`（调用点传 ctx）
- Modify: `internal/protocol/convert_openai_inbound_test.go`（两处既有调用补 `context.Background()`）
- Test: `internal/protocol/convert_openai_inbound_test.go`

**Interfaces:**
- Consumes: `SawTerminal()`、`Abort()`（Task 5）
- Produces: `func AnthropicStreamToOpenAIStream(ctx context.Context, r io.Reader, requestedModel string) io.ReadCloser`

- [ ] **Step 1: 写失败测试**

```go
func TestAnthropicStreamToOpenAIStreamTruncated(t *testing.T) {
	src := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"par\"}}\n\n"
	out, err := io.ReadAll(AnthropicStreamToOpenAIStream(context.Background(), strings.NewReader(src), "logical-1"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "upstream stream interrupted") {
		t.Fatalf("missing error chunk:\n%s", s)
	}
	if strings.Contains(s, "[DONE]") {
		t.Fatalf("truncated stream must not emit [DONE]:\n%s", s)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestAnthropicStreamToOpenAIStreamTruncated -v`
Expected: FAIL — 输出含 `[DONE]`、无错误文本

- [ ] **Step 3: 最小实现**

签名与泵：

```go
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
```

`handlers.go:449` 调用改为：

```go
			copyStream(w, protocol.AnthropicStreamToOpenAIStream(ctx, res.Body, oar.Model))
```

既有测试两处 `AnthropicStreamToOpenAIStream(strings.NewReader(src), "logical-1")` 改为 `AnthropicStreamToOpenAIStream(context.Background(), strings.NewReader(src), "logical-1")`，并补 `"context"` 导入。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test -race ./internal/protocol ./internal/gateway -run 'TestAnthropicStreamToOpenAI' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/convert_openai_inbound.go internal/protocol/convert_openai_inbound_test.go internal/gateway/handlers.go
git commit -m "fix(protocol): 截断的 Anthropic 流转出 error chunk"
```

---

### Task 7: 通用直通 SSE 守卫

**Files:**
- Create: `internal/provider/stream.go`
- Test: `internal/provider/provider_test.go`

**Interfaces:**
- Produces: `func WrapPassthroughStream(ctx context.Context, src io.ReadCloser, onTruncate []byte, terminals ...string) io.ReadCloser`

- [ ] **Step 1: 写失败测试**

```go
func TestWrapPassthroughStream(t *testing.T) {
	t.Run("normal terminal passes through unchanged", func(t *testing.T) {
		src := io.NopCloser(strings.NewReader("data: a\n\ndata: [DONE]\n\n"))
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(), src, []byte("ERR\n\n"), "[DONE]"))
		if string(out) != "data: a\n\ndata: [DONE]\n\n" {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("clean EOF without terminal appends error", func(t *testing.T) {
		src := io.NopCloser(strings.NewReader("data: a\n\n"))
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(), src, []byte("ERR\n\n"), "[DONE]"))
		if string(out) != "data: a\n\nERR\n\n" {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("read error appends error", func(t *testing.T) {
		er := &errAfterReader{data: "data: a\n\n"}
		out, _ := io.ReadAll(WrapPassthroughStream(context.Background(), er, []byte("ERR\n\n"), "[DONE]"))
		if string(out) != "data: a\n\nERR\n\n" {
			t.Fatalf("out = %q", out)
		}
	})
	t.Run("canceled ctx does not append", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		src := io.NopCloser(strings.NewReader("data: a\n\n"))
		out, _ := io.ReadAll(WrapPassthroughStream(ctx, src, []byte("ERR\n\n"), "[DONE]"))
		if string(out) != "data: a\n\n" {
			t.Fatalf("out = %q", out)
		}
	})
}

type errAfterReader struct {
	data string
	done bool
}

func (e *errAfterReader) Read(p []byte) (int, error) {
	if e.done {
		return 0, errors.New("boom")
	}
	e.done = true
	n := copy(p, e.data)
	return n, nil
}
func (e *errAfterReader) Close() error { return nil }
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/provider -run TestWrapPassthroughStream -v`
Expected: 编译失败 `WrapPassthroughStream undefined`

- [ ] **Step 3: 最小实现**

新建 `internal/provider/stream.go`：

```go
package provider

import (
	"context"
	"io"
	"strings"
)

// WrapPassthroughStream 包装直通 SSE 流：字节原样转发；跟踪终止标记
// （terminals 任一出现即视为正常结束）。EOF/读错误且未见终止标记时追加
// onTruncate；ctx 取消时静默关闭。src 由泵关闭。
func WrapPassthroughStream(ctx context.Context, src io.ReadCloser, onTruncate []byte, terminals ...string) io.ReadCloser {
	pr, pw := io.Pipe()
	go func() {
		defer src.Close()
		seen := false
		maxTail := 0
		for _, m := range terminals {
			if len(m)-1 > maxTail {
				maxTail = len(m) - 1
			}
		}
		tail := "" // 保留 marker 长度的尾部，防止终止标记被 chunk 边界切开
		scan := func(p []byte) {
			window := tail + string(p)
			for _, m := range terminals {
				if strings.Contains(window, m) {
					seen = true
				}
			}
			if len(window) > maxTail {
				window = window[len(window)-maxTail:]
			}
			tail = window
		}
		buf := make([]byte, 32<<10)
		for {
			n, rerr := src.Read(buf)
			if n > 0 {
				scan(buf[:n])
				if _, werr := pw.Write(buf[:n]); werr != nil {
					return
				}
			}
			if rerr == nil {
				continue
			}
			if ctx.Err() == nil && (!seen || rerr != io.EOF) {
				_, _ = pw.Write(onTruncate)
			}
			pw.Close()
			return
		}
	}()
	return pr
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test -race ./internal/provider -run TestWrapPassthroughStream -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/provider/stream.go internal/provider/provider_test.go
git commit -m "fix(provider): 新增直通 SSE 截断守卫"
```

---

### Task 8: Anthropic 直通流接守卫

**Files:**
- Modify: `internal/provider/anthropic.go`（`Do`）
- Test: `internal/provider/provider_test.go`

**Interfaces:**
- Consumes: `WrapPassthroughStream`（Task 7）、`protocol.AnthropicErrorEvent`（Task 3）
- Produces: 无新导出 API

- [ ] **Step 1: 写失败测试**

```go
func TestAnthropicAdapterStreamInterrupted(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"par\"}}\n\n"))
		f.Flush()
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer up.Close()
	res, err := NewAnthropicAdapter().Do(context.Background(), DoRequest{
		BaseURL: up.URL, AllowPrivate: true, APIKey: "sk-x", UpstreamModel: "up-1", Stream: true,
		RawBody: []byte(`{"model":"up-1","stream":true,"messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if !strings.Contains(string(b), "event: error") {
		t.Fatalf("missing error event:\n%s", b)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/provider -run TestAnthropicAdapterStreamInterrupted -v`
Expected: FAIL — 输出无 `event: error`

- [ ] **Step 3: 最小实现**

`AnthropicAdapter.Do` 的 return 改为：

```go
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
```

需在 `anthropic.go` 导入 `AgentSwitch/internal/protocol`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test -race ./internal/provider -run 'TestAnthropicAdapter' -v`
Expected: PASS（含既有 headers/路径用例）

- [ ] **Step 5: 提交**

```bash
git add internal/provider/anthropic.go internal/provider/provider_test.go
git commit -m "fix(provider): Anthropic 直通流截断时发出 error 事件"
```

---

### Task 9: OpenAI 直通流接守卫

**Files:**
- Modify: `internal/gateway/handlers.go`（`openaiPassthrough.Do`）
- Test: `internal/gateway/gateway_test.go`

**Interfaces:**
- Consumes: `provider.WrapPassthroughStream`（Task 7）、`protocol.OpenAIStreamErrorChunk`（Task 5）
- Produces: 无新导出 API

- [ ] **Step 1: 写失败测试**

```go
func TestOpenAIInboundPassthroughStreamInterrupted(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.Write([]byte(`data: {"id":"c","choices":[{"delta":{"content":"par"}}]}` + "\n\n"))
		f.Flush()
		hj, _ := w.(http.Hijacker)
		conn, _, _ := hj.Hijack()
		conn.Close()
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1",
				ChannelName: "main", SecretKey: "account:k1", Protocol: "openai",
				BaseURL: up.URL, AllowPrivate: true, UpstreamModel: "up-1", Priority: 10}}, nil
		}
	})
	body := `{"model":"pub-1","stream":true,"messages":[{"role":"user","content":"q"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	out := w.Body.String()
	if !strings.Contains(out, `"error"`) || !strings.Contains(out, "upstream stream interrupted") {
		t.Fatalf("missing error chunk:\n%s", out)
	}
	if strings.Contains(out, "[DONE]") {
		t.Fatalf("truncated stream must not emit [DONE]:\n%s", out)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/gateway -run TestOpenAIInboundPassthroughStreamInterrupted -v`
Expected: FAIL — 输出无 error chunk

- [ ] **Step 3: 最小实现**

`openaiPassthrough.Do` 最后一个 return 改为：

```go
	if r.Stream {
		resp.Body = provider.WrapPassthroughStream(ctx, resp.Body,
			protocol.OpenAIStreamErrorChunk("upstream stream interrupted"), "[DONE]")
	}
	return provider.ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body, RetryAfterSec: ra}, nil
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test -race ./internal/gateway -run 'TestOpenAIInbound' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/gateway/handlers.go internal/gateway/gateway_test.go
git commit -m "fix(gateway): OpenAI 直通流截断时发出 error chunk"
```

---

### Task 10: copyStream 空闲 watchdog

**Files:**
- Modify: `internal/gateway/handlers.go`（`copyStream` 与 3 个调用点先传 `0, nil`）
- Test: `internal/gateway/gateway_test.go`

**Interfaces:**
- Produces: `func copyStream(w http.ResponseWriter, src io.ReadCloser, idle time.Duration, onIdle []byte) (timedOut bool)`

- [ ] **Step 1: 写失败测试**

```go
func TestCopyStreamIdleTimeout(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()
	w := httptest.NewRecorder()
	start := time.Now()
	timedOut := copyStream(w, pr, 50*time.Millisecond, []byte("EVENT\n\n"))
	if !timedOut {
		t.Fatal("expected idle timeout")
	}
	if time.Since(start) > time.Second {
		t.Fatal("watchdog took too long")
	}
	if !strings.Contains(w.Body.String(), "EVENT") {
		t.Fatalf("idle payload missing: %q", w.Body.String())
	}
}

func TestCopyStreamPassesData(t *testing.T) {
	pr, pw := io.Pipe()
	go func() {
		pw.Write([]byte("hello"))
		pw.Close()
	}()
	w := httptest.NewRecorder()
	if copyStream(w, pr, time.Second, []byte("EVENT")) {
		t.Fatal("must not report timeout")
	}
	if w.Body.String() != "hello" {
		t.Fatalf("body = %q", w.Body.String())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/gateway -run TestCopyStream -v`
Expected: 编译失败（签名不匹配）

- [ ] **Step 3: 最小实现**

在 `handlers.go` 导入块补 `"sync/atomic"`，然后：

```go
// copyStream 逐块转发并 flush；idle>0 时监控客户端可见输出的空闲间隔，
// 超时关闭上游并写出 onIdle。返回是否因空闲超时结束。
func copyStream(w http.ResponseWriter, src io.ReadCloser, idle time.Duration, onIdle []byte) bool {
	flusher, _ := w.(http.Flusher)
	var timedOut atomic.Bool
	var timer *time.Timer
	if idle > 0 {
		timer = time.AfterFunc(idle, func() {
			timedOut.Store(true)
			src.Close()
		})
		defer timer.Stop()
	}
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			if timer != nil && !timedOut.Load() {
				timer.Reset(idle)
			}
			if _, werr := w.Write(buf[:n]); werr != nil {
				return timedOut.Load()
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			if timedOut.Load() && len(onIdle) > 0 {
				w.Write(onIdle)
				if flusher != nil {
					flusher.Flush()
				}
			}
			return timedOut.Load()
		}
	}
}
```

3 个既有调用点先改为 `copyStream(w, res.Body, 0, nil)` / `copyStream(w, protocol.AnthropicStreamToOpenAIStream(...), 0, nil)`（Task 11/12 接线）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test -race ./internal/gateway -run 'TestCopyStream' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/gateway/handlers.go internal/gateway/gateway_test.go
git commit -m "fix(gateway): copyStream 支持输出空闲超时"
```

---

### Task 11: handleMessages 流式超时改造

**Files:**
- Modify: `internal/gateway/handlers.go`（`handleMessages` ctx/timer、提交点、`copyStream` 调用）
- Test: `internal/gateway/gateway_test.go`

**Interfaces:**
- Consumes: `copyStream`（Task 10）
- Produces: 无新导出 API；`rec.ErrorClass = "stream"` 语义

- [ ] **Step 1: 写失败测试**

```go
func TestMessagesStreamLongerThanTimeoutNotCut(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.WriteHeader(200)
		for i := 0; i < 6; i++ {
			w.Write([]byte("event: message_start\ndata: {}\n\n"))
			f.Flush()
			time.Sleep(40 * time.Millisecond)
		}
		w.Write([]byte("event: message_stop\ndata: {}\n\n"))
		f.Flush()
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Limits = func() Limits { return Limits{MaxBodyBytes: 1 << 20, RequestTimeout: 100 * time.Millisecond} }
	})
	body := `{"model":"pub-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`
	resp := post(t, s, body, "tok")
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "message_stop") {
		t.Fatalf("long active stream was cut at total timeout:\n%s", b)
	}
}

func TestMessagesStreamIdleTimeoutEmitsError(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.WriteHeader(200)
		w.Write([]byte("event: message_start\ndata: {}\n\n"))
		f.Flush()
		<-release // 此后不再输出
	}))
	defer up.Close()
	defer close(release)
	s := newGW(t, up.URL, func(d *Deps) {
		d.Limits = func() Limits { return Limits{MaxBodyBytes: 1 << 20, RequestTimeout: 50 * time.Millisecond} }
	})
	body := `{"model":"pub-1","max_tokens":8,"stream":true,"messages":[{"role":"user","content":"q"}]}`
	resp := post(t, s, body, "tok")
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "upstream stream idle timeout") {
		t.Fatalf("missing idle error event:\n%s", b)
	}
	recs := s.deps.Ring.List()
	if len(recs) == 0 || recs[len(recs)-1].ErrorClass != "stream" {
		t.Fatalf("request record must carry error_class=stream: %+v", recs)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -race ./internal/gateway -run 'TestMessagesStream(Longer|Idle)' -v`
Expected: `TestMessagesStreamLongerThanTimeoutNotCut` FAIL（流在 100ms 被切）；`...IdleTimeout...` FAIL（无 error 事件）

- [ ] **Step 3: 最小实现**

`handleMessages` 的 ctx 构造改为：

```go
	ctx := r.Context()
	var cancel context.CancelFunc
	var headerTimer *time.Timer
	if lim.RequestTimeout > 0 {
		if cr.Stream {
			ctx, cancel = context.WithCancel(ctx)
			headerTimer = time.AfterFunc(lim.RequestTimeout, cancel)
		} else {
			ctx, cancel = context.WithTimeout(ctx, lim.RequestTimeout)
		}
		defer cancel()
		defer func() {
			if headerTimer != nil {
				headerTimer.Stop()
			}
		}()
	}
```

提交点（`w.WriteHeader(res.StatusCode)` 前）加：

```go
		if cr.Stream && headerTimer != nil {
			headerTimer.Stop()
		}
```

流式分支改为：

```go
		if cr.Stream {
			onIdle := protocol.AnthropicErrorEvent("upstream stream idle timeout")
			if copyStream(w, res.Body, lim.RequestTimeout, onIdle) {
				rec.ErrorClass = "stream"
			}
		} else {
			io.Copy(w, res.Body)
		}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test -race ./internal/gateway -run 'TestMessages' -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/gateway/handlers.go internal/gateway/gateway_test.go
git commit -m "fix(gateway): 流式请求改用空闲超时"
```

---

### Task 12: handleChatCompletions 流式超时改造

**Files:**
- Modify: `internal/gateway/handlers.go`（`handleChatCompletions` ctx/timer、两个流式分支）
- Test: `internal/gateway/gateway_test.go`

**Interfaces:**
- Consumes: `copyStream`（Task 10）
- Produces: 无新导出 API

- [ ] **Step 1: 写失败测试**

```go
func TestOpenAIInboundStreamIdleTimeoutEmitsError(t *testing.T) {
	release := make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		w.WriteHeader(200)
		w.Write([]byte(`data: {"id":"c","choices":[{"delta":{"content":"x"}}]}` + "\n\n"))
		f.Flush()
		<-release
	}))
	defer up.Close()
	defer close(release)
	s := newGW(t, up.URL, func(d *Deps) {
		d.Limits = func() Limits { return Limits{MaxBodyBytes: 1 << 20, RequestTimeout: 50 * time.Millisecond} }
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1",
				ChannelName: "main", SecretKey: "account:k1", Protocol: "openai",
				BaseURL: up.URL, AllowPrivate: true, UpstreamModel: "up-1", Priority: 10}}, nil
		}
	})
	body := `{"model":"pub-1","stream":true,"messages":[{"role":"user","content":"q"}]}`
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	out := w.Body.String()
	if !strings.Contains(out, "upstream stream idle timeout") {
		t.Fatalf("missing idle error chunk:\n%s", out)
	}
	recs := s.deps.Ring.List()
	if len(recs) == 0 || recs[len(recs)-1].ErrorClass != "stream" {
		t.Fatalf("request record must carry error_class=stream: %+v", recs)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test -race ./internal/gateway -run TestOpenAIInboundStreamIdleTimeoutEmitsError -v`
Expected: FAIL — 无 error chunk（沿用总超时）

- [ ] **Step 3: 最小实现**

`handleChatCompletions` 的 ctx 构造与 `handleMessages` 同型（`oar.Stream` 分支、`headerTimer`）。注意该函数当前在 `if lim := s.deps.Limits(); lim.RequestTimeout > 0 {` 中短声明 `lim`，需先提为函数级变量 `lim := s.deps.Limits()`（闭包内要引用它），再构造 ctx。提交点停止 timer。

两个流式分支接线：

```go
		case c.Protocol == "openai":
			if oar.Stream {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(res.StatusCode)
				onIdle := protocol.OpenAIStreamErrorChunk("upstream stream idle timeout")
				if copyStream(w, res.Body, lim.RequestTimeout, onIdle) {
					rec.ErrorClass = "stream"
				}
			} else {
				/* 保持原样 */
			}
```

```go
		case oar.Stream:
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(res.StatusCode)
			onIdle := protocol.OpenAIStreamErrorChunk("upstream stream idle timeout")
			if copyStream(w, protocol.AnthropicStreamToOpenAIStream(ctx, res.Body, oar.Model), lim.RequestTimeout, onIdle) {
				rec.ErrorClass = "stream"
			}
			res.Body.Close()
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test -race ./internal/gateway ./internal/protocol ./internal/provider -v`
Expected: PASS（全量）

- [ ] **Step 5: 提交**

```bash
git add internal/gateway/handlers.go internal/gateway/gateway_test.go
git commit -m "fix(gateway): OpenAI 入站流式改用空闲超时"
```

---

### Task 13: 文档同步与全量验证

**Files:**
- Modify: `CLAUDE.md`（超时语义、流式截断行为）
- Modify: `docs/superpowers/specs/2026-10-22-stream-truncation-idle-timeout-design.md`（状态改为已实现）

- [ ] **Step 1: 更新 CLAUDE.md**

在「路由与故障转移不变量」小节追加一条：

```markdown
- 流式响应的 `RequestTimeoutSeconds` 语义（2026-10-22）：非流式=整次请求总时限；流式=等待上游响应头的时限 + 客户端可见输出的空闲时限。上游流中断/空闲超时必须向客户端发 error 事件，残缺工具参数不得以空 input 下发；截断后不写 `message_stop`/`[DONE]`。设计见 `docs/superpowers/specs/2026-10-22-stream-truncation-idle-timeout-design.md`。
```

- [ ] **Step 2: 更新 spec 状态行**

```markdown
- 状态：已实现（2026-10-22）
```

- [ ] **Step 3: 全量验证**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: 全部 PASS

- [ ] **Step 4: 提交**

```bash
git add CLAUDE.md docs/superpowers/specs/2026-10-22-stream-truncation-idle-timeout-design.md
git commit -m "docs: 记录流式截断与空闲超时语义"
```

---

## Self-Review

**Spec coverage：**

| Spec 要求 | 任务 |
|---|---|
| 终止标记跟踪（四条路径） | Task 1、5、7 |
| 参数残缺不发空块 | Task 2 |
| `Abort` 不写 `message_stop` | Task 3 |
| OpenAI adapter 区分 EOF/取消/错误 | Task 4 |
| Anthropic→OpenAI 截断语义 | Task 5、6 |
| 两条直通路径截断检测 | Task 7、8、9 |
| 空闲 watchdog | Task 10 |
| 两种入站超时改造 | Task 11、12 |
| 遥测 `error_class="stream"` | Task 11、12 |
| 文档同步 | Task 13 |

**类型一致性：** `SawTerminal()`、`Abort(msg string) []byte`、`DroppedTools() int`、`WrapPassthroughStream(ctx, src, onTruncate, terminals...)`、`copyStream(w, src, idle, onIdle) bool`、`AnthropicStreamToOpenAIStream(ctx, r, model) io.ReadCloser` 在各任务中的签名一致。

**已知取舍：** `Finish()` 的兜底收口方法保留（既有方法级测试 `TestStreamTransformerTruncatedFinish` 不变），但 adapter 泵只在 `SawTerminal()` 为真时调用它——行为变更由 Task 4 的 adapter 测试锁定。
