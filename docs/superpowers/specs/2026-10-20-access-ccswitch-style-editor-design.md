# 接入页 cc-switch 式编辑器设计（2026-10-20）

## 背景与意图

现有接入页（spec `2026-10-18-agent-access-config-design.md`，下称「前版」）的编辑器是
「名称 + 单逻辑模型下拉 + 覆盖项键值表」，并明示「不提供整份 JSON 自由编辑」。用户参照
cc-switch 的供应商配置页与配置 JSON 页（三张截图）要求改版：**角色级模型映射** +
**最终 JSON 手动编辑**，且 `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN` 恒为本地网关
地址与网关令牌（自动注入）。本设计取代前版的编辑器与合成模型；多配置列表、应用写入、
持久文件、启动命令、打开终端、令牌联动等机制沿用前版，仅其内容生成规则按本设计变更。

cc-switch 源码（`/home/zhangcao/下载/tmp/cc-switch-main`，参考提交为 2026-10 下载的
main）已核对：角色→env 键、`[1M]` 标记、快捷开关映射、表单↔JSON 同步语义均以其
实现为依据（下文注明出处）。

## 用户决策记录（2026-10-20 对话中确认，逐项）

1. **模型映射 = 角色映射表（同 cc-switch）**：默认兜底模型 + Sonnet/Opus/Fable/Haiku/
   Subagent 五个角色，每个角色一个逻辑模型下拉 + 1M 声明。
2. **JSON 是唯一真相，表单是它的视图**：每份配置在库存一份 JSON 文档；表单字段直接改
   这份文档；JSON 文本框展示同一份文档。两者永不打架（同步机制见「UI」一节）。
3. **整份接管**：Claude 配置 JSON 就是完整 `~/.claude/settings.json` 内容，「应用」时
   整份写入。**此为对前版反目标「不整文件覆盖用户配置」的明示推翻**（前版 spec 不改，
   以本文档为凭证）；写入前备份机制保留，作为唯一恢复手段。
4. **本期只改 Claude，Codex 维持现状**：Codex tab 继续用「单逻辑模型下拉 + 覆盖项」
   表单与 `logical_model_id`/`overrides` 列，零改动。
5. **快捷开关复选框与 1M 声明都要**（映射见下文「快捷开关」与「1M 声明」两节）。
6. **取消全局配置**：前版的「全局 env 合并层」（`access.global.<app_type>` 设置键、
   全局配置卡）整体移除。每份配置完全自包含。**此为对前版「配置合成规则」第 2 层与
   验收场景④的明示推翻。**

## 目标

- Claude 接入配置的编辑体验对齐 cc-switch：角色映射下拉、快捷开关、最终 JSON 手编。
- `ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN` 自动注入、用户改不动（保存时剥回真值）。
- 网关正确路由带 `[1M]` 后缀的模型名。
- 用户已有配置经删库重建流程可预期地重建（项目惯例，非无感迁移）。

## 非目标

- Codex 侧的角色映射 / JSON 编辑（后续按需另开设计）。
- cc-switch 的供应商侧能力：供应商库、连通测速、API 地址「完整 URL」模式、API 格式/
  认证字段选择、自定义 User-Agent、本地代理 Header/Body 覆盖——AgentSwitch 的上游
  细节在渠道页，不在接入页。
- 通用配置（cc-switch 的「应用通用配置」勾选）——全局层已按决策 6 取消。
- 显示名称列（cc-switch 的 `ANTHROPIC_DEFAULT_*_MODEL_NAME` 可编辑列）——表单自动
  维护该键（见「角色映射」），不做独立可编辑列；用户可在 JSON 里改。
- Gemini CLI 等新 Agent 类型。

## 反目标（做错即失败）

- 锁定键（`ANTHROPIC_BASE_URL`/`ANTHROPIC_AUTH_TOKEN`）落库或被用户值持久化：库里
  的 `config_json` **永远不含这两键**（保存时剥离）；它们只在合成（展示/写文件）时注入。
