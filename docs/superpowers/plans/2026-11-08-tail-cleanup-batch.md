# 尾款收口批次实施计划（2026-11-08）

> 目标版本：v0.2.1。
> 来源：`docs/product/roadmap.md` §2.2「实施状态明细」记录的未做项 + CLAUDE.md 2026-11-07 设置区/原型对齐遗留。全部为**已立项能力的最后一公里**，无新产品需求，不走 PM/UI 工作流。
> 硬约束：不改网关三入站契约；schema 变更仅限本计划明示项（无）；导出/诊断包不得含明文 Key、prompt/response 正文。

## 范围与不做

**做**：请求历史时间筛选 + CSV/诊断包、半开「恢复中」可见、额度阈值告警、导出加密、复制渠道可选复制 Key、设置区四遗留（候选路径切换 / Skills 错误上浮 / IO 异步化 / 应用接入对话框化）、插件三态、项目选择器搜索与最近排序。

**不做**（本轮明确排除，理由）：
- `CLAUDE_CONFIG_DIR` 支持——新功能非尾款，涉及 claudecfg 全部路径语义，另立项。
- JSON 编辑器行号——受 MyGo TextArea 限制，仅调研（E3）不承诺实现。
- token 用量与供应商控制台对照、系统通知实机验证——人工验收活动，随 F2 记录。
- cc-switch 导入（P1-8）、P2 各项——按 roadmap 触发条件另行决策。

## 子批 A：请求历史收尾（P0-1 尾款）

| 任务 | 内容 | 文件 | 验证 |
|---|---|---|---|
| A1 | `RequestHistoryRepo` 查询支持时间范围（`since` 参数，索引/扫描择优）；bridge `History` 透传 | `internal/storage/request_history_repo.go`、`internal/bridge/bridge.go` | 新测试：插入跨天记录，`since` 过滤后只返回窗口内 |
| A2 | 设置页历史区加时间范围 `uikit.Segmented`（今天 / 24 小时 / 7 天 / 全部，默认全部），切换即按窗口重查 | `internal/desktop/state/state.go`（`Settings` 增字段）、`internal/desktop/pages/settings/settings.go` | 页面测试：切「今天」后调用带 since 的 History |
| A3 | `ExportHistory` 支持 CSV（同数据、脱敏同 JSON 路径；参数选格式） | `internal/app`（ExportHistory 实现）、`internal/bridge/bridge.go` | 单测：CSV 表头 + 行数与 JSON 记录一致、无正文列 |
| A4 | 诊断包：历史（JSON+CSV）+ 当前日志文件 + 渠道/逻辑模型清单（脱敏）打 zip 到 exports 目录 | `internal/app`、`internal/telemetry`（暴露当前日志路径）、`internal/bridge/bridge.go` | 单测：zip 内条目名断言；扫描无明文 Key（复用导出脱敏测试思路） |
| A5 | 「导出诊断」按钮改 `ui.PopoverMenuButton`：导出 JSON / 导出 CSV / 诊断包 三个菜单项 | `internal/desktop/pages/settings/settings.go` | 页面测试：菜单三项分别落对应消息 |

## 子批 B：路由状态可见（P1-6 / P1-4 尾款）

| 任务 | 内容 | 文件 | 验证 |
|---|---|---|---|
| B1 | 冷却快照暴露半开态：`Cooldowns.Snapshot` 条目带 `HalfOpen bool`（半开中=已到期、待探测）；`CooldownView` 增字段 | `internal/router/cooldown.go`、`internal/bridge/bridge.go` | 单测：到期未探测 → HalfOpen=true；`MarkSuccess` 后 false |
| B2 | 渠道卡/逻辑模型卡冷却徽章区分「冷却 12m」与「恢复中（待探测）」两种文案 | `internal/desktop/pages/channels/channels_list.go`、`internal/desktop/pages/logical/logical_view.go` | 页面测试：HalfOpen 条目渲染「恢复中」 |
| B3 | 额度阈值告警：本地渠道配额（P1-2c）使用率 ≥80% 与 ≥100% 各告警一次（沿用 alerts.go 5–10 分钟去重窗口，按渠道去重） | `internal/desktop/alerts.go` | 单测：注入 79%/81%/100% 快照，断言恰两次告警且文案含渠道名 |

