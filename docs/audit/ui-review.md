# AgentSwitch 桌面 UI/UX 独立审查（2026-10-29）

> **方法限制（必读）**：本审查**没有启动 GUI、没有截图、没有实机逐屏走查**。所有视觉结论来自 `internal/desktop` 的 Go 渲染代码、`uikit` 设计 token 与 MyGo v0.6.2 框架源码的交叉阅读；文中出现的尺寸（内容区 848×688 等）是按 `desktop.go` 的固定窗口 1080×720、侧栏 200、内容 padding 16 计算的**估算值**，不是像素级实测。凡"首屏能看到什么"均属代码推断，实机确认前不应作为最终验收依据。
>
> 审查期间**未修改任何业务代码**，只新增本文件。工作区 `CLAUDE.md` 的用户未提交改动未触碰。

## 1. 审查范围与方法

**阅读对象**

- 组合根：`internal/desktop/desktop.go`、`shell.go`、`closemodal.go`、`tray_linux.go`
- 设计系统：`internal/desktop/uikit/theme.go`、`widgets.go`、`icons.go`
- 共享状态：`internal/desktop/state/state.go`
- 页面：`pages/logical/*`、`pages/channels/*`、`pages/access/*`（宿主 + `claude`/`codex`/`claudedesktop`/`guide`）、`pages/settings/settings.go`、`accesskit/*`
- 测试夹具：`uitest/uitest.go`、各页 `*_test.go`（只用于确认渲染/断言方式）
- 设计文档：`2026-10-17-desktop-ui-ccswitch-redesign-design.md`、`2026-10-17-logical-models-ccswitch-ui-design.md`、`2026-10-20-access-ccswitch-style-editor-design.md`、`2026-10-27-codex-access-models-metadata-design.md`
- 框架能力核对：MyGo v0.6.2 的 `ui/feedback.go`（AlertDialog 按钮语义）、`ui/scroll.go`（Scroll/ScrollIntoView）、`ui/input.go`（AutoFocus）、`ui/element.go`（字体回退）、`menu.go`（应用菜单/加速键）

**核对方式**

1. 逐页阅读渲染函数，记录布局容器、间距、按钮、反馈位、状态来源。
2. 对照 token 表检查硬编码色值：除 `closemodal.go:20` 的遮罩 `ui.RGBA(0,0,0,0.45)` 外，页面代码**没有**散落十六进制色值，颜色纪律良好。
3. 用 WCAG 相对亮度公式计算关键前景/背景对比度（见 §2.4）。
4. 对"是否存在滚动/焦点/快捷键"做全仓 grep：`ui.Scroll` 仅 4 处，`ScrollIntoView`/`AutoFocus`/`FocusGroup`/`Shortcut` 在 `internal/desktop` 中**零命中**。
5. 阅读部分 `bridge` 视图结构，确认 UI 已有哪些数据可展示（如 `TargetView.Healthy`、`Recent` 遥测）。

**未做的事**：未运行 `go tool mygo dev`（本机无可用截图链路）；未验证实际字体渲染、焦点环、滚动条外观、DPI 缩放与 Windows 表现。这些应作为后续实机走查项。

## 2. 设计系统现状

### 2.1 Token 清单

`theme.go` 以 `ui.LightTheme()/DarkTheme()` 为底，覆写 **19 个颜色 token**（`theme.go:34-72`）：

| 组 | token | 备注 |
|---|---|---|
| 底色 | Background / Surface / SurfaceHover / SurfacePressed / Border | 浅色 Surface=Background=#FFFFFF，卡片只靠 1px 边框区分 |
| 文字 | Text / TextMuted / Selection | Selection 仅文本选区，保持暖橙 |
| 品牌操作 | Accent / AccentHover / AccentPressed / AccentText | 主按钮、Switch、焦点环 |
| 语义 | Success / Danger / Warning | 绿橙分离已落实 |
| 其他 | Focus / Inverse / InverseText / Scrollbar | 深色 Focus 降饱和 |
| 应用级 | SelectedColor / SidebarColor | 不在 `ui.Theme` 中，按主题取值（`theme.go:118-132`） |

**软色板**（`theme.go:84-113`）：`Act/Ok/Warn/Danger/Gray` 各一对「软底 + 前景字」+ 单值 `Link`，明暗两套完整。另有 `tileRots` 三组蓝/紫/青（`widgets.go:87-91`），合计五组色相，符合"不超过五组防彩虹"。

**度量**：`Radius=8`、卡片显式 12、胶囊 999、`Spacing=4`、`FontSize=13×textScale`；侧栏 200、内容 padding 16、页面列 gap 12。

### 2.2 组件与视觉词汇

| 组件 | 位置 | 使用面 | 缺口 |
|---|---|---|---|
| `Card`（12 圆角 / 14 padding / Surface + 1px Border） | `widgets.go:17-19` | 逻辑模型卡、渠道详情/新建卡、接入各卡、设置卡 | 渠道列表的**空态**没进 Card（`channels_list.go:31-39`） |
| `SoftBadge`（999 / FontSize-2 / 软色对） | `widgets.go:40-44` | 启用/停用/有 Key/使用中/默认/预留 | 无 |
| `PillButton`（Accent 实心胶囊） | `widgets.go:50-52` | 各页主操作 | 无 disabled/loading 变体 |
| `SoftButton`（软底胶囊，kind 驱动） | `widgets.go:58-61` | 次级/危险操作、**接入页页签** | 被当作 tab 使用，语义过载 |
| `Tile`（40/32 圆角色块 + 名称前 2 字） | `widgets.go:113-135` | 渠道列表、预设网格 | 逻辑模型/接入配置没有等价身份标识，视觉锚点只存在于渠道页 |
| `LedDot`（10px 常亮点） | `theme.go:182-184` | 侧栏底条 | 无 |
| 图标（5 枚 16px 线性 SVG） | `icons.go` | 侧栏 4 项 + 关窗模态 | 页面内无图标，按钮全靠文字 |
| `MonoFont = "Menlo"` | `theme.go:20` | 地址、BaseURL、日志路径、请求行、侧栏地址 | **无字体族回退**（见 P1-11） |
| `StateWord` / `ReasonWord` | `theme.go:135-157` | 状态词/降级原因 | 原因可能很长，展示处无省略号 |

