# OpenAI Responses 入站（Codex CLI 0.143+）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 为网关新增 `POST /v1/responses` 入站，使 Codex CLI 0.143+ 能通过 AgentSwitch 完成对话与工具调用，并把 Codex 接入配置切到 `wire_api = "responses"`。

**Architecture:** 新增一个与 `/v1/chat/completions` 同构的网关 handler；协议层先把 Responses 请求转换成 chat.completions 请求，再复用现有「openai 直通 / anthropic 转换」出站路径；响应方向把 chat JSON/SSE 转换成 Responses JSON/SSE。工具上下文负责 function/custom/namespace/tool_search 四类工具的双向名称与形状映射；web_search 丢弃。

**Tech Stack:** Go 1.27.2、`net/http`、`encoding/json`、`log/slog`、`github.com/pelletier/go-toml/v2`；测试全部离线（httptest Fake Provider）。

**Spec:** `docs/superpowers/specs/2026-10-23-openai-responses-inbound-design.md`

## Global Constraints

- 渠道协议保持 `anthropic` / `openai`（chat）两种，不新增第三种上游协议。
- 首字节（响应头）提交后绝不换候选、绝不重放；客户端取消即取消上游且零状态副作用。
- 截断/上游错误/空闲超时对 Codex 一律发 `response.failed`，绝不发 `response.completed`。
- 未知模型 404、冷却耗尽 503、上游 400 不冷却不转移，语义与 `handleChatCompletions` 完全一致。
- 日志/遥测禁止出现令牌、Authorization、完整 Prompt、完整请求体。
- 工具映射以 cc-switch `src-tauri/src/proxy/providers/` 为参考实现（本地副本 `/home/zhangcao/下载/tmp/cc-switch-main`）。
- 每个任务结束后运行对应测试并提交；提交信息末尾附 `Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>`。
- 当前分支为 `master`；开始前先建分支：`git switch -c feat/responses-inbound`。

> 粒度说明（CLAUDE.md 允许的例外）：任务按「单一可独立验证的业务行为」切分。协议转换类任务（10-13、15-16）代码量天然偏大（事件状态机与双协议接线），实际编码可能超过 5 分钟；不得再把一个任务拆成无独立验证点的碎片。其余任务保持在 2-5 分钟量级。

## 文件结构

| 文件 | 职责 |
|---|---|
| `internal/protocol/responses.go`（新建） | Responses 请求解析、input item 解析、Responses→chat 请求组装 |
| `internal/protocol/responses_tools.go`（新建） | 工具上下文：四类工具注册与双向名称映射 |
| `internal/protocol/responses_response.go`（新建） | chat 非流式响应 → Responses JSON |
| `internal/protocol/responses_stream.go`（新建） | chat SSE → Responses SSE 状态机 |
| `internal/gateway/responses.go`（新建） | `/v1/responses` handler（复用 gate/failover/copyStream） |
| `internal/gateway/server.go`（修改） | 注册路由 |
| `internal/bridge/accessconfigs.go`（修改） | `wire_api = "responses"` |
| `README.md` / `CLAUDE.md`（修改） | 入站与接入说明 |

---

### Task 0: 建分支

- [ ] **Step 1: 创建功能分支**

```bash
git switch -c feat/responses-inbound
```

- [ ] **Step 2: 确认工作区干净**

Run: `git status --short`
Expected: 无输出（spec 文档已提交或保留也未纳入本任务）。

---

### Task 1: 解析 Responses 请求顶层字段

**Files:**
- Create: `internal/protocol/responses.go`
- Test: `internal/protocol/responses_test.go`

**Interfaces:**
- Produces:
  - `type ResponsesRequest struct { Model string; Stream bool; Instructions string; Input json.RawMessage; Tools []json.RawMessage; ToolChoice json.RawMessage; MaxOutputTokens *int; Temperature *float64; TopP *float64; PreviousResponseID json.RawMessage }`
  - `func ParseResponsesRequest(body []byte) (ResponsesRequest, error)`

- [ ] **Step 1: 写失败测试**

```go
package protocol

import (
	"strings"
	"testing"
)

func TestParseResponsesRequestCodexShape(t *testing.T) {
	body := `{"model":"my-code","stream":true,"instructions":"be brief",
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}],
		"tool_choice":"auto","max_output_tokens":256,"temperature":0.2}`
	r, err := ParseResponsesRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if r.Model != "my-code" || !r.Stream || r.Instructions != "be brief" {
		t.Fatalf("top-level fields: %+v", r)
	}
	if len(r.Tools) != 1 || r.MaxOutputTokens == nil || *r.MaxOutputTokens != 256 {
		t.Fatalf("tools/limits: %+v", r)
	}
	if r.Temperature == nil || *r.Temperature != 0.2 {
		t.Fatalf("temperature: %+v", r)
	}
}

func TestParseResponsesRequestRejectsMissingModel(t *testing.T) {
	_, err := ParseResponsesRequest([]byte(`{"input":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "model") {
		t.Fatalf("want model error, got %v", err)
	}
}

func TestParseResponsesRequestRejectsPreviousResponseID(t *testing.T) {
	_, err := ParseResponsesRequest([]byte(`{"model":"m","input":"hi","previous_response_id":"resp_1"}`))
	if err == nil || !strings.Contains(err.Error(), "previous_response_id") {
		t.Fatalf("want previous_response_id error, got %v", err)
	}
}

func TestParseResponsesRequestAllowsNullPreviousResponseID(t *testing.T) {
	if _, err := ParseResponsesRequest([]byte(`{"model":"m","input":"hi","previous_response_id":null}`)); err != nil {
		t.Fatalf("null previous_response_id must be accepted: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestParseResponsesRequest -v`
Expected: 编译失败（`ParseResponsesRequest` 未定义）。

- [ ] **Step 3: 实现**

```go
package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ResponsesRequest 是 /v1/responses 请求的解析视图。只保留路由与转换需要的
// 字段；store/client_metadata/prompt_cache_key 等字段忽略（不下发上游）。
type ResponsesRequest struct {
	Model              string            `json:"model"`
	Stream             bool              `json:"stream"`
	Instructions       string            `json:"instructions"`
	Input              json.RawMessage   `json:"input"`
	Tools              []json.RawMessage `json:"tools"`
	ToolChoice         json.RawMessage   `json:"tool_choice"`
	MaxOutputTokens    *int              `json:"max_output_tokens"`
	Temperature        *float64          `json:"temperature"`
	TopP               *float64          `json:"top_p"`
	PreviousResponseID json.RawMessage   `json:"previous_response_id"`
}

// ParseResponsesRequest 解析并校验 Responses 请求。previous_response_id 需要
// 服务端会话存储，本网关不实现——显式 400，避免静默丢失上下文。
func ParseResponsesRequest(body []byte) (ResponsesRequest, error) {
	var r ResponsesRequest
	if err := json.Unmarshal(body, &r); err != nil {
		return r, fmt.Errorf("%s: invalid body: %w", ErrTypeInvalidRequest, err)
	}
	if strings.TrimSpace(r.Model) == "" {
		return r, fmt.Errorf("%s: model is required", ErrTypeInvalidRequest)
	}
	if len(r.PreviousResponseID) > 0 && string(r.PreviousResponseID) != "null" {
		return r, fmt.Errorf("%s: previous_response_id is not supported", ErrTypeInvalidRequest)
	}
	return r, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestParseResponsesRequest -v`
Expected: 4 个测试 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses.go internal/protocol/responses_test.go
git commit -m "feat(protocol): 解析 Responses 请求顶层字段

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 2: 工具上下文——function 工具与名称注册

**Files:**
- Create: `internal/protocol/responses_tools.go`
- Test: `internal/protocol/responses_tools_test.go`

**Interfaces:**
- Consumes: `ResponsesRequest`（Task 1）
- Produces:
  - `type responseToolKind int`（`responseToolFunction`/`responseToolCustom`/`responseToolNamespace`/`responseToolSearch`）
  - `type responseToolSpec struct { Kind responseToolKind; Name string; Namespace string }`
  - `func NewResponseToolContext(req ResponsesRequest) *ResponseToolContext`
  - `func (tc *ResponseToolContext) ChatTools() []map[string]any`
  - `func (tc *ResponseToolContext) DroppedTools() int`
  - `func (tc *ResponseToolContext) Spec(chatName string) (responseToolSpec, bool)`
  - `func (tc *ResponseToolContext) ChatName(name, namespace string) string`

- [ ] **Step 1: 写失败测试**

```go
package protocol

import "testing"

func toolCtx(t *testing.T, tools string) *ResponseToolContext {
	t.Helper()
	r, err := ParseResponsesRequest([]byte(`{"model":"m","tools":` + tools + `}`))
	if err != nil {
		t.Fatal(err)
	}
	return NewResponseToolContext(r)
}

func TestToolContextFunctionTool(t *testing.T) {
	tc := toolCtx(t, `[{"type":"function","name":"exec_command","description":"run","parameters":{"type":"object","properties":{"cmd":{"type":"string"}}}}]`)
	got := tc.ChatTools()
	if len(got) != 1 {
		t.Fatalf("chat tools: %+v", got)
	}
	fn := got[0]["function"].(map[string]any)
	if fn["name"] != "exec_command" || fn["description"] != "run" {
		t.Fatalf("function: %+v", fn)
	}
	spec, ok := tc.Spec("exec_command")
	if !ok || spec.Kind != responseToolFunction || spec.Name != "exec_command" {
		t.Fatalf("spec: %+v ok=%v", spec, ok)
	}
	if tc.ChatName("exec_command", "") != "exec_command" {
		t.Fatal("plain function name must be unchanged")
	}
	if tc.DroppedTools() != 0 {
		t.Fatalf("dropped=%d", tc.DroppedTools())
	}
}

func TestToolContextNormalizesBadParameters(t *testing.T) {
	tc := toolCtx(t, `[{"type":"function","name":"a"},{"type":"function","name":"b","parameters":null}]`)
	for _, tool := range tc.ChatTools() {
		p := tool["function"].(map[string]any)["parameters"].(map[string]any)
		if p["type"] != "object" || p["properties"] == nil {
			t.Fatalf("parameters must normalize to object: %+v", p)
		}
	}
}

func TestToolContextDropsHostedAndDuplicateTools(t *testing.T) {
	tc := toolCtx(t, `[{"type":"web_search"},{"type":"function","name":"a"},{"type":"function","name":"a"},{"type":"unknown"}]`)
	if len(tc.ChatTools()) != 1 || tc.DroppedTools() != 3 {
		t.Fatalf("tools=%d dropped=%d", len(tc.ChatTools()), tc.DroppedTools())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestToolContext -v`
Expected: 编译失败（`NewResponseToolContext` 未定义）。

- [ ] **Step 3: 实现**

```go
package protocol

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"strings"
)

type responseToolKind int

const (
	responseToolFunction responseToolKind = iota
	responseToolCustom
	responseToolNamespace
	responseToolSearch
)

type responseToolSpec struct {
	Kind      responseToolKind
	Name      string
	Namespace string
}

// ResponseToolContext 持有本次请求「chat 名 → Responses 工具语义」映射。
// 同一请求内工具集合固定，转换器只读它，不并发写。
type ResponseToolContext struct {
	specsByChatName map[string]responseToolSpec
	chatNamesByKey  map[string]string // key: namespace + "\x00" + name
	tools           []map[string]any
	dropped         int
}

func NewResponseToolContext(req ResponsesRequest) *ResponseToolContext {
	tc := &ResponseToolContext{
		specsByChatName: map[string]responseToolSpec{},
		chatNamesByKey:  map[string]string{},
	}
	for _, raw := range req.Tools {
		tc.addTool(raw)
	}
	tc.addInputDeclaredTools(req.Input)
	return tc
}

func (tc *ResponseToolContext) ChatTools() []map[string]any { return tc.tools }
func (tc *ResponseToolContext) DroppedTools() int          { return tc.dropped }

func (tc *ResponseToolContext) Spec(chatName string) (responseToolSpec, bool) {
	s, ok := tc.specsByChatName[chatName]
	return s, ok
}

// ChatName 把 Responses 侧的 (name, namespace) 映射为下发给 chat 上游的名字。
func (tc *ResponseToolContext) ChatName(name, namespace string) string {
	if n, ok := tc.chatNamesByKey[namespace+"\x00"+name]; ok {
		return n
	}
	return name
}

type responseToolDef struct {
	Type        string            `json:"type"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Parameters  json.RawMessage   `json:"parameters"`
	Strict      *bool             `json:"strict"`
	Tools       []responseToolDef `json:"tools"`
}

func (tc *ResponseToolContext) addTool(raw json.RawMessage) {
	var t responseToolDef
	if err := json.Unmarshal(raw, &t); err != nil {
		tc.dropped++
		return
	}
	switch t.Type {
	case "function":
		tc.addFunction(t, "")
	case "custom":
		tc.addCustom(raw, t)
	case "namespace":
		tc.addNamespace(t)
	case "tool_search":
		tc.addToolSearch()
	default:
		// web_search 等托管工具无法由 chat/Anthropic 上游兑现。
		tc.dropped++
	}
}

func (tc *ResponseToolContext) addFunction(t responseToolDef, namespace string) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		tc.dropped++
		return
	}
	chatName := name
	if namespace != "" {
		chatName = flattenResponseToolName(namespace, name)
	}
	if _, exists := tc.specsByChatName[chatName]; exists {
		tc.dropped++
		return
	}
	fn := map[string]any{
		"name":       chatName,
		"parameters": normalizeToolParameters(t.Parameters),
	}
	if t.Description != "" {
		fn["description"] = t.Description
	}
	if t.Strict != nil {
		fn["strict"] = *t.Strict
	}
	kind := responseToolFunction
	if namespace != "" {
		kind = responseToolNamespace
	}
	tc.specsByChatName[chatName] = responseToolSpec{Kind: kind, Name: name, Namespace: namespace}
	tc.chatNamesByKey[namespace+"\x00"+name] = chatName
	tc.tools = append(tc.tools, map[string]any{"type": "function", "function": fn})
}

