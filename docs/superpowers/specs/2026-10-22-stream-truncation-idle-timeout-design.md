# 流式截断显式化与空闲超时设计

- 日期：2026-10-22
- 状态：已批准（用户确认设计方向与实现范围）
- 相关：docx §6.3（首字节后不重放、取消即停）、§8（超时/限流）、§13（上游中断测试边界）

## 1. 背景与根因

2026-10-22，用户在 Claude Code 中写大计划文件时连续三次出现 `Error writing file`。排查证据：

1. 网关日志 `~/.config/AgentSwitch/logs/app.log` 在失败窗口（15:27–15:35）内每 2 分钟出现一个**恰好 120 秒**的请求，与「写计划失败 → 重试」的节奏一一对应。
2. `internal/config/config.go` 中 `RequestTimeoutSeconds` 默认 120；`internal/gateway/handlers.go` 对流式与非流式请求一律施加 `context.WithTimeout(ctx, RequestTimeout)`，覆盖整个流式响应。
3. 流被切断后，`internal/provider/openai.go` 的泵 goroutine 对**任何**读错误（含 ctx 超时、连接中断）都调用 `t.Finish()`，输出 `message_stop`，把截断伪装成正常结束。
4. `internal/protocol/openai.go` 的 `emitTools` 对不完整的工具参数 JSON 解析失败时忽略错误，降级为 `input: {}` 的 `tool_use` 块。

复现（截断一半的 `Write` 参数）：

```
content_block_start: {"content_block":{"id":"call_1","input":{},"name":"Write","type":"tool_use"},...}
content_block_delta: {"delta":{"partial_json":"{}","type":"input_json_delta"},...}
message_delta:       {"delta":{"stop_reason":"end_turn",...}}
message_stop
```

客户端因此执行参数为空的 `Write` 工具，报 `Error writing file`；网关日志仍记录 `status: 200, error_class: ""`。

**结论**：不是小米上游的流式格式不符合 Anthropic 规范（标准 OpenAI chunk 的转换本身正确），而是网关总超时切断流、截断未被识别、残缺参数被静默降级。

同类缺陷存在于另外三条流式路径（见 §4）。

## 2. 目标与非目标

### 目标

1. 任何上游流中断必须让客户端收到明确的 error 事件，绝不伪装成正常结束。
2. 残缺的工具参数不得以空 `input` 的 `tool_use`/`tool_calls` 下发。
3. 持续有输出的长任务不受总时长限制；上游连续静止超过阈值才切断。
4. 覆盖「Anthropic / OpenAI 两种入站 × Anthropic / OpenAI 两种上游协议」共四条流式路径。
5. 保持 docx §6.3 不变量：首字节已写出后绝不静默重放；客户端取消立即取消上游且零状态副作用。

### 非目标

- 不改变非流式请求的超时语义与错误处理。
- 不新增设置项或 UI（复用 `RequestTimeoutSeconds`，见 §3.1）。
- 不在首字节后做任何故障转移或重放。
- 不实现上游 SSE 心跳的保活/重连。

## 3. 设计

### 3.1 超时语义（复用 `RequestTimeoutSeconds`）

| 请求形态 | `RequestTimeoutSeconds` 的含义 |
|---|---|
| 非流式 | 整次请求（含上游读体）总时限；超时行为不变 |
| 流式 | ① 等待上游响应头的时限；② 客户端可见 SSE 输出的空闲时限（两者同值） |

- 流式请求不再对整次请求施加 `context.WithTimeout`。handler 使用 `context.WithCancel(r.Context())`，并以 `time.AfterFunc(RequestTimeout, cancel)` 约束响应头等待；拿到 2xx 后停止该计时器。
- 首字节后的控制权交给空闲 watchdog（§3.4）。
- 语义变化记录在本 spec 与 CLAUDE.md，不新增配置字段。

### 3.2 分层职责

