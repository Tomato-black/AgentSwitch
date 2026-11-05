# AgentSwitch

[![CI](https://github.com/Tomato-black/AgentSwitch/actions/workflows/ci.yml/badge.svg)](https://github.com/Tomato-black/AgentSwitch/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#平台支持)

[官网](https://tomato-black.github.io/AgentSwitch/) · [使用手册](docs/manual/README.md) · [Releases](../../releases) · [Discussions](../../discussions)

**本地 AI Agent 模型网关**——为 Claude Code / Codex（CLI / 桌面版）/ Claude Desktop 等本地 Agent 提供统一的模型入口：集中管理多供应商渠道与密钥，按「逻辑模型」做优先级故障转移，Agent 无需重启即可切换上游。Go 单二进制 + MyGo 原生 UI 桌面应用。

```
Claude Code ─┐                                   ┌─→ DeepSeek（按量）
Codex       ──┼─→ AgentSwitch 网关（127.0.0.1） ──┼─→ 智谱 GLM Coding Plan
Claude Desktop ┘     │ 逻辑模型 · 冷却 · 故障转移  └─→ Kimi Code（订阅）…
```

## 与 cc-switch 的区别

[cc-switch](https://github.com/farion1231/cc-switch) 是**配置切换器**——改写 Agent 的配置文件在多个供应商间手动/自动切换，任一时刻只有一个生效。AgentSwitch 是**本地网关**——Agent 永远只指向 `127.0.0.1:17890`，上游切换、多渠道故障转移、协议转换都在网关内完成，对 Agent 完全透明、即时生效。两者可以互补，也可以只取其一。

## 功能特性

- **逻辑模型路由**：Agent 请求 `model=my-code`，网关按目标链有序尝试（渠道 + 模型），失败自动切换下一目标（仅在响应尚未提交前，绝不重放已计费请求）
- **逻辑模型测试**：在逻辑模型列表直接按目标链发送最小请求，显示每个目标的成功/失败、耗时、错误分类与下一步；不修改冷却或请求统计（会消耗少量额度）
- **首次上手与搜索**：空库默认落到渠道页；「渠道 → 逻辑模型 → 接入」三步清单完成后自动消失；渠道/逻辑模型/接入配置与预设均支持搜索
- **三入站协议**：Anthropic `POST /v1/messages`、OpenAI Chat `POST /v1/chat/completions`、OpenAI Responses `POST /v1/responses`——与上游渠道协议自动互转
- **20 条双协议预置**：14 家按量供应商 + 6 家 Coding Plan 订阅（智谱 GLM / 豆包 / Kimi / 百炼 / 阶跃 Step Plan / 小米 Token Plan），每家 openai/anthropic 双端点，选协议自动带出 base_url 与模型建议；预置数据 2026-10 逐家按官网核实
- **用户自定义预置**：`~/.agentswitch/presets.json` 覆盖/追加内置预置（自托管中转站 http/私网地址放行）
- **渠道复制与主备链**：列表/详情一键复制渠道（保留 Base URL、协议、建议模型与启停设置），默认不复制 Key；副本自动加入引用源渠道的逻辑模型目标链尾部，填写新 Key 后即形成主备链
- **模型自动拉取**：渠道页「拉取模型」调用上游模型列表端点自动填充建议模型，不必手填（openai / anthropic 两协议分头鉴权；base 已带 `/v1` 等版本段时不重复补、命中 Anthropic 兼容子路径时回退重试，避免 `…/v1/v1/models` 404；支持分页续拉与跨页去重、SSRF 校验）
- **密钥安全**：API Key 以 AES-256-GCM 加密存 SQLite，主密钥 `master.key`（0600）与库分离；明文永不落盘、落日志
- **接入管理**：一键把网关地址/令牌/模型写入 Claude Code（`~/.claude/settings.json`）、Codex（`~/.codex/config.toml`，CLI / 桌面版 / IDE 插件共用；Key 写 `experimental_bearer_token`，不改 `auth.json`，并生成逻辑模型目录供桌面版选择器使用）、Claude Desktop 配置——只改受管键、写前自动备份；支持多套配置与启动命令
- **Codex 多模型与元数据**：每份 Codex 接入配置维护有序逻辑模型列表（第一项为默认），并可在接入页按配置编辑显示名、上下文窗口、思考等级（未填回退逻辑模型库值）；应用时据此生成 Codex 模型目录，写入 1M 上下文/自动压缩阈值、子代理与自动审批设置，并提供只读的完整 `config.toml` 预览
- **冷却与可解释路由**：上游 429/5xx 按渠道冷却（原子记账、发请求前二次校验），每次路由决策有候选过滤原因可查
- **请求历史持久化**：最近请求与尝试链落 SQLite（默认保留 7 天，仅脱敏元数据），重启后仍可筛选查看，并可导出诊断 JSON（不含密钥/prompt 正文）
- **用量与成本估算**：非流式与流式请求的输入/输出 token 均落库（OpenAI Chat 流自动注入 `stream_options.include_usage`），设置页按渠道/模型/日聚合；提供「每日 Token」「按渠道 token」柱状图与「按渠道响应时长」折线图；可编辑价格表（美元/1M token）估算成本，明确标注不是账单
- **配额与上游余额**：设置页可按渠道配置本地月度 token/美元配额（按 `reset_day` 重置，本地估算）；渠道详情可查询 DeepSeek / Kimi / OpenRouter 上游余额（OpenRouter 需 Management Key，其余供应商明确提示不支持）
- **故障告警**：全渠道冷却、连续故障转移、上游额度耗尽时给出应用内 + 系统通知（5–10 分钟去重），设置页可关闭；系统通知不可用时仅侧栏提示
- **半开恢复**：冷却到期只放行一个探测请求，成功即清除冷却、失败重新冷却；30 秒无结果允许再次探测，避免渠道永久半开
- **变更可见性**：接入编辑器显示「原配置 vs 当前编辑」的键级变更（Token/Key 等敏感值遮蔽）；设置页可一键打开日志目录
- **配置导出/导入**：设置页「数据」卡导出渠道、逻辑模型、接入配置与全局 env 的版本化 JSON；默认不含渠道 Key，导入前预览冲突、只新增不覆盖，导入后渠道需重新填写 Key
- **零遥测上报**：所有遥测（请求环 + 轮转文件日志）仅存本机

## 安装

### macOS（已验证）

从 [Releases](../../releases) 下载 dmg，或源码构建：

```bash
go tool mygo build   # 产物在 build/darwin-amd64/（.app + dmg）
```

### Linux

```bash
go tool mygo build   # 产物在 build/linux-amd64/（含 .deb 与 install.sh）
sh build/linux-amd64/install.sh && sh scripts/linux-desktop.sh
```

依赖 GTK3（无需 WebKitGTK）。已知问题：亮色主题下文字发白时应用已自动启用灰度抗锯齿规避（仅本进程，不改系统设置），详见[开发说明](#已知问题linux)。

### Windows

```bash
go tool mygo build -platform windows/amd64
# 产物在 build/windows-amd64/：AgentSwitch.exe + "AgentSwitch Setup <版本> amd64.exe"
```

安装包按用户安装到 `%LOCALAPPDATA%\Programs`，无需管理员权限；未签名，首次运行 Windows SmartScreen 会提示。也可直接运行便携版 `AgentSwitch.exe`（`icon.png` 需与其同目录）。Windows 下的 Claude Desktop 配置写入 `%LOCALAPPDATA%\Claude` / `Claude-3p`（与 cc-switch 一致）；非默认接入配置通过生成的 `.cmd` 启动器复制命令或一键打开终端。

## 快速上手

1. 启动应用 → 网关自动运行（侧栏底条 LED 可停/重启）
2. **渠道**页：预设网格选一家（自动带出双协议 base_url 与模型建议；订阅用户选对应的 Coding Plan 预置）或自定义；每渠道填**一个** API Key
3. **逻辑模型**页：新建（名字即 Agent 请求的模型名，如 `my-code`）→ 挂有序目标链（渠道 + 该渠道上的模型名，越靠前越优先）；显示名、上下文窗口、思考等级在**接入页 Codex 编辑器**中按配置维护（仅影响 Codex 模型菜单）
4. **接入**页：为 Claude Code / Codex / Claude Desktop 新建配置并点「应用」——Claude 绑定一个逻辑模型，Codex 维护有序模型列表（第一项为默认，可在 Codex 中切换）；应用直接写入 Agent 真实配置文件，下次启动生效（Codex 桌面版需完全退出后重新打开，并新建对话）
5. 发起真实请求；此后改目标链/密钥/启停即时生效，**无需重启 agent**

## 接入详情

「接入」页为每个 Agent 管理**全局配置**（env 键值）与任意多份**实际配置**（Claude 绑定单个逻辑模型；Codex 维护有序逻辑模型列表，第一项为默认）。「仅保存」只把配置保存到 AgentSwitch，不会改写 Agent 的配置文件；点「应用」才会把受管键写入 Agent 文件，写入前自动备份到 `~/.agentswitch/backups/`（每 Agent 最近 10 份）。当前配置保存后尚未应用时，列表会显示「待应用」。接入页「备份与恢复」可查看历史备份（时间/文件/大小）并选择任意一份还原，还原前会再快照当前状态。

| Agent | 写入文件 | 流量入站 |
|-------|---------|---------|
| Claude Code | `~/.claude/settings.json`（env） | `/v1/messages` |
| Codex（CLI / 桌面版） | `~/.codex/config.toml`（`wire_api="responses"`、`experimental_bearer_token`、`model_catalog_json`、`disable_response_storage`、`web_search`、窗口/压缩阈值、`[agents]` 子代理）+ `~/.codex/agentswitch-model-catalog.json`；`auth.json` 不改写 | `/v1/responses` |
| Claude Desktop | `~/.config/Claude/claude_desktop_config.json`（inferenceGateway profile + 角色别名） | `/v1/messages` |
| 连接指南（第三方 Agent） | 不写入文件；展示网关地址、三协议端点、Bearer 认证与启用模型，供人工填写 | 三入站均可 |

Codex 接入页还提供只读的**完整 `config.toml` 预览**（应用后的受管键 + 用户原有键）与**模型元数据编辑**（显示名、上下文窗口、思考等级；按配置覆盖，保存并「应用」后生效）。Codex 桌面版的模型选择器只读 `~/.codex/agentswitch-model-catalog.json`（「应用」时用 Codex 自带模型模板生成，目录条目来自有序模型列表与模型元数据）；旧对话保留创建时的 provider，重新应用后请新建对话。

非默认配置生成持久文件（`~/.agentswitch/access/…`，令牌轮换自动刷新）：复制启动命令或一键「打开终端」以该配置启动，不动已应用的默认配置。

## 安全设计

- 模型 API 仅监听 `127.0.0.1`，独立访问令牌鉴权；网关重启或令牌轮换后所有派生配置自动刷新
- 渠道 base_url 做 SSRF/重定向/私网校验（渠道级 `allowPrivate` 显式放行）；上游不跟随重定向
- 日志/错误/UI 禁止出现密钥、Authorization、完整 Prompt（遥测只记脱敏元数据）
- 出站透传客户端 User-Agent（部分订阅供应商条款要求）

详见 [SECURITY.md](SECURITY.md)。

## 平台支持

| 平台 | 状态 |
|------|------|
| macOS（Intel） | ✅ 已验证（打包 + 真实 Agent） |
| macOS（Apple Silicon） | 🚧 未打包验证（理论兼容，欢迎反馈） |
| Linux（GTK3） | ✅ 已验证（含字体渲染规避） |
| Windows（10/11 x64） | 🚧 构建与打包通过（exe + NSIS 安装包），路径/启动命令/终端已适配；待实机验证 |

## 开发

```bash
go build ./...        # 编译
go vet ./...          # 静态检查
go test ./...         # 全部测试（离线，Fake Provider，不打真实上游）
go test -race ./...   # 并发相关必带 -race
go tool mygo dev      # 开发运行（热重载）
go tool mygo build    # 打包
```

- Go 1.27+；UI 为 MyGo（v0.6.2）原生视图，无前端工程
- 设计文档：`docs/superpowers/specs/`（按日期命名，功能实现以 spec 为准）
- 贡献指南：[CONTRIBUTING.md](CONTRIBUTING.md)；架构概览：[ARCHITECTURE.md](ARCHITECTURE.md)

### 已知问题（Linux）

**亮色主题下窗口文字全白**：MyGo v0.5.1 起 Linux 子像素抗锯齿（`font-antialiasing=rgba`）下的字形着色有缺陷——文字颜色不生效、一律画成白色。应用启动时对本进程把 GTK 的 `gtk-xft-rgba` 设为 `none`（`internal/desktop/fontrender_linux.go`）改用灰度抗锯齿绕开，**不影响系统设置与其他应用**。（v0.6.2 复查上游无对口修复，规避保留）

## 从旧版本升级（多 Key / 渠道级路由旧库）

**删库重建**，不提供就地迁移。若启动报「检测到旧版多 Key 数据库（存在 channel_keys 表）」，删除一次 `~/Library/Application Support/AgentSwitch/agentswitch.db`（Linux：`~/.config/AgentSwitch/`）后重启：库按新 schema 重建，渠道/逻辑模型需重新配置。不会静默清空你的库。

**例外（2026-10-27，migration v2）**：逻辑模型元数据（显示名、上下文窗口、思考等级）为纯增列，旧库启动时**就地升级、无需删库**，已有渠道/逻辑模型/接入配置保留。

## License

[MIT](LICENSE)。桌面框架 [MyGo](https://github.com/egoist/mygo)（MIT）与 modernc.org/sqlite 等依赖见 `go.mod`。