**缺失的共享原语**（导致各页重复手写）：`PageHeader`、`FormRow`（标签宽度/必填/错误）、`TabBar`/`Segmented`、`ConfirmDialog`（安全默认）、`StatusLine`（成功/失败/警告）、`Toast`、`EmptyState`、`ListRow`、`SearchField`、`SectionHeader`、`StickyActionBar`。

### 2.3 布局与空间预算（按代码计算）

- 窗口 1080×720（`desktop.go:204`），侧栏固定 200（`desktop.go:72`），右列 padding 16（`desktop.go:84`）。
- 内容区约 **848×688**；扣页面头 30 + gap 12 ≈ **646px 可用高度**。
- 渠道卡约 94px/张 → 首屏约 **6 张**；逻辑模型卡约 82px/张 → 约 **7 张**；之后没有滚动（P0-3）。
- 接入页在 0 条配置时约 330px 后进入编辑器；编辑器（Claude 约 700px+、Codex 约 900px+）远超视口。
- 设置页三卡在 textScale=1 时约 640px，**贴着视口上限**；textScale 稍大即溢出且无滚动。

### 2.4 明暗主题与对比度

明暗两套 token 成对齐全，没有"某一边缺失"的字段；软色板与 tile 轮转色也各有一套。问题不在覆盖率，而在**若干组合的对比度不达 AA**（13px 正文需 4.5:1）：

| 组合 | 色值 | 对比度 | 结论 |
|---|---|---:|---|
| AccentText on Accent（所有主按钮） | #FFFFFF / #F97316 | **2.80** | 不达 AA，也不达大字号 3.0（`theme.go:42-45`） |
| AccentText on AccentHover | #FFFFFF / #EA620A | 3.36 | 不足 4.5 |
| Warning 文本 on 白底（降级原因等） | #D97706 / #FFFFFF | 3.19 | 不足 4.5（`theme.go:68`） |
| TextMuted on 浅色侧栏 | #71717A / #F5F5F7 | 4.44 | 边缘不达 4.5 |
| 浅色导航 hover | #F4F4F5 / #F5F5F7 | **1.01** | 几乎不可见（`theme.go:56` + `shell.go:49-52`） |
| 浅色导航选中底 | #E4E4EA / #F5F5F7 | 1.16 | 很弱，仅靠橙色图标区分 |
| 预设 tile 选中 vs 未选（浅/深） | #E4E4EA vs #EFEFF2 / #3A3A40 vs #2E2E33 | **1.10 / 1.20** | 选中态基本不可辨（`channels_new.go:96-101,119-124`） |

其余软色板组合（软底深字）普遍 4.6–9.1，达标。

## 3. 逐页视觉走查（基于代码推断）

### 3.1 侧栏 + 底条（`shell.go:44-106`）

- 结构清楚：品牌行（14 粗体）→ 四项导航（高 30、图标 16、选中灰底 + 橙图标）→ 弹性空白 → 底条（LED + 状态词 + 等宽地址 + 降级原因 + 启停按钮）。层级与"状态是主角"的意图一致。
- **优点**：状态来自 `Gateway.Status()` 实际状态；LED 与文字同时表达，不依赖颜色；启停守卫按状态放行。
- **问题**：浅色 hover 不可见、选中底过弱（§2.4）；逻辑模型编辑态没有任何导航项高亮（P1-8，`shell.go:32-37,46`）；降级原因 `SingleLine()` 无省略号，系统长错误会被硬切（`shell.go:91`）；地址不可复制（设置页/指南页都可复制，交互不一致）。
- 底条启动/停止点击后没有"启动中/停止中"的禁用态，状态更新前可重复点击（`shell.go:93-103`）。

### 3.2 逻辑模型列表（`logical_view.go:20-85`）

- 首屏：页头（16 粗体标题 + 橙色「＋ 新建逻辑模型」）→ 每卡名称 + 状态徽章 + 「目标失效」徽章 + 编辑/删除/启停三个软按钮 → 目标链摘要（muted、`→` 分隔）。
- 信息密度合适，单卡两行能表达"是什么、挂了什么、能不能用"。
- **问题**：
  - 无 Scroll（P0-3）。超过约 7 个模型后不可达。
  - 状态徽章「启用/停用」与启停按钮（标签为反向的「停用/启用」）**同卡重复表达同一状态**，再加编辑、删除，一张卡 3 个按钮；建议收进 `⋯` 菜单或直接以 Switch 取代徽章+按钮。
  - 「目标失效」只覆盖渠道被删（P1-9），渠道被停用/无 Key 时不提示。
  - 标题字号固定 16（不随 textScale），与渠道页的 `FontSize+2` 不一致（P2-1）。
  - 空态放在 Card 内再 `Padding(24)`，形成"卡中卡"的双层留白（`logical_view.go:33-43`）。

### 3.3 逻辑模型编辑（`logical_view.go:119-187`）

- 首屏：返回文字按钮 + 标题 → 基本信息卡（名称/描述）→ 目标链卡（说明 + N 行）→ 保存胶囊 → 错误文本。信息层级清楚，Scroll 已包（`:127`）。
- **问题**：
  - 没有"取消"按钮；离开只能点「返回列表」或侧栏，且无脏检查（P1-7、P1-17）。
  - 每行目标有渠道下拉（MinWidth 140）、模型下拉、上移/下移/删除 3 个按钮；5 个目标即 15 个按钮，横向拥挤且无拖拽（P2 增强项）。
  - 标签宽度 112，渠道/接入页是 96，Codex 高级设置是 120（§4.5）。
  - 校验错误全部落在保存按钮下方的 `Edit.Msg`，不指出是哪一行（P1-13）。
  - 标题固定"编辑逻辑模型"，不显示模型名，而渠道详情标题显示渠道名（§4.1）。

### 3.4 渠道列表（`channels_list.go:15-45`）

