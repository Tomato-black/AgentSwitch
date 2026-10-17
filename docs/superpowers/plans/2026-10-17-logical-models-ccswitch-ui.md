# 逻辑模型 + cc-switch 式 UI 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把路由主体从「渠道·模型」改为用户自定义的有序「逻辑模型目标链」，存储删库重建，桌面 UI 重构为 cc-switch 式侧栏 + 卡片 + 弹窗，内置精选渠道预设，接入页支持 Claude Code / Codex。

**Architecture:** 存储层一次性重写 schema（删库重建，无 v2 迁移；旧 schema 启动报错）：`channels` 单 Key + `preset_id`、新增 `logical_models` / `logical_model_targets`；`channel_keys` 与旧渠道级故障转移路径删除。`app.candidates` 按逻辑模型名展开目标链，冷却键改为 ChannelID。网关双入站：既有 Anthropic `/v1/messages` + 新增 OpenAI `/v1/chat/completions`（共用故障转移核心）。桌面端改为侧栏导航（逻辑模型/渠道/接入/设置 + 底条网关状态），桥接层拆分 channels / logicalmodels / presets / access。

**Tech Stack:** Go 1.27、MyGo v0.5.1 原生视图、modernc.org/sqlite、OS Keychain（mygo.Secrets）。测试全部离线（httptest Fake Provider）。

**Spec:** `docs/superpowers/specs/2026-10-17-logical-models-ccswitch-ui-design.md`

## Global Constraints

- 一切测试离线：不打真实上游；`go test ./...` 全绿；并发/冷却相关测试必须 `go test -race` 通过。
- 删库重建：不写迁移 v2。`storage.Open` 检测到旧 schema（存在表 `channel_keys`）时返回明确错误（提示删除 `agentswitch.db`）；**不**静默清库。schema 只保留一份最终形态（直接改写现有 `migrationV1`——本项目尚未对外发版承诺）。
- 主键一律沿用 TEXT `NewID()`（crypto/rand hex），**不**引入 INTEGER PK。
- 每渠道恰好一个 API Key：OS 钥匙串 `credential_ref`；SQLite 无明文密钥。渠道表保留 `allow_private_upstream`，新增 `preset_id TEXT NOT NULL DEFAULT ''`。
- 遥测不落 SQLite：仅内存环 + 文件日志；不新建事件表。
- 网关双入站：`POST /v1/messages`（Anthropic，已有）+ `POST /v1/chat/completions`（OpenAI，本计划新增）；共用逻辑模型/冷却/故障转移核心。
- 日志/UI/错误禁止出现完整 Key、Authorization、完整 Prompt。
- `base_url` 校验沿用 `provider.ValidateBaseURL`（SSRF）；新增 `allow_private_upstream` 仅显式开启放行。
- 首字节（响应头/任何流式事件）提交前才允许跨目标切换；提交后绝不重放。
- 未登记模型名默认拒绝（明确错误）；设置项 `routing.passthrough_unregistered` 默认 "0"。
- UI 全部 MyGo 原生视图（`internal/desktop`）；设计 token 沿用 `internal/desktop/theme.go`。
- 通信：`mygo.Bind` 仅绑定桥接对象；桌面进程内直调需保持可测。
- 旧代码直接删除：`channel_keys` 全部、旧 overview/models 页、无调用方的 `ChannelKeyRepo.List`、死配置 `CredentialFallback` 不保留兼容层。
- 提交信息用中文短句 + conventional 前缀（feat/fix/refactor/test/docs）；每个 Task 末尾提交一次。

## Review Focus

以下输入/失败模式最容易在实现中被破坏，评审时优先核对：

1. **双逻辑模型指向同一渠道时冷却串扰**——`my-a` 打挂渠道 C 冷却后，`my-b` 的同渠道目标也必须被跳过（冷却键=ChannelID）；反向：同逻辑模型内两个不同渠道不得互相拖累。
2. **流式响应中途上游断开**——首字节已提交后的错误不得触发下一目标重放；必须原样把错误交给客户端。
3. **目标链顺序与过滤的可解释性**——`Decision.Filtered` 必须带上被滤目标及原因（cooling/disabled/missing-credential），耗尽时 502 响应体可区分「全部冷却」vs「全部失败」。
4. **渠道删除级联**——删除渠道时其 target 行必须消失；逻辑模型删空目标后仍可存在但路由时明确报错（无目标），UI 不崩溃。
5. **预设 base_url 必须能过 SSRF 校验**——内置预设若用私网/localhost 会导致「选了预设却保存失败」；每个预设有单测逐条 Validate。

---

### Phase A — 存储删库重建

#### Task 1: 重写 schema（channels 单 Key + logical models）

**Files:**
- Modify: `internal/storage/db.go`（替换 `migrationV1` SQL）
- Test: `internal/storage/storage_test.go`

**Interfaces:**
- Produces: 最终表结构（ID 均为 TEXT）：
  - `channels(id, name UNIQUE, protocol, base_url, credential_ref, preset_id DEFAULT '', allow_private_upstream DEFAULT 0, enabled DEFAULT 1, created_at, updated_at)`
  - `channel_models(id, channel_id FK CASCADE, model_name, created_at, UNIQUE(channel_id, model_name))`
  - `logical_models(id, name UNIQUE, description, enabled, created_at, updated_at)`
  - `logical_model_targets(id, logical_model_id FK CASCADE, channel_id FK CASCADE, model_name, priority, UNIQUE(logical_model_id, channel_id, model_name))`
  - `settings` 不变。
  - **无 `channel_keys`、无 `channels.priority`/`type`、`channel_models` 无 public/upstream/enabled。**

