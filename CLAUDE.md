# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目状态与权威文档

AgentSwitch：面向本地 AI Agent 的**模型 API 网关**——为 Claude Code / Codex CLI 等工具提供统一的本地模型入口，集中管理多供应商/多账号，支持手动指定账号与有限、安全的自动故障转移。

技术选型（v2.0 设计稿确定）：Go 实现网关核心，**MyGo**（https://github.com/egoist/mygo，桌面框架）做桌面壳与前后端桥接，前端 TypeScript + HTML/CSS，数据用 SQLite，凭据存 OS 密钥存储。

当前仓库已完成 P0-A′ 桌面优先 MVP（实现计划：`docs/superpowers/plans/2026-10-15-agentswitch-p0a-desktop-first.md`）：单二进制桌面应用，入口 `cmd/agentswitch`，Go 1.27.2，依赖 mygo v0.5.1 与 modernc.org/sqlite；核心包在 `internal/{app,bridge,config,credential,desktop,gateway,protocol,provider,router,storage,telemetry}`，测试全部离线（httptest Fake Provider），`README.md` 含首次使用流程。相对 v2.0 目标结构（§12）的落地差异：管理面以进程内 `bridge`（`mygo.Bind` 桌面桥接、无独立管理端口）替代 `controlapi`；migrations 内嵌在 `internal/storage`（当前单一 v1）。**UI 决策（2026-10-17）**：后续全部使用 MyGo 原生视图（`internal/desktop`），`frontend/` TS 工程方案**废弃**——这是对 docx §12 前端规划的明示偏离（docx 不改，以 `docs/superpowers/specs/2026-10-17-desktop-ui-mygo-native-design.md` 为凭证）；桌面 UI 设计系统见 `docs/superpowers/specs/2026-10-17-desktop-ui-ccswitch-redesign-design.md` 与 `internal/desktop/theme.go`——视觉系统由该 spec 取代旧「交换机面板」（橙主操作/绿橙分离/卡片流/列表+详情 IA）。P0-B 已随上述 spec 落地，后续项留档见该计划文档末尾。2026-10-17 起渠道模型为 new-api 式 **channels 聚合根**（`channels`/`channel_keys`/`channel_models` 三表，渠道级模型清单与重定向；providers/accounts/models 旧表已退役）——设计见 `docs/superpowers/specs/2026-10-17-channels-models-p0b-design.md`，实现计划见 `docs/superpowers/plans/2026-10-17-channels-models-p0b.md`。P0-B（优先级故障转移、冷却、二次校验）随该 spec 落地。**当前形态（2026-10-17 逻辑模型重构）**：路由主体是用户自定义的**逻辑模型**（`logical_models` + `logical_model_targets` 有序目标链），渠道为**单 Key** 执行单元（`channels` + `channel_models` 模型建议列表，`channel_keys` 多密钥表及其代码路径已删除）；桌面 UI 为 cc-switch 式侧栏（逻辑模型/渠道/接入/设置 + 底条网关状态）；网关**双入站**（Anthropic `POST /v1/messages` + OpenAI `POST /v1/chat/completions`，共用同一套逻辑模型/冷却/故障转移核心）——设计见 `docs/superpowers/specs/2026-10-17-logical-models-ccswitch-ui-design.md`，实现计划见 `docs/superpowers/plans/2026-10-17-logical-models-ccswitch-ui.md`。旧「按渠道优先级故障转移」路由入口已删除，由逻辑模型目标链取代。**重构均为删库重建**：schema 合并为单一 migration v1；旧库含 `channel_keys` 表时启动报错，老用户需删除一次 `~/Library/Application Support/AgentSwitch/agentswitch.db`（不静默清库、不就地迁移）。

**权威设计文档**是仓库根目录的 docx（磁盘上文件名经 URL 编码，实际为 `AgentSwitch_MyGo桌面应用重新设计方案_v2.0.docx`）。任何功能实现前先查文档对应章节；代码与文档冲突时，以文档为准并指出来。文档是 zip 格式，可用以下命令提取纯文本：

```bash
python3 -c "import zipfile,re,html,sys; x=zipfile.ZipFile(sys.argv[1]).read('word/document.xml').decode().replace('</w:p>','\n'); print(html.unescape(re.sub(r'<[^>]+>','',x)))" <file.docx>
```

## 常用命令

```bash
go build ./...        # 编译全部包
go run .              # 运行当前入口
go vet ./...          # 静态检查
go test ./...         # 全部测试
go test ./internal/app -run TestConfigChangeTakesEffectWithoutRestart -v   # 跑单个测试
go test -race ./...   # 涉及并发/冷却状态的测试必须带 -race
go tool mygo dev      # 开发运行（热重载；入口 cmd/agentswitch，UI 在 internal/desktop）
go tool mygo build    # 打包（产物在 build/）
```

无独立前端工程（UI 决策 2026-10-17：全部 MyGo 原生视图，设计 token 在 `internal/desktop/theme.go`）。MyGo 项目模板与打包命令以选定版本的官方仓库/文档为准（文档明确要求不假定未经验证的框架 API）。

