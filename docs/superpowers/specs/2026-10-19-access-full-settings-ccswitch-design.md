# 接入配置完整 settings 文档化（cc-switch 式编辑器）设计

- 日期：2026-10-19
- 状态：已评审（用户批准：后端两节 + UI/测试节 + 删库重建决策）
- 参考实现：cc-switch（`/home/zhangcao/下载/tmp/cc-switch-main`，React + Tauri）
- 关系：本设计**取代** `2026-10-18-agent-access-config-design.md` 中接入配置的
  存储形态（env 覆盖项）、合成输入与持久文件格式；全局 env 层、「应用」投影、
  备份、令牌轮换联动、启动命令/打开终端等机制沿用该 spec 不变。
  对 docx 的偏离沿用 2026-10-18 spec 的记录方式（docx 未规划接入配置管理细节，无新冲突）。

> 注（2026-10-27）：Codex 接入文档的顶层键已由仅 `env` 扩为 `env` / `models` /
> `codex`（有序逻辑模型列表 + 子代理/自动审批设置）；存储为 v1 + 就地 migration v2
> （`logical_models` 元数据列，非破坏性），不再只有单一 v1。详见
> `2026-10-27-codex-access-models-metadata-design.md` §4.2（文档）与 §4.1（migration）。

## 1. 背景与目标

现有接入配置每条只存「逻辑模型绑定 + env 覆盖项（`overrides`）」，编辑表单是
键值行。cc-switch 的供应商编辑页提供了更完整的形态：配置 JSON 是唯一共享存储，
表单字段（含 5 角色模型映射）是它的双向同步视图，文档可含任意顶层键
（`mcpServers`、`enabledPlugins` 等）。

目标：接入编辑器升级为 cc-switch 式「表单 + 完整 settings JSON 文档」，同时保持
我们与 cc-switch 的三个结构差异：

1. `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` / `ANTHROPIC_MODEL`（Codex 为
   `OPENAI_BASE_URL` / `OPENAI_API_KEY`）是**网关派生锁定键**，用户不可改
   （`ANTHROPIC_MODEL` 为半锁定：允许追加 `[1M]` 声明，2026-10-21 修订，§3.2）；
2. 所有模型选择（主模型 + 角色映射）的选项是**逻辑模型**下拉，不是自由文本；
3. 网关令牌由应用自动生成。原设计为 UI 永不展示令牌真值（遮蔽占位符）；
   **2026-10-21 用户裁定改为编辑器默认渲染真值**（见 §3.4 修订）。

## 2. 数据模型与存储（删库重建，用户 2026-10-19 决策）

沿用项目先例（2026-10-17 逻辑模型重构）：**schema 合并为单一 migration v1**，
不提供就地迁移，旧库启动报错提示删一次库。

### 2.1 最终 schema（v1 + 2026-10-27 就地 v2）

现有 v1 + v2（`channel_secrets`）+ v3（`access_configs`）全部并入新 v1；2026-10-27
另加就地 migration v2（`logical_models` 元数据列，非破坏性，见
`2026-10-27-codex-access-models-metadata-design.md` §4.1）。`access_configs` 的
`overrides` 列**删除**，改为：

```sql
CREATE TABLE access_configs (
  id TEXT PRIMARY KEY,
  app_type TEXT NOT NULL,
  name TEXT NOT NULL,
  logical_model_id TEXT NOT NULL,
  settings TEXT NOT NULL DEFAULT '{}',
  is_current INTEGER NOT NULL DEFAULT 0,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
```

- `settings` 存整份 JSON 文档：Claude 为 `{"env": {...}, ...任意顶层键}`；
  Codex 仅允许 `{"env": {...}}`（其 live 是 TOML，顶层文档编辑仅 Claude 享受）。
  2026-10-27 修订：Codex 顶层键已扩为 `env` / `models` / `codex`，见
  `2026-10-27-codex-access-models-metadata-design.md` §4.2。
- `logical_model_id` 保留——主模型仍走 ID 绑定（逻辑模型改名自动跟随）。

### 2.2 repo 层

`storage.AccessConfig.Overrides map[string]string` 字段删除，新增
`Settings string`（JSON 文本直通；解析/校验在 bridge 层做）。Save/Get/scan
随之简化（不再有 nil map 归一化逻辑）。

### 2.3 旧库守卫

