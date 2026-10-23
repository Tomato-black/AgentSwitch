# OpenAI Responses 入站（Codex CLI 0.143+）设计

- 日期：2026-10-23
- 状态：待实现
- 相关：docx §6.3（首字节后不重放、取消即停）、§7（冷却/二次校验）、§8（安全默认）、§13（测试边界）；spec `2026-10-22-stream-truncation-idle-timeout-design.md`（流式收口语义）
- 参考实现：cc-switch `src-tauri/src/proxy/providers/`（`transform_codex_chat.rs`、`streaming_codex_chat.rs`、`transform_codex_anthropic.rs`、`streaming_codex_anthropic.rs`、`codex_responses_sse.rs`，本地副本 `/home/zhangcao/下载/tmp/cc-switch-main`）

## 1. 背景与根因

Codex CLI 0.143.0 起彻底移除 Chat Completions 线路协议（`wire_api = "chat"`）。启动即报错：

```
Error loading config.toml: `wire_api = "chat"` is no longer supported.
How to fix: set `wire_api = "responses"` in your provider config.
```

AgentSwitch 的两个位置因此失效：

1. `internal/bridge/accessconfigs.go` 的 `writeCodexLive` 固定写入 `wire_api = "chat"`；
2. 网关只注册 `POST /v1/messages`（Anthropic）与 `POST /v1/chat/completions`（OpenAI Chat），**没有 `/v1/responses` 入站**。

只把配置改成 `responses` 会让 Codex 打到不存在的端点，因此必须为网关新增 Responses 入站。

### 1.1 探针证据（2026-10-23，Codex 0.143.0）

用临时 `CODEX_HOME` 将 Codex 指向本地抓包服务器，实际抓到的请求：

- `store: false`；不发送 `previous_response_id`（含显式 `disable_response_storage = true` 与不设置两种情况）。
- 字段：`model`、`instructions`（约 2 万字符 system 提示）、`input`（数组）、`tools`、`tool_choice: "auto"`、`parallel_tool_calls`、`stream: true`、`prompt_cache_key`、`client_metadata`；支持推理的模型额外发送 `reasoning: {"effort":"high"}` 与 `include: ["reasoning.encrypted_content"]`。
- `input` 项类型：`message`（role `developer`/`user`/`assistant`，content 为 `input_text`/`output_text` 分片）、`function_call`、`function_call_output`。
- 工具类型：`function`（exec_command 等）、`custom`（`apply_patch`，自由文本 grammar）、`namespace`（`multi_agent_v1`，内含子 function）、`tool_search`、`web_search`。
- 鉴权为 `Authorization: Bearer <OPENAI_API_KEY>`，与网关令牌机制兼容。
- Codex 接受不含 reasoning item 的响应；工具调用往返正常（探针中 Codex 实际执行了工具）。

## 2. 目标与非目标

### 目标

1. 网关新增 `POST /v1/responses` 入站，Codex CLI 0.143+ 可真实接入并完成「对话 + 工具调用」往返。
2. 入站转换复用既有逻辑模型/候选/冷却/故障转移/超时/遥测核心，语义与现有两入站一致。
3. 工具映射与 cc-switch 对齐：`function`、`custom`、`namespace`、`tool_search` 四类；`web_search` 丢弃（托管工具，chat/Anthropic 上游无法兑现）。
4. 接入配置写入 `wire_api = "responses"`（仅用户重新「应用」时生效）。
5. 流式截断、上游错误、空闲超时向 Codex 发 `response.failed`，绝不发 `response.completed`。

### 非目标

- 不新增 `openai-responses` 上游渠道协议；渠道协议仍为 `anthropic` / `openai`（chat）。
- 不实现 `previous_response_id` 与服务端会话存储（实测 Codex 对自定义 provider 恒 `store:false`）。
- 不回传 `reasoning.encrypted_content`（不生成 encrypted reasoning；`include` 忽略）。
- 不做 `inline  thinking` 拆分、工具媒体抽取、compaction 回合、各供应商 schema 特例（cc-switch 的这些分支超出本次范围）。
- 不实现 `web_search` 托管工具。

## 3. 设计

### 3.1 分层与入口

| 层 | 新增/改动 | 职责 |
|---|---|---|
| `gateway` | 新增 `internal/gateway/responses.go`，`server.go` 注册路由 | 请求检查、候选解析、failover、超时、遥测；按候选协议选择出站与响应回程 |
| `protocol` | 新增 `internal/protocol/responses.go`、`responses_stream.go`（名称以实现计划为准） | Responses ⇄ chat.completions 的请求/响应/流转换；工具上下文 |
| `bridge` | `accessconfigs.go` 写 `wire_api = "responses"` | 接入配置投影 |
| 文档 | README、CLAUDE.md | 入站与配置说明 |

