# AgentSwitch 渠道管理与模型维护 + P0-B 设计：channels 聚合根、双协议上游、账号池故障转移

- 日期：2026-10-17
- 状态：设计方向已由用户逐节确认（§1 数据模型 / §2 协议与路由 / §3 管理面与 UI / §4 测试），待实现
- 决策人：仓库所有者（zhang.cao）
- 参考：本地运行的 new-api v1.0.0-rc.44（http://localhost:3000/channels 与 /models/metadata）实地考察
- 范围：`internal/{storage,provider,protocol,router,gateway,bridge,desktop,app}`；`CLAUDE.md`、`README.md` 同步更新
- 前置：P0-A′ 已完成（单协议 anthropic 端到端、桌面 UI 四页、凭据链路）

## 1. 决策记录

1. **范围**：渠道管理 + 模型维护 + **P0-B（多账号池、优先级、故障转移与冷却）一次做完**（用户裁定；原计划分步）。
2. **协议**：**同时支持 OpenAI 协议上游**（新增出站协议转换层）；网关对 Agent 的入站协议本期仍只有 Anthropic Messages API。
3. **模型语义**：**渠道级模型清单 + 重定向**（new-api 式）取代全局别名表；每渠道每条目 = 公开名 → 上游名；路由按公开模型过滤候选渠道。
4. **架构路线**：**new-api 式 channels 单一聚合实体**（用户裁定，否决"保留 Provider+Account 双实体做聚合视图"的推荐方案）；provider/account/models 三张旧表退役。
5. **存量数据**：**不做数据迁移，删库重建**（用户裁定）——migration 列表重写为单一 v1 新 schema；落地后删除本地 DB 文件一次。
6. **信息架构**：边栏合并为 **概览 / 渠道 / 模型** 三页；渠道页 = new-api 心智模型的聚合视图（渠道含密钥们+模型清单）。
7. **有意收窄**：模型聚合页只读 + 行内启停；增删改入口收敛在渠道详情——避免双入口状态不一致（与 new-api 不同，偏离已确认）。

## 2. 参考要点（new-api 考察结论）

采纳：渠道类型预设先行（创建渠道先选类型）、渠道级模型清单与重定向、批量密钥导入、渠道测试、「从上游获取」模型列表、渠道状态 Badge 与筛选心智。

不采纳（YAGNI 或与本地单用户网关定位冲突）：分组/标签体系、定价与广场展示元数据、权重负载均衡、状态码映射/参数覆盖/请求头覆盖、代理与 HTTP/2 分片、多协议入站、渠道 JSON 内联密钥（违反 docx §8，见 §3）。

## 3. 数据模型（`internal/storage`）

### 3.1 新 schema（migration 重写为单一 v1，删库重建）

```sql
CREATE TABLE channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  type TEXT NOT NULL,            -- 类型预设 id（§4），决定 protocol 与默认 BaseURL
  protocol TEXT NOT NULL,        -- anthropic | openai；数据面按此分派 adapter（由 type 推导后冗余存储）
  base_url TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 10,   -- 数值小者优先（沿用现语义）
  enabled INTEGER NOT NULL DEFAULT 1,
  allow_private_upstream INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);

CREATE TABLE channel_keys (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  secret_key TEXT NOT NULL,      -- OS Keychain 引用（docx §8：SQLite 只存 credential_ref）
  secret_backend TEXT NOT NULL,  -- os-keychain | fallback-file
  key_tail TEXT NOT NULL DEFAULT '',
  priority INTEGER NOT NULL DEFAULT 10,   -- 渠道内排序
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX idx_channel_keys_channel ON channel_keys(channel_id);

CREATE TABLE channel_models (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  public_model TEXT NOT NULL,    -- Agent 请求名
  upstream_model TEXT NOT NULL,  -- 上游名（可与公开名相同）
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(channel_id, public_model)   -- 同渠道公开名唯一；跨渠道可重名（渠道级语义核心）
);
CREATE INDEX idx_channel_models_channel ON channel_models(channel_id);
CREATE INDEX idx_channel_models_public ON channel_models(public_model);

CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
```

密钥独立成子表而非 JSON 内联：docx §8 硬约束（SQLite 只存 credential_ref，本体在 OS Keychain）+ 冷却状态需按密钥粒度原子更新。UI 心智模型仍是"渠道一体化"。

### 3.2 冷却状态不建表

