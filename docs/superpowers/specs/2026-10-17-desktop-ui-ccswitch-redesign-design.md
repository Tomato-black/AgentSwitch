# AgentSwitch 桌面 UI 重设计：cc-switch 风格「中性灰 + 橙操作色 + 卡片流」

- 日期：2026-10-17
- 状态：已评审通过的设计方向（用户逐屏确认 mockup），待实现
- 决策人：仓库所有者（zhang.cao）
- 范围：`internal/desktop`（UI 层）+ `CLAUDE.md`；不触碰 gateway/protocol/router/provider 数据面与 bridge 语义
- 参照物：cc-switch（https://github.com/farion1231/cc-switch ，本地源码 `/Users/caozhang/work/java_work/cc/cc-switch-main`）v7 设计语言；其设计 token 见该仓库 `src/index.css`

## 1. 决策记录与同旧档的关系

**决策**：桌面 UI 视觉系统全面重做，采用 cc-switch v7 风格——中性 zinc 灰阶 + 白色大圆角卡片 + 软色徽章 + **橙色主操作色（#F97316）**；渠道从「master-detail 面板」改为「**卡片列表 + 整页详情**」的信息架构。

**用户三项确认**（mockup 逐屏点选）：

1. 视觉方向 **A：cc-switch 橙**（否决保留品牌青的 B 方案）。
2. 渠道交互 **方案 2：详情页式**（否决弹窗式；理由：密钥池/渠道模型是重内容，弹窗装不下）。
3. 概览页四卡布局与模型页聚合表稿「没问题」。
4. 模型页**行 = 渠道模型记录**（builder check 后用户裁定）：不按对外模型合并行、不做批量启停——启停开关只切单条渠道模型记录，路由语义不变；mockup 中的合并行仅为视觉示意。

**附带规则（本次新定）**：**状态绿与操作橙分离**。旧档 §2.3「一个颜色想法：Success = Accent」废除——Accent 变橙后，运行/健康状态若沿用 Accent 会与主操作同色（「运行中」看着像警告）。`Success` 改为独立绿色；LED 语义改为 running=绿、degraded/cooling=琥珀、failed=红、stopped=弱灰。

**已知偏离（2026-10-21 用户决策，实测后定）**：LED 呼吸动画取消，running 改为**常亮静态点**——对 §2 非目标「保留 LED 呼吸」、§4 组件表「`ledDot`（保留）」、§5.3「大 LED（12px，绿呼吸）」与 §6「LED 呼吸、ReduceMotion 语义不变」的明示偏离。理由：`ui.Element.Loop` 会调 `c.AnimationFrame()` 让**整个视图**按显示刷新率重建（不只是那个点），实测空转常驻约 20% 单核、与网关流量无关；取消后空闲 CPU 为 0%（内存同时降约 20 MB）。状态语义由颜色 + 标签文字承载，不依赖动效；`ledDot` 头部注释已记录「不要加回 Loop/Animate」。

**与旧档关系**：本文档**取代** `2026-10-17-desktop-ui-mygo-native-design.md` 的视觉系统（其 §2 设计原则第 3 条、§3 token 表、§5 各页视觉构成）与 `internal/desktop/theme.go` 现行 token 值；旧档的**交互语义与安全不变量全部保留**（令牌掩码、两段式删除确认、确认位绑定渠道、首关教育、状态来自实际服务状态等，见 §8）。实现落地时同步更新 `CLAUDE.md` 中对旧档的指称。UI 决策不变：全部 MyGo 原生视图，无前端工程。

## 2. 目标 / 非目标 / 反目标

**目标**：三页（概览/渠道/模型）+ 侧栏全部按新视觉系统重做；渠道页信息架构重构为列表+详情页；明暗两套主题同步交付；所有现有功能与安全语义不丢。

