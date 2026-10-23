# 渠道预置配置文件化 + 双协议联动 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 预置外置为 embed JSON + 用户覆盖文件，每家供应商按 openai/anthropic 双协议组织 base_url，表单切协议自动联动，新增 6 家 Coding Plan 订阅预置，出站透传客户端 UA。

**Architecture:** `internal/presets` 从硬编码静态表改为「embed 默认 + `~/.agentswitch/presets.json` 合并」的包内只读状态；bridge/desktop 消费新双协议结构；网关 `DoRequest` 增加 `UserAgent` 字段由入站透传到三处出站。不涉及 storage schema 与路由运行时行为。

**Tech Stack:** Go 1.27.2 + `//go:embed` + encoding/json + MyGo v0.6.2（`ui.Select(...).Changed()`）

**Spec:** `docs/superpowers/specs/2026-10-23-channel-presets-config-design.md`（§6 查证表是唯一数据来源，本计划不复述查证注释）

## Global Constraints

- 测试全部离线：httptest / Fake Provider，绝不打真实上游。
- 涉并发的测试跑 `-race`；presets 包状态约定「启动加载一次、此后只读」，不加锁。
- UI 文案中文；API Key 永不入 presets.json、日志或警告文本。
- 预置 base_url/模型名一律照抄 spec §6 表格与 §3 示例，禁止凭记忆改动。
- 渠道协议枚举仅 `"openai"` / `"anthropic"`，未知协议键跳过。
- 每任务以 `go build ./... && go vet ./...` 收尾无错再 commit（下述「验证」均含此步）。

## Review Focus

1. **anthropic base 拼接回归**（`/coding/` 尾斜杠、无 `/v1` 路径形态拼 `/v1/messages`、OpenRouter 已含 `/v1` 只拼 `/messages`）→ Task 1 特征测试全表锁死。
2. **用户文件单条坏**（id 空 / protocols 空 / 协议键未知）只跳过该条，不炸整表 → Task 4。
3. **用户文件整体语法坏** → 降级内嵌默认且 `Warning()` 非空 → Task 4。
4. **协议往返**：切到无预设协议再切回，空值须恢复预设（不得误判「用户已手改」）→ Task 7。
5. **客户端无 UA 头** → 出站回落 Go 默认 UA，不得发出空 `User-Agent:` 头 → Task 9/10。

---

### Task 1: anthropicEndpoint 全表特征测试护栏

**Files:**
- Test: `internal/provider/provider_test.go`（追加）

**Interfaces:**
- Consumes: `anthropicEndpoint(baseURL string) (string, error)`（已存在，provider 包内）
- Produces: 无（纯测试，锁死 spec §6 全部 anthropic base_url 的拼接行为）

- [ ] **Step 1: 写测试 `TestAnthropicEndpointAllPresetBases`**

表驱动，断言「完整端点 URL」字段，行覆盖 spec §6 全部含 anthropic 协议的条目（16 个 base，含订阅）：

```go
cases := []struct{ base, want string }{
	// 按量（base 无 /v1 → 拼 /v1/messages）
	{"https://api.xiaomimimo.com/anthropic", "https://api.xiaomimimo.com/anthropic/v1/messages"},
	{"https://dashscope.aliyuncs.com/apps/anthropic", "https://dashscope.aliyuncs.com/apps/anthropic/v1/messages"},
	{"https://api.deepseek.com/anthropic", "https://api.deepseek.com/anthropic/v1/messages"},
	{"https://api.moonshot.cn/anthropic", "https://api.moonshot.cn/anthropic/v1/messages"},
	{"https://open.bigmodel.cn/api/anthropic", "https://open.bigmodel.cn/api/anthropic/v1/messages"},
	{"https://api.siliconflow.cn", "https://api.siliconflow.cn/v1/messages"},
	{"https://api.stepfun.com", "https://api.stepfun.com/v1/messages"},
	{"https://ark.cn-beijing.volces.com/api/compatible", "https://ark.cn-beijing.volces.com/api/compatible/v1/messages"},
	{"https://api.anthropic.com", "https://api.anthropic.com/v1/messages"},
	// base 已含 /v1 → 只拼 /messages
	{"https://openrouter.ai/api/v1", "https://openrouter.ai/api/v1/messages"},
	// 订阅（Coding Plan）
	{"https://ark.cn-beijing.volces.com/api/coding", "https://ark.cn-beijing.volces.com/api/coding/v1/messages"},
	{"https://api.kimi.com/coding/", "https://api.kimi.com/coding/v1/messages"}, // 尾斜杠
	{"https://coding.dashscope.aliyuncs.com/apps/anthropic", "https://coding.dashscope.aliyuncs.com/apps/anthropic/v1/messages"},
	{"https://api.stepfun.com/step_plan", "https://api.stepfun.com/step_plan/v1/messages"},
	{"https://token-plan-cn.xiaomimimo.com/anthropic", "https://token-plan-cn.xiaomimimo.com/anthropic/v1/messages"},
	// 智谱订阅与按量 anthropic 同址，不重复列
}
```

