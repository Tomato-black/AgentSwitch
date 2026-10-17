# 逻辑模型 + cc-switch 式 UI——设计文档

日期：2026-10-17  
状态：待评审

## 1. 意图与动机

AgentSwitch 现状是「模型 API 网关 + channels 聚合根」，桌面 UI 仍是交换机面板式的卡片流（概览/渠道/模型三页），添加渠道要手填 base_url 与模型名，密钥模型是每渠道多 Key（`channel_keys` 一对多）。用户希望：

1. **UI 与添加体验向 cc-switch 看齐**：左侧栏 + 卡片列表 + 预设一键导入（小米、千问、DeepSeek、Kimi、Anthropic、OpenAI、硅基流动等精选 15～25 个），而不是交换机面板。
2. **故障转移的主体是用户自定义的「逻辑模型」**：一个逻辑模型（如 `my-code`）挂有序的「渠道·具体模型」目标链；Agent 只调逻辑模型名，网关按序尝试，只在响应尚未提交给客户端前跨目标切换。可定义任意多个逻辑模型。
3. **密钥简化**：每渠道一个 API Key（`channel_keys` 多密钥表退役）；工具侧只见网关密钥。

**保留的核心卖点**：本地模型 API 网关 + 可自定义故障转移。不是 cc-switch 式全家桶。

## 2. 目标 / 非目标

### 目标（首版）

- 逻辑模型 CRUD：多逻辑模型，每个含名字、有序目标链（渠道 + 该渠道上的模型名）。
- 逻辑模型名作为网关对外暴露的模型名；路由按目标顺序 + 既有故障转移不变量（首字节前可切、429 冷却、二次校验、原子并发）。
- 每渠道单 Key（OS 钥匙串，`credential_ref` 语义不变）。
- cc-switch 式桌面骨架：左侧栏（逻辑模型 / 渠道 / 接入 / 设置）+ 主区卡片列表 + 添加/编辑弹窗；网关状态收在侧栏底条（LED + 启停）。
- 内置精选渠道预设（15～25 个），添加渠道时带出 base_url/协议/模型列表，只填一个 Key。
- 接入引导：Claude Code + Codex（生成指向网关的环境变量/配置片段 + 网关密钥 + 选定的逻辑模型名）。**网关新增 OpenAI 入站**（`POST /v1/chat/completions`）以真实接入 Codex；与既有 Anthropic 入站（`POST /v1/messages`）共用同一套逻辑模型/故障转移。
- 存储 **删库重建**（用户 2026-10-17 决策）：schema 重写为最终形态，不做 v2 增量迁移；**检测到旧 schema（存在 `channel_keys`）时启动报错并提示用户删除一次库文件**，不静默清空数据。被新模型取代的旧代码直接删除，仓库只保留最新实现。

### 非目标（首版不做，记为后续方向）

- MCP / Skills / Prompts 集中管理。
- 会话阅读视图、用量/配额统计页。
- 十工具全套接入（Gemini CLI、Grok Build、OpenCode、OpenClaw、Hermes、Pi、MiniMax Code 等）。
- cc-switch 的 Direct / Routing / Aggregation 三模式开关（一切流量走网关；单目标逻辑模型≈直连，多目标≈聚合+故障转移）。
- Web 前端（延续 2026-10-17 UI 决策：MyGo 原生视图）。
- 每渠道多 Key、全局共享 Key、逻辑模型级 Key。

### 反目标

- 不做成「配置文件改写器」——网关始终在路径上，不绕过。
- 不静默明文落库密钥；不在日志/UI 出现完整 Key 或 Prompt。
- 不在首字节后静默重放跨目标请求。

## 3. 约束

- 技术栈不变：Go + MyGo 原生视图 + SQLite + OS 钥匙串；测试离线 Fake Provider。
- 两平面分离、路由安全不变量、SSRF 校验、脱敏等 CLAUDE.md 既有硬约束全部延续。
- cc-switch 预设仅作**数据参考**（base_url、模型名、协议类型），不直接拷贝其代码；协议与字段逐个核对。
- 桌面 UI 设计 token 沿用 `internal/desktop/theme.go` 体系，布局从「卡片流页面」改为「侧栏 + 列表页 + 弹窗」。

## 4. 领域模型

### 4.1 概念

| 概念 | 说明 |
|------|------|
| **渠道 (Channel)** | 一个上游供应商接入点：base_url、协议（anthropic/openai-compat 等）、**一个** API Key（钥匙串 ref）。可挂可选的「模型建议列表」供 UI 选择。 |
| **逻辑模型 (Logical Model)** | 用户定义的对外模型名（网关路由键）。可创建任意多个。 |
| **目标 (Target)** | 逻辑模型下的有序条目：`(channel_id, model_name, order)`。故障转移按 order 升序尝试。 |