- 首屏：页头「渠道 + N 个渠道 + ＋ 添加渠道」→ 渠道卡流。每卡 tile + 名称/启用/有 Key 徽章 + `协议 · BaseURL`（等宽、单行）+ 建议模型数与近况成败。密度与信息质量是本项目最好的一页。
- **问题**：
  - 无 Scroll（P0-3），约 6 张卡后不可达；无搜索/筛选。
  - 整张卡是 `ButtonBase`（`:52,76-78`），但**没有 hover 反馈**（未使用 `Hovered()`），与导航/预设 tile 的处理不一致；用户很难发现卡片可点。
  - 列表没有启停开关，必须进详情才能停用；逻辑模型列表却支持行内启停（`logical_view.go:65-71`）。同一概念两套操作模型（P1-16）。
  - 删除后的 `TestMsg`（"已删除 X"）会显示在页头下方，默认文字色，无成功样式，且在下一次进入新建/详情前一直停留（`channels_list.go:28-30`）。
  - 空态不在 Card 内（`:31-39`），与逻辑模型空态不同构。
  - `✓/✗` 字符依赖系统字体回退（`:71`），跨平台字形可能不一致。

### 3.5 渠道新建（`channels_new.go:14-90`）

- 首屏：预置异常警告（如有）→ 返回 + 「添加渠道」→ 20+1 个预设 tile 的 Wrap 网格 → 连接配置卡 → 建议模型卡。Scroll 已包（`:17`）。
- **优点**：预设 tile + 自动回填 + 协议联动的手改保护，交互意图明确。
- **问题**：
  - 预设网格没有分组/搜索，20 条平铺，4–5 行；首屏被网格占满，表单在下方。
  - 预设 tile 选中态几乎不可见（§2.4），用户无法确认当前选的是哪家。
  - 「创建渠道」主按钮在**连接配置卡内部**（`:68-73`），而"建议模型"卡在它之后；用户填完建议模型后要向上找创建按钮，主操作不在内容末端（P2-2）。
  - 没有取消按钮，只有「返回列表」；返回时草稿保留在 state 中但无提示（P1-7/P1-17）。
  - 「custom」格显示英文 `custom` 小字，而其余信息为中文（`:130`），术语不统一（P2）。

### 3.6 渠道详情（`channels_detail.go:17-107`）

- 首屏：返回 + 渠道名 + 有/无 Key 徽章 → 连接配置卡（名称、BaseURL、协议、API Key、启用/私网开关）→ 建议模型卡（TextArea 96 高）→ 保存 / 测试连通 / 右对齐删除渠道 → 结果文本。Scroll 已包（`:30`）。
- **优点**：协议可编辑 + 手改保护；Key 留空不改的 placeholder 明确；删除带引用警告。
- **问题**：
  - 「保存」与「测试连通」的结果共用 `TestMsg`，**同一默认文字色**（`:101-103`）：`saveDetail` 写"已保存"，`testDetail` 写"连通正常"或"xx 失败: …"（`channels_form.go:224,229-235`）。成功/失败视觉不可区分，且失败文本没有任何红色/图标。
  - 测试连通与拉取模型是**同步阻塞调用**（P0-2）。
  - Key 更新框没有展示当前 Key 的尾号/掩码，只有"有 Key"徽章；用户无法确认正在替换的是哪把 Key（P2）。
  - 删除确认弹窗的危险按钮是末位主钮（P0-1）。

### 3.7 接入页（`access.go:42-214` + 四个子包）

- 首屏：页签行（4 个 SoftButton）→ 全局配置卡（键值行）→ 实际配置卡（行 + 应用/编辑/更多 + 新建配置）→ 编辑器卡（打开时）。Scroll 已包（`:54`）。
- **优点**：页签切换有脏检查；网关不可用时只隐藏应用类按钮、保留纯 DB 操作（`access.go:288-317`）；Codex/Claude/Desktop 的差异被收进子包 hooks，结构清晰。
- **问题**：
  - **页签用橙色软底表示选中**（`accesskit/helpers.go:33-39`），而 spec §3.2 规定 Accent 仅用于主操作；同页的「应用」「保存」「新建配置」也是橙色，选中态与主操作争抢注意力（P1-6）。页签与侧栏导航的选中语义（灰 vs 橙）也不一致。
  - **编辑器在页面末尾**，打开后不滚动、不聚焦（`access.go:195-206`，全仓无 `ScrollIntoView`/`AutoFocus`）。错误信息在编辑器底部（`accesskit/editor.go:70-72`），Codex 还隔着 320px JSON + 240px 预览，用户点保存失败后要滚很久才看到原因（P1-1/P1-13）。
  - 「更多」注释写"展开行"，实际是把次级按钮塞进**同一个 Row**（`access.go:334-359`）：名称 140 + 徽章 + 模型摘要 + 应用 + 编辑 + 更多 + 复制启动命令 + 打开终端 + 删除，848px 宽下极易溢出，且没有 Wrap。
  - 配置行名称 `Width(140)` 且无 `SingleLine`（`access.go:291`、`claudedesktop/desktop.go:237`），长名称换行破坏行高。
  - 行式列表没有列标题（名称 / 模型 / 操作），首见用户需要从"→ 模型名"猜语义；与渠道/逻辑模型的卡片流是两套视觉语言（P2-10）。
  - 全局配置行只有 placeholder「键」「值」，没有列头/标签；空键或空值行保存时会被静默丢弃（`accesskit/helpers.go:52-63`），用户无感知（P2-11）。
  - 页签把「连接指南」（第三方手工接入说明）与三个受管 Agent 并列，语义不同类（P2-12）。
  - 编辑器打开时，页签与配置列表仍在同一滚动流里，没有"编辑态"的视觉隔离；建议改为子页/抽屉/行内展开（§8）。
  - Codex 模型元数据每行固定 150/150/120 + 弹性 + 120 下拉（`codex.go:350-366`），textScale 放大后固定像素宽度不跟随，长模型名/中文标签会挤压（P1-12）。
  - JSON 文本域与三份只读预览都用默认比例字体，没有等宽（`accesskit/editor.go:58`、`claude.go:30`、`codex.go:474`、`claudedesktop/desktop.go:171`），与"配置/代码"的语义不符（P2-13）。