// flattenResponseToolName 生成 chat 上游可接受的名字（≤64 字节）；超长时截断
// 并追加 FNV-1a 短哈希，保证同一 namespace/name 总是得到同一 chat 名。
func flattenResponseToolName(namespace, name string) string {
	full := namespace + "__" + name
	if len(full) <= 64 {
		return full
	}
	h := fnv.New32a()
	h.Write([]byte(full))
	suffix := fmt.Sprintf("__%08x", h.Sum32())
	return full[:64-len(suffix)] + suffix
}

// normalizeToolParameters 保证 chat 上游拿到 {"type":"object","properties":{}}。
func normalizeToolParameters(raw json.RawMessage) map[string]any {
	var p map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &p) != nil || p == nil {
		p = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	if p["type"] != "object" {
		p["type"] = "object"
	}
	if p["properties"] == nil {
		p["properties"] = map[string]any{}
	}
	return p
}
```

（`addCustom`/`addNamespace`/`addToolSearch`/`addInputDeclaredTools` 在 Task 3、4 中实现；本任务先加空实现以避免编译失败：）

```go
func (tc *ResponseToolContext) addCustom(raw json.RawMessage, t responseToolDef) {}
func (tc *ResponseToolContext) addNamespace(t responseToolDef)                  {}
func (tc *ResponseToolContext) addToolSearch()                                  {}
func (tc *ResponseToolContext) addInputDeclaredTools(input json.RawMessage)     {}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestToolContext -v`
Expected: 3 个测试 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses_tools.go internal/protocol/responses_tools_test.go
git commit -m "feat(protocol): Responses 工具上下文与 function 工具映射

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 3: custom 工具映射（apply_patch）

**Files:**
- Modify: `internal/protocol/responses_tools.go`
- Test: `internal/protocol/responses_tools_test.go`

**Interfaces:**
- Consumes: Task 2 的 `addTool` 分派
- Produces: `responseToolCustom` 的注册与 `Spec` 查询

- [ ] **Step 1: 写失败测试**

```go
func TestToolContextCustomTool(t *testing.T) {
	raw := `{
		"type":"custom","name":"apply_patch",
		"description":"Use the apply_patch tool",
		"format":{"type":"grammar","syntax":"lark","definition":"start: patch"},
		"parameters":{"type":"object"}}`
	tc := toolCtx(t, "["+raw+"]")
	got := tc.ChatTools()
	if len(got) != 1 {
		t.Fatalf("chat tools: %+v", got)
	}
	fn := got[0]["function"].(map[string]any)
	if fn["name"] != "apply_patch" {
		t.Fatalf("name: %+v", fn)
	}
	desc, _ := fn["description"].(string)
	if !strings.Contains(desc, "Original tool definition:") || !strings.Contains(desc, "grammar") {
		t.Fatalf("custom definition must be embedded: %q", desc)
	}
	params := fn["parameters"].(map[string]any)
	props := params["properties"].(map[string]any)
	if props["input"].(map[string]any)["type"] != "string" {
		t.Fatalf("custom tool must accept {input:string}: %+v", params)
	}
	spec, ok := tc.Spec("apply_patch")
	if !ok || spec.Kind != responseToolCustom || spec.Name != "apply_patch" {
		t.Fatalf("spec: %+v ok=%v", spec, ok)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestToolContextCustomTool -v`
Expected: FAIL（chat tools 为空）。

- [ ] **Step 3: 实现**

把 Task 2 的空实现替换为：

```go
const (
	customToolInputField        = "input"
	customToolInputDescription  = "Raw string input for the original custom tool. Preserve formatting exactly and follow the original tool definition embedded in the description."
	customToolDefinitionHeading = "Original tool definition:"
)

func (tc *ResponseToolContext) addCustom(raw json.RawMessage, t responseToolDef) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		tc.dropped++
		return
	}
	if _, exists := tc.specsByChatName[name]; exists {
		tc.dropped++
		return
	}
	def, _ := json.Marshal(json.RawMessage(raw))
	fn := map[string]any{
		"name":        name,
		"description": customToolDefinitionHeading + "\n```json\n" + string(def) + "\n```",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				customToolInputField: map[string]any{
					"type":        "string",
					"description": customToolInputDescription,
				},
			},
			"required": []string{customToolInputField},
		},
	}
	tc.specsByChatName[name] = responseToolSpec{Kind: responseToolCustom, Name: name}
	tc.chatNamesByKey["\x00"+name] = name
	tc.tools = append(tc.tools, map[string]any{"type": "function", "function": fn})
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestToolContextCustomTool -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses_tools.go internal/protocol/responses_tools_test.go
git commit -m "feat(protocol): custom 工具（apply_patch）映射为 chat 函数

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 4: namespace、tool_search 与动态工具注册

