# AgentSwitch 渐进式重构计划（2026-10-29）

> 输入：`current-state.md`、`product-review.md`（PM）、`ui-review.md`（UI）、主 Agent 对关键结论的代码复核。
> 本计划**尚未执行**；等待用户确认总体方案后再进入代码改造。
> 硬约束：不改前端框架/组件库，不改数据库 schema，不破坏 bridge/HTTP 契约，不删现有功能，不覆盖用户本地改动。

## 1. 审查结论共识

1. **架构与领域模型健康**：渠道 / 逻辑模型 / 接入配置三层清晰，网关不变量与测试基线（build + test 全绿）良好；不需要推倒重来。
2. **最大风险是「状态不真相」与「操作不可达」**，不是视觉不够漂亮：
   - 逻辑模型改名/删除后，已应用的 Agent 配置文件仍是旧模型名 → Agent 404；
   - 测试连通/拉取模型/Codex 应用在主线程同步执行 → 窗口冻结；
   - 破坏性确认默认聚焦危险按钮，Enter 即执行；
   - 逻辑模型列表、渠道列表、设置页没有滚动，内容多了不可达。
3. **视觉方向本身成立**（中性灰 + 橙主操作 + 卡片流，颜色纪律好）；问题主要来自**共享组件缺失**（页头、表单行、确认框、空态、反馈、加载）与**跨页规则不统一**。
4. **文档漂移需要先裁决**：Kun spec 与实现「有无该功能」级冲突；2026-10-20 spec 与现状冲突；README/ARCHITECTURE 有过期描述。

## 2. 问题合并与冲突裁决

### 2.1 统一 P0（建议最先修，独立于视觉改版）

| 统一 ID | 问题 | 来源 | 证据（已复核） |
|---|---|---|---|
| P0-1 | 破坏性 `AlertDialog` 危险按钮居末位，框架会 Accent + AutoFocus + Enter 触发 | UI + 主 Agent | `shell.go:128`、`logical_view.go:102`、`channels_detail.go:120`、`access.go:382,436,453`、`channels.go:39`；`mygo/ui/feedback.go:181-199`；项目 spec 要求取消居末位 |
| P0-2 | 测试连通 / 拉取模型 / Codex 应用与目录生成在渲染线程同步执行，无 loading/禁用/取消 | PM + UI | `channels_form.go:229,262,270`、`channels.go:173-183`、`channels_fetchmodels.go:53-58`、`codex_catalog.go:211-222`、`codex.go:450-465` |
| P0-3 | 逻辑模型改名/删除后不刷新已应用 live 文件，也无过期提示 | PM + 主 Agent | `logicalmodels.go:194-224` 未调 `RegenerateDerived`；派生刷新只在 `access_configs.go:94/127/141/233`、`bridge.go:64` |
| P0-4 | 逻辑模型列表、渠道列表、设置页没有 `ui.Scroll`，超出视口的内容不可达 | UI + 主 Agent | `logical_view.go:20-85`、`channels_list.go:15-45`、`settings.go:61-154`；对照有 Scroll 的 4 处 |

### 2.2 统一 P1（第二批至第三批）