| 层 | 职责 |
|---|---|
| `gateway` | 超时语义（§3.1）；流式复制时的空闲 watchdog；空闲超时后按**入站协议**写 error 事件；遥测标记 |
| `provider` | 读取上游时区分 **EOF / 客户端取消 / 非 EOF 错误**；非 EOF 错误不再调用 `Finish()`；Anthropic 直通流的截断检测 |
| `protocol` | 转换器新增 `Abort`：丢弃未完成工具块、关闭已开 text 块、写 error 事件，**不写** `message_stop` / `[DONE]`；工具参数解析失败时**不发**块 |

依赖方向不变：`protocol` 不依赖 `provider`/`gateway`；`provider` 依赖 `protocol`；`gateway` 组合两者。

### 3.3 四条路径的行为矩阵

| # | 入站 → 上游 | 位置 | 正常结束判据 | 截断/读错误处理 |
|---|---|---|---|---|
| 1 | Anthropic → OpenAI | `provider/openai.go` + `protocol/openai.go` | 收到 `finish_reason` 或 `[DONE]` | `Abort("upstream stream interrupted")`：关已开 text 块、丢弃未闭合工具块、发 Anthropic error 事件，不发 `message_stop` |
| 2 | Anthropic → Anthropic（直通） | `provider/anthropic.go` | 见过 `message_stop`（或上游已发 `error`） | 追加 Anthropic error 事件 |
| 3 | OpenAI → Anthropic | `protocol/convert_openai_inbound.go` | 见过 `message_stop` | `Abort`：发 OpenAI error chunk，不写 finish_reason、不写 `[DONE]` |
| 4 | OpenAI → OpenAI（直通） | `gateway/handlers.go` 的 `openaiPassthrough` | 见过 `[DONE]` | 追加 OpenAI error chunk |

通用规则：

- 所有路径（直通与转换器）都需跟踪是否见过正常终止标记（`message_stop` / `[DONE]` / 非空 `finish_reason`）。
- `rerr == io.EOF` 且已见终止标记 → 正常 `Finish()`。
- `rerr == io.EOF` 但**未见**终止标记 → 视为截断，走 `Abort` / 追加 error 事件。
- 非 EOF 读错误（含连接中断、上游取消）→ 走 `Abort` / 追加 error 事件。
- **客户端取消**（`ctx.Err() != nil` 或 request context 已结束）→ 静默关闭，不写 error、不冷却、不重放。

### 3.4 空闲 watchdog（gateway）

- `copyStream` 接收 `io.ReadCloser` 源、空闲阈值与「入站协议」标记。
- 每次成功读到 `n > 0` 时重置 `time.Timer`；计时器到期表示客户端可见输出已空闲超过阈值。
- 到期动作：按入站协议向客户端写 error 事件 → `Flush` → 关闭上游 body → 返回。
- watchdog 监测的是**客户端可见 SSE 输出**的间隔，不是上游原始字节间隔：上游心跳不构成有效输出，四条路径可共用同一实现。
- 客户端主动断开时，`Write` 失败即退出，不写 error。

### 3.5 错误事件形状

Anthropic 入站：

```
event: error
data: {"type":"error","error":{"type":"api_error","message":"..."}}
```

OpenAI 入站：

```
data: {"error":{"message":"...","type":"api_error"}}

```

OpenAI 入站截断后**不发送** `data: [DONE]`。错误消息只含稳定描述（如 `upstream stream interrupted` / `upstream stream idle timeout`），不得包含上游响应体、密钥或 Prompt。

### 3.6 API 变更

- `protocol.OpenAIStreamTransformer`
  - 新增 `Abort(msg string) []byte`：幂等；关闭已开 text 块（`content_block_stop`）、丢弃累积中的工具块、发 `event: error`；不写 `message_delta`/`message_stop`。
  - 新增 `DroppedTools() int`：因参数 JSON 解析失败而丢弃的工具块计数，供 adapter 记录 WARN。
  - `emitTools` 解析失败时**跳过该块**（不发空 `input`）。
- `protocol.anthropicToOpenAIStream`
  - 新增 `Abort(msg string) []byte`：幂等；发 OpenAI error chunk；不写 finish_reason、`[DONE]`。
  - `AnthropicStreamToOpenAIStream` 的泵改为区分 EOF/取消/错误，收到 `ctx` 以识别客户端取消。
