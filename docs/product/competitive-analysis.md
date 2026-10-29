# AgentSwitch 竞品分析（2026-10-29）

> 访问日期：2026-10-29。本文只使用官方仓库、官方文档、Release Notes、Issues 与可核实的仓库页面；营销页与第三方文章仅作辅助，并单独标注。**本文不引用具体 Star/Issue 数字**：PM 子 Agent 记录了页面快照，但主 Agent 复核时 GitHub API 限流，无法独立验证；为避免虚假精确，只讨论官方文档可支持的功能事实。
>
> 证据标签：
> - **【事实·官方】**：官方 README / 文档 / Release Notes / 仓库页面原文可支持。
> - **【事实·Issue】**：公开 Issue 原文可支持；Issue 是用户报告，不等于官方确认的缺陷。
> - **【推断】**：由文档、代码或产品形态推断，未经实测。
> - **【未能核实】**：本次访问无法确认，不补全细节。
>
> **主 Agent 复核说明（2026-10-29）**：本文由 `pm` 子 Agent 研究，主 Agent 对关键竞品事实做了抽查复核：CC Switch 的 12 工具/90+ 预置/热切换/Routing/Aggregation/Codex daemon 重启、9Router 的默认弱安全配置、LiteLLM 的虚拟 Key/预算、new-api 的多协议/配额均与官方 README 一致。**对 AgentSwitch 现状有一处重要修正**：审查发现对已应用配置点「仅保存」会直接改写 Agent live 文件且不备份（`internal/bridge/access_configs.go:101-128,258-300`；`internal/bridge/access_live.go:173-260`），因此本文中「可靠性/写前备份」的表述必须结合 `feature-gap-analysis.md` §A0 与 `decisions.md` PD-2 阅读——这是当前必须先修的 P0 缺陷，不是竞品差距。

---

## 0. 结论摘要

1. **品类正在收敛，AgentSwitch 的“配置切换器”替代叙事已经过期。** CC Switch 已从配置切换器扩展为带本地路由、故障转移、聚合、用量统计的桌面控制面；Claude Code Router 和 9Router 本身就是本地网关。AgentSwitch 不能再以“我不是 cc-switch”作为主要卖点，必须把“逻辑模型级故障转移 + 可解释性 + 本地隐私”做成可感知的产品能力。
2. **AgentSwitch 真正稀缺的不是“多渠道”，而是“一个逻辑模型 → 跨渠道·跨模型名的有序目标链”。** CC Switch 的 Routing 是供应商队列级故障转移，Aggregation 则是“按模型分流但不提供故障转移”；AgentSwitch 把两者合并在逻辑模型层，这是目前最清晰的功能差异。但用户目前很难看见它发生了什么。
3. **最大的产品缺口是运维闭环，而不是供应商数量。** AgentSwitch 能自动切换，却没有持久化请求/路由历史、用量/成本/配额、告警、备份历史与导入导出；竞品已把这些当作基础能力。结果是“网关在替我做什么”无法被验证，故障转移的信任价值被浪费。
4. **不要追 CC Switch 的 12 工具广度，也不要追 LiteLLM 的企业网关闭环。** 前者会把 AgentSwitch 拖进配置管理与 MCP/Skills/会话阅读的红海；后者会把单机桌面产品拖进多租户、虚拟 Key、预算、SSO 的服务端复杂度。
5. **建议的定位是“本地 AI Agent 的可靠性层”**：单二进制、零云依赖、中文供应商/Coding Plan 友好、按逻辑模型做可解释故障转移；用请求历史、端到端验证、用量与诊断把“可靠”变成可验证的承诺。
6. **定位成立的前提是先修一个现有缺陷**：对已应用配置点「仅保存」会改 live 且不备份，直接破坏“写前备份/可回滚”的信任。竞品对比之前，必须先让保存/应用/备份的语义与真实写入一致（见 `feature-gap-analysis.md` A0、`decisions.md` PD-2）。

---

## 1. 研究方法与证据边界

### 1.1 研究问题

- AgentSwitch 的真实品类是什么？它与“配置切换器”“本地代理”“服务端 AI Gateway”分别是什么关系？
- 直接竞品已经提供了哪些能力？哪些是品类基础能力，哪些是 AgentSwitch 定位必需，哪些只是“竞品有所以想要”？
- 竞品的公开痛点集中在哪些环节？AgentSwitch 是否已经解决、还是同样会踩坑？
- 哪些能力应进入 P0/P1/P2，哪些应明确不做？

### 1.2 样本选择

| 竞品 | 选择理由 | 与 AgentSwitch 的关系 |
|---|---|---|
| CC Switch（farion1231/cc-switch） | 中文社区最知名的 Agent 配置管理器，且 4.x 已加入本地路由/聚合/用量 | 最直接的用户心智与桌面体验竞品 |
| Claude Code Router（musistudio/claude-code-router） | 本地模型网关 + 控制面，支持 Claude Code/Codex 与多上游路由 | 功能形态最接近的本地网关竞品 |
| 9Router（decolua/9router） | 本地 AI Router，强调自动降级、配额窗口、Token 节省 | 价值主张最接近“永不中断”的本地竞品 |
| LiteLLM（BerriAI/litellm） | 通用 LLM Gateway，100+ 供应商、预算、虚拟 Key、可观测性 | 能力上限参照；不是桌面直接竞品 |
| new-api / one-api | 自托管 AI 网关/聚合与额度管理，中文生态常见 | 渠道聚合、额度、模型映射的参照 |

未选入正式分析但相邻的产品：Portkey、Cloudflare AI Gateway、Kong AI Gateway、Bifrost 等。它们主要面向服务端/企业网关，与 AgentSwitch 的单机桌面用户差异较大，本次不展开；如果 AgentSwitch 未来考虑团队/服务端形态，应另行评估。

### 1.3 证据边界

- 本次**没有**进行竞品实机安装、GUI 逐屏走查、真实上游压测或用户访谈。
- 竞品功能以官方 README/文档为准；README 声称但手册未展开的能力，标注为“官方 README 声称，细节未核实”。
- Issue 只说明“有用户报告”，不自动等同于普遍缺陷；本文用它识别**反复出现的痛点模式**。
- Star/Issue 数是 2026-10-29 页面快照，可能快速变化。
- AgentSwitch 现状以 `capability-matrix.md` 与本次对关键代码的抽查为准；凡标注“已实现·未实机”的，本文不升级为“已验证”。

---

## 2. 产品定位判断

### 2.1 AgentSwitch 不是“配置切换器”

AgentSwitch 的核心行为是：Agent 固定指向 `127.0.0.1:<port>`，请求进入网关后按**逻辑模型**解析出有序的「渠道·模型」目标链，在响应提交前做故障转移，并在三种入站协议与两种上游协议之间转换。配置投影只是它的“接入层”，不是产品本体。

因此，竞品分析的主轴应是：

1. **路由与故障转移语义**（是否按模型、是否可解释、是否可控制）；
2. **协议转换正确性**（Claude Code/Codex 真实请求能否稳定通过）；
3. **运维闭环**（请求历史、用量/配额、告警、诊断、恢复）；
4. **接入与配置管理**（多 Agent、多配置、备份、迁移）；
5. **本地隐私与部署形态**（单机、零云、单二进制）。

### 2.2 定位卡