- 表单与 JSON 双向同步出现「静默丢改动」：任何表单操作前若 JSON 非法，禁用表单控件并
  提示，绝不静默重置文本框。
- 「应用」再退回读旧文件合并——整份接管语义下读-合并-写是错的。
- 日志/UI 诊断明文展示网关令牌（沿用现状：掩码展示；JSON 文本框内为用户自己的本机
  配置，与 cc-switch 同暴露面，不额外扩散）。

## 数据模型（storage，删库重建为单一 migration v1）

前版的 v1+v2+v3 迁移全部合并重写为新 v1（项目惯例：重构均为删库重建）。渠道、逻辑
模型、`channel_secrets` 等表照现状并入；`access_configs` 变更：

```sql
CREATE TABLE access_configs (
  id              TEXT PRIMARY KEY,
  app_type        TEXT NOT NULL,             -- 'claude' | 'codex'
  name            TEXT NOT NULL,             -- 同 app_type 内唯一
  logical_model_id TEXT NOT NULL DEFAULT '', -- codex 用；claude 行恒空串
  overrides       TEXT NOT NULL DEFAULT '{}',-- codex 用；claude 行恒 '{}'
  config_json     TEXT,                      -- claude 用；codex 行恒 NULL
  is_current      INTEGER NOT NULL DEFAULT 0,
  sort_order      INTEGER NOT NULL DEFAULT 0,
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_access_configs_app_name ON access_configs(app_type, name);
CREATE UNIQUE INDEX idx_access_configs_app_current ON access_configs(app_type) WHERE is_current = 1;
```

- **Claude 行**：`config_json` 存配置文档（JSON 对象文本，**不含锁定键**）；
  `logical_model_id` 恒 `''`、`overrides` 恒 `'{}'`。
- **Codex 行**：沿用前版字段，`config_json` 恒 `NULL`。
- 全局配置设置键 `access.global.<app_type>` 不再存在（重建后自然消失；代码不再读写）。
- 列表行「→ 模型名」展示：claude 行解析 `config_json.env.ANTHROPIC_MODEL`（原值含
  `[1M]` 也原样显示）；codex 行照旧解析逻辑模型。

### 旧库守卫（`rejectLegacySchema` 升级）

重建后新 v1 与旧库（旧 v1~v3）必须可判定地区分，任一命中即报既有 `errLegacyDB`
（文案沿用「删库重建，不提供就地迁移；请删除数据库文件后重启」）：

1. 存在旧表 `channel_keys`/`providers`/`accounts`（沿用现名单）；
2. `schema_migrations` 记录了 version ≥ 3，或记录了 v2 但 `logical_models`
   缺元数据列（见下方 2026-10-27 修订）；
3. `access_configs` 存在但 `PRAGMA table_info` 缺 `config_json` 列。

> 2026-10-27 修订：migration v2 自本日起合法（新库记录 v1+v2，`logical_models`
> 增加 `display_name`/`context_window`/`reasoning_levels`/`default_reasoning_level`
> 元数据列）。守卫规则相应改为：记录 v3 的旧库、旧表形态（`channel_keys` 等）
> 与列形状不符的残库仍拒绝；记录 v2 时仅当元数据列存在才就地放行。详见
> `2026-10-27-codex-access-models-metadata-design.md` §4.1。

## 配置文档规格（写入内容的唯一定义）

一份 Claude 配置 = 一个 JSON 对象，语义上是完整 `settings.json`：

- 允许任意顶层键（`env`、`mcpServers`、`attribution`、`alwaysThinkingEnabled` ……）。
- `env` 若存在必须是 `map[string]string` 形态（值为字符串；数字/布尔值拒绝并报错——
  Claude Code 的 env 均为字符串，避免静默 `fmt` 转换歧义）。
- **必需键**：`env.ANTHROPIC_MODEL` 非空（TrimSpace 后）。为空时 Claude Code 会以
  内置模型名发请求，网关按「未登记模型」报错——保存时直接拦下，错误更早更明确。
- **锁定键**：`ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN`。保存时从用户输入中剥离；
  合成时注入：

