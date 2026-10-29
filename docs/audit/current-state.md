# AgentSwitch 现状梳理（2026-10-29）

> 本文是「全面产品审查 + UI/UX 优化 + 渐进式重构」的第一阶段产物，由主 Agent 基于代码、文档与本地构建/测试基线整理。
> 审查期间**未修改任何业务代码**。视觉相关结论基于 Go/MyGo 源码阅读，未在 GUI 中逐屏实机走查（本机无截图流程），凡属推断均在文中标注。

## 1. 审查方法

- 通读 `README.md`、`ARCHITECTURE.md`、`CLAUDE.md`、`docs/superpowers/specs/` 下 13 份设计 spec。
- 通读桌面 UI 全部实现：组合根、`uikit`、`state`、四个页面及接入页四个子包、`accesskit`。
- 通读 bridge 对外方法、storage schema 与 CI 配置。
- 执行基线：`go build ./...`（通过）、`go test ./...`（通过，全部离线）。
- 独立审查：`pm` 子 Agent 输出 `product-review.md`，`ui` 子 Agent 输出 `ui-review.md`；本文与两份审查由主 Agent 交叉核对。

## 2. 产品定位与核心概念

一句话：**面向本地 AI Agent 的模型 API 网关**——Claude Code / Codex / Claude Desktop 只指向 `127.0.0.1:<port>`，上游供应商切换、多渠道故障转移、协议转换全部在网关内完成。

与 cc-switch 的差异：cc-switch 是「配置切换器」，任一时刻只有一个供应商生效；AgentSwitch 是「常驻网关」，Agent 配置一次、之后上游变更即时生效、失败可自动切目标。

核心领域概念（代码与 UI 一致）：

| 概念 | 代码载体 | 用户可见语义 |
|---|---|---|
| 渠道 Channel | `channels` + `channel_secrets` + `channel_models` | 一个上游 + 一个 API Key；模型清单仅作建议 |
| 逻辑模型 Logical Model | `logical_models` + `logical_model_targets` | Agent 请求的模型名（如 `my-code`），下挂有序「渠道·模型」目标链 |
| 目标链 Target | `logical_model_targets.priority` | 按序故障转移，仅在响应未提交前切换 |
| 接入配置 Access Config | `access_configs` | 每个 Agent 可存多份配置；「应用」把受管键写入 Agent 真实配置文件 |
| 冷却 Cooldown | `router/cooldown.go`（内存） | 上游 429/5xx 后按 ChannelID 暂时跳过 |
| 遥测 Telemetry | 内存环 + 轮转文件日志 | 最近请求供状态/诊断展示；不落 SQLite、不上报 |

## 3. 技术栈与运行形态

- Go 1.27.2；单二进制桌面应用，入口 `cmd/agentswitch`。
- UI：**MyGo v0.6.2 原生视图**（`github.com/egoist/mygo/ui`），无 HTML/TS 前端工程。`frontend/` 方案已于 2026-10-17 明确废弃。
- 存储：modernc.org/sqlite；密钥 AES-256-GCM，主密钥 `master.key` 与库同目录、0600。
- 生命周期：关窗 = 隐藏到托盘，网关继续运行；退出走 `OnBeforeQuit` 优雅收尾。
- 平台：macOS / Linux 已验证；Windows 构建与打包通过、待实机验证。

代码规模（非测试 / 测试）：

| 包 | 非测试行数 | 职责 |
|---|---:|---|
| `internal/bridge` | 3520 | 桌面桥接与应用服务门面（渠道/逻辑模型/接入/网关） |
| `internal/desktop` | 4899 | 桌面 UI（含 `uikit`/`state`/`accesskit`/`pages/*`） |
| `internal/protocol` | 2927 | Anthropic / OpenAI Chat / Responses 双向转换 |
| `internal/gateway` | 1384 | 三入站 HTTP、故障转移编排、错误映射 |
| `internal/storage` | 1104 | Repository、migration、密钥密文 |
| `internal/app` | 539 | 装配、生命周期、事件回调 |
| `internal/provider` | 540 | 上游调用、错误分类、探活 |
| `internal/telemetry` | 296 | 内存环、轮转日志、脱敏 |
| `internal/presets` | 177 | 渠道预置（embed + 用户覆盖） |
| `internal/router` | 172 | 候选解析、冷却 |
| 其余 | ~300 | config/credential/modelid/cmd |
| **测试** | **14803** | 56 个测试文件，全部离线 Fake Provider |

