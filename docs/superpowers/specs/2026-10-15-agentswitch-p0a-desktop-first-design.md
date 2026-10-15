# AgentSwitch P0-A′ 桌面优先 MVP 设计

- 日期：2026-10-15
- 状态：已批准的设计稿（经 brainstorming 逐节确认）
- 来源：`AgentSwitch_MyGo桌面应用重新设计方案_v2.0.docx`（权威架构文档，下称"v2.0 文档"）+ 本切片的用户决策
- 范围：首个实施切片 P0-A′（桌面优先）。本文只覆盖此切片；P0-B/C/E 见 §16。

## 1. 背景与切片定位

AgentSwitch 为本地 AI Agent（Claude Code 等）提供统一模型 API 网关：agent 永远指向 `127.0.0.1` 上的固定地址，供应商/账号/模型的配置与切换全部发生在网关层——对 agent 透明，**切换无需重启 agent**（对标 cc-switch 的配置体验，但切换从"改写 agent 配置文件"移到"网关内部路由配置"）。

v2.0 文档把实施分为 P0-A（核心网关）→ P0-B（账号池/故障转移）→ P0-C（持久化/安全）→ P0-D（桌面端）→ P0-E（打包）。本切片按用户决策**重排**：把持久化与桌面端提前，合并为"桌面优先 MVP"（P0-A′）。故障转移仍留在 P0-B。

## 2. 已确认决策（含对 v2.0 文档的修订）

| # | 决策 | 依据 |
|---|---|---|
| 1 | 首个协议：**Anthropic `/v1/messages`**，用 Claude Code 做真实端到端验收；OpenAI 兼容协议为紧随的下一切片 | 文档 §14.1"先一种协议端到端再加第二种" |
| 2 | 配置载体：**SQLite**（providers/accounts/models/settings），**不引入 YAML 配置文件** | 用户决策；原 P0-C 的 SQLite 提前到本切片 |
| 3 | 管理面：**桌面 UI 本身**，经 MyGo 桥接服务（`mygo.Bind`）调用 Go；**不做 CLI、不开放管理 HTTP 端口** | 用户决策；文档 §4.4 允许优先评估桥接方式 |
| 4 | UI 路线：**原生 UI**（MyGo `ui` 包，Go-only） | 实测资源对比（§3）；**v2.0 文档 §4.2 的 TS 前端约定自本切片起作废** |
| 5 | 进程模型：**单进程**（网关为应用进程内 goroutine） | spike 证实可行；**文档 §16 该开放决策在本切片收敛** |
| 6 | 代码结构：按需建包 + 接口先行（方案 3），零空壳包 | 文档 §12 目标结构与 §12.1 依赖规则 |
| 7 | 凭据：**`mygo.Secrets`（OS 密钥存储）为主**，无密钥环环境显式降级 | 文档 §8 凭据要求 |
| 8 | SQLite 驱动：`modernc.org/sqlite`（纯 Go，无 cgo） | 与 MyGo"纯 Go 跨平台编译"一致 |
| 9 | 验收上游：**凭据到位后选定**；设计不假设上游地址；此前全部验证用 Fake Provider | 用户决策；文档 §13 |

## 3. MyGo 技术验证结论（spike，2026-10-15，本机 Linux amd64）

`mygo init` → 依赖安装 → `bun run build` → 打包 → 真实起窗口，全链路一次通过。

- **版本**：mygo **v0.5.1**（模板 go.mod 钉死；CLI `mygo-cli@0.5.1`）；要求 **Go 1.27.2**（`GOTOOLCHAIN=auto` 经 goproxy.cn 自动下载已验证）；Bun/Vite 仅 Web 路线需要，本切片不用
- **产物**：9–13MB 无 cgo 独立二进制；Linux 打包含 .deb、tar.gz、install.sh、.desktop
- **桥接**：`mygo.Bind(服务)` 自动生成类型化 TS 客户端（本切片原生 UI 不需要客户端生成，类型化仍可用于原生侧调用约定）；方法每调用独立 goroutine 可阻塞；首参 `context.Context` 在窗口关闭/页面离开时取消；`NewEvent[T]` + `Emit/Broadcast` 推送状态；**仅本应用页面可调 Go 方法**（dev 构建额外允许 localhost）
- **生命周期**：`App.Run()` 占主线程；最后窗口关闭默认退出 App，**`OnWindowAllClosed` 加监听即可拦截**；托盘 `NewTray` 可用；`OnBeforeQuit`/`OnWillQuit` 可挂优雅停止；SIGINT/SIGTERM 走同一退出路径；`RequestSingleInstanceLock` 单实例；`App.Path(PathUserData)` → `~/.config/<App>`
- **凭据**：`mygo.Secrets`（macOS Keychain / Linux libsecret / Windows Credential Manager，单值 ≤2560 字节）；无密钥环环境返回可判定的 `ErrUnsupported`
- **UI 路线实测**（官方最小示例）：Web ≈ 410MB / 3 进程（主 187MB + WebKitWeb 170MB + WebKitNetwork 53MB）；原生 ≈ **154MB / 1 进程** → 选择原生
- spike 产物为一次性验证（/tmp 下），不入仓库

