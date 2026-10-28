# Changelog

本项目的显著变更记录。格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本遵循 [SemVer](https://semver.org/lang/zh-CN/)。

## [Unreleased]

### 新增

- 渠道页「拉取模型」：调用上游 `/v1/models` 自动填充建议模型列表，不必手填。openai / anthropic 两协议分头鉴权，支持 `has_more`/`next_cursor`/`after_id` 分页续拉（上限 10 页 / 500 条）、跨页按 id 去重、失败时保留已填内容；详情页与新建渠道表单均可用

- Codex 接入支持桌面版：接入页页签改为「Codex」（CLI / 桌面版 / IDE 插件共用 `~/.codex/config.toml`）；第三方 Key 改写入 `[model_providers.agentswitch].experimental_bearer_token`（Codex 0.149+ 兼容），`auth.json` 只保留官方 ChatGPT 登录、接入配置不再改写；「应用」时用 Codex 自带模型模板生成 `~/.codex/agentswitch-model-catalog.json` 并写 `model_catalog_json`，桌面版模型选择器只显示 AgentSwitch 逻辑模型；应用后需完全退出 Codex 再重新打开，旧对话不迁移、请新建对话
- Codex 接入支持有序逻辑模型列表（第一项为默认，可在 Codex 中切换）与逻辑模型元数据（显示名、上下文窗口、思考等级/默认等级）；模型目录按显式列表与元数据生成，默认模型上下文窗口驱动 `model_context_window` / `model_auto_compact_token_limit`，高级设置写 `[agents]` 子代理模型/推理强度与目录 `auto_review_model_override`，并固定 `disable_response_storage = true`、`web_search = "disabled"`
- Codex 接入页新增只读的完整 `config.toml` 预览与只读模型元数据列表（不可编辑；用户其他键原样保留）
- 存储 migration v2：`logical_models` 增加元数据列，纯增列非破坏性，旧库就地升级、无需删库
- Windows 支持（10/11 x64）：`go tool mygo build -platform windows/amd64` 产出 exe + NSIS 安装包；接入配置在 Windows 生成 `.cmd` 启动器，启动命令为 `cmd /c call "<launcher>.cmd"`（cmd/PowerShell 通用），「打开终端」走 `cmd /c start "" "<launcher>.cmd"`
- Claude Desktop 配置路径支持 Windows：`%LOCALAPPDATA%\Claude` / `Claude-3p`（镜像 cc-switch 的目录选择规则）
- CI 新增 `windows-latest` 打包 job 与 Ubuntu 上的 `GOOS=windows` 交叉编译检查

### 已知限制

- Windows 构建与打包已通过，尚未在 Windows 实机运行验证

## [0.1.0] - 2026-10-24

首个开源版本。

### 新增

- 逻辑模型路由：有序目标链跨「渠道·模型」优先级故障转移（首字节前可切换、首字节后不重放、400 不重试、客户端取消不转移）
- 三入站网关：Anthropic `POST /v1/messages`、OpenAI Chat `POST /v1/chat/completions`、OpenAI Responses `POST /v1/responses`（Codex CLI 0.143+），与上游渠道协议自动互转
- 渠道管理：20 条双协议预置（14 家按量 + 6 家 Coding Plan 订阅，官网核实）；`~/.agentswitch/presets.json` 用户覆盖/追加
- 接入配置：Claude Code / Codex CLI / Claude Desktop 一键应用（只改受管键、写前备份、令牌轮换自动刷新派生文件）
- 密钥安全：AES-256-GCM 加密落库、master.key 分离、SSRF/私网校验、日志零密钥
- 冷却与可解释路由：按 ChannelID 原子记账、请求前二次校验、候选过滤原因
- 流式健壮性：上游中断/空闲超时向客户端发 error 事件，残缺工具参数不以空 input 下发
- 出站透传客户端 User-Agent（订阅供应商条款合规）

### 已知限制

- Windows 未支持；Apple Silicon 打包未验证
- UI 为中文（国际化在路线图中）
- 破坏性 schema 变更走删库重建（不就地迁移）
