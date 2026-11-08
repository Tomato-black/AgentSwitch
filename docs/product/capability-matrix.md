# AgentSwitch 能力对比矩阵（2026-10-29）

> **2026-11-08 状态修订**：本矩阵为 2026-10-29 快照，竞品各列此后未复查；AgentSwitch 列的下列条目已过时，以 `roadmap.md` §1.2/§2.2 实施状态注为准——#9 导入/导出（P1-1 已完成，含可选口令加密 v2 信封）、#12 用量/成本（P1-2a/b/c 已完成：token 统计含流式、日聚合、可编辑价格表估算、DeepSeek/Kimi/OpenRouter 余额）、#14 历史与故障排查（P0-1 已完成持久化 + 时间筛选 + CSV + 诊断包，重启不清零）、#20 通知/告警（P1-4 已完成：全渠道冷却/连续切换/额度耗尽/额度阈值，应用内 + 系统通知——后者待实机验证）、#25 写入一致性（P0-0 已修复）、#27 配置差异（P1-5 已完成键级 diff）、#28 搜索/筛选（P1-3 已完成列表/预设/项目选择器搜索）；#8 备份已升级列表 + 选择恢复；#10 半开探测已实现（含「恢复中」可见）。仍 ❌/未变的：自动更新、完整多 Key 池、更多 Agent、条件路由、云同步、多租户；Windows 实机与真实 Agent smoke 仍未验证。

> 配套文档：`competitive-analysis.md`（竞品证据与来源）、`feature-gap-analysis.md`（问题分类）、`feature-backlog.md`（RICE 与候选池）、`roadmap.md`（阶段计划）、`decisions.md`（待拍板决策）。
>
> 状态口径：
> - **AgentSwitch**：`✅ 已验证` = 代码 + 离线测试覆盖核心行为；`○ 已实现·未实机` = 代码/测试存在，但需真实 Agent/OS 验证；`◐ 部分实现` = 只覆盖部分场景或存在明确缺口；`❌ 未实现`；`⚠ 现有缺陷`。
> - **竞品**：`✅ 官方明确支持`；`◐ 部分/有条件`；`❌ 未见或明确不支持`；`? 本次未核实`；`— 不适用`。
> - 竞品支持不等于质量与稳定性；质量证据见 `competitive-analysis.md` §3 与 §5.2。
> - 访问日期：2026-10-29。竞品事实来自官方 README/文档/Release/Issue，未实机安装。

---

## 1. 维度与选择理由

除任务要求的 15 个维度外，本矩阵增加 5 个对 AgentSwitch 定位至关重要的维度：

| 新增维度 | 为什么必须看 |
|---|---|
| 写入一致性与备份安全 | AgentSwitch 的承诺是「只改受管键、写前备份」；本次审查发现「仅保存」对已应用配置会直接改 live 且不备份（见 §3.1），这是信任底线，必须单独成维度 |
| 真实 Agent 兼容性门禁 | 所有网关竞品的 Issue 都集中在协议转换/流式/工具调用的边界错误；离线测试通过不等于真实 Agent 可用 |
| 配置差异/变更审计 | 用户需要知道「应用后到底改了什么」，也是排障与回滚的前提 |
| 搜索/筛选/排序 | 条目规模增长后的基础可用性；竞品普遍具备 |
| 桌面效率（菜单/快捷键） | AgentSwitch 是桌面工具，但当前无应用菜单与快捷键；需明确是否为当前优先级 |

---

## 2. Part A：能力支持矩阵