- [ ] **Step 1: 改写 storage_test.go 为新 schema 断言（失败先行）**

保留/改写为：
- `TestMigrationIdempotent`：仍打开两次 Open 成功。
- 新增 `TestMigrationFinalSchema`：Open 后用 `sqlite_master` 断言存在 `logical_models`、`logical_model_targets`，不存在 `channel_keys`；`channels` 含列 `credential_ref`、`preset_id`、`allow_private_upstream`，不含 `priority`。
- 新增 `TestOpenRejectsLegacySchema`：先手工建含 `channel_keys` 的库文件再 `Open`，断言返回错误且信息含「删除」。
- `channelFixture` 改为构造新 `Channel`（带 `CredentialRef`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/storage -run TestMigration -v`
Expected: FAIL（列/表不存在或编译失败）

- [ ] **Step 3: 替换 db.go 中 migrationV1 SQL**

按 Interfaces 的表结构写 DDL（TEXT 主键逻辑用 INTEGER PK AUTOINCREMENT 沿用现状即可；时间戳 INTEGER 毫秒沿用 `NewID` 旁现有约定——查看旧 SQL 里 created_at 类型保持一致）。删除 channel_keys 相关索引。

- [ ] **Step 4: 测试通过**

Run: `go test ./internal/storage -v`
Expected: 新断言 PASS（repos.go 尚未改则其余测试允许本 Task 暂时红——见 Step 5）

- [ ] **Step 5: 最小同步 structs/repos 编译并全绿**

同步修改 `storage.go` / `repos.go` 结构体与 Repo 接口至 Task 2 将定的形状（见下），删除 `ChannelKey`/`ChannelKeyRepo`；`ChannelModel` 简化为 `{ID, ChannelID, ModelName, CreatedAt}`；新增 `LogicalModel`/`LogicalModelTarget` 结构体与 Repo 接口**空实现骨架**（方法返回 ErrNotFound/nil 亦可，Task 2 补测试实现）。`go test ./internal/storage` 必须全绿。

- [ ] **Step 6: Commit**

```bash
git add internal/storage
git commit -m "feat(storage): 删库重建——channels 单 Key 与 logical models 表"
```

#### Task 2: Channel/ChannelModel 仓储新语义

**Files:**
- Modify: `internal/storage/storage.go`、`internal/storage/repos.go`
- Test: `internal/storage/storage_test.go`

**Interfaces:**
- Produces（后续 Task 依赖，签名固定）:
  - `Channel{ID, Name, Protocol, BaseURL, CredentialRef, PresetID string; Enabled, AllowPrivateUpstream bool; CreatedAt, UpdatedAt int64}`
  - `ChannelRepo{List() ([]Channel, error); Get(id string) (Channel, error); Save(c Channel) (Channel, error); Delete(id string) error; SetEnabled(id string, on bool) error; HasUsableKey() (bool, error)}`
  - `ChannelModel{ID, ChannelID, ModelName string; CreatedAt int64}`
  - `ChannelModelRepo{ListByChannel(channelID string) ([]ChannelModel, error); Save(m ChannelModel) (ChannelModel, error); Delete(id string) error; DeleteByChannel(channelID string) error; Suggestions(channelID string) ([]string, error)}`

- [ ] **Step 1: 写失败测试**

- `TestChannelSaveRoundTrip`：Save 含 CredentialRef=true 的渠道，Get 字段一致。
- `TestChannelDeleteCascadeModels`：Save 渠道 + 2 条 model suggestion，Delete 渠道后 `ListByChannel` 空。
- `TestChannelModelSuggestions`：Save 三条，Suggestions 返回名字列表。
- `TestHasUsableKey`：无渠道 false；有渠道 true。

- [ ] **Step 2: 确认失败**
Run: `go test ./internal/storage -run 'Channel|HasUsable' -v` → FAIL

- [ ] **Step 3: 在 repos.go 实现**

`dbStore` 上实现上述方法；`Save` 支持 insert/update（ID 空则 NewID）。删除 `DeleteByChannel` 之外若有旧 `SetEnabled` on models 的残留一并删。

- [ ] **Step 4: 通过**
Run: `go test ./internal/storage -v` → PASS

- [ ] **Step 5: Commit**
```bash
git commit -m "feat(storage): 渠道单 Key 仓储与模型建议列表"
```

#### Task 3: LogicalModel/Target 仓储

**Files:**
- Modify: `internal/storage/storage.go`、`internal/storage/repos.go`
- Test: `internal/storage/storage_test.go`

**Interfaces:**
- Produces:
  - `LogicalModel{ID, Name, Description string; Enabled bool; CreatedAt, UpdatedAt int64}`
  - `LogicalModelTarget{ID, LogicalModelID, ChannelID, ModelName string; Priority int}`
  - `LogicalModelRepo{List() ([]LogicalModel, error); Get(id string) (LogicalModel, error); GetByName(name string) (LogicalModel, error); Save(m LogicalModel) (LogicalModel, error); Delete(id string) error; SetEnabled(id string, on bool) error}`
  - `LogicalModelTargetRepo{ListByModel(modelID string) ([]LogicalModelTarget, error); Save(t LogicalModelTarget) (LogicalModelTarget, error); Delete(id string) error; DeleteByModel(modelID string) error}`
  - `Store` 增加 `LogicalModels() LogicalModelRepo; LogicalModelTargets() LogicalModelTargetRepo`；删除 `ChannelKeys() ChannelKeyRepo`

- [ ] **Step 1: 失败测试**

- `TestLogicalModelNameUnique`：同名 Save 第二次返回错误。
- `TestLogicalModelGetByName`：Save 后 GetByName 命中；未知名返回 ErrNotFound。
- `TestLogicalModelDeleteCascadeTargets`：模型+目标，Delete 后目标空。
- `TestTargetListOrdered`：同模型三条不同 priority，ListByModel 按 priority 升序。

- [ ] **Step 2: 确认失败**
Run: `go test ./internal/storage -run Logical -v` → FAIL

- [ ] **Step 3: 实现 repos + Store 接线**

- [ ] **Step 4: 通过 + 全包**
Run: `go test ./internal/storage -v` → PASS

- [ ] **Step 5: Commit**
```bash
git commit -m "feat(storage): logical_models 与目标链仓储"
```

---

### Phase B — 路由与网关

#### Task 4: router.Candidate 改为目标链形状；冷却键改 ChannelID

**Files:**
- Modify: `internal/router/router.go`、`internal/router/cooldown.go`
- Test: `internal/router/router_test.go`、`internal/router/cooldown_test.go`

**Interfaces:**
- Produces:
  - `Candidate{TargetID, LogicalModel, ChannelID, ChannelName, Protocol, BaseURL, SecretKey string; AllowPrivate bool; UpstreamModel string; Priority int; CoolingUntil time.Time; CoolingReason string}`（删除 KeyID/KeyName/ChannelPriority/KeyPriority）
  - `Decision{Model string; Ordered []Candidate; Filtered []FilteredCandidate}` 不变
  - `PriorityRouter.Resolve`：滤掉 cooling，按 `Priority` 升序、ChannelID 稳定排序
  - `CooldownEntry{ChannelID, Until, Reason string}`；`MarkClassified(channelID string, ...)`；`TryAcquire(channelID string, now) (CooldownEntry, bool)`；`Snapshot`

- [ ] **Step 1: 改写 router/cooldown 测试为 ChannelID**

`cand()` helper 与冷却用例改为传 ChannelID；新增 `TestCooldownSharedAcrossModels`：同一 ChannelID 被 MarkClassified 后 TryAcquire 失败——语义不变仅键名变，此测试文档化「跨逻辑模型共享冷却」。

- [ ] **Step 2: 确认失败**
Run: `go test ./internal/router -v` → 编译 FAIL

- [ ] **Step 3: 改 router.go / cooldown.go 实现**

- [ ] **Step 4: 通过**
Run: `go test -race ./internal/router -v` → PASS

- [ ] **Step 5: Commit**
```bash
git commit -m "refactor(router): 目标链 Candidate 与 ChannelID 冷却"
```

#### Task 5: app.candidates 按逻辑模型展开 + 透传设置

**Files:**
- Modify: `internal/app/app.go`、`internal/config/config.go`
- Test: `internal/app/app_test.go`（新建或扩展）、`internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `config.Settings` 增加 `PassthroughUnregistered bool`；存储键 `routing.passthrough_unregistered`，`Load` 缺省 false；`Default()` 同。
  - `(*App).candidates(logicalName string) ([]router.Candidate, error)`：`GetByName` → enabled 检查 → `ListByModel` 目标 → join channel（disabled/缺 credential_ref 过滤进 Decision 由 router 层收——candidates 只返回可用候选，不可用记入日志）。
  - `publicModels()`：改为 `LogicalModels().List()` 中 enabled 的 name。
  - 无目标或模型不存在：candidates 返回空切片；gateway 侧已有「无候选」错误路径。