（同文件测试函数若在 `provider_test.go` 且 `anthropicEndpoint` 未导出——测试同包直调即可。）

- [ ] **Step 2: 运行验证**

Run: `go test ./internal/provider -run TestAnthropicEndpointAllPresetBases -v`
Expected: **PASS**（2026-10-22 小米修复后实现已正确；这是防回归护栏，若 FAIL 先按 spec 修正 `anthropicEndpoint` 再继续）

- [ ] **Step 3: Commit**

```bash
git add internal/provider/provider_test.go
git commit -m "test(provider): 锁死全量预置 anthropic base_url 的端点拼接"
```

---

### Task 2: presets 双协议结构 + 加载器 + 全链路编译适配

**Files:**
- Modify: `internal/presets/channels.go`（结构重写 + 加载器）
- Modify: `internal/bridge/presets.go`（PresetView 双协议化）
- Modify: `internal/desktop/channels.go`（`applyPreset` 等最小适配，完整语义留 Task 5）
- Test: `internal/presets/channels_test.go`（重写）、`internal/bridge/presets_test.go`（适配）

**Interfaces:**
- Produces（后续任务依赖的精确签名）:

```go
// internal/presets/channels.go
type ProtocolEndpoint struct {
	BaseURL       string   `json:"baseUrl"`
	DefaultModels []string `json:"defaultModels"`
}
type ChannelPreset struct {
	ID          string                      `json:"id"`
	DisplayName string                      `json:"displayName"`
	DocURL      string                      `json:"docUrl"`
	Protocols   map[string]ProtocolEndpoint `json:"protocols"` // 键: "openai"|"anthropic"
}
func Load(embedded []byte, userFile string) // 解析+校验+合并，更新包内表与警告；启动调用一次，此后只读
func All() []ChannelPreset
func ById(id string) (ChannelPreset, bool)
func Warning() string // 最近一次 Load 的降级/跳过警告，空=无
```

- Consumes: `provider.ValidateBaseURL(url, false)`（内嵌数据条目校验；用户文件分档在 Task 4）

**说明（超 5 分钟的原因）**：结构体字段变更牵动 bridge/desktop 全部引用点的编译修复与既有测试适配，属同一「数据结构双协议化」变更的机械部分，不可拆。

- [ ] **Step 1: 重写 `internal/presets/channels_test.go` 失败测试**