依赖方向不变：`protocol` 不依赖 `provider`/`gateway`；`gateway` 组合两者。

`handleResponses` 与 `handleChatCompletions` 同构，复用 `gate`、`failover`、`copyStream`、`writeErrOpenAI` 与现有超时逻辑；错误 envelope 使用 OpenAI 形状。

### 3.2 请求解析与转换（Responses → chat.completions）

#### 3.2.1 顶层字段

| Responses 字段 | 处理 |
|---|---|
| `model` | 路由主体；匹配前剥离尾部 `[1M]`（同现有入站） |
| `stream` | 决定出站与响应形态 |
| `instructions` | 首个 system 消息 |
| `input` | string 或 item 数组，见 §3.2.2 |
| `tools` | 工具上下文，见 §3.2.3 |
| `tool_choice` | 映射后透传，见 §3.2.4 |
| `max_output_tokens` | → chat `max_tokens` |
| `temperature` / `top_p` | 透传 |
| `reasoning` / `include` / `store` / `prompt_cache_key` / `client_metadata` / `parallel_tool_calls` | 忽略（不下发上游） |
| `previous_response_id` | 显式 400：`previous_response_id is not supported` |
| 其他未知字段 | 忽略（前向兼容） |

#### 3.2.2 `input` 项映射

| Responses item | chat 消息 |
|---|---|
| 字符串 | `{role: "user", content: <text>}` |
| `message`（`developer`/`system`） | system 消息；全部合并到消息列表头部（instructions 在前），拼接文本 |
| `message`（`user`/`assistant`） | 对应角色；content 数组取 `input_text`/`output_text`/`text` 拼接；非文本分片丢弃（媒体不在本次范围） |
| `function_call` | 进入待合并的 assistant `tool_calls`：`{id: call_id, type: "function", function: {name: <chat 名>, arguments}}`；名字按工具上下文还原（namespace 拍平） |
| `function_call_output` | `{role: "tool", tool_call_id: call_id, content: <output 文本>}`；`output` 为数组/对象时序列化为 JSON 文本 |
| `custom_tool_call` | assistant `tool_calls`，`arguments = {"input": <input>}` |
| `custom_tool_call_output` | 同 `function_call_output` |
| `tool_search_call` | assistant `tool_calls`，`name = "tool_search"`，`arguments = <arguments 对象序列化>` |
| `tool_search_output` | 同 `function_call_output`；其 `tools` 字段注册进本次工具上下文（见 §3.2.3） |
| `reasoning` | 忽略 |
| `item_reference` | 400：无法解析引用 |
| 其他未知类型 | 忽略 |

连续 `function_call` 等调用项合并为同一条 assistant 消息的多个 `tool_calls`；在遇到非调用项或回合结束时 flush。**孤儿工具输出**（找不到匹配 call_id）降级为 user 文本消息 `[tool_result without a matching tool_call] ...`，避免上游 400。

#### 3.2.3 工具上下文与四类工具映射

每个请求构建 `ToolContext`：`chat 名 → {kind, 原名, namespace}`，同时产出下发给 chat 上游的 `tools`：

| Responses 工具 | chat 工具 | 回程 item |
|---|---|---|
| `function` | 原名直转；`parameters` 缺失或非 object 时归一化为 `{"type":"object","properties":{}}` | `function_call` |
| `namespace`（含子 `function`） | 子函数拍平为 `<namespace>__<name>`；超过 64 字符时截断并加短哈希后缀 | `function_call`，附带 `namespace` 字段 |
| `custom`（如 `apply_patch`） | 伪函数：`{name, description: 原始定义 JSON 嵌入, parameters: {input: string}}`，`required: ["input"]` | `custom_tool_call`，`input` 为解包后的字符串 |
| `tool_search` | 伪函数 `tool_search`，参数 `{query: string, limit?: integer}`，`required: ["query"]` | `tool_search_call`，`arguments` 为对象，`execution: "client"` |
| `web_search` | 丢弃并计数 | 不会产生 |

`input` 中 `tool_search_output` / `additional_tools` 携带的 `tools` 定义（含 namespace 子工具）注册进 `ToolContext` 并加入下发工具列表——Codex 动态加载的多智能体工具因此可被调用并正确还原。

重名冲突时保留首个并计数告警；不猜测改名，避免回程歧义。

#### 3.2.4 `tool_choice`

| Responses | chat |
|---|---|
| `"auto"` / `"none"` / `"required"` | 原样 |
| `{type:"function", name, namespace?}` | `{type:"function", function:{name: <chat 名>}}` |
| `{type:"custom", name}` | 同名伪函数 |
| `{type:"tool_search"}` | `tool_search` |

### 3.3 上游出站矩阵