### 3.8 设置页（`settings.go:46-154`）

- 结构：网关卡（端口/超时/透传预留/保存）→ 网关诊断卡（状态、地址、日志路径+复制、最近 20 条请求）→ 关于卡。（2026-10-29 复核：条数已由 8 改为 20，并有持久化历史。）
- **优点**：状态、日志路径、最近请求集中，诊断信息密度合理。
- **问题**：
  - 无页标题、无 Scroll（P0-3/P2-1）；三卡高度接近 688px 上限，textScale 稍大即溢出。
  - 输入框没有 `Grow`/宽度约束（`:67,71`），与其它表单行不一致；端口/超时是自由文本，保存时才报"端口/超时必须是整数"（P2-14）。
  - 保存结果用默认文字色，成功/失败不可区分（`:96-98`，P1-3）。
  - 最近请求只在设置页出现且限 8 条，渠道卡只给成败计数；路由决策/切换原因没有可见入口（P2-15，属产品范围但不是视觉缺陷）。

### 3.9 对话框与关窗模态

- 关窗模态是自绘 `DialogBase`，安全按钮序（灰色「取消」在前，橙色「最小化到托盘」在后），文案分行避免 CJK 断行问题（`closemodal.go:16-52`）——**这是全项目最正确的一个确认对话框**。
- `AlertDialog` 的 7 处调用则普遍把危险动作放在末位（P0-1）。框架语义：末位按钮为 Accent、`AutoFocus`、按 Enter 触发（MyGo `ui/feedback.go:181-199`）。这意味着：打开「删除渠道」后直接按 Enter 就会删除；打开「停止网关」后按 Enter 就会停掉所有 Agent 的模型入口。
- 对话框圆角：卡片 12、关窗模态 14（`closemodal.go:21`）、框架 AlertDialog 10（`t.Space(2.5)`）；三种规格并存（P2-5）。
- AlertDialog 面板宽 `t.Space(75)`=300px，长引用列表（`logical_view.go:98-101`、`channels_detail.go:117-119`）可能撑高但无内部滚动（P2-6）。

## 4. 跨页一致性问题

### 4.1 页头与标题

| 页面 | 标题 | 字号 | 返回 | 主操作位置 |
|---|---|---|---|---|
| 逻辑模型列表 | 有 | 固定 16（`logical_view.go:24`） | — | 右上胶囊 |
| 逻辑模型编辑 | "编辑逻辑模型"（不含名称） | `FontSize+2`（`:133`） | 文字按钮「返回列表」 | 内容底部 |
| 渠道列表 | 有 + 计数 | `FontSize+2`（`channels_list.go:21`） | — | 右上胶囊 |
| 渠道新建/详情 | 有（详情显示渠道名） | `FontSize+2`（`channels_new.go:27`、`channels_detail.go:36`） | 文字按钮 | 新建在卡内、详情在底部 |
| 接入页 | **无页标题** | — | — | 列表卡内 + 编辑器底部 |
| 设置页 | **无页标题** | — | — | 卡内 |
| 关窗模态 | 15 | `FontSize+2` | — | 底部右侧 |

结论：标题字号有 3 套、页头有无不一致、返回按钮永远是 `ui.Button` 而其它次级操作是 `SoftButton`（§4.3）；主操作既可能在右上、卡内、内容底部，也可能被 Scroll 带到视口外。

### 4.2 主操作与破坏性操作

- 主操作统一用 `PillButton`（橙），次级/危险用 `SoftButton`，方向正确。
- 但破坏性按钮的位置不统一：删除渠道在底部右对齐（`channels_detail.go:97-99`）、删除逻辑模型在行内（`logical_view.go:60-64`）、删除接入配置藏在「更多」里（`access.go:354-357`）。三种暴露层级，用户无法形成"危险操作在哪"的预期。
- 破坏性确认的按钮序全部相反（P0-1），且标签有「删除」「确认删除」「停止网关」「还原」「放弃并切换」五种写法。

### 4.3 按钮与选中语义

- 返回按钮用框架 `ui.Button`（`logical_view.go:130`、`channels_new.go:24`、`channels_detail.go:24,33`），其余次级操作用 `SoftButton`，视觉不同族。
- 选中态有 4 套：侧栏灰底 + 橙图标（`shell.go:49-58`）、预设 tile 灰底（`channels_new.go:96-101`）、接入页橙色软底（`helpers.go:33-39`）、Switch 橙开态。其中"橙色=选中"与"橙色=主操作"冲突（spec §3.2 第 53 行）。
- 同页三种按钮高度/内边距：`PillButton`/`SoftButton` 共用 `capsuleButton`（`widgets.go:66-77`），`ui.Button`、`ui.Select`、`ui.TextInput` 走框架默认。高度可能不齐，代码层面无法确认，需实机看。

### 4.4 反馈文案

全项目有 **10 个独立消息槽**：`Access.Msg`、`Access.GlobMsg`、`Access.GuideMsg`、`Access.Form.Msg`、`DesktopForm.Msg`、`Channels.Form.Msg`、`Channels.TestMsg`、`Logical.Edit.Msg`、`Settings.Msg`、`MetaEditState.Msg`（`state.go:42-112`）。它们：

- 颜色不一致：多数错误用 `Danger`，但 `Access.Msg`/`GlobMsg`/`GuideMsg` 用 `TextMuted`，`Channels.TestMsg`/`Settings.Msg` 用默认正文色。
- 位置不一致：有的在按钮旁、有的在卡内、有的在页面底部、有的在列表顶部。
- 生命周期不一致：有的下次进入清空（`openDetail`），有的跨页残留（`TestMsg`）。
- 成功/失败共槽：`TestMsg`、`Settings.Msg`、`Access.Msg` 都同时承载两类语义。

### 4.5 表单行