| 项 | 结论 |
|---|---|
| 一句话定位 | 面向本地 AI Agent 的可靠性层：Agent 固定指向本地入口，按逻辑模型在多个供应商/模型之间做可解释的故障转移；单二进制、零云依赖、密钥本地加密。 |
| 目标用户 | **主目标**：中文个人开发者，使用 Claude Code / Codex / Claude Desktop，有 2–5 个渠道，关注稳定、隐私与配置简单。**高价值**：多供应商/多订阅重度用户，额度敏感、愿意折腾。**非目标**：小团队/共享网关、需要多租户/审计/远程访问的用户。 |
| 核心场景 | 首次接入；上游故障/限流时自动切换；换 Key/换渠道/调目标顺序；多 Agent/多配置；第三方 Agent 手工接入；排障与恢复。 |
| 核心痛点 | 上游不稳定/额度有限；配置切换后 Agent 失效；故障时不知道为什么、切到哪；改坏配置无法回滚；不知道消耗/剩余；配置无法迁移。 |
| 核心价值主张 | “配置一次、上游随时换、失败可解释、数据不出本机。” |
| 差异化方向 | 逻辑模型目标链（跨渠道·跨模型名）；可解释故障转移；三入站协议转换；Codex 深度接入；本地隐私/单二进制；中文 Coding Plan 预置。 |
| 非目标 | 不做 12+ Agent 全覆盖、MCP/Skills/Prompts、会话阅读、Token 压缩、云同步、多租户/计费、Guardrails、插件系统、远程访问。 |

### 2.3 品类地图

| 形态 | 代表产品 | 核心价值 | 与 AgentSwitch 的重叠 |
|---|---|---|---|
| 配置切换器 | CC Switch 的 Direct 模式、早期 cc-switch | 改配置文件，切换供应商 | 接入层重叠；不承载流量 |
| 本地路由/控制面 | CC Switch Routing/Aggregation、Claude Code Router、9Router、AgentSwitch | 本地代理、协议转换、故障转移、用量 | 高度重叠，是主战场 |
| 服务端 AI Gateway | LiteLLM、new-api、one-api、Portkey | 多租户、预算、审计、可观测性 | 能力参照；部署与用户不同 |
| Agent 原生多供应商 | Claude Code/Codex 自带登录与模型选择 | 官方体验 | 不构成直接替代，但影响用户是否愿意装网关 |

**判断**：AgentSwitch 处在“本地路由/控制面”格子里；在本次样本中，它是唯一以**逻辑模型目标链**为核心抽象的产品（这是基于公开文档的判断，未逐一审查竞品内部实现）。这个格子已经很拥挤，但“按逻辑模型做跨供应商、跨模型名的可解释故障转移”仍有空位。

---

## 3. 竞品逐个分析

> 样本选择说明：本轮对 CC Switch、Claude Code Router（CCR）、9Router、LiteLLM、new-api/one-api 做了官方 README/文档/Release/Issue 研究。**未引用具体 Star/Issue 数**（主 Agent 复核时 GitHub API 限流，无法独立验证）；竞品选择基于定位与功能形态，而不是热度。Issue 只用于识别反复出现的痛点模式，不代表官方确认的缺陷。

### 3.1 CC Switch

**定位与目标用户**

- 【事实·官方】官方仓库描述为“cross-platform desktop All-in-One assistant for Claude Code, Codex, OpenCode, OpenClaw, Grok Build & Hermes Agent”，README 称“One App, Twelve Tools”，目标是不再手写 JSON/TOML/YAML 配置。
- 【事实·官方】支持 12 个 Agent 工具、90+ 供应商预置、Direct/Routing/Aggregation 三种连接模式、MCP/Skills/Prompts 集中管理、会话阅读、用量与配额、项目切换、WebDAV/S3 云同步、自动备份、自动更新、i18n。
- 【事实·官方】Tauri 2 + Rust + React 18 + TypeScript + SQLite；仅桌面 GUI，无官方无头版本。

**核心场景**

1. 在多个供应商/官方账号之间一键切换；
2. 用本地 Routing 做格式转换与故障转移；
3. 用 Aggregation 把多供应商模型放进同一个 `/model` 菜单；
4. 集中管理 MCP、Skills、Prompts、会话与用量；
5. 把整套配置保存为项目，随项目切换；
6. 通过 WebDAV/S3 在多机之间同步。

**模型/供应商/端点管理**

- 【事实·官方】90+ 预置，覆盖 AWS Bedrock、NVIDIA NIM、社区中转；内置 Claude/OpenAI/Google/Grok 官方；支持 Universal Provider 把一套配置同步到 Claude Code/Codex/Gemini CLI。
- 【事实·官方】支持 Anthropic Messages、OpenAI Chat Completions、OpenAI Responses、Gemini Native 四种上游格式；Routing 模式可做格式转换。
- 【事实·官方】Accounts（Beta）支持 GitHub Copilot、ChatGPT、xAI、Kimi Code 订阅作为供应商；README 同时提示“在官方客户端之外使用订阅可能违反供应商条款”。

**对 Claude Code / Codex 的支持**

- 【事实·官方】两者都是一等公民：供应商切换、本地 Routing、Aggregation、托盘切换、MCP/Skills、会话、用量统计均支持。
- 【事实·官方】Claude Code 支持热切换，无需重启；Codex 普通切换需重启终端，Aggregation 模型列表变化需重启 Codex，且所有 `codex` 终端共享一个后台 daemon，CC Switch 提供“Restart Codex daemon”。
- 【事实·官方】Claude Code Aggregation 要求 2.1.243+；Claude 官方订阅不支持 Aggregation。
- 【事实·官方】Routing 默认 `http://127.0.0.1:15721`，配置里用占位 Key `PROXY_MANAGED`，真实 Key 留在 CC Switch。

**配置切换/备份/恢复/隔离**

- 【事实·官方】切换只替换端点、Key、模型、协议等连接信息，保留 plugins、hooks、MCP、用户自定义设置与注释；编辑器可预览切换后的配置。
- 【事实·官方】首次改写前备份到 `~/.cc-switch/backups/live-first-write/`；日常每 24 小时备份，保留最近 10 份；Skills 备份保留 20 份。
- 【事实·官方】支持导入/导出、拖拽排序、首次启动导入现有配置、MCP 导入、`ccswitch://` Deep Link；项目可保存 Claude Code 的供应商/MCP/Skills/Prompt 文件。
- 【事实·官方】支持 WebDAV、S3 兼容存储（AWS S3、Cloudflare R2、阿里云 OSS、腾讯云 COS）云同步；WSL 配置管理。

**扩展性与设计特点**

- 极强的“生态广度”：预置、Deep Link、Universal Provider、项目、云同步、i18n、自动更新。
- 把“配置管理”做成了完整的数据产品：SQLite、备份、云同步、跨工具统一模型。

**已知限制与用户痛点**

- 【事实·官方】官方供应商通常不能走 Routing（Codex 的 OpenAI Official 例外）；Claude 官方订阅不支持 Aggregation。
- 【事实·官方】Aggregation 不提供故障转移；故障转移只属于 Routing。
- 【事实·官方】桌面 GUI only；社区 CLI 可能落后于桌面数据库版本。
- 【事实·官方】连通性检查只测可达性，不验证 Key 或模型有效性。
- 【事实·官方】Claude Desktop 仅 Model Mapping，无 MCP/Skills/Prompts/Sessions，且 Model Mapping 需要 CC Switch 持续运行。
- 【事实·Issue】Routing/Aggregation 的兼容性与稳定性是高频痛点：跨供应商 header 注入导致 400（#9018）、Claude Desktop 经 Codex 路由 502（#8888）、`base_url` 重复拼接与格式限制（#8989）、连接重置导致熔断器长时间打开并全局 503、突发流量 EMFILE（#8668）、Aggregation 破坏 DeepSeek 历史会话（#8516）、跨供应商路由组自动故障转移仍属需求（#8665）、故障转移队列缺少会话粘性（#8645）。
- 【事实·Issue】WSL/平台问题（#721、#4043）、后台进程无 GUI（#1927）、Codex 官方额度耗尽后其他聚合 API 无法在新会话使用（#7962）。