内存态（`sync.Mutex` 保护，键 = key id），重启即清。冷却本就是短生命周期运行态；docx 要求的原子性用锁 + 发送前二次校验满足（§6）。UI 经 bridge 实时读内存态。

### 3.3 删库重建

migration 列表重写为单一 v1（上表全部）。`db.go` 既有"追加新版本、勿改已发布语句"纪律本处以用户决策（决策 5）偏离：发布前无存量用户，重置 schema，落地后手动删除本地 DB 文件一次（README/CLAUDE.md 注明路径与操作）。

## 4. 渠道类型预设

预设目录写死在代码（`internal/provider/presets.go`），bridge 与 desktop 共用；预设 = 默认 BaseURL + protocol 推导 + UI 说明文字，BaseURL 仍可显式覆盖（SSRF/私网校验 `provider.ValidateBaseURL` 原样沿用）。

| 预设 id | 名称 | protocol | 默认 BaseURL |
|---|---|---|---|
| `anthropic-official` | Anthropic 官方 | anthropic | `https://api.anthropic.com` |
| `anthropic-compatible` | Anthropic 兼容中转 | anthropic | （必填） |
| `openai-official` | OpenAI 官方 | openai | `https://api.openai.com` |
| `openai-compatible` | OpenAI 兼容 | openai | （必填） |

## 5. 协议适配（`internal/protocol` + `internal/provider`）

### 5.1 入站不变

网关对 Agent 只暴露既有端点（`POST /v1/messages`、`GET /v1/models` 及其 `/models` 别名、`/healthz`、`/readyz`），不新增路由。OpenAI 协议只出现在出站上游侧，由 `channel.protocol` 分派。

### 5.2 出站 anthropic

现有 adapter 原样保留（`x-api-key` + `anthropic-version`）。

### 5.3 出站 openai（新增，本设计最大工作量块）

- `provider.NewOpenAIAdapter()`：HTTP 层与 anthropic adapter 同构（超时、私网校验、禁重定向、错误体限长读取），路径 `/v1/chat/completions`，`Authorization: Bearer` 头。
- `protocol/openai.go` 转换器：
  1. **请求转换**（Anthropic → ChatCompletions）：system → system role 消息；`tool_use`（assistant 块）→ `tool_calls`；`tool_result`（user 块）→ `role:"tool"` 消息；tools（`input_schema` → `parameters`）；`tool_choice` auto/any/具名 → `auto`/`required`/`{"type":"function","function":{"name":…}}`；`max_tokens`/`temperature`/`top_p`/`stream` 直映，`stop_sequences` → `stop`。
  2. **非流响应转换**（ChatCompletions → Anthropic）：`choices[0].message` → content blocks（text；`tool_calls` → `tool_use` 块）；`finish_reason` stop/length/tool_calls → `end_turn`/`max_tokens`/`tool_use`；usage `prompt_tokens`/`completion_tokens` → `input_tokens`/`output_tokens`。
  3. **流式转换**：OpenAI chunk 流 → Anthropic 事件流（`message_start` → `content_block_start/delta/stop` → `message_delta`（stop_reason+usage）→ `message_stop`）；tool_call 增量按 index 累积、收口一次性发 `tool_use` 块；逐事件 flush；`data: [DONE]` 哨兵。
  4. **错误转换**：OpenAI `{error:{message,type}}` → Anthropic envelope；状态码透传；`provider.Classify` 不动（只依赖状态码+响应体标记，对两协议通用）。
- 明确丢弃并记一行日志的入站字段：`thinking`、`metadata` 等 Anthropic 专有参数（不做透传映射）。

### 5.4 从上游获取模型列表

两协议同形 `GET /v1/models`；bridge `FetchModels(channelID)` 返回上游模型 id 列表供 UI 填充。

## 6. 路由与故障转移（`internal/router` + `internal/gateway`）

### 6.1 候选集与排序

```
请求公开模型 X
  → 候选 = enabled 渠道 × enabled 密钥 × enabled channel_models(public_model = X)
  → 排序：渠道 priority 升序 → 密钥 priority 升序 → id 升序（稳定全序）
  → 过滤：冷却中的密钥剔除（快照传入，记原因进 Decision）
```

router 保持纯函数（不持共享可变状态）：gateway 取冷却快照随候选传入，router 只排序过滤解释。