标签宽度出现 **80 / 96 / 110 / 112 / 120 / 150** 六种（`claude.go:70`、`accesskit/editor.go:39`、`guide.go:84`、`logical_view.go:139`、`codex.go:410`、`codex.go:351`），冒号有无也不统一（"名称:" vs "Sonnet" vs "启用"）。标签是普通 `Text`，没有使用框架的 `Label()` 关联输入件（全仓无 `.Label(` 调用），屏幕阅读器无法把标签与输入框关联。

### 4.6 空态

- 逻辑模型空态：Card + 居中 + `Padding(24)` + 主按钮（`logical_view.go:33-43`）。
- 渠道空态：无 Card、`Padding(24)` + 主按钮（`channels_list.go:31-39`）。
- 接入空态：仅一行 muted 文案 + 下方主按钮（`access.go:159-161,179-191`）。
- 编辑器内"无启用逻辑模型"：三份不同的 muted 文案（`accesskit/editor.go:107`、`codex.go:143`、`claudedesktop/desktop.go:152`）。

### 4.7 滚动容器

- 有 Scroll：逻辑模型编辑（`logical_view.go:127`）、渠道新建（`channels_new.go:17`）、渠道详情（`channels_detail.go:30`）、接入页（`access.go:54`）。
- 无 Scroll：逻辑模型列表、渠道列表、设置页。长内容直接溢出 688px 内容区。这是"部分页面有、部分没有"的遗漏，不是统一设计（P0-3）。

### 4.8 等宽字体

`MonoFont` 只用于 5 处：渠道 URL、设置页地址/日志/请求行、侧栏地址（§2.2）。JSON/配置预览、指南页地址、详情页 BaseURL 输入、模型名都不是等宽。spec §3.4 的落点（地址、令牌、状态码、耗时、上游模型名）尚未全面落实。

## 5. 组件与实现重复

| 重复项 | 位置 | 说明 |
|---|---|---|
| 删除二次确认 | `logical_view.go:90-113`、`channels_detail.go:112-126`、`access.go:365-396` | 三份近乎相同的 open 位/对象绑定/消息拼装/清场逻辑；按钮序与标签还各不相同 |
| 接入配置行 | `access.go:289-361`、`claudedesktop/desktop.go:235-279` | 应用/重新应用、编辑、更多/收起、删除四个动作重复实现；Desktop 版只是少两个按钮 |
| 页头 + 返回 | `channels_new.go:23-28`、`channels_detail.go:22-42`、`logical_view.go:129-134` | 三份手写 Row |
| 表单行 | 约 30 处（如 `accesskit/editor.go:38-41`、`channels_new.go:38-45`、`settings.go:65-72`） | 标签 + 输入 + 宽度逐页手写，导致 §4.5 的漂移 |
| 自绘选择态 | `shell.go:44-64`（导航）、`channels_new.go:94-139`（预设）、`access.go:57-69`（页签） | 三种"选中"视觉，无共享组件 |
| 下拉弹层外观 | `logical_view.go:223-249`、`codex.go:296-323` | 自定义 Trigger + Popup 的样式重复 |
| 空态 | `logical_view.go:33-43`、`channels_list.go:31-39` | 两种结构 |
| "无启用逻辑模型"提示 | `accesskit/editor.go:107`、`codex.go:143`、`claudedesktop/desktop.go:152` | 文案/位置三份 |
| AlertDialog 调用 | `shell.go:128`、`logical_view.go:102`、`channels_detail.go:120`、`access.go:382,436,453`、`channels.go:39` | 七份调用，无统一封装 |

**不是问题的重复**：`pages/access/{claude,codex,claudedesktop}` 的页面级差异（角色表/模型列表/元数据）属于子包边界内的合理分化，已通过 `accesskit.EditorHooks` 收敛共享骨架，不建议为了"去重"合并。

## 6. 交互反馈问题

1. **危险确认的默认动作是"执行"**：`AlertDialog` 末位按钮 Accent + AutoFocus + Enter（P0-1）。影响停止网关、删除渠道/逻辑模型/接入配置、还原备份、放弃未保存编辑、替换模型文本。spec 明确要求"取消居末位"（`2026-10-17-desktop-ui-ccswitch-redesign-design.md:94`），实现方向相反。
2. **同步网络调用阻塞渲染帧**：测试连通最长 15s（`channels_form.go:229` → `bridge/channels.go:173-183`），拉取模型同姿态（`channels_form.go:262,270`；bridge 注释自认"单次调用阻塞 UI 线程"，`bridge/channels_fetchmodels.go:53-54`）。点击后窗口冻结，无 loading、无禁用，用户会重复点击。
3. **无 loading/禁用态**：全仓无 `Disabled`/loading 状态（grep 零命中）。网关启动/停止是异步的（`shell.go:95-103`），状态更新前按钮仍可重复点；保存/应用也是同步执行，快速双击可能重复写入。
4. **成功与失败不可区分**：见 §4.4。最严重的是渠道详情 `TestMsg`（"已保存"/"连通正常"/"xx 失败"同一颜色）与接入页 `Access.Msg`（"已应用"/"应用失败"均为 muted）。
5. **错误不在字段旁**：保存错误统一进底部消息槽（`accesskit/editor.go:70-72`、`logical_view.go:182-184`、`channels_new.go:65-67`），无字段级红框/必填标记，Codex 编辑器错误距离名称字段约 900px。
6. **未保存修改保护不完整**：接入页只保护页签切换（`access.go:404-414`）；侧栏切页、逻辑模型「返回列表」、逻辑模型「新建」重置、渠道「新建」重置都可能静默丢弃草稿（`shell.go:61-63`、`logical_form.go:62-91`、`channels_form.go:143-186`）。
7. **JSON 非法时表单静默失同步**：`SyncRolesFromDoc` 解析失败直接 return（`accesskit/roles.go:40-43`），`syncFromDoc` 同理（`codex.go:23-26`）；用户继续改角色行时，行会变而文档不变，保存可能写回旧文档。2026-10-20 spec 的反目标明确要求"禁用表单控件并提示"（`2026-10-20-access-ccswitch-style-editor-design.md:54-55`）。
8. **健康警告不完整**：列表只检测"渠道被删"（`logical_form.go:141-147`），bridge 已提供 `TargetView.Healthy`（渠道存在、启用且有 Key，`bridge/logicalmodels.go:64-71`）却未使用；停用/无 Key 的目标在列表上看不出风险。
9. **可点击区域无反馈**：渠道卡整卡可点但无 hover（`channels_list.go:52-78`）；浅色主题导航 hover 对比度 1.01（§2.4）。
10. **打开编辑器无定位**：接入页编辑器在页尾，打开后不 `ScrollIntoView`、不 `AutoFocus` 第一字段；保存成功后编辑器收起，成功提示在上方的列表卡里，用户可能看不到。
11. **滚动位置不持久**：`state.State` 不保存 Scroll 偏移（`state.go:22-38` 无相关字段），切页返回后长页面回到顶部。
12. **对话框宽度与长文案**：AlertDialog 固定 300px，长引用列表无内部滚动；关窗模态 420px 是另一种规格。