`TestLoadEmbeddedOnly`：Load(两三条 fixture JSON 字节, "")（用户文件空）→ All() 返回该表、Warning()==""；fixture 含一条双协议（qwen 形态）与一条仅 openai。
`TestLoadSkipsInvalidEntries`：fixture 混入 id 空、protocols `{}`、协议键 `"grpc"` 三条坏条目 → All() 只剩好条目、Warning() 含被跳过条目说明。
`TestById`：命中与未命中。
`TestLoadEmbeddedMustPassValidateBaseURL`：fixture 一条 `baseUrl: "ftp://x"` → 被跳过 + Warning() 非空。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/presets -v`
Expected: FAIL（新 API 未定义）

- [ ] **Step 3: 重写 `internal/presets/channels.go`**

删除旧静态表与旧 `All/ById`；实现 Interfaces 块签名。要点：包内 `var (table []ChannelPreset; warn string)`；Load 用 `json.Unmarshal` 到 `[]ChannelPreset`（Unmarshal 后逐条校验：id 非空、Protocols 非空、键仅 openai/anthropic、每条 baseUrl 过 `ValidateBaseURL(_, false)`，坏条目跳过并拼进 warn）；本任务暂以「内嵌字节解析结果」为 table（`//go:embed` 数据文件在 Task 3 落地，本任务 Load 的 embedded 参数由测试传 fixture）。
bridge：`PresetView` 改 `{ID, DisplayName, DocURL string; Protocols map[string]presets.ProtocolEndpoint}`，`List()` 透传 `presets.All()`。
desktop `channels.go` 最小适配：`applyPreset` 暂取 `Protocols["openai"]`（无则 anthropic，再无则不动）回填 protocol/baseURL/suggestions；渲染处编译修复（presetTile 不变，仍只显示 ID/DisplayName）。

- [ ] **Step 4: 跑测试通过 + 全链路编译**

Run: `go test ./internal/presets ./internal/bridge ./internal/desktop -v && go build ./...`
Expected: PASS（desktop 现有测试若断言旧单协议字段，同步改为新结构断言）

- [ ] **Step 5: Commit**

```bash
git add internal/presets internal/bridge internal/desktop
git commit -m "feat(presets): 渠道预置双协议结构与 JSON 加载器"
```

---

### Task 3: default.json 20 条查证数据 + embed + 启动接线

**Files:**
- Create: `internal/presets/default.json`
- Modify: `internal/presets/channels.go`（embed 变量 + `DefaultJSON()`）
- Modify: `internal/app/app.go`（启动时 Load，用户路径 `~/.agentswitch/presets.json`；Task 4 前传 ""）
- Test: `internal/presets/channels_test.go`（追加）

**Interfaces:**
- Produces: `func DefaultJSON() []byte`（embed 访问器）
- Consumes: Task 2 的 `Load`；`os.UserHomeDir()`（bridge/accessconfigs.go 已有同款用法）

**说明（约 5-8 分钟）**：数据转录 20 条是 spec §6 钉死的值，无决策空间但体量大。

- [ ] **Step 1: 写失败测试 `TestDefaultJSONTwentyEntries`**

Load(DefaultJSON(), "") 后断言：len(All())==20；id 顺序恰为 `xiaomi,qwen,deepseek,kimi,zhipu,siliconflow,stepfun,doubao,anthropic,openai,google,groq,together,openrouter,zhipu-coding,doubao-coding,kimi-coding,qwen-coding,stepfun-coding,xiaomi-coding`；每条至少一协议、每 baseUrl 过 `ValidateBaseURL(_, false)`；抽查 3 条端点值逐字相等（xiaomi 双协议、openai-coding 的 anthropic 为 `https://api.kimi.com/coding/`、openrouter 双协议同址）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/presets -run TestDefaultJSONTwentyEntries -v`
Expected: FAIL（default.json 不存在）

- [ ] **Step 3: 写 `internal/presets/default.json`（数据照抄 spec §6 两张表）**

14 条按量 + 6 条订阅，字段 id/displayName/docUrl/protocols；docUrl 沿用现有值，仅两处更新：小米改 `https://mimo.mi.com/`、Together 改 `https://docs.together.ai/`；订阅条目 docUrl 用各自官方文档根（zhipu-coding→`https://docs.bigmodel.cn/cn/coding-plan`、doubao-coding→`https://www.volcengine.com/docs/ark/coding-plan-personal-get-started`、kimi-coding→`https://www.kimi.com/code/docs`、qwen-coding→`https://help.aliyun.com/zh/model-studio/coding-plan`、stepfun-coding→`https://platform.stepfun.com/docs/zh/step-plan/quick-start`、xiaomi-coding→`https://mimo.mi.com/docs/tokenplan/integration/claudecode`）。条目内 protocols 键序 openai 在前（纯为可读，加载不依赖键序）。

