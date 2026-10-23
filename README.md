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
- Linux 用户级安装：`sh build/linux-amd64/install.sh && sh scripts/linux-desktop.sh`——第二个脚本修正桌面入口（反向域名 id + hicolor 主题图标），规避 GNOME Shell 42 按应用 id 永久缓存图标加载失败的问题；只跑 install.sh 时，重装后应用网格可能显示灰色齿轮直到注销
- 测试：`go test ./...`（全部离线，Fake Provider）

### 已知问题（Linux）

- **亮色主题下窗口文字全白**：MyGo v0.5.1 在 Linux 子像素抗锯齿（`font-antialiasing=rgba`）下的字形着色有缺陷——文字颜色不生效、一律画成白色；深色主题正文本就接近白色，因此不易察觉。应用启动时对本进程把 GTK 的 `gtk-xft-rgba` 设为 `none`（`internal/desktop/fontrender_linux.go`），改用灰度抗锯齿绕开，**不影响系统设置与其他应用**。升级 MyGo 后若上游已修复，可去掉该规避。（v0.6.2 复查无对口修复，规避保留）

## 首次使用

1. 启动应用 → 网关自动运行（侧栏底条 LED 可停/重启）
2. 「渠道」页添加渠道：预设一键导入（小米、千问、DeepSeek、Kimi、Anthropic、OpenAI 等精选）或自定义 base_url/协议；每渠道填**一个** API Key（AES-256-GCM 加密后存库，主密钥 `master.key` 与数据库分离，密钥本体不以明文落盘）
3. 「逻辑模型」页新建逻辑模型，挂有序目标链（渠道 + 该渠道上的模型名，越靠前越优先）
4. 「接入」页为 Claude Code / Codex CLI 新建配置（绑定逻辑模型）并点「应用」——配置直接写入 Agent 的真实配置文件，下次启动即生效（网关已随应用自动启动）
5. 用 Claude Code / Codex 发起真实请求；UI 改目标链/密钥/启停即时生效，无需重启 agent

### 逻辑模型快速上手

1. **建渠道**：「渠道」页 → 预设网格选一家（自动带出 base_url/协议/模型建议）或自定义；只填一枚 API Key。
2. **建逻辑模型**：「逻辑模型」页 → 新建（名字即 Agent 请求的模型名，如 `my-code`）→ 添加目标：选渠道 + 该渠道上的具体模型名，排出优先顺序。
3. **Agent 调用**：请求 `model=my-code`，网关按目标链顺序尝试；失败在响应尚未提交前切到下一目标。

### 从旧版本升级（多 Key / 渠道级路由旧库）

本次为**删库重建**，不提供就地迁移。若启动报「检测到旧版多 Key 数据库（存在 channel_keys 表）」，删除一次 `~/Library/Application Support/AgentSwitch/agentswitch.db` 后重启应用：库按新 schema 重建，渠道/逻辑模型需重新配置一次。不会静默清空你的库。

## 接入 Claude Code / Codex

「接入」页为每个 Agent 管理**全局配置**（env 键值，合并进该 Agent 所有实际配置）与任意多份**实际配置**（绑定逻辑模型，可带覆盖项）。对某配置点「应用」，网关地址、令牌、模型名等受管键直接投影写入该 Agent 的真实配置文件——只改受管键、其余内容保留，写入前自动备份到 `~/.agentswitch/backups/`（每 Agent 保留最近 10 份）。

### Claude Code（应用后写入 `~/.claude/settings.json` 的 `env`）

受管键：`ANTHROPIC_BASE_URL=http://127.0.0.1:<port>`、`ANTHROPIC_AUTH_TOKEN=<网关令牌>`、`ANTHROPIC_MODEL=<逻辑模型名>`、`ANTHROPIC_SMALL_FAST_MODEL=<逻辑模型名>`（小模型可用覆盖项改写；前三键不可篡改）。流量走网关 Anthropic 入站 `POST /v1/messages`。

### Codex CLI（应用后写入 `~/.codex/config.toml` + `~/.codex/auth.json`）

config.toml 受管键：`model`、`model_provider = "agentswitch"`、`[model_providers.agentswitch]`（name="agentswitch" / base_url / wire_api="responses" / requires_openai_auth=false）；auth.json 受管键 `OPENAI_API_KEY`。流量走网关 Responses 入站 `POST /v1/responses`（Codex 0.143+ 只支持该线路；旧配置需在接入页重新「应用」一次）。

### 使用非默认配置

每份配置在保存时生成持久文件（`~/.agentswitch/access/<agent>/<配置id>.json|.env`，随令牌轮换自动刷新）：

- **复制启动命令**：`claude --settings "<持久文件>"`，或 `sh -c 'set -a; . "<持久文件>"; set +a; exec codex -m "<模型>"'`——在终端直接运行即以该配置启动（不改动已应用的默认配置）；
- **打开终端**：一键拉起已带好命令的 Terminal。

「应用」的那份始终是默认；网关重新生成令牌或改端口后，live 文件与所有持久文件自动刷新。

## 三入站

网关暴露三套入站协议，**共用同一套逻辑模型 / 冷却 / 故障转移核心**：

| 入站 | 端点 | 面向 |
|------|------|------|
| Anthropic | `POST /v1/messages` | Claude Code 等 |
| OpenAI | `POST /v1/chat/completions` | Codex 等 |
| OpenAI Responses | `POST /v1/responses` | Codex CLI 0.143+ |

另有 `GET /v1/models` 列出启用的逻辑模型名。客户端协议与上游渠道协议不一致时在网关适配层互转；故障转移不变量（首字节前可切换、首字节后不重放、400 不重试、客户端取消不上抛）对三套入站一致。

## 验收清单（对应 spec §15）

见各 spec 验收节；核心自动化回归：`go test ./internal/app -run TestConfigChangeTakesEffectWithoutRestart`