| # | 维度 | AgentSwitch | CC Switch | CCR | 9Router | LiteLLM | new-api |
|---:|---|---|---|---|---|---|---|
| 1 | 模型供应商管理 | ◐ 20 条双协议预置；自定义渠道；单 Key/渠道 | ✅ 90+ 预置；官方/自定义；多账号（Beta） | ✅ 预置/自定义；协议探测；模型发现；凭证池 | ✅ 40+ providers；OAuth/免费/API Key/自托管 | ✅ 100+ providers | ✅ 多渠道/多 Key/多分组 |
| 2 | 多模型配置 | ✅ 逻辑模型目标链（渠道·模型，有序） | ✅ 模型映射；Aggregation 多供应商模型列表 | ✅ 模型目录/别名/虚拟模型/条件路由 | ✅ combos/aliases | ✅ model list/router | ✅ 模型映射 |
| 3 | 端点/协议适配 | ◐ 入站 Anthropic Messages、OpenAI Chat、OpenAI Responses；上游 Anthropic/OpenAI Chat | ✅ Anthropic Messages、OpenAI Chat、OpenAI Responses、Gemini Native | ✅ OpenAI Chat/Responses、Anthropic、Gemini 等 | ✅ OpenAI/Claude/Gemini/Cursor/Kiro/Vertex/Ollama/Responses | ✅ 100+ providers，多端点 | ✅ 四种文本协议 + 多模态/Realtime |
| 4 | Claude Code 接入 | ✅ 文件投影 `~/.claude/settings.json`；角色映射；1M | ✅ 切换/路由/聚合；热切换 | ✅ profile/启动入口/CLI & APP | ✅ Dashboard 配置 CLI 工具 | ◐ 网关/harness，不写客户端配置 | ◐ 暴露端点，不写客户端配置 |
| 5 | Codex 接入 | ✅ `config.toml` 受管键；模型目录；per-config 元数据；子代理/自动审批 | ✅ 切换/路由/聚合；Codex 模型列表 | ✅ profile/启动入口/CLI & APP | ✅ Dashboard 配置 CLI 工具 | ◐ 网关/harness | ◐ 暴露端点 |
| 6 | 其他 Agent 覆盖 | ❌ 仅 Claude Code / Codex / Claude Desktop + 通用指南 | ✅ 12 工具 | ✅ 10+ | ✅ 15+ | ◐ 多 harness | ❌ |
| 7 | 配置切换与反馈 | ◐ 应用/重新应用；保存并应用；无 diff；无「待应用」状态 | ✅ Direct/Routing/Aggregation；预览；热切换；项目 | ✅ profile/scope/启动入口；规则 | ✅ Dashboard/CLI 配置 | ◐ 服务端配置 | ◐ 控制台 |
| 8 | 备份/恢复 | ◐ 每 Agent 最近 10 份；仅还原最近一份；无备份时间/列表 | ✅ 首写备份 + 每日备份 + 项目 | ? 未核实 | ✅ 自动备份 + 云同步 | ◐ DB/部署备份 | ◐ DB/部署备份 |
| 9 | 导入/导出/迁移 | ❌ 无配置导出/导入/迁移 | ✅ 导入导出 + Deep Link + 云同步 | ◐ 本地登录/供应商导入 | ◐ 云同步 | ◐ config/DB | ◐ DB/config |
| 10 | 可用性检测 | ◐ 手动测试连通 + 反应式冷却；无定时探测 | ◐ 连通性检查；README 称健康监控/熔断 | ✅ 协议探测/连通性/模型发现 | ◐ 配额跟踪 + 错误 fallback | ◐ health/router | ◐ 渠道测试（未核实） |
| 11 | 配额/错误/限流反馈 | ◐ 冷却 + 尝试链 + 中文错误；无配额/余额 | ✅ 配额/余额/用量/请求日志 | ✅ 配额/trace/account status | ✅ 配额窗口/成本估算 | ✅ 预算/虚拟 Key | ✅ 配额/订阅/日志 |
| 12 | 用量/成本统计 | ❌ 无 token/成本/按渠道聚合 | ✅ usage/heatmap/custom pricing | ✅ token/cost/latency/traces | ✅ usage analytics | ✅ spend tracking | ✅ usage logs |
| 13 | 多配置/环境隔离 | ◐ 每 Agent 多配置 + 全局 env + 启动器 | ✅ 项目/WSL/云同步 | ✅ profile/scope/多实例/客户端 Key | ✅ 多账号/combos | ✅ team/project | ✅ user/group |
| 14 | 历史与故障排查 | ◐ 20 条 UI 请求 + 100 条内存环 + 尝试链 + 轮转日志；重启清零 | ✅ 会话/日志/用量 | ✅ 请求/响应详情/trace | ✅ 请求/翻译日志 | ✅ 日志/回调/dashboard | ✅ 日志/playground |
| 15 | 扩展机制 | ◐ 用户预置 JSON；无插件 API | ✅ 预置/Deep Link/Universal Provider | ✅ 插件/ToolHub/Fusion/本地路由/虚拟模型 | ✅ combos/provider data/headroom | ✅ SDK/callback/guardrail | ✅ JS 插件/任务插件 |
| 16 | 本地数据与隐私 | ✅ loopback + AES-GCM + 零遥测 + 无云 | ◐ 本地 SQLite + 可选 WebDAV/S3 | ◐ 本地 SQLite | ◐ 本地 SQLite；默认弱安全 | ◐ 自托管；多租户 | ◐ 自托管；多用户 |
| 17 | 易用性/新手引导 | ◐ 4 页；无端到端向导；无搜索；空态有引导 | ✅ GUI/预置/i18n/首次导入 | ◐ 功能多、概念多 | ◐ dashboard/概念多 | ❌ 服务端配置 | ❌ 运营配置 |
| 18 | 安装/升级/容灾 | ◐ 单二进制；无自动更新；Win/ARM 未实机；破坏性变更需删库 | ✅ 自动更新/云备份/跨平台 | ◐ npm/桌面/Docker | ◐ npm/Docker/PM2 | ◐ 多部署/迁移复杂 | ◐ Docker/DB 迁移 |
| 19 | 多账号/密钥池 | ❌ 单 Key/渠道（可用重复渠道近似） | ✅ 多账号（Beta） | ✅ 凭证池/轮换 | ✅ 多账号/轮询 | ✅ virtual keys | ✅ 多 Key |
| 20 | 通知/告警 | ❌ 无故障/冷却/额度告警 | ? 未核实 | ? 未核实 | ? 未核实 | ? 未核实 | ? 未核实 |
| 21 | 会话粘性/亲和 | ❌ 无 | ◐ Issue 有需求 | ◐ Release 修 Codex affinity | ? 未核实 | ? 未核实 | ✅ 渠道亲和 |
| 22 | 条件路由/请求改写 | ❌ 无 | ? 未核实 | ✅ header/body/prefix/rewrite | ❌ 未见 | ✅ routing policies | ✅ 模型映射/重试 |
| 23 | 团队/多租户 | ❌ | ❌ | ◐ 客户端 Key/额度 | ❌ | ✅ | ✅ |
| 24 | 云同步 | ❌ | ✅ WebDAV/S3 | ? | ✅ | ❌ | ❌ |
| 25 | **写入一致性与备份安全** | ⚠ **现有缺陷**：对已应用配置点「仅保存」会直接改 live，且不备份；`RegenerateDerived` 写 live 不备份 | ✅ 首写/每日备份；写入前备份 | ? 未核实 | ✅ 自动备份 | ◐ DB/部署 | ◐ DB/部署 |
| 26 | **真实 Agent 兼容性门禁** | ◐ 三入站有大量离线测试；无 CI 真实 Agent smoke；Windows/ARM 未实机 | ◐ 有真实用户与 Issue，但兼容性仍是痛点 | ◐ 有真实用户与 Issue | ◐ 有真实用户与 Issue | ◐ harness | ◐ 端点可用性有 Issue |
| 27 | **配置差异/变更审计** | ❌ 编辑器有应用后预览，但无「当前文件 vs 应用后」diff | ✅ 预览切换后配置 | ? 未核实 | ? 未核实 | ◐ 配置中心 | ◐ 日志 |
| 28 | **搜索/筛选/排序** | ❌ 列表与 20 条预设均无搜索/筛选/排序 | ✅ | ✅ | ✅ | ◐ | ✅ |
| 29 | **桌面效率（菜单/快捷键）** | ❌ 仅托盘菜单；无应用菜单/快捷键（`TestAppMenuShortcuts` 不存在） | ✅ 托盘/快捷键/自动更新 | ◐ 桌面/CLI | ◐ Dashboard | — | — |