**与 AgentSwitch 的差异与可借鉴处**

| 维度 | CC Switch | AgentSwitch | 启示 |
|---|---|---|---|
| 路由抽象 | 供应商队列/按模型聚合 | 逻辑模型 → 有序「渠道·模型」目标链 | AgentSwitch 的模型级故障转移更细；必须让用户看见 |
| 故障转移 | Routing 有；Aggregation 无 | 有，且与逻辑模型绑定 | “聚合 + 故障转移”是 AgentSwitch 的差异点 |
| 协议 | 4 种上游格式 + 多 Agent | 3 入站 / 2 上游协议 | 不追 Gemini/Cursor 广度，先把 Claude/Codex 做到最稳 |
| 用量/配额 | 用量统计、配额/余额、自定义价格、请求日志 | 仅 20 条内存请求 + 尝试链；无用量/成本 | 这是 AgentSwitch 最该补的短板 |
| 备份/迁移 | 首写备份、每日备份、导入导出、Deep Link、云同步 | 每 Agent 最近 10 份，仅还原最近一份；无导入导出 | 先补导出/导入与备份列表，不做云同步 |
| 生态 | 12 工具、90+ 预置、MCP/Skills/Sessions | 3 类 Agent、20 预置、聚焦网关 | 不追广度；做深中文 Coding Plan 与 Codex 目录 |
| 部署 | Tauri 桌面，依赖 WebKitGTK | Go 单二进制 + MyGo 原生 UI | 单二进制/低依赖是优势，但 UI 成熟度是风险 |

---

### 3.2 Claude Code Router（CCR）

**定位与目标用户**

- 【事实·官方】README 称其为“a local model gateway and control plane for coding agents”，给多个 Agent 客户端“one stable local endpoint”，集中管理 providers、models、accounts、routing rules、tools。
- 【事实·官方】支持 Claude Code、Claude Design、Codex、Grok CLI、Kimi CLI、Kilo Code、OpenCode、Pi、ZCode、WorkBuddy 等。
- 【事实·官方】提供桌面 App、npm CLI（Node.js 22+）、Docker；网关默认 `127.0.0.1:3456`，管理 UI `3458`。

**核心场景**

1. 多 Agent 共用一个本地网关；
2. 多 provider/account 的凭证池与轮换；
3. 按 header/body 条件、前缀、重写做路由；
4. 请求重试、有序 fallback；
5. 查看请求/响应详情、解析后的 provider/model/credential、状态、延迟、token、估算成本、工具调用、trace；
6. 给客户端分配独立 Key、过期时间、请求/token/图片限制；
7. 通过 Fusion/ToolHub/插件扩展能力。

**模型/供应商/端点管理**

- 【事实·官方】预置 + 自定义端点、协议探测、模型发现、连通性检查、本地登录导入、单 Key、凭证池。
- 【事实·官方】支持 OpenAI Chat/Responses、Anthropic Messages、Gemini Generate Content/Interactions、OpenRouter、DeepSeek、SiliconFlow、Moonshot、Kimi Code、Mistral、Z.AI、Bailian 等。

**对 Claude Code / Codex 的支持**

- 【事实·官方】两者均列出，含 CLI & APP 变体；profile 支持模型覆盖、scope、环境变量、启动入口、多实例工作流。
- 【事实·Release】v3.1.3 修复 Codex session affinity、provider alias protocol handling、上游 stream reset；v3.1.2 修复 Codex workspace startup、approval reviewer preservation、Anthropic tool IDs、replay histories、OAuth credential refresh。说明 Codex/Claude 真实兼容性仍是持续投入点。

**配置切换/备份/恢复/隔离**

- 【事实·官方】profiles、scopes、环境变量、启动入口、多实例、客户端 Key、额度限制。
- 【事实·Release】v3.1.2 保护配置保存免受并发陈旧编辑，保留 provider capabilities、routing rules、fallback 设置；说明配置一致性是已知风险。
- 【未能核实】是否有面向用户的备份/恢复/导出机制；README 未展开。

**扩展性与设计特点**

- 功能面最宽的本地控制面之一：条件路由、重写、插件、ToolHub、Fusion、虚拟模型、本地路由、客户端配额、可观测性。
- 代价是概念多、配置复杂；桌面/CLI/Docker 三形态增加维护面。

**已知限制与用户痛点**

- 【事实·Issue】高频错误报告：`A.map is not a function`（#23）、`Missing model in request body`（#542、#150）、500（#527）、连接错误（#19）。
- 【事实·Issue】transformer 自定义文档含糊、缺少示例（#614）；VS Code Claude Code 扩展如何使用 `ccr code` 不清楚（#852）；Kimi/Gemini proxy/Fireworks 等 provider 集成反复出现（#245、#160、#377）。
- 【事实·官方】README 没有专门的 limitations 章节；只能从 Issue 与 Release 看出兼容性/稳定性成本。
- 【推断】功能密度高，新用户需要理解 provider、profile、scope、routing condition、credential pool、virtual model 等概念，学习曲线明显高于 AgentSwitch。

**与 AgentSwitch 的差异与可借鉴处**

| 维度 | CCR | AgentSwitch | 启示 |
|---|---|---|---|
| 抽象 | provider + profile + routing rules | 渠道 + 逻辑模型目标链 + 接入配置 | AgentSwitch 更简单；不要引入条件 DSL |
| 多账号 | 凭证池、轮换 | 单 Key/渠道 | 多账号是真实需求，但可先用“渠道复制 + 主备链”满足 |
| 可观测性 | 请求/响应详情、trace、token、成本、延迟 | 20 条请求 + 尝试链 | 追平“请求详情 + 用量”，不必追平 trace 平台 |
| 客户端隔离 | 独立 Key、过期、限额 | 单一网关令牌 | 单机个人用户不需要；团队形态再考虑 |
| 扩展 | 插件、ToolHub、Fusion、虚拟模型 | 预置 JSON、固定协议 | 不建插件系统；保持产品边界 |
| 部署 | Node/桌面/CLI/Docker | Go 单二进制/桌面 | AgentSwitch 部署更轻，但生态与功能面更窄 |

---

### 3.3 9Router

**定位与目标用户**

- 【事实·官方】“FREE AI Router & Token Saver”，本地 OpenAI 兼容代理，默认 `http://localhost:20128/v1`，Dashboard 在 `/dashboard`。
- 【事实·官方】目标用户：使用 AI 编程 CLI/IDE、想把手上的订阅额度用满、想通过自动 fallback 避免限流、想用免费/低价层、管理多账号的用户。
- 【事实·官方】Node.js 20+、Next.js 16、React 19、Tailwind 4、SQLite；npm/Docker/PM2 部署。
- 【事实·仓库】仓库页面显示活跃提交（PM 记录 master 最新提交 2026-10-28 / v0.5.112，主 Agent 未独立复核）；Releases 页面本次未见发布条目。

**核心场景**

1. “Subscription → Cheap → Free” 三层 fallback；
2. 配额窗口跟踪（5 小时/日/周/月）与消费估算；
3. 多账号 round-robin/优先级；
4. Token 压缩（RTK、Caveman、Ponytail）；
5. 多 Agent/CLI 工具统一接入；
6. 请求日志与 usage analytics。

**模型/供应商/端点管理**

- 【事实·官方】40+ providers、100+ models；OAuth providers（Claude Code、Antigravity、Codex、GitHub、Cursor、Kimchi）、free providers（Kiro、OpenCode Free、Vertex）、API-key providers、自托管 STT/TTS/embeddings。
- 【事实·官方】协议翻译覆盖 OpenAI、Claude、Gemini、Cursor、Kiro、Vertex、Antigravity、Ollama、OpenAI Responses；暴露 `/v1/chat/completions` 与 `/v1/models`。