**非目标**：不加新业务功能（配额查询、用量统计、拖拽排序等 cc-switch 有而 AgentSwitch 没有的能力，一律不做）；不改 SQLite schema 与 bridge API（例外见 §5.3 的只读统计）；不做多语言；不引入动画堆砌（保留 LED 呼吸——2026-10-21 起改为常亮静态点，见 §1 已知偏离）。

**反目标**（出现即算失败）：密钥/令牌明文出现在无遮蔽的位置；橙绿不分（状态色当操作色用）；卡片信息密度低到一屏放不下三个渠道；为好看牺牲既有二次确认流程。

## 3. 设计系统 — Theme token

### 3.1 构建方式

沿用 `buildTheme(dark bool, textScale float32) *ui.Theme` 签名与调用点（`view` 根部 `c.SetTheme`），仅覆写 token 值；`theme.go` 另新增包内软色板（§3.3）——软色不进 `ui.Theme`（框架没有这些字段），以包级常量/结构体随主题切换取值。

### 3.2 ui.Theme 覆写表

| token | 浅色 | 深色 | 说明 |
|---|---|---|---|
| Background | `#FFFFFF` | `#1B1B1F` | 窗口/内容区底 |
| Surface | `#FFFFFF` | `#2C2C31` | 卡片、输入件底 |
| SurfaceHover | `#F4F4F5` | `#333338` | 行 hover |
| SurfacePressed | `#EFEFF2` | `#3A3A40` | 按下/侧栏 subtle |
| Selection | `#FFDDBB` | `#4A3423` | 框架 `Theme.Selection` 只画文本选区高亮（element.go:1016），须保持暖色可见；**不复用为选中态** |
| Selected（**应用级 token**，Go 常量，非 ui.Theme 字段） | `#E4E4EA` | `#3A3A40` | 侧栏选中、segmented 选中片；**中性灰，不是 Accent**。theme.go 内按 dark 取值，与软色板同处管理 |
| Border | `#E4E4E9` | `#3F3F46` | 卡片边、输入边、分隔线共用 |
| Text | `#18181B` | `#E4E4E7` | |
| TextMuted | `#71717A` | `#A1A1AA` | |
| Accent | `#F97316` | `#F97316` | **仅主操作**：主按钮、Switch 开态、segmented 指示、焦点环 |
| AccentHover | `#EA620A` | `#EA620A` | |
| AccentPressed | `#DD5A0B` | `#DD5A0B` | |
| AccentText | `#FFFFFF` | `#FFFFFF` | |
| Success | `#16A34A` | `#22C55E` | 独立绿：运行 LED、「可用」点、「全部就绪」 |
| Danger | `#DC2626` | `#EF4444` | |
| Warning | `#D97706` | `#FBBF24` | |
| Focus | = Accent | `#D8783A` | 深色下降饱和（cc-switch 同做法） |
| Inverse | `#18181B` | `#E4E4E7` | |
| InverseText | `#FFFFFF` | `#18181B` | |
| Scrollbar | `#C9C9CF` | `#4A4A52` | |

### 3.3 软色板（包内常量，Badge/警示底色用）

「soft 背景 + 深文字」徽章体系（cc-switch 的 `--*-soft/--*-text`）：

| 语义 | 浅色 bg / text | 深色 bg / text |
|---|---|---|
| 操作（首选徽章、chips 选中） | `#FFF1E6` / `#C2410C` | `#33221A` / `#FB923C` |
| 健康（启用、可用、就绪） | `#E8F7EE` / `#007634` | `#12261A` / `#4ADE80` |
| 警示（冷却、降级、429） | `#FEF3C7` / `#A64A00` | `#2E2410` / `#FBBF24` |
| 危险（失败、删除确认） | `#FEE2E2` / `#B91C1C` | `#2C1516` / `#F87171` |
| 中性（已停用、计数） | `#EFEFF2` / `#52525B` | `#2E2E33` / `#A1A1AA` |
| 链接（Base URL） | `#0A6CD6` | `#5AA9F8` |