## 架构（依据设计文档 v2.0）

### 两平面分离——最重要的边界

- **桌面控制面**（MyGo 窗口 + TS UI + controlapi）只做配置、状态、操作、查询；**绝不承载模型请求正文流量**。
- **模型数据面**（gateway → protocol → router → provider → 上游 Provider API）对 Agent 暴露统一模型 API。
- 网关生命周期独立于 UI：关闭窗口 ≠ 停止网关；停止必须走优雅收尾（拒绝新请求、宽限在途请求、刷事件队列、关 SQLite）。网关运行状态必须来自实际服务状态，不是桌面按钮状态。
- 核心业务包必须能在**无桌面 UI** 的测试环境中运行。

### 目标目录与依赖规则（文档 §12、§12.1，硬约束）

目标结构：`cmd/{agentswitch,agentswitch-gateway}`、`internal/{app,gateway,controlapi,protocol,provider,router,config,credential,storage,telemetry,desktop}`、`migrations/`、`tests/{fixtures,fakeprovider,integration}`；`frontend/` 不再建设（UI 决策 2026-10-17）。

- `router` 不依赖 MyGo、UI 或具体 HTTP handler；`provider` 通过接口暴露调用能力，不反向依赖 `router`。
- `gateway` 组合 protocol/router/provider，不自行实现每家供应商的错误判断；`controlapi` 只调应用服务，不绕过 Repository。
- SQLite（`storage`）与密钥存储（`credential`）通过接口注入，便于测试与平台适配。
- `desktop` 只负责生命周期与桥接，平台差异收敛在此层。

### 路由与故障转移不变量（文档 §6.3、§7；路由主体为逻辑模型）

- 路由主体是**逻辑模型目标链**（`logical_models`/`logical_model_targets`）：Agent 请求逻辑模型名，网关按目标 `priority` 升序跨「渠道·模型」切换；手动固定（Pinned）语义**必须**由用户显式选择“失败即停止”还是“允许回退”。
- 只有在**尚未向客户端提交响应头/任何流式事件前**才允许跨目标切换；流已开始输出后绝不静默重放（上游可能已计费）。
- 错误分类不能只凭 HTTP 状态码断定额度耗尽（需结合供应商错误码/响应体/文档）；参数错误 400 不做无意义重试；客户端取消不转移，直接取消上游。
- 冷却按 **ChannelID** 记账，状态并发更新必须原子（事务/锁/原子更新）；路由器真正发请求前要**二次校验**渠道状态。
- 每次路由决策输出可解释记录：被过滤候选及原因、最终目标、切换次数。
- 未登记模型名默认返回明确错误（「透传未登记模型」开关默认关，首版仅预留），避免静默错路由。

### 安全默认（文档 §8、§10）

- 模型 API 与管理 API 默认只监听 127.0.0.1，且使用**相互独立**的访问令牌。
- API Key 存 OS Keychain/Credential Manager，SQLite 只存 `credential_ref`；平台不支持时须显式安全降级，禁止默默明文落库。
- 日志、错误、UI、诊断包中禁止出现密钥、Authorization、Cookie、完整 Prompt（事件只记脱敏元数据）。
- 用户配置的 `base_url` 要做 SSRF/重定向/私网地址校验；配置导出默认脱敏。

### 存储与测试（文档 §8、§13）

- SQLite 存非秘密配置与状态：`channels`（**单 Key**，`credential_ref` 进 OS 钥匙串；`channel_keys` 已删除）+ `channel_models`（仅模型建议列表，非路由主体）+ `logical_models`/`logical_model_targets`（路由主体目标链）。docx §8/§13 转述保留 Provider/Account/ModelMapping/RoutePolicy 原文名。事件遥测沿用内存环 + 文件日志，**不落 SQLite**。经 Repository + `migrations/`。
- 测试以 **Fake Provider** 为主（稳定复现额度耗尽、429/Retry-After、5xx、流式中断），不依赖真实供应商；真实 Agent 兼容性验证只作受控测试，常规 CI 不打真实上游。
- 必测边界：首字节前可切换、首字节后不重放、客户端取消、并发 429 冷却原子性（`-race`，冷却键 ChannelID）、停止期间请求收尾；多逻辑模型并存互不串扰、目标顺序生效。

### MVP 实施顺序（文档 §14）

P0-A 核心网关（单协议端到端）→ P0-B 账号池与故障转移 → P0-C 持久化与安全 → P0-D MyGo 桌面端 → P0-E 打包验收。原则：先用 MyGo 官方模板验证最小桌面能力（窗口关闭行为、Go 调用桥接、资源打包），再大规模 UI；先做一个真实 Agent 的完整协议路径，再加第二种协议；MyGo 版本锁进仓库并记录到 README。

### 待决策事项（文档 §16）

- 网关单进程 vs 独立子进程**未定**——先做生命周期抽象，验证 MyGo 运行模型后再决定。
- MyGo 具体 API、托盘/开机启动等能力未经验证，以选定版本官方文档与仓库实现为准。