| 候选渠道协议 | 请求 | 响应回程 |
|---|---|---|
| `openai`（chat） | Responses → chat 请求后经 `openaiPassthrough` 直发 `/v1/chat/completions` | chat JSON/SSE → Responses JSON/SSE |
| `anthropic` | Responses → chat → `protocol.OpenAIRequestToAnthropic`，经 Anthropic adapter 发 `/v1/messages` | Anthropic SSE →（既有 `AnthropicStreamToOpenAIStream`）→ chat SSE → Responses SSE；非流式同理经 `AnthropicResponseToOpenAI` 中转一次 |

超时、冷却、二次校验、切换与提交点语义完全复用 `handleChatCompletions` 实现：

- 首字节（响应头）提交后绝不换候选、绝不重放；
- 客户端取消即取消上游，零状态副作用；
- 上游 400 类不冷却不转移，原样回传；
- 全部仅因冷却不可用 → 503，否则 502。

已知限制：Anthropic 流式转换器当前不输出 usage，因此 anthropic 渠道的 Responses 事件中 usage 为零；chat 渠道正常。记录为后续改进项，不影响 Codex 使用。

### 3.4 响应转换（chat → Responses）

#### 3.4.1 非流式

`choices[0].message` 映射为 `output` 数组：

- `content` 非空 → `message` item（`content: [{type:"output_text", text, annotations:[]}]`）；
- `reasoning_content` / `reasoning` 文本 → `reasoning` item（`summary: [{type:"summary_text", text}]`，无 encrypted content）；
- `tool_calls` 按工具上下文还原为 `function_call`（可带 `namespace`）、`custom_tool_call`（解包 `input`）、`tool_search_call`（`arguments` 解析为对象）；
- `usage` 映射见 §3.4.3；`finish_reason = "length"` → `status: "incomplete"` + `incomplete_details.reason = "max_output_tokens"`，其余 → `completed`。

响应对象：

```json
{
  "id": "resp_<chat id 或随机>",
  "object": "response",
  "created_at": 0,
  "status": "completed",
  "model": "<请求的逻辑模型名>",
  "output": [],
  "usage": {}
}
```

#### 3.4.2 流式状态机

输入为 chat `chat.completion.chunk` SSE（含直通上游与「Anthropic→chat」泵出的 chunk），输出为 Responses SSE。事件顺序：

| 时机 | 事件 |
|---|---|
| 首个数据 chunk 前 | `response.created`、`response.in_progress`（`status: "in_progress"`，`output: []`） |
| 首个正文增量 | `response.output_item.added`（message, `status: in_progress`）→ `response.content_part.added` → 每个增量一条 `response.output_text.delta` |
| 首个 reasoning 增量 | `response.output_item.added`（reasoning）→ `response.reasoning_summary_part.added` → `response.reasoning_summary_text.delta`；正文开始或回合结束时关闭 reasoning item |
| 工具调用首次出现 | `response.output_item.added`：`function_call`（`status: in_progress`，空 `arguments`）/ `custom_tool_call` / `tool_search_call` |
| 函数参数增量 | `response.function_call_arguments.delta`（custom / tool_search 不逐片下发，收口时给最终值） |
| `finish_reason` 到达 | 关闭正文/推理 item（`output_text.done` → `content_part.done` → `output_item.done`）；按 index 收口工具调用（`function_call_arguments.done` / `custom_tool_call_input.done` → `output_item.done`）；随后 `response.completed`（含完整 `output` 与 usage） |
| 上游 error chunk / 流被截断 / 空闲超时 | `response.failed`（`status: "failed"`，`error: {message, type?}`，`output` 为已完成的 item）；**不发** `response.completed` |

item 形状与 Codex 探针/cc-switch 一致：

```json
{"id":"fc_xxx","type":"function_call","status":"completed","call_id":"call_x","name":"exec_command","arguments":"{...}","namespace":"multi_agent_v1"}
{"type":"custom_tool_call","status":"completed","call_id":"call_x","name":"apply_patch","input":"*** Begin Patch..."}
{"type":"tool_search_call","status":"completed","call_id":"call_x","execution":"client","arguments":{"query":"..."}}
```

`response.completed` 的 `response` 携带按 `output_index` 排序的完整 `output`、`usage`、`status`、`model`（逻辑模型名，不泄露上游名）、`id`、`created_at`。

空闲超时由 `copyStream` 现有 watchdog 触发，`onIdle` 为一条 `response.failed` 事件；截断判据沿用「未见 `finish_reason`/`[DONE]` 即截断」。

#### 3.4.3 usage 映射

| chat | Responses |
|---|---|
| `prompt_tokens` | `input_tokens` |
| `completion_tokens` | `output_tokens` |
| `total_tokens`（缺省 = input + output） | `total_tokens` |
| `prompt_tokens_details.cached_tokens` | `input_tokens_details.cached_tokens` |
| `completion_tokens_details.reasoning_tokens` | `output_tokens_details.reasoning_tokens` |