### 3.4 度量、字体与侧栏

- `Radius: 8`（控件：按钮/输入/chip）；**卡片一律 `Box.Radius(12)`** 显式盖过主题值。`Spacing: 4`；`FontSize: 13 * textScale`（textScale 接法不变）。
- 内容区密度：页面 padding 16、卡片间距 10–12、卡片内 padding 12–14。等宽仍 Menlo，落点不变（地址、令牌、密钥尾、状态码、耗时、片段、上游模型名）。
- **侧栏自绘，弃用 `ui.Sidebar`**：其选中项硬编码 `Accent` 底（sidebar.go:188），橙块选中不符合 cc-switch 灰底选中。自绘 = `Box.Background(SurfacePressed 系)` 列 + 导航行（高 30、Radius 8、选中 `Selected` 灰底、hover `SurfaceHover`、激活项图标用 Accent）；宽 200。导航底色与窗口底区分：浅色侧栏 `#F5F5F7`（= `SurfacePressed` 基调，实现时以常量落在自绘 Box 上），深色侧栏 `#232327`。页脚状态位（LED+状态词+等宽地址）保留，搬进自绘侧栏。

## 4. 组件词汇（全部已在 mygo v0.5.1 验证存在）

| 词汇 | 实现 | 用于 |
|---|---|---|
| 卡片 | `Box.Radius(12).Background(Surface).Border(1, Border)` | 各页容器 |
| 软徽章 | `Box.Radius(999)` + 软色板 bg/text | 首选/启用/冷却/停用/计数 |
| LED | `ledDot`（保留），色改走 Success/Warning/Danger/TextMuted；2026-10-21 起常亮不呼吸，见 §1 已知偏离 | 状态位、池状态 |
| Switch | `ui.Switch`（开态=Accent，框架行为吻合） | 渠道启停、模型启停 |
| Segmented | `SegmentedBase` 自组，选中片显式上 `Selected` 灰 | 详情页三段。**不可直接用 `ui.Segmented`**：其选中片取 `pressedColor()`=浅色 `Background`（toggle.go:45-54），白底白片会隐身 |
| MenuButton | `ui.MenuButton` | 卡片「⋯」（测试/编辑/删除） |
| AlertDialog | `ui.AlertDialog` | 删除渠道/密钥、重新生成令牌确认（替换现行两段按钮位）。**按钮序：取消居末位**——框架末位按钮为 Accent 且默认响应 Enter（feedback.go:195-207），取消居末 = Enter 恒为安全动作；破坏动作不设橙色/红色主钮，危险性由标题与正文承载 |
| SearchField | `ui.SearchField` | 模型页搜索 |
| Avatar/Box tile | 40px `Radius(10)` + 渠道类型 soft 底色块 | 渠道卡图标位 |
| Table | `ui.Table` 保留 | 最近请求、密钥、模型表 |
| Field/Input/TextArea/Select/Meter/Toast | 照旧 | 表单、池状态条、轻提示 |

渠道类型 → tile 底色（固定映射，软色板风格）：Anthropic=橙系、OpenAI 系=绿系、GLM/DeepSeek/Kimi 等自定义=蓝/紫/青按名称哈希取一组预设。**不超过软色板五组，避免彩虹。**

## 5. 页面构成

### 5.1 侧栏（全页常驻）

自绘列：顶部品牌行「AgentSwitch」（粗体 14）；导航：概览/渠道/模型（图标+文字，选中灰底）；底部页脚：LED+状态词+等宽地址（现状保留）。

### 5.2 渠道页 — 卡片列表 + 整页详情 + 整页新建

**列表态**：页头「渠道」+ 小字统计（`N 个渠道 · M 个启用 · 密钥池 a/b 可用`）+ 右侧胶囊主按钮「＋ 添加渠道」（Accent 底、Radius 999）。下方渠道卡流，每卡：