---

## 3. Part B：AgentSwitch 差距分析

> 「现状证据」以本次代码核查与本文第 5 节为准；「竞品证据」见 `competitive-analysis.md` §9 来源清单。

### 3.1 先修：写入一致性与备份安全（P0）

| 项 | 内容 |
|---|---|
| 现状 | 对**当前已应用**的接入配置点「仅保存」时，`AccessConfigs.Update` → `RegenerateDerived` 会对 current 配置执行 `writeClaudeLive`/`writeCodexLive`，**不调用 `backupLive`**。全局配置保存同样会重写 live 且不备份。 |
| 代码证据 | `internal/bridge/access_configs.go:101-128`（Update→RegenerateDerived）、`:258-300`（RegenerateDerived 对 current 写 live）、`internal/bridge/access_live.go:173-260`（备份只在 `Apply`/`RestoreLatestBackup` 调用）；UI 的「仅保存」入口在 `internal/desktop/accesskit/editor.go:85-96,163-223`。 |
| 用户影响 | 「仅保存」不是仅保存；「还原最近备份」无法撤销这次保存/应用，安全网失效；README「写入前备份」的承诺对这条路径不成立。 |
| 竞品对照 | CC Switch 首写前备份、每日备份；9Router 自动备份；LiteLLM/new-api 以 DB/部署备份为主。竞品也没有完全等价的「仅保存」语义，但备份安全是共同底线。 |
| 复杂度 | S–M（拆分「落库」与「投影」、任何 live 写入前备份、补「待应用」状态与测试） |
| 建议 | **P0 立即修**，先于任何新功能。验收：编辑 current → 仅保存 → live 字节不变；保存并应用 → 最新备份内容等于保存前 live；列表出现「有未应用的修改」。 |