关系：`逻辑模型 1—N 目标 N—1 渠道`。同一渠道可被多个逻辑模型引用；同一逻辑模型内同一渠道可出现多次（不同 model_name）。

### 4.2 路由语义

1. Agent 请求 `model=my-code` → 查逻辑模型 `my-code` 的目标链。
2. 按 order 依次选择候选；应用既有过滤（冷却、健康、SSRF 等）与二次校验。
3. 上游错误按既有分类（额度/429/5xx 可切换；400 参数错误不重试；客户端取消不上抛切换）。
4. **仅在尚未向客户端提交响应头/任何流式事件前**允许切到下一目标；已开始输出绝不重放。
5. 每次决策输出可解释记录：过滤原因、命中目标、切换次数（对齐既有遥测/事件，扩展字段带 logical_model 与 target）。
6. 未定义的模型名：若请求的模型名恰好是某渠道原生模型且用户开启了「透传未登记模型」（设置项，默认关），可直连单渠道；默认返回明确错误，避免静默错路由。

### 4.3 与旧模型关系（删库重建，无迁移）

- schema 直接按最终形态重建；老库删除一次（README 写明）。不做 channel_keys 数据搬迁。
- `channel_keys` 表及其多密钥代码路径**删除**；渠道单 Key 即 `credential_ref`。
- `channel_models` 职责收窄为渠道的「模型建议列表」（添加逻辑模型目标时的下拉数据源）；**不再是路由主体**。
- 旧「按渠道优先级故障转移」（P0-B）的路由入口**删除**；能力由「逻辑模型目标链」取代。冷却、二次校验、错误分类等底层安全机制保留并挂到新路由路径上。
- 长期无引用的旧 UI 页/桥接 API/测试一并删除，不留兼容层。

## 5. 存储（删库重建后的最终 schema）

主键继续使用现有 TEXT `NewID()`（crypto/rand hex），**不**改为 INTEGER——沿用全链路现有 ID 类型，避免无意义全量改动。

```sql
CREATE TABLE channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  protocol TEXT NOT NULL,             -- anthropic | openai
  base_url TEXT NOT NULL,
  credential_ref TEXT NOT NULL,       -- OS 钥匙串引用；每渠道恰好一个 Key
  preset_id TEXT NOT NULL DEFAULT '', -- 内置预设 ID，自定义为空串（UI 回显）
  allow_private_upstream INTEGER NOT NULL DEFAULT 0,  -- 保留：本地 Ollama 等显式放行
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE channel_models (         -- 仅建议列表，非路由主体
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  model_name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE(channel_id, model_name)
);

CREATE TABLE logical_models (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,          -- 网关对外模型名，[a-zA-Z0-9._-]+
  description TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);

CREATE TABLE logical_model_targets (
  id TEXT PRIMARY KEY,
  logical_model_id TEXT NOT NULL REFERENCES logical_models(id) ON DELETE CASCADE,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  model_name TEXT NOT NULL,           -- 渠道上的具体模型
  priority INTEGER NOT NULL,          -- 顺序，越小越优先
  UNIQUE(logical_model_id, channel_id, model_name)
);
```

**不新增** RequestEvent/SwitchEvent/AuditEvent 等 SQLite 事件表：遥测沿用现有内存环（`internal/telemetry/ring.go`）+ 文件日志（重启清零，卡片「近 N 条」徽章同现状）。P0-B 亦将事件落库列为非目标，本次不扩大。

## 6. 桌面 UI（cc-switch 式骨架）

### 6.1 导航

```
┌──────────┬─────────────────────────────┐
│ 侧栏      │ 主区                         │
│ 逻辑模型  │  卡片列表 / 表单 / 设置       │
│ 渠道      │                             │
│ 接入      │                             │
│ 设置      │                             │
│ ──────── │                             │
│ ● 运行中  │  ← 侧栏底条：LED+状态+启停   │
└──────────┴─────────────────────────────┘
```

- 默认页 = 逻辑模型列表。
- 网关启停与状态来自 `Gateway.Status()` 实际状态（真实性不变量），收在底条，不再占概览整页。
- MyGo 原生视图实现侧栏+主区切换；动画/收起图标轨非首版必须。

### 6.2 逻辑模型页

