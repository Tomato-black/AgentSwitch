# Agent 接入配置管理设计（2026-10-18）

> 注：Codex 接入的 `wire_api` 已于 2026-10-23 改为 `responses`（见 `2026-10-23-openai-responses-inbound-design.md`），本文中 `wire_api = "chat"` 的表述已被取代。

> 注（2026-10-27）：Codex 桌面版与 CLI 共用 `~/.codex/config.toml`；第三方 Key 改写入 `[model_providers.agentswitch].experimental_bearer_token`，`auth.json` 只由 Codex 维护官方 ChatGPT 登录、接入配置不读写（Codex 0.149 起自定义 provider 不再从 `auth.json` 继承凭据）。「应用」同时生成 `~/.codex/agentswitch-model-catalog.json` 并写 `model_catalog_json`——桌面版模型选择器只读该文件，不读网关 `/v1/models`。下文 Codex 行已按此修订。
>
> 注（2026-10-27，多逻辑模型与元数据）：Codex 接入配置不再只绑定一个逻辑模型，而是在 settings 文档中存有序 `models` 列表（第一项为默认，镜像 `logical_model_id`）；逻辑模型增加 `display_name`/`context_window`/`reasoning_levels`/`default_reasoning_level` 元数据（migration v2，纯增列非破坏性）。模型目录改为按显式列表与元数据生成，并新增 `[agents]` 子代理、自动审批覆盖、1M 上下文/压缩阈值、只读完整 `config.toml` 预览等 A 类客户端设置。**该部分以 `2026-10-27-codex-access-models-metadata-design.md` 为权威**；本文关于 Codex 单模型绑定的表述已被取代，其余机制（应用投影、备份、令牌轮换联动、启动命令/打开终端）不变。

> **状态（2026-10-19）：存储形态（overrides）、合成输入与 Claude 持久文件格式已被 `2026-10-19-access-full-settings-ccswitch-design.md` 取代；其余机制（应用投影、备份、令牌轮换联动、启动命令/打开终端、全局 env 层）仍有效。**

## 背景与意图

当前接入页（`internal/desktop/access.go`）只是展示两段可复制的环境变量片段（Claude Code / Codex CLI），用户需要自己复制并手动编辑 Agent 的配置文件，体验差、易错、无状态。

本设计把接入页升级为 **Agent 接入配置管理**：每个 Agent 支持多份「实际配置」，其中一份可被「应用」——由 AgentSwitch 直接写入该 Agent 的真实配置文件；未应用的配置可通过生成的持久文件 + 启动命令（或「打开终端」按钮）在启动 Agent 时手动指定。交互形态参考 cc-switch（本地克隆路径略），但只取其「多配置 + 应用写入 + 可视化编辑」模型，不引入其「直连多家上游供应商」的定位——AgentSwitch 的所有配置一律指向本地网关。

## 用户决策记录（对话中已确认）

1. **每个 Agent 的配置模型**：一个「全局配置」+ 任意多个「实际配置」。
   - 全局配置：应用到该 Agent 的所有实际配置（合并语义：实际配置的字段覆盖全局配置）。
   - 实际配置：可视化编辑，其中之一可被「应用」；应用 = 写入该 Agent 的真实配置文件（接管写入）。
   - 非默认（未应用）配置：用户启动 Agent 时手动指定。
2. **「手动指定」形态：两者都要**——
   - 为每个实际配置生成持久文件，接入页展示对应启动命令（如 `claude --settings <路径>`）供复制；
   - 同时提供「用此配置打开终端」按钮，一键拉起已带好参数的终端。
3. **第一版范围：仅 Claude Code + Codex CLI**（对应网关双入站协议 Anthropic `/v1/messages` 与 OpenAI `/v1/chat/completions`）。其他 Agent（Gemini CLI 等）后续按同一模式扩展，本设计的数据模型为此预留 `app_type`。
4. **Codex `config.toml` 重写采用语义保留**：键值不丢即可，用户注释允许丢失、格式允许重排（不自研注释保留编辑器）。
5. **逻辑模型变动不联动接入配置**：逻辑模型改名/禁用/删除时，接入配置与其写入文件一律不做自动重写或阻塞，由用户自行维护（详见「逻辑模型绑定」一节）。

## 目标

- 一键应用：在 UI 中对某实际配置点「应用」，对应 Agent 的真实配置文件受管键立即更新，Agent 下次启动即生效。
- 多配置并存：每个 Agent 任意多个实际配置，恰好一个处于「使用中」（或没有）。
- 全局配置合并：全局 env 键值自动并入每个实际配置的写入结果，实际配置的同名键优先。
- 手动指定路径可用：每个实际配置有持久文件 + 可复制启动命令 + 「打开终端」。
- 可视化编辑：MyGo 原生表单编辑（名称、绑定逻辑模型、覆盖项、全局配置），不需要用户手写 JSON。
- 安全与可恢复：写入前备份原文件；只替换受管键，用户其余内容原样保留。

