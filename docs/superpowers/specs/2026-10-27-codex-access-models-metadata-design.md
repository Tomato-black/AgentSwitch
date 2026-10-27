# Codex 接入多逻辑模型与元数据设计（2026-10-27）

> 状态：待用户审阅。本文取代 `2026-10-18-agent-access-config-design.md` 中「每个 Codex 接入配置只绑定一个逻辑模型」以及 `2026-10-23-openai-responses-inbound-design.md` §3.5 中「目录由所有启用逻辑模型自动生成」的表述；其余机制（应用投影、备份、令牌轮换联动、启动命令/打开终端）不变。

## 1. 背景

Codex 桌面版的模型选择器只读 `model_catalog_json` 指向的文件，不读网关 `/v1/models`。上一版实现已做到：

- 第三方 Key 写 `[model_providers.agentswitch].experimental_bearer_token`，不碰 `auth.json`；
- 用 Codex 自带 `debug models --bundled` 模板生成 `agentswitch-model-catalog.json`；
- 目录包含「绑定模型 + 所有启用逻辑模型」。

但仍有一个产品缺口：**接入页只能绑定一个默认逻辑模型，用户无法显式配置 Codex 里出现哪些逻辑模型、顺序如何**。用户的实际使用方式是：

> 可以使用的模型就是逻辑模型。如果我有多个逻辑模型，接入页面应该也支持我配置多个逻辑模型列表，这样就能在 Codex 中切换模型。

同时，cc-switch 的 Codex 表单暴露了推理强度、上下文窗口、子代理模型、模型显示名等客户端设置。经逐项核对，其中大部分属于 cc-switch 的「配置切换器/本地代理」定位（上游地址、Key、协议转换、OAuth、auth.json、原始 TOML 编辑器），**不迁移**；只迁移真正影响 Codex 客户端行为、且 AgentSwitch 网关无法替代的部分。

## 2. 目标

本设计对齐 cc-switch Codex 页面的 **A 类：Codex 客户端设置**，不搬 B 类上游供应商连接。

1. **Codex 接入配置支持有序逻辑模型列表**：第一项是默认模型；用户可添加、删除、上移、下移；列表写入 `config.toml` 的 `model` 与 `model_catalog_json` 目录。
2. **逻辑模型可声明元数据**：显示名、上下文窗口、思考等级列表、默认思考等级；目录条目使用这些元数据。
3. **1M 上下文与压缩阈值**：由默认逻辑模型的上下文窗口驱动，写 `model_context_window` 与 `model_auto_compact_token_limit`。
4. **Codex 高级设置**：子代理模型、子代理推理强度（`[agents]`），自动审批模型（目录 `auto_review_model_override`）。
5. **网关兼容项**：自动写 `disable_response_storage = true`、`web_search = "disabled"`。
6. **整份 config.toml 只读文本**：展示本次 Apply 后的完整 `config.toml`（受管键 + 原样保留的用户键），不可编辑；不提供 cc-switch 的原始 TOML 编辑器。
7. **存储非破坏性升级**：给 `logical_models` 增加元数据列（migration v2），旧库就地升级，不删库。

### 2.1 A 类字段对齐表

| cc-switch Codex 客户端设置 | AgentSwitch 处理 |
|---|---|
| 默认模型 | `models[0]`，写 `config.toml` 的 `model` |
| 模型列表（显示名 / 实际模型 / 上下文窗口 / 思考等级 / 默认等级） | 有序逻辑模型列表 + 逻辑模型元数据；目录自动生成，不手填模型行 |
| 子代理模型 / 推理强度 | 接入配置高级设置，写 `[agents]` |
| 自动审批模型 | 接入配置高级设置，写目录每行的 `auto_review_model_override`；空 = 主模型自己审批 |
| 1M 上下文 / 压缩阈值 | 默认逻辑模型 `context_window` 驱动，写 `model_context_window` / `model_auto_compact_token_limit` |
| `web_search` | 固定写 `"disabled"`（网关 Responses 转换会丢弃该托管工具） |
| `disable_response_storage` | 固定写 `true`（网关是无状态转发） |
| `[features]`、MCP、plugins、projects、permissions、其他 `model_providers` | 不管理；原样保留并出现在只读 config.toml 文本里 |
| `AGENTS.md` | 不管理；文件保留（如需只读入口，后续单独做） |