## 子批 C：配置安全（P1-1 / P1-7 尾款）

| 任务 | 内容 | 文件 | 验证 |
|---|---|---|---|
| C1 | 导出可选密码加密：`ExportConfig(path, passphrase)`（空=现状明文 JSON v1）；加密为 v2 格式：`{"encrypted":true,"kdf":…,"nonce":…,"ciphertext":…}`，密钥 `crypto/pbkdf2`（stdlib）派生 + AES-256-GCM（复用 credential 包原语风格） | `internal/bridge/config_transfer.go` | 单测：加密导出不可被明文解析；错密码导入失败且报错可读；v1 包导入不受影响 |
| C2 | 导入支持加密包：检测 `encrypted` 标记 → UI 密码输入（`uikit.RichDialog`）→ 解密预览走既有流程 | `internal/bridge/config_transfer.go`、接入/设置页导入入口 | 单测：正确密码解密后与明文导入等价 |
| C3 | 复制渠道可选复制 Key：详情页「复制渠道」与列表行「复制」改弹 `uikit.RichDialog`（含「同时复制 API Key」勾选，默认不勾） | `internal/desktop/pages/channels/channels_detail.go`、`channels_list.go` | 页面测试：不勾→不含 Key（现状文案）；勾→bridge `Copy(id,true)` |

## 子批 D：设置区遗留（CLAUDE.md 2026-11-07）

| 任务 | 内容 | 文件 | 验证 |
|---|---|---|---|
| D1 | 候选路径手动切换：项目本地层 `Candidates` 非空时展示候选切换（下拉或按钮组），切换即重载该路径文档 | `internal/desktop/pages/access/claude/claudecfg/view.go`（projectContextCard）、`internal/bridge/claude_paths.go` | 单测：多候选时切换后 Doc 来自所选路径 |
| D2 | Skills 扫描错误上浮：`ListClaudeSkills` 聚合各来源扫描错误（不中断列表），Skills 卡顶部警示行展示 | `internal/bridge/claude_skills.go`、`internal/desktop/pages/access/claude/claudecfg/skills.go` | 单测：注入不可读目录 → 列表仍返回 + 错误字段非空 → UI 有警示 |
| D3 | 设置页读路径异步化：刷新/切层/切项目的文件读与合并预览计算走既有 Async（`state.Dispatch`），主线程只渲染 | `internal/desktop/pages/access/claude/claudecfg/`（view/save）、`internal/desktop/state/state.go` | 页面测试：慢文件加载时 UI 不阻塞（任务态显示 BusyRow）；既有 dirty 守卫不回归 |
| D4 | 应用接入对话框化：接入列表行「应用接入/重新应用」改 `uikit.RichDialog` 确认——列出将写入的目标文件与受管键变更摘要（复用 P1-5 diff，敏感值遮蔽），确认后才执行 | `internal/desktop/pages/access/access.go` | 页面测试：弹窗出现且取消不写入；确认走既有 ApplyTask |

## 子批 E：UI 打磨（原型对齐 P1）

| 任务 | 内容 | 文件 | 验证 |
|---|---|---|---|
| E1 | 插件行三态：`enabledPlugins` 由 checkbox 改 `uikit.Segmented`（继承 / 开 / 关），与 bool 三态形态一致 | `internal/desktop/pages/access/claude/claudecfg/form.go` | 单测：三态分别写 true/false/删键；合并预览生效 |
| E2 | 项目选择器搜索 + 最近使用排序：候选列表顶部搜索框（复用 `ui.SearchField`）；最近使用的 3 个项目置顶 | `internal/desktop/pages/access/claude/claudecfg/view.go`、`internal/desktop/state/state.go`（持久化最近项目） | 页面测试：过滤命中；选择后置顶排序 |
| E3 | 调研（不编码）：MyGo v0.8.3 TextArea 是否支持行号/gutter——查上游仓库与 release notes，结论写回本计划与 CLAUDE.md | 无代码 | 结论记录 |