## 7. 问题清单（P0 / P1 / P2）

> 证据均为仓库内文件行号；框架行号标为 `mygo/ui/...`。影响判断基于 1080×720 默认窗口与 13px 正文字号。

### P0（应在任何视觉优化之前修复）

| ID | 问题 | 证据 | 影响 | 建议 |
|---|---|---|---|---|
| P0-1 | 破坏性确认把危险按钮放在末位，Accent + AutoFocus + Enter 默认执行 | `shell.go:128`；`logical_view.go:102`；`channels_detail.go:120`；`access.go:382,436,453`；`channels.go:39`；`mygo/ui/feedback.go:181-199`；spec `2026-10-17-desktop-ui-ccswitch-redesign-design.md:94` | 误按 Enter 即停网关/删数据/覆盖 live 配置；与既有安全不变量和 spec 直接冲突 | 统一封装 `ConfirmDialog`：取消恒居末位并 AutoFocus；危险动作不用 Accent 主钮；标签统一「取消 / 删除」等 |
| P0-2 | 测试连通、拉取模型在渲染帧内同步发 HTTP，最长 15s，无 loading/禁用 | `channels_detail.go:93-95`、`channels_form.go:229,262,270`、`bridge/channels.go:173-183`、`bridge/channels_fetchmodels.go:53-54` | 窗口冻结、无反馈、重复点击；这是"应用卡死"级别体验 | 改为异步执行 + 状态位（进行中/成功/失败），按钮禁用并显示进度；结果回到状态位 |
| P0-3 | 逻辑模型列表、渠道列表、设置页没有 `ui.Scroll`，超过视口的内容不可达 | `logical_view.go:20-85`；`channels_list.go:15-45`；`settings.go:61-154`；对照有 Scroll 的 `logical_view.go:127`、`channels_new.go:17`、`channels_detail.go:30`、`access.go:54` | 约 7 个逻辑模型或 6 个渠道后，后续条目完全无法访问；设置页在 textScale>1 时溢出 | 所有页面主体统一包 Scroll；把"页面头固定 + 内容滚动"做成壳层布局 |

### P1（下一个迭代应解决）

| ID | 问题 | 证据 | 影响 | 建议 |
|---|---|---|---|---|
| P1-1 | 接入编辑器在页尾，打开不滚动/不聚焦；保存与错误信息在 ~900px 之下 | `access.go:179-206`、`accesskit/editor.go:58,70-72`、`codex.go:474`；全仓无 `ScrollIntoView`/`AutoFocus` | 编辑/新建后看不到编辑器（多配置时），保存失败不知原因 | 编辑器改为子页/抽屉/行内展开，或打开时 ScrollIntoView + AutoFocus；错误贴近字段 |
| P1-2 | 白字橙底主按钮对比度 2.80:1 | `theme.go:42-45`（Accent/AccentText），`widgets.go:50-52` | 所有主操作文字在浅色/深色下都低于 AA，低视力用户难以阅读 | 改用深橙底 + 白字（如 #C2410C）或橙底 + 深色字；保持品牌色的同时达标 |
| P1-3 | 成功/失败反馈同色同槽，成功无提示 | `channels_detail.go:101-103`；`access.go:176-178`；`settings.go:96-98`；`logical_form.go:136`（保存成功无消息） | 用户无法确认操作是否生效，失败可能被忽略 | 统一 `StatusLine(kind, text)`：success=Success 色，error=Danger，warning=Warning；关键保存加 toast 或行内成功态 |
| P1-4 | 渠道卡整卡可点但无 hover；浅色导航 hover 不可见 | `channels_list.go:52-78`；`shell.go:49-52`；`theme.go:56,131`（对比 1.01） | 可点击性不可发现；导航悬停无反馈 | 卡片加 hover 背景/描边/箭头；浅色导航 hover 用更深一档的中性色 |
| P1-5 | 预设 tile 选中态对比 1.10/1.20，几乎不可辨 | `channels_new.go:94-113,117-139`；`theme.go:118-123`；软灰 `theme.go:110` | 用户不知当前选中的是哪家预设，可能改错 BaseURL | 选中加橙色描边/对勾/左侧色条，不只靠底色 |
| P1-6 | 接入页签用橙色软底表示选中，与"Accent 仅主操作"冲突；页签与侧栏选中语义分裂 | `accesskit/helpers.go:33-39`、`access.go:57-69`、spec §3.2（`...redesign-design.md:53`） | 选中态与主按钮争抢注意力；四页签像四个可点按钮 | 用 Segmented/下划线 tab + 灰色选中；主操作保持唯一橙色 |
| P1-7 | 未保存编辑保护只覆盖接入页签切换 | `access.go:404-414` vs `shell.go:61-63`、`logical_form.go:62-91`、`channels_form.go:143-186` | 侧栏切页/返回/新建会静默丢草稿 | 壳层统一脏检查（页级 `Dirty()`），或自动保存草稿 |
| P1-8 | 逻辑模型编辑态侧栏无任何选中项 | `shell.go:32-37,46`；`st.Page="logical-edit"`（`logical_form.go:65,90`） | 用户失去位置感，不知道当前属于哪一页 | `normalizedPage` 把 `logical-edit` 归一到 `logical` |
| P1-9 | 「目标失效」只检测渠道删除，忽略渠道停用/无 Key | `logical_form.go:141-147`；`bridge/logicalmodels.go:64-71` 的 `Healthy` 未使用 | 列表显示正常但目标实际不可用，故障转移到运行期才暴露 | 用 `Healthy` 补「渠道不可用」徽章/原因 |
| P1-10 | JSON 非法时表单静默失同步，不提示不禁用 | `accesskit/roles.go:40-43`、`codex.go:23-26`；spec `2026-10-20-...:54-55` | 行显示已改但文档未改，保存可能丢失修改；违反 spec 反目标 | JSON 解析失败时禁用表单区并显示错误条；或阻止保存并高亮 JSON |
| P1-11 | `MonoFont="Menlo"` 在 Linux/Windows 不存在，框架回退到系统比例字体 | `theme.go:20`；`mygo/ui/element.go:1150-1153`（字体族缺失时回退系统选择） | 地址/令牌/请求行在 Linux 上不等宽，列对齐失效 | 改为 `"Menlo, monospace"` 或平台化字体族列表 |
| P1-12 | 固定像素宽度不随 `textScale` 缩放 | 标签 80/96/110/112/120/150；输入 150/120/220；`theme.go:77` 仅字号缩放 | 系统放大字体后标签/输入被裁切或挤压，可访问性回退 | 用 MinWidth + Grow，或按 textScale 计算宽度；关键行允许 Wrap |
| P1-13 | 表单无必填标记/字段级错误，错误只出现在底部；标签宽度不统一 | `accesskit/editor.go:70-72`、`logical_view.go:182-184`、`channels_new.go:65-67`；标签宽度见 §4.5 | 用户需猜测哪个字段出错，长表单尤其严重 | 建 `FormRow`（label + 必填 + error 位）；提交时聚焦第一个错误字段 |
| P1-14 | 接入配置行「更多」展开仍在同一 Row，无 Wrap；长名称无单行截断 | `access.go:334-359,291`、`claudedesktop/desktop.go:237` | 848px 下按钮溢出/行高跳动 | 次级操作放到第二行或弹出菜单；名称 `SingleLine + Ellipsis` |
| P1-15 | 渠道列表缺少行内启停，逻辑模型列表却有 | `channels_list.go:41-79` vs `logical_view.go:65-71`；redesign spec `:110-116` 期望卡上有 Switch | 日常"临时停用一个渠道"要多两步；两页操作模型不一致 | 渠道卡加 Switch 或 `⋯` 菜单；与逻辑模型统一 |
| P1-16 | 取消/返回语义不统一：有的编辑器有「取消」，有的只有「返回列表」 | `accesskit/editor.go:74-84` vs `logical_view.go:129-181`、`channels_new.go:23-27,68-73` | 用户不知道如何放弃修改，且可能误以为返回会保存 | 所有表单统一「取消 / 保存」；返回按钮明确"未保存将丢弃" |