### 3.2 其余差距

| 维度 | AgentSwitch 现状与证据 | 差距 | 用户影响 | 复杂度 | 建议处理 |
|---|---|---|---|---|---|
| 模型供应商管理 | 20 条预置；自定义渠道；单 Key。 | 竞品 40–100+ 预置、多账号、协议探测/模型发现。 | 中：中文 Coding Plan 覆盖够用，长尾供应商需手填；多账号需建重复渠道。 | M | **补齐（P1/P2）**：保持精选预置，增加渠道复制、导入/自定义预置；不追 90+。 |
| 多模型配置 | 逻辑模型目标链；无权重/轮询/条件。 | 竞品有模型映射、聚合、权重、条件路由。 | 中：主备够用；多账号轮询/成本优先不够。 | M | **保持**：目标链是核心差异；P2 再评估权重/轮询，不引入条件 DSL。 |
| 端点/协议适配 | 3 入站 / 2 上游协议；离线测试丰富。 | 竞品支持 Gemini Native、更多上游格式。 | 中：中文用户影响小；Gemini CLI 用户受影响。 | L | **观察**：先保 Claude/Codex 真实兼容；Gemini 视目标市场再定。 |
| Claude Code 接入 | 文件投影、角色映射、1M、全局 env、非默认启动器。 | 竞品有热切换/项目/多账号。 | 中：重启成本低；项目隔离缺失影响多项目用户。 | M | **保持 + P1 小补**：明确重启提示；配置集/项目语义可用接入配置近似。 |
| Codex 接入 | config.toml 受管键、目录、per-config 元数据、子代理、自动审批、只读预览。 | 竞品有 Codex 路由/聚合；AgentSwitch 目录/元数据更深，但无多账号/会话粘性。 | 中：Codex 用户是核心；目录/元数据是优势。 | M | **保持**：这是现有差异化；补「重启/新会话」提示与失效徽章。 |
| 其他 Agent 覆盖 | 3 类 + 通用指南。 | 竞品 10–15+。 | 中：限制获客面；每加一个都有维护成本。 | L | **观察/按需**：只加 OpenAI/Anthropic 兼容且配置稳定的 Agent（Gemini CLI/OpenCode 候选）。 |
| 配置切换与反馈 | 应用/重新应用、保存并应用、Toast；无 diff、无「待应用」状态。 | 竞品有预览/diff/项目/热切换。 | 中：用户不知道「改了什么、写入了什么」。 | S–M | **补齐（P1）**：配置差异预览；「待应用/已失效」状态；应用结果靠近触发点。 |
| 备份/恢复 | 最近 10 份，仅还原最近一份。 | 竞品有备份列表/项目/云备份。 | 高：改坏后无法回到更早版本。 | S–M | **补齐（P0/P1，与写入一致性同批）**：备份列表/时间/选择恢复；不做云同步。 |
| 导入/导出/迁移 | 无。 | 竞品普遍有导入导出/Deep Link/云同步。 | 高：配置是资产；换机/重装成本高。 | M | **补齐（P1）**：全量导出/导入（默认不含明文密钥）、版本化 schema、导入预览。 |
| 可用性检测 | 手动测试 + 反应式冷却；无定时探测。 | 竞品有连通性/协议探测/健康监控/配额。 | 中：首次失败后才冷却；用户可能反复撞坏渠道。 | M | **补齐（P1）**：冷却到期半开探测；定时探测默认关闭。 |
| 配额/错误/限流反馈 | 冷却 + 尝试链 + 中文错误；无配额/余额。 | 竞品有配额/余额/用量/trace。 | 高：用户不知道「还剩多少、为什么被限」。 | M–L | **分两步（P0/P1）**：先做请求/错误/冷却历史；配额探测 P2（供应商差异大）。 |
| 用量/成本统计 | 无 token/成本/按渠道聚合。 | 竞品普遍有。 | 高：无法回答「这个月花了多少、哪个渠道最贵」。 | M–L | **补齐（P1，战略上调）**：token/请求/渠道/模型/时间；成本用可编辑价格表，标注估算。 |
| 多配置/环境隔离 | 每 Agent 多配置 + 全局 env + 启动器。 | 竞品有项目/scope/多实例/客户端 Key。 | 中：多项目用户需要「项目切换」。 | M | **观察/P1**：评估「配置集/项目」轻量方案；不做多租户。 |
| 历史与故障排查 | 20 条 UI 请求 + 100 条内存环 + 尝试链 + 轮转日志。 | 竞品有持久化日志/搜索/导出/trace。 | 高：重启清零；长会话排障困难。 | M | **补齐（P0）**：持久化元数据、搜索/筛选、导出诊断包；不存 prompt/response 正文。 |
| 扩展机制 | 用户预置 JSON；无插件 API。 | 竞品有插件/Deep Link/Universal Provider。 | 低–中：个人用户不需要插件；导入/导出预置有价值。 | M | **保持 + 小补**：预置 JSON + 导入/导出；不建插件系统。 |
| 本地数据与隐私 | loopback、AES-GCM、零遥测、无云。 | 竞品多为本地/自托管，但 CC Switch/9Router 有云同步；9Router 默认弱安全。 | 正：差异化；需在 UI/文档讲清楚。 | S | **保持**：导出默认脱敏；不引入云。 |
| 易用性/新手引导 | 4 页、空态引导、无端到端向导、无搜索。 | 竞品有预设/导入/引导/i18n。 | 高：首次上手 12–18 次交互；条目多后找不到。 | S–M | **补齐（P1）**：三步清单 + 测试请求 + 搜索；不做大而全向导。 |
| 安装/升级/容灾 | 单二进制；无自动更新；Win/ARM 未实机；破坏性变更需删库。 | 竞品有自动更新/云备份/跨平台验证。 | 高：Windows 用户无法确认可用；手动升级有摩擦。 | M–L | **P0 质量门禁 / P2 自动更新**：先实机验证；自动更新延后到签名/公证。 |
| 多账号/密钥池 | 单 Key/渠道。 | 竞品普遍多账号/凭证池。 | 中–高：订阅额度用户需要；变通是重复渠道。 | L | **观察/轻量（P1/P2）**：先渠道复制 + 主备链；完整密钥池视证据。 |
| 通知/告警 | 无。 | 竞品未完全核实；运维需求明确。 | 中–高：全渠道冷却/额度耗尽时用户不知道。 | S–M | **补齐（P1）**：应用内/托盘告警；MyGo 支持系统通知，可后续验证。 |
| 会话粘性/亲和 | 无。 | CC Switch Issue 有需求；CCR 修 Codex affinity；new-api 有渠道亲和。 | 中：多目标切换会打断 prompt cache/会话。 | L | **观察（P2）**：先提示「切换可能导致缓存失效」；不做复杂粘性。 |
| 条件路由/请求改写 | 无。 | CCR/LiteLLM/new-api 有。 | 低–中：高级用户需要；普通用户不需要。 | L | **不做（当前）**：避免把简单产品做成规则引擎。 |
| 团队/多租户 | 无。 | LiteLLM/new-api 有。 | 低：非目标用户。 | XL | **不做**：与单机桌面定位冲突。 |
| 云同步 | 无。 | CC Switch/9Router 有。 | 中：多机用户需要；与隐私定位冲突。 | L | **不做**：用加密导出/导入替代。 |
| 配置差异/审计 | 无 diff；只有应用后预览。 | CC Switch 有预览；LiteLLM 有配置中心。 | 中：应用前不知道改了什么。 | S | **补齐（P1）**：受管键 diff（不含密钥）。 |
| 搜索/筛选/排序 | 无。 | 竞品普遍有。 | 中：>8–10 条后效率下降。 | S | **补齐（P1）**：阈值出现；批量暂缓。 |
| 桌面效率 | 无应用菜单/快捷键；仅托盘。 | CC Switch 有托盘/快捷键；CCR 桌面/CLI。 | 中：桌面工具效率不足。 | S | **P2**：先 App 菜单 + Cmd/Ctrl+S/F/1–4；不做命令面板。 |