- [ ] **Step 1: config 失败测试**
- `TestPassthroughDefaultFalse`：`Load(空)` → false；`Set "1"` → true。

- [ ] **Step 2: config 实现并绿**
Run: `go test ./internal/config -v`

- [ ] **Step 3: app 测试（Fake storage 或 tempStore）失败先行**

在 `internal/app` 用 `storage.Open(t.TempDir())`：
- `TestCandidatesFromLogicalModel`：两渠道两目标，candidates 长度=2 且 Priority 排序、UpstreamModel=目标 model_name、SecretKey=渠道 CredentialRef。
- `TestCandidatesUnknownModelEmpty`：未登记名 → 空。
- `TestPublicModelsListsLogicalNames`：两个 enabled + 一个 disabled → 仅前两个。

- [ ] **Step 4: 实现 candidates/publicModels（及 StartGateway 组装处最小适配）**

`a.hasUsableChannel` 若仅服务旧路径则改为 `HasUsableKey` 或删除（网关启动守卫改为「至少一个 enabled 逻辑模型且至少一个可用渠道」——用 `logical_models`+`channels` 判断）。

- [ ] **Step 5: 通过**
Run: `go test ./internal/app ./internal/config -v` → PASS
（gateway/desktop 若编译失败，在本 Task 一并做**编译级**最小修复，行为测试放后续 Task。）

- [ ] **Step 6: Commit**
```bash
git commit -m "feat(app): candidates 展开逻辑模型目标链"
```

#### Task 6: gateway 对齐新 Candidate + 透传拒绝 + 耗尽错误可区分（Anthropic 入站）

