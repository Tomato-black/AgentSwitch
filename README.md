# AgentSwitch

本地 AI Agent 模型网关 + 桌面控制面（MyGo 原生 UI，单二进制）。
Agent 指向 `127.0.0.1:17890` 并调用**逻辑模型名**（如 `my-code`）；网关按逻辑模型的目标链在渠道间做优先级故障转移，agent 无需重启。

设计文档：

- 逻辑模型 + cc-switch 式 UI：`docs/superpowers/specs/2026-10-17-logical-models-ccswitch-ui-design.md`
- 渠道化设计：`docs/superpowers/specs/2026-10-17-channels-models-p0b-design.md`
- 桌面 MVP：`docs/superpowers/specs/2026-10-15-agentswitch-p0a-desktop-first-design.md`
- 架构文档（v2.0）：仓库根目录 docx。

## 开发

- 依赖：Go 1.27+（`GOTOOLCHAIN=auto` 会自动拉取）、原生 UI 在 Linux 仅需 GTK3，不需要 WebKitGTK
- 运行（开发模式，热重载）：`go tool mygo dev`
- 打包：`go tool mygo build`（产物在 `build/`，含 .deb 与 install.sh）
- 测试：`go test ./...`（全部离线，Fake Provider）

## 首次使用

1. 启动应用 → 网关自动运行（侧栏底条 LED 可停/重启）
2. 「渠道」页添加渠道：预设一键导入（小米、千问、DeepSeek、Kimi、Anthropic、OpenAI 等精选）或自定义 base_url/协议；每渠道填**一个** API Key（AES-256-GCM 加密后存库，主密钥 `master.key` 与数据库分离，密钥本体不以明文落盘）
3. 「逻辑模型」页新建逻辑模型，挂有序目标链（渠道 + 该渠道上的模型名，越靠前越优先）
4. 「接入」页选定逻辑模型，复制配置片段到 shell 环境（网关已随应用自动启动）
5. 用 Claude Code / Codex 发起真实请求；UI 改目标链/密钥/启停即时生效，无需重启 agent

### 逻辑模型快速上手

1. **建渠道**：「渠道」页 → 预设网格选一家（自动带出 base_url/协议/模型建议）或自定义；只填一枚 API Key。
2. **建逻辑模型**：「逻辑模型」页 → 新建（名字即 Agent 请求的模型名，如 `my-code`）→ 添加目标：选渠道 + 该渠道上的具体模型名，排出优先顺序。
3. **Agent 调用**：请求 `model=my-code`，网关按目标链顺序尝试；失败在响应尚未提交前切到下一目标。

### 从旧版本升级（多 Key / 渠道级路由旧库）

本次为**删库重建**，不提供就地迁移。若启动报「检测到旧版多 Key 数据库（存在 channel_keys 表）」，删除一次 `~/Library/Application Support/AgentSwitch/agentswitch.db` 后重启应用：库按新 schema 重建，渠道/逻辑模型需重新配置一次。不会静默清空你的库。

## 接入 Claude Code / Codex

「接入」页选定逻辑模型后一键复制片段（含网关密钥与逻辑模型名）。等价形式如下（端口以设置页为准，默认 `17890`）：

### Claude Code

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:17890
export ANTHROPIC_AUTH_TOKEN=<网关密钥>
export ANTHROPIC_MODEL=my-code
export ANTHROPIC_SMALL_FAST_MODEL=my-code
```

写入 shell 环境变量，或 `~/.claude/settings.json` 的 `env` 字段。流量走网关 Anthropic 入站 `POST /v1/messages`。

### Codex CLI

```bash
export OPENAI_BASE_URL=http://127.0.0.1:17890/v1
export OPENAI_API_KEY=<网关密钥>
```

前两行写入 shell 环境变量；`model = "my-code"` 写入 `~/.codex/config.toml`。流量走网关 OpenAI 入站 `POST /v1/chat/completions`。

## 双入站

网关暴露两套入站协议，**共用同一套逻辑模型 / 冷却 / 故障转移核心**：

| 入站 | 端点 | 面向 |
|------|------|------|
| Anthropic | `POST /v1/messages` | Claude Code 等 |
| OpenAI | `POST /v1/chat/completions` | Codex 等 |

另有 `GET /v1/models` 列出启用的逻辑模型名。客户端协议与上游渠道协议不一致时在网关适配层互转；故障转移不变量（首字节前可切换、首字节后不重放、400 不重试、客户端取消不上抛）对两套入站一致。

## 验收清单（对应 spec §15）

见各 spec 验收节；核心自动化回归：`go test ./internal/app -run TestConfigChangeTakesEffectWithoutRestart`