---

## 4. Part C：优势与短板排序

### 4.1 应保持的优势（不要为了「补齐」而破坏）

1. **逻辑模型目标链**：一个逻辑模型 → 跨渠道、跨模型名的有序故障转移；与 CC Switch 的 Routing（供应商队列）/Aggregation（按模型分流、无故障转移）形成差异。
2. **故障转移不变量**：首字节前切换、流开始后不重放、客户端取消取消上游；竞品 Issue 中反复出现流中断/错误重放问题，这是可守住的可靠性底线。
3. **本地隐私默认**：loopback、AES-GCM、零遥测、无云；与 CC Switch/9Router 的云同步和弱默认形成对比。
4. **单二进制/少依赖**：Go + MyGo，无 Node/Python/Redis；部署与供应链更简单。
5. **Codex 深度**：config.toml 受管键、模型目录、per-config 元数据、子代理、自动审批、只读预览；目前比多数竞品更深。
6. **中文 Coding Plan 预置**：20 条精选双协议，比泛化 90+ 更适合目标用户；保持质量而非数量。

### 4.2 最需要补的短板（按用户影响）

1. **写入一致性/备份安全**（P0 缺陷）：保存/应用/备份语义不一致，直接破坏信任。
2. **历史与故障排查**（高）：20 条内存记录无法支撑「为什么切了」的信任。
3. **真实 Agent 兼容性门禁**（高）：离线测试不等于真实 Claude Code/Codex 可用。
4. **配置迁移/备份列表**（高）：配置是资产；不能只还原最近一份、不能导出。
5. **用量/配额**（高）：用户不知道消耗与剩余；订阅额度用户尤其痛。
6. **端到端验证**（高）：首次接入后没有「发一条测试请求」的闭环。
7. **多账号/密钥池**（中–高）：重复建渠道是可用但笨拙的变通。
8. **可用性检测/告警**（中–高）：反应式冷却让用户先撞一次失败。
9. **易用性/搜索/引导**（中）：首次上手路径长；条目多后找不到。
10. **平台验证**（高质量门禁）：Windows/macOS ARM 未实机是发布风险。

