# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目状态与权威文档

AgentSwitch：面向本地 AI Agent 的**模型 API 网关**——为 Claude Code / Codex CLI 等工具提供统一的本地模型入口，集中管理多供应商/多账号，支持手动指定账号与有限、安全的自动故障转移。

技术选型（v2.0 设计稿确定）：Go 实现网关核心，**MyGo**（https://github.com/egoist/mygo，桌面框架）做桌面壳与前后端桥接，前端 TypeScript + HTML/CSS，数据用 SQLite。**凭据存储（2026-10-18 用户决策，对 docx §8/§10「OS Keychain」的明示偏离）**：渠道 API Key 以 AES-256-GCM 加密存 SQLite（`channel_secrets` 表，v1 建表），主密钥为库同目录 `master.key`（32 字节、0600、缺失自动生成）；OS 钥匙串与明文降级文件已退役。

当前仓库已完成 P0-A′ 桌面优先 MVP（实现计划：`docs/superpowers/plans/2026-10-15-agentswitch-p0a-desktop-first.md`）：单二进制桌面应用，入口 `cmd/agentswitch`，Go 1.27.3，依赖 mygo v0.8.3（2026-11-07 自 v0.6.2 升级：工具链随升 1.27.3；API 零改动编译通过。适配两处——v0.8.0 起窗口失焦 Accent 默认褪灰，`theme.go` 深浅两支钉死 `InactiveAccent = Accent`（托盘驻留常失焦，主操作橙不随焦点褪色）；Tester 文本树顺序变化令页面级用例里 `Click("关闭")` 命中遮罩下三态分段同名文本，改走 `Key(Escape)`。v0.7.0 起安装包/dmg 按 slug 命名（AgentSwitch 为 ASCII 名不受影响，CI 无产物名 glob）。此前 2026-10-22 自 v0.5.1 升级 v0.6.2，动机：v0.5.1 在「关窗驻留托盘 + 网关持续运行」形态下反复 SIGSEGV——`gdk_window_get_display` 断言后空指针，两天 7 次；v0.6.0 起隐藏窗口不再画帧，属该族修复）与 modernc.org/sqlite；核心包在 `internal/{app,bridge,config,credential,desktop,gateway,protocol,provider,router,storage,telemetry}`，测试全部离线（httptest Fake Provider），`README.md` 含首次使用流程。相对 v2.0 目标结构（§12）的落地差异：管理面以进程内 `bridge`（`mygo.Bind` 桌面桥接、无独立管理端口）替代 `controlapi`；migrations 内嵌在 `internal/storage`（v1 + 2026-10-27 逻辑模型元数据 v2）。**UI 决策（2026-10-17）**：后续全部使用 MyGo 原生视图（`internal/desktop`），`frontend/` TS 工程方案**废弃**——这是对 docx §12 前端规划的明示偏离（docx 不改，以 `docs/superpowers/specs/2026-10-17-desktop-ui-mygo-native-design.md` 为凭证）；桌面 UI 设计系统见 `docs/superpowers/specs/2026-10-17-desktop-ui-ccswitch-redesign-design.md` 与 `internal/desktop/uikit/theme.go`——视觉系统由该 spec 取代旧「交换机面板」（橙主操作/绿橙分离/卡片流/列表+详情 IA）。**desktop 分包（2026-10-28）**：`internal/desktop` 收敛为组合根（shell 导航/窗口/托盘/关窗模态/嵌入资源），按页拆子包——`uikit`（主题 token/控件/图标）、`state`（共享 UI 状态与各页表单，叶子包）、`accesskit`（接入文档 JSON 管道、角色助手、通用编辑器骨架）、`uitest`（页面测试夹具）、`pages/{logical,channels,settings,access}`；接入页内部再按 Agent 分 `pages/access/{claude,codex,desktop,guide}`（宿主 `pages/access` 只做页签分发/全局卡/配置列表，Agent 子包互不依赖、不得反向 import 宿主）。纯结构重构，界面与行为零变化；新增页面按此格局落位。2026-10-17 起渠道模型为 new-api 式 **channels 聚合根**（`channels`/`channel_keys`/`channel_models` 三表，渠道级模型清单与重定向；providers/accounts/models 旧表已退役）——设计见 `docs/superpowers/specs/2026-10-17-channels-models-p0b-design.md`，实现计划见 `docs/superpowers/plans/2026-10-17-channels-models-p0b.md`。P0-B（优先级故障转移、冷却、二次校验）随该 spec 落地。**当前形态（2026-10-17 逻辑模型重构）**：路由主体是用户自定义的**逻辑模型**（`logical_models` + `logical_model_targets` 有序目标链），渠道为**单 Key** 执行单元（`channels` + `channel_models` 模型建议列表，`channel_keys` 多密钥表及其代码路径已删除）；桌面 UI 为 cc-switch 式侧栏（逻辑模型/渠道/接入/设置 + 底条网关状态）；网关**三入站**（Anthropic `POST /v1/messages` + OpenAI Chat `POST /v1/chat/completions` + OpenAI Responses `POST /v1/responses`，共用同一套逻辑模型/冷却/故障转移核心）——设计见 `docs/superpowers/specs/2026-10-17-logical-models-ccswitch-ui-design.md`，实现计划见 `docs/superpowers/plans/2026-10-17-logical-models-ccswitch-ui.md`。旧「按渠道优先级故障转移」路由入口已删除，由逻辑模型目标链取代。**重构均为删库重建**：schema 合并为单一 migration v1；旧库含 `channel_keys` 表时启动报错，老用户需删除一次 `~/Library/Application Support/AgentSwitch/agentswitch.db`（不静默清库、不就地迁移）。**例外（2026-10-27）**：逻辑模型元数据走 migration v2（`logical_models` 纯增列），旧 v1 库就地升级、不需删库；仍拒绝 providers 时代旧表与 `overrides` 形态的 v3 残库。**接入配置管理（2026-10-18）**：接入页从「片段复制」升级为每 Agent（Claude Code / Codex，后者覆盖 CLI / 桌面版 / IDE 插件）多实际配置 + 全局配置（env 合并，实际配置覆盖项优先）；「应用」即受管键投影写入 agent 真实配置文件（`~/.claude/settings.json` 或 `~/.codex/config.toml`，写入前备份至 `~/.agentswitch/backups/`，只改受管键、其余保留；**2026-10-27 修订**：Codex 第三方 Key 写 `config.toml` 的 `[model_providers.agentswitch].experimental_bearer_token`，`auth.json` 只由 Codex 维护官方登录、接入配置不再读写它）；非默认配置经持久文件（`~/.agentswitch/access/…`）+ 启动命令 / 「打开终端」使用；令牌/端口变更自动刷新全部派生文件——设计见 `docs/superpowers/specs/2026-10-18-agent-access-config-design.md`，实现计划见 `docs/superpowers/plans/2026-10-18-agent-access-config.md`；storage 曾以 migration v3 增 `access_configs` 表（已被 2026-10-19 单一 v1 取代），新依赖仅 `github.com/pelletier/go-toml/v2`。旧接入片段页（`bridge.Access`）已删除。