## 3. 非目标

- **B 类上游供应商连接**：base URL、API Key、完整 URL、测速/自定义端点、上游格式（Responses/Chat/Anthropic）、Anthropic 认证字段、模拟 Claude Code、最大输出 tokens、Codex OAuth/Copilot/xAI 账号、`auth.json` 编辑器、官方登录保留/暂存、本地路由/聚合/Stack、用量查询、prompt cache 路由、本地代理 headers/body 覆盖、自定义 User-Agent——这些属于「渠道 / 逻辑模型 / 网关」层，不进 Codex 接入页，避免 Codex 绕过网关直连上游。
- **cc-switch 代理专属的思考能力开关**（支持思考模式/思考等级、Chat 上游参数映射）：Codex 客户端始终走 Responses → 网关，协议转换由网关负责。
- **远程压缩开关**：cc-switch 通过把 provider `name` 改成 `"OpenAI"` 触发远程压缩；AgentSwitch 网关未实现该压缩端点，暂不提供，避免制造不可用开关。
- **Codex fast mode / service tier**：属于上游账号能力，不在接入页。
- **原始可编辑 config.toml / auth.json 编辑器**：只提供只读文本；受管键由 AgentSwitch 生成。
- **MCP / AGENTS.md 可视化管理**：保留用户文件，不接管。
- Claude Code / Claude Desktop 的接入形态不变（单模型 / 角色映射）。

## 4. 数据模型

### 4.1 logical_models 元数据（migration v2）

```sql
ALTER TABLE logical_models ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE logical_models ADD COLUMN context_window INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logical_models ADD COLUMN reasoning_levels TEXT NOT NULL DEFAULT '';
ALTER TABLE logical_models ADD COLUMN default_reasoning_level TEXT NOT NULL DEFAULT '';
```

- `display_name`：Codex 模型菜单显示名；空串 → 使用 `name`。
- `context_window`：0 = 不声明；>0 时写入目录条目以及默认模型的顶层窗口/压缩阈值。
- `reasoning_levels`：JSON 字符串数组（如 `["low","medium","high","xhigh"]`）；空串 = 沿用 Codex 模板。
- `default_reasoning_level`：必须属于 `reasoning_levels`；空串 = 沿用模板默认；`reasoning_levels` 非空但默认空时取列表第一项。
- 合法等级白名单：`none`、`minimal`、`low`、`medium`、`high`、`xhigh`、`max`。
- 迁移 v2 是非破坏性的；`rejectLegacySchema` 更新为允许 v2，仅继续拒绝 v3 形态的旧 `access_configs`（`overrides` 列）与 providers 时代旧表。

`LogicalModel` / `LogicalModelRepo` 增加以上字段；`Save` / `List` / `Get` / `GetByName` 读写；逻辑模型编辑页增加对应输入。

### 4.2 Codex 接入配置文档

`access_configs.settings` 的 Codex 文档扩展为：

```json
{
  "models": ["my-code", "test"],
  "codex": {
    "subagent_model": "test",
    "subagent_reasoning_effort": "high",
    "auto_review_model": "my-code"
  },
  "env": {}
}
```

- `models`：有序、非空、无重复的逻辑模型名；第一项是默认模型，必须等于 `access_configs.logical_model_id` 指向的逻辑模型名。
- `codex.subagent_model`：空串 = 跟随主模型；非空必须在 `models` 中。
- `codex.subagent_reasoning_effort`：空串 = Codex 默认；非空必须在上面的等级白名单中。
- `codex.auto_review_model`：空串 = 主模型自己审批（Codex 默认回退）；非空必须在 `models` 中。不提供 `codex-auto-review` 选项（网关没有该逻辑模型，会 404）。
- `env`：沿用现有 env 合成/归一化（锁定键剔除、未定制键剔除）。
- 兼容：旧配置缺少 `models` 时，运行时回退为「绑定模型 + 所有启用逻辑模型」；缺少 `codex` 视为空。
- `parseAccessDoc("codex", raw)` 允许且仅允许 `env`、`models`、`codex` 三个顶层键；其余键拒绝并给中文错误。