**对 Claude Code / Codex 的支持**

- 【事实·官方】支持 Claude Code、Codex、OpenCode、Cursor、Antigravity、Cline、Continue、Droid、Roo、Copilot、Kilo Code、OpenDesign、jcode、Grok Build、Devin CLI、DeepSeek TUI、Qwen Code 等。
- 【事实·官方】Dashboard 管理 CLI 工具配置；但 README 没有逐工具的配置写入细节。

**配置切换/备份/恢复/隔离**

- 【事实·官方】SQLite 存 providers、combos、aliases、keys、settings、usage history；自动备份到 `${DATA_DIR}/db/backups/`；支持 cloud sync。
- 【事实·官方】多账号/多 provider 的 combos；请求日志/翻译日志可选。
- 【未能核实】备份恢复 UI、导入导出格式、配置隔离的细节。

**扩展性与设计特点**

- 极强的“成本/额度优化”导向：fallback 分层、配额窗口、Token 压缩、Caveman/Ponytail 提示词。
- 生态覆盖广、迭代快；但功能带有明显的“免费层套利”色彩。

**已知限制与用户痛点**

- 【事实·官方】免费层有上限且易变：Kiro 约 50 credits/月，OpenCode 免费模型列表会变；iFlow、Qwen Code 免费 OAuth、Gemini CLI 免费层已停止。
- 【事实·官方】README 对 Kiro 的“Unlimited”与“~50 credits/month”自相矛盾。
- 【事实·官方】成本数字只是估算/比较，不是账单；9Router 不收费。
- 【事实·官方】默认 Dashboard 密码 `123456`、`REQUIRE_API_KEY` 默认 false；对外暴露时必须加固。
- 【事实·官方】OpenClaw 仅本地可用；建议用 `127.0.0.1` 避免 IPv6 解析问题。
- 【推断】Token 压缩/提示词注入会改变模型行为，存在输出质量与可解释性风险；不适合作为“透明网关”的默认能力。

**与 AgentSwitch 的差异与可借鉴处**

| 维度 | 9Router | AgentSwitch | 启示 |
|---|---|---|---|
| 价值主张 | 免费/低价 + 永不限流 + Token 节省 | 可靠性 + 本地隐私 + 中文供应商 | 不追免费层套利；可借鉴“配额窗口”表达 |
| 路由 | combos、三层 fallback | 逻辑模型目标链 | AgentSwitch 的语义更适合“指定模型 + 故障转移”；9Router 更适合“成本优先” |
| 多账号 | 强 | 弱 | 多账号是共同需求；先做轻量方案 |
| 可观测性 | usage analytics、配额、请求日志 | 请求环 + 尝试链 | 补用量/配额视图 |
| 安全默认 | 默认弱（需加固） | 默认强（loopback/AES-GCM/令牌） | 隐私与安全是 AgentSwitch 的差异化，不要为了易用性牺牲 |
| 协议/工具广度 | 很广 | 窄而深 | 不追广度；把 Claude/Codex 做到最稳 |

---

### 3.4 LiteLLM

**定位与目标用户**

- 【事实·官方】“Open Source AI Gateway for 100+ LLMs. Self-hosted. Enterprise-ready. Call any LLM in OpenAI format.” 提供 Python SDK 与集中式 Proxy。
- 【事实·官方】目标用户是平台/ML 团队与 LLM 应用开发者，强调集中访问、治理、成本可见性。
- 【事实·仓库】仓库描述为“Rust core with Python SDK”（PM 记录，主 Agent 未独立复核具体数字）。

**核心场景**

1. 用统一 OpenAI 格式调用 100+ providers；
2. 虚拟 Key、团队/项目、预算与花费追踪；
3. Router 重试/fallback/负载均衡；
4. 日志、guardrails、缓存、回调（Langfuse、MLflow 等）；
5. 企业部署（Docker/Helm/Terraform/AWS/GCP）；
6. 企业 SSO、SLA、专属支持。

**模型/供应商/端点管理**

- 【事实·官方】100+ providers，覆盖 OpenAI、Anthropic、Gemini、Bedrock、Azure 等；端点覆盖 chat/messages/responses/embeddings/images/audio/moderations/batches/rerank，但各 provider 支持不均匀。
- 【事实·官方】Proxy 支持 virtual keys、团队/项目、预算、日志、guardrails、缓存、dashboard；SDK 支持 Router 重试/fallback。

**对 Claude Code / Codex 的支持**

- 【事实·官方】提供 Claude Code、Codex、OpenCode、pi、Deep Agents 的 agent harness；Proxy 暴露 Anthropic/OpenAI 兼容端点，可作为这些工具的网关。
- 【推断】它不是桌面配置管理器，不负责把配置写入 Claude Code/Codex 文件；用户需要自行配置 base URL/key。

**配置切换/备份/恢复/隔离**

- 【事实·官方】以 `config.yaml` + 数据库/部署配置为中心；虚拟 Key/团队/项目提供逻辑隔离。
- 【事实·官方】部署支持 Docker、Helm、Terraform、Render、Railway；stable 镜像经过 12 小时负载测试。
- 【未能核实】面向个人用户的配置备份/恢复/迁移体验；这不是它的主要场景。

**扩展性与设计特点**

- 能力上限最高：Provider 覆盖、预算、虚拟 Key、可观测性、guardrails、回调、企业部署。
- 代价是服务端复杂度：Python/DB/Redis/容器/迁移/企业配置。

**已知限制与用户痛点**

- 【事实·Issue】#30484 是官方“Stability Sprint Roadmap”，说明稳定性是持续投入项；#47826 报告 `litellm 1.107.0` 被入侵并从 PyPI/Docker 移除，供应链安全是真实风险；#26672 报告预算 enforcement 被绕过；#26886 报告 Prisma 重连失败；#9753 是 Prisma Python client 弃用迁移。
- 【事实·官方】各 provider 端点支持不均匀；`litellm` 与 `litellm-core` 不能同环境安装；核心构建需要 Rust 工具链；GCP Cloud Run 不能直接拉 ghcr.io；MCP OAuth 需要 provider 预注册应用。
- 【推断】对个人开发者而言，部署与维护成本远高于 AgentSwitch；但它的功能清单是“网关能力上限”的参照。

**与 AgentSwitch 的差异与可借鉴处**

| 维度 | LiteLLM | AgentSwitch | 启示 |
|---|---|---|---|
| 用户 | 平台/企业团队 | 单机个人开发者 | 不照搬多租户/预算/SSO |
| 部署 | 服务端（Python/DB/容器） | 桌面单二进制 | 单二进制是优势 |
| 路由 | Router 重试/fallback/负载均衡 | 逻辑模型目标链 | 可借鉴 fallback 策略与可观测性 |
| 成本 | 花费追踪、预算、虚拟 Key | 无 | 做“估算/相对比较”，不做账单级 |
| 可观测性 | 日志、回调、dashboard | 20 条请求 | 这是必须补的短板 |
| 供应链风险 | 依赖多、曾有安全事件 | 依赖少、单二进制 | 安全/简洁是差异化，不要引入不必要依赖 |

---

### 3.5 new-api / one-api

**定位与目标用户**