- 卡片：逻辑模型名、enabled、目标链摘要（`渠道A·model → 渠道B·model`）、当前/最近命中、近 N 次切换失败徽章（沿用遥测环）。
- 操作：新建、编辑（目标链排序拖拽或上下移）、启用/停用、删除（确认）。
- 空态：引导「先建渠道，再把渠道挂进逻辑模型」。

### 6.3 渠道页

- 卡片：渠道名、协议、base_url（脱敏显示）、模型建议数、健康/冷却指示。
- 添加：弹窗 → 预设网格（图标+名称）或自定义 → 填单 Key → 保存。
- 编辑/删除；删除时若有逻辑模型引用则警告并列出引用者。

### 6.4 接入页

- Claude Code / Codex 两个 tab 或卡片：一键复制环境变量/配置片段（网关 base_url + 网关密钥 + 说明）。
- 网关密钥查看需确认/掩码，对齐既有安全展示。

### 6.5 设置页

网关端口/监听、启动行为、透传未登记模型开关、日志路径、导出脱敏配置等；沿用既有设置项迁移。

## 7. 内置预设（精选）

首版清单（实现时以核对过的官方文档为准，可微调）：

- 国内：小米 MiMo、阿里千问、DeepSeek、Kimi（月之暗面）、智谱 GLM、硅基流动、阶跃星辰、豆包等。
- 海外：Anthropic、OpenAI、Google（AI Studio）、Groq、Together、OpenRouter 等。
- 中转/聚合类可选少量常见社区中转模板（标注「第三方中转，自行核实」）。

每条预设字段：`id、显示名、协议、base_url、默认模型建议列表、官网/文档链接、图标标识`。实现为 Go 内嵌数据表 + UI 网格选择；不从 cc-switch 仓库拷贝源码。

## 8. 网关与桥接

- bridge 新增/调整 API：逻辑模型 CRUD、目标排序、渠道 CRUD（单 Key）、预设列表、接入片段生成、网关启停状态（已有）。
- router 输入从「渠道池 + 渠道级优先级」改为「逻辑模型目标链」；渠道仍作为执行单元。
- 协议层（anthropic/openai-compat）与上游错误分类不变。

## 9. 安全

- 渠道 Key：OS 钥匙串单值；导出默认脱敏。
- 网关密钥与管理面令牌维持相互独立（既有 §8）。
- base_url 新增/编辑仍走 SSRF/私网校验。
- 目标链编辑不落任何密钥进 SQLite 明文。

## 10. 测试要点（延续 Fake Provider）

- 逻辑模型：多模型并存互不串扰；目标顺序生效。
- 故障转移：首字节前切换、首字节后不重放、400 不重试、客户端取消、并发冷却原子性（`-race`）。
- 删库重建：旧库文件存在时应用按「库不存在」路径重建 schema 并可启动；无逻辑模型时网关与 UI 正常。
- 旧代码清理：`go build ./...` / `go vet ./...` 全绿，无对 channel_keys、旧渠道级路由入口的残留引用。
- UI 桥接：CRUD 经 bridge 往返；底条状态与 Gateway.Status 一致。
- 预设：每个内置预设字段完整、base_url 可被 SSRF 校验接受。

## 11. 后续方向（明确不在首版）

十工具接入、MCP/Skills/Prompts、会话阅读、用量配额、Direct/Routing/Aggregation 显式模式、图标轨收起动画、预设扩到 90+。

## 12. 已决事项（2026-10-17 收口）

1. 「透传未登记模型」**默认关闭**（未登记模型名返回明确错误）；设置里可开；**首版开关仅预留**，打开后若仍无候选同样 404（不做半成品透传上游）。
2. 接入页**只生成配置片段**供复制，不自动写入 Claude Code / Codex 配置文件；片段含 base_url、网关密钥、**用户在接入页选定的逻辑模型**（Claude Code：`ANTHROPIC_MODEL` + `ANTHROPIC_SMALL_FAST_MODEL`；Codex：`OPENAI_MODEL` 等价配置），避免默认真实模型名打到「未登记」。
3. 存储 **删库重建**，不做 migration v2；旧代码直接删除。检测到旧 schema（存在 `channel_keys`）时**启动报错**提示删除库文件，不静默清库。
4. **Codex 首版真接入**：网关新增 OpenAI 入站 `POST /v1/chat/completions`，与 Anthropic 入站共用逻辑模型/冷却/故障转移核心；客户端协议与上游协议不一致时在适配层转换。
5. 遥测**不落 SQLite**：沿用内存环 + 日志文件。
6. 渠道表**保留** `allow_private_upstream`；新增可空语义 `preset_id`（预设回显）；主键保持 TEXT `NewID()`。