```
合成 JSON = 解析(config_json) → env["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:<port>"
                                          env["ANTHROPIC_AUTH_TOKEN"] = "<gateway_token>"
          → 美化序列化
```

  无其他合并、无其他派生：文档里有什么，最终就是什么。

### 保存校验（bridge 层）

| 检查 | 失败行为 |
|---|---|
| 合法 JSON 对象（非数组/非标量） | 报「配置 JSON 不是合法的对象」 |
| `env` 存在时为对象且值全为字符串 | 报具体键名 |
| `env.ANTHROPIC_MODEL` 非空 | 报「默认兜底模型不能为空」 |
| 锁定键被用户修改 | 剥回真值；返回警告文案（非错误，保存继续） |

### 写入路径（整份接管）

- **Apply**：备份（沿用前版 `backupLive`）→ **整份写** `~/.claude/settings.json`
  （0600 原子写；不再读旧文件、不合并）→ 置 `is_current`。
- **持久文件**：`~/.agentswitch/access/claude/<id>.json` = 合成 JSON（含锁定键）。
  `claude --settings <路径>` 启动命令机制不变。
- **RegenerateDerived**（令牌/端口变更联动）：流程不变——重合成、重写全部持久文件与
  current 的 live 文件。
- Codex 的全部写入路径零改动。

## 角色映射

表单行 ↔ 文档键（映射与回落语义镜像 cc-switch
`src/components/providers/forms/hooks/useModelState.ts`）：

| 表单行 | 写入的 env 键 | 说明 |
|---|---|---|
| 默认兜底模型 | `env.ANTHROPIC_MODEL` | 必需；未命中角色的请求用它 |
| Sonnet | `env.ANTHROPIC_DEFAULT_SONNET_MODEL` | 可空（空=删键） |
| Opus | `env.ANTHROPIC_DEFAULT_OPUS_MODEL` | 可空（空=删键） |
| Fable | `env.ANTHROPIC_DEFAULT_FABLE_MODEL` | 可空（空=删键） |
| Haiku | `env.ANTHROPIC_DEFAULT_HAIKU_MODEL` **+ `env.ANTHROPIC_SMALL_FAST_MODEL`（同值）** | 兼容仍读旧键的 Claude Code |
| Subagent | `env.CLAUDE_CODE_SUBAGENT_MODEL` | 可空（空=删键） |

- **显示名**：四角色（Sonnet/Opus/Fable/Haiku）写入模型值时同步维护
  `env.ANTHROPIC_DEFAULT_*_MODEL_NAME` = 剥掉 `[1M]` 后的名（保证 /model 菜单显示
  干净名；cc-switch 的 NAME 默认值同为剥后缀名）。用户在 JSON 里改过 NAME 后，表单
  再次改动该角色时以表单值重新覆盖（表单操作优先，文档可手改回）。
- 角色下拉留空 = 不写该键（Claude Code 内部回落链生效：fable→opus→兜底；haiku→
  small→兜底）。下拉选项 = **启用中**的逻辑模型名（禁用的不在下拉出现；JSON 里手写
  的禁用名/不存在名不拦截保存——延续前版「逻辑模型变动不联动、不阻塞」原则，请求时
  网关报明确错误）。
- 值的来源永远是逻辑模型名；表单不提供自由文本模型输入。

## 1M 声明

- 表单：每个模型行一个「1M」勾选。勾选 = 存入文档的值追加 `[1M]` 后缀；下拉回显时
  剥后缀（下拉选项始终是干净逻辑模型名）。机制镜像 cc-switch `CLAUDE_ONE_M_MARKER`。
- **网关侧**（必须配套，否则带后缀名路由失败）：`app` 层逻辑模型解析（`GetByName`
  调用点）改为——先精确匹配；未命中再剥离尾部 `[1M]`（大小写不敏感，同 cc-switch
  `stripClaudeOneMMarker`）重试；两者皆无 → 既有「未登记模型」明确错误。两个入站
  协议共用该解析点，行为一致。

## 快捷开关