- [ ] **Step 4: embed 与启动接线**

channels.go：`//go:embed default.json` + `var defaultJSON []byte` + `DefaultJSON()`。app.go 启动处调 `presets.Load(presets.DefaultJSON(), "")`（用户文件路径 Task 4 接）。

- [ ] **Step 5: 跑测试通过**

Run: `go test ./internal/presets -v && go build ./... && go test ./... -count=1`
Expected: PASS；若 bridge/desktop 既有测试硬编码旧条数（14）改为 20 或改不断言条数

- [ ] **Step 6: Commit**

```bash
git add internal/presets internal/app
git commit -m "feat(presets): 内嵌 20 条双协议预置默认表（14 按量+6 Coding Plan）"
```

---

### Task 4: 用户文件合并与放宽校验

**Files:**
- Modify: `internal/presets/channels.go`（Load 的 userFile 分支）
- Modify: `internal/app/app.go`（用户路径 `filepath.Join(home, ".agentswitch", "presets.json")` 接入）
- Test: `internal/presets/channels_test.go`（追加）

**Interfaces:**
- Consumes: Task 2 `Load(embedded, userFile)` 签名不变，实现用户文件分支
- Produces: 合并语义（spec §4）：同 id 整体覆盖原位、新 id 追加末尾；文件不存在→只用内嵌；解析失败→整体降级+Warning；用户文件条目校验放宽（不跑 ValidateBaseURL，仅 `url.Parse` 成功且 scheme/host 非空，http/私网/localhost 放行）

- [ ] **Step 1: 写失败测试（4 个）**

`TestUserFileOverrideAndAppend`：t.TempDir 写用户文件（覆盖 qwen 整条 + 新增 `"myrelay"` 私网条目 `http://192.168.1.10:3000`）→ Load(内嵌, 用户路径)：qwen 位在原序且值为用户版、myrelay 在末尾、Warning()==""（私网用户条目不告警）。
`TestUserFileGarbageDegrades`：用户文件写 `{{{` → All()==内嵌表、Warning() 非空。
`TestUserFileMissing`：路径指向不存在文件 → 等于内嵌表、Warning()==""。
`TestUserFileSkipBadEntries`：用户文件混 id 空条目 → 该条跳过、其余生效、Warning() 非空。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/presets -run TestUserFile -v`
Expected: FAIL

- [ ] **Step 3: 实现 Load 用户文件分支**

读文件→`json.Unmarshal` 失败整体降级（warn 记「用户预置文件解析失败，已忽略：err」）；逐条校验（同 Task 2 规则但 baseUrl 只做 url.Parse+scheme/host，不做 ValidateBaseURL）；合并：map[id]→index 定位覆盖，否则 append。app.go 接真实路径（home 取不到时传 ""）。

- [ ] **Step 4: 跑测试通过**

Run: `go test ./internal/presets ./internal/app -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/presets internal/app
git commit -m "feat(presets): 用户预置文件覆盖/追加合并与降级容错"
```

---

### Task 5: bridge 警告透传 + UI 网格顶部提示

**Files:**
- Modify: `internal/bridge/presets.go`（加 `Warning()` 桥接方法）
- Modify: `internal/desktop/channels.go`（channelNewView 预设网格顶部一行警告文本）
- Test: `internal/bridge/presets_test.go`（追加）

**Interfaces:**
- Consumes: Task 2 `presets.Warning()`、Task 4 的警告语义（降级/跳过后非空）
- Produces: `func (Presets) Warning() string`（透传 `presets.Warning()`）；网格顶部当返回值非空时渲染一行 `t.Danger` 色文本（MyGo theme 沿用现有错误文案样式），空则不渲染

- [ ] **Step 1: 写失败测试 `TestPresetsWarningBridge`**

bridge `Presets{}.Warning()` 透传断言：先 `presets.Load(坏 fixture, "")` 使 Warning() 非空 → 桥接返回同值；再 Load 干净数据 → 返回 ""。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run TestPresetsWarningBridge -v`
Expected: FAIL（方法未定义）

- [ ] **Step 3: 实现桥接方法与 UI 一行**