`rejectLegacySchema` 扩展：除既有检查（`channel_keys`/`providers`/`accounts`
旧表、已记 v1 但 channels 缺失或无 `credential_ref`）外，新增：

- `schema_migrations` 含 version 3 → `errLegacyDB`；含 version 2 但 `logical_models`
  缺元数据列（2026-10-27 新增列）→ `errLegacyDB`（含元数据列的 v2 就地放行）；
- `access_configs` 存在但缺 `settings` 列 → `errLegacyDB`；
- 已记 v1 但缺 `channel_secrets` 表或缺 `access_configs` 表 → `errLegacyDB`
  （2026-10-19 评审补充：2026-10-17 时代 v1 库两者皆无，放行后会在首次读
  密钥/接入配置时裸 SQL 报错，正是本守卫要避免的故障形态；最终建库
  （v1 + 2026-10-27 就地 v2）时两表必然同时存在）。

文案沿用 `errLegacyDB`（删库重建、不就地迁移、删 `agentswitch.db` 重启）。

### 2.4 删除清单（不留兼容层）

- `migrationV2`/`migrationV3` 常量与 `migrations` 表项；
- `AccessConfig.Overrides` 全路径（repo/bridge/desktop）；
- desktop 旧覆盖项行编辑 helpers（`accessOverridesFromRows` 等），由文档 helpers 取代。

## 3. 配置合成

### 3.1 env 合成优先级（`composeAccessEnv` 扩展）

```
网关基础键 → 全局 env（access.global.<app>）→ 文档 env → 锁定键回写
```

- Claude 锁定键：`ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN`、`ANTHROPIC_MODEL`
  （**半锁定**，2026-10-21 修订：模型名不可改，但允许追加 `[1M]` 上下文声明后缀，
  见 §3.2 末尾）；
- Codex 锁定键：`OPENAI_BASE_URL`、`OPENAI_API_KEY`；
- `ANTHROPIC_SMALL_FAST_MODEL` 维持现状：默认=逻辑模型名，允许被文档 env 覆盖。
- 输入从 `overrides map[string]string` 改为文档 env 提取物，函数其余语义不变。

### 3.2 角色映射 = 文档 env 的普通键（UI 糖，无新结构）

| 角色 | 实际请求模型键 | 显示名称键 |
|---|---|---|
| Sonnet | `ANTHROPIC_DEFAULT_SONNET_MODEL` | `ANTHROPIC_DEFAULT_SONNET_MODEL_NAME` |
| Opus | `ANTHROPIC_DEFAULT_OPUS_MODEL` | `ANTHROPIC_DEFAULT_OPUS_MODEL_NAME` |
| Fable | `ANTHROPIC_DEFAULT_FABLE_MODEL` | `ANTHROPIC_DEFAULT_FABLE_MODEL_NAME` |
| Haiku | `ANTHROPIC_DEFAULT_HAIKU_MODEL` | `ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME` |
| Subagent | `CLAUDE_CODE_SUBAGENT_MODEL` | （无） |

- 下拉选项 = 启用的逻辑模型；空 = 不设该键（回落主模型）。
- 1M 勾选 = env 值尾部拼字面量 `"[1M]"`（cc-switch 规则）；显示名称键永不带标记。
- 值是**名字字符串**：逻辑模型改名不自动跟随角色行（主模型走 ID 绑定不受影响）——
  已知限制，与 cc-switch 同款。

**主模型 1M 声明（2026-10-21 修订，取代「主模型不提供 1M 勾选」限制）**：
Claude Code 对不认识的模型名按 200k 上下文兜底并要求 `[1m]` 后缀声明，而
`ANTHROPIC_MODEL` 此前恒为纯名，导致自建逻辑模型（如 `my-code`）必然触发
「not a model this version recognizes」提示。现改为**半锁定**：

- 文档 env 的 `ANTHROPIC_MODEL` 允许两种值：`<绑定逻辑模型名>` 或
  `<绑定逻辑模型名>[1M]`（大小写不敏感，归一一律规范为 `[1M]`）；
- 合成（`composeAccessEnv`）回写时，基名匹配则保留后缀，否则回写纯名；
- 保存归一化（`normalizeAccessEnv`）保留带 `[1M]` 的合法声明，纯名视为未定制
  剔除，基名不匹配视为篡改剔除——1M 标记随 `settings` 文档持久化，无需改表；
