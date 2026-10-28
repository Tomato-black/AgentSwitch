# Kun 接入设计（2026-10-28）

## 背景与意图

Kun（https://github.com/KunAgent/Kun，本地优先的桌面 AI 工作台，Electron + 内嵌 kun 运行时）自带有完整的模型接入体系：Provider 连接（预设/自定义 HTTP）、四种 wire 协议（OpenAI Chat Completions / OpenAI Responses / Anthropic Messages / custom_endpoint）、加密凭据存储与路由。用户希望像 Claude Code / Codex / Claude Desktop 一样，在 AgentSwitch 接入页集中管理 Kun 的模型接入——「应用」后 Kun 直接经 AgentSwitch 本地网关访问模型，无需在 Kun 设置里手工填写。

调研结论（2026-10-28，基于 Kun v0.4.3 源码与本机实测）：

1. **Kun 的 API Key 无法经普通文件写入**。凭据存在 Kun 自己的加密存储（`kun/src/security/secret-store.ts`，主密钥 `secret.key` 或系统钥匙串保护）；权威注册表 `~/.kun/data/model-connections.v2.json` 不含任何密钥字段；GUI 设置 `~/.config/Kun/kun-settings.json` 与运行时投影 `~/.kun/data/config.json` 中的 `apiKey` 字段是兼容投影，直接写入会被 GUI 内存态覆盖，且违反 Kun「注册表 v2 为权威、投影只读」的设计契约。
2. **Kun 提供受支持的本地 admin API**。运行时在 `127.0.0.1:<agents.kun.port>`（本机实测默认 18899）监听，认证用 `~/.config/Kun/kun-settings.json` 中 `agents.kun.runtimeToken`（Bearer）。连接管理路由：`GET /v1/model-connections`、`POST /v1/model-connections/connect`（**支持内联 credential**）、`PATCH /v1/model-connections/:providerId`、`PUT /v1/model-connections/:providerId/credential`、`POST /v1/model-connections/:providerId/probe`、`POST /v1/model-connections/select`。请求带 `expectedRevision` 做 CAS 乐观锁。
3. Kun 出站对 AgentSwitch 三入站全部兼容；本设计统一走 OpenAI Chat（`endpointFormat: "chat_completions"`），baseUrl 指向网关 `/v1`（Kun 的 URL 构建器对已含 `/v1` 的 base 直接拼 `chat/completions`）。模型名直接用逻辑模型名（Kun 无 Claude Desktop 的 `claude-*` 白名单限制，不需要别名表）；`[1M]` 后缀由网关入站剥离，Kun 侧无需处理。

## 用户决策记录（2026-10-28 对话中已确认）

1. **单受管连接**：接入页新增「Kun」页签，只维护一个受管连接——不做成 Codex 式多份接入配置。理由：Kun 的连接模型（单连接 + models 清单 + 默认选中）与「多份配置文件」语义不匹配，多份配置需引入多连接管理复杂度，首版不需要。
2. **模型清单自动同步**：「应用」时把 AgentSwitch 当前**全部启用的逻辑模型**同步进受管连接的 `models`，`selectedModel` 取第一项；逻辑模型增删后重新「应用」即同步。不在页签内维护勾选/排序编辑器。
3. **走 admin API 而非写文件**：见调研结论 1；这是对 2026-10-18 接入设计「应用 = 写 Agent 真实配置文件」模式的**明示偏离**（Claude Code / Codex / Claude Desktop 均为文件投影），偏离理由：Kun 凭据加密存储、外部进程不可写；admin API 是 Kun 受支持的程序化配置入口，原子性与安全性均优于文件投影。

## 目标

- 接入页新增第四种 Agent 类型「Kun」，一键「应用」在 Kun 中创建/更新受管连接，Kun 的模型选择器立即可用逻辑模型（无需重启 Kun、无需在 Kun 内手工配置）。
- 幂等：重复「应用」只更新受管连接，不产生重复连接、不触碰用户手建的其他连接。
- 令牌/端口变更后可一键重刷；未应用或已过期状态在页签内可见。
- 全离线测试（httptest 假 Kun admin API + 注入式路径），不依赖真实 Kun。

## 非目标（Non-goals）

- 不做多份接入配置、启动命令/「打开终端」派生（Kun GUI 不接受启动参数指定 Provider；`kun serve` TUI 场景不在首版）。
- 不做接入文档 JSON 编辑器（无 settings 文档；页签无自由编辑区）。
- 不做 Kun 内其他连接/路由/网关设置的管理；不读写 `auth.json` 类比物。
- Kun 未运行时不做「排队待应用」——明确报错提示先启动 Kun。
- 不支持 Gemini-native 入站协议的 Kun 连接（Kun 可发 Gemini 协议，但 AgentSwitch 网关无该入站；超出本设计范围）。

## 反目标（Anti-goals，做错即失败）