### 4.3 不应补的「竞品功能」

- 12+ Agent、MCP/Skills/Prompts、会话阅读、Token 压缩、云同步、多租户/计费、Guardrails/语义缓存、条件路由 DSL、插件系统、远程访问/移动端。
- 理由：不是 AgentSwitch 的定位需要，且会显著增加维护面/隐私风险/认知负担。详见 `decisions.md`。

---

## 5. 证据索引

### AgentSwitch（代码/测试）

- 能力基线：本文 §2–§4 与下列代码路径（本次 `go build ./...`、`go test ./...` 通过；59 个 `_test.go`、537 个 `Test*`）
- 三入站与鉴权：`internal/gateway/server.go:69-75,126-130`
- 逻辑模型/目标链：`internal/bridge/logicalmodels.go`、`internal/storage/db.go:43-56`
- 冷却/故障转移：`internal/router`、`internal/gateway/handlers_failover.go`
- 遥测：`internal/telemetry/ring.go`（100 条内存环）、`pages/settings/settings.go`（20 条 UI）
- 接入写入/备份：`internal/bridge/access_configs.go:101-128,258-300`、`internal/bridge/access_live.go:173-260`
- 编辑保存：`internal/desktop/accesskit/editor.go:85-96,163-223`
- 渠道/逻辑模型/接入页：`internal/desktop/pages/*`
- 预置：`internal/presets/default.json`（20 条）
- 测试：59 个 `_test.go`、537 个 `Test*`；本次 `go build ./...`、`go test ./...` 通过

### 竞品（访问日期 2026-10-29）

- CC Switch：<https://github.com/farion1231/cc-switch>；README <https://raw.githubusercontent.com/farion1231/cc-switch/main/README.md>；Issues <https://github.com/farion1231/cc-switch/issues>
- Claude Code Router：<https://github.com/musistudio/claude-code-router>；README <https://raw.githubusercontent.com/musistudio/claude-code-router/main/README.md>；Issues <https://github.com/musistudio/claude-code-router/issues>
- 9Router：<https://github.com/decolua/9router>；README <https://raw.githubusercontent.com/decolua/9router/master/README.md>
- LiteLLM：<https://github.com/BerriAI/litellm>；README <https://raw.githubusercontent.com/BerriAI/litellm/main/README.md>
- new-api：<https://github.com/QuantumNous/new-api>；README <https://raw.githubusercontent.com/QuantumNous/new-api/main/README.md>；one-api <https://github.com/songquanpeng/one-api>

> 说明：GitHub Star/Issue 数是页面快照，不代表真实用户量或市场份额；Issue 是用户报告，不等于官方确认的缺陷。未实机安装竞品，功能细节以官方文档为准。