## 4. 包结构与依赖规则

```
cmd/agentswitch → desktop → bridge → app → storage / credential / config
gateway → protocol / router / provider / telemetry
```

- `internal/desktop` 已按 2026-10-28 方案拆包：
  - `desktop`：组合根（`shell.go` 导航、`desktop.go` 窗口/托盘、`closemodal.go` 首关模态）
  - `uikit`：主题 token、软色板、控件（Card/SoftBadge/PillButton/SoftButton/Tile）、图标
  - `state`：全局 UI 状态与各页表单（叶子包）
  - `accesskit`：接入文档 JSON 管道、角色助手、通用编辑器骨架
  - `pages/{logical,channels,settings,access}`；`pages/access/{claude,codex,claudedesktop,guide}`
- 设计不变量保持：router 不依赖 MyGo/UI；gateway 不自行实现供应商错误判断；desktop 只做生命周期与桥接。

## 5. 数据模型（SQLite，单一 v1 + v2 增列）

| 表 | 关键列 | 说明 |
|---|---|---|
| `channels` | id, name, base_url, protocol, preset_id, enabled, allow_private, credential_ref | 单 Key 执行单元 |
| `channel_secrets` | credential_ref, ciphertext BLOB | AES-256-GCM 密文 |
| `channel_models` | channel_id, model_id | 建议模型清单（非路由主体） |
| `logical_models` | id, name UNIQUE, description, enabled, display_name, context_window, reasoning_levels, default_reasoning_level | v2 纯增列，旧库就地升级 |
| `logical_model_targets` | logical_model_id, channel_id, model_name, priority | 有序目标链 |
| `access_configs` | id, app_type, name, logical_model_id, settings JSON | 接入配置；`settings` 为完整 JSON 文档 |
| `settings` | key, value | `auth.gateway_token`、`access.desktop_aliases` 等 |
| `schema_migrations` | version | 启动校验旧库形态，拒绝已知旧结构 |

## 6. 桌面 UI 架构要点

- **主题**：`uikit.BuildTheme(dark, textScale)` 覆写 19 个颜色 token；`Softs()` 提供 6 组软色；`SelectedColor`/`SidebarColor` 为应用级 token；`Radius=8`、卡片显式 12、`Spacing=4`、`FontSize=13×textScale`。
- **壳层**：自绘 200px 侧栏（品牌行 + 四项导航 + 底条网关状态）；内容列 `Padding(16)`；默认页 = 逻辑模型。
- **状态**：单一 `state.State`，包含 `Page`、各页表单/提示/二次确认 open 位；页面每帧直接读 bridge（进程内调用）。
- **反馈**：删除/切换/还原/停网均用 `ui.AlertDialog`；编辑器脏检查用内容签名 `Signature()`；提示多为 `st.*.Msg` 内联文本。
- **无动效**：LED 常亮，`ui.Element.Loop` 会重建整视图（实测约 20% 单核），刻意不用。
- **危险动作用软底红钮**（`SoftButton(..., "danger", ...)`），主操作使用橙色 `PillButton`。

## 7. 页面与功能清单

### 7.1 侧栏壳（`shell.go`）

| 元素 | 行为 | 状态 |
|---|---|---|
| 导航：逻辑模型 / 渠道 / 接入 / 设置 | 点击切换 `state.Page` | 无未保存提示（接入页内部有） |
| 底条：LED + 状态词 + 地址 + 启停 | 状态来自 `Gateway.Status()` 实际状态 | running/degraded 可停；stopped/failed 可启 |
| 停止网关确认 | `AlertDialog`，确认后异步 Stop | 有 |
| 关窗首关模态 | 说明网关继续运行，确认后隐藏到托盘 | 有；确认后不再提示 |

### 7.2 逻辑模型页（`pages/logical`）

- 列表：卡片行（名称、启用/停用徽章、「目标失效」徽章、编辑、删除、启用/停用）+ 目标链摘要；空态引导去渠道页。
- 编辑页：返回、基本信息（名称/描述）、目标链行（渠道下拉、模型下拉或输入、上移/下移/删除）、添加目标、保存。
- 删除：`AlertDialog`，列出引用该模型的接入配置。
- 缺省：**无搜索/筛选/排序**；无「复制模型」；无目标链测试入口。