五个复选框，勾选态**从当前 JSON 文档解析**，点击 = 改文档（映射已从 cc-switch 源码
`CommonConfigEditor.tsx` 与 CHANGELOG 第 587 行查证）：

| 开关 | 勾选写入 | 取消 |
|---|---|---|
| 隐藏 AI 署名 | 顶层 `attribution: {"commit":"","pr":"","sessionUrl":false}` | 删顶层键 |
| Teammates 模式 | `env.CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS="1"` | 删该 env 键 |
| 启用 Tool Search | `env.ENABLE_TOOL_SEARCH="true"` | 删该 env 键 |
| 最大强度思考 | `env.CLAUDE_CODE_EFFORT_LEVEL="max"` | 删该 env 键 |
| 禁用自动升级 | `env.DISABLE_AUTOUPDATER="1"` | 删该 env 键 |

- env 删键后若 `env` 为空对象则一并删 `env` 顶层键（同 cc-switch）。
- JSON 非法时开关禁用（同表单控件）。
- 「隐藏 AI 署名」取消勾选仅在文档里该对象与标准形态完全一致时执行删除；不一致
  （用户手改过）则禁用勾选交互并提示走 JSON 编辑——避免静默删用户数据。

## UI（接入页改版，MyGo 原生视图，遵循 `internal/desktop/theme.go`）

页面骨架不变（Agent tab 行 → 配置列表卡 → 编辑器卡）；变化：

- **删除全局配置卡**（决策 6）。
- 配置列表行操作不变；行内「→ 模型名」按数据模型一节的规则展示。
- **编辑器卡**（仅 Claude tab 新版；Codex tab 表单照旧）自上而下：

```
标题（新建/编辑）+ 名称输入
网关（只读行）: http://127.0.0.1:<port> · 令牌掩码 · 说明「保存/应用时自动注入，不可手工指定」
默认兜底模型: [逻辑模型下拉] [1M 勾选] · 说明（对应 ANTHROPIC_MODEL）
模型角色表: Sonnet / Opus / Fable / Haiku / Subagent 各一行 [下拉] [1M]
  （Haiku 行附注：同时写入 ANTHROPIC_SMALL_FAST_MODEL）
快捷开关行: 五个复选框
配置 JSON: [从当前 settings.json 导入] 按钮 + ui.TextArea（等宽字体）+ 有效性状态行
[保存] [取消]
```

### 表单 ↔ JSON 同步（单一真相 = 内存中一份 JSON 文本）

- **打开编辑器**：读库内 `config_json` → 注入锁定键真值 → 美化 → 填入 JSON 文本框；
  名称、下拉、1M、开关全部从该文本解析回显（新建时以空文档 `{}` 起步，兜底模型
  默认取第一个启用逻辑模型）。
- **改表单控件**（下拉/1M/开关/名称）：解析当前文本 → 改对应键 → 美化写回文本框。
  文本非法 → 控件禁用并提示「配置 JSON 有误，请先修正」（cc-switch 的 refuse 行为）。
- **改文本框**：立即为真相；下一次表单控件操作时重解析回显。有效性状态行实时显示
  解析结果（✓ 有效 / ✗ 错误摘要）。
- **保存**：走「保存校验」表 → 剥锁定键 → 存库（claude 走 `config_json` 新签名；
  codex 沿用旧签名）。
- MyGo 为 immediate-mode：实现层以「文本为唯一状态源，控件事件触发重解析」组织，
  具体接线在实现计划细化。

### 「从当前 settings.json 导入」

按钮仅 Claude 编辑器：读 `~/.claude/settings.json` 全文 → 填入 JSON 文本框（覆盖当前
文本，作为新建起点；不自动保存）。文件不存在/解析失败 → 内联中文错误。整份接管模式
下这是防丢配置的安全阀（用户现有 mcpServers/插件配置可带入）。

## 桥接层（internal/bridge）