## 非目标（Non-goals）

- 不支持配置绕过本地网关直连上游（base_url 永远是本地网关地址；这是 AgentSwitch 与 cc-switch 的本质定位差异）。
- 不做第一版以外的 Agent（Gemini CLI、Claude Desktop 等）。
- 不做 cc-switch 式「检测连通 / 端点测速 / 供应商预设库 / 统一供应商」。
- 不做配置导入导出、云同步。
- 不做 MCP / 提示词 / 会话管理。

## 反目标（Anti-goals，做错即失败）

- 不整文件覆盖用户配置：写入必须是「受管键投影」——读原文件 → 只改受管键 → 写回，绝不清空用户手工加的其他内容。
- 不在日志/UI 诊断中明文展示网关令牌（沿用现状：展示掩码、复制给真值）。
- 不依赖真实上游做测试：全部走离线 Fake Provider / 临时目录文件断言。

## 数据模型（storage，migration v3）

### 新表 `access_configs`

```sql
CREATE TABLE access_configs (
  id              TEXT PRIMARY KEY,          -- uuid
  app_type        TEXT NOT NULL,             -- 'claude' | 'codex'
  name            TEXT NOT NULL,             -- 用户可编辑，同 app_type 内唯一
  logical_model_id TEXT NOT NULL,            -- 绑定的逻辑模型（logical_models.id）
  overrides       TEXT NOT NULL DEFAULT '{}',-- JSON：覆盖 env 键值对，如 {"ANTHROPIC_SMALL_FAST_MODEL":"..."}
  is_current      INTEGER NOT NULL DEFAULT 0,-- 该 app_type 下至多一行 = 1
  sort_order      INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_access_configs_app_name ON access_configs(app_type, name);
CREATE UNIQUE INDEX idx_access_configs_app_current ON access_configs(app_type) WHERE is_current = 1;
```

### 全局配置

存 `settings` KV（不新增表）：键 `access.global.<app_type>`，值为 JSON env 键值对，如
`{"HTTPS_PROXY":"http://127.0.0.1:7890"}`。

### 配置合成规则（写入内容的唯一定义）

对某实际配置 C（app_type=A）：

```
合成 env = {
  // 1. 网关派生基础键（不可被覆盖，来自 bridge.Gateway.Status()）
  claude: { ANTHROPIC_BASE_URL: "http://127.0.0.1:<port>",
            ANTHROPIC_AUTH_TOKEN: "<gateway_token>",
            ANTHROPIC_MODEL: "<逻辑模型名>",
            ANTHROPIC_SMALL_FAST_MODEL: "<逻辑模型名>" }
  codex:  { OPENAI_BASE_URL: "http://127.0.0.1:<port>/v1",
            OPENAI_API_KEY: "<gateway_token>" }
  // 2. 全局配置 env（access.global.<app_type>）
  // 3. C.overrides（最高优先，但不得覆盖第 1 组基础键）
}（第 3 组 > 第 2 组；第 1 组恒定）
```

Codex 的 `model` 不走 env：写入 `~/.codex/config.toml` 的受管键 `model = "<逻辑模型名>"`（2026-10-27 起取有序 `models` 列表首项，见 `2026-10-27-codex-access-models-metadata-design.md` §4.2）。

## 接管写入（应用配置）

「应用」= 合成 C 的内容 → 投影进该 Agent 的真实配置文件 → 置 `is_current=1`（同 app_type 先全部清 0）。

| Agent | 目标文件 | 受管键 | 其余内容 |
|---|---|---|---|
| Claude Code | `~/.claude/settings.json` | `env.ANTHROPIC_BASE_URL`、`env.ANTHROPIC_AUTH_TOKEN`、`env.ANTHROPIC_MODEL`、`env.ANTHROPIC_SMALL_FAST_MODEL` | 原样保留（含用户其他 env、permissions、model 等） |
| Codex（CLI / 桌面版） | `~/.codex/config.toml` + `~/.codex/agentswitch-model-catalog.json` | config.toml：`model`、`model_provider = "agentswitch"`、`model_catalog_json = "agentswitch-model-catalog.json"`、`[model_providers.agentswitch]`（`base_url`、`wire_api = "responses"`、`requires_openai_auth = false`、`experimental_bearer_token = 网关令牌`，**不用** `env_key`）；目录：按有序 `models` 列表生成（首项即绑定模型，2026-10-27 起，见 `2026-10-27-codex-access-models-metadata-design.md` §5）；`auth.json` 不读写（只由 Codex 维护官方登录） | 文件其余键/段语义保留 |