### 6.2 冷却策略（内存 CooldownStore，键 = key id）

| 错误分类 | 冷却时长 |
|---|---|
| rate_limited | 上游 `Retry-After`（上限 10min），缺省 30s |
| quota_exhausted | 30min |
| server（5xx） | 15s |
| connect | 15s |
| auth（401/403） | 1h |

时长为 P0-B 常量（代码内定义，不进配置），后续可调。

### 6.3 转移规则（docx §6.3/§7 不变量逐条落地）

- gateway 按 Decision 顺序逐候选**串行**尝试（不做并行竞速）；每候选发送前 `CooldownStore.TryAcquire(keyID)` 锁内原子二次校验，失败跳下一候选并记录。
- **可转移**：connect、429、5xx、quota、auth——且必须还没向客户端写过任何字节（响应头/流事件均未发）。
- **不可转移**：400 类参数错误（客户端自身问题，原样返回）；**客户端取消**（取消上游 ctx，绝不转移）。
- 流式一旦首字节写给客户端 → 该请求锁定当前候选，后续失败只能断流，不重放（上游可能已计费）。
- 整个候选循环共享客户端请求的既有超时 ctx（不引入每候选独立超时旋钮）。

### 6.4 可解释记录

每次路由决策输出结构化记录：被过滤候选+原因、最终目标（渠道/密钥/上游模型）、切换次数。载体沿用 P0-A′ 现状——结构化日志一行 + ring 缓冲（`RequestRecord` 扩展 switch 字段）；不新增 SQLite 事件表（P0-A′ 已有"事件只进 ring"的既成偏离，本期不扩大也不回改）。

### 6.5 端点语义随动

`/v1/models` = 各渠道 enabled 模型公开名并集去重；`readyz`/Degraded 判定 = 存在 enabled 渠道 + enabled 密钥（无 → 503/Degraded 带原因，语义沿用）。

## 7. 管理面 Bridge（`internal/bridge`）

`Providers`、`Accounts` 服务退役；`internal/account` 包随之删除。

```
Channels
  List()            → 渠道行 + 聚合计数（密钥数/模型数/冷却中数）
  Get(id)           → 渠道字段 + keys[] + models[]
  Create(type, name, baseURL, priority, enabled, allowPrivate)
                    → 校验预设合法，推导 protocol 与默认 BaseURL（显式传 URL 则覆盖）
  Update / Remove / SetEnabled / Test（沿用 Probe）
  FetchModels(id)   → §5.4
  AddKeys(channelID, name, keys 多行文本, allowFallback)
                    → 批量导入：每行一枚 → N 条 channel_keys（key_tail/后端判定沿用现逻辑）
  SetKeyEnabled / RemoveKey / TestKey
  AddModel / UpdateModel / SetModelEnabled / RemoveModel
Models（聚合）
  ListAll()         → 公开名 × 渠道 × 上游名 × enabled 跨渠道 join
  SetEnabled(rowID) → 聚合页快捷启停
Gateway（新增）
  PoolStatus()      → 每密钥：渠道名/密钥名/冷却至/剩余秒/最近错误类（读内存 CooldownStore）
```

审计日志沿用现约定：只记成功变更、actor=ui、零密钥值。

## 8. 桌面 UI（`internal/desktop`，设计系统沿用，theme.go 不动）

- **边栏**：概览 / 渠道 / 模型（供应商、账号项退役；渠道复用插头图标，模型沿用路由箭头；icons.go 增删）。
- **渠道页**（master-detail 上下布局）：
  - 渠道表：名称 / 类型 / 状态 Badge / 密钥数 / 模型数 / 优先级；页头记数；空态引导。
  - 创建渠道：类型 Select（4 预设 + 说明文字随动；MyGo 无卡片组件，用 Select+说明行承载选型体验）；选中类型后 BaseURL 预填默认值。
  - 选中渠道详情：① 连接配置丝印表单（名称/BaseURL/优先级/启用/允许私网 + 保存、测试连通、删除两段确认）；② 密钥子表（名称/尾 4 等宽/后端/冷却点 Warning+剩余秒/启用/删除）+ 添加表单（名称 + 密钥多行 textarea 批量 + 允许文件降级）；③ 模型子表（公开名/上游名等宽/启用）+ 添加表单 + 「从上游获取」。