- 【事实·官方】new-api 是“self-hosted AI gateway for applications, agents, and teams”，集中管理上游连接、统一客户端 API、路由、访问控制、用量与成本。
- 【事实·官方】目标用户：团队共享模型访问、希望客户端不重复配置、需要私有化多模型服务的运营者。
- 【事实·官方】基于 one-api（MIT）开发；new-api 使用 AGPLv3 + 附加条款（保留前端署名与可见链接）。
- 【事实·仓库】new-api 基于 one-api 开发；两者仓库均显示活跃 Issue/提交，但主 Agent 未独立复核具体数字；one-api 维护活跃度本次未能确认。

**核心场景**

1. 团队/多用户共享上游模型；
2. 渠道优先级/权重、模型映射、重试、渠道亲和、多上游 Key；
3. 配额、订阅、用量日志、缓存计费、表达式定价；
4. 用户/分组/权限、API Key 限制、OAuth/OIDC、Passkey、2FA；
5. Web 控制台、Playground、七种 UI 语言；
6. 异步任务插件、图像/音频/Embedding/Rerank 等端点。

**模型/供应商/端点管理**

- 【事实·官方】OpenAI、Anthropic、Gemini、Azure OpenAI、AWS Bedrock、Vertex AI、DeepSeek、Qwen 等；暴露 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`、Gemini `generateContent`、Realtime/WebSocket、图像/音频/Embedding/Rerank、任务插件路由。
- 【事实·官方】RelayKit 做四种文本协议转换，但“可用功能取决于渠道、上游模型与转换路径”，工具/字段可能无法精确映射。

**对 Claude Code / Codex 的支持**

- 【事实·官方】协议端点可服务 Claude Code/Codex；但它不是桌面配置管理器，不负责写 Agent 配置文件。
- 【事实·Issue】Claude Code 使用 OpenAI 格式接入 DeepSeek V4 上游会遇到 400 `reasoning_content`（#4543）；接入 Claude Code 报错（#1618）；OpenRouter 原生 `/v1/messages`、`/v1/responses` 端点仍属需求（#2822）。说明“能暴露端点”与“真实 Agent 稳定可用”之间有差距。

**配置切换/备份/恢复/隔离**

- 【事实·官方】以渠道/分组/用户/令牌为中心；数据库支持 SQLite/MySQL/PostgreSQL；支持独立日志库。
- 【事实·官方】生产部署需要 HTTPS 反代、持久化备份、共享数据库与认证密钥；多节点需要共享 Redis/限流器。
- 【未能核实】面向个人用户的配置导出/导入/恢复体验；不是其主要场景。

**扩展性与设计特点**

- 渠道优先级/权重、模型映射、重试、亲和、多 Key、配额、订阅、计费、插件、Web 控制台。
- 运营/多用户导向强；配置与计费概念多。

**已知限制与用户痛点**

- 【事实·官方】协议转换可能不完整；高级功能依赖渠道/上游/转换路径；WebSocket 依赖兼容上游。
- 【事实·官方】AGPLv3 + 附加条款；生产运营涉及合规与上游授权责任。
- 【事实·Issue】渠道异常响应无法重试其他渠道（#4139）；流式响应偶发 `scanner_error`（#6547）；Claude Code/DeepSeek 兼容问题（#4543、#1618）；OpenRouter 原生端点需求（#2822）；计费/订阅码需求（#2845、#4242）。说明协议转换、故障转移与计费是持续痛点。
- 【推断】对单机个人开发者而言，new-api 的部署、用户/分组/计费模型过重；AgentSwitch 不应复制。

**与 AgentSwitch 的差异与可借鉴处**

| 维度 | new-api / one-api | AgentSwitch | 启示 |
|---|---|---|---|
| 用户 | 团队/运营者 | 单机个人开发者 | 不做多用户/计费 |
| 路由 | 渠道优先级/权重/模型映射/重试/亲和 | 逻辑模型目标链 | 可借鉴“权重/亲和”但不必全做 |
| 协议 | 四种文本协议 + 多模态 | 三种入站/两种上游 | 先保 Claude/Codex 稳定，不追多模态 |
| 用量 | 配额/订阅/日志/计费 | 无 | 做轻量用量/配额，不做账单 |
| 部署 | Docker/DB/Redis/多节点 | 单二进制 | 简洁是优势 |
| 许可 | AGPL + 附加条款 | MIT | 开源许可更友好，是潜在优势 |

---

### 3.6 核心事实核实表

> 本表只覆盖用于本文结论的核心事实；完整来源见第 9 节。状态口径：**已核实** = 官方 README/文档/Release/页面原文；**事实·Issue** = 公开 Issue 报告，不等于官方确认；**推断** = 由文档/产品形态推断；**无法确认** = 本次访问未能核实。

| 竞品 | 事实 | 来源 | 状态 |
|---|---|---|---|
| CC Switch | 12 工具、90+ 预置、Direct/Routing/Aggregation、用量/配额、备份、云同步 | https://raw.githubusercontent.com/farion1231/cc-switch/main/README.md | 已核实（官方 README） |
| CC Switch | Routing 默认 `127.0.0.1:15721`、占位 Key `PROXY_MANAGED`；官方供应商通常不能路由（Codex OpenAI Official 例外） | https://raw.githubusercontent.com/farion1231/cc-switch/main/docs/user-manual/en/4-proxy/4.2-routing.md | 已核实（官方手册） |
| CC Switch | Aggregation 仅 Claude Code/Codex；不提供故障转移；Claude Code 需 2.1.243+ | README + https://qcode.cc/en/cc-switch-4-aggregate-mode | 已核实（官方 README + 第三方引述；官方 Aggregation 手册本次未获取） |
| CC Switch | Routing 熔断/连接重置/跨供应商兼容问题 | https://github.com/farion1231/cc-switch/issues/8668、https://github.com/farion1231/cc-switch/issues/8888、https://github.com/farion1231/cc-switch/issues/8989 | 事实·Issue |
| CC Switch | 仓库活跃、Issue 量大（具体数字本轮未独立复核，故不引用） | https://github.com/farion1231/cc-switch | 页面快照（2026-10-29，未复核数字） |
| CCR | 本地模型网关/控制面；10+ Agent；providers/profiles/routing/quotas/traces | https://raw.githubusercontent.com/musistudio/claude-code-router/main/README.md | 已核实（官方 README） |
| CCR | v3.1.3（2026-10-09）修复 Codex session affinity、stream reset、provider alias 等 | https://github.com/musistudio/claude-code-router/releases | 已核实（Release Notes） |
| CCR | API 400/500/连接错误、transformer 文档不清 | https://github.com/musistudio/claude-code-router/issues/23、https://github.com/musistudio/claude-code-router/issues/542、https://github.com/musistudio/claude-code-router/issues/614 | 事实·Issue |
| CCR | 备份/恢复、通知、完整会话粘性细节 | README 未展开 | 无法确认 |
| 9Router | 本地 OpenAI 兼容路由；40+ providers；Subscription→Cheap→Free 三层 fallback；配额/成本估算；RTK | https://raw.githubusercontent.com/decolua/9router/master/README.md | 已核实（官方 README） |
| 9Router | 默认 Dashboard 密码 `123456`、`REQUIRE_API_KEY` 默认 false；成本只是估算 | 同上 | 已核实（官方 README） |
| 9Router | 最新提交 2026-10-28（v0.5.112） | https://github.com/decolua/9router/commits/master | 已核实（提交页） |
| 9Router | 最新正式 Release/版本节奏 | Releases 页面本次未见条目 | 无法确认 |
| LiteLLM | 100+ providers；Proxy/SDK；预算/虚拟 Key/可观测性/企业部署 | https://raw.githubusercontent.com/BerriAI/litellm/main/README.md | 已核实（官方 README） |
| LiteLLM | 1.107.0 被入侵/从 PyPI/Docker 移除；Stability Sprint；预算 enforcement 绕过；Prisma 重连失败 | https://github.com/BerriAI/litellm/issues/47826、https://github.com/BerriAI/litellm/issues/30484、https://github.com/BerriAI/litellm/issues/26672、https://github.com/BerriAI/litellm/issues/26886 | 事实·Issue（未独立确认） |
| LiteLLM | 各 provider 端点支持不均匀；`litellm`/`litellm-core` 不能同环境安装 | 官方 README | 已核实 |
| new-api | 自托管 AI 网关；四种文本协议；渠道/配额/订阅/用量；AGPLv3 + 附加条款 | https://raw.githubusercontent.com/QuantumNous/new-api/main/README.md | 已核实（官方 README） |
| new-api | 协议转换可能不完整；高级功能依赖渠道/上游/转换路径 | 官方 README | 已核实 |
| new-api | Claude Code/DeepSeek 400、渠道异常无法重试、流式 scanner_error | https://github.com/QuantumNous/new-api/issues/4543、https://github.com/QuantumNous/new-api/issues/4139、https://github.com/QuantumNous/new-api/issues/6547 | 事实·Issue |
| new-api / one-api | new-api 基于 one-api；两者仓库均活跃（具体数字本轮未独立复核，故不引用） | https://github.com/QuantumNous/new-api、https://github.com/songquanpeng/one-api | 页面快照（2026-10-29，未复核数字） |
| one-api | 最近维护状态 | 仓库页面未显示提交/发布时间 | 无法确认 |

---

## 4. 能力对比总表

> 详细矩阵与证据见 `docs/product/capability-matrix.md`。下表只做总览。
>
> 图例：✅ 官方明确支持；◐ 部分/有条件；❌ 未见或明确不支持；? 本次未核实；— 不适用。

| 能力维度 | AgentSwitch | CC Switch | CCR | 9Router | LiteLLM | new-api |
|---|---|---|---|---|---|---|
| 供应商/模型管理 | ◐ 20 预置、2 协议、单 Key | ✅ 90+ 预置、官方/自定义、多账号 | ✅ 预置+自定义+发现+凭证池 | ✅ 40+ providers、多账号 | ✅ 100+ providers | ✅ 多渠道/多 Key |
| 多模型配置 | ✅ 逻辑模型目标链 | ✅ 模型映射/聚合 | ✅ catalog/alias/virtual model | ✅ combos/alias | ✅ model list/router | ✅ model mapping |
| 端点/协议适配 | ◐ Anthropic/OpenAI Chat/Responses | ✅ 四种上游格式 | ✅ 多协议 | ✅ 多协议 | ✅ 多协议/多模态 | ✅ 四种文本协议+多模态 |
| Claude Code 接入 | ✅ 文件投影 | ✅ 切换/路由/聚合 | ✅ profile/启动 | ✅ | ◐ 网关/harness | ◐ 端点服务 |
| Codex 接入 | ✅ config.toml/目录/元数据 | ✅ 切换/路由/聚合 | ✅ profile/启动 | ✅ | ◐ 网关/harness | ◐ 端点服务 |
| 其他 Agent 覆盖 | ❌ 仅 3 类 | ✅ 12 工具 | ✅ 10+ | ✅ 15+ | ◐ 多 harness | ❌ |
| 配置切换与反馈 | ◐ 应用/重新应用，无 diff | ✅ 预览/热切换/项目 | ✅ profile/规则 | ✅ dashboard | ◐ 服务端配置 | ◐ 控制台 |
| 备份/恢复 | ◐ 最近 10 份，仅还原最近 | ✅ 首写+每日+项目 | ? | ✅ 自动备份 | ◐ DB/部署 | ◐ DB/部署 |
| 导入/导出/迁移 | ❌ | ✅ 导入导出/Deep Link/云同步 | ◐ 本地登录/配置导入 | ◐ 云同步 | ◐ config/DB | ◐ DB/config |
| 可用性检测 | ◐ 手动测试+反应式冷却 | ◐ 连通性检查（README 称健康检查） | ✅ 协议探测/连通性/模型发现 | ◐ 配额/错误 fallback | ◐ health/router | ◐ 渠道测试（未核实） |
| 配额/错误/限流反馈 | ◐ 冷却+尝试链，无配额 | ✅ 配额/余额/用量 | ✅ 配额/trace/account status | ✅ 配额窗口/成本估算 | ✅ 预算/virtual key | ✅ 配额/订阅/日志 |
| 用量/成本统计 | ❌ | ✅ usage/heatmap/custom pricing | ✅ token/cost/latency | ✅ usage analytics | ✅ spend tracking | ✅ usage logs |
| 多配置/环境隔离 | ◐ 每 Agent 多配置+启动器 | ✅ 项目/WSL/云同步 | ✅ profile/scope/多实例 | ✅ 多账号/combos | ✅ team/project | ✅ user/group |
| 历史与故障排查 | ◐ 20 条请求+尝试链+轮转日志 | ✅ 会话/日志/用量 | ✅ 请求详情/trace | ✅ 请求/翻译日志 | ✅ 日志/回调/dashboard | ✅ 日志/playground |
| 扩展机制 | ◐ 用户预置 JSON | ✅ 预置/Deep Link/Universal | ✅ 插件/ToolHub/Fusion | ✅ combos/provider data/headroom | ✅ SDK/callback/guardrail | ✅ JS 插件/任务插件 |
| 本地数据与隐私 | ✅ loopback+AES-GCM+零遥测 | ◐ 本地 SQLite+可选云同步 | ◐ 本地 SQLite | ◐ 本地 SQLite，默认弱安全 | ◐ 自托管 | ◐ 自托管 |
| 易用性/新手引导 | ◐ 4 页、无向导 | ✅ GUI/预置/i18n | ◐ 功能多、概念多 | ◐ dashboard/概念多 | ❌ 服务端配置 | ❌ 运营配置 |
| 安装/升级/容灾 | ◐ 单二进制；无自动更新；Win/ARM 未实机 | ✅ 自动更新/云备份 | ◐ npm/桌面/Docker | ◐ npm/Docker/PM2 | ◐ 多部署/迁移复杂 | ◐ Docker/DB 迁移 |
| 多账号/密钥池 | ❌ 单 Key/渠道 | ✅ 多账号（Beta） | ✅ 凭证池 | ✅ 多账号 | ✅ virtual keys | ✅ 多 Key |
| 通知/告警 | ❌ | ? | ? | ? | ? | ? |
| 会话粘性/亲和 | ❌ | ◐ Issue 有需求 | ◐ Release 修 Codex affinity | ? | ? | ✅ 渠道亲和 |
| 条件路由/请求改写 | ❌ | ? | ✅ header/body/prefix/rewrite | ❌ 未见 | ✅ routing policies | ✅ 模型映射/重试 |
| 团队/多租户 | ❌ | ❌ | ◐ 客户端 Key/额度 | ❌ | ✅ | ✅ |
| 云同步 | ❌ | ✅ WebDAV/S3 | ? | ✅ | ❌ | ❌ |

**总览判断**：

- AgentSwitch 在“协议转换 + 逻辑模型故障转移 + 本地隐私 + 单二进制”上不弱，但在**可观测性、用量、备份迁移、多账号、Agent 覆盖、易用性**上明显落后。
- 竞品的共同方向是“从配置管理走向本地网关，再从网关走向可观测性/配额/多账号”。AgentSwitch 已经在网关层，但缺了后半个闭环。
- 在本次样本中，没有一家竞品同时做到“单机、零云、模型级故障转移、强隐私、中文 Coding Plan 友好”。这是 AgentSwitch 可守住的组合，但需要把每个词都做成用户可感知的事实；该结论基于公开文档，未逐一验证竞品的私有实现。

---

## 5. 跨竞品洞察

### 5.1 品类基础能力（Table Stakes）

以下能力在 2026-10 的竞品中已经普遍出现，不能再视为“差异化”：

1. **多供应商预置与自定义端点**：CC Switch 90+、9Router 40+、LiteLLM 100+、new-api 多渠道。AgentSwitch 20 条精选预置可以接受，但需要让用户方便地添加自定义/导入。
2. **配置应用与备份**：CC Switch 有首写备份、每日备份、项目；9Router 自动备份；AgentSwitch 只有最近 10 份且仅能还原最近一份。
3. **请求日志/用量**：CC Switch、CCR、9Router、LiteLLM、new-api 都有。AgentSwitch 只有 20 条内存记录。
4. **多账号/多 Key**：CC Switch、CCR、9Router、new-api 都有；AgentSwitch 单 Key/渠道。
5. **导入/导出/迁移**：CC Switch 在本次样本中最完整；AgentSwitch 没有。
6. **搜索/筛选/新手引导**：CC Switch/CCR/9Router 都有不同程度的列表管理与引导；AgentSwitch 没有搜索与端到端向导。
7. **协议转换的真实兼容性**：所有网关竞品都在 Issue 里暴露大量 400/500/stream 兼容问题；这不是“有没有转换层”，而是“有没有真实 Agent 回归”。

### 5.2 竞品反复暴露的痛点

| 痛点模式 | 竞品证据 | 对 AgentSwitch 的含义 |
|---|---|---|
| 路由配置脆弱/不透明 | CC Switch #8989、#8665；CCR #542、#614 | 逻辑模型目标链必须可解释、可测试、可回滚；不要引入条件 DSL |
| 协议转换边界错误 | CC Switch #9018、#8888；new-api #4543、#1618；CCR #23 | 真实 Agent E2E 回归比“支持协议列表”更重要 |
| 熔断/故障转移状态错误 | CC Switch #8668（连接重置导致全局 503）、#8499（流中断未计入）；new-api #4139 | AgentSwitch 的“首字节后不重放”是正确不变量；还要做半开探测与会话粘性 |
| 聚合/多模型与会话冲突 | CC Switch #8516、#7962 | 多模型列表/目录变更要明确重启/新会话语义；Codex 尤其 |
| 用量/成本不准确 | LiteLLM #39057、#26672；9Router “成本只是估算” | 做“估算/相对比较”，不要承诺账单级；标注数据来源与置信度 |
| 供应链/部署复杂 | LiteLLM #47826、#9753；new-api AGPL/多节点 | 单二进制/少依赖是优势；不要引入 Node/Python/Redis |
| 平台/安装问题 | CC Switch #721、#1927；9Router 默认弱安全 | Windows/macOS ARM/托盘/二启必须先验证；安全默认不能降 |
| 免费层/订阅条款风险 | 9Router 免费层变动；CC Switch Accounts Beta 提示条款风险 | 不追免费层套利；中文 Coding Plan 预置要明确“按供应商条款使用” |

### 5.3 竞品有，但 AgentSwitch 不应做

| 能力 | 为什么竞品有 | 为什么 AgentSwitch 不做 |
|---|---|---|
| 12+ Agent 覆盖 | CC Switch 的生态护城河 | 每个 Agent 都有配置/协议/重启语义；维护成本高，且会稀释“网关可靠性”定位 |
| MCP/Skills/Prompts 管理 | CC Switch 的配置管理延伸 | 与模型网关无关；cc-switch 已占心智；AgentSwitch 只做“连接指南”即可 |
| 会话阅读/搜索 | CC Switch 的增值功能 | 需要解析各 Agent 会话格式；隐私敏感；非核心 |
| Token 压缩/RTK/提示词注入 | 9Router 的成本优化 | 改变模型行为、影响可解释性；透明网关不应默认改写请求 |
| 云同步 | CC Switch/9Router 的多机需求 | 与“本地优先/零云”冲突；用加密导出/导入替代 |
| 多租户/虚拟 Key/计费 | LiteLLM/new-api 的团队场景 | 单机个人开发者不需要；引入账号、权限、计费复杂度 |
| Guardrails/语义缓存 | 企业网关能力 | 不是 AgentSwitch 的核心用户问题；可由上游/其他工具承担 |
| 免费层套利 | 9Router 的增长手段 | 条款/稳定性/合规风险高；AgentSwitch 应服务“授权渠道” |

### 5.4 竞品有，且 AgentSwitch 定位需要

| 能力 | 竞品证据 | 为什么是定位需要 |
|---|---|---|
| 持久化请求/路由历史 | CC Switch/CCR/LiteLLM/new-api | 故障转移是核心卖点；不可解释就无法建立信任 |
| 用量/配额视图 | CC Switch/CCR/9Router/new-api | 用户需要知道“哪个渠道在消耗、何时会耗尽” |
| 备份历史/导出/导入 | CC Switch | 配置是用户资产；不能只还原最近一份；迁移是增长杠杆 |
| 多账号/多 Key（轻量） | CC Switch/CCR/9Router | 订阅/额度用户真实存在；至少让“主备 Key”易用 |
| 主动健康检查/半开熔断 | CC Switch README/CCR | 减少“冷却到期后再次失败”的无效请求 |
| 告警/通知 | 竞品未完全核实，但运维需求明确 | 全渠道冷却/额度耗尽时，用户需要被动知道 |
| 搜索/筛选/新手引导 | CC Switch/CCR/9Router | 条目变多后可用性下降；首次上手路径仍长 |
| 真实 Agent 兼容性回归 | 所有网关竞品的 Issue | 协议转换的正确性是网关的生命线 |

---

## 6. 对 AgentSwitch 的启示

### 6.1 定位建议

**推荐定位：本地 AI Agent 的可靠性层（Local Reliability Layer for AI Coding Agents）。**

一句话：

> AgentSwitch 让 Claude Code / Codex / Claude Desktop 永远指向一个本地入口；它按逻辑模型把请求路由到不同供应商和模型，失败时在响应提交前自动切换，并把“为什么切换、切到哪、消耗多少”如实告诉用户。单二进制、零云依赖、密钥本地加密。

这个定位的三个支点：

1. **模型级故障转移**：不是“换供应商”，而是“同一个逻辑模型跨渠道、跨模型名保持可用”。
2. **可解释性**：每次请求的候选、跳过原因、冷却、耗时、用量都可查。
3. **本地隐私**：loopback、AES-GCM、零遥测、无云同步；导出也默认不含明文密钥。

### 6.2 差异化主线

| 差异化点 | 竞品现状 | AgentSwitch 应怎么做 |
|---|---|---|
| 逻辑模型目标链 | CC Switch 的 Routing 是供应商队列，Aggregation 无故障转移；CCR 是 provider/profile + 条件规则 | 把“一个逻辑模型 → 多个「渠道·模型」目标”做成第一屏可理解的概念；提供目标链测试/模拟 |
| 可解释故障转移 | 竞品有日志/trace，但多为“请求详情”；少有“为什么跳过这个目标” | 持久化尝试链，展示候选过滤、冷却、错误分类、耗时；提供“本次为什么失败”摘要 |
| 协议转换正确性 | 竞品 Issue 大量 400/500/stream 问题 | 建真实 Agent E2E 回归（Claude Code/Codex CLI/桌面版），把“三入站”变成可信承诺 |
| 本地隐私/单二进制 | 竞品多为 Node/Python/Tauri/多依赖 | 保持单二进制、零云、强默认；导出加密；不引入 Redis/Node/Python |
| 中文 Coding Plan 友好 | CC Switch 90+ 预置但偏通用；9Router 偏免费层 | 维护 20-30 条高质量中文 Coding Plan 预置与模型建议；明确条款与额度差异 |
| Codex 深度 | CC Switch/CCR 也支持 Codex，但多为切换/路由 | 保持 config.toml 受管键、目录、元数据、子代理、自动审批的深度；这是现有优势 |

### 6.3 不建议的路线

- **“另一个 cc-switch”**：追 12 工具、MCP/Skills/Prompts、会话、云同步；资源与心智都不在一条线上。
- **“另一个 LiteLLM”**：多租户、虚拟 Key、预算、SSO、Guardrails；会把桌面产品拖入服务端运维。
- **“免费层套利路由器”**：短期增长快，但条款、稳定性、安全默认风险高；与“可靠/隐私”定位冲突。
- **“Token 压缩/提示词注入”**：改变模型行为，破坏“透明网关”的信任；不建议默认做。

### 6.4 增长与迁移

- 竞品用户已经有大量配置资产。提供**从 cc-switch/CCR 导入**（或至少通用 JSON/CSV 导入）可以降低切换成本；但要注意密钥安全与格式兼容，先做“导入预览 + 差异确认”，不要静默写入。
- 导出/导入也是“反锁定”承诺：用户可以随时带走配置，这反而会增加尝试意愿。

---

## 7. 结论与建议

### 7.1 三个最重要的结论

1. **AgentSwitch 的竞争窗口在“可靠性层”，不在“配置管理”。** CC Switch 已经在配置管理 + 本地路由上形成规模优势；AgentSwitch 只有在“模型级故障转移 + 可解释性 + 本地隐私”上做深，才有独立价值。
2. **可观测性是 P0，不是锦上添花。** 没有持久化请求/路由历史、用量/配额、告警，用户无法验证“自动切换”是否发生、是否有效；这会直接摧毁核心卖点。
3. **配置可迁移性是重要能力，但优先级应低于“写入可信”。** 没有导出/导入/备份历史，用户不敢把生产配置交给 AgentSwitch；但当前更紧急的是修复「仅保存」改 live 且不备份的语义缺陷。主 Agent 建议：先修写入一致性，再做备份列表/导出导入（`roadmap.md` P0-0 → P1-1），而不是把两者都塞进 P0。

### 7.2 建议的下一步

1. 用 `feature-backlog.md` 的 RICE 排序确认 P0/P1；
2. 用 `roadmap.md` 把 P0 拆成可验收的发布范围；
3. 用 `decisions.md` 拍板“是否推翻旧 spec 的用量统计非目标”“是否做多账号”“是否做竞品导入”等关键问题；
4. 在进入实现前，补一次真实 Agent 兼容性基线（Claude Code/Codex CLI/桌面版），否则无法判断协议转换的真实质量。

---

## 8. 未能核实与本次分析的限制

1. **未实机安装竞品**：CC Switch、CCR、9Router、LiteLLM、new-api 的功能细节均来自官方文档/README/Release/Issue，未逐屏验证。
2. **未做用户访谈**：本文的“用户痛点”来自公开 Issue 与产品逻辑推断，不代表 AgentSwitch 目标用户的真实分布。
3. **Star/Issue 数未引用**：PM 子 Agent 记录了页面快照，但主 Agent 复核时 GitHub API 限流，无法独立验证；本文不引用具体数字，也不把热度当市场份额。
4. **CC Switch 健康检查/熔断细节**：README 声称有 health monitoring/circuit breaking，但本次获取的 Routing 手册未展开；标注为“官方 README 声称，细节未核实”。
5. **CCR 备份/恢复、通知、会话粘性**：README 未展开，部分只能从 Release 推断；标注为“未能核实/部分”。
6. **9Router 最新版本/发布节奏**：Releases 页面本次未见条目；以 commits 页面（2026-10-28，v0.5.112）作为活跃度证据。
7. **one-api 维护状态**：仓库页面未显示最近提交/发布时间，本次无法确认；仅作为 new-api 的前身参照。
8. **LiteLLM/new-api 的企业功能**：未部署验证；只作为能力上限参照，不作为直接竞品结论。
9. **AgentSwitch 的 GUI 体感**：本次未做 GUI 逐屏走查；相关判断以 `capability-matrix.md` 与代码抽查为准。

---

## 9. 来源清单（访问日期 2026-10-29）

### CC Switch

- 仓库主页：https://github.com/farion1231/cc-switch
- README：https://raw.githubusercontent.com/farion1231/cc-switch/main/README.md
- Releases：https://github.com/farion1231/cc-switch/releases
- Issues（按评论排序）：https://github.com/farion1231/cc-switch/issues?q=is%3Aissue%20is%3Aopen%20sort%3Acomments-desc
- Routing 相关 Issues：https://github.com/farion1231/cc-switch/issues?q=is%3Aissue%20routing%20OR%20aggregation%20OR%20failover
- 官方 Routing 手册：https://raw.githubusercontent.com/farion1231/cc-switch/main/docs/user-manual/en/4-proxy/4.2-routing.md
- 官方 Routing 文档页：https://ccswitch.io/zh/docs?section=proxy&item=routing
- 第三方 Aggregation 解读（辅助）：https://qcode.cc/en/cc-switch-4-aggregate-mode
- 具体 Issue：#721、#1927、#7962、#8516、#8645、#8665、#8668、#8888、#8989、#9018（URL 形如 `https://github.com/farion1231/cc-switch/issues/<编号>`）