**接入文档化（2026-10-19）**：接入配置升级为整份 settings JSON 文档（`access_configs.settings` 列取代 `overrides`，**删库重建**——schema 合并单一 v1，旧库启动报错需删一次 `agentswitch.db`）；编辑器为 cc-switch 式「角色映射表 + 配置 JSON 双向同步」，网关三键（`ANTHROPIC_BASE_URL/AUTH_TOKEN/MODEL`，Codex 为 `OPENAI_BASE_URL/API_KEY`）锁定（`ANTHROPIC_MODEL` 为**半锁定**：模型名不可改、允许追加 `[1M]` 上下文声明，编辑器主模型行有 1M 勾选；2026-10-21）、保存归一化剔除未定制键；**令牌展示（2026-10-21 用户决策，对 spec 2026-10-19 §3.4「预览令牌遮蔽」与 §8「UI 禁止出现密钥」的明示偏离）**：编辑器配置 JSON 默认渲染**真实网关令牌**（不再是 `__GATEWAY_TOKEN__` 占位符），保存路径仍剔除锁定键、令牌永不落库；网关路由匹配前剥离模型名尾部 `[1M]`——设计见 `docs/superpowers/specs/2026-10-19-access-full-settings-ccswitch-design.md`（§3.4 已修订），旧「overrides 覆盖项」表述作废。

**Responses 入站（2026-10-23）**：Codex CLI 0.143 起移除 `wire_api="chat"`，网关新增 `POST /v1/responses` 入站；渠道协议仍为 anthropic/openai(chat)，请求与响应在 `internal/protocol/responses*.go` 双向转换；工具映射对齐 cc-switch（function/custom/namespace/tool_search，web_search 丢弃）；Codex 接入配置写 `wire_api="responses"`。设计见 `docs/superpowers/specs/2026-10-23-openai-responses-inbound-design.md`，实现计划见 `docs/superpowers/plans/2026-10-23-openai-responses-inbound.md`。