- **模型页**：跨渠道聚合表（公开模型/渠道/上游模型/启用）+ 记数 + 空态；行内启停；编辑入口收敛在渠道详情（决策 7）。
- **概览页**：启用 P0-B 预留槽位——池状态条（面板头下方）：每密钥一枚状态点 + 「渠道名/密钥名」；正常 = Accent，冷却 = Warning 常亮 + 剩余秒；`PoolStatus()` 驱动。其余不动。
  > supersede（2026-10-17 终审裁定）：池状态条实现为「冷却中的密钥逐枚 Warning 点 + 剩余秒，无冷却时显示一行『密钥池: 全部就绪』」——收窄于本条原文的每密钥常态 Accent 点（桌面单用户下常态点无信息量），以实现为准。
- 文案不变量与语义色映射沿用桌面 UI spec §3.3/§5.4；删除一律两段确认。

## 9. 包结构与依赖规则影响

- `internal/account` 删除；`internal/storage` 重写 repo（channels/keys/models）；`internal/router` 增冷却相关类型与真实现（`Single` 退役）；`internal/protocol` 增 `openai.go`；`internal/provider` 增 presets.go 与 OpenAI adapter；`internal/gateway` 改造 handlers 为候选循环；`internal/desktop` 删 accounts.go、改 providers.go→channels.go、models.go 重写、overview.go 增池状态条。
- 依赖规则不变：router 不依赖 MyGo/UI/HTTP handler；provider 经接口暴露；desktop 只管生命周期与桥接；核心包无 UI 可测。
- 测试目录沿用包内 `_test.go` + httptest Fake；无新增顶层目录。

## 10. 测试策略

- **转换器单测**（table-driven）：请求转换（system/messages/tools/tool_choice/参数）；非流响应（finish_reason 三映射、tool_use 还原、usage）；流式（chunk→事件序、tool_call 按 index 收口、SSE 跨包分块边界、`[DONE]`）；错误体转换。
- **路由单测**：排序稳定全序；按公开模型过滤；冷却过滤 + Decision 可解释字段完整。
- **冷却并发**（`-race` 必跑）：并发 429 同一密钥冷却只置一次；TryAcquire 锁内二次校验。
- **转移边界**（docx §13 必测逐条）：首字节前可切换 / 首字节后不重放 / 客户端取消不转移直取上游 / 400 不转移。
- **Fake 扩展**：现有 Fake Provider 旁新增 **OpenAI 伪上游**（ChatCompletions + SSE chunk 流，可脚本化注入 429/5xx/流中断），端到端：Anthropic 入站 → 转换 → OpenAI 假上游 → 逆转换 → 客户端收 Anthropic 流。
- **bridge/desktop**：文案不变量测试沿用；`go test -race ./...` 全绿为完成线。
- **不在 CI**：真实 Agent（Claude Code 指向网关）受控手测，README 流程沿用。

## 11. 非目标

入站 OpenAI 端点（Codex CLI 直连）；`thinking`/`metadata` 透传映射；权重负载均衡；分组/标签；定价元数据；渠道 JSON 导入导出；状态码映射/参数覆盖/请求头覆盖；代理设置；冷却时长可配置化；事件落 SQLite。

## 12. 与权威文档（docx v2.0）的关系

- **符合**：§6.3/§7 路由与故障转移不变量（首字节前切换、冷却原子、二次校验、可解释决策、400 不重试、取消不转移）；§8/§10 安全默认（密钥只存引用、SSRF 校验、脱敏日志、令牌独立）；§12.1 依赖规则；§13 测试边界。
- **偏离（凭证即本档）**：
  1. §12 目标目录含 `internal/account` 与 accounts 实体——本期按决策 4/6 退役为 channel_keys（概念重构，非放弃分层）。
  2. migrations"勿改已发布语句"纪律——按决策 5 删库重建。
  3. 事件持久化——P0-A′ 起即"ring only"，本期沿用（§6.4），不视为本次新增偏离。
- `CLAUDE.md` 在实现阶段同步更新（项目状态、命令、架构段落）。

## 13. 安全不变量（沿用，逐条不受本次重构影响）

密钥本体只入 OS Keychain/降级文件（显式确认闸不变）；日志/错误/UI/诊断零密钥、零 Authorization、零完整 Prompt；模型 API 与管理面令牌相互独立不变；`base_url` SSRF/重定向/私网校验不变（`allow_private_upstream` 显式放行）；key_tail 只存尾 4。