**Files:**
- Modify: `internal/gateway/handlers.go`、`internal/gateway/server.go`
- Test: `internal/gateway/gateway_test.go`

**Interfaces:**
- Consumes: Task 4 Candidate；Task 5 空 candidates 语义。
- Produces:
  - `Deps` 增加 `Passthrough func() bool`（读设置）；`Deps.Candidates` 签名不变 `func(model string) ([]router.Candidate, error)`。
  - 未登记且 passthrough 关：404 明确 JSON 错误（沿用现有错误 JSON 形状），**不**进入 failover 循环。
  - passthrough 开且 candidates 空：仍 404，设置项仅预留（注释标明后续接单渠道直连；避免半成品）。
  - 全部目标失败：502，body 含 `all targets failed`；全部仅因冷却：503 含 `all targets cooling`。
  - 为 Task 6B 复用：抽出 `(*Server).failover(model string, run func(c router.Candidate) (committed bool, err error))` 内部循环骨架亦可——若两 Task 间抽象成本高，允许 6B 复制循环并标注 TODO 合并；**优先抽公共循环**。

- [ ] **Step 1: 失败测试（改 fakeUpstream/twoCands helper 的 Candidate 字段）**

- 更新既有 failover 用例字段名（TargetID/ChannelID）。
- `TestUnknownModelRejected`：Candidates 空 → 404，上游 0 调用。
- `TestAllCooling503`：候选全 cooling（Resolve 滤光且 Filtered 非空）→ 503。
- `TestFirstByteThenUpstreamErrorNoReplay`：既有首字节不重放用例保持绿。

- [ ] **Step 2: 确认失败/编译错**
Run: `go test ./internal/gateway -v`

- [ ] **Step 3: 实现 handlers 调整**

冷却 TryAcquire 改传 `c.ChannelID`。凭证 Get 用 `c.SecretKey`（已是 credential_ref）。

- [ ] **Step 4: 通过**
Run: `go test -race ./internal/gateway -v` → PASS

- [ ] **Step 5: Commit**
```bash
git commit -m "feat(gateway): 透传拒绝与冷却耗尽语义"
```

#### Task 6A: 协议转换——OpenAI 客户端 ↔ Anthropic 上游（非流式）

**Files:**
- Create: `internal/protocol/convert_openai_inbound.go`（或并入 `internal/protocol/openai.go`）
- Test: `internal/protocol/convert_openai_inbound_test.go`

**Interfaces:**
- Produces（供 Task 6B 使用）:
  - `ParseOpenAIChatRequest(body []byte) (OpenAIChatRequest, error)`——字段含 `Model string; Stream bool; Messages []json.RawMessage; 其余透传`。
  - `OpenAIRequestToAnthropic(oa OpenAIChatRequest, anthropicModel string) ([]byte, error)`——messages/system/温度等映射到 Anthropic `/v1/messages` body；model 替换为 `anthropicModel`。
  - `AnthropicResponseToOpenAI(anthropicBody []byte, requestedModel string) ([]byte, error)`——非流式：id/choices/content/usage 映射回 chat.completions 形状。
  - 已有方向（anthropic 客户端 → openai 上游）的 `AnthropicRequestToOpenAI` 等**不改语义**，仅复用/整理。

- [ ] **Step 1: 失败测试**
- `TestOpenAIRequestToAnthropicMapsMessages`：user/system/assistant 序列 + model 改写断言。
- `TestAnthropicResponseToOpenAIMapsUsage`：usage tokens 字段名正确（prompt/completion）。
- [ ] **Step 2: 失败** `go test ./internal/protocol -run OpenAIInbound -v`
- [ ] **Step 3: 实现**
- [ ] **Step 4: 通过** `go test ./internal/protocol -v`
- [ ] **Step 5: Commit**
```bash
git commit -m "feat(protocol): OpenAI 入站与 Anthropic 上游互转（非流式）"
```

#### Task 6B: OpenAI 入站流式转换 + 网关端点

**Files:**
- Modify: `internal/protocol/convert_openai_inbound.go`、`internal/gateway/handlers.go`、`internal/gateway/server.go`
- Test: `internal/protocol/convert_openai_inbound_test.go`、`internal/gateway/gateway_test.go`

**Interfaces:**
- Produces:
  - `AnthropicStreamToOpenAIStream(r io.Reader, requestedModel string) io.Reader`——SSE：Anthropic `message_start/content_block_delta` → OpenAI `chat.completion.chunk`（含 finish 时 `finish_reason`）；与既有 `OpenAIStreamTransformer` 方向相反，风格对齐。
  - 网关注册 `POST /v1/chat/completions`：body 解析 → `ParseOpenAIChatRequest` → 同一 candidates/Resolve/冷却循环 → 按候选 `Protocol` 分支：
    - 上游 `openai`：`RewriteModel` 后直接 OpenAI adapter。
    - 上游 `anthropic`：转 Anthropic body → Anthropic adapter →（流式则包 `AnthropicStreamToOpenAIStream`，非流式则 `AnthropicResponseToOpenAI`）。
  - 鉴权、并发上限、body 上限与 `/v1/messages` 同一套中间件/检查。