### 7.3 渠道页（`pages/channels`）

- 列表：页头「渠道 + N 个渠道 + 添加渠道」；渠道卡（类型 tile、名称、启用/停用、有/无 Key、协议·BaseURL、建议模型数、近 N 条成败）。
- 新建：20 条预设网格 + 自定义；连接配置（名称/BaseURL/协议/单 Key/允许私网）；建议模型文本域 + 拉取模型。
- 详情：返回；连接配置（协议可编辑、Key 留空不改）；建议模型；保存；测试连通；删除（引用警告）。
- 缺省：**无搜索/筛选**；无批量启停；无渠道复制；模型建议为自由文本，非结构化列表。

### 7.4 接入页（`pages/access`）

四个页签：Claude Code / Codex / Claude Desktop / 连接指南。页签用 `SoftButton` 实现，不是标准 Tab。

| 区域 | 说明 |
|---|---|
| 全局配置卡 | env 键值行（Claude/Codex 有，Desktop 无）；保存与编辑行重定位 |
| 实际配置列表 | 名称、使用中徽章、模型摘要、应用/重新应用、编辑、「更多」（复制启动命令/打开终端/删除） |
| 编辑器 | Claude：逻辑模型下拉 + 1M + 5 角色映射 + settings.json 预览；Codex：模型列表 + 元数据 + 高级设置 + config.toml 预览；Desktop：4 角色映射 + profile 预览 |
| 连接指南 | 网关地址、三协议端点、Bearer 说明、令牌掩码+复制、启用模型清单 |

交互细节：页签切换有未保存确认；删除/还原备份有确认；网关未运行时应用类按钮隐藏（编辑/删除仍可用）。

- 缺省：**无配置搜索/排序**；编辑器很长（320px JSON + 220–240px 预览），信息密度高；Codex 编辑器的元数据区按模型逐行展示，模型多时很长；「更多」收纳次级操作，但列表行本身仍较多按钮。

### 7.5 设置页（`pages/settings`）

- 网关卡：端口、请求超时、透传未登记模型（预留只读）、保存。
- 网关诊断卡：状态/原因、地址、日志路径+复制、最近 20 条请求（2026-10-29 复核修正；P0-1 起另持久化最近 100 条供重启后查看）。
- 关于卡：版本。

- 缺省：无日志打开、无诊断包导出（spec 曾提及但未实现）；最近请求只在设置页出现，且仅 8 条。

## 8. 核心用户流程

### 8.1 首次上手

1. 启动应用 → 网关自动启动（底条可见）。
2. 渠道页 → 添加渠道 → 选预置 → 填 Key → 创建 → 进详情 → 保存建议模型。
3. 逻辑模型页 → 新建 → 填名称 → 添加目标（选渠道/模型）→ 保存。
4. 接入页 → 选 Agent → 新建配置 → 选逻辑模型（Codex 为模型列表）→ 保存 → 应用。
5. 重启 Agent 或新建对话 → 发起请求。

**成本**：至少 4 个页面、约 12–18 次交互；渠道与逻辑模型之间的依赖顺序在空态文案中有引导，但**没有端到端向导**。

### 8.2 日常运维

- 看状态：侧栏底条（任意页可见）。
- 换上游/改 Key：渠道页 → 详情 → 改 Key/BaseURL → 保存（即时生效，无需重启 Agent）。
- 调优先级：逻辑模型 → 编辑 → 上移/下移 → 保存。
- 上游故障：自动切换（响应未提交前）；用户可看设置页最近请求，但**无专门的路由决策/切换原因视图**（只有 `FailoverCount` 字段）。
- 停用某模型：逻辑模型列表 → 停用。

### 8.3 多 Agent / 多配置

- 每个 Agent 维护多份配置；「应用」切换使用中的配置；非默认配置可复制启动命令/打开终端。
- 全局配置合并进所有实际配置，实际配置覆盖项优先。
- 备份与还原：每 Agent 最近 10 份，可一键还原最近备份。

## 9. 接入能力矩阵

| Agent | 写入方式 | 文件 | 入站 | 特殊约束 |
|---|---|---|---|---|
| Claude Code | 文件投影 | `~/.claude/settings.json` | `/v1/messages` | 受管 env 键、锁定网关三键、1M 半锁定 |
| Codex | 文件投影 | `~/.codex/config.toml` + `agentswitch-model-catalog.json` | `/v1/responses` | `wire_api=responses`、`experimental_bearer_token`、不改 `auth.json`、需完全退出重启 |
| Claude Desktop | 文件投影 | `claude_desktop_config.json` | `/v1/messages` | 仅 claude-* 角色路由，别名表 |
| 第三方（Kun 等） | 人工填写 | 无 | 三入站均可 | 连接指南只展示信息，不写配置 |