## 4. 交付范围

### 4.1 In scope

- 单二进制桌面应用（MyGo 原生 UI）：概览页（网关状态/地址/访问令牌/Claude Code 配置片段/最近请求）、供应商 CRUD+连通测试、账号 CRUD+启停+测试（密钥掩码）、模型别名 CRUD
- 网关核心：`POST /v1/messages`（流式+非流式）、`GET /v1/models`、`/healthz`、`/readyz`；单账号；模型别名映射；仅 127.0.0.1；Bearer 令牌
- SQLite 四张表 + migration 脚手架；凭据双后端（OS 密钥存储 + 显式降级）
- 结构化日志（脱敏）、审计日志行、内存环形请求记录（100 条）
- 测试：单元、Fake Provider 集成、桥接服务、原生视图测试

### 4.2 Out of scope（→ 后续切片）

- 故障转移/多账号候选/冷却状态机 → P0-B
- 请求与切换事件落库、诊断页、备份导出、OpenAI 协议 → P0-C/紧随切片
- 打包打磨、签名、自动更新、开机自启 UI 开关、图表 → P0-E/P1
- 自动改写 Claude Code `settings.json`（cc-switch 式写入外部工具配置）→ 未来单独评估的特性

## 5. 进程模型与生命周期

- `App.Run()` 主线程事件循环；网关 HTTP listener 与全部业务为进程内 goroutine
- 启动序：解析参数 → `mygo.Bind` 桥接服务 → `App.WhenReady`：打开 SQLite/执行迁移 → 首次运行生成网关令牌（crypto/rand 32 字节 hex，入 settings）→ 启动网关 listener → 创建主窗口（状态事件推送 UI）
- 关主窗口：`OnWindowAllClosed` 拦截 → 应用驻留托盘（`NewTray`：打开主窗口 / 退出）——**关窗 ≠ 停网关**
- 显式退出（托盘菜单、菜单项、SIGINT/SIGTERM）：`App.Quit` → `OnBeforeQuit` 钩子执行网关优雅停止（停止接收新请求 → 宽限在途请求，超时后取消上游 → flush 事件与日志 → 关闭 SQLite）→ `App.Run` 返回
- 单实例：`RequestSingleInstanceLock`，二次启动聚焦已有窗口
- 网关状态机（文档 §4.3）：`Stopped / Starting / Running / Degraded / Stopping / Failed`。无可用启用账号时为 `Degraded`（listener 正常但 `/readyz` 报告降级原因）；状态必须来自实际服务状态而非按钮状态

## 6. 包结构与依赖规则

```text
cmd/agentswitch/main.go   # 薄入口：flag 解析 → app.Run
internal/
  app/        # 组装与生命周期编排（启动序/优雅停止/状态机）
  desktop/    # MyGo 窗口/托盘/菜单/事件桥（平台差异收敛处）
  bridge/     # mygo.Bind 的服务实现（管理面）
  gateway/    # HTTP 模型 API + 中间件（令牌/限额/超时）
  protocol/   # anthropic：解析、别名映射、流式适配、错误 envelope
  provider/   # ProviderAdapter 接口 + AnthropicAdapter
  router/     # Router 接口 + SingleCandidate 实现（P0-B 换实现不换接口）
  account/    # 账号类型与状态
  config/     # 默认值与校验（settings 的读写在 storage）
  credential/ # CredentialStore 接口 + mygoSecrets 实现 + fallbackFile 实现
  storage/    # SQLite 连接、migrations、Repository（接口注入）
  telemetry/  # slog JSON 日志、脱敏、请求事件行、审计行
```

依赖规则（沿文档 §12.1）：`router` 不依赖 HTTP/MyGo/具体 adapter；`provider` 经接口暴露、不反向依赖 `router`；`gateway` 组合 protocol/router/provider；`bridge` 只调应用服务，不直接写 SQL；`desktop` 只做生命周期与桥接；storage 与 credential 经接口注入；核心包必须能在无窗口测试中运行。

go.mod：module 名保持 `AgentSwitch`；Go 升 **1.27.2**；新增依赖仅 `github.com/egoist/mygo v0.5.1` 与 `modernc.org/sqlite`（无 YAML 配置，不引入 yaml 库）。MyGo CLI 以 `go tool github.com/egoist/mygo/cmd/mygo`（版本钉死在 go.mod）使用。

## 7. 数据模型（SQLite）