| 统一 ID | 问题 | 合并自 |
|---|---|---|
| P1-1 | 保存与应用两动作无衔接，首次上手断在「应用」 | PM P1-1 |
| P1-2 | 侧栏切页/返回列表/新建会静默丢弃未保存编辑，接入页却有保护 | PM P2-1 + UI P1-7 |
| P1-3 | 渠道详情测试/拉取使用已保存值，忽略未保存表单；新建页却用草稿值 | PM P1-2 |
| P1-4 | 冷却与路由决策 UI 不可见，README 承诺未兑现 | PM P1-3 + UI P2-19 |
| P1-5 | 成功/失败反馈同色同槽，10 个消息位风格不一 | PM P2-6 + UI P1-3 |
| P1-6 | 接入页签用橙色软底，与「Accent 仅主操作」冲突；伪 tab 无键盘语义 | UI P1-6 |
| P1-7 | 浅色导航 hover 1.01:1、选中底 1.16:1、预设 tile 选中 1.10/1.20 | UI P1-4/P1-5 |
| P1-8 | 接入编辑器在页尾，打开不滚动/不聚焦，错误距字段约 900px | UI P1-1 |
| P1-9 | 逻辑模型编辑态侧栏无选中项 | UI P1-8 |
| P1-10 | 「目标失效」只看渠道删除，未用 `TargetView.Healthy`（停用/无 Key 不提示） | UI P1-9 + PM P2-2 |
| P1-11 | JSON 非法时表单静默失同步，不提示不禁用 | UI P1-10 |
| P1-12 | `MonoFont="Menlo"` 无回退，Linux/Windows 不等宽 | UI P1-11 |
| P1-13 | 接入配置行「更多」仍在同一 Row、无 Wrap；长名称不截断 | UI P1-14 |
| P1-14 | 渠道列表无行内启停，逻辑模型列表有；同概念两套操作模型 | UI P1-15 |
| P1-15 | 隐藏到托盘后二启不唤窗；托盘创建失败时关窗后无出路 | PM P1-4 |
| P1-16 | 文档漂移：Kun spec、2026-10-20 spec、README/ARCHITECTURE | PM P1-5/P2-9 + 主 Agent |
| P1-17 | 白字橙底主按钮对比度 2.80:1（品牌级决策） | UI P1-2 |

### 2.3 冲突与裁决

| 冲突 | 双方观点 | 主 Agent 裁决 |
|---|---|---|
| 是否加搜索/批量 | PM 建议暂缓；UI 建议 >8 项加 SearchField | **按阈值**：列表 >8 项时加搜索；批量操作仍暂缓（收益不明） |
| 保存与应用是否合并 | PM 建议「保存并应用」；UI 更关注编辑器形态 | **两者可并存**：主按钮「保存并应用」+ 次按钮「仅保存」；编辑器形态见决策 D2 |
| 接入页是否卡片化 | UI 明确建议保持行式表格 | **采纳 UI**：接入配置保持行式，统一页头/表单/反馈，不强行卡片化 |
| 全局配置卡去留 | 2026-10-20 spec 要求删除；现状保留 | **保留现状**，把 2026-10-20 spec 标记为部分作废（决策 D5） |
| 主按钮用橙/深橙/深字 | UI 指出对比度不达标 | **品牌级决策 D3**，未拍板前不改 token |
| Kun 是否实现 | spec 要求实现；代码只有 guide | **决策 D1**，未拍板前只修文档、不动代码 |

## 3. 统一设计方向（详见 `design-system.md`）

1. 先修安全与可达性（P0），再统一视觉。
2. 建 6 个共享原语：`PageHeader`、`FormRow`、`ConfirmDialog`、`Feedback`、`EmptyState`、`AsyncButton/LoadingRow`。
3. 页头固定、内容滚动；主操作位置统一。
4. 成功/失败/进行中三态成为一等公民：Toast 用于瞬时结果，内联 Feedback 用于需处理结果。
5. 选中语义收敛：Accent 只给主操作/开关/焦点；导航、页签、预设 tile 用中性选中灰 + 形状/对勾/描边。
6. 桌面化增量：快捷键、搜索、最小窗口尺寸；批量/拖拽暂缓。
7. 可访问性基线：对比度、Label 关联、textScale、等宽回退、长文本省略。

## 4. 分批实施方案

> 每批独立可验收、可回滚。任务粒度按项目 CLAUDE.md 要求：一个任务一个可独立验证的变更，2–5 分钟编码为主，复杂任务注明原因。
> 所有批次均**不涉及数据库迁移**；bridge 只做向后兼容的新增（返回值/快照），不改既有方法语义。

### 批次 0：文档裁决与口径统一（约 0.5 天，无业务代码）