**Codex 桌面版与多逻辑模型（2026-10-27）**：Codex CLI / 桌面版 / IDE 插件共用 `~/.codex/config.toml`，接入页页签由「Codex CLI」改为「Codex」；第三方 Key 改写 `[model_providers.agentswitch].experimental_bearer_token`（Codex 0.149 起自定义 provider 不再从 `auth.json` 继承凭据；桌面版从 Dock/开始菜单启动也不继承 shell env，故不用 `env_key`），`auth.json` 只保留官方 ChatGPT 登录、接入配置不读写。**模型列表（本次新增）**：每份 Codex 接入配置的 settings 文档存有序 `models`（逻辑模型名数组），第一项为默认模型并镜像 `access_configs.logical_model_id`；目录**只按该显式列表**生成（旧配置缺 `models` 时回退「绑定模型 + 全部启用模型」），`models[0]` 写 `config.toml` 的 `model`。**逻辑模型元数据（migration v2，纯增列、旧库就地升级不删库）**：`display_name`、`context_window`、`reasoning_levels`（JSON 数组）、`default_reasoning_level`；目录条目用它们替换 `slug`/`display_name`/窗口/思考等级。**2026-10-28 调整**：这四项只影响 Codex 模型菜单，编辑入口从逻辑模型编辑页迁到接入页 Codex 编辑器的「模型元数据」区——该区改为 **per-config 覆盖**：编辑即时写入本配置文档的 `model_meta`（按字段省略=回退逻辑模型库值），目录生成按 `model_meta` → 逻辑模型元数据 的顺序取值；逻辑模型页保存时保留既有元数据，新建模型不带元数据。**config.toml 受管键**：默认模型 `context_window > 0` 时写 `model_context_window` 与 `model_auto_compact_token_limit`（窗口的 90%，0 则不写不删、保留用户设置）；高级设置的子代理模型/推理强度写 `[agents].default_subagent_model`/`default_subagent_reasoning_effort`（空值删受管键、保留同表其他键）；自动审批模型写入目录每行 `auto_review_model_override`（空则 Codex 回落主模型）；并固定写 `disable_response_storage = true`、`web_search = "disabled"`。目录文件 `~/.codex/agentswitch-model-catalog.json` 仍用 Codex 自带 `debug models --bundled` 模板，`model_catalog_json` 指向它——桌面版模型选择器只读该文件、不读网关 `/v1/models`，否则会回写旧 cc-switch 目录里的模型名导致网关 404。**接入页边界**：只放 A 类 Codex 客户端设置（模型列表、元数据、子代理、自动审批、1M/压缩、兼容项），并展示只读的完整 config.toml 预览与只读「将写入 Codex 的模型」元数据列表；B 类上游连接（base URL、Key、协议转换、OAuth、`auth.json` 编辑器、路由/聚合、用量、测速）仍在渠道/逻辑模型层，不进接入页。应用后需完全退出 Codex 再重新打开（桌面版只关窗口不够），旧对话的 provider 不迁移、需新建对话。设计见 `docs/superpowers/specs/2026-10-27-codex-access-models-metadata-design.md`。

**权威设计文档（2026-10-23 更新）**：原仓库根目录的 v2.0 docx 设计稿已随开源准备移出仓库（历史版本仍在 git log 中；正文中「docx §x」的旧引用指该稿）。现行权威设计是 `docs/superpowers/specs/` 下按日期命名的各 spec（功能实现前先查对应 spec；代码与 spec 冲突时，以 spec 为准并指出来）。

**开源卫生（2026-10-23）**：`.idea/`、`.claude/`、`.superpowers/`、`*.iml` 已停止跟踪并加入 `.gitignore`（个人工作区不入库）；CLAUDE.md 中的网关令牌样例已占位符化——真实令牌只存 SQLite `auth.gateway_token`；本地网关令牌**不提供轮换**（2026-10-28 决策，网关仅监听 127.0.0.1），如需更换需删除该设置键并重启、再重新「应用」全部接入配置。

**Claude Desktop 接入（2026-10-22）**：接入页第三种 Agent 类型 `claude_desktop`——配置为角色映射（sonnet/opus/haiku/fable → 逻辑模型，无 env 层）；「应用」把网关 profile（`inferenceProvider=gateway`、`inferenceGatewayBaseUrl/ApiKey`、`inferenceGatewayAuthScheme=bearer`、`inferenceModels`）patch 合并进 `~/.config/Claude/claude_desktop_config.json`（MCP 等键原样保留、写前备份），并把「claude-sonnet-5 → 逻辑模型」别名表持久化到 settings（`access.desktop_aliases`，last-applied-wins）；网关 candidates 解析入站名前先查别名表、`/v1/models` 追加别名（跟随逻辑模型启用态）。Desktop 只接受 claude-* 角色路由 ID（白名单参照 cc-switch：sonnet/opus/haiku/fable）。参考 cc-switch 的 inferenceGateway 方案。

**Linux 字体渲染规避（2026-10-21）**：MyGo v0.5.1 的 Linux 子像素字形着色有缺陷——Pango 以白色栅格化字形、GPU 着色未正确应用文字颜色，所有文字实际画成白色（深色主题正文本就近白，不易察觉；亮色主题下整窗不可读）。`internal/desktop/fontrender_linux.go` 在 `WhenReady` 建窗前用 purego 对本进程 GTK 设 `gtk-xft-rgba=none`，改走灰度抗锯齿；**不改系统设置、不影响其他应用**。升级 MyGo 时先复查上游是否已修复，再决定是否移除该规避。**v0.6.2 复查（2026-10-22）**：上游 issue/changelog 无对口修复记录，保守保留。**v0.8.3 复查（2026-11-07）**：仍无对口修复（#1025 为旧 cairo 的 LCD 滤镜缺陷、不同问题），规避保留；若亮色主题下实测文字颜色正常可移除。