- `AccessConfigs` 变更：
  - `Create(appType, name, logicalModelID, overrides, configJSON)` / `Update(...)` 增
    `configJSON` 参数（claude 传文档文本、忽略旧参数；codex 反之）。校验按「保存
    校验」表；claude 行落库前剥离锁定键。
  - `AccessConfigView` 增 `ConfigJSON string`（codex 为空串）。
  - `composeFor`：claude 走「配置文档规格」的注入合成；codex 照旧。
  - `writeClaudeLive`：改整份写（删除读-合并-写逻辑）。
  - `GlobalConfig`/`SaveGlobalConfig` 删除。
- `app` 层：逻辑模型解析点增加 `[1M]` 剥离重试（精确优先）。
- `desktop`：`access.go` 编辑器重写；删除全局配置卡相关状态与渲染。

## 测试策略

全部离线（`t.TempDir()` 注入 home、httptest Fake Provider）：

- **bridge**：合成注入（任意顶层键保留、锁定键覆盖）；保存校验四条（非法 JSON、
  env 非对象/非字符串值、缺兜底模型、锁定键剥回+警告）；Apply 整份接管（旧文件键
  **不**保留——与前版 merge 测试相反断言）；备份仍生成；`is_current` 唯一；派生
  联动（令牌轮换刷持久文件与 current live）；持久文件内容=合成 JSON；旧库守卫三条
  判定（含 `access_configs` 缺列）。
- **app**：`[1M]` 解析——精确命中优先、大小写不敏感剥后缀重试、皆无时报未登记错误
  （Anthropic/OpenAI 两入站）。
- **desktop**：表单↔JSON 同步纯函数（解析回显、表单改文本、开关写删键、非法禁用
  判定）；编辑器渲染快照（区块文案、五角色行、五开关、导入按钮）。
- 并发/状态类测试带 `-race`。

## 验收场景（第一版完成的定义）

1. 新建 Claude 配置：兜底模型 + 各角色选不同逻辑模型（部分勾 1M）→ JSON 区实时呈现
   最终内容 → 应用 → `~/.claude/settings.json` 整份等于该 JSON、锁定键为网关真值。
2. JSON 区手改（加 `mcpServers`、改角色键）→ 保存 → 表单回显一致 → 应用生效。
3. 手改锁定键 → 保存 → 被剥回真值 + 警告文案，保存成功。
4. 勾「Teammates 模式」→ JSON 出现 `CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS`；取消 → 键消失。
5. 请求模型名 `my-code[1M]` → 网关剥后缀命中逻辑模型 `my-code` 正常路由。
6. 重新生成网关令牌 → current live 文件与全部持久文件锁定键自动刷新。
7. 「从当前 settings.json 导入」→ 文本框出现原文件全文；保存后成为该配置内容。

## 对现有代码的影响

- `internal/storage`：schema 重建单一 v1（`access_configs` 增 `config_json`）；
  `rejectLegacySchema` 守卫升级。
- `internal/bridge/accessconfigs.go`：合成/校验/写入重写（整份接管）；删除全局配置
  方法；Create/Update 签名扩展。
- `internal/app`：模型解析 `[1M]` 剥离。
- `internal/desktop/access.go`：编辑器重写（角色表、开关行、JSON 区、导入按钮）；
  删除全局配置卡。
- 前版 spec `2026-10-18-agent-access-config-design.md` 不改；其「配置合成规则」「接管
  写入」「UI」「测试策略」与本文档冲突处以本文档为准（推翻点已在「用户决策记录」
  逐条列明）。
- `CLAUDE.md` 项目状态段在实现完成后随代码同步更新。

## 留给实现计划的细节

- MyGo immediate-mode 下文本框与控件的重解析触发点（每帧解析 vs 事件驱动缓存）——
  以「文本为唯一状态源」为约束，实现计划里定接线方式与防抖。
- JSON 有效性状态行的错误摘要格式（Go `encoding/json` 的 `SyntaxError.Offset` 换算
  行号）。
- 导入按钮读文件失败的具体错误文案层级（不存在 vs 非法 JSON vs 权限）。
- 编辑器卡在窗口窄宽度下的布局（角色表两列的最小宽度，遵循 theme.go 既有断点习惯）。