### Claude Code Router

- 仓库主页：https://github.com/musistudio/claude-code-router
- README：https://raw.githubusercontent.com/musistudio/claude-code-router/main/README.md
- Releases：https://github.com/musistudio/claude-code-router/releases
- Issues（按评论排序）：https://github.com/musistudio/claude-code-router/issues?q=is%3Aissue%20is%3Aopen%20sort%3Acomments-desc
- 具体 Issue：#19、#23、#150、#160、#245、#377、#527、#542、#614、#852

### 9Router

- 仓库主页：https://github.com/decolua/9router
- README：https://raw.githubusercontent.com/decolua/9router/master/README.md
- Commits：https://github.com/decolua/9router/commits/master

### LiteLLM

- 仓库主页：https://github.com/BerriAI/litellm
- README：https://raw.githubusercontent.com/BerriAI/litellm/main/README.md
- Releases：https://github.com/BerriAI/litellm/releases
- Issues（按评论排序）：https://github.com/BerriAI/litellm/issues?q=is%3Aissue%20is%3Aopen%20sort%3Acomments-desc
- 具体 Issue：#9753、#18686、#25762、#26672、#26886、#30484、#31263、#39057、#47826

### new-api / one-api

- new-api 仓库主页：https://github.com/QuantumNous/new-api
- new-api README：https://raw.githubusercontent.com/QuantumNous/new-api/main/README.md
- new-api Releases：https://github.com/QuantumNous/new-api/releases
- new-api Issues（按评论排序）：https://github.com/QuantumNous/new-api/issues?q=is%3Aissue%20is%3Aopen%20sort%3Acomments-desc
- 具体 Issue：#994、#1618、#2822、#2845、#4139、#4242、#4543、#4711、#6547、#7260
- one-api 仓库主页：https://github.com/songquanpeng/one-api

### AgentSwitch 内部证据

- 能力基线：`docs/product/capability-matrix.md`
- 产品介绍：`README.md`
- 架构：`ARCHITECTURE.md`
- 路由与故障转移：`internal/gateway/`、`internal/router/`、`internal/telemetry/ring.go`
- 渠道/逻辑模型/接入：`internal/bridge/`、`internal/desktop/pages/`
- 预置：`internal/presets/default.json`
- 存储与密钥：`internal/storage/`、`internal/credential/`