### P2（体验打磨与桌面惯例）

| ID | 问题 | 证据 | 建议 |
|---|---|---|---|
| P2-1 | 页头体系不统一：逻辑模型 16 固定、渠道 15、接入/设置无页标题；标题不随 textScale | `logical_view.go:24`、`channels_list.go:21`、`access.go:42-57`、`settings.go:61` | 建 `PageHeader`（标题 15/16 可缩放 + 副标题 + 主操作） |
| P2-2 | 主操作位置不统一（右上/卡内/底部） | `channels_new.go:68-73`、`channels_detail.go:89-100`、`accesskit/editor.go:74-84` | 统一"页头或粘性底栏"放置主操作 |
| P2-3 | 空态结构不一致（卡中卡 / 无卡 / 纯文本） | `logical_view.go:33-43`、`channels_list.go:31-39`、`access.go:159-161` | 建 `EmptyState` 组件 |
| P2-4 | 大量原始间距数字，`t.Space` 只在壳层/模态使用 | 页面内 `Gap(8/10/12/16)`、`Padding(24)`；对照 `closemodal.go:22-24` | 页面统一用 `t.Space(n)` 或 layout token，便于全局调密度 |
| P2-5 | 圆角三套：卡片 12、模态 14、AlertDialog 10 | `widgets.go:18`、`closemodal.go:21`、`mygo/ui/feedback.go:186` | 统一对话框 12（或全部 12/14 二选一） |
| P2-6 | AlertDialog 固定 300px，长引用列表无滚动 | `mygo/ui/feedback.go:186`；`logical_view.go:98-101`、`channels_detail.go:117-119` | 允许 `MaxHeight + Scroll`，或把引用列表改为紧凑摘要 |
| P2-7 | 重复实现：删除确认 3 份、配置行 2 份、页头 3 份、表单行约 30 份 | 见 §5 | 抽到 `uikit`/`accesskit`，签名即契约 |
| P2-8 | 文本域尺寸/字体不统一：JSON 320、Claude 220、Desktop 200、Codex 240；JSON/预览非等宽 | `accesskit/editor.go:58`、`claude.go:30`、`codex.go:474`、`claudedesktop/desktop.go:171` | 统一高度档位；配置文本用 mono |
| P2-9 | 接入页无页标题；「连接指南」与三个 Agent 并列 | `access.go:57-69`、`guide/guide.go:62` | 加页标题；指南改为入口链接/独立卡片 |
| P2-10 | 接入配置列表是行式无列头，与其它页卡片流两套语言 | `access.go:289-361` | 加列头或改为紧凑卡片；明确主次 |
| P2-11 | 全局配置行只有 placeholder，空键/空值保存时静默丢弃 | `access.go:95-101`、`accesskit/helpers.go:52-63` | 加列头/标签；丢弃前提示或行内校验 |
| P2-12 | 设置页输入框无宽度约束、无数字输入约束；页面无 Scroll | `settings.go:65-72`、`:61` | 统一 FormRow；端口/超时做输入校验与范围提示；包 Scroll |
| P2-13 | 警告文本 3.19:1（浅色）用于降级原因 | `theme.go:68`、`shell.go:86-92` | 用深一档的 warning 文本色（≥4.5） |
| P2-14 | `✓/✗` 字符依赖字体回退 | `channels_list.go:71`、`settings.go:162-164` | 用图标或彩色数字（`3 成功 / 2 失败`） |
| P2-15 | 无搜索/筛选/排序；无应用菜单、全局快捷键、焦点管理 | 全仓无 `Shortcut`/`FocusGroup`/`AutoFocus`；`desktop.go:119-136` 只建托盘菜单 | 先加 `Cmd/Ctrl+F` 搜索、`Cmd/Ctrl+N` 新建、`Cmd/Ctrl+S` 保存、`Cmd/Ctrl+1..4` 切页；MyGo 已支持 Menu Accelerator |
| P2-16 | 窗口无最小尺寸；侧栏不可折叠；列表密度固定 | `desktop.go:203-206`、`:72` | 设 MinWidth/MinHeight；窄窗下允许侧栏收成图标轨（后续） |
| P2-17 | 托盘菜单只有打开/退出，驻留时无法确认/控制网关 | `desktop.go:119-136` | 托盘菜单加"运行中/已停止"禁用项与启停动作（需评估权限/安全） |
| P2-18 | 退出流程无进度（最多等待 grace 15s） | `desktop.go:167-185` | 退出前显示"正在停止网关"或托盘提示；超时反馈 |
| P2-19 | 切页后滚动位置丢失；最近请求只在设置页且仅 8 条 | `state.go:22-38` 无 Scroll 状态；`settings.go:130-140` | 保存各页 ScrollState；考虑把请求记录提升为可展开的诊断区 |
| P2-20 | 无手动主题切换（只随系统） | `desktop.go:64` 用 `c.Theme().Dark` | 设置页加"跟随系统/浅色/深色"（后续） |