库位置：`mygo.App.Path(PathUserData)/agentswitch.db`（即 `~/.config/AgentSwitch/agentswitch.db`），WAL 模式，文件权限 0600。

```sql
-- migration 001；时间戳统一 RFC3339 TEXT
CREATE TABLE providers (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, protocol TEXT NOT NULL,  -- P0-A 仅 'anthropic'
  base_url TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  allow_private_upstream INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE accounts (
  id TEXT PRIMARY KEY, provider_id TEXT NOT NULL REFERENCES providers(id),
  name TEXT NOT NULL, secret_key TEXT NOT NULL,   -- Secrets 键名，如 "account:<id>"；降级时为空
  secret_backend TEXT NOT NULL,                   -- 'os-keychain' | 'fallback-file'
  priority INTEGER NOT NULL DEFAULT 10, enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE models (
  id TEXT PRIMARY KEY, public_model TEXT NOT NULL UNIQUE,
  upstream_model TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
```

- 密钥本体不进数据库（§8）
- 单账号前提下 `models` 为全局别名表；P0-B 扩展归属列
- 网关每请求经 Repository 实时读配置（本地 SQLite 微秒级）→ UI 修改**即时生效**，无缓存失效逻辑
- Repository 以接口定义、实现放 `storage`，测试用临时库文件

## 8. 凭据存储

- 接口 `credential.Store`：`Set(ref, secret)` / `Get(ref)` / `Delete(ref)`
- 主实现 `mygoSecrets`：`mygo.Secrets` 封装，键名 `account:<id>`
- 降级实现 `fallbackFile`：`{PathUserData}/secrets-fallback.json`，0600，仅当 `mygo.Secrets` 返回可判定的 `ErrUnsupported` 时允许；用户在 UI 显式确认后启用，UI 常驻警示条
- `accounts.secret_backend` 记录来源；`secret_key` 仅存引用
- 密钥只在构造上游请求瞬间读取；不缓存、不进日志、不作为桥接方法返回（UI 回显尾 4 位）

## 9. 桥接服务 API（管理面）

经 `mygo.Bind` 暴露（仅本应用页面可调，无管理端口）：

- `Gateway.status() → {state, addr, token, uptimeMs, degradedReason, version}`；`start()`；`stop()`；`regenerateToken()`；`recentRequests() → [100 条内存环形缓冲]`
- `Providers.list/create/update/remove/test(id)`——test 为分级探测（DNS → TCP/TLS → 鉴权），错误信息脱敏
- `Accounts.list/create/update/remove/setEnabled/test(id)`
- `Models.list/create/update/remove`
- 事件：`GatewayStateChanged`、`RequestCompleted`（`mygo.NewEvent`，Broadcast）
- 所有变更操作写审计日志行（telemetry；落库随 P0-C）
- 错误经桥接层统一为对用户可读的信息，不含密钥与请求体

## 10. 模型 API 与请求流水线

端点：`POST /v1/messages`、`GET /v1/models`（公开别名列表）、`GET /healthz`（免令牌）、`GET /readyz`（配置/存储/监听就绪 + 降级详情）。

单次请求：

1. 中间件：请求体上限（10MB）→ 并发上限（32）→ Bearer 令牌常量时间比较 → 生成 `request_id` 与计时
2. `protocol/anthropic` 解析：`model`、`stream`、必填字段；错误统一为 Anthropic 错误 envelope（`{"type":"error","error":{...}}`）
3. 别名映射：`models` 表 public → upstream；未命中返回模型不存在错误并列出可用别名
4. `router.Resolve` → SingleCandidate 决策记录（含"为何选中"；P0-B 换实现）
5. `credential.Get` 取密钥 → `AnthropicAdapter` 构造上游请求：Base URL 拼接、重建 `x-api-key` 与 `anthropic-version` 头、**头白名单（本地令牌绝不发给上游）**、传播客户端取消（ctx）与超时（默认 120s）
6. 响应：非流式整体转发；流式逐块读写并及时 flush
7. 结束：telemetry 记事件行（request_id、模型、账号、状态、耗时、错误类别、failover_count=0、上游返回的 usage）并广播 `RequestCompleted`

流式安全边界（文档 §6.3，P0-A 即落地）：写给客户端首字节**之前**出错 → 返回 JSON 错误；**之后**出错 → 结束流并记事件，不重放。

错误分类器（只记录、不转移；reason_code 供 P0-B 消费）：401/403 凭据、429 限流（读 Retry-After）、5xx 端点、400 参数、连接失败/响应前超时。

## 11. 安全默认