| 任务 | 内容 | 文件 | 验证 |
|---|---|---|---|
| T0.1 | 决策 D1（Kun）落文档：实现则保留 spec 并排期；延后则把 spec 标为「未实现，由连接指南替代」 | `docs/superpowers/specs/2026-10-28-kun-access-design.md` 顶部状态、`README.md` | 文档评审 |
| T0.2 | 决策 D5：把 2026-10-20 spec 中「删除全局配置卡/快捷开关/导入」标为「未采纳/延后」，说明以现状为准 | `docs/superpowers/specs/2026-10-20-access-ccswitch-style-editor-design.md` | 文档评审 |
| T0.3 | 修订 README/ARCHITECTURE 过期描述：元数据可编辑且 per-config；migration v1+v2；连接指南页签 | `README.md`、`ARCHITECTURE.md` | `grep` 关键词对照 |
| T0.4 | 在 CLAUDE.md 记录本次审计产物与最终决策（用户确认后） | `CLAUDE.md`（用户已有未提交改动，增量追加，不覆盖） | diff 审阅 |

**回滚**：纯文档，`git revert` 即可；无数据影响。

### 批次 1：基础规范与安全修复（约 2–3 天）

目标：建立共享组件、修 P0-1/P0-4、统一字体/反馈/页头；不改业务逻辑。

| 任务 | 内容 | 文件 | 验证 |
|---|---|---|---|
| T1.1 | `MonoFont` 改 `"Menlo, monospace"`（P1-12） | `uikit/theme.go`、`theme_test.go` | `go test ./internal/desktop/uikit` |
| T1.2 | 新增 `PageHeader`（标题 + 副标题 + 返回 + 主操作）；四页接入，修正字号漂移 | `uikit/widgets.go`、`pages/*` | 组件单测 + 页面渲染测试 |
| T1.3 | 新增 `FormRow`（LabelWidth 112、Label 关联、必填标记位）；迁移约 30 处表单行 | `uikit/widgets.go`、各页表单 | 单测 + 页面测试 |
| T1.4 | 新增 `Feedback(kind,text)` 与 `Toast` 出口；替换 10 个消息槽的颜色规则 | `uikit/widgets.go`、`state/state.go`、各页 | 单测：success/error/warning 样式不同 |
| T1.5 | 新增 `EmptyState`；统一四页空态（卡中卡/无卡/纯文本） | `uikit/widgets.go`、四页 | 页面测试 |
| T1.6 | 新增 `ConfirmDialog`（取消居末位 + AutoFocus 安全）；迁移 7 处 `AlertDialog`（P0-1） | `uikit/widgets.go`、`shell.go`、三页 | 单测：Enter 触发取消；页面测试 |
| T1.7 | 逻辑模型列表、渠道列表、设置页补 `ui.Scroll`；页头固定在外（P0-4） | `logical_view.go`、`channels_list.go`、`settings.go` | 渲染测试 + 1080×720 手动检查 |
| T1.8 | 浅色导航 hover/选中、预设 tile 选中态加深（P1-7） | `theme.go`、`shell.go`、`channels_new.go` | 主题测试锚点 + 手动检查 |
| T1.9 | 圆角/文本域高度统一：对话框 12、预览 200/240、配置文本等宽 | `uikit/widgets.go`、access 子包 | 单测 + 手动 |
| T1.10 | 可访问性回归测试：对比度计算、textScale 下无固定宽度裁切 | `uikit/*_test.go` | `go test ./internal/desktop/...` |

**风险**：中低（视觉回归）。**回滚**：每个组件一个提交，按任务 revert；无数据变化。

### 批次 2：核心闭环与稳定性（约 3–5 天）

目标：修 P0-2/P0-3 与 P1-1/2/3/10/14/15，让「配置一次、即时生效」重新成立。

