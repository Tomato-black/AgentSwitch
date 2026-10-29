# P1-2b 流式 token 统计实施记录

## 方案

在客户端 SSE 写入侧统一扫描 `usage`，覆盖四路流式路径：

- Anthropic `message_start`（`message.usage.input_tokens`）与 `message_delta`（`usage.output_tokens`）
- OpenAI Chat 尾块 `usage.prompt_tokens/completion_tokens`
- Responses `response.completed.usage`

OpenAI Chat 上游请求自动注入 `stream_options.include_usage = true`，确保流末尾有 usage 尾块。

## 变更

- `internal/protocol/stream_usage.go`
  - `StreamUsageTracker`：按行扫描 SSE `data:` JSON，支持跨 Write 分片与 EOF 残余
  - `EnableChatStreamUsage`：给 OpenAI Chat 请求注入 include_usage
- `internal/gateway/handlers_common.go`
  - `usageWriter`：写客户端的同时喂给 tracker，保留 Flush
- 四路流式分支接入（messages/chat-openai/chat-anthropic/responses）
- OpenAI Chat/Responses 的 openai 上游请求注入 include_usage

## 测试

- `TestStreamUsageTrackerAnthropic`：跨分片的 message_start/message_delta
- `TestStreamUsageTrackerOpenAIAndResponses`：OpenAI 尾块与 Responses completed
- `TestEnableChatStreamUsage`
- 网关流式：messages 流 usage 落记录；OpenAI 流注入 include_usage 并落记录
- 全量 `go build ./...`、`go vet ./...`、`go test ./...` 通过
- `go test -race ./internal/gateway ./internal/protocol ./internal/app` 通过

## 已知限制

- 上游不返回 usage（部分兼容端点）时仍为 0；UI 只能显示已知部分。
- 扫描的是客户端可见 SSE；若客户端提前断开，已收到的 usage 仍会记录。