## 8. 建议的统一设计方向（方向级，不是完整 design system）

1. **先修安全与可达性，再谈美化**：P0-1/P0-2/P0-3 是"会让用户丢数据或以为应用卡死"的问题，应排在任何视觉改版之前。
2. **建三个共享原语**：
   - `PageHeader`：标题（随 textScale）、副标题、返回、主操作，固定不随内容滚动。
   - `FormRow`：统一标签宽度/冒号/必填标记/字段级错误；替代目前约 30 处手写 Row。
   - `ConfirmDialog`：封装 AlertDialog，强制"取消居末位 + AutoFocus"，危险动作不使用 Accent 主钮。
3. **滚动与页骨架统一**：`页头固定 + 内容区 Scroll + 可选粘性操作栏`。编辑器不应再作为页尾追加卡，建议改为：
   - 接入配置：点击行后进入独立子页/侧滑面板（保留列表上下文），或行内展开；
   - 逻辑模型/渠道：保持整页编辑，但把保存放在固定底栏。
4. **成功/失败/进行中三态成为一等公民**：统一 `StatusLine`/`Toast`，success 用 Success、error 用 Danger、warning 用 Warning；禁止再用 muted 表示失败；同步网络操作全部异步化并显示进行中。
5. **选中语义收敛**：Accent 只给"主操作/开关开态/焦点"；导航、页签、预设 tile 的选中统一用中性选中灰 + 明确的形状/对勾/描边，不再用橙色软底表示"当前 tab"。
6. **桌面化增强（增量）**：应用菜单 + 快捷键（新建/保存/搜索/切页）、列表搜索筛选、窗口最小尺寸；批量操作与拖拽排序按 spec 明确暂缓，不在此轮引入。
7. **可访问性基线**：主按钮对比度 ≥4.5、warning 文本 ≥4.5、字体族回退、标签关联（`Label`）、固定像素宽度改为弹性 + `textScale` 感知、长文本省略号。建议把这六条写成 `uikit` 的验收清单。
8. **视觉语言保持一致，不要全盘卡片化**：渠道/逻辑模型适合卡片流，接入配置适合行式表格；统一的是页头、表单行、按钮、反馈、间距，而不是把接入页也改成卡片。

## 9. 待确认问题

1. **令牌展示策略**：编辑器渲染真实网关令牌（2026-10-21 用户决策），而连接指南页掩码前 6 位（`guide.go:28-36`）。这是有意区分（编辑器=本机配置、指南=对外展示），还是应统一？
2. **接入页全局配置的去留**：2026-10-20 spec 的决策 6 写"取消全局配置"，但当前代码/CLAUDE.md（2026-10-18）仍保留全局配置卡。视觉上是否继续把它放在接入页首屏？
3. **接入编辑器形态**：是否接受改为独立子页/抽屉？这会改变"列表 + 页尾编辑器"的现有交互，需要产品确认。
4. **危险操作强度**：删除渠道/逻辑模型是否需要在弹窗中显示引用对象并要求二次输入名称？当前仅列出引用，且 Enter 直接执行（P0-1）。
5. **主题策略**：是否需要在设置页提供浅色/深色/跟随系统三选一？当前只随系统。
6. **托盘驻留形态**：驻留托盘时是否需要在托盘菜单显示网关状态/启停？这涉及"关窗后如何确认网关仍在工作"的产品预期。
7. **列表密度**：渠道/逻辑模型是否引入紧凑模式或密度切换？在 1080×720 下目前约 6–7 条/屏，长期多条目用户可能不够。
8. **Codex 元数据区**：每模型四列固定宽度在模型多时会很长；是否接受折叠/按需展开，或改为"选中模型后编辑元数据"？
9. **视觉验收方式**：本项目目前没有截图/视觉回归链路；本轮改造后是否引入人工逐屏截图验收（或 MyGo `ui.NewTester` 的布局断言）来替代"只读代码"的审查？

---

**审查结论一句话**：设计 token 与组件骨架已经成型、颜色纪律良好，但交互安全（确认弹窗默认动作）、长操作反馈（同步阻塞/无 loading）和页面可达性（列表无滚动、编辑器不可见）是当前最值得先修的三类问题；视觉一致性问题多来自"组件没有抽出来"而非审美方向错误。