缺失时全部为 0。

### 3.5 接入配置

`writeCodexLive` 的受管键改为：

```toml
model = "<逻辑模型名>"
model_provider = "agentswitch"

[model_providers.agentswitch]
base_url = "<OPENAI_BASE_URL>"
wire_api = "responses"
requires_openai_auth = false
```

- 只修改受管键，其余键保留；写前备份语义不变。
- 老配置迁移：**仅当用户重新「应用」接入配置（或保存/令牌/端口联动）时改写**；不新增启动自愈、不做启动时写入用户文件。
- README 与 CLAUDE.md 同步说明「Codex 需 0.143+ 的 responses 线路；旧配置需重新应用一次」。

### 3.6 错误与遥测

- 提交前错误：HTTP 状态码 + OpenAI error envelope（`{"error":{...}}`），状态码映射沿用现有规则。
- 提交后错误：`response.failed` 事件；遥测 `error_class = "stream"`，记录 WARN（含 `request_id`）。
- 遥测/日志字段与现有入站一致（模型、上游模型、渠道、状态、时长、切换次数、是否流式）；不记录请求体、令牌、完整 Prompt。
- 上游 400 类不冷却不转移；429/5xx/连接错误按现有 `provider.Classify` 冷却并切换。

### 3.7 安全

- 与现有入站相同的常量时间令牌比较、并发上限、body 上限、SSRF/私网校验（复用 `openaiPassthrough` 与 Anthropic adapter）。
- 不新增任何落盘内容；Responses 请求中的 `client_metadata` 等字段不下发上游、不落日志。

## 4. 测试计划

### 4.1 协议层单测（离线）

以真实 Codex 请求裁剪为 fixture（去掉 2 万字符 instructions 正文）：

1. 请求转换：instructions + developer/user 消息、function_call/function_call_output 历史、tool_calls 合并、孤儿输出降级、`previous_response_id` 400。
2. 工具上下文：function / custom(`apply_patch`) / namespace 拍平与回程 `namespace` 字段 / tool_search 伪函数 / `tool_search_output` 动态注册工具 / `web_search` 丢弃 / 重名冲突。
3. 非流式响应：文本、工具调用三类、usage、`length` → incomplete。
4. 流式状态机：文本事件序列；工具调用参数跨 chunk 分片；custom 输入收口；reasoning 事件；`[DONE]` 正常收口；无 `finish_reason` 截断 → `response.failed`；error chunk → `response.failed`。
5. `tool_choice` 映射四种形态。

### 4.2 网关测试（Fake Provider，`-race`）

1. `openai` 渠道：Responses 请求 → 上游收到 chat 请求（工具已转换）→ 客户端收到 Responses SSE，事件顺序与 usage 正确。
2. `anthropic` 渠道：上游收到 `/v1/messages`，工具块/文本正确回转。
3. 故障转移：首个候选 429 → 冷却 → 第二候选成功；客户端只看到一份 `response.created`（无重复/重放）。
4. 首字节后上游截断 → `response.failed`，无 `response.completed`。
5. 空闲超时 → `response.failed`，遥测 `error_class=stream`。
6. 未知模型 404、冷却耗尽 503 的 OpenAI 形状错误体。
7. 并发 `-race`：冷却状态与流式路径无数据竞争。

### 4.3 接入配置

`accessconfigs_test.go` 断言 `wire_api == "responses"` 且其余受管键不变；备份与「一败俱不写」语义回归。

### 4.4 端到端（受控，默认跳过）

用真实 `codex` 二进制 + 临时 `CODEX_HOME` 对测试网关执行 `codex exec`：

- 第一轮模型返回 `exec_command` 调用 → Codex 执行 → 第二轮模型返回正常文本收尾；
- 断言往返轮数 ≥ 2 且退出码 0。
`apply_patch`（custom 工具）需要模型目录元数据才会被 Codex 下发，端到端不覆盖，由协议层单测覆盖；无 `codex` 可执行文件时跳过，不在常规 CI 依赖真实供应商。

## 5. 兼容性

- 现有 `/v1/messages` 与 `/v1/chat/completions` 入站行为不变；chat 入站继续服务其他客户端。
- 渠道协议不变，现有渠道无需迁移。
- Codex 用户升级 AgentSwitch 后需重新「应用」一次接入配置（`wire_api` 由 `chat` 变为 `responses`）。

## 6. 后续项（不在本次范围）

- 原生 `openai-responses` 上游渠道协议（直通，需同时定义它与 Anthropic 入站的转换矩阵）。
- `previous_response_id` 的内存会话存储。
- reasoning encrypted content 回传与 reasoning 连续性。
- Anthropic 流式转换器补齐 usage。
- inline think、媒体抽取、compaction、供应商 schema 特例。