写入安全（我的决策，实现时照此执行）：

- **读-合并-写**：解析原文件 → 只改受管键 → 序列化写回；解析失败（非法 JSON/TOML）则中止并报错，不猜测修复。
- **保留级别**：JSON（Claude `settings.json`）键值语义保留；TOML（Codex `config.toml`）按用户决策语义保留——注释允许丢失、格式允许重排。
- **Codex 格式权威**：`[model_providers.agentswitch]` 以 cc-switch 实测可用模板为准（`wire_api = "responses"`、key 写 provider 表的 `experimental_bearer_token`、显式 `requires_openai_auth = false`，防止用户已登录 ChatGPT 账号时 Codex 把官方 OAuth 令牌误发给本地网关）。2026-10-27 修订：不再写 `auth.json`（Codex 0.149 起自定义 provider 不从该文件继承凭据；桌面版从 Dock 启动也不继承 shell env，故不用 `env_key`）；同时用 Codex 自带 `debug models --bundled` 模板生成 `agentswitch-model-catalog.json` 并写 `model_catalog_json`（桌面版模型选择器只读该文件；旧 cc-switch 目录里的模型名会让网关 404）。本机未装 Codex，端到端验证列为实现计划的前置步骤。
- **应用前备份**：把原文件复制到 `~/.agentswitch/backups/<app_type>/<时间戳>-<文件名>`，每 app_type 只保留最近 10 份。
- **令牌/端口联动**：网关令牌重新生成或端口变更后，自动重写「当前应用」配置的 live 文件与所有已生成的持久文件（用户无感，不弹窗）。
- **删除使用中的配置**：先清 `is_current`，live 文件保持最后一次写入内容不变（不回滚、不清空），UI 提示「已删除，文件保留最后状态」。

## 逻辑模型绑定（按用户决策：变动不联动）

- `access_configs.logical_model_id` 仅作 UI 展示与选择之用，不设外键、不加 `ON DELETE` 级联；逻辑模型的改名/禁用/删除均不触发接入配置或任何文件的重写，也不阻塞逻辑模型自身的操作。
- 写入文件（live 与持久文件）中的模型名在**每次写入时**从当前逻辑模型记录解析：改名后用户重新应用/保存即自然带入新名，不做自动联动。
- 若绑定的逻辑模型已不存在，「应用」「生成持久文件」「打开终端」报错提示用户改绑；其余功能不受影响。禁用的逻辑模型不额外拦截（网关侧对未注册/禁用名的行为按既有网关规则处理，由用户自行维护）。

## 非默认配置：持久文件 + 启动命令 + 打开终端

### 持久文件

每个实际配置在保存/更新时同步生成一份文件（内容随令牌联动刷新）：

```
~/.agentswitch/access/<app_type>/<config_id>.json   # claude：{"env": {合成 env}}
~/.agentswitch/access/<app_type>/<config_id>.env    # codex：KEY=VALUE 行
```

Windows 无 `sh` source 语义，改为生成 `<config_id>.cmd` 启动器（CRLF、0600）：
claude 在 `.json` 之外另加 `.cmd`（引用 settings JSON），codex 直接以
`set "KEY=VALUE"` + `codex -m "<model>"` 启动。批处理中的 `%` 转义为 `%%`，
值/模型含双引号或换行时拒绝生成（行格式无法安全表达）。

用 `config_id`（而非名称）作文件名，避免改名导致用户已复制的命令失效；UI 展示的真实路径以实际生成为准。

### 启动命令（接入页展示，可一键复制）

- Claude Code：`claude --settings ~/.agentswitch/access/claude/<config_id>.json`
  （`--settings` 只覆盖其中给出的键，未给的键回落 `~/.claude/settings.json`。）
- Codex（CLI / 桌面版）：Codex 无整份配置文件参数，用环境变量方式：
  `env $(cat ~/.agentswitch/access/codex/<config_id>.env) codex -m <逻辑模型名>`
  （具体拼装以实现验证为准，设计只要求：复制出来的命令在干净 shell 里可直接运行。）
- Windows（2026-10-26）：统一为 `cmd /c call "<config_id>.cmd"`——cmd 与
  PowerShell 均可直接粘贴；令牌只落在 0600 启动器文件，不出现在命令文本中。

### 「用此配置打开终端」按钮

点按钮 → 拉起系统终端，工作目录为用户常用目录（默认 home），预置好上述启动命令（粘贴到输入行或自动执行，实现时取最可靠方式）。macOS 经 AppleScript（`osascript`）执行；Windows 经 `cmd /c start "" "<config_id>.cmd"`；其他平台明确报「当前平台不支持打开终端」（2026-10-26 扩展 Windows）。