- [ ] **Step 1: 失败测试**
- `TestAnthropicStreamToOpenAIStreamChunks`：用固定 Anthropic SSE 字符串断言产出 chunk 序列含 `data: ` JSON 且末帧 finish。
- `TestOpenAIInboundFailoverAcrossChannels`：twoCands 模式——第一目标 429，第二目标 200，断言客户端收到 OpenAI 形状 200 且第一目标只调一次。
- `TestOpenAIInboundUnknownModel404`
- `TestOpenAIInboundCommitNoReplay`：首 chunk 写出后上游再错，不调第二目标。
- [ ] **Step 2: 失败** `go test ./internal/gateway ./internal/protocol -v`
- [ ] **Step 3: 实现端点与循环复用**
- [ ] **Step 4: 通过** `go test -race ./internal/gateway ./internal/protocol -v`
- [ ] **Step 5: Commit**
```bash
git commit -m "feat(gateway): OpenAI 入站端点与流式转换"
```

---

### Phase C — 桥接层

#### Task 7: Channels 桥接改单 Key

**Files:**
- Modify: `internal/bridge/channels.go`
- Test: `internal/bridge/channels_test.go`

**Interfaces:**
- Produces（Bind 面）:
  - `Channels` 方法：`List() []ChannelView`、`Get(id) (ChannelDetail, error)`、`Create(presetID, name, baseURL, protocol, apiKey string, enabled, allowPrivate bool) (ChannelView, error)`、`Update(id, name, baseURL, apiKey string, enabled, allowPrivate bool) error`（apiKey 空=不改 Key）、`Remove(id)`、`SetEnabled(id string, on bool)`、`Test(id) provider.ProbeResult`、`FetchModels(id) ([]string, error)`、`SetSuggestions(channelID string, models []string) error`、`Suggestions(channelID string) []string`
  - 删除：`AddKeys`、`SetKeyEnabled`、`RemoveKey`、`TestKey`、旧 `AddModel/UpdateModel/SetModelEnabled/RemoveModel`（模型建议并入 SetSuggestions）
  - `ChannelView{ID, Name, PresetID, Protocol, BaseURL string; Enabled, AllowPrivate bool; HasKey bool; SuggestionCount int}`
  - `ChannelDetail{ChannelView; Suggestions []string}`
  - Key 存取：Create/Update 里 `Creds.Set(credential.ChannelKey(id), []byte(apiKey))`；`credential.ChannelKey(id) = "channel:"+id`（替代 AccountKey 命名，删 AccountKey 若无他用）。

- [ ] **Step 1: 改写 channels_test.go 失败先行**

- `TestCreateChannelStoresOneKey`：Create 后 HasKey、keychain 可 Get。
- `TestUpdateEmptyKeyKeepsOld`：Update(apiKey="") 后旧 Key 仍在。
- `TestRemoveDeletesKeyAndSuggestions`：Remove 后 keychain Delete 被调（fallback file store 断言）。
- `TestSetSuggestionsReplaces`：两次 SetSuggestions 以第二次为准。

- [ ] **Step 2: 确认失败**
Run: `go test ./internal/bridge -run Channel -v`

- [ ] **Step 3: 实现 channels.go 重写**

- [ ] **Step 4: 通过（全 bridge 包若有 logicalmodels 占位编译错，见 Task 8 前先保证本文件相关绿）**
Run: `go test ./internal/bridge -v`

- [ ] **Step 5: Commit**
```bash
git commit -m "feat(bridge): 渠道单 Key 桥接 API"
```

#### Task 8: LogicalModels 桥接 CRUD

**Files:**
- Create: `internal/bridge/logicalmodels.go`
- Modify: `internal/bridge/bridge.go`（如需共享类型）
- Test: `internal/bridge/logicalmodels_test.go`

**Interfaces:**
- Produces:
  - `LogicalModels{A *app.App; Creds credential.Store}`（或仅 A，若 app 已暴露 Store——实现时取最短路径；测试注入 tempStore）
  - `TargetSpec{ChannelID, ModelName string; Priority int}`
  - `ModelView{ID, Name, Description string; Enabled bool; Targets []TargetView}`
  - `TargetView{ID, ChannelID, ChannelName, ModelName string; Priority int; Healthy bool}`
  - 方法：`List() []ModelView`、`Get(id) (ModelView, error)`、`Create(name, description string, targets []TargetSpec) (ModelView, error)`、`Update(id, name, description string, enabled bool, targets []TargetSpec) error`、`Remove(id)`、`SetEnabled(id string, on bool)`
  - 校验：name 匹配 `^[a-zA-Z0-9._-]+$`；targets 非空（Create 时）；Update 全量替换目标（DeleteByModel + 重插）。

- [ ] **Step 1: 失败测试**

- `TestCreateModelWithOrderedTargets`：Create 两目标，List 顺序与 ChannelName 正确。
- `TestCreateRejectsBadName`：`my model`（空格）报错。
- `TestUpdateReplacesTargets`：Update 后仅新目标。
- `TestRemoveModel`：删除后 List 无。

- [ ] **Step 2: 确认失败**
Run: `go test ./internal/bridge -run Logical -v`

- [ ] **Step 3: 实现 logicalmodels.go**
- [ ] **Step 4: 通过** `go test ./internal/bridge -v`
- [ ] **Step 5: Commit**
```bash
git commit -m "feat(bridge): 逻辑模型 CRUD 桥接"
```

#### Task 9: 内置预设数据与桥接