**Files:**
- Modify: `internal/protocol/responses_tools.go`
- Test: `internal/protocol/responses_tools_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestToolContextNamespaceFlattening(t *testing.T) {
	tc := toolCtx(t, `[{"type":"namespace","name":"multi_agent_v1","tools":[
		{"type":"function","name":"spawn_agent","parameters":{"type":"object"}},
		{"type":"function","name":"close_agent","parameters":{"type":"object"}}]}]`)
	if len(tc.ChatTools()) != 2 {
		t.Fatalf("chat tools: %+v", tc.ChatTools())
	}
	if got := tc.ChatName("spawn_agent", "multi_agent_v1"); got != "multi_agent_v1__spawn_agent" {
		t.Fatalf("flattened name: %q", got)
	}
	spec, ok := tc.Spec("multi_agent_v1__spawn_agent")
	if !ok || spec.Kind != responseToolNamespace || spec.Namespace != "multi_agent_v1" || spec.Name != "spawn_agent" {
		t.Fatalf("spec: %+v ok=%v", spec, ok)
	}
}

func TestToolContextToolSearchProxy(t *testing.T) {
	tc := toolCtx(t, `[{"type":"tool_search"}]`)
	if len(tc.ChatTools()) != 1 {
		t.Fatalf("chat tools: %+v", tc.ChatTools())
	}
	fn := tc.ChatTools()[0]["function"].(map[string]any)
	if fn["name"] != "tool_search" {
		t.Fatalf("tool_search proxy: %+v", fn)
	}
	spec, ok := tc.Spec("tool_search")
	if !ok || spec.Kind != responseToolSearch {
		t.Fatalf("spec: %+v ok=%v", spec, ok)
	}
}

func TestToolContextRegistersToolsFromInput(t *testing.T) {
	r, err := ParseResponsesRequest([]byte(`{"model":"m","input":[
		{"type":"tool_search_output","call_id":"c1","tools":[
			{"type":"namespace","name":"mcp__apps","tools":[{"type":"function","name":"search","parameters":{"type":"object"}}]}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	tc := NewResponseToolContext(r)
	if _, ok := tc.Spec("mcp__apps__search"); !ok {
		t.Fatalf("dynamically declared tools must register: %+v", tc.ChatTools())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run 'TestToolContext(NamespaceFlattening|ToolSearchProxy|RegistersToolsFromInput)' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

```go
func (tc *ResponseToolContext) addNamespace(t responseToolDef) {
	namespace := strings.TrimSpace(t.Name)
	if namespace == "" {
		tc.dropped++
		return
	}
	for _, child := range t.Tools {
		if child.Type == "function" {
			tc.addFunction(child, namespace)
		}
	}
}

func (tc *ResponseToolContext) addToolSearch() {
	const name = "tool_search"
	if _, exists := tc.specsByChatName[name]; exists {
		return
	}
	fn := map[string]any{
		"name":        name,
		"description": "Search and load Codex tools, plugins, connectors, and MCP namespaces for the current task.",
		"parameters": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query for tools or connectors to load."},
				"limit": map[string]any{"type": "integer", "description": "Maximum number of tool groups to return."},
			},
			"required": []string{"query"},
		},
	}
	tc.specsByChatName[name] = responseToolSpec{Kind: responseToolSearch, Name: name}
	tc.chatNamesByKey["\x00"+name] = name
	tc.tools = append(tc.tools, map[string]any{"type": "function", "function": fn})
}

// addInputDeclaredTools 递归扫描 input：tool_search_output / additional_tools
// 中声明的工具（含 namespace 子工具）注册进本次工具上下文。
func (tc *ResponseToolContext) addInputDeclaredTools(input json.RawMessage) {
	if len(input) == 0 {
		return
	}
	var v any
	if json.Unmarshal(input, &v) != nil {
		return
	}
	tc.walkDeclaredTools(v)
}

func (tc *ResponseToolContext) walkDeclaredTools(v any) {
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			tc.walkDeclaredTools(e)
		}
	case map[string]any:
		switch x["type"] {
		case "tool_search_output", "additional_tools":
			if arr, ok := x["tools"].([]any); ok {
				for _, t := range arr {
					if b, err := json.Marshal(t); err == nil {
						tc.addTool(b)
					}
				}
			}
		}
		for _, e := range x {
			tc.walkDeclaredTools(e)
		}
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run 'TestToolContext' -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses_tools.go internal/protocol/responses_tools_test.go
git commit -m "feat(protocol): namespace 拍平、tool_search 与动态工具注册

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 5: input 消息项与 instructions → chat messages

**Files:**
- Modify: `internal/protocol/responses.go`
- Test: `internal/protocol/responses_test.go`

**Interfaces:**
- Produces:
  - `func responsesInputToMessages(input json.RawMessage, instructions string, tc *ResponseToolContext) ([]map[string]any, error)`
  - `type responsesInputItem struct { Type, Role, CallID, Name, Namespace, Input string; Content, Arguments, Output json.RawMessage }`

- [ ] **Step 1: 写失败测试**

```go
func TestResponsesInputMessages(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{
		"model":"m","instructions":"system rules",
		"input":[
			{"type":"message","role":"developer","content":[{"type":"input_text","text":"dev rules"}]},
			{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"},{"type":"input_image","image_url":"data:x"}]},
			{"type":"message","role":"assistant","content":[{"type":"output_text","text":"previous"}]}
		]}`))
	tc := NewResponseToolContext(r)
	msgs, err := responsesInputToMessages(r.Input, r.Instructions, tc)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("messages: %+v", msgs)
	}
	if msgs[0]["role"] != "system" || msgs[0]["content"] != "system rules\n\ndev rules" {
		t.Fatalf("system merge: %+v", msgs[0])
	}
	if msgs[1]["role"] != "user" || msgs[1]["content"] != "hello" {
		t.Fatalf("user text: %+v", msgs[1])
	}
	if msgs[2]["role"] != "assistant" || msgs[2]["content"] != "previous" {
		t.Fatalf("assistant text: %+v", msgs[2])
	}
}

func TestResponsesInputString(t *testing.T) {
	msgs, err := responsesInputToMessages([]byte(`"hi"`), "", NewResponseToolContext(ResponsesRequest{}))
	if err != nil || len(msgs) != 1 || msgs[0]["role"] != "user" {
		t.Fatalf("string input: %+v err=%v", msgs, err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestResponsesInput -v`
Expected: 编译失败（函数未定义）。

- [ ] **Step 3: 实现**

在 `responses.go` 追加：

```go
type responsesInputItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Namespace string          `json:"namespace"`
	Arguments json.RawMessage `json:"arguments"`
	Input     string          `json:"input"`
	Output    json.RawMessage `json:"output"`
}

// responsesInputToMessages 把 instructions + input 转成 chat messages。
// developer/system 文本合并为一条头部 system；非文本分片丢弃。
func responsesInputToMessages(input json.RawMessage, instructions string, tc *ResponseToolContext) ([]map[string]any, error) {
	var systemParts []string
	if strings.TrimSpace(instructions) != "" {
		systemParts = append(systemParts, instructions)
	}
	var msgs []map[string]any

	if len(input) > 0 && string(input) != "null" {
		var s string
		if json.Unmarshal(input, &s) == nil {
			if s != "" {
				msgs = append(msgs, map[string]any{"role": "user", "content": s})
			}
		} else {
			var items []responsesInputItem
			if err := json.Unmarshal(input, &items); err != nil {
				return nil, fmt.Errorf("%s: input must be a string or an array", ErrTypeInvalidRequest)
			}
			for _, item := range items {
				switch item.Type {
				case "message":
					text := responsesContentText(item.Content)
					switch item.Role {
					case "developer", "system":
						if text != "" {
							systemParts = append(systemParts, text)
						}
					case "assistant":
						if text != "" {
							msgs = append(msgs, map[string]any{"role": "assistant", "content": text})
						}
					default:
						if text != "" {
							msgs = append(msgs, map[string]any{"role": "user", "content": text})
						}
					}
				}
			}
		}
	}
	if len(systemParts) > 0 {
		msgs = append([]map[string]any{{"role": "system", "content": strings.Join(systemParts, "\n\n")}}, msgs...)
	}
	return msgs, nil
}

// responsesContentText 提取 Responses 内容分片的文本（input_text/output_text/text）。
func responsesContentText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
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
		switch p.Type {
		case "input_text", "output_text", "text":
			if p.Text != "" {
				out = append(out, p.Text)
			}
		}
	}
	return strings.Join(out, "")
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestResponsesInput -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses.go internal/protocol/responses_test.go
git commit -m "feat(protocol): Responses input 消息项与 instructions 转 chat messages

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 6: 工具调用历史与孤儿输出

**Files:**
- Modify: `internal/protocol/responses.go`
- Test: `internal/protocol/responses_test.go`

**Interfaces:**
- Consumes: `responsesInputToMessages`（Task 5）、`ResponseToolContext.ChatName`（Task 2）
- Produces: 同一函数新增 `function_call` / `function_call_output` / `custom_tool_call(_output)` / `tool_search_call(_output)` / `reasoning` / `item_reference` 分支

- [ ] **Step 1: 写失败测试**

```go
func TestResponsesInputToolHistory(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{
		"model":"m",
		"tools":[{"type":"custom","name":"apply_patch"},
		         {"type":"namespace","name":"ns","tools":[{"type":"function","name":"run"}]}],
		"input":[
			{"type":"message","role":"user","content":"hi"},
			{"type":"function_call","call_id":"c1","name":"ns__run","namespace":"ns","arguments":"{\"a\":1}"},
			{"type":"function_call","call_id":"c2","name":"apply_patch","arguments":"{}"},
			{"type":"function_call_output","call_id":"c1","output":"ok"},
			{"type":"custom_tool_call","call_id":"c2","name":"apply_patch","input":"*** Begin Patch"},
			{"type":"custom_tool_call_output","call_id":"c2","output":"done"},
			{"type":"reasoning","summary":[]},
			{"type":"function_call_output","call_id":"missing","output":"orphan"}
		]}`))
	tc := NewResponseToolContext(r)
	msgs, err := responsesInputToMessages(r.Input, r.Instructions, tc)
	if err != nil {
		t.Fatal(err)
	}
	// user, assistant(tool_calls c1+c2), tool c1, assistant(tool_calls custom c2), tool c2, user(orphan)
	if len(msgs) != 6 {
		t.Fatalf("messages: %d %+v", len(msgs), msgs)
	}
	asst := msgs[1]
	calls := asst["tool_calls"].([]map[string]any)
	if len(calls) != 2 || calls[0]["id"] != "c1" {
		t.Fatalf("merged tool calls: %+v", calls)
	}
	fn := calls[0]["function"].(map[string]any)
	if fn["name"] != "ns__run" {
		t.Fatalf("namespace name must stay flattened on history replay: %+v", fn)
	}
	if msgs[2]["role"] != "tool" || msgs[2]["tool_call_id"] != "c1" {
		t.Fatalf("tool result: %+v", msgs[2])
	}
	if msgs[4]["role"] != "tool" || msgs[4]["tool_call_id"] != "c2" {
		t.Fatalf("custom tool result: %+v", msgs[4])
	}
	orphan, _ := msgs[5]["content"].(string)
	if msgs[5]["role"] != "user" || !strings.Contains(orphan, "without a matching tool_call") {
		t.Fatalf("orphan output: %+v", msgs[5])
	}
}

func TestResponsesInputRejectsItemReference(t *testing.T) {
	_, err := responsesInputToMessages([]byte(`[{"type":"item_reference","id":"x"}]`), "", NewResponseToolContext(ResponsesRequest{}))
	if err == nil {
		t.Fatal("item_reference must be rejected")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestResponsesInputToolHistory -v`
Expected: FAIL（消息数不符）。

- [ ] **Step 3: 实现**

在 `responsesInputToMessages` 的 `for _, item := range items` 循环前加入状态，并扩充 switch：

```go
			var pendingCalls []map[string]any
			knownCallIDs := map[string]bool{}
			flushCalls := func() {
				if len(pendingCalls) == 0 {
					return
				}
				// 相邻 assistant 文本与 tool_calls 合并成一条消息（部分上游拒绝
				// 连续 assistant 消息；cc-switch 同样合并）。
				if n := len(msgs); n > 0 && msgs[n-1]["role"] == "assistant" {
					existing, _ := msgs[n-1]["tool_calls"].([]map[string]any)
					msgs[n-1]["tool_calls"] = append(existing, pendingCalls...)
					pendingCalls = nil
					return
				}
				msgs = append(msgs, map[string]any{
					"role": "assistant", "content": nil, "tool_calls": pendingCalls,
				})
				pendingCalls = nil
			}
			appendToolResult := func(item responsesInputItem) {
				flushCalls()
				text := responsesOutputText(item.Output)
				if knownCallIDs[item.CallID] {
					msgs = append(msgs, map[string]any{
						"role": "tool", "tool_call_id": item.CallID, "content": text,
					})
					return
				}
				msgs = append(msgs, map[string]any{
					"role": "user",
					"content": "[tool_result without a matching tool_call] " + text,
				})
			}
```

switch 分支（替换 Task 5 的 `case "message"` 之后新增）：

```go
				case "function_call":
					flushCalls()
					knownCallIDs[item.CallID] = true
					pendingCalls = append(pendingCalls, map[string]any{
						"id": item.CallID, "type": "function",
						"function": map[string]any{
							"name":      tc.ChatName(item.Name, item.Namespace),
							"arguments": rawString(item.Arguments, "{}"),
						},
					})
				case "custom_tool_call":
					flushCalls()
					knownCallIDs[item.CallID] = true
					args, _ := json.Marshal(map[string]any{customToolInputField: item.Input})
					pendingCalls = append(pendingCalls, map[string]any{
						"id": item.CallID, "type": "function",
						"function": map[string]any{"name": tc.ChatName(item.Name, ""), "arguments": string(args)},
					})
				case "tool_search_call":
					flushCalls()
					knownCallIDs[item.CallID] = true
					pendingCalls = append(pendingCalls, map[string]any{
						"id": item.CallID, "type": "function",
						"function": map[string]any{"name": "tool_search", "arguments": rawString(item.Arguments, "{}")},
					})
				case "function_call_output", "custom_tool_call_output", "tool_search_output":
					appendToolResult(item)
				case "reasoning":
					// 本网关不生成 reasoning item，历史中的 reasoning 直接忽略。
				case "item_reference":
					return nil, fmt.Errorf("%s: item_reference is not supported", ErrTypeInvalidRequest)
				case "message":
					text := responsesContentText(item.Content)
					switch item.Role {
					case "developer", "system":
						if text != "" {
							systemParts = append(systemParts, text)
						}
					case "assistant":
						flushCalls()
						if text != "" {
							msgs = append(msgs, map[string]any{"role": "assistant", "content": text})
						}
					default:
						flushCalls()
						if text != "" {
							msgs = append(msgs, map[string]any{"role": "user", "content": text})
						}
					}
```

循环结束后、构造 system 前调用 `flushCalls()`。辅助函数追加：

```go
func rawString(raw json.RawMessage, fallback string) string {
	if len(raw) == 0 || string(raw) == "null" {
		return fallback
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// responsesOutputText 把工具输出（字符串或内容分片数组）压成文本。
func responsesOutputText(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}
```

注意：上面的 `message` 分支替换 Task 5 中的同名分支；system 分支不 flush，保持「工具调用块 → 普通消息」的顺序正确。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestResponsesInput -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses.go internal/protocol/responses_test.go
git commit -m "feat(protocol): Responses 工具调用历史与孤儿输出映射

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 7: ResponsesRequestToChat 组装

**Files:**
- Modify: `internal/protocol/responses.go`
- Test: `internal/protocol/responses_test.go`

**Interfaces:**
- Consumes: Task 2-6 的所有部件
- Produces:
  - `func ResponsesRequestToChat(req ResponsesRequest, tc *ResponseToolContext, chatModel string) ([]byte, error)`
  - `func mapResponsesToolChoice(raw json.RawMessage, tc *ResponseToolContext) json.RawMessage`

- [ ] **Step 1: 写失败测试**

```go
func TestResponsesRequestToChat(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{
		"model":"logical","stream":true,"instructions":"sys","max_output_tokens":512,
		"temperature":0.3,"top_p":0.9,
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}}],
		"tool_choice":{"type":"function","name":"exec_command"},
		"input":[{"type":"message","role":"user","content":"hi"}],
		"store":false,"reasoning":{"effort":"high"},"include":["reasoning.encrypted_content"]}`))
	tc := NewResponseToolContext(r)
	b, err := ResponsesRequestToChat(r, tc, "upstream-model")
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out["model"] != "upstream-model" || out["stream"] != true {
		t.Fatalf("model/stream: %+v", out)
	}
	if out["max_tokens"] != float64(512) || out["temperature"] != 0.3 || out["top_p"] != 0.9 {
		t.Fatalf("limits: %+v", out)
	}
	if _, ok := out["store"]; ok {
		t.Fatalf("store must not be forwarded: %+v", out)
	}
	if _, ok := out["reasoning"]; ok {
		t.Fatalf("reasoning must not be forwarded: %+v", out)
	}
	choice := out["tool_choice"].(map[string]any)
	if choice["function"].(map[string]any)["name"] != "exec_command" {
		t.Fatalf("tool_choice: %+v", choice)
	}
	msgs := out["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("messages: %+v", msgs)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestResponsesRequestToChat -v`
Expected: 编译失败。

- [ ] **Step 3: 实现**

```go
// ResponsesRequestToChat 把 Responses 请求转换成 chat.completions 请求体。
// 只下发 chat 上游可理解的字段；store/reasoning/include 等一律丢弃。
func ResponsesRequestToChat(req ResponsesRequest, tc *ResponseToolContext, chatModel string) ([]byte, error) {
	msgs, err := responsesInputToMessages(req.Input, req.Instructions, tc)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"model":    chatModel,
		"stream":   req.Stream,
		"messages": msgs,
	}
	if req.MaxOutputTokens != nil && *req.MaxOutputTokens > 0 {
		out["max_tokens"] = *req.MaxOutputTokens
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	if tools := tc.ChatTools(); len(tools) > 0 {
		out["tools"] = tools
	}
	if choice := mapResponsesToolChoice(req.ToolChoice, tc); len(choice) > 0 {
		out["tool_choice"] = json.RawMessage(choice)
	}
	return json.Marshal(out)
}

// mapResponsesToolChoice 把 Responses poll 的 tool_choice 映射为 chat 形状。
func mapResponsesToolChoice(raw json.RawMessage, tc *ResponseToolContext) json.RawMessage {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var obj struct {
		Type      string `json:"type"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	}
	if json.Unmarshal(raw, &obj) != nil || obj.Type == "" {
		return raw // "auto"/"none"/"required" 原样
	}
	switch obj.Type {
	case "function":
		b, _ := json.Marshal(map[string]any{
			"type": "function",
			"function": map[string]any{"name": tc.ChatName(obj.Name, obj.Namespace)},
		})
		return b
	case "custom":
		b, _ := json.Marshal(map[string]any{
			"type": "function", "function": map[string]any{"name": obj.Name},
		})
		return b
	case "tool_search":
		b, _ := json.Marshal(map[string]any{
			"type": "function", "function": map[string]any{"name": "tool_search"},
		})
		return b
	default:
		return raw
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestResponsesRequestToChat -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses.go internal/protocol/responses_test.go
git commit -m "feat(protocol): Responses 请求组装为 chat.completions

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 8: 非流式响应——信封、文本与 usage

**Files:**
- Create: `internal/protocol/responses_response.go`
- Test: `internal/protocol/responses_response_test.go`

**Interfaces:**
- Produces:
  - `func ChatCompletionToResponses(chatBody []byte, requestedModel string, tc *ResponseToolContext) ([]byte, error)`
  - `func chatUsageToResponsesUsage(u *chatUsage) map[string]any`
  - `func responseIDFromChatID(id string) string`
  - `func responsesResponseObject(id, model, status string, createdAt int64, output []map[string]any, usage map[string]any) map[string]any`

- [ ] **Step 1: 写失败测试**

```go
package protocol

import (
	"encoding/json"
	"testing"
)

func TestChatCompletionToResponsesText(t *testing.T) {
	chat := `{"id":"chatcmpl_1","object":"chat.completion","created":123,"model":"up",
		"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
		"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,
		         "prompt_tokens_details":{"cached_tokens":4},
		         "completion_tokens_details":{"reasoning_tokens":1}}}`
	tc := NewResponseToolContext(ResponsesRequest{})
	b, err := ChatCompletionToResponses([]byte(chat), "logical", tc)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.Unmarshal(b, &out)
	if out["object"] != "response" || out["status"] != "completed" || out["model"] != "logical" {
		t.Fatalf("envelope: %s", b)
	}
	output := out["output"].([]any)
	if len(output) != 1 {
		t.Fatalf("output: %s", b)
	}
	item := output[0].(map[string]any)
	if item["type"] != "message" {
		t.Fatalf("item: %+v", item)
	}
	part := item["content"].([]any)[0].(map[string]any)
	if part["type"] != "output_text" || part["text"] != "pong" {
		t.Fatalf("part: %+v", part)
	}
	usage := out["usage"].(map[string]any)
	if usage["input_tokens"] != float64(10) || usage["output_tokens"] != float64(2) {
		t.Fatalf("usage: %+v", usage)
	}
	if usage["input_tokens_details"].(map[string]any)["cached_tokens"] != float64(4) {
		t.Fatalf("cached: %+v", usage)
	}
}

func TestChatCompletionToResponsesLengthIncomplete(t *testing.T) {
	chat := `{"id":"c","created":1,"choices":[{"message":{"role":"assistant","content":"x"},"finish_reason":"length"}]}`
	b, _ := ChatCompletionToResponses([]byte(chat), "m", NewResponseToolContext(ResponsesRequest{}))
	var out map[string]any
	json.Unmarshal(b, &out)
	if out["status"] != "incomplete" {
		t.Fatalf("status: %s", b)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestChatCompletionToResponses -v`
Expected: 编译失败。

- [ ] **Step 3: 实现**

```go
package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

type chatResponse struct {
	ID      string `json:"id"`
	Created int64  `json:"created"`
	Choices []struct {
		Message struct {
			Content          *string        `json:"content"`
			ReasoningContent string         `json:"reasoning_content"`
			Reasoning        string         `json:"reasoning"`
			ToolCalls        []chatToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *chatUsage `json:"usage"`
}

type chatToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type chatUsage struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	TotalTokens             int `json:"total_tokens"`
	PromptTokensDetails     *struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails *struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

func ChatCompletionToResponses(chatBody []byte, requestedModel string, tc *ResponseToolContext) ([]byte, error) {
	var cr chatResponse
	if err := json.Unmarshal(chatBody, &cr); err != nil {
		return nil, fmt.Errorf("invalid chat completion: %w", err)
	}
	id := responseIDFromChatID(cr.ID)
	status := "completed"
	incomplete := ""
	if len(cr.Choices) > 0 && cr.Choices[0].FinishReason == "length" {
		status, incomplete = "incomplete", "max_output_tokens"
	}
	var output []map[string]any
	if len(cr.Choices) > 0 {
		output = chatMessageToResponseItems(cr.Choices[0].Message.Content, cr.Choices[0].Message.ReasoningContent,
			cr.Choices[0].Message.Reasoning, cr.Choices[0].Message.ToolCalls, id, tc)
	}
	resp := responsesResponseObject(id, requestedModel, status, cr.Created, output, chatUsageToResponsesUsage(cr.Usage))
	if incomplete != "" {
		resp["incomplete_details"] = map[string]any{"reason": incomplete}
	}
	return json.Marshal(resp)
}

func responseIDFromChatID(id string) string {
	if id == "" {
		return "resp_local"
	}
	return "resp_" + strings.TrimLeft(strings.ReplaceAll(id, " ", "_"), "_")
}

func responsesResponseObject(id, model, status string, createdAt int64, output []map[string]any, usage map[string]any) map[string]any {
	if output == nil {
		output = []map[string]any{}
	}
	return map[string]any{
		"id": id, "object": "response", "created_at": createdAt,
		"status": status, "model": model, "output": output, "usage": usage,
	}
}

func chatUsageToResponsesUsage(u *chatUsage) map[string]any {
	var in, out, total, cached, reasoning int
	if u != nil {
		in, out, total = u.PromptTokens, u.CompletionTokens, u.TotalTokens
		if u.PromptTokensDetails != nil {
			cached = u.PromptTokensDetails.CachedTokens
		}
		if u.CompletionTokensDetails != nil {
			reasoning = u.CompletionTokensDetails.ReasoningTokens
		}
	}
	if total == 0 {
		total = in + out
	}
	return map[string]any{
		"input_tokens": in, "input_tokens_details": map[string]any{"cached_tokens": cached},
		"output_tokens": out, "output_tokens_details": map[string]any{"reasoning_tokens": reasoning},
		"total_tokens": total,
	}
}
```

`chatMessageToResponseItems` 在 Task 9 实现；本任务先加最小实现（仅文本与 reasoning）：

```go
func chatMessageToResponseItems(content *string, reasoningContent, reasoning string,
	calls []chatToolCall, respID string, tc *ResponseToolContext) []map[string]any {
	var out []map[string]any
	if content != nil && *content != "" {
		out = append(out, map[string]any{
			"id": respID + "_msg", "type": "message", "status": "completed",
			"role": "assistant",
			"content": []map[string]any{{"type": "output_text", "text": *content, "annotations": []any{}}},
		})
	}
	text := reasoningContent
	if text == "" {
		text = reasoning
	}
	if text != "" {
		out = append(out, map[string]any{
			"id": "rs_" + respID, "type": "reasoning",
			"summary": []map[string]any{{"type": "summary_text", "text": text}},
		})
	}
	return out
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestChatCompletionToResponses -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses_response.go internal/protocol/responses_response_test.go
git commit -m "feat(protocol): chat 非流式响应转 Responses 信封与 usage

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 9: 非流式工具 item 还原（function/namespace/custom/tool_search）

**Files:**
- Modify: `internal/protocol/responses_response.go`
- Test: `internal/protocol/responses_response_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestChatCompletionToResponsesToolItems(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{"model":"m","tools":[
		{"type":"function","name":"exec_command"},
		{"type":"custom","name":"apply_patch"},
		{"type":"namespace","name":"ns","tools":[{"type":"function","name":"run"}]},
		{"type":"tool_search"}]}`))
	tc := NewResponseToolContext(r)
	chat := `{"id":"c1","created":1,"choices":[{"message":{"role":"assistant","content":null,"tool_calls":[
		{"id":"call_1","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"ls\"}"}},
		{"id":"call_2","type":"function","function":{"name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}"}},
		{"id":"call_3","type":"function","function":{"name":"ns__run","arguments":"{}"}},
		{"id":"call_4","type":"function","function":{"name":"tool_search","arguments":"{\"query\":\"gmail\"}"}}
	]},"finish_reason":"tool_calls"}]}`
	b, err := ChatCompletionToResponses([]byte(chat), "m", tc)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.Unmarshal(b, &out)
	items := out["output"].([]any)
	if len(items) != 4 {
		t.Fatalf("items: %s", b)
	}
	if items[0].(map[string]any)["type"] != "function_call" ||
		items[0].(map[string]any)["name"] != "exec_command" {
		t.Fatalf("function item: %+v", items[0])
	}
	if items[1].(map[string]any)["type"] != "custom_tool_call" ||
		items[1].(map[string]any)["input"] != "*** Begin Patch" {
		t.Fatalf("custom item: %+v", items[1])
	}
	if items[2].(map[string]any)["namespace"] != "ns" || items[2].(map[string]any)["name"] != "run" {
		t.Fatalf("namespace item: %+v", items[2])
	}
	if items[3].(map[string]any)["type"] != "tool_search_call" ||
		items[3].(map[string]any)["arguments"].(map[string]any)["query"] != "gmail" {
		t.Fatalf("tool_search item: %+v", items[3])
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestChatCompletionToResponsesToolItems -v`
Expected: FAIL（items 为 0）。

- [ ] **Step 3: 实现**

在 `chatMessageToResponseItems` 末尾追加：

```go
	for _, call := range calls {
		name := call.Function.Name
		spec, ok := tc.Spec(name)
		if !ok {
			spec = responseToolSpec{Kind: responseToolFunction, Name: name}
		}
		callID := call.ID
		if callID == "" {
			callID = "call_" + fmt.Sprint(len(out))
		}
		switch spec.Kind {
		case responseToolCustom:
			out = append(out, map[string]any{
				"id": "ctc_" + callID, "type": "custom_tool_call", "status": "completed",
				"call_id": callID, "name": spec.Name, "input": customToolInput(call.Function.Arguments),
			})
		case responseToolSearch:
			out = append(out, map[string]any{
				"id": "ts_" + callID, "type": "tool_search_call", "status": "completed",
				"call_id": callID, "execution": "client",
				"arguments": parseToolArgumentsObject(call.Function.Arguments),
			})
		default:
			item := map[string]any{
				"id": "fc_" + callID, "type": "function_call", "status": "completed",
				"call_id": callID, "name": spec.Name, "arguments": call.Function.Arguments,
			}
			if spec.Namespace != "" {
				item["namespace"] = spec.Namespace
			}
			out = append(out, item)
		}
	}
	return out
```

辅助函数：

```go
// customToolInput 从 {"input":"..."} 解包自由文本；解析失败时原样返回。
func customToolInput(arguments string) string {
	var obj map[string]any
	if json.Unmarshal([]byte(arguments), &obj) == nil {
		if s, ok := obj[customToolInputField].(string); ok {
			return s
		}
	}
	return arguments
}

// parseToolArgumentsObject 把参数 JSON 解析为对象；失败时退化为 {"query": 原文}。
func parseToolArgumentsObject(arguments string) map[string]any {
	var obj map[string]any
	if json.Unmarshal([]byte(arguments), &obj) == nil && obj != nil {
		return obj
	}
	return map[string]any{"query": arguments}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestChatCompletionToResponses -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses_response.go internal/protocol/responses_response_test.go
git commit -m "feat(protocol): 非流式响应还原四类工具 item

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 10: 流式状态机——生命周期与文本事件

**Files:**
- Create: `internal/protocol/responses_stream.go`
- Test: `internal/protocol/responses_stream_test.go`

**Interfaces:**
- Produces:
  - `func ChatStreamToResponsesStream(ctx context.Context, r io.Reader, requestedModel string, tc *ResponseToolContext) io.ReadCloser`
  - `func ResponsesFailedEvent(message string) []byte`
  - `func responsesSSEEvent(event string, payload map[string]any) []byte`

- [ ] **Step 1: 写失败测试**

```go
package protocol

import (
	"context"
	"io"
	"strings"
	"testing"
)

func streamEvents(t *testing.T, chatSSE string) string {
	t.Helper()
	rc := ChatStreamToResponsesStream(context.Background(), strings.NewReader(chatSSE), "logical",
		NewResponseToolContext(ResponsesRequest{}))
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	return string(b)
}

func TestChatStreamToResponsesText(t *testing.T) {
	chat := `data: {"id":"chatcmpl_1","created":9,"choices":[{"delta":{"role":"assistant"}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":"po"}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"content":"ng"}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	want := []string{
		"event: response.created",
		"event: response.in_progress",
		"event: response.output_item.added",
		"event: response.content_part.added",
		`"delta":"po"`,
		`"delta":"ng"`,
		"event: response.output_text.done",
		"event: response.content_part.done",
		"event: response.output_item.done",
		"event: response.completed",
	}
	pos := 0
	for _, token := range want {
		idx := strings.Index(out[pos:], token)
		if idx < 0 {
			t.Fatalf("missing %q in order; output:\n%s", token, out)
		}
		pos += idx + len(token)
	}
	if strings.Contains(out, "response.failed") {
		t.Fatalf("normal stream must not fail:\n%s", out)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestChatStreamToResponsesText -v`
Expected: 编译失败。

- [ ] **Step 3: 实现**

```go
package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
)

// responsesSSEEvent 生成一条 Responses SSE 帧。
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

// ResponsesFailedEvent 生成流中失败事件（网关空闲超时等无法访问转换器状态时使用）。
func ResponsesFailedEvent(message string) []byte {
	resp := responsesResponseObject("resp_local", "", "failed", 0, nil, chatUsageToResponsesUsage(nil))
	resp["error"] = map[string]any{"message": message}
	return responsesSSEEvent("response.failed", map[string]any{"type": "response.failed", "response": resp})
}

type responsesTextState struct {
	index int
	id    string
	text  strings.Builder
	open  bool
	done  bool
}

type chatToResponsesStream struct {
	out        bytes.Buffer
	raw        []byte
	started    bool
	finished   bool
	failed     bool
	respID     string
	model      string
	createdAt  int64
	nextIndex  int
	text       responsesTextState
	finish     string
	sawTerminal bool
	usage      *chatUsage
	tc         *ResponseToolContext
}

// ChatStreamToResponsesStream 把 chat.completion.chunk SSE 泵成 Responses SSE。
// 调用方必须关闭返回的 ReadCloser 与上游 body（同 AnthropicStreamToOpenAIStream）。
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
				// 客户端取消：静默收尾。
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

func (t *chatToResponsesStream) ensureStarted() {
	if t.started {
		return
	}
	t.started = true
	resp := t.baseResponse("in_progress")
	t.out.Write(responsesSSEEvent("response.created", map[string]any{"type": "response.created", "response": resp}))
	t.out.Write(responsesSSEEvent("response.in_progress", map[string]any{"type": "response.in_progress", "response": resp}))
}

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
```

`Feed`/`Finish`/`Fail`/`handleChunk` 本任务先实现到文本收口（工具分支留空，Task 11/12 填充）：

```go
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

func (t *chatToResponsesStream) drain() []byte {
	if t.out.Len() == 0 {
		return nil
	}
	out := make([]byte, t.out.Len())
	copy(out, t.out.Bytes())
	t.out.Reset()
	return out
}
```

注意：`model` 必须回显逻辑模型名（不泄露上游名）——解析 chunk 时可以读 `id`/`created`，但**不得**用上游 `model` 覆盖 `requestedModel`。`createdAt` 可保留。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run TestChatStreamToResponsesText -v`
Expected: PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses_stream.go internal/protocol/responses_stream_test.go
git commit -m "feat(protocol): Responses 流式状态机与文本事件

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 11: 流式工具事件——function 工具

**Files:**
- Modify: `internal/protocol/responses_stream.go`
- Test: `internal/protocol/responses_stream_test.go`

**Interfaces:**
- Produces: `chatToResponsesStream.pushToolDelta(index int, id, name, args string)` 与收口逻辑

- [ ] **Step 1: 写失败测试**

```go
func TestChatStreamToResponsesFunctionCall(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{"model":"m","tools":[{"type":"function","name":"exec_command"}]}`))
	tc := NewResponseToolContext(r)
	chat := `data: {"id":"chatcmpl_1","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"exec_command","arguments":""}}]}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"cmd\":"}}]}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"ls\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"chatcmpl_1","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	rc := ChatStreamToResponsesStream(context.Background(), strings.NewReader(chat), "m", tc)
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	out := string(b)
	for _, want := range []string{
		`"type":"function_call"`, `"call_id":"call_1"`, `"name":"exec_command"`,
		"response.function_call_arguments.delta", "response.function_call_arguments.done",
		`"arguments":"{\"cmd\":\"ls\"}"`, "event: response.completed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestChatStreamToResponsesFunctionCall -v`
Expected: FAIL（无 function_call 事件）。

- [ ] **Step 3: 实现**

新增状态与分支：

```go
type responsesToolState struct {
	index     int
	id        string
	callID    string
	name      string
	args      strings.Builder
	open      bool
	done      bool
	completed map[string]any
}

// 在 chatToResponsesStream 结构体追加：
//   tools     map[int]*responsesToolState
//   toolOrder []int
```

`pushToolDelta`：

```go
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
		return
	}
	if st.open && args != "" && st.kind == responseToolFunction {
		t.out.Write(responsesSSEEvent("response.function_call_arguments.delta", map[string]any{
			"type": "response.function_call_arguments.delta", "item_id": st.idPrefix + st.callID,
			"output_index": st.index, "delta": args,
		}))
	}
}
```

在 `handleChunk` 的 choices 分支中，解析 `tool_calls` 并逐项调用：

```go
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
```

循环：

```go
	for _, call := range c.Choices[0].Delta.ToolCalls {
		t.pushToolDelta(call.Index, call.ID, call.Function.Name, call.Function.Arguments)
	}
```

`Finish` 中在 `closeText` 之后收口工具：

```go
	output := t.closeText()
	for _, idx := range t.toolOrder {
		st := t.tools[idx]
		if st == nil || st.done {
			continue
		}
		st.done = true
		args := st.args.String()
		if args == "" {
			args = "{}"
		}
		spec, ok := t.tc.Spec(st.name)
		if !ok {
			spec = responseToolSpec{Kind: responseToolFunction, Name: st.name}
		}
		item := map[string]any{
			"id": "fc_" + st.callID, "type": "function_call", "status": "completed",
			"call_id": st.callID, "name": spec.Name, "arguments": args,
		}
		if spec.Namespace != "" {
			item["namespace"] = spec.Namespace
		}
		t.out.Write(responsesSSEEvent("response.function_call_arguments.done", map[string]any{
			"type": "response.function_call_arguments.done", "item_id": "fc_" + st.callID,
			"output_index": st.index, "arguments": args,
		}))
		t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
			"type": "response.output_item.done", "output_index": st.index, "item": item,
		}))
		output = append(output, item)
	}
```

`Fail` 同样用已完成的工具 item（未完成的不下发）。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run 'TestChatStreamToResponses' -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses_stream.go internal/protocol/responses_stream_test.go
git commit -m "feat(protocol): Responses 流式 function_call 事件

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 12: 流式 custom 与 tool_search 工具收口

**Files:**
- Modify: `internal/protocol/responses_stream.go`
- Test: `internal/protocol/responses_stream_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestChatStreamToResponsesCustomAndToolSearch(t *testing.T) {
	r, _ := ParseResponsesRequest([]byte(`{"model":"m","tools":[
		{"type":"custom","name":"apply_patch"},{"type":"tool_search"}]}`))
	tc := NewResponseToolContext(r)
	chat := `data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_p","function":{"name":"apply_patch","arguments":"{\"input\":\"*** Begin Patch\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_t","function":{"name":"tool_search","arguments":"{\"query\":\"gmail\"}"}}]}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	rc := ChatStreamToResponsesStream(context.Background(), strings.NewReader(chat), "m", tc)
	defer rc.Close()
	b, _ := io.ReadAll(rc)
	out := string(b)
	for _, want := range []string{
		`"type":"custom_tool_call"`, `"input":"*** Begin Patch"`,
		"response.custom_tool_call_input.done",
		`"type":"tool_search_call"`, `"query":"gmail"`, "event: response.completed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "response.function_call_arguments.delta") {
		t.Fatalf("custom/tool_search must not stream argument deltas:\n%s", out)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run TestChatStreamToResponsesCustomAndToolSearch -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

在 `responsesToolState` 增加三个字段：`kind responseToolKind`、`itemType string`、`idPrefix string`、`kindResolved bool`（零值即 function/`fc_`/`function_call`，无需显式初始化）。

在 `pushToolDelta` 的 `if name != "" { st.name = name }` 之后、added 判定之前，解析一次工具类型：

```go
	if st.name != "" && !st.kindResolved {
		st.kindResolved = true
		if spec, ok := t.tc.Spec(st.name); ok {
			st.kind = spec.Kind
		}
		switch st.kind {
		case responseToolCustom:
			st.itemType, st.idPrefix = "custom_tool_call", "ctc_"
		case responseToolSearch:
			st.itemType, st.idPrefix = "tool_search_call", "ts_"
		}
	}
```

Task 11 写好的 added item 与 delta 发射逻辑已经按 `st.kind`/`st.itemType`/`st.idPrefix` 取形状（custom 用 `input`，tool_search 用 `arguments: {}` 与 `execution: "client"`），本任务只需补上面的赋值，不要把同一段逻辑抄第二遍。

（`responses_stream.go` 的 import 补 `fmt`。）

`Finish` 的工具收口按 kind 分支：

```go
		callID := st.callID
		if callID == "" {
			callID = fmt.Sprintf("call_%d", st.index)
		}
		switch st.kind {
		case responseToolCustom:
			input := customToolInput(st.args.String())
			item := map[string]any{
				"id": "ctc_" + callID, "type": "custom_tool_call", "status": "completed",
				"call_id": callID, "name": spec.Name, "input": input,
			}
			if input != "" {
				t.out.Write(responsesSSEEvent("response.custom_tool_call_input.delta", map[string]any{
					"type": "response.custom_tool_call_input.delta", "item_id": "ctc_" + callID,
					"output_index": st.index, "delta": input,
				}))
			}
			t.out.Write(responsesSSEEvent("response.custom_tool_call_input.done", map[string]any{
				"type": "response.custom_tool_call_input.done", "item_id": "ctc_" + callID,
				"output_index": st.index, "input": input,
			}))
			t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done", "output_index": st.index, "item": item,
			}))
			output = append(output, item)
		case responseToolSearch:
			argsObj := parseToolArgumentsObject(st.args.String())
			item := map[string]any{
				"id": "ts_" + callID, "type": "tool_search_call", "status": "completed",
				"call_id": callID, "execution": "client", "arguments": argsObj,
			}
			t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
				"type": "response.output_item.done", "output_index": st.index, "item": item,
			}))
			output = append(output, item)
		default:
			// Task 11 的 function 分支
		}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run 'TestChatStreamToResponses' -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses_stream.go internal/protocol/responses_stream_test.go
git commit -m "feat(protocol): Responses 流式 custom/tool_search 工具事件

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 13: 流式 reasoning、截断与失败事件

**Files:**
- Modify: `internal/protocol/responses_stream.go`
- Test: `internal/protocol/responses_stream_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestChatStreamToResponsesReasoning(t *testing.T) {
	chat := `data: {"id":"c","choices":[{"delta":{"reasoning_content":"think"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{"content":"answer"}}]}` + "\n\n" +
		`data: {"id":"c","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n" +
		`data: [DONE]` + "\n\n"
	out := streamEvents(t, chat)
	for _, want := range []string{
		`"type":"reasoning"`, "response.reasoning_summary_text.delta", `"delta":"think"`,
		"response.reasoning_summary_text.done", "event: response.completed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}

func TestChatStreamTruncatedEmitsFailed(t *testing.T) {
	chat := `data: {"id":"c","choices":[{"delta":{"content":"half"}}]}` + "\n\n"
	out := streamEvents(t, chat)
	if !strings.Contains(out, "event: response.failed") || strings.Contains(out, "event: response.completed") {
		t.Fatalf("truncated stream must fail:\n%s", out)
	}
}

func TestChatStreamErrorChunkEmitsFailed(t *testing.T) {
	chat := `data: {"error":{"message":"upstream exploded","type":"server_error"}}` + "\n\n"
	out := streamEvents(t, chat)
	if !strings.Contains(out, "event: response.failed") || !strings.Contains(out, "upstream exploded") {
		t.Fatalf("error chunk must surface:\n%s", out)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/protocol -run 'TestChatStream(ToResponsesReasoning|TruncatedEmitsFailed|ErrorChunkEmitsFailed)' -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

reasoning 状态（结构体增加 `reasoning responsesReasoningState`）：

```go
type responsesReasoningState struct {
	index int
	id    string
	text  strings.Builder
	open  bool
	done  bool
}

func (t *chatToResponsesStream) pushReasoning(delta string) {
	t.ensureStarted()
	if !t.reasoning.open {
		t.closeText()
		t.reasoning.index = t.nextIndex
		t.nextIndex++
		t.reasoning.id = "rs_" + t.respID
		t.reasoning.open = true
		t.out.Write(responsesSSEEvent("response.output_item.added", map[string]any{
			"type": "response.output_item.added", "output_index": t.reasoning.index,
			"item": map[string]any{"id": t.reasoning.id, "type": "reasoning", "status": "in_progress", "summary": []any{}},
		}))
		t.out.Write(responsesSSEEvent("response.reasoning_summary_part.added", map[string]any{
			"type": "response.reasoning_summary_part.added", "item_id": t.reasoning.id,
			"output_index": t.reasoning.index, "summary_index": 0,
			"part": map[string]any{"type": "summary_text", "text": ""},
		}))
	}
	t.reasoning.text.WriteString(delta)
	t.out.Write(responsesSSEEvent("response.reasoning_summary_text.delta", map[string]any{
		"type": "response.reasoning_summary_text.delta", "item_id": t.reasoning.id,
		"output_index": t.reasoning.index, "summary_index": 0, "delta": delta,
	}))
}

func (t *chatToResponsesStream) closeReasoning() []map[string]any {
	if !t.reasoning.open || t.reasoning.done {
		return nil
	}
	t.reasoning.done = true
	text := t.reasoning.text.String()
	item := map[string]any{
		"id": t.reasoning.id, "type": "reasoning",
		"summary": []map[string]any{{"type": "summary_text", "text": text}},
	}
	t.out.Write(responsesSSEEvent("response.reasoning_summary_text.done", map[string]any{
		"type": "response.reasoning_summary_text.done", "item_id": t.reasoning.id,
		"output_index": t.reasoning.index, "summary_index": 0, "text": text,
	}))
	t.out.Write(responsesSSEEvent("response.reasoning_summary_part.done", map[string]any{
		"type": "response.reasoning_summary_part.done", "item_id": t.reasoning.id,
		"output_index": t.reasoning.index, "summary_index": 0,
		"part": map[string]any{"type": "summary_text", "text": text},
	}))
	t.out.Write(responsesSSEEvent("response.output_item.done", map[string]any{
		"type": "response.output_item.done", "output_index": t.reasoning.index, "item": item,
	}))
	return []map[string]any{item}
}
```

`handleChunk` 开头先检测 error 对象，再处理 delta：

```go
	if t.finished {
		return
	}
	var probe struct {
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.Error != nil && probe.Error.Message != "" {
		t.Fail(probe.Error.Message)
		return
	}
```

delta 解析（在 Task 10 的匿名结构体上补 `ReasoningContent string `json:"reasoning_content"`` 与 `Reasoning string `json:"reasoning"`` 两个字段）：

```go
	d := c.Choices[0].Delta
	if r := d.ReasoningContent; r != "" {
		t.pushReasoning(r)
	} else if r := d.Reasoning; r != "" {
		t.pushReasoning(r)
	}
	if d.Content != "" {
		t.closeReasoning() // reasoning item 在正文开始前收口
		t.pushText(d.Content)
	}
```

若正文先到、reasoning 后到，则按 delta 顺序：reasoning 在 `pushReasoning` 内部先 `closeText()`，两段 item 各自独立收口。

在 `Finish` 与 `Fail` 的 `output` 聚合中加入 `closeReasoning()` 返回项，并按 output_index 排序：把 text、reasoning、tools 的 item 都放进 `[]struct{idx int; item map[string]any}` 再排序。**实现要求**：`response.completed.output` 必须按 `output_index` 升序，与已发事件一致。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/protocol -run 'TestChatStream' -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/protocol/responses_stream.go internal/protocol/responses_stream_test.go
git commit -m "feat(protocol): Responses 流式 reasoning 与失败收口

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 14: 网关路由与 handler 骨架

**Files:**
- Create: `internal/gateway/responses.go`
- Modify: `internal/gateway/server.go:66-70`
- Test: `internal/gateway/gateway_test.go`

**Interfaces:**
- Consumes: `ParseResponsesRequest`、`NewResponseToolContext`、`stripOneM`、`gate`、`failover`、`copyStream`
- Produces: `func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request)`

- [ ] **Step 1: 写失败测试**

```go
func postResponses(t *testing.T, s *Server, body, token string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest("POST", "/v1/responses", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	return w.Result()
}

func TestResponsesAuthRequired(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"id":"ok"}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi"}`, "wrong")
	defer resp.Body.Close()
	if resp.StatusCode != 401 || calls != 0 {
		t.Fatalf("status=%d calls=%d", resp.StatusCode, calls)
	}
}

func TestResponsesUnknownModel404(t *testing.T) {
	var calls int
	up := fakeUpstream(t, 200, `{"id":"ok"}`, &calls)
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := postResponses(t, s, `{"model":"nope","input":"hi"}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 404 || calls != 0 || !strings.Contains(string(b), "pub-1") {
		t.Fatalf("status=%d calls=%d body=%s", resp.StatusCode, calls, b)
	}
}

func TestResponsesPreviousResponseID400(t *testing.T) {
	s := newGW(t, "http://127.0.0.1:1", nil)
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi","previous_response_id":"r"}`, "tok")
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/gateway -run TestResponses -v`
Expected: FAIL（404 路由未注册）。

- [ ] **Step 3: 实现**

`server.go` 注册：

```go
	s.mux.HandleFunc("POST /v1/responses", s.handleResponses)
```

`responses.go` 骨架（完整 failover 在 Task 15/16 填充；本任务先把鉴权/解析/404/400 与错误形状做对）：

```go
package gateway

import (
	"net/http"
	"strings"
	"time"

	"AgentSwitch/internal/protocol"
	"AgentSwitch/internal/telemetry"
)

func (s *Server) handleResponses(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	rec := telemetry.RequestRecord{ID: telemetryID(), StartedAt: started}
	switches := 0
	defer func() {
		rec.DurationMs = time.Since(started).Milliseconds()
		rec.FailoverCount = switches
		if s.deps.Log != nil {
			s.deps.Log.Info("request", "request_id", rec.ID, "model", rec.Model,
				"upstream_model", rec.UpstreamModel, "channel", rec.Channel,
				"account", rec.Account, "status", rec.Status,
				"duration_ms", rec.DurationMs, "error_class", rec.ErrorClass,
				"streaming", rec.Streaming, "switches", switches)
		}
		if s.deps.Ring != nil {
			s.deps.Ring.Add(rec)
		}
		if s.deps.OnRecord != nil {
			s.deps.OnRecord(rec)
		}
	}()

	body, release, status, errType, msg, ok := s.gate(r, &rec)
	if release != nil {
		defer release()
	}
	if !ok {
		writeErrOpenAI(w, status, errType, msg)
		return
	}
	req, err := protocol.ParseResponsesRequest(body)
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, err.Error())
		return
	}
	req.Model = stripOneM(req.Model)
	rec.Model, rec.Streaming = req.Model, req.Stream

	cands, err := s.deps.Candidates(req.Model)
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErrOpenAI(w, 500, protocol.ErrTypeAPI, "candidates unavailable")
		return
	}
	if len(cands) == 0 {
		rec.Status, rec.ErrorClass = 404, protocol.ErrTypeNotFound
		writeErrOpenAI(w, 404, protocol.ErrTypeNotFound,
			"unknown model "+req.Model+"; models: "+strings.Join(s.deps.ListPublicModels(), ", "))
		return
	}
	// Task 15/16 在此接入 failover。
	rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
	writeErrOpenAI(w, 500, protocol.ErrTypeAPI, "responses handler not wired")
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/gateway -run TestResponses -v`
Expected: 3 个测试 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/gateway/responses.go internal/gateway/server.go internal/gateway/gateway_test.go
git commit -m "feat(gateway): 注册 /v1/responses 并落鉴权与错误骨架

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 15: handler 接入 openai 渠道（直通 + 流/非流转换）

**Files:**
- Modify: `internal/gateway/responses.go`
- Test: `internal/gateway/gateway_test.go`

**Interfaces:**
- Consumes: `protocol.ResponsesRequestToChat`、`newOpenAIPassthrough`、`protocol.ChatStreamToResponsesStream`、`protocol.ChatCompletionToResponses`
- Produces: 完整 failover 循环（openai 分支）

- [ ] **Step 1: 写失败测试**

```go
func TestResponsesOpenAIChannelNonStream(t *testing.T) {
	var gotModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		gotModel, _ = body["model"].(string)
		if _, ok := body["messages"]; !ok {
			t.Errorf("upstream must receive chat messages: %+v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":1,
			"choices":[{"index":0,"message":{"role":"assistant","content":"pong"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":3,"completion_tokens":1,"total_tokens":4}}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{
				TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1", ChannelName: "oa",
				SecretKey: "account:k1", Protocol: "openai", BaseURL: up.URL,
				AllowPrivate: true, UpstreamModel: "up-1", Priority: 10,
			}}, nil
		}
	})
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi"}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || gotModel != "up-1" {
		t.Fatalf("status=%d model=%q body=%s", resp.StatusCode, gotModel, b)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil || out["object"] != "response" {
		t.Fatalf("must be Responses shape: %s", b)
	}
	if out["model"] != "pub-1" {
		t.Fatalf("logical model echoed: %s", b)
	}
}

func TestResponsesOpenAIChannelStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte(`data: {"id":"c1","choices":[{"delta":{"content":"hi"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"c1","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer up.Close()
	s := newGW(t, up.URL, func(d *Deps) {
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{
				TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1", ChannelName: "oa",
				SecretKey: "account:k1", Protocol: "openai", BaseURL: up.URL,
				AllowPrivate: true, UpstreamModel: "up-1", Priority: 10,
			}}, nil
		}
	})
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi","stream":true}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	out := string(b)
	for _, want := range []string{"event: response.created", `"delta":"hi"`, "event: response.completed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/gateway -run 'TestResponsesOpenAIChannel' -v`
Expected: FAIL（500 handler not wired）。

- [ ] **Step 3: 实现**

把 Task 14 骨架中的占位替换为完整 failover（参照 `handleChatCompletions` 的超时写法）：

```go
	decision := s.deps.Router.Resolve(req.Model, cands)
	tc := protocol.NewResponseToolContext(req)
	if tc.DroppedTools() > 0 && s.deps.Log != nil {
		s.deps.Log.Debug("responses tools dropped", "request_id", rec.ID, "count", tc.DroppedTools())
	}
	lim := s.deps.Limits()
	ctx := r.Context()
	var cancel context.CancelFunc
	var headerTimer *time.Timer
	var headerTimedOut atomic.Bool
	if lim.RequestTimeout > 0 {
		if req.Stream {
			ctx, cancel = context.WithCancel(ctx)
			headerTimer = time.AfterFunc(lim.RequestTimeout, func() {
				headerTimedOut.Store(true)
				cancel()
			})
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

	lastClass := provider.ClassUnknown
	out, ferr := s.failover(ctx, decision, func(c router.Candidate) (bool, error) {
		apiKey, err := s.deps.Cred.Get(c.SecretKey)
		if err != nil {
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.ChannelID, provider.ClassAuth, 0, time.Now())
			}
			lastClass = "credential"
			return false, nil
		}
		chatBody, err := protocol.ResponsesRequestToChat(req, tc, c.UpstreamModel)
		if err != nil {
			rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
			writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
			return true, nil
		}
		var rawBody []byte
		var adapter provider.Adapter
		switch c.Protocol {
		case "openai":
			rawBody, adapter = chatBody, newOpenAIPassthrough()
		case "anthropic":
			lastClass = provider.ClassUnknown
			return false, nil // Task 16 实现
		default:
			lastClass = provider.ClassUnknown
			return false, nil
		}
		res, err := adapter.Do(ctx, provider.DoRequest{
			BaseURL: c.BaseURL, AllowPrivate: c.AllowPrivate, APIKey: string(apiKey),
			UpstreamModel: c.UpstreamModel, RawBody: rawBody, Stream: req.Stream,
		})
		if err != nil {
			if ctx.Err() != nil {
				return false, ctx.Err()
			}
			lastClass = provider.ClassConnect
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.ChannelID, provider.ClassConnect, 0, time.Now())
			}
			return false, nil
		}
		if res.StatusCode >= 400 {
			snippet, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
			res.Body.Close()
			class := provider.Classify(res.StatusCode, res.RetryAfterSec, snippet)
			s.logUpstreamError(rec.ID, c, res.StatusCode, class, snippet)
			if class == provider.ClassParam {
				rec.Status, rec.ErrorClass = res.StatusCode, string(class)
				rec.Account, rec.Channel = c.ChannelName, c.ChannelName
				writeUpstreamErrorOpenAI(w, res.StatusCode, snippet, res.Header)
				return true, nil
			}
			lastClass = class
			if s.deps.Cooldowns != nil {
				s.deps.Cooldowns.MarkClassified(c.ChannelID, class, res.RetryAfterSec, time.Now())
			}
			return false, nil
		}
		rec.Status = res.StatusCode
		rec.UpstreamModel, rec.Account, rec.Channel = c.UpstreamModel, c.ChannelName, c.ChannelName
		for _, h := range responseHeaderWhitelist {
			if v := res.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		if req.Stream && headerTimer != nil {
			headerTimer.Stop()
		}
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(res.StatusCode)
			src := protocol.ChatStreamToResponsesStream(ctx, res.Body, req.Model, tc)
			onIdle := protocol.ResponsesFailedEvent("upstream stream idle timeout")
			if copyStream(w, src, lim.RequestTimeout, onIdle) {
				rec.ErrorClass = "stream"
				s.logIdleTimeout(rec.ID, lim.RequestTimeout)
			}
			res.Body.Close()
			return true, nil
		}
		full, rerr := io.ReadAll(io.LimitReader(res.Body, 64<<20))
		res.Body.Close()
		if rerr != nil {
			lastClass = provider.ClassConnect
			return false, nil
		}
		converted, cerr := protocol.ChatCompletionToResponses(full, req.Model, tc)
		if cerr != nil {
			lastClass = provider.ClassUnknown
			return false, nil
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.StatusCode)
		w.Write(converted)
		return true, nil
	})
	switches = out.Switches
	if ferr != nil || out.Committed {
		if ferr != nil && !out.Committed && headerTimedOut.Load() {
			rec.Status, rec.ErrorClass = http.StatusGatewayTimeout, "timeout"
			writeErrOpenAI(w, http.StatusGatewayTimeout, protocol.ErrTypeAPI, "upstream response header timeout")
		}
		return
	}
	if !out.Attempted && (out.CoolSkips > 0 || len(decision.Filtered) > 0) && allCooling(decision.Filtered) {
		rec.Status, rec.ErrorClass = 503, "no_candidate"
		writeErrOpenAI(w, 503, protocol.ErrTypeAPI, coolingExhaustedMsg(decision.Filtered))
		return
	}
	rec.Status, rec.ErrorClass = 502, string(lastClass)
	writeErrOpenAI(w, 502, protocol.ErrTypeAPI, "all targets failed")
```

补 import：`context`、`io`、`sync/atomic`、`provider`、`router`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/gateway -run 'TestResponses' -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/gateway/responses.go internal/gateway/gateway_test.go
git commit -m "feat(gateway): Responses 入站接入 openai 直通渠道

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 16: handler 接入 anthropic 渠道（双跳转换）

**Files:**
- Modify: `internal/gateway/responses.go`
- Test: `internal/gateway/gateway_test.go`

- [ ] **Step 1: 写失败测试**

```go
func TestResponsesAnthropicChannelNonStream(t *testing.T) {
	var path string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","model":"u",
			"content":[{"type":"text","text":"pong"}],"stop_reason":"end_turn",
			"usage":{"input_tokens":3,"output_tokens":2}}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil) // 默认候选为 anthropic
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi"}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || path != "/v1/messages" {
		t.Fatalf("status=%d path=%q body=%s", resp.StatusCode, path, b)
	}
	if !strings.Contains(string(b), `"output_text"`) || !strings.Contains(string(b), "pong") {
		t.Fatalf("must convert anthropic to responses: %s", b)
	}
}

func TestResponsesAnthropicChannelStream(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"usage\":{\"input_tokens\":3}}}\n\n"))
		w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"))
		w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"))
		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi","stream":true}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	out := string(b)
	for _, want := range []string{"event: response.created", `"delta":"hi"`, "event: response.completed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q:\n%s", want, out)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/gateway -run 'TestResponsesAnthropicChannel' -v`
Expected: FAIL（504/502，anthropic 分支未实现）。

- [ ] **Step 3: 实现**

把 `case "anthropic"` 分支替换为：

```go
		case "anthropic":
			oa, perr := protocol.ParseOpenAIChatRequest(chatBody)
			if perr != nil {
				rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
				writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
				return true, nil
			}
			rawBody, err = protocol.OpenAIRequestToAnthropic(oa, c.UpstreamModel)
			if err != nil {
				rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
				writeErrOpenAI(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
				return true, nil
			}
			adapter, err = s.deps.AdapterFor("anthropic")
			if err != nil {
				lastClass = provider.ClassUnknown
				return false, nil
			}
```

流式分支按协议串联：

```go
		if req.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(res.StatusCode)
			var mid io.ReadCloser // anthropic 双跳的中间 chat 流，必须显式关闭
			var src io.ReadCloser
			if c.Protocol == "anthropic" {
				mid = protocol.AnthropicStreamToOpenAIStream(ctx, res.Body, req.Model)
				src = protocol.ChatStreamToResponsesStream(ctx, mid, req.Model, tc)
			} else {
				src = protocol.ChatStreamToResponsesStream(ctx, res.Body, req.Model, tc)
			}
			onIdle := protocol.ResponsesFailedEvent("upstream stream idle timeout")
			if copyStream(w, src, lim.RequestTimeout, onIdle) {
				rec.ErrorClass = "stream"
				s.logIdleTimeout(rec.ID, lim.RequestTimeout)
			}
			if mid != nil {
				mid.Close() // 解除中间泵写端阻塞，避免 goroutine 泄漏
			}
			res.Body.Close()
			return true, nil
		}
```

非流式分支在读取后按协议中转：

```go
		chatJSON := full
		if c.Protocol == "anthropic" {
			var cerr error
			chatJSON, cerr = protocol.AnthropicResponseToOpenAI(full, req.Model)
			if cerr != nil {
				lastClass = provider.ClassUnknown
				return false, nil
			}
		}
		converted, cerr := protocol.ChatCompletionToResponses(chatJSON, req.Model, tc)
```

注意：`copyStream` 只关闭最外层 `src`。anthropic 串联有两段 pipe——最外层关闭后，中间 `AnthropicStreamToOpenAIStream` 的泵仍可能阻塞在向 pipe 写入；必须显式 `mid.Close()`，否则 goroutine 泄漏。最后再 `res.Body.Close()` 关闭上游，与 `handleChatCompletions` 现有写法一致。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/gateway -run 'TestResponses' -v`
Expected: 全部 PASS。

- [ ] **Step 5: 提交**

```bash
git add internal/gateway/responses.go internal/gateway/gateway_test.go
git commit -m "feat(gateway): Responses 入站接入 anthropic 渠道双跳转换

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 17: 故障转移、提交点与空闲超时回归

**Files:**
- Test: `internal/gateway/gateway_test.go`
- Modify: `internal/gateway/responses.go`（仅在测试暴露缺陷时）

- [ ] **Step 1: 写失败测试**

```go
func TestResponsesFailoverBeforeCommit(t *testing.T) {
	var calls1, calls2 int
	busy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls1++
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"message":"busy"}}`))
	}))
	defer busy.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls2++
		w.Write([]byte(`{"id":"msg_2","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer good.Close()
	tw, _ := twoCands(t, busy.URL, good.URL)
	s := newGW(t, busy.URL, tw)
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi"}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || calls1 != 1 || calls2 != 1 {
		t.Fatalf("status=%d calls1=%d calls2=%d body=%s", resp.StatusCode, calls1, calls2, b)
	}
	if strings.Count(string(b), `"object":"response"`) != 1 {
		t.Fatalf("must produce exactly one response: %s", b)
	}
}

func TestResponsesCommitNoReplay(t *testing.T) {
	var calls2 int
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls2++ }))
	defer good.Close()
	flaky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"usage\":{\"input_tokens\":1}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n"))
		w.(http.Flusher).Flush()
		panic(http.ErrAbortHandler) // 首 chunk 后断流（同 TestOpenAIInboundCommitNoReplay）
	}))
	defer flaky.Close()
	tw, _ := twoCands(t, flaky.URL, good.URL)
	s := newGW(t, flaky.URL, tw)
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi","stream":true}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if calls2 != 0 {
		t.Fatalf("must not replay after commit; second calls=%d", calls2)
	}
	if !strings.Contains(string(b), "event: response.failed") || strings.Contains(string(b), "event: response.completed") {
		t.Fatalf("truncation must fail: %s", b)
	}
}

func TestResponsesIdleTimeoutFails(t *testing.T) {
	var calls2 int
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls2++ }))
	defer good.Close()
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m1\",\"usage\":{\"input_tokens\":1}}}\n\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"x\"}}\n\n"))
		w.(http.Flusher).Flush()
		time.Sleep(2 * time.Second)
	}))
	defer slow.Close()
	tw, _ := twoCands(t, slow.URL, good.URL)
	s := newGW(t, slow.URL, func(d *Deps) {
		d.Limits = func() Limits { return Limits{MaxBodyBytes: 1 << 20, RequestTimeout: 100 * time.Millisecond} }
		tw(d)
	})
	resp := postResponses(t, s, `{"model":"pub-1","input":"hi","stream":true}`, "tok")
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "event: response.failed") || calls2 != 0 {
		t.Fatalf("idle timeout must fail without replay: calls2=%d %s", calls2, b)
	}
}
```

- [ ] **Step 2: 运行测试**

Run: `go test -race ./internal/gateway -run 'TestResponses(FailoverBeforeCommit|CommitNoReplay|IdleTimeoutFails)' -v`
Expected: PASS；若失败按报错修 `responses.go`（不得改测试预期）。

- [ ] **Step 3: 全量回归**

Run: `go test -race ./... && go vet ./...`
Expected: 全部 PASS。

- [ ] **Step 4: 提交**

```bash
git add internal/gateway/responses.go internal/gateway/gateway_test.go
git commit -m "test(gateway): Responses 故障转移与流式收口回归

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 18: Codex 接入配置写入 responses

**Files:**
- Modify: `internal/bridge/accessconfigs.go:417-481`
- Test: `internal/bridge/accessconfigs_test.go:88-110`

- [ ] **Step 1: 改断言（先让测试失败）**

把现有断言 `wire_api != "chat"` 改为 `wire_api != "responses"`，并在注释中写明 Codex 0.143+ 只支持 responses。

Run: `go test ./internal/bridge -run TestCodexLiveProjectionPreservesAndMerges -v`
Expected: FAIL（实际仍写 chat）。

- [ ] **Step 2: 实现**

`writeCodexLive` 中：

```go
	agent["base_url"] = env["OPENAI_BASE_URL"]
	agent["wire_api"] = "responses"
	agent["requires_openai_auth"] = false
```

同步更新函数注释（第 424 行附近）为 `wire_api: "responses"（Codex 0.143+ 仅支持 Responses 线路）`。

- [ ] **Step 3: 运行测试**

Run: `go test ./internal/bridge -v`
Expected: 全部 PASS。

- [ ] **Step 4: 提交**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/accessconfigs_test.go
git commit -m "fix(bridge): Codex 接入配置改用 wire_api=responses

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 19: 文档同步（README 与 CLAUDE.md）

**Files:**
- Modify: `README.md:53`、`README.md:71`
- Modify: `CLAUDE.md`（「当前形态」段落与接入配置段落）

- [ ] **Step 1: 更新 README**

第 53 行受管键说明改为：

```markdown
config.toml 受管键：`model`、`model_provider = "agentswitch"`、`[model_providers.agentswitch]`（base_url / wire_api="responses" / requires_openai_auth=false）；auth.json 受管键 `OPENAI_API_KEY`。流量走网关 Responses 入站 `POST /v1/responses`（Codex 0.143+ 只支持该线路；旧配置需在接入页重新「应用」一次）。
```

第 71 行入站表新增一行：

```markdown
| OpenAI Responses | `POST /v1/responses` | Codex CLI 0.143+ |
```

- [ ] **Step 2: 更新 CLAUDE.md**

「网关**双入站**」改为「网关**三入站**（Anthropic `POST /v1/messages` + OpenAI Chat `POST /v1/chat/completions` + OpenAI Responses `POST /v1/responses`）」，并在接入配置段落末尾追加：

```markdown
**Responses 入站（2026-10-23）**：Codex CLI 0.143 起移除 `wire_api="chat"`，网关新增 `POST /v1/responses` 入站；渠道协议仍为 anthropic/openai(chat)，请求与响应在 `internal/protocol/responses*.go` 双向转换；工具映射对齐 cc-switch（function/custom/namespace/tool_search，web_search 丢弃）；Codex 接入配置写 `wire_api="responses"`。设计见 `docs/superpowers/specs/2026-10-23-openai-responses-inbound-design.md`，实现计划见 `docs/superpowers/plans/2026-10-23-openai-responses-inbound.md`。
```

- [ ] **Step 3: 校验**

Run: `grep -n "responses" README.md CLAUDE.md | head -20`
Expected: 能看到新增说明。

- [ ] **Step 4: 提交**

```bash
git add README.md CLAUDE.md
git commit -m "docs: 记录 Responses 入站与 Codex wire_api=responses

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

### Task 20: 真实 Codex 端到端验证（受控，默认跳过）

**Files:**
- Create: `internal/gateway/codex_e2e_test.go`

- [ ] **Step 1: 写测试**

```go
//go:build e2e

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"AgentSwitch/internal/router"
)

// TestCodexCLIResponsesToolRoundTrip 用真实 codex 二进制跑一次工具调用往返。
// 运行：CODEX_E2E=1 go test -tags e2e ./internal/gateway -run TestCodexCLIResponsesToolRoundTrip -v
func TestCodexCLIResponsesToolRoundTrip(t *testing.T) {
	if os.Getenv("CODEX_E2E") != "1" {
		t.Skip("set CODEX_E2E=1 to run")
	}
	codexPath, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex not installed")
	}
	var turns int
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.NotFound(w, r)
			return
		}
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		turns++
		w.Header().Set("Content-Type", "text/event-stream")
		if turns == 1 {
			w.Write([]byte(`data: {"id":"c1","choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"exec_command","arguments":"{\"cmd\":\"echo hi\"}"}}]}}]}` + "\n\n"))
			w.Write([]byte(`data: {"id":"c1","choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
			return
		}
		w.Write([]byte(`data: {"id":"c2","choices":[{"delta":{"content":"done"}}]}` + "\n\n"))
		w.Write([]byte(`data: {"id":"c2","choices":[{"delta":{},"finish_reason":"stop"}]}` + "\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer up.Close()

	s := newGW(t, up.URL, func(d *Deps) {
		d.Candidates = func(string) ([]router.Candidate, error) {
			return []router.Candidate{{
				TargetID: "t1", LogicalModel: "pub-1", ChannelID: "c1", ChannelName: "fake",
				SecretKey: "account:k1", Protocol: "openai", BaseURL: up.URL,
				AllowPrivate: true, UpstreamModel: "up-1", Priority: 10,
			}}, nil
		}
	})
	gw := httptest.NewServer(s.Handler())
	defer gw.Close()

	home := t.TempDir()
	work := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".codex"), 0o755)
	os.WriteFile(filepath.Join(home, ".codex", "config.toml"), []byte(`
model = "pub-1"
model_provider = "agentswitch"

[model_providers.agentswitch]
base_url = "`+gw.URL+`/v1"
wire_api = "responses"
requires_openai_auth = false
`), 0o600)
	os.WriteFile(filepath.Join(home, ".codex", "auth.json"), []byte(`{"OPENAI_API_KEY":"tok"}`), 0o600)

	cmd := exec.Command(codexPath, "exec", "--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox", "run echo hi")
	cmd.Dir = work
	cmd.Stdin = strings.NewReader("") // 避免 codex exec 阻塞在「Reading additional input from stdin...」
	cmd.Env = append(os.Environ(), "HOME="+home, "CODEX_HOME="+filepath.Join(home, ".codex"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("codex failed: %v\n%s", err, out)
	}
	if turns < 2 {
		t.Fatalf("expected tool round trip, turns=%d\n%s", turns, out)
	}
}
```

- [ ] **Step 2: 运行**

Run: `CODEX_E2E=1 go test -tags e2e ./internal/gateway -run TestCodexCLIResponsesToolRoundTrip -v`
Expected: PASS（本机 codex 0.143.0）。若失败，按输出修正网关事件形状，不得放宽断言。

- [ ] **Step 3: 提交**

```bash
git add internal/gateway/codex_e2e_test.go
git commit -m "test(gateway): 真实 Codex CLI Responses 工具往返验证

Co-Authored-By: Claude Fable 5 <noreply@anthropic.com>"
```

---

## 完成检查

- [ ] `go test -race ./...` 全量通过。
- [ ] `go vet ./...` 无输出。
- [ ] `CODEX_E2E=1 go test -tags e2e ./internal/gateway -run TestCodexCLIResponsesToolRoundTrip -v` 通过。
- [ ] 手工验收：桌面端对 Codex 接入配置点一次「应用」，确认 `~/.codex/config.toml` 的 `wire_api` 为 `responses`，随后 `codex` 可启动并完成一次工具调用。
- [ ] README/CLAUDE.md 已同步。