## UI（接入页改版，MyGo 原生视图，遵循 `internal/desktop/theme.go` 设计系统）

接入页整体结构（取代现有「逻辑模型选择器 + 两张片段卡」）：

```
[ Agent 切换 Tab：Claude Code | Codex ]

┌─ 全局配置卡 ────────────────────────────────┐
│ env 键值列表（键/值两列，可增删改）  [保存]  │
│ 说明：应用到本 Agent 的所有实际配置          │
└─────────────────────────────────────────────┘
┌─ 实际配置列表 ──────────────────────────────┐
│ 行：名称 | 绑定逻辑模型 | [使用中]徽标      │
│     [应用] [编辑] [复制启动命令] [⋯▾]       │
│     ⋯▾： [用此配置打开终端] [删除]          │
│                                  [+ 新建配置]│
└─────────────────────────────────────────────┘
```

- 配置编辑器（新建/编辑）：表单字段 = 名称、绑定逻辑模型（下拉，来自 enabled 逻辑模型）、覆盖 env 键值列表（高级折叠区，默认收起）；不提供整份 JSON 自由编辑（网关场景不需要，降低误配面）。
- 无「使用中」配置时列表顶部显示提示条：「当前没有任何配置被应用，Agent 仍使用其原配置文件」。
- 现有设置键 `access.logical_model`（`access.last_logical_model`）随本设计废弃：选择状态由 `access_configs.is_current` 承担。
- 网关未启动/令牌不可用时：「应用」「打开终端」「复制启动命令」禁用并给出原因（网关未运行）。

## 桥接层（internal/bridge）

新增 `bridge.AccessConfigs`（沿用现有 bridge 模式）：

- `List(appType)` / `Create` / `Update` / `Delete` / `SetCurrent(id)`
- `GlobalConfig(appType)` / `SaveGlobalConfig(appType, env)`
- `Apply(id)`：合成 → 备份 → 写 live 文件 → 置 is_current
- `LaunchCommand(id)`：返回展示用启动命令（含真实路径）
- `OpenTerminal(id)`：生成/刷新持久文件并拉起终端
- `RegenerateDerived()`：令牌/端口变更后的联动重写（由 app 层在令牌重生成后调用）

`bridge.Access`（现有片段生成）在本设计落地后删除。

## 测试策略

全部离线、临时目录（`t.TempDir()` + 环境变量/注入覆盖 home 路径）：

- 合成规则：基础键不可覆盖、overrides 优先于全局、逻辑模型名正确带入。
- 投影写入：原文件含用户自定义键 → 应用后受管键更新、其余键值语义保留（TOML 注释允许丢失）；非法文件中止并报错。
- 备份：应用前生成备份，超过 10 份滚动清理。
- is_current 唯一性、切换、删除使用中配置的行为。
- 持久文件生成与令牌联动重写。
- 并发/联动涉及状态更新的测试带 `-race`。

## 验收场景（第一版完成的定义）

1. 在 UI 为 Claude Code 新建配置「工作」（绑定逻辑模型 my-code）并应用 → `~/.claude/settings.json` 四个受管键更新，文件其余内容不变。
2. 再新建配置「个人」（绑定另一逻辑模型）→ 复制其启动命令，在终端执行可正常经网关对话；「用此配置打开终端」按钮同效。
3. 为 Codex 完成同样流程（config.toml + env 启动命令；`auth.json` 不读写）。
4. 修改全局配置并保存 → 重新应用后 live 文件包含全局 env；「个人」配置的持久文件同步刷新。
5. 重新生成网关令牌 → live 文件与所有持久文件自动刷新，无需手动操作。

## 对现有代码的影响

- `internal/desktop/access.go`：整体重写（新页面结构）。
- `internal/bridge/access.go`：删除，由 `bridge.AccessConfigs` 取代。
- `internal/storage`：migration v3 增加 `access_configs`；`Store` 接口增加 AccessConfigs repo。
- 设置键 `access.logical_model` 废弃（读到时忽略即可，不迁移）。
- 现有「复制片段」能力不保留为独立形态（被「复制启动命令」取代；应用后的 live 文件即是用户想要的最终状态）。

## 留给实现计划的细节

- Codex 启动命令的最终拼装格式（以本机验证可运行为准；**前置：本机安装 Codex CLI**——当前未安装，live 文件与启动命令的端到端验证依赖它）。
- 「打开终端」的具体拉起方式（Terminal.app / osascript，取最可靠者）。
- MyGo 表单控件的具体组合（遵循 theme.go 与现有 channels/logical 编辑页惯例）。
- `~/.agentswitch/` 目录布局若与现有 `master.key`/备份目录冲突，以不冲突的子路径为准并回填本文档。