- 编辑器主模型下拉旁提供 1M 复选框；切换主模型时勾选状态跟随新模型名；
- 默认 `ANTHROPIC_SMALL_FAST_MODEL` 不带后缀（Claude Code 后台小模型用不着
  1M，且角色 Haiku 已有独立 1M 控制）；
- 网关路由前已剥离 `[1M]`（§3.3），路由零改动。

### 3.3 `[1M]` 网关剥离

路由匹配必须看到剥离后的模型名——注意 `Candidates(publicModel)` 在
`Router.Resolve` 之前按公开名查候选，所以剥离点是**网关双入口 Parse 之后、
Candidates 调用之前**（gateway 包共享私有助手 `stripOneM`，Anthropic +
OpenAI 两处各调一次）：剥离模型名尾部 `"[1M]"`（大小写不敏感，如 `[1m]`；
无后缀不动作），使 `my-code[1M]` 命中逻辑模型 `my-code`。若只在
`router.Resolve` 内剥离则为时已晚（候选集已按未剥离名查空）。

### 3.4 合成预览与令牌渲染（2026-10-21 修订）

bridge 新增 `PreviewDoc(appType, logicalModelID, settingsJSON) (string, error)`：
按 3.1 合成后返回整份文档的 JSON 文本（env 写回文档 + 文档顶层键保留）。

**2026-10-21 用户裁定**：`ANTHROPIC_AUTH_TOKEN` / `OPENAI_API_KEY` 值渲染为
**真实网关令牌**（原设计遮蔽为 `"__GATEWAY_TOKEN__"` 占位符，作废）——编辑器
默认即可看到、复制最终写入 Agent 的完整文档。保存路径不变：锁定键仍被归一化
剔除（例外：`ANTHROPIC_MODEL` 的 `<模型名>[1M]` 声明保留，§3.2），令牌永不
落库；令牌随 `RegenerateToken` 变化后由重预览/重开编辑器刷新。

## 4. 应用、持久文件与启动

### 4.1 写 live（`writeClaudeLive` 升级）

- env 子对象：按合成结果合并（同键替换，现有语义）；
- **文档其他顶层键逐键写入 live**（任意 JSON 值，同键替换）；
- live 中不在文档内的键（含非 env 顶层键）仍原样保留——只增改、不删除。
  换应用配置 B 不会清掉配置 A 写入的 `mcpServers`，直到被同名覆盖（cc-switch
  同款行为）——已知限制。
- 先读后写、解析失败绝不猜测修复、0600 原子写、写前备份、任一步失败即停：
  既有不变量全部保留。

Codex 写 live 逻辑不变（受管键仍为 model / model_provider / providers.agentswitch / auth）。

### 4.2 持久文件 / 启动命令 / 联动

- Claude 持久文件 `~/.agentswitch/access/claude/<id>.json` 从只含 env 升级为
  **整份合成文档**（`claude --settings` 接受完整 settings JSON）；
- Codex 持久文件不变；
- `LaunchCommand`/`OpenTerminal`/`RegenerateDerived`/`Apply`/`SetCurrent` 流程不变，
  只是合成输入变为文档。

## 5. UI 设计（`internal/desktop/access.go` 重做编辑器卡）

### 5.1 Claude 编辑器卡布局（自上而下）

1. **名称**（必填）+ **逻辑模型**下拉（选项 = 启用的逻辑模型，即主模型）+
   **1M 复选框**（Claude tab；勾选即 `ANTHROPIC_MODEL=<模型名>[1M]`，见 §3.2）。
   下方只读小字：`网关地址 http://127.0.0.1:<port> · 令牌自动生成并填入下方 JSON（保存时自动剔除，不落库）`。
   网关不可用时在卡内显示原因，但**不禁用保存**——保存是纯 DB 操作，与既有
   「编辑/删除不受门禁影响」原则一致（2026-10-18 spec 终审#3）；「应用/复制
   启动命令/打开终端」仍受 `accessGatewayGate` 门禁。
2. **模型映射表**（仅 Claude tab）：5 行角色，每行 = 角色名 + 显示名称输入（可选，
   Subagent 行无此列）+ 逻辑模型下拉（可选）+ 1M 复选框。