**Windows 支持（2026-10-26）**：`go tool mygo build -platform windows/amd64` 可交叉产出 `AgentSwitch.exe` + NSIS 安装包（纯 Go、CGO 关闭；Linux 打安装包需装 NSIS，Windows 上由 mygo 自动下载）。Claude Desktop 配置路径按 cc-switch 映射为 `%LOCALAPPDATA%\Claude` / `Claude-3p`（精确目录优先，否则按 `Claude` 前缀 + 是否含 `-3p` 挑选）；非默认接入配置在 Windows 生成 `.cmd` 启动器（`%` 转义为 `%%`，值/模型拒绝双引号与换行），展示命令为 `cmd /c call "<launcher>"`，「打开终端」走 `cmd /c start "" "<launcher>"`；平台分支经 `accessPlatform` 注入以便非 Windows 主机测试。CI 新增 `windows-latest` 打包 job 与 Ubuntu 上的 `GOOS=windows` 交叉编译检查。**尚未在 Windows 实机运行验证**（本机 Wine 6.0.3 缺 `bcryptprimitives.dll`，Go 1.27 运行时无法启动）。

**渠道预置配置文件化（2026-10-23）**：预置不再硬编码于 Go，`internal/presets` 改为 embed 默认表（`default.json`，20 条：14 按量 + 6 家 Coding Plan 订阅独立条目）+ 用户文件 `~/.agentswitch/presets.json`（同 id 整体覆盖原位、新 id 追加；解析失败降级内嵌并在预设网格顶部提示警告）；每条按 `openai`/`anthropic` 双协议组织 baseUrl 与默认模型（`protocols` map），表单切协议经 `ui.Select(...).Changed()` 联动换值（手改保护：值非空且不等于任一协议预设值则不动；空值视为未修改）。内嵌条目 baseUrl 必须过 `ValidateBaseURL(_, false)`，**用户条目放宽**（http/私网/localhost 放行，仅要求可解析——自托管中转站模板用）。启动 `cmd/agentswitch/main.go` 加载一次、此后只读。全部 baseUrl/模型名以 `docs/superpowers/specs/2026-10-23-channel-presets-config-design.md` §6 官网查证表为准（当日逐家核实；anthropic 端点拼接护栏见 `TestAnthropicEndpointAllPresetBases`）。附带网关改动：出站请求透传客户端 `User-Agent`（Kimi Code 条款要求，`DoRequest.UserAgent`，空则保持 Go 默认）；`[1m]` 小写后缀剥离确认已兼容（`EqualFold`）。

**Claude Code 配置中心（2026-11-07，v0.1.1）**：不新增侧栏导航——「接入」页 Claude 页签改为「接入摘要卡（状态/配置/模型/编辑接入/应用接入）+ 下方 Claude Code 设置区」。设置区管理 Claude Code 自身分层 settings：用户级 `~/.claude/settings.json`、项目共享 `.claude/settings.json`、项目本地 `.claude/settings.local.json`（默认落 Git 仓库根，Windows/非 Git/家目录仓库回退项目目录并显示实际路径）；企业托管只读（含 `managed-settings.d` drop-in 按 spec §6.1.1 合并，MDM/远程标「不可见」）。三视图：七组结构化表单（模型/环境变量/权限/状态栏/MCP 与插件/Skills 四态 `skillOverrides`/记忆与上下文，`skipDangerousModePermissionPrompt` 默认 true、项目共享禁写、OR 合并）、严格 JSON 编辑器（行列错误、scope 与受管键阻断、损坏文件只读修复态+空配置重建）、跨层合并预览（最终值+来源+合并方式；数组并集、env 逐变量、hooks 累加、四键整值替换、权限 deny→ask→allow）。**受管键动态化**：13 个接入投影 env 键（`AccessManagedEnv`）全层只读；接入文档顶层键动态只读；全局 env 弱受管（可编辑+「接入应用可能覆盖」警告）。**写入安全**：`claudeSettingsMu` 串行化接入 Apply/配置保存/两类备份恢复（接入页 `RestoreBackup` 同样持锁）；保存走「备份（按目标路径 sha256 分桶 + meta.json 绑定，每桶保留 10 份）→ Hash 冲突三动作对话框（取消默认/重载/覆盖）→ 原子写 → 只 patch 变更叶子」；`Diff` 为叶子语义（一侧缺整棵子树展开叶子）；`cloneDoc` 深拷贝保 `json.Number`（防幻影脏）。设置区 dirty 时侧栏切页/刷新/切层走统一确认框，并阻断接入应用。`CLAUDE_CONFIG_DIR` 本期不支持（检测即警告）；JSON 无行号槽（MyGo TextArea 限制）。核心包：`internal/claudecfg`（纯逻辑：路径/键注册表/合并/scope/敏感值/Skills）、`internal/bridge/claude_*.go`、`internal/desktop/pages/access/claude/claudecfg`（UI）；`uikit.RichDialog` 为富内容对话框（ConfirmDialog 不动）。设计见 `docs/superpowers/specs/2026-11-07-claude-code-config-page-design.md`，计划 `docs/superpowers/plans/2026-11-07-claude-code-config-center.md`，需求/原型在 `docs/requirements/`、`docs/prototypes/`。遗留（不阻塞）：候选路径手动切换、应用接入对话框化、Skills 扫描错误上浮、IO 异步化。