- [ ] **Step 4: 跑测试通过**

Run: `go test ./internal/bridge ./internal/desktop -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/presets.go internal/desktop/channels.go internal/bridge/presets_test.go
git commit -m "feat(bridge): 预置加载警告透传并在预设网格顶部提示"
```

---

### Task 6: applyPreset 第一可用协议回填

**Files:**
- Modify: `internal/desktop/channels.go:36-41`（applyPreset）
- Test: `internal/desktop/channels_test.go`（追加/改造）

**Interfaces:**
- Consumes: Task 2 `presets.ChannelPreset`（Protocols map）、既有的 `channelForm`（channels.go:21-32）
- Produces: `applyPreset(form *channelForm, p presets.ChannelPreset)` 语义（spec §5）：presetID=p.ID；protocol=第一可用协议（`"openai"` 先于 `"anthropic"` 固定序）；baseURL/suggestions=该协议预设；`name` 仅当为空时回填 p.DisplayName

- [ ] **Step 1: 写失败测试 `TestApplyPresetDualProtocol`**

三例：双协议预置（断言落 openai 值与 protocol=="openai"）；仅 anthropic 预置（落 anthropic）；name 已填「我的中转」不覆盖 + name 空时回填 DisplayName。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run TestApplyPresetDualProtocol -v`
Expected: FAIL

- [ ] **Step 3: 实现 applyPreset 完整语义**

固定序 `for _, proto := range []string{"openai", "anthropic"}` 取第一个存在的键。

- [ ] **Step 4: 跑测试通过**

Run: `go test ./internal/desktop -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/channels.go internal/desktop/channels_test.go
git commit -m "feat(desktop): 预设回填取第一可用协议并保留已填名称"
```

---

### Task 7: 协议下拉联动 + 手改保护 + 空值恢复

**Files:**
- Modify: `internal/desktop/channels.go`（新增 `switchProtocol`；`ui.Select` 接线约 :310）
- Test: `internal/desktop/channels_test.go`（追加）

**Interfaces:**
- Consumes: `presets.ById`、Task 5 的 channelForm
- Produces: `switchProtocol(form *channelForm, p presets.ChannelPreset, newProto string)` 规则（spec §5）：目标协议在 p 中→当 `form.baseURL == "" || 等于 p 任一协议的预设 baseURL` 时替换为目标协议预设值（suggestions 同规则，比较值为各协议 `strings.Join(DefaultModels,"\n")`）；目标协议不在 p 中→同条件下清空 baseURL/suggestions
- 渲染接线：`el := ui.Select(c, &u.chForm.protocol, []string{"openai","anthropic"}); if el.Changed() { if p, ok := presets.ById(u.chForm.presetID); ok { switchProtocol(&u.chForm, p, u.chForm.protocol) } }`

- [ ] **Step 1: 写失败测试 `TestSwitchProtocolRules`**

五例：①双协议 openai→anthropic 值替换；②手改保护（baseURL 已人为改为第三值→切协议不动）；③空值恢复（baseURL 被清空后切回→恢复预设）；④预置无目标协议（仅 openai 预置切 anthropic→baseURL/suggestions 清空）；⑤suggestions 手改后切协议不被覆盖。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run TestSwitchProtocolRules -v`
Expected: FAIL

- [ ] **Step 3: 实现 switchProtocol + 接线**

判定函数一行思路：`matchAny := func(v string, cands ...string) bool { v=="" 或 v∈cands }`，baseURL 与 suggestions 各自判定，互不牵连。

- [ ] **Step 4: 跑测试通过**

Run: `go test ./internal/desktop -v -count=1 && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/channels.go internal/desktop/channels_test.go
git commit -m "feat(desktop): 协议切换联动预设 base_url 与模型建议（手改保护）"
```

---

### Task 8: stripOneM 小写 [1m] 断言

**Files:**
- Test: `internal/gateway/handlers_test.go`（追加 case）

**Interfaces:**
- Consumes: `stripOneM(model string) string`（handlers.go:32，已 `EqualFold`）

- [ ] **Step 1: 追加断言 case**