- 左：40px 类型 tile（色块+缩写）；
- 中：名称（粗体）+ 徽章行（「首选」=优先级最高且启用，soft 橙；「启用/已停用」soft 绿/灰；「冷却 n 把」soft 琥珀）＋ Base URL（链接色，等宽不强制）＋ meta 行小字 `优先级 N · M 把密钥 · 近 100 条 a✓ b✗`（统计来自内存遥测环按渠道名过滤，重启清零，诚实展示不持久化）；
- 右：`Switch` 启停 + `MenuButton`「⋯」（测试连通/编辑/删除——删除走 AlertDialog）。

卡片点击进详情页；Switch/⋯ 阻断冒泡不触发进入。空状态：居中插画位（简单图形）+「还没有渠道」+ 主按钮引导添加。

**详情页**（`u.page="channel-detail"`，锚定渠道 id）：页头 = 返回按钮（←，回列表）+ tile + 名称+徽章 + 右侧「连通测试」（次级钮）与「保存」（主钮）。下方 `Segmented`：**连接配置｜密钥池 n｜渠道模型 m**。

- 连接配置：类型（Select，改类型弹确认——会重置预设 BaseURL）/名称/Base URL/优先级/启用 + 允许私网地址（语义照旧）；测试结果信息位照旧。
- 密钥池：工具行（「＋ 批量导入」soft 橙胶囊钮开 TextArea 表单）+ 密钥表（名称/密钥尾/状态点+冷却到点/24h 段内成败/⋯ 删除）；删除二次确认照旧且确认位绑定该渠道。
- 渠道模型：工具行（「＋ 添加模型」）+ 表（对外名/上游映射等宽灰字/启停/⋯ 删除）；两段确认照旧。

**新建页**（`u.page="channel-new"`）：页头 = 返回 + 「添加渠道」；顶部类型预设 chips（现 typeID 预设集，选中 soft 橙底，点选自动填 BaseURL——现 prevType 预填逻辑平移）**取代类型 Select**——新建页不出现类型 Select 与「更换类型重置确认」（对未保存渠道无意义）；chips 之下是名称/Base URL/优先级/启用 + 密钥批量 TextArea；保存后落回列表并选中新渠道。类型 Select 与更换确认只存在于详情页（已保存渠道换类型才需要确认）。

### 5.3 概览页 — 四卡