**接入页原型对齐（2026-11-07，v0.2.0 UI 批次）**：接入页 Claude 页签全面对齐原型 `docs/prototypes/claude-code-config-page.html`（计划 `docs/superpowers/plans/2026-11-07-claude-access-prototype-alignment.md`）。**Claude 全局配置下线（用户决策，对需求 §6.2.2「抽屉内只读折叠区」的明示偏离）**：UI 卡删除、bridge `GlobalConfig("claude")` 恒空、`SaveGlobalConfig("claude")` 拒绝；残留 settings 键 `access.global.claude` 不再参与合成（可手工删键）；Codex 全局配置与 Desktop 页签不受影响。**形态**：侧栏/页头改「Agent 接入」；**修订（2026-11-07 用户实测反馈）**：「刷新/保存设置」是 Claude 设置区专属动作，收回设置区区块头渲染（橙色胶囊主操作仍在区块头行内），不进全页签共享的页头——对需求 §4.1「页头右侧动作区」的明示偏离；接入编辑器从独立子页改右侧抽屉（`uikit.Drawer`：`ui.Overlay` + 遮罩 + Esc，宽 min(560, 窗宽-48)）——抽屉含配置选择（新建草稿伪选项）/新建/删除/复制启动命令/打开终端/备份恢复/还原最近备份/名称/默认模型+1M/角色映射/受管键只读预览/变更对比/关闭（保留草稿 `AccessForm.Draft`，摘要卡显示「有未保存的接入更改」）/仅保存（恒可用）/保存并应用（网关停止禁用）；主页 Claude 页签＝摘要卡（LED+地址+配置/模型+1M 徽章+接入状态与网关停止双徽章）→ 设置区，实际配置列表卡仅剩 codex/desktop；受管键行「打开接入」跳抽屉。**设置区**：区块标题出卡+文件状态条（已保存/有未保存修改/JSON 错误/只读/文件不存在）+`ui.Segmented` 层级切换+项目/托管上下文卡+七组独立卡片流；表单控件升级——布尔三态分段（继承/开启/关闭）、列表 chip 编辑器（fallbackModel 带 ↑ 排序）、env 列头；合并预览权限求值三段式（1 拒绝→2 询问→3 允许）。对 spec 偏离与 P1 遗留：摘要卡「最后应用时间」无数据源不做、JSON 无行号、插件行仍 checkbox（三态 P1）、项目选择器无搜索/最近排序（P1）、抽屉窄窗动画 P1。

**表单缺键直接可编辑（2026-11-07 用户实测「权限无法设置」的根因修复，计划 `docs/superpowers/plans/2026-11-07-form-direct-edit.md`）**：缺键字段的「继承自下层 + 覆盖按钮」两步门槛导致输入控件不可见、用户感知为不能设置——改为**直接渲染编辑控件**（空值起步，首次变更写当前层、继承值不抄入），继承信息降为控件下方只读提示行「下层：值（来源）」；「覆盖」按钮与 `ClaudeCfgState.Overrides` 机制整体移除（对需求 §6.6「继承行+覆盖按钮」的明示简化，与 bool 三态形态对齐）。

**接入模型配置全下拉（2026-11-07 用户指令，计划 `docs/superpowers/plans/2026-11-07-access-model-dropdowns.md`）**：主模型改 `ui.Select`（「（跟随网关）」哨兵＝删 model 键）；角色显示名称随模型下拉自动填充——名称为空或恰为某逻辑模型名（自动填充痕迹）时覆盖为新模型名，手动改过不动，「跟随主模型」清空（`autofillRoleName`）。

