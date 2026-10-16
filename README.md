# AgentSwitch

本地 AI Agent 模型网关 + 桌面控制面（MyGo 原生 UI，单二进制）。
Agent 指向 `127.0.0.1:17890`，供应商/账号/模型切换在网关层完成，agent 无需重启。

设计文档：`docs/superpowers/specs/2026-10-15-agentswitch-p0a-desktop-first-design.md`
架构文档（v2.0）：仓库根目录 docx。

## 开发

- 依赖：Go 1.27+（`GOTOOLCHAIN=auto` 会自动拉取）、原生 UI 在 Linux 仅需 GTK3，不需要 WebKitGTK
- 运行（开发模式，热重载）：`go tool mygo dev`
- 打包：`go tool mygo build`（产物在 `build/`，含 .deb 与 install.sh）
- 测试：`go test ./...`（全部离线，Fake Provider）

## 首次使用

1. 启动应用 → 网关自动运行（概览页可停/重启）；供应商页添加上游（Base URL + 协议 anthropic）
2. 账号页添加 API Key（优先系统密钥环；无密钥环环境勾选"允许文件降级"）
3. 模型页添加别名（公开模型名 → 上游模型名）
4. 概览页复制"Claude Code 配置片段"到 shell 环境（网关已随应用自动启动）
5. 用 Claude Code 发起真实请求；在 UI 改别名/账号即时生效，无需重启 agent

## 验收清单（对应 spec §15）

见 spec §15；核心自动化回归：`go test ./internal/app -run TestConfigChangeTakesEffectWithoutRestart`