1. **状态 hero 卡**：大 LED（12px，绿呼吸；2026-10-21 起常亮，见 §1 已知偏离）+ 状态词（16 粗体）+ 等宽 `http://127.0.0.1:port · 超时 Ns`；日志路径恒显为卡内第二行 mono（可诊断性照旧，**不做「日志」按钮**——无既有打开日志能力，按非目标不新增）；右侧「近 100 条 · N 失败」（内存环统计，中性徽章）+「停止网关」（danger 软底胶囊）/「启动网关」（Accent 主钮，仅 stopped/failed）。degraded/failed 时 hero 卡下出琥珀横幅（DegradedReason，语义照旧）。
2. **接入卡**：「接入 Claude Code」标题 + 片段代码块（`#FAFAFA`/深色 `#232327` 底、Radius 9、等宽 11.5，令牌行掩码不变量照旧）+ 按钮行（显示/隐藏、复制片段、重新生成——重新生成走 AlertDialog 确认）。
3. **密钥池 + 设置** 并排两小卡：池卡 = `Success` 点「全部就绪 · n 把可用」或冷却 chips（渠道/密钥+剩余秒，琥珀）；设置卡 = 端口/超时输入 + 保存（现为 Collapsible，改常开小卡，保存语义与回显逻辑照旧）。
4. **最近请求卡**：`ui.Table` 包进卡片；列改为 时间/模型/**渠道/密钥**（现 Account 列升级为 `Channel/Account` 两段显示）/状态/耗时/错误；状态码染 Success/Warning/Danger；错误列显示 ErrorClass + 「→ 已切换」类迁移标记（数据已有则显）。

### 5.4 模型页 — 搜索 + 聚合表

页头「模型」+ 小字（`N 条渠道模型 · 来自 M 渠道 · 停用 X`）+ 右 `SearchField`。**行 = 渠道模型记录**（评审裁定，非对外模型合并行）：启停 Switch 只切本行记录，语义与现状完全一致，不改路由行为；同名对外模型相邻排列、视觉上归组。表列：启停 Switch｜对外模型（粗体）｜来源渠道（单渠道 chip，点击跳该渠道详情）｜上游映射（等宽灰字）。数据与启停语义完全照旧（跨渠道聚合只读 + 行内启停）。

## 6. 明暗主题

`view` 根按 `c.Theme().Dark` 选边照旧；§3.2/§3.3 两套值成对实现。LED 呼吸、ReduceMotion 语义不变（2026-10-21 起 LED 改常亮静态点、不再有呼吸动效，见 §1 已知偏离；ReduceMotion 对 LED 不再适用）。深色下 Focus/Accent 微调值照表。

## 7. 验收标准

1. 三页+侧栏在新视觉下明暗两套均无破相控件（未覆写 token 的框架件不裸奔）。
2. 渠道增删改测、密钥批量导入/删除、渠道模型增删启停、连通测试——全部功能路径可用，交互落点为新 IA（列表/详情/新建三态）。
3. §8 不变量逐条对拍通过（评审重点：掩码、确认位绑定、状态真实性）。
4. 侧栏为自绘实现，选中态为中性灰。
5. `go test ./...`、`go test -race ./internal/desktop` 全绿（desktop 测试随 UI 重构同步改写，断言语义不放松）。

## 8. 保留不变量（实现时逐条对拍）

1. 令牌默认掩码 `***`，显示/隐藏单按钮换 label（不能双按钮同帧并存）；重新生成须二次确认；复制片段用真实令牌、展示用掩码（Review Focus 1 语义原样保留）。
2. 渠道/密钥/模型删除与重新生成令牌：AlertDialog 二次确认；**确认位武装时绑定渠道 id**，详情切换即解除（现 confirmOwnerID 机制平移）。
3. 网关状态一律来自 `app.StateInfo` 实际状态，按钮可用性按状态守卫；启停按钮语义集合不变（stopped/failed 才可启动；running/degraded 才可停止）。
4. 首关教育横条逻辑不变（closeNotice/closeConfirmed）。
5. 等宽丝印落点不减：地址、令牌、密钥尾、时间/状态码/耗时、片段、上游模型名。
6. 日志与 UI 不得出现密钥明文（表内一律尾 4/掩码）；`base_url` 私网校验的「允许私网」开关保留原语义。
7. 概览「近 100 条」统计只读内存遥测环，不落库、不扩 schema；渠道卡统计同源。

## 9. 风险与实现注意

- `ui.Switch`/`Segmented`/`MenuButton`/`AlertDialog`/`SearchField` 的具体签名以 v0.5.1 源码为准（已确认存在，参数细节实现时对源码，不凭记忆）。
- 自绘侧栏需处理焦点/键盘可达性最低要求（可 Tab 聚焦、Enter 激活）；若 `ui.Sidebar` 能经 token 压制 Accent 选中态则可回头用框架件——实现时先试 token、不行再自绘（自绘是保底方案，不是首选）。
- ~~卡片点击进详情与卡内 Switch/⋯ 的命中冲突~~——评审已对照框架源码解除（input.go:251,271：点击命中最内层交互件，卡内 Switch/⋯ 不会冒泡触发进详情）。
- Segmented 三段内容较多，详情页需可滚动（`ui.Scroll` 包内容列）。
- CLAUDE.md 的旧档指称、theme.go 头注释（§3 引用旧 spec 之处）随实现一并更新。