- 不直接改写 Kun 的任何配置文件（`kun-settings.json`、`model-connections.v2.json`、`config.json`）——一律经 admin API。
- 不删除/暂停/改名受管连接以外的任何 Kun 连接；不做全量 import。
- 网关令牌与 `runtimeToken` 不落日志、不落新存储、UI 只展示掩码（沿用现有安全规则）。
- 不依赖真实 Kun 或真实上游做测试。

## 机制设计

### 受管连接标识

- 固定连接 `id = "agentswitch"`，`name = "AgentSwitch"`。connect 带固定 id 天然幂等；「应用」前先 `GET /v1/model-connections` 判断存在与否，存在则 PATCH + 按需更新凭据。
- 只读写这一个 id；不做列表枚举式批处理。

### 定位 Kun admin API（accessPlatform 注入，便于测试）

按平台读 Kun GUI 设置文件 `kun-settings.json`（JSON），取 `agents.kun.port`（int）与 `agents.kun.runtimeToken`（string）：

| 平台 | 路径 |
| --- | --- |
| Linux | `~/.config/Kun/kun-settings.json` |
| macOS | `~/Library/Application Support/Kun/kun-settings.json` |
| Windows | `%APPDATA%\Kun\kun-settings.json` |

- 依据：Electron `app.getPath('userData')` 默认目录名为 `Kun`（本机 Linux 源码运行实测 settings 落 `~/.config/Kun/`；chromium 数据另在 `~/.config/kun-dv/` 是 dev flavor 差异，settings 目录名独立）。**官方打包版三平台路径在实现时各实测一次**，不符则修正常量表（集中在 `accessPlatform` 分支，不散落硬编码）。
- Base URL 为 `http://127.0.0.1:<port>`；`runtimeToken` 作为 `Authorization: Bearer` 头。文件缺失/字段缺失/JSON 解析失败 → 归类为「未检测到 Kun」或「Kun 管理接口不可用」错误（见错误处理）。

### 「应用」流程（internal/bridge，新文件 accesskun.go）

1. **网关自检与模型清单**：读网关状态（端口 + `auth.gateway_token`，与现有 composeAccessEnv 同源）；`GET` 自身 `/v1/models`（带令牌）确认有效并取逻辑模型清单；失败则报「网关令牌无效」类错误，不向 Kun 发任何请求。第一项作为 `selectedModel`。
2. **定位 Kun**：按上节读取端口与 runtimeToken；失败即报错返回。
3. **读 revision 与现状**：`GET http://127.0.0.1:<port>/v1/model-connections`（Bearer runtimeToken）→ 取 `revision` 与 `agentswitch` 连接是否存在。
4. **创建或更新**：
   - 不存在 → `POST /v1/model-connections/connect`：
     ```json
     {
       "expectedRevision": <revision>,
       "id": "agentswitch",
       "name": "AgentSwitch",
       "kind": "http",
       "authType": "api-key",
       "baseUrl": "http://127.0.0.1:<gatewayPort>/v1",
       "endpointFormat": "chat_completions",
       "credential": "<gateway_token>",
       "models": ["<逻辑模型1>", "..."],
       "selectedModel": "<逻辑模型1>",
       "probe": false,
       "select": true
     }
     ```
   - 已存在 → `PATCH /v1/model-connections/agentswitch`（`expectedRevision`、`baseUrl`、`name`、`models`、`selectedModel`）；若当前网关令牌与上次应用不同（见状态记录）或 Kun 侧凭据指纹不可知，则同时 `PUT /v1/model-connections/agentswitch/credential` 更新凭据。PUT 的请求体与是否两段式（fence/commit）以实现时对真实 Kun 的实测为准——本设计锁定语义：「更新受管连接的 API Key 为网关令牌」。
   - `probe: false`：不让 Kun 在 connect 时自行探测（探测失败会拒绝连接；连通性由 AgentSwitch 侧自检与用户在 Kun 内的实际使用体现）。
5. **记录状态**：apply 成功后把摘要写 settings 键 `access.kun.last_applied`（JSON：`baseUrl`、`models`、`selectedModel`、`appliedAt`、网关令牌指纹 SHA-256 前 8 字节），供页签显示「已同步 / 已过期」。**不存令牌本身**。
6. **CAS 冲突**：任一写请求因 revision 冲突失败 → 重读 revision 重试一次；再失败报「Kun 配置正在被其他客户端修改，请重试」。

### 状态语义（页签展示）

| 状态 | 判定 |
| --- | --- |
| 未安装 | `kun-settings.json` 不存在 |
| 未运行 | 设置文件在，但 `http://127.0.0.1:<port>/v1/model-connections` 连接失败/超时（短超时，~2s） |
| 未应用 | Kun 运行中，`agentswitch` 连接不存在 |
| 已同步 | 连接存在，且 last_applied 的 baseUrl/models/selectedModel 与当前网关配置+逻辑模型清单一致，令牌指纹一致 |
| 已过期 | 连接存在，但无 `access.kun.last_applied` 记录，或 baseUrl/models/selectedModel/令牌指纹任一项与当前不一致（逻辑模型增删、令牌轮换、端口变更） |