- 监听地址代码写死 127.0.0.1，settings 无 host 字段
- 令牌：首启生成入 settings；UI 展示/复制/重新生成；模型 API 全路由校验；仅 healthz/readyz 免令牌
- 上游：非回环 base_url 强制 https；HTTP client 显式不跟随重定向；私网/回环上游须 `allow_private_upstream=true`
- 限额：请求体 10MB、并发 32、请求超时 120s、关闭宽限 15s（超时/端口 settings 可调，改端口 UI 提示重启）
- 脱敏：`Authorization`/`x-api-key`/`Cookie` 掩码；请求/响应体不落日志；审计不含秘密值
- Claude Code 配置片段（`ANTHROPIC_BASE_URL` + `ANTHROPIC_AUTH_TOKEN`）由概览页一键复制；**不自动改写任何外部工具配置文件**

## 12. 桌面 UI（原生）

MyGo `ui` 包实现，全部 Go；视图函数无窗口可测（官方 `main_test.go` 模式）。

- 概览页：状态徽标（六态）、监听地址、令牌展示/复制/重生成、Claude Code 配置片段复制、启动/停止按钮（Starting/Stopping 时禁用重复操作）、最近请求表、降级/失败原因与日志路径提示
- 供应商页：列表 + 表单（name/protocol 仅 anthropic/base_url/enabled/allow_private_upstream）+ 测试按钮 + 删除确认
- 账号页：列表（所属供应商、优先级、启用开关、密钥尾 4 位）+ 表单（密钥写入走 credential.Store，不回显全文）+ 测试 + 删除确认
- 模型页：别名表 CRUD
- 设置：端口、请求超时（改动提示是否重启网关）
- 危险操作（删除、重新生成令牌）二次确认；所有耗时操作显示进行中状态、防重复提交

## 13. 日志与可观测性

- `log/slog` JSON 结构化：`{ts, level, msg, request_id, ...}`；文件落 `App.Path(PathLogs)`
- 请求事件行（§10 第 7 步字段）；审计行（actor=ui, action, target, result）
- P0-A 最近请求仅内存环形缓冲 + 日志；落库随 P0-C

## 14. 测试策略

全部离线可跑（Fake Provider），CI 不触真实上游。

- 单元：别名映射、错误分类器、settings 校验与默认值、降级判定、migration runner
- Fake Provider 集成（httptest）：200 非流式；200 流式分块+flush；401；429+Retry-After；5xx；连接拒绝；响应头慢→超时；断言上游收到的请求**不含本地令牌**、白名单外头被剔除；客户端取消 → 上游 ctx 同步取消
- 桥接服务：无窗口直接调用服务方法 + 临时 SQLite 覆盖 CRUD 与校验
- 原生视图：`main_test.go` 模式渲染断言
- 并发：并发上限生效、优雅停止期间请求收尾

## 15. 验收清单

凭据到位后执行（上游待用户选定，设计不依赖具体供应商）：

1. UI 添加供应商 + 账号（密钥）+ 模型别名 → 复制配置片段到 Claude Code 环境 → **真实模型调用成功**（流式与非流式各一次）
2. **切换验收**：会话进行中在 UI 修改别名映射或账号 → 下一次请求即走新配置 → Claude Code 全程未重启
3. 关闭主窗口 → 网关继续服务（curl `/healthz` 验证）→ 托盘"打开主窗口"可返回
4. 托盘退出 → 在途请求优雅收尾（宽限期内完成），进程干净退出
5. 日志检查：无明文密钥/Authorization/完整请求体
6. 降级验证：临时禁用环境密钥环 → fallback 路径可用且 UI 警示条出现

## 16. 后续切片映射

- **P0-B**：多账号池、优先级排序、错误分类驱动的故障转移、冷却状态机（原子更新）、Router 完整实现（接口已就位）；`models` 归属扩展
- **P0-C**：请求/切换事件落库、AuditEvent 表、诊断页（文档 §11.2）、配置导出脱敏、SSRF 精细策略
- **紧随切片**：OpenAI 兼容协议（protocol/provider 各加一个实现）
- **P0-E**：打包安装验证（spike 已证实 .deb/install.sh 可产出）、升级路径、基础文档

## 17. 风险与开放事项

| 事项 | 状态 | 处理 |
|---|---|---|
| 真实验收上游与凭据 | 开放 | 用户在验收环节选定；不影响其余验收项 |
| mygo v0.5.1 年轻，原生 `ui` 生态小 | 已知风险 | spike 已验证构建/运行/打包；视图可测；锁定版本降低漂移 |
| Linux 无密钥环环境（服务器/最小桌面） | 已知 | fallbackFile 降级 + UI 警示（§8） |
| 关窗驻留托盘后"应用消失"困惑 | 已知 | 首次关闭弹一次说明（"已驻留托盘，网关继续运行"） |
| 网关端口被占用 | 已知 | 状态 Failed + UI 展示脱敏错误与处理建议（文档 §5.2 区分失败原因） |
| 自动写入 Claude Code 配置 | 暂不做 | 未来作为独立特性单独评估 |