**E3 结论（2026-11-08）**：MyGo **v0.8.3 为当前最新版本**（`go list -m -versions` 至 v0.8.3 止），`ui.TextArea` 无行号/gutter API；编辑器内部滚动偏移无公开接口，外部自绘行号列无法可靠同步 → **JSON 编辑器行号维持不做**（错误仍以「第 X 行第 Y 列」文本形式给出，已满足定位需求）。若上游未来提供 gutter/scroll-offset 能力再评估。

## 收尾

| 任务 | 内容 | 验证 |
|---|---|---|
| F1 | 全量 `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...`（A/B/C 涉及存储与并发） | 全绿 |
| F2 | `docs/product/roadmap.md` 实施状态明细回写（勾销本批各项，标注人工验收剩余项）；CLAUDE.md 记录本批决策；`docs/audit/qa-report.md` 增补 | 文档评审 |

## 依赖与顺序

- A1 → A2 → A5；A3 → A4 → A5；B1 → B2；C1 → C2；D、E 内部无依赖。
- 子批间相互独立，可按 A → B → C → D → E 串行，亦可并行。
- 每任务一个提交（`feat/fix/test(scope): 描述`），失败按任务逆序 revert，不跨子批回滚。

## 工作量说明

多数任务 2–5 分钟粒度；超限项及原因：A4（zip 组装 + 脱敏扫描测试，约 15 分钟）、C1+C2（加密格式设计与正反测试，合计约 25 分钟）、D3（异步化触点多，约 20 分钟）、D4（对话框 + diff 集成，约 15 分钟）。

## 执行记录（2026-11-08，本批全部完成）

- **子批 A**：A1 `ListRecent(sinceMs)` 下推 SQL + app/bridge `History(since)`；A2 设置页历史切持久化视图 + 时间范围分段（全部/今天/24小时/7天），顺带修复「重启后历史为空」；A3 CSV 导出；A4 诊断包 zip（历史 JSON+CSV/日志/脱敏渠道与逻辑模型清单）；A5 导出菜单三入口。
- **子批 B**：B1 冷却快照 `HalfOpen`（半开探测条目此前被快照跳过、UI 不可见）；B2 渠道卡/逻辑模型卡「恢复中」徽章；B3 额度阈值告警（本地配额 ≥80%/≥100%，按渠道 24h 去重，OnRecord 10 分钟闸门）。B4（系统通知实机验证）留在人工验收。
- **子批 C**：C1 口令导出 v2 加密信封（PBKDF2-SHA256 60 万轮 + AES-256-GCM，内层 v1 bundle）；C2 导入口令流（`ErrEncryptedExport` → 口令对话框 → 解密预览）；C3 复制渠道确认框可选同时复制 Key（默认不复制）。
- **子批 D**：D1 本地层候选路径手动切换（bridge `LocalPath` 既有语义接线）；D2 Skills 扫描告警上浮（目录不可读/清单损坏）；D3 设置页读路径异步化（computeLoad/applyLoad 拆分 + LoadGen 代次 + BusyRow + uitest.AsyncWaiter）；D4 应用接入确认对话框（目标文件与受管键范围先示后写）。
- **子批 E**：E1 插件行三态分段（跟随下层/启用/停用）；E2 项目选择器搜索 + 最近选择置顶（`RecentProjects`/`RememberProject`）；E3 行号调研结论见上（不做）。
- **验证**：`go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...` 全绿；每任务一个提交。