**Files:**
- Modify: `internal/provider/presets.go`（或 Create: `internal/presets/channels.go`——**决定：新建 `internal/presets/channels.go`**，避免与 Adapter 类型预设混淆）
- Create: `internal/presets/channels_test.go`
- Modify: `internal/bridge/bridge.go` 或新文件 `internal/bridge/presets.go`
- Test: `internal/bridge/presets_test.go`

**Interfaces:**
- Produces:
  - `presets.ChannelPreset{ID, DisplayName, Protocol, BaseURL string; DefaultModels []string; DocURL string}`
  - `presets.All() []ChannelPreset`：至少含 `xiaomi, qwen, deepseek, kimi, zhipu, siliconflow, anthropic, openai, google, groq, together, openrouter`（12 个起步；DefaultModels 填各家代表性模型名，DocURL 指向官网控制台）。
  - `presets.ById(id string) (ChannelPreset, bool)`
  - Bridge: `Presets{}` → `List() []PresetView{ID, DisplayName, Protocol, BaseURL, DefaultModels, DocURL}`；`mygo.Bind` 由 desktop 侧加。

- [ ] **Step 1: 失败测试**
- `TestAllPresetsValidateBaseURL`：对 `All()` 逐条 `provider.ValidateBaseURL(p.BaseURL, false)` 通过。
- `TestPresetIDsUnique`
- `TestPresetsBridgeList`：List 长度 = len(All)，字段透传。

- [ ] **Step 2: 确认失败** `go test ./internal/presets ./internal/bridge -run Preset -v`
- [ ] **Step 3: 实现 12+ 条数据（URL 以官方为准，写进表）+ bridge**
- [ ] **Step 4: 通过** `go test ./internal/presets ./internal/bridge -v`
- [ ] **Step 5: Commit**
```bash
git commit -m "feat(presets): 精选渠道预设与桥接列表"
```

#### Task 10: Access 接入片段桥接

**Files:**
- Create: `internal/bridge/access.go`
- Test: `internal/bridge/access_test.go`

**Interfaces:**
- Produces:
  - `Access{A *app.App}` → `ClaudeCodeSnippet(logicalModel string) AccessSnippet`、`CodexSnippet(logicalModel string) AccessSnippet`
  - `AccessSnippet{Title string; Lines []string; Hint string}`
  - 内容（logicalModel 由 UI 传入，必选一个 enabled 逻辑模型）：
    - Claude Code：`ANTHROPIC_BASE_URL=http://127.0.0.1:<port>`、`ANTHROPIC_AUTH_TOKEN=<gateway token>`、`ANTHROPIC_MODEL=<logicalModel>`、`ANTHROPIC_SMALL_FAST_MODEL=<logicalModel>`；Hint 说明写入 shell 环境或 `~/.claude/settings.json` 的 env。
    - Codex：`OPENAI_BASE_URL=http://127.0.0.1:<port>/v1`、`OPENAI_API_KEY=<gateway token>`、模型相关键指向 logicalModel（以 Codex 官方 env/`config.toml` 等价写法为准，Hint 说明粘贴位置）。
  - Token 从 `Gateway.Status().Token` 取；片段中 token **完整展示**（用户复制用），不写入日志。
  - logicalModel 为空：返回 Hint 为「请先创建并选择逻辑模型」的空 Lines 片段（不 panic）。

- [ ] **Step 1: 失败测试**
- `TestClaudeSnippetContainsModelEnv`：断言四行含 BASE_URL/AUTH_TOKEN/MODEL/SMALL_FAST_MODEL 且 model=入参。
- `TestCodexSnippetContainsModelAndToken`
- `TestSnippetEmptyModelHint`
- [ ] **Step 2: 确认失败** `go test ./internal/bridge -run Snippet -v`
- [ ] **Step 3: 实现**
- [ ] **Step 4: 通过** `go test ./internal/bridge -v`
- [ ] **Step 5: Commit**
```bash
git commit -m "feat(bridge): Claude Code / Codex 接入片段（含逻辑模型）"
```

---

### Phase D — 桌面 UI 重构

#### Task 11: 壳——侧栏导航 + 底条网关状态

**Files:**
- Modify: `internal/desktop/desktop.go`
- Create: `internal/desktop/shell.go`（侧栏/底条绘制函数）
- Test: `internal/desktop/shell_test.go`、更新 `desktop_test.go`

**Interfaces:**
- Produces:
  - `uiState.page` 取值：`"logical" | "channels" | "access" | "settings"`（默认 `"logical"`）
  - `navItems` 改为上述四项；`navSel` 删除旧归一逻辑
  - 底条：`gatewayStatusBar(c, u)`——LED（复用 `ledDot`/`stateWord`）+ 状态词 + 启动/停止按钮（复用 `pillButton`/`softButton`）；数据 `u.svc.Gateway.Status()`
  - `pageView` switch 分发到占位函数（本 Task 各页可先返回 `ui.Text(c, "TODO")`，后续 Task 填充——**占位必须在 Task 12–16 清空**）

- [ ] **Step 1: 测试失败先行**
- `TestNavItemSet`：断言 nav 项恰为四页 ID 有序。
- `TestPageDefaultLogical`：`uiState{}` 零值 page 归一为 logical（若零值是 ""，`navSel`/渲染处归一并测该函数）。

- [ ] **Step 2: 失败** `go test ./internal/desktop -run 'Nav|Page' -v`
- [ ] **Step 3: 实现 shell.go + desktop.go 导航改造**
- [ ] **Step 4: 通过** `go test ./internal/desktop -v`
- [ ] **Step 5: Commit**
```bash
git commit -m "feat(desktop): cc-switch 式侧栏壳与网关底条"
```