在 stripOneM 既有测试表加两行：`"glm-5.3-flash[1m]" → "glm-5.3-flash"`、`"my-code[1M]" → "my-code"`（大小写各一）。

- [ ] **Step 2: 运行验证**

Run: `go test ./internal/gateway -run TestStripOneM -v`
Expected: **PASS**（已兼容，特征测试；FAIL 则修 stripOneM）

- [ ] **Step 3: Commit**

```bash
git add internal/gateway/handlers_test.go
git commit -m "test(gateway): 锁死 [1m] 小写后缀剥离（智谱 Coding Plan 模型名）"
```

---

### Task 9: DoRequest.UserAgent + 两 adapter 出站设置

**Files:**
- Modify: `internal/provider/provider.go:13-20`（DoRequest 加字段）
- Modify: `internal/provider/anthropic.go:86-91`、`internal/provider/openai.go:53-56`（出站设置）
- Test: `internal/provider/provider_test.go`（追加）

**Interfaces:**
- Produces: `DoRequest` 新字段 `UserAgent string`；两 adapter Do 在构造 req 后 `if r.UserAgent != "" { req.Header.Set("User-Agent", r.UserAgent) }`（空则不设，保持 Go 默认）
- Consumes: 既有 httptest Fake 上游模式（provider_test 已有）

- [ ] **Step 1: 写失败测试 `TestUserAgentPassthroughAdapters`**

httptest 上游收请求断言 `User-Agent == "claude-cli/2.0.0 (test)"`（anthropic 与 openai 两 adapter 各一例）；`UserAgent: ""` 时上游收到 Go 默认 `Go-http-client/1.1`（断言非空即可）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/provider -run TestUserAgentPassthrough -v`
Expected: FAIL（字段不存在）

- [ ] **Step 3: 实现字段与两处设置**

- [ ] **Step 4: 跑测试通过**

Run: `go test ./internal/provider -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider
git commit -m "feat(provider): 出站请求透传客户端 User-Agent"
```

---

### Task 10: gateway 入站 UA 传递 + openaiPassthrough

**Files:**
- Modify: `internal/gateway/handlers.go`（入站 handler 组装 DoRequest 处填 `UserAgent: r.Header.Get("User-Agent")`；openaiPassthrough.Do 约 :579 同步设置）
- Test: `internal/gateway/handlers_test.go`（追加端到端）

**Interfaces:**
- Consumes: Task 8 `DoRequest.UserAgent`
- Produces: 端到端行为：入站 UA → Fake 上游同值；入站无 UA → 上游收到 Go 默认（无空 UA 头）

- [ ] **Step 1: 写失败测试 `TestGatewayUserAgentEndToEnd`**

既有 Fake Provider 端到端用例基础上：入站带 `User-Agent: claude-cli/2.0.0 (test)` → Fake 上游记录断言同值；入站不带 → 上游 UA 非空。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/gateway -run TestGatewayUserAgent -v`
Expected: FAIL

- [ ] **Step 3: 实现传递（含 openaiPassthrough 同款 Set）**

DoRequest 组装点集中一处则改一处；openaiPassthrough.Do 加同 Task 8 的条件设置。

- [ ] **Step 4: 跑测试通过**

Run: `go test ./internal/gateway -v -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway
git commit -m "feat(gateway): 入站 User-Agent 透传至上游（Kimi Code 合规）"
```

---

### Task 11: 收尾——CLAUDE.md 记录 + 全量验证

**Files:**
- Modify: `CLAUDE.md`（「项目状态」追加一段：预置配置文件化/双协议/Coding Plan/UA 透传/[1m] 兼容，注明 spec 与 plan 路径，2026-10-23）

- [ ] **Step 1: CLAUDE.md 追加机制段**（一段以内，含 `~/.agentswitch/presets.json` 覆盖语义与「数据以 spec §6 查证表为准」）

- [ ] **Step 2: 全量验证**

Run: `go build ./... && go vet ./... && go test -race ./... -count=1`
Expected: 全绿

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: 记录渠道预置配置文件化与双协议联动"
```