3. **配置 JSON**（`ui.TextArea` 多行编辑）：显示 `PreviewDoc` 合成结果（令牌渲染真值，见 §3.4 修订）。
4. 保存 / 取消按钮；错误内联中文显示（`form.msg`，沿用）。

Codex tab：不渲染模型映射表；JSON 编辑器同样可用但文档仅允许 `env` 键。

**全局配置卡维持现状**（env 键值行，不动）。

### 5.2 表单 ↔ JSON 双向同步（沿用 cc-switch 机制）

- 单一共享存储 = 编辑器 JSON 文本（`docJSON`）；
- 表单（主模型/角色行/1M）变化 → 解析当前文本 → 只补丁对应 env 键 → 重新
  序列化写回（2 空格缩进）；
- JSON 手改 → 解析成功时回填表单（解析失败静默保留上一合法状态，提交时才报错）；
- 保存 = 以 JSON 文本为准：根必须是对象、`env` 必须是对象（或缺省视为 `{}`），
  然后做**保存归一化**（在 bridge `Create`/`Update` 内做，UI 只送原始文本）：
  1. 锁定键恒剔除（网关自动生成，改了无效）——例外：`ANTHROPIC_MODEL` 为
     半锁定，`<绑定模型名>[1M]` 合法保留（§3.2）；
  2. 值与「无文档层合成结果」（网关基础键 + 当前全局 env）相同的键剔除——
     视为未定制、保持继承语义。否则编辑器展示的是合成结果，用户直接保存会把
     全局键与 `ANTHROPIC_SMALL_FAST_MODEL` 默认值全部冻结成该配置的覆盖项，
     破坏全局层后续变更的传播。
- 提示文案（编辑器下方）：网关地址/令牌自动锁定、模型名仅可追加 1M 声明；与
  全局/默认值相同的键保存时自动视为未定制（保持继承）；要覆盖全局值请改成
  不同值，要删除继承请去全局配置卡改。

### 5.3 保存校验（中文内联）

JSON 语法错误、根非对象、env 非对象、Codex 文档含 `env` 以外顶层键、名称为空/
同 app 重名（既有 UNIQUE 友好化）、逻辑模型不存在。`Create`/`Update` 签名中
`overrides` 参数改为 `settingsJSON string`。

## 6. 已知限制（记录在案）

1. 角色行存逻辑模型名字符串，改名不自动跟随（需重绑）；
2. 「应用」只增改 live 键、不删除：旧配置写入的残留键保留至被同名覆盖；
3. JSON 删除全局来源键 → 合成时全局层补回（UI 有提示）；
4. 文档顶层键编辑仅 Claude；Codex 仅 env（2026-10-27 起扩为 `env` / `models` / `codex`，见 `2026-10-27-codex-access-models-metadata-design.md` §4.2）。

> 2026-10-21 修订：原第 5 条「主模型不提供 1M 勾选」已撤销，改为半锁定 1M
> 声明（§3.2）。

## 7. 测试（全部离线）

- **storage**：v1 建库（含 `settings` 列、无 `overrides`），2026-10-27 起另加
  migration v2（`logical_models` 元数据列）；守卫拒绝 v3 记录库、旧表形态、
  缺元数据列的 v2 残库与无 `settings` 列的 `access_configs`；含元数据列的
  v2 库就地放行（见 `2026-10-27-codex-access-models-metadata-design.md` §4.1）。
- **bridge**：合成优先级与锁定键回写（含 SMALL_FAST 可覆盖）；保存归一化
  （锁定键剔除、与无文档层合成结果相同的键剔除）；主模型半锁定的 1M 声明
  （合成保留后缀/大小写归一、归一化保留合法声明、基名不匹配剔除）；
  `writeClaudeLive` 顶层键写入 + 非文档键保留；Codex 文档校验（`env` 以外
  顶层键拒绝）；`PreviewDoc` 渲染真实令牌；Claude 持久文件整份文档；
  `Create`/`Update` JSON 校验错误文案。
- **router**：`[1M]` 后缀剥离（大小写不敏感、无后缀不动作、双协议路径）；
  涉及并发/路由的带 `-race`。
- **desktop**：角色行↔JSON 补丁纯函数、主模型 1M 补丁与回填、保存剔除锁定键、
  预览渲染真实令牌、Codex tab 校验路径；沿用 `ui.Tester` 模式。