#### Task 12: 逻辑模型列表页

**Files:**
- Create: `internal/desktop/logicalmodels.go`
- Modify: `internal/desktop/desktop.go`（接线）
- Test: `internal/desktop/logicalmodels_test.go`

**Interfaces:**
- Consumes: `bridge.LogicalModels`（进程内直调 `u.svc`——在 `desktop.Run` 组装 `uiState` 时挂 `u.svc.Logical *bridge.LogicalModels`）
- Produces:
  - `logicalModelsView(c, u)`：Row 列表（沿用 `card`）；每卡：名称、enabled badge、目标链摘要 `渠道A·model → 渠道B·model`、操作钮「编辑」「删除」「启用/停用」
  - 空态文案 +「新建逻辑模型」按钮
  - 删除走二次确认（复用现有 channels 删除确认交互模式）

- [ ] **Step 1: 失败测试**
- `TestLogicalCardShowsTargetChain`：构造 ModelView 两目标，渲染后（沿用现有 desktop 测试对 ui 树的断言方式，若现有测试是逻辑函数测试则测 `formatTargetChain(m ModelView) string` 纯函数——**决定：抽 `formatTargetChain` 纯函数并单测**，渲染薄壳不测字符串细节）。
- `TestFormatTargetChainEmpty`：无目标 → `（无目标）`。

- [ ] **Step 2: 失败** `go test ./internal/desktop -run Logical -v`
- [ ] **Step 3: 实现 view + 纯函数**
- [ ] **Step 4: 通过** `go test ./internal/desktop -v`
- [ ] **Step 5: Commit**
```bash
git commit -m "feat(desktop): 逻辑模型列表页"
```

#### Task 13: 逻辑模型新建/编辑页（目标链编辑）

**Files:**
- Modify: `internal/desktop/logicalmodels.go`
- Test: `internal/desktop/logicalmodels_test.go`

**Interfaces:**
- Produces:
  - `u.page` 增加 `"logical-edit"`；携带 `u.editModelID string`（空=新建）
  - `logicalModelEditView(c,u)`：名称/描述输入、目标行（渠道下拉=Channel list、模型名输入或 suggestion 选择、上移/下移/删除）、添加目标、保存调 `u.svc.Logical.Create/Update`
  - 校验错误显示在表单位（复用 channels 页信息位模式）
  - 保存成功后 `u.page="logical"`

- [ ] **Step 1: 失败测试（纯逻辑）**
- `TestTargetMoveUpDown`：对 `[]TargetSpec` 的 move 函数上/下移边界。
- `TestEditValidationEmptyName`：保存前校验函数返回错误信息。

- [ ] **Step 2: 失败** `go test ./internal/desktop -run 'Move|Validation|Edit' -v`
- [ ] **Step 3: 实现**
- [ ] **Step 4: 通过** `go test ./internal/desktop -v`
- [ ] **Step 5: Commit**
```bash
git commit -m "feat(desktop): 逻辑模型编辑与目标链排序"
```

#### Task 14: 渠道页重构（单 Key + 预设添加）

**Files:**
- Modify: `internal/desktop/channels.go`（重写）
- Modify: `internal/desktop/desktop.go`
- Test: `internal/desktop/channels_test.go`（大幅改写）

**Interfaces:**
- Consumes: `bridge.Channels`（Task 7 形状）、`bridge.Presets`
- Produces:
  - `channelsView`：卡片（名、protocol、base_url、HasKey、建议模型数）+「添加渠道」
  - `channelNewView`：预设网格（ID+DisplayName tile，复用 `tile`/`tileSoft`）→ 选中预设回填 protocol/base_url/建议；或「自定义」；表单：名称、base_url、**单个 API Key 输入**、allowPrivate；保存 → `Create`
  - `channelDetailView`：信息 + Key 更新框（留空不改）+ 建议模型列表编辑（文本多行，逗号/换行分隔 → `SetSuggestions`）+ 测试连通（`Test`）+ 删除（引用警告：调用 Logical 列表检查 target 引用）
  - **删除** key pool 区块、public/upstream 模型映射区块及相应测试

- [ ] **Step 1: 改写测试失败先行**
- `TestPresetPreselectFillsForm`：选 qwen 预设后表单状态字段断言（抽纯函数 `applyPreset(form *channelForm, p presets.ChannelPreset)`）。
- `TestChannelFormValidate`：缺 name/key/base_url 的错误信息。
- `TestDeleteWarnsWhenReferenced`：纯函数 `referencedBy(models []bridge.ModelView, channelID string) []string`。

- [ ] **Step 2: 失败** `go test ./internal/desktop -run 'Preset|ChannelForm|Referenced' -v`
- [ ] **Step 3: 实现 channels.go 重写 + 删旧测试**
- [ ] **Step 4: 通过** `go test ./internal/desktop -v`
- [ ] **Step 5: Commit**
```bash
git commit -m "feat(desktop): 渠道页单 Key 与预设添加"
```

#### Task 15: 接入页 + 设置页

**Files:**
- Create: `internal/desktop/access.go`、`internal/desktop/settings.go`
- Modify: `internal/desktop/desktop.go`
- Test: `internal/desktop/access_test.go`、`internal/desktop/settings_test.go`