| 任务 | 内容 | 文件 | 验证 |
|---|---|---|---|
| T2.1 | `LogicalModels.Update/Remove` 触发派生刷新（P0-3） | `bridge/logicalmodels.go` | 新增测试：应用 Claude/Codex → 改名 → 断言 live 文件更新 |
| T2.2 | `RegenerateDerived` 返回失败摘要；接入页显示「已保存，但文件刷新失败」 | `bridge/access_configs.go`、`pages/access` | 注入只读目录测试 |
| T2.3 | 新增 UI 异步任务状态（running/cancel/progress）与 helper | `state/state.go`、`uikit` | 单测状态流转 |
| T2.4 | 渠道测试连通/拉取模型改异步 + 禁用/Spinner/取消（P0-2） | `channels_form.go`、`channels_detail.go` | Fake Provider 慢响应测试 + 手动 |
| T2.5 | Codex 应用/目录生成改异步 + 缓存模板（P0-2） | `bridge/codex_catalog.go`、`pages/access` | 缓存单测、应用进度手动 |
| T2.6 | 编辑器主按钮改「保存并应用」，保留「仅保存」（P1-1） | `accesskit/editor.go`、access 子包 | 页面测试：保存后 live 文件已写 |
| T2.7 | 全局脏检查：侧栏切页/返回列表/新建前确认（P1-2） | `state/state.go`、`shell.go`、logical/channels | 页面测试：切页弹确认 |
| T2.8 | 渠道详情测试/拉取改用表单草稿值或提示先保存（P1-3） | `channels_form.go` | 单测：改 Key 未保存 → 测试用新值 |
| T2.9 | 目标健康用 `TargetView.Healthy`，显示「渠道停用/无 Key」原因（P1-10） | `logical_view.go`、`logical_form.go` | 页面测试 |
| T2.10 | 侧栏归一化 `logical-edit` → 逻辑模型选中（P1-9） | `shell.go` | 页面测试 |
| T2.11 | 渠道卡 hover + 行内启停，与逻辑模型一致（P1-14） | `channels_list.go` | 页面测试 |
| T2.12 | 注册 `OnSecondInstance` 唤回窗口；托盘失败时关窗策略（P1-15） | `desktop.go`、`cmd/agentswitch/main.go` | 手动二启；模拟托盘失败 |
| T2.13 | 端口变更：派生文件延后到网关重启成功后刷新（P2-4） | `bridge/bridge.go`、`app` | 测试：改端口不重启时 live 仍指旧端口 |

**风险**：中高（并发/异步/文件写入）。**回滚**：任务级提交；异步失败可退回同步路径；无 schema 变更。

### 批次 3：页面 IA 与视觉统一（约 3–4 天）

目标：修 P1-5/6/8/11/13/17（17 取决于决策 D3），统一接入页与设置页。

| 任务 | 内容 | 文件 | 验证 |
|---|---|---|---|
| T3.1 | 接入页加 `PageHeader`；页签改 `ui.Tabs`，选中改中性（P1-6） | `pages/access/access.go`、`uikit` | 页面测试 + 键盘方向键手动 |
| T3.2 | 接入编辑器打开时 ScrollIntoView + AutoFocus；错误靠近保存（P1-8）；若决策 D2 选子页则拆子页 | `access.go`、`accesskit/editor.go` | 页面测试 + 手动 |
| T3.3 | 接入配置行改两行布局/换行，名称 `SingleLine`，次级操作不溢出（P1-13） | `access.go`、`claudedesktop/desktop.go` | 页面测试（长名称） |
| T3.4 | JSON 非法时禁用角色/模型表单并显示错误（P1-11） | `accesskit/roles.go`、`codex.go`、`editor.go` | 单测：非法 JSON 禁用 |
| T3.5 | 设置页：页头、Scroll、FormRow、数字校验、隐藏死开关、保存 Toast | `settings.go` | 页面测试 + 手动 |
| T3.6 | 连接指南：并入页头/空态规范，令牌/地址复制统一 Toast；文档记录 | `guide.go` | 页面测试 |
| T3.7 | Codex 元数据区固定宽度改弹性 + textScale 感知；模型多时折叠 | `codex.go` | 页面测试 + 大字号手动 |
| T3.8 | 全局配置行加列头/标签，空键空值保存前提示 | `access.go`、`accesskit/helpers.go` | 单测 |
| T3.9 | 主按钮配色按 D3 落地；补对比度测试（P1-17） | `theme.go`、`widgets.go` | 对比度单测 |