**接入编辑回归子页 + 列表（2026-11-07 用户实测裁定，计划 `docs/superpowers/plans/2026-11-07-access-editor-page.md`）**：设置拆独立页后抽屉的动机消失——**抽屉方案废弃（对原型 §6.2.2 的明示推翻）**，Claude 页签回归与 codex/desktop 一致的结构：摘要卡 + 配置列表卡（行含 应用/编辑/**更多菜单**——复制启动命令/打开终端/删除，卡头含 备份与恢复/还原最近备份/新建）+ **编辑独立子页**（editorPage 三页签共用，claude.EditorCard：名称/模型+1M/角色映射/Claude 模型选择/预览/diff/取消/仅保存/保存并应用）。抽屉机制整体移除：`uikit.Drawer`、`AccessForm.Draft`（Dirty 回归 Open 语义）、`DrawerSwitchArm/ID`、`drawerSelect/ConfirmDrawerSwitch/DrawerHooks`、`openAccessEditor` 草稿守卫；「打开接入」跨页入口退化为纯导航 `access.GoToAccess`。

**模型键归接入（2026-11-07 用户实测裁定，计划 `docs/superpowers/plans/2026-11-07-model-keys-to-access.md`）**：主模型/回退模型不再在设置页编辑（「模型与响应」卡撤下两字段，改名「推理与响应」只留推理强度/思考开关）——接入抽屉新增「Claude 模型选择」段：顶层 `model`（空＝跟随网关 ANTHROPIC_MODEL，placeholder 提示）与 `fallbackModel`（有序回退链，↑/×/添加，候选＝逻辑模型）写入接入文档顶层键，经既有 `mergeClaudeLive` 投影链路进 settings.json，`StrongTopLevel` 让设置页自动只读（投影与受管投影由 bridge 测试钉住）。**这是对需求 §8.8「`model` 键由设置区管理」的明示偏离**。继承行补字段标签（inheritedRow 此前无标签——权限卡四行不可识别，「无法修改权限」的根因，用户实测发现）。

**设置页拆分与选中态加强（2026-11-07 用户实测反馈，计划 `docs/superpowers/plans/2026-11-07-claude-settings-split-page.md`）**：**部分推翻「合并为唯一入口」决策（需求痛点 4）**——「接入只配置模型和网关」：Claude Code 设置区整体拆为侧栏第五项「Claude Code 设置」独立页（`claudecfg.Page`：页头含标题/副标题/受管徽章/刷新+保存设置——此前「不进共享页头」的裁定随迁；滚动主体＝文件条/层级/视图/七卡），脏守卫 `claudeCfgDirty`/`discardDrafts` 改按 `Page=="claude-settings"` 判定（接入页 `tabDirty` 不再计入）；接入页 Claude 页签只留摘要卡+抽屉，摘要卡提供「打开设置页」入口；设置页受管键行「打开接入」经 `access.OpenAccessDrawer` 跳回接入页并开抽屉。**选中态加强**：根因是框架 Segmented 选中面取 `t.Background`、与本主题 `t.Surface` 同为 #FFFFFF（白上加白）——新增 `uikit.Segmented`（SegmentedBase 自绘：选中段软橙底+粗体+深字）替换层级/共享-本地/布尔三态全部调用；视图页签 TabsBase 自绘（选中＝橙字+粗体+3px 下划线）。

**v0.8.3 组件利用（2026-11-07）**：升级 mygo 后按 release notes 逐项映射的落地批次（计划 `docs/superpowers/plans/2026-11-07-mygo-083-component-uplift.md`）。① 次级操作「更多」自造展开行全部换 `ui.PopoverMenuButton` 主题化菜单（键盘+读屏支持；网关停止时菜单项 `Disabled` 置灰而非隐藏；删除为 `Danger` 红项；选中即关）——codex/desktop 配置行与 Claude 接入抽屉页脚（抽屉页脚回到原型的 关闭/仅保存/保存并应用 三键）。② 两处搜索框（codex 配置过滤、合并预览键路径筛选）换 `ui.SearchField`（放大镜/一键清除/Esc 清空/读屏 search 角色）。③ 全局浏览器式缩放（`desktop/zoom.go`：Cmd/Ctrl `=`/`-`/`0`，0.5–3 十三档，`c.Shortcut`+`c.SetZoom`，随会话不持久化）——密集设置页的低视力可放大。④ 托盘「打开主窗口」菜单项加图标（`mygo.NewMenuIconSVG` 复用 uikit SVG；失败降级纯文字）。随升级免费生效：文本输入原生 Undo/Redo/Cut/Copy/Paste 菜单（v0.8.0 Edit 菜单跟随焦点）、Windows resize 重绘修复与渲染提速、macOS 动画显存回收与空闲功耗下降、Linux 打包附 PKGBUILD。终审 minor 清理批次（同日）：RowMore 死状态字段随菜单化删除；menuIcon 失败路径记 slog 日志（降级纯文字）；zoomOne 漂移保护断言；抽屉菜单备份/还原选中落位用例补齐。**工具备注**：`mygo-automation`（v0.8.0）可在开发期以 CLI 驱动真实运行的应用（snapshot/press/fill），UI 改动的实机验证优先考虑它。

## 常用命令

```bash
go build ./...        # 编译全部包
go run .              # 运行当前入口
go vet ./...          # 静态检查
go test ./...         # 全部测试
go test ./internal/app -run TestConfigChangeTakesEffectWithoutRestart -v   # 跑单个测试
go test -race ./...   # 涉及并发/冷却状态的测试必须带 -race
go tool mygo dev      # 开发运行（热重载；入口 cmd/agentswitch，UI 在 internal/desktop）
go tool mygo build    # 打包（产物在 build/）
```

无独立前端工程（UI 决策 2026-10-17：全部 MyGo 原生视图，设计 token 在 `internal/desktop/theme.go`）。MyGo 项目模板与打包命令以选定版本的官方仓库/文档为准（文档明确要求不假定未经验证的框架 API）。

## 架构（依据设计文档 v2.0）

### 两平面分离——最重要的边界

- **桌面控制面**（MyGo 窗口 + TS UI + controlapi）只做配置、状态、操作、查询；**绝不承载模型请求正文流量**。
- **模型数据面**（gateway → protocol → router → provider → 上游 Provider API）对 Agent 暴露统一模型 API。
- 网关生命周期独立于 UI：关闭窗口 ≠ 停止网关；停止必须走优雅收尾（拒绝新请求、宽限在途请求、刷事件队列、关 SQLite）。网关运行状态必须来自实际服务状态，不是桌面按钮状态。
- 核心业务包必须能在**无桌面 UI** 的测试环境中运行。

### 目标目录与依赖规则（文档 §12、§12.1，硬约束）

目标结构：`cmd/{agentswitch,agentswitch-gateway}`、`internal/{app,gateway,controlapi,protocol,provider,router,config,credential,storage,telemetry,desktop}`、`migrations/`、`tests/{fixtures,fakeprovider,integration}`；`frontend/` 不再建设（UI 决策 2026-10-17）。

- `router` 不依赖 MyGo、UI 或具体 HTTP handler；`provider` 通过接口暴露调用能力，不反向依赖 `router`。
- `gateway` 组合 protocol/router/provider，不自行实现每家供应商的错误判断；`controlapi` 只调应用服务，不绕过 Repository。
- SQLite（`storage`）与密钥存储（`credential`）通过接口注入，便于测试与平台适配。
- `desktop` 只负责生命周期与桥接，平台差异收敛在此层。

### 路由与故障转移不变量（文档 §6.3、§7；路由主体为逻辑模型）

- 路由主体是**逻辑模型目标链**（`logical_models`/`logical_model_targets`）：Agent 请求逻辑模型名，网关按目标 `priority` 升序跨「渠道·模型」切换；手动固定（Pinned）语义**必须**由用户显式选择“失败即停止”还是“允许回退”。
- 只有在**尚未向客户端提交响应头/任何流式事件前**才允许跨目标切换；流已开始输出后绝不静默重放（上游可能已计费）。
- 错误分类不能只凭 HTTP 状态码断定额度耗尽（需结合供应商错误码/响应体/文档）；参数错误 400 不做无意义重试；客户端取消不转移，直接取消上游。
- 冷却按 **ChannelID** 记账，状态并发更新必须原子（事务/锁/原子更新）；路由器真正发请求前要**二次校验**渠道状态。
- 每次路由决策输出可解释记录：被过滤候选及原因、最终目标、切换次数。
- 未登记模型名默认返回明确错误（「透传未登记模型」开关默认关，首版仅预留），避免静默错路由。
- 流式响应的 `RequestTimeoutSeconds` 语义（2026-10-22）：非流式=整次请求总时限；流式=等待上游响应头的时限 + 客户端可见输出的空闲时限。上游流中断/空闲超时必须向客户端发 error 事件，残缺工具参数不得以空 input 下发；截断后不写 `message_stop`/`[DONE]`。设计见 `docs/superpowers/specs/2026-10-22-stream-truncation-idle-timeout-design.md`。

### 安全默认（文档 §8、§10）

- 模型 API 与管理 API 默认只监听 127.0.0.1，且使用**相互独立**的访问令牌。
- API Key 以 AES-256-GCM 加密存 SQLite `channel_secrets`（密文 BLOB），主密钥 `master.key`（0600）与库分离存放，`channels.credential_ref` 只作引用/有钥标志；明文永不落库落日志（2026-10-18 用户决策替代 OS Keychain 方案，docx §8 偏离已记录）。
- 日志、错误、UI、诊断包中禁止出现密钥、Authorization、Cookie、完整 Prompt（事件只记脱敏元数据）。
- 用户配置的 `base_url` 要做 SSRF/重定向/私网地址校验；配置导出默认脱敏。

### 存储与测试（文档 §8、§13）

- SQLite 存非秘密配置与状态：`channels`（**单 Key**，`credential_ref` 仅作引用；密文在 `channel_secrets`，主密钥 `master.key` 分离；`channel_keys` 已删除）+ `channel_models`（仅模型建议列表，非路由主体）+ `logical_models`/`logical_model_targets`（路由主体目标链）。docx §8/§13 转述保留 Provider/Account/ModelMapping/RoutePolicy 原文名。事件遥测沿用内存环 + 文件日志，**不落 SQLite**；文件日志由 `internal/telemetry` 的 `rotatingWriter` 按 5 MiB 轮转、gzip 归档保留 2 份（磁盘占用长期稳定在 ~6 MB 量级）。经 Repository + `migrations/`。
- 测试以 **Fake Provider** 为主（稳定复现额度耗尽、429/Retry-After、5xx、流式中断），不依赖真实供应商；真实 Agent 兼容性验证只作受控测试，常规 CI 不打真实上游。
- 必测边界：首字节前可切换、首字节后不重放、客户端取消、并发 429 冷却原子性（`-race`，冷却键 ChannelID）、停止期间请求收尾；多逻辑模型并存互不串扰、目标顺序生效。

### MVP 实施顺序（文档 §14）

P0-A 核心网关（单协议端到端）→ P0-B 账号池与故障转移 → P0-C 持久化与安全 → P0-D MyGo 桌面端 → P0-E 打包验收。原则：先用 MyGo 官方模板验证最小桌面能力（窗口关闭行为、Go 调用桥接、资源打包），再大规模 UI；先做一个真实 Agent 的完整协议路径，再加第二种协议；MyGo 版本锁进仓库并记录到 README。

### 待决策事项（文档 §16）

- 网关单进程 vs 独立子进程**未定**——先做生命周期抽象，验证 MyGo 运行模型后再决定。
- MyGo 具体 API、托盘/开机启动等能力未经验证，以选定版本官方文档与仓库实现为准。

本机 Agent 接入环境变量（真实令牌不入库；当前会话值见 `~/.claude/settings.json` 的 env）：

  ANTHROPIC_BASE_URL=http://127.0.0.1:17890
  ANTHROPIC_AUTH_TOKEN=<gateway-token>
  ANTHROPIC_MODEL=my-code
  ANTHROPIC_SMALL_FAST_MODEL=my-code

### 更严格的任务粒度约束

生成实施计划时，必须遵守以下规则：

1. 每个任务只包含一个可独立验证的主要变更。
2. 默认每个任务只需要 2–5 分钟的实际编码工作；复杂任务允许适当延长，但必须说明原因。
3. 每个任务必须明确列出具体文件、修改内容和验证命令。
4. 如果任务同时涉及多个独立业务行为，必须继续拆分。
5. 如果一个任务需要同时实现多个 API、多个业务场景或多个模块，必须继续拆分。
6. 不得将整个模块的实现、完整功能开发或所有测试编写作为单个任务。
7. 拆分必须保持 TDD 流程完整：每个任务有对应的失败测试、最小实现和通过验证。
8. 任务拆分完成后，检查任务之间的依赖关系，确保每个任务可以依据前序任务的明确产物独立执行。

特别注意：任务拆分应减少每个执行者需要阅读的代码和理解的业务范围，而不仅仅是把一个大任务改写成多个标题。


# 自动化产品设计与开发工作流

当用户提出涉及新功能、新页面、交互改版或产品体验优化的任务时，自动执行以下工作流，不要要求用户分别调用 PM Agent 和 UI Agent。

## 阶段一：产品分析

1. 自动调用 `pm` 子 Agent。
2. 分析需求、用户场景、页面结构和交互流程。
3. 涉及页面的任务，生成可交互 HTML 原型。
4. 输出页面需求、原型文件路径、交互说明和验收标准。
5. 将产物写入项目 `docs/prototypes/`，并将正式需求说明写入 `docs/requirements/`。

## 阶段二：界面设计与实现

1. 等待 PM 阶段完成后，自动调用 `ui` 子 Agent。
2. 将 PM 输出的需求文件、原型文件路径和验收标准传递给 UI Agent。
3. UI Agent 先读取项目现有前端结构和组件规范。
4. 在不改变业务规则和后端接口的前提下，完成视觉设计与前端实现。
5. 对照验收标准检查页面，并修复明显的交互和布局问题。

## 阶段三：最终验收

1. 主 Agent 检查两个阶段的输出。
2. 运行适用的测试、构建或静态检查。
3. 检查实现是否满足 PM 的验收标准。
4. 如果不符合要求，自动将具体问题交给对应的子 Agent 修复。
5. 最后向用户汇报完成情况、修改文件和验证结果。

## 调度规则

- 用户只需要描述目标，不需要指定 Agent。
- 页面相关任务必须按 PM → UI 的顺序执行。
- 纯需求分析任务只调用 PM，不必调用 UI。
- 纯视觉优化任务可以直接调用 UI。
- 纯后端任务不必调用 PM 或 UI，除非涉及产品需求变更。
- 不允许仅仅因为创建了子 Agent 文件就认为任务已经执行，必须实际委派任务。
- 如果子 Agent 不可用或产物缺失，明确说明问题，不得假装已完成。
- 涉及删除数据、破坏性操作或重大业务决策时，先征求用户确认。

## 2026-10-29 产品审计与渐进式重构

- 审计产物：`docs/audit/current-state.md`（现状）、`product-review.md`（PM）、`ui-review.md`（UI）、`refactoring-plan.md`（分批任务/回滚/验收）、`design-system.md`（统一规范）。
- 用户已确认的决策：删除 Kun spec（只保留「连接指南」）；接入编辑器改为独立子页/抽屉；主按钮改橙底深字（`AccentText=#18181B`）；保留全局配置卡；危险对话框取消居末位；暂不做独立运行状态页。
- 执行顺序：批次 0 文档裁决 → 批次 1 基础规范与安全 → 批次 2 核心闭环 → 批次 3 页面统一 → 批次 4 可见性与 QA。每批完成须运行 `go build ./...`、`go vet ./...`、`go test ./...`（并发相关加 `-race`）。
