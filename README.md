# AgentSwitch

[![CI](https://img.shields.io/badge/CI-github--actions-blue)](.github/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Platform](https://img.shields.io/badge/platform-macOS%20%7C%20Linux%20%7C%20Windows-lightgrey)](#平台支持)

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
- **三入站协议**：Anthropic `POST /v1/messages`、OpenAI Chat `POST /v1/chat/completions`、OpenAI Responses `POST /v1/responses`——与上游渠道协议自动互转
- **20 条双协议预置**：14 家按量供应商 + 6 家 Coding Plan 订阅（智谱 GLM / 豆包 / Kimi / 百炼 / 阶跃 Step Plan / 小米 Token Plan），每家 openai/anthropic 双端点，选协议自动带出 base_url 与模型建议；预置数据 2026-10 逐家按官网核实
- **用户自定义预置**：`~/.agentswitch/presets.json` 覆盖/追加内置预置（自托管中转站 http/私网地址放行）
- **密钥安全**：API Key 以 AES-256-GCM 加密存 SQLite，主密钥 `master.key`（0600）与库分离；明文永不落盘、落日志
- **接入管理**：一键把网关地址/令牌/模型写入 Claude Code（`~/.claude/settings.json`）、Codex（`~/.codex/config.toml`，CLI / 桌面版 / IDE 插件共用；Key 写 `experimental_bearer_token`，不改 `auth.json`，并生成逻辑模型目录供桌面版选择器使用）、Claude Desktop 配置——只改受管键、写前自动备份；支持多套配置与启动命令
- **冷却与可解释路由**：上游 429/5xx 按渠道冷却（原子记账、发请求前二次校验），每次路由决策有候选过滤原因可查
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
3. **逻辑模型**页：新建（名字即 Agent 请求的模型名，如 `my-code`）→ 挂有序目标链（渠道 + 该渠道上的模型名，越靠前越优先）
4. **接入**页：为 Claude Code / Codex / Claude Desktop 新建配置（绑定逻辑模型）并点「应用」——直接写入 Agent 真实配置文件，下次启动生效（Codex 桌面版需完全退出后重新打开，并新建对话）
5. 发起真实请求；此后改目标链/密钥/启停即时生效，**无需重启 agent**

## 接入详情

「接入」页为每个 Agent 管理**全局配置**（env 键值）与任意多份**实际配置**（绑定逻辑模型）。「应用」只改受管键、其余保留，写入前备份到 `~/.agentswitch/backups/`（每 Agent 最近 10 份）。

| Agent | 写入文件 | 流量入站 |
|-------|---------|---------|
| Claude Code | `~/.claude/settings.json`（env） | `/v1/messages` |
| Codex（CLI / 桌面版） | `~/.codex/config.toml`（`wire_api="responses"`、`experimental_bearer_token`、`model_catalog_json`）+ `~/.codex/agentswitch-model-catalog.json`；`auth.json` 不改写 | `/v1/responses` |
| Claude Desktop | `~/.config/Claude/claude_desktop_config.json`（inferenceGateway profile + 角色别名） | `/v1/messages` |

Codex 桌面版的模型选择器只读 `~/.codex/agentswitch-model-catalog.json`（「应用」时用 Codex 自带模型模板生成）；旧对话保留创建时的 provider，重新应用后请新建对话。

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

## License

[MIT](LICENSE)。桌面框架 [MyGo](https://github.com/egoist/mygo)（MIT）与 modernc.org/sqlite 等依赖见 `go.mod`。
