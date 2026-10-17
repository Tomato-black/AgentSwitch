# AgentSwitch

本地 AI Agent 模型网关 + 桌面控制面（MyGo 原生 UI，单二进制）。
Agent 指向 `127.0.0.1:17890`，渠道/密钥/模型切换在网关层完成，agent 无需重启。

设计文档：`docs/superpowers/specs/2026-10-15-agentswitch-p0a-desktop-first-design.md`
渠道化设计：`docs/superpowers/specs/2026-10-17-channels-models-p0b-design.md`
架构文档（v2.0）：仓库根目录 docx。

## 开发

- 依赖：Go 1.27+（`GOTOOLCHAIN=auto` 会自动拉取）、原生 UI 在 Linux 仅需 GTK3，不需要 WebKitGTK
- 运行（开发模式，热重载）：`go tool mygo dev`
- 打包：`go tool mygo build`（产物在 `build/`，含 .deb 与 install.sh）
- 测试：`go test ./...`（全部离线，Fake Provider）

## 首次使用

1. 启动应用 → 网关自动运行（概览页可停/重启）
2. 渠道页配置上游、导入密钥并添加模型（见下「渠道配置」）
3. 概览页复制"Claude Code 配置片段"到 shell 环境（网关已随应用自动启动）
4. 用 Claude Code 发起真实请求；在 UI 改模型/密钥/启停即时生效，无需重启 agent

## 渠道配置

1. 打开「渠道」页 → 表单选渠道类型（Anthropic 官方 / Anthropic 兼容中转 / OpenAI 官方 / OpenAI 兼容），类型决定协议与默认地址。
2. 填名称与 Base URL（兼容类必填），保存渠道。
3. 在渠道详情导入 API Key（每行一枚，可批量）；密钥进系统钥匙串，库里只存引用与尾 4 位。
4. 为渠道添加模型：公开模型名（Agent 请求名）→ 上游模型名；同渠道公开名唯一，跨渠道可重名。
5. 用「测试连通 / 测试所选密钥」验证后启动网关；`/v1/models` 输出所有渠道启用模型的并集。

### 从旧版本升级（0.1.0 之前）

渠道化重构采用删库重建：删除一次 `~/Library/Application Support/AgentSwitch/agentswitch.db` 后重启应用即可（供应商/账号/别名需按新模型重新配置）。

## 验收清单（对应 spec §15）

见 spec §15；核心自动化回归：`go test ./internal/app -run TestConfigChangeTakesEffectWithoutRestart`
