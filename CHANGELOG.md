# Changelog

本项目的显著变更记录。格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)，版本遵循 [SemVer](https://semver.org/lang/zh-CN/)。

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