- `provider.OpenAIAdapter.streamBody`：接收 `ctx`；仅 EOF 时 `Finish()`；取消时静默；其他错误 `Abort`；`Finish` 后若 `DroppedTools() > 0` 记 WARN。
- `provider.AnthropicAdapter.Do`：流式 2xx 时包装 body，扫描 SSE 终止标记并在截断时追加 error 事件。
- `gateway.openaiPassthrough`：同 Anthropic 直通，扫描 `[DONE]`。
- `gateway.copyStream`：签名扩展为 `(w, src, idle, inboundProtocol, cancel)`；空闲超时按入站协议写 error。

### 3.7 遥测与日志

- 空闲超时：`rec.ErrorClass = "stream"`（HTTP 状态已提交为 200，无法回改）；日志级别 WARN，消息 `stream idle timeout`，字段含 `request_id`、`idle_ms`。
- 上游中断：provider 层 WARN（`upstream stream interrupted`）；转换器丢弃工具块时 WARN（`dropped incomplete tool call`，含计数）。
- 请求日志行格式不变；不新增密钥、请求体或上游原文。

## 4. 测试计划（TDD）

| 层 | 用例 |
|---|---|
| `protocol/openai_test.go` | 截断参数不发空 `input` 的 tool_use；`Abort` 输出 error 事件、无 `message_stop`，text 块已开时先补 `content_block_stop`；正常 `Finish` 行为不变；`DroppedTools` 计数正确 |
| `protocol/convert_openai_inbound_test.go` | 上游 EOF 未见 `message_stop` → error chunk，无 finish_reason、无 `[DONE]`；正常流行为不变；客户端取消静默 |
| `provider/provider_test.go` | OpenAI adapter：上游中途断开 → 客户端流含 error 事件且无 `message_stop`；正常 `[DONE]` 不变；取消静默。Anthropic adapter：中途断开 → 追加 error；正常 `message_stop` 不变 |
| `gateway/gateway_test.go` | 流式持续输出超过 `RequestTimeout` 不被切断；空闲超过阈值 → error 事件 + `error_class="stream"`；非流式超时行为不变；客户端取消不写 error；四条路径各一条截断用例（OpenAI 直通可覆盖 [#4]） |

所有并发相关用例带 `-race`；测试全部离线（Fake Provider）。

## 5. 风险与兼容

- **直通扫描器**：需正确处理 `\n\n` / `\r\n\r\n` 事件边界与跨 chunk 切分；复用 `protocol.findEvent` 的逻辑（必要时导出内部 helper）。
- **pipe 写失败**：客户端或 watchdog 关闭 reader 后，泵 goroutine 的 `pw.Write` 失败必须立即退出并由 `defer src.Close()` 释放上游。
- **空闲阈值与心跳**：合法但极慢的上游（>120s 无任何 chunk）会被切断；这与现有总超时相比并未放宽「卡死」上限，且现在会显式报错。
- **HTTP 状态不可回改**：流已提交后截断只能以 error 事件表达，遥测以 `error_class` 标记，README/spec 中说明。
- **现有行为回归**：正常流（含 tool_use 完整参数）的事件序列必须逐字节/逐事件保持不变（`openai_test.go` 现有 pin 测试保留）。

## 6. 实现任务拆分预览（交 writing-plans 细化）

1. `protocol/openai.go`：`emitTools` 跳过残缺工具块 + `DroppedTools` 计数（含测试）
2. `protocol/openai.go`：`Abort` 方法（含测试）
3. `provider/openai.go`：`streamBody` 区分 EOF/取消/错误并接线 `Abort`（含测试）
4. `protocol/convert_openai_inbound.go`：`Abort` + 泵区分错误/取消（含测试）
5. `provider/anthropic.go`：直通流截断检测包装（含测试）
6. `gateway/handlers.go`：`openaiPassthrough` 截断检测（含测试）
7. `gateway/handlers.go`：`handleMessages` 流式超时改为响应头计时 + 空闲 watchdog（含测试）
8. `gateway/handlers.go`：`handleChatCompletions` 同上（含测试）
9. 遥测字段与 CLAUDE.md 文档同步（含测试）

每个任务保持「先失败测试、再最小实现、再通过」的 TDD 节奏，可独立验证。