「已过期 / 未应用」时主按钮为「应用」；「已同步」时按钮为「重新应用」（仍可点，用于强制刷新）。

### 令牌/端口变更联动

沿用现有「令牌/端口变更自动刷新派生配置」的挂点：变更钩子里对 Kun 尝试重新 apply；Kun 未运行不算失败——状态自然落为「已过期」，用户下次点「应用」即可。（与 Claude Code/Codex 的差异：那两者无运行前提，可静默刷文件。）

## UI（internal/desktop）

新增 Kun 页签（与 Claude Code / Codex / Claude Desktop 并列），无编辑器，仅：

- 状态行：Kun 状态徽标 +（已同步时）模型清单摘要（`模型1（默认）、模型2、…`）+ 应用时间。
- 按钮：「应用 / 重新应用」、「重新检测」（刷新状态）。
- 错误就地展示（红字），不做弹窗。

页签注册进现有接入页 tab 布局；底条网关状态复用现状。

## 桥接 API（mygo.Bind 暴露）

- `AccessKunStatus() → {installed, running, applied, stale, models[], selectedModel, appliedAt, error}`——状态与摘要一次取齐。
- `AccessKunApply() → {ok, error}`——执行应用流程；错误信息中文、面向用户。

## 错误处理（面向用户的中文文案）

| 场景 | 文案方向 |
| --- | --- |
| 设置文件不存在 | 未检测到 Kun（未安装，或安装后从未启动过） |
| 端口/token 缺失、连接拒绝、超时 | Kun 管理接口不可用，请确认 Kun 正在运行 |
| runtimeToken 401 | Kun 管理接口认证失败，可能 Kun 已升级或设置文件被改，请重启 Kun 后重试 |
| 网关自查 401 | 网关令牌无效，请在设置页重新生成令牌后再应用 |
| 网关未运行 | 网关未启动，请先在底条启动网关 |
| CAS 冲突（重试后仍失败） | Kun 配置正在被其他客户端修改，请稍后重试 |
| connect/PATCH 其他 4xx/5xx | Kun 拒绝了配置写入：\<脱敏后的响应摘要\> |

## 安全

- `runtimeToken`、网关令牌仅存在于进程内存与 loopback HTTP 头；日志打印遵循现有脱敏规则（事件只记脱敏元数据）。
- admin API 仅监听 127.0.0.1（Kun 侧保证）；AgentSwitch 只向 127.0.0.1 发起。
- `kun-settings.json` 只读，不写。

## 测试（全部离线）

`internal/bridge/accesskun_test.go`（httptest 假 Kun admin API；`accessPlatform`/设置文件路径注入临时目录；网关侧用现有测试基建）：

1. 首次应用：无 `agentswitch` 连接 → 断言 connect 请求体（id/name/kind/authType/baseUrl/endpointFormat/credential/models/selectedModel/probe/select 全字段）。
2. 幂等更新：连接已存在 → 断言走 PATCH，且不调用 connect；models/selectedModel/ baseUrl 正确。
3. 令牌轮换：last_applied 指纹与当前令牌不一致 → 断言发出 credential 更新。
4. 模型清单变化：逻辑模型增删 → PATCH 的 models 与网关 `/v1/models` 一致，第一项为默认。
5. Kun 未安装 / 未运行 / token 401 / CAS 冲突重试 / 网关令牌 401：各错误文案与不发多余请求。
6. 状态推导：五种状态（未安装/未运行/未应用/已同步/已过期）矩阵。
7. Windows/macOS 路径分支：经注入的 accessPlatform 覆盖三平台拼接（不依赖宿主机 OS）。

`internal/desktop` 侧：页签注册与状态渲染的现有测试模式补一两条冒烟。

## 实现落点

| 位置 | 内容 |
| --- | --- |
| `internal/bridge/accesskun.go`（新） | 定位、admin client、apply 流程、状态推导；不进 `access_configs` 表（参照 accessdesktop.go 的独立入口模式，settings 键存 last_applied） |
| `internal/bridge/accesskun_test.go`（新） | 上述离线测试 |
| `internal/bridge/access.go` / 桥接注册 | 暴露 AccessKunStatus / AccessKunApply |
| `internal/desktop/accesskun.go`（新）+ `access.go` | Kun 页签 UI 与按钮动作 |
| CLAUDE.md | 落地后补一段「Kun 接入」记录（含对 2026-10-18 文件投影模式的偏离说明） |

## 与既有设计的关系

- 对 `2026-10-18-agent-access-config-design.md`「应用 = 受管键投影写入真实配置文件」的明示偏离（决策记录 3），docx/旧 spec 不改，以本文为凭证。
- Claude Desktop 接入（2026-10-22）确立的「简单形态 Agent 类型」（无多配置、独立 bridge 入口、不进 access_configs 表）是本设计的直接先例。
- 网关侧零改动：Kun 走既有 OpenAI Chat 入站与 `/v1/models`。