`logical_model_id` 仍是默认模型引用；保存时由 `models[0]` 映射回 ID，列表变化同步更新该列。`Create` / `Update` bridge 接口仍接收一个 `logicalModelID`（默认模型），bridge 校验它必须等于 `models[0]` 对应的逻辑模型；UI 由列表第一行计算该 ID。

## 5. 写入语义（`writeCodexLive`）

`config.toml` 受管键：

```toml
model = "<models[0]>"
model_provider = "agentswitch"
model_catalog_json = "agentswitch-model-catalog.json"
disable_response_storage = true
web_search = "disabled"

[model_providers.agentswitch]
name = "agentswitch"
base_url = "<网关 /v1>"
wire_api = "responses"
requires_openai_auth = false
experimental_bearer_token = "<网关令牌>"
```

- 默认模型 `context_window > 0` 时，追加/覆盖：
  - `model_context_window = <context_window>`
  - `model_auto_compact_token_limit = <context_window * 9 / 10>`（整数，向下取整）
- `context_window == 0` 时**不写也不删**这两个键，保留用户已有设置。
- `codex.subagent_model` 非空时写 `[agents] default_subagent_model`；为空时删除该受管键（保留 `[agents]` 下其他键）。
  `codex.subagent_reasoning_effort` 同理写/删 `default_subagent_reasoning_effort`。
- `codex.auto_review_model` 非空时，目录每一行写 `auto_review_model_override = <该模型>`；为空时不写（Codex 回落到主模型）。
- `auth.json` 仍然不读不写。
- **只读 config.toml 预览**：`Project` 纯函数产出「应用后的完整 config.toml」字节，Apply 与 UI 预览共用；UI 只读展示，可复制文本，不可编辑。与现有接入 JSON 编辑器一致，预览渲染真实网关令牌（2026-10-21 用户裁定）；用户其他键按原样显示。
- `disable_response_storage`、`web_search`、`[agents].default_subagent_model`、`[agents].default_subagent_reasoning_effort` 视为接入配置受管键：前者固定写，后两者按接入配置写/删（保留 `[agents]` 其他键）。
- 目录文件 `~/.codex/agentswitch-model-catalog.json`：
  - 条目顺序与 `models` 一致；第一项为默认（`isDefault` 由 app-server 按顺序/优先级决定，实测第一项为默认）。
  - 每个条目克隆 Codex 模板，替换：
    - `slug` / `display_name` = 逻辑模型名 / `display_name`（空则 name）
    - `description` = 逻辑模型 `description`（空则 `AgentSwitch 逻辑模型：<name>`）
    - `priority` = 1000 + 列表下标
    - `context_window` / `max_context_window` = `context_window`（>0 时）
    - `supported_reasoning_levels` = `reasoning_levels`（非空时，`description` 用等级名）
    - `default_reasoning_level` = `default_reasoning_level`（非空时）
    - 删除 `upgrade` / `upgrade_info` / `availability_nux`
  - 模板来源不变：桌面版 bundle / PATH 上的 `codex` 的 `debug models --bundled`，再退回本地已有目录。

校验：

- `models` 中任一逻辑模型不存在或已停用 → Apply / 保存返回中文错误，指明模型名。
- 默认模型（`models[0]`）必须启用；否则报错。
- 列表为空、重复、第一项与 `logical_model_id` 不一致 → 报错。

## 6. UI

### 6.1 逻辑模型编辑页

在现有名称/描述/目标链之外增加：