**风险**：中（IA 变化）。**回滚**：T3.2 若改子页，保留旧编辑器代码一个批次以便回退。

### 批次 4：运维可见性、桌面化与质量验收（约 3–5 天）

| 任务 | 内容 | 文件 | 验证 |
|---|---|---|---|
| T4.1 | 冷却快照经 bridge 暴露；渠道卡/逻辑模型卡显示冷却倒计时（P1-4） | `bridge`、`logical_view.go`、`channels_list.go` | Fake Provider 429 → UI 显示冷却 |
| T4.2 | 最近请求改 `ui.Table`，可展开尝试链/跳过原因（需给 `RequestRecord` 增字段） | `telemetry/ring.go`、`gateway`、`settings.go` | 单测 + 手动 |
| T4.3 | Agent 面向的 503/404 错误中文化，保留机器可读 error type | `gateway/handlers_failover.go`、`handlers_errors.go` | 响应体断言 |
| T4.4 | 应用菜单与快捷键：新建/保存/搜索/切页；窗口最小尺寸 | `desktop.go`、`shell.go` | 手动 + 单元测试（如可断言） |
| T4.5 | 全流程 QA：空态/错误/加载/禁用/取消；Linux + macOS 实机；`go build`、`go vet`、`go test -race ./...` | 全仓 | 验收清单 |

**风险**：中（遥测字段、快捷键冲突）。**回滚**：T4.2 字段为内存结构，回退无数据影响。

## 5. 工作量、风险与验收总表

| 批次 | 预估 | 风险 | 主要验收 |
|---|---|---|---|
| 0 文档裁决 | 0.5 天 | 低 | 文档一致、无矛盾表述 |
| 1 基础与安全 | 2–3 天 | 中低 | 组件单测、P0-1/P0-4 复现用例通过、build/test |
| 2 核心闭环 | 3–5 天 | 中高 | live 文件刷新测试、异步不阻塞、Fake Provider 慢响应、`-race` |
| 3 页面统一 | 3–4 天 | 中 | 页面测试、键盘/大字号/长名称手动检查 |
| 4 可见性与 QA | 3–5 天 | 中 | 冷却可见、错误文案、全流程清单、`-race` 全绿 |

总预估 **12–18 个开发日**（不含用户评审与实机环境准备）。这是量级估计，不是承诺。

## 6. 回滚策略（统一）

1. **数据**：本计划不改 schema、不迁移、不删数据；所有测试使用临时目录；接入 live 写入沿用既有备份机制。
2. **提交**：一个任务一个提交，提交信息含统一 ID（如 `fix(ui): P0-1 confirm dialog safe default`）；每批结束打 tag 或记录批次基线。
3. **行为开关**：T3.2 若改为子页编辑器，保留旧「页尾编辑器」代码一个批次，通过页面常量切换，确认后再删。
4. **失败处理**：任一批次验收不通过，按任务逆序 revert；不跨批次回滚，避免混合状态。
5. **文档**：批次 0 的文档修订独立提交，可与代码解耦回滚。

## 7. 验收方式

- **自动**：`go build ./...`、`go vet ./...`、`go test ./...`、涉及并发/冷却/异步的 `go test -race ./...`；新增组件与页面测试；对比度计算测试。
- **手动**（必须实机）：1080×720 与 textScale 1.25 下逐页检查；慢上游（故意不响应）下测试连通/拉取模型；改名逻辑模型后 Agent 请求；二启唤窗；托盘不可用模拟；键盘 Tab/Enter/Escape。
- **回归对照**：用 `current-state.md` 的功能清单逐项勾选，确认无功能删除或契约变化。
- **未验证项必须标记**：Windows 实机、macOS Apple Silicon、视觉截图链路（当前不存在）。

## 8. 明确不做