## 10. 测试与 CI

- 56 个测试文件、14803 行测试；全部离线（httptest Fake Provider）。
- 覆盖重点：协议转换、流式中断/空闲超时、故障转移不变量、冷却并发（`-race`）、接入文档归一化、Codex 目录生成、桌面页渲染与交互（`uitest` 夹具）。
- CI：Ubuntu/macOS `build + vet + test -race`；Windows 打包 job；golangci-lint 因 Go 1.27 暂不阻塞。
- 基线（本次）：`go build ./...` 通过；`go test ./...` 通过。

## 11. 文档体系与已知漂移

- 权威文档：`docs/superpowers/specs/` 按日期命名的 13 份 spec；旧 docx 已移出仓库。
- **漂移 1（重要）**：`2026-10-28-kun-access-design.md` 描述「接入页新增 Kun 页签 + 通过 Kun admin API 自动写入连接」，但代码中**没有 Kun 相关 bridge/页面**；实际实现是 `guide` 页签（通用连接信息，人工填写）。该 spec 应标记为「未实现/已被 guide 方案取代」，需用户确认。
- **漂移 2**：`2026-10-17-desktop-ui-ccswitch-redesign-design.md` 的 §5 仍描述「概览页 / 模型页 / 密钥池 / 渠道详情分段」，这些页面在 2026-10-17 逻辑模型重构后已被「逻辑模型 / 渠道 / 接入 / 设置」取代；视觉 token 仍有效，IA 部分已过期。
- **漂移 3**：CLAUDE.md 记录了 2026-10-28 Codex 元数据迁入接入页，但同一段旧文仍写「逻辑模型元数据（migration v2）」，读者需读到末尾才知道编辑入口已迁移；`state.AccessForm` 注释里仍写「Codex 元数据…数据仍存在逻辑模型上」，与实际 per-config `model_meta` 覆盖不完全一致（代码注释与实现漂移，非功能缺陷）。
- 工作区 `CLAUDE.md` 有一处用户未提交的本地改动（新增「自动化产品设计与开发工作流」章节），本次审查**未修改**。

## 12. 已知限制与风险

| 类别 | 现状 | 影响 |
|---|---|---|
| 视觉验证 | 无 GUI 截图/自动化视觉回归；UI 测试只断言元素存在与交互 | 布局问题只能靠代码推断，需实机确认 |
| 长页面 | 接入编辑器、Codex 元数据区随模型数线性增长 | 1080×720 下需大量滚动 |
| 错误呈现 | 多数错误写入同一 `Msg` 文本，颜色多为 `Danger` 或 `TextMuted` | 成功/失败视觉区分不稳定 |
| 空态 | 渠道/逻辑模型/接入/设置各有空态文案，但无统一组件 | 风格与位置不一致 |
| 导航状态 | 侧栏切页不保留滚动位置（每帧重建） | 长页面返回后可能回到顶部 |
| 键盘/快捷键 | 仅框架默认焦点行为；无全局快捷键 | 桌面效率工具属性偏弱 |
| 诊断能力 | 设置页最近 20 条 + 尝试链；P0-1 起持久化最近 100 条（2026-10-29 复核修正） | 更长历史仍需导出诊断 JSON；日志文件仍是深度排障入口 |
| 文档规模 | 13 份 spec + CLAUDE.md 长文 | 新维护者认知负担高，且存在上述漂移 |

## 13. 本阶段结论

1. 系统架构清晰、分层与不变量保持良好，测试基线健康，**不存在需要推倒重来的架构问题**。
2. 主要改进空间集中在**桌面 UI 的信息架构、长页面组织、反馈一致性与诊断可见性**，以及**接入配置这一最复杂区域的可理解性**。
3. 文档漂移（Kun spec、旧视觉 spec 的 IA 部分）会误导后续开发，应在重构计划中一并修订，但不属于业务代码变更。
4. 下一阶段应由 PM 明确核心页面目标与验收标准，UI 给出统一设计方向；在用户确认总体方案前不进入代码改造。