- 显示名（可选，目录展示用）
- 上下文窗口（数字输入；空 = 不声明；提供「1M」快捷填 1000000）
- 思考等级（逗号分隔，如 `low,medium,high,xhigh`；空 = 沿用 Codex 模板）
- 默认思考等级（下拉，来自已填写的等级；空 = 模板默认）

校验在 bridge 层；UI 内联显示中文错误。

### 6.2 Codex 接入编辑器

- 把「逻辑模型」单选改为「模型列表」：
  - 有序行，每行一个逻辑模型下拉；第一行标注「默认」。
  - 每行：上移、下移、删除；底部「＋ 添加模型」。
  - 至少一行；不允许重复。
  - 保存时 `logical_model_id` = 第一行对应 ID。
- 高级设置折叠区：
  - 子代理模型（下拉，含「跟随主模型」）
  - 子代理推理强度（默认 / low / medium / high / xhigh）
  - 自动审批模型（下拉：「主模型自己审批（默认）」+ 模型列表中的逻辑模型）
  - 说明：子代理写入 `[agents]`，自动审批写入目录 `auto_review_model_override`；改后需重启 Codex。
- 只读预览：显示「将写入 Codex 的模型」列表（名称、显示名、上下文窗口、思考等级），数据来自逻辑模型元数据。
- 只读 `config.toml` 文本预览：展示**应用后的完整 config.toml**（受管键 + 用户原样保留的 `[features]`、MCP、projects、其他 provider 等），可复制、不可编辑。
- 兼容项（`disable_response_storage`、`web_search`）不提供开关，固定写入。
- Claude Code / Claude Desktop 编辑器不变。

## 7. 兼容与迁移

- migration v2 就地升级旧库；不删库、不静默清配置。
- 旧 Codex 接入配置（无 `models`）：
  - 运行时目录回退为「绑定模型 + 所有启用逻辑模型」；
  - 用户下次编辑并保存时，UI 写入回退列表，之后显式可控。
- 旧 `agentswitch-model-catalog.json` 在下次 Apply 时整体重建。
- 旧的 `cc-switch-model-catalog.json` 不再被引用，但文件保留（不主动删除用户文件）。
- 旧对话的 provider 仍不迁移；UI 继续提示「新建对话」。

## 8. 测试

- storage：v2 迁移幂等；旧 v1 库就地升级；legacy v3 仍拒绝；元数据 round-trip。
- bridge：
  - Codex 文档解析/校验：`models` 顺序、重复、缺失/停用模型、默认模型一致性；`codex` 子代理字段白名单。
  - 目录：顺序、显示名、上下文窗口、思考等级、默认等级、禁用模型排除。
  - config.toml：`model`/`model_catalog_json`/`disable_response_storage`/`web_search`/窗口与压缩阈值/`[agents]` 写与删/自动审批覆盖。
  - 旧配置回退行为。
  - 预览：Project 纯函数与应用写入字节一致；只读文本包含用户原样保留的键。
- desktop：模型列表增删改序、默认模型映射、高级设置（子代理 + 自动审批）渲染、完整 config.toml 只读文本、错误文案。
- `go test ./...`、`go test -race ./internal/bridge ./internal/desktop ./internal/storage`、`go vet ./...`、`go build ./...`。

## 9. 风险与取舍

- **Codex 桌面版会回写客户端设置**：`model` 已由目录约束；`model_context_window`/`[agents]` 归 Codex 客户端设置，桌面版若改写以桌面版为准，接入页只负责写入初始值。
- **逻辑模型元数据与上游真实能力可能不一致**：上下文窗口/思考等级由用户声明，网关不校验上游；错误声明可能导致 400/截断，属于用户配置责任。
- **migration v2 打破「单一 v1」约定**：本次为纯增列、非破坏性；用户已选择「每逻辑模型元数据」能力，故采用就地 v2，不删库。若后续需要回退，需保留 v2 记录，不能把版本表改回 v1。
- **模型列表与逻辑模型停用/删除的耦合**：列表引用已删除/停用模型时 Apply 报错，要求用户先修正列表；不静默跳过默认模型。