**Interfaces:**
- Consumes: `bridge.Access`（Task 10）、`bridge.Gateway.SaveSettings/RegenerateToken`（已有）
- Produces:
  - `accessView`：顶部「当前逻辑模型」选择器（数据来自 `u.svc.Logical.List()` 中 enabled 项，记忆上次选择到 settings 键 `access.last_logical_model`）；下方 Claude Code / Codex 两块卡片，每块：片段代码块（`snippetBg` 等宽字体，复用 overview 旧片段样式函数迁入 access.go）+ 复制按钮（优先 `transfer` 包现有能力，无则 Read-only 多行文本 + Hint「Cmd/Ctrl+C」）
  - `settingsView`：端口、请求超时、透传开关（预留）、关于版本；保存 → `SaveSettings`；重启提示复用现有逻辑
  - 删除 `overview.go` 中 settingsCard/accessCard（随 Task 16 删 overview）

- [ ] **Step 1: 失败测试**
- `TestSettingsFormMapsPort`：表单↔Settings 编解码纯函数。
- `TestAccessCardTitles`：两卡标题字符串快照/断言。
- `TestAccessDefaultModelSelection`：无 `access.last_logical_model` 时默认选第一个 enabled 逻辑模型的纯函数。
- [ ] **Step 2: 失败** `go test ./internal/desktop -run 'Settings|Access' -v`
- [ ] **Step 3: 实现**
- [ ] **Step 4: 通过** `go test ./internal/desktop -v`
- [ ] **Step 5: Commit**
```bash
git commit -m "feat(desktop): 接入页与设置页"
```

#### Task 16: 删除旧概览/模型页与死代码

**Files:**
- Delete: `internal/desktop/overview.go`、`internal/desktop/models.go` 及其 `_test.go`
- Modify: `internal/desktop/desktop.go`（去引用）
- Modify: `internal/credential/credential.go`（删 `AccountKey` 若已无引用，改 `ChannelKey(id) = "channel:"+id`）
- Modify: `internal/config/config.go`（删 `CredentialFallback`/`KeyCredFallback` 若确认无消费）
- Modify: `internal/provider/provider.go`（删未用 `AdapterForProtocol` 或仅保留被测引用——**决定：删除并改其测试直接用 New*Adapter**）
- Test: 全量

- [ ] **Step 1: 删除文件与符号，编译修复**
- [ ] **Step 2: 全量测试**
Run: `go build ./... && go vet ./... && go test ./...`
Expected: PASS
- [ ] **Step 3: 确认无残留**
Run: `grep -rn 'channel_keys\|ChannelKeyRepo\|AddKeys\|overviewView\|modelsView' internal/ || true`
Expected: 无输出
- [ ] **Step 4: Commit**
```bash
git commit -m "refactor: 删除旧概览/模型页与多密钥遗留"
```

---

### Phase E — 收尾

#### Task 17: README 与 CLAUDE.md 同步

**Files:**
- Modify: `README.md`（首次使用：删库说明、逻辑模型快速上手、接入 CC/Codex 步骤）
- Modify: `CLAUDE.md`（项目状态段：指向新 spec/plan；架构段 channels→逻辑模型表述更新）

- [ ] **Step 1: 更新 README 首次使用与迁移说明**
- [ ] **Step 2: 更新 CLAUDE.md 状态与不变量段**
- [ ] **Step 3: Commit**
```bash
git commit -m "docs: 逻辑模型与 ccswitch UI 落地说明"
```

#### Task 18: 全量验证与竞态

- [ ] **Step 1:**
```bash
go build ./... && go vet ./... && go test ./... && go test -race ./internal/router ./internal/gateway ./internal/app ./internal/storage
```
Expected: 全 PASS

- [ ] **Step 2: 手动冒烟（实现者本地）**
`go run ./cmd/agentswitch`：删旧库 → 建渠道（预设）→ 建逻辑模型挂两目标 → 底条启网关 → curl 逻辑模型名（Fake 不可用则跳过上游，仅验证 404/路由日志）。

- [ ] **Step 3: Commit（如有修补）**

---

## 任务依赖

```
Task1 → Task2 → Task3 → Task5 → Task6 → Task6A → Task6B
Task4 → Task6                  （Task4 可与 2/3 并行）
Task5 → Task7 → Task8 → Task12 → Task13
Task7 → Task9 → Task14
Task6B → Task10 → Task15       （接入片段依赖双入站端口/协议）
Task11 → Task12/14/15
Task12–15 → Task16 → Task17 → Task18
```

## 自检记录（writing-plans self-review）

- Spec 覆盖：§4 逻辑模型/目标链→T3/T5/T6/T8/T12/13；§4.2 透传默认关→T5/T6；§5 删库 schema→T1–3（含旧库报错、preset_id、TEXT id、无事件表）；§6 UI 骨架→T11–15；§7 预设→T9/T14；§8 桥接→T7–10；Codex 真接入→T6A/T6B；接入含模型 env→T10/T15；§9 安全→T7（Key 不落库）、T9（SSRF 测）、T10（token 不进日志）；§10 测试→各 Task + T18；旧代码删除→T16。
- 类型一致性：Candidate/Cooldown 键、ChannelView、ModelView、TargetSpec 在跨 Task 引用处签名一致。
- 粒度：存储/路由任务偏大（>5min），因删库重建会连锁破坏编译，拆开反而无法各自全绿；已在 Task 5/7/14 标注「编译级最小修复」边界。
