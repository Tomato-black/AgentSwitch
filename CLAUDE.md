# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目状态与权威文档

AgentSwitch：面向本地 AI Agent 的**模型 API 网关**——为 Claude Code / Codex CLI 等工具提供统一的本地模型入口，集中管理多供应商/多账号，支持手动指定账号与有限、安全的自动故障转移。

技术选型（v2.0 设计稿确定）：Go 实现网关核心，**MyGo**（https://github.com/egoist/mygo，桌面框架）做桌面壳与前后端桥接，前端 TypeScript + HTML/CSS，数据用 SQLite，凭据存 OS 密钥存储。

当前仓库仅为 GoLand 初始骨架：`go.mod`（module `AgentSwitch`，Go 1.25，无第三方依赖）+ `main.go`（GoLand 模板示例，将被设计文档 §12 的目录结构取代）。尚无 README、测试、前端工程。

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
go test ./internal/router -run TestFailover   # 跑单个测试
go test -race ./...   # 涉及并发/冷却状态的测试必须带 -race
go tool mygo dev      # 开发运行（热重载；入口 cmd/agentswitch，UI 在 internal/desktop）
go tool mygo build    # 打包（产物在 build/）
```

前端（`frontend/` + `mygo.config.ts`，见文档 §12）尚未建立，npm/pnpm 命令待引入后再补充，不要臆造。MyGo 项目模板与打包命令以选定版本的官方仓库/文档为准（文档明确要求不假定未经验证的框架 API）。

## 架构（依据设计文档 v2.0）

### 两平面分离——最重要的边界

- **桌面控制面**（MyGo 窗口 + TS UI + controlapi）只做配置、状态、操作、查询；**绝不承载模型请求正文流量**。
- **模型数据面**（gateway → protocol → router → provider → 上游 Provider API）对 Agent 暴露统一模型 API。
- 网关生命周期独立于 UI：关闭窗口 ≠ 停止网关；停止必须走优雅收尾（拒绝新请求、宽限在途请求、刷事件队列、关 SQLite）。网关运行状态必须来自实际服务状态，不是桌面按钮状态。
- 核心业务包必须能在**无桌面 UI** 的测试环境中运行。

### 目标目录与依赖规则（文档 §12、§12.1，硬约束）

目标结构：`cmd/{agentswitch,agentswitch-gateway}`、`internal/{app,gateway,controlapi,protocol,provider,router,account,config,credential,storage,telemetry,desktop}`、`frontend/`、`migrations/`、`tests/{fixtures,fakeprovider,integration}`。

- `router` 不依赖 MyGo、UI 或具体 HTTP handler；`provider` 通过接口暴露调用能力，不反向依赖 `router`。
- `gateway` 组合 protocol/router/provider，不自行实现每家供应商的错误判断；`controlapi` 只调应用服务，不绕过 Repository。
- SQLite（`storage`）与密钥存储（`credential`）通过接口注入，便于测试与平台适配。
- `desktop` 只负责生命周期与桥接，平台差异收敛在此层。

### 路由与故障转移不变量（文档 §6.3、§7）

- 默认策略 Priority + Failover；手动固定（Pinned）账号**必须**由用户显式选择“失败即停止”还是“允许回退”。
- 只有在**尚未向客户端提交响应头/任何流式事件前**才允许跨账号切换；流已开始输出后绝不静默重放（上游可能已计费）。
- 错误分类不能只凭 HTTP 状态码断定额度耗尽（需结合供应商错误码/响应体/文档）；参数错误 400 不做无意义重试；客户端取消不转移，直接取消上游。
- 冷却状态并发更新必须原子（事务/锁/原子更新）；路由器真正发请求前要**二次校验**账号状态。
- 每次路由决策输出可解释记录：被过滤候选及原因、最终目标、切换次数。

### 安全默认（文档 §8、§10）

- 模型 API 与管理 API 默认只监听 127.0.0.1，且使用**相互独立**的访问令牌。
- API Key 存 OS Keychain/Credential Manager，SQLite 只存 `credential_ref`；平台不支持时须显式安全降级，禁止默默明文落库。
- 日志、错误、UI、诊断包中禁止出现密钥、Authorization、Cookie、完整 Prompt（事件只记脱敏元数据）。
- 用户配置的 `base_url` 要做 SSRF/重定向/私网地址校验；配置导出默认脱敏。

### 存储与测试（文档 §8、§13）

- SQLite 存非秘密配置与状态（Provider/Account/ModelMapping/RoutePolicy + RequestEvent/SwitchEvent/AuditEvent），经 Repository + `migrations/`。
- 测试以 **Fake Provider** 为主（稳定复现额度耗尽、429/Retry-After、5xx、流式中断），不依赖真实供应商；真实 Agent 兼容性验证只作受控测试，常规 CI 不打真实上游。
- 必测边界：首字节前可切换、首字节后不重放、客户端取消、并发 429 冷却原子性、停止期间请求收尾。

### MVP 实施顺序（文档 §14）

P0-A 核心网关（单协议端到端）→ P0-B 账号池与故障转移 → P0-C 持久化与安全 → P0-D MyGo 桌面端 → P0-E 打包验收。原则：先用 MyGo 官方模板验证最小桌面能力（窗口关闭行为、Go 调用桥接、资源打包），再大规模 UI；先做一个真实 Agent 的完整协议路径，再加第二种协议；MyGo 版本锁进仓库并记录到 README。

### 待决策事项（文档 §16）

- 网关单进程 vs 独立子进程**未定**——先做生命周期抽象，验证 MyGo 运行模型后再决定。
- MyGo 具体 API、托盘/开机启动等能力未经验证，以选定版本官方文档与仓库实现为准。