- 不换 MyGo、不引入 Web 前端、不引入组件库。
- 不改 SQLite schema、不改 gateway 三入站契约、不改 access 配置文件格式。
- 不做批量操作、拖拽排序、云同步、用量统计、更多 Agent。
- 不做全局重构/目录重排；只新增共享组件并逐页替换。
- 不在未确认真实需求前做端到端新手向导或 Playground。

## 9. 用户决策（2026-10-29 已确认）

| ID | 决策 | 结论 |
|---|---|---|
| D1 | Kun 接入 | **删除 Kun spec**，只保留通用「连接指南」；不做 Kun 专门页签 |
| D2 | 接入编辑器形态 | **独立子页/抽屉**（批次 3 实施；保留列表上下文） |
| D3 | 主按钮配色 | **橙底深字**：背景保持 `#F97316`，`AccentText` 改 `#18181B` |
| D4 | 保存与应用 | **接受「保存并应用」为主按钮**，保留「仅保存」 |
| D5 | 2026-10-20 spec | **保留全局配置卡**；快捷开关、「导入 settings.json」、整份接管均延后/不采纳 |
| D6 | 危险操作强度 | **取消居末位**（安全默认）；删除时附带引用摘要，不要求输入名称 |
| D7 | 独立「运行状态」页 | **暂不做**；先用冷却徽章 + 最近请求展开验证需求 |

已按 D1 删除 `docs/superpowers/specs/2026-10-28-kun-access-design.md`，并在 2026-10-20 spec 顶部加入效力说明。

## 10. 建议的执行顺序

1. 用户确认 D1–D7 与总体方案。
2. 批次 0 → 批次 1（先让应用「安全、可达、样式一致」）。
3. 批次 2（核心闭环与稳定性，优先于更多视觉改造）。
4. 批次 3 → 批次 4（页面统一、可见性、QA）。
5. 每批结束向用户汇报：问题、方案、改动文件、影响页面、测试结果、遗留问题、下一批建议。

## 11. 执行记录（2026-10-29）

- **批次 0 完成**：删除 Kun spec；2026-10-20 spec 顶部加效力说明；README/ARCHITECTURE 修正元数据、migration v2、连接指南描述；CLAUDE.md 记录决策。
- **批次 1 完成**：`ConfirmDialog` 安全默认（取消居末位）、三页补 Scroll、`PageHeader`/`FormRow`/`Feedback`/`EmptyState`/`BusyRow`、橙底深字、等宽回退、导航/预设选中态、消息语义推断、渠道行内启停。
- **批次 2 完成**：逻辑模型增删改/启停触发派生刷新；测试连通/拉取/应用异步化 + 取消；Codex 模板缓存；保存并应用；全局脏检查与切页确认；详情脏表单阻止测试/拉取；目标健康提示；二启唤回；端口变更延后派生刷新；全局配置刷新失败提示。
- **批次 3 完成**：接入编辑器改独立子页；页签改框架 `ui.Tabs`；配置行两行布局 + 分隔线；JSON 非法禁用表单/保存；设置页移除死开关、范围校验、Toast；连接指南复制 Toast；Codex 元数据响应式两行；全局配置列头与行校验。
- **批次 4 完成**：冷却快照经 bridge 暴露，渠道卡/逻辑模型卡/侧栏显示倒计时与原因；请求记录增加尝试链，设置页最近请求可展开；Agent 面向的 404/503/400 错误中文化；窗口最小尺寸；新增 `docs/audit/qa-report.md`。**2026-10-29 复核修正**：应用菜单/Cmd·Ctrl 快捷键未实现，原记录不实；见 `docs/product/feature-gap-analysis.md` B15。
- **尚未完成**：T2.2 仅覆盖全局配置保存（Create/Update/Remove 的派生刷新失败仍只记日志）；搜索/筛选、Cmd/Ctrl+S、详情页逐目标冷却显示留待后续；GUI 视觉与 Windows/macOS 实机未验证。
- **验证**：每批均执行 `go build ./...`、`go vet ./...`、`go test ./...`；批次 1–4 另跑 `go test -race`（全仓），全部通过。详细状态矩阵见 `qa-report.md`。
