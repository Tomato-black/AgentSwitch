# AgentSwitch 桌面 UI 设计：MyGo 原生「交换机面板」设计系统

> 注：本文档的视觉系统（§2 原则 3、§3 token、§5 页面视觉）已被 `2026-10-17-desktop-ui-ccswitch-redesign-design.md` 取代；交互语义与安全不变量仍有效。

- 日期：2026-10-17
- 状态：已评审通过的设计方向（用户逐节确认），待实现
- 决策人：仓库所有者（zhang.cao）
- 范围：`internal/desktop`（UI 层）+ `CLAUDE.md`；不触碰 gateway/protocol/router/provider 等数据面与 bridge 语义

## 1. 决策记录

**决策**：自本日起，AgentSwitch 的全部 UI 以 MyGo 原生视图（`internal/desktop`）实现；**放弃权威设计文档 v2.0 §12 规划的 `frontend/` TypeScript 工程**——不再建立前端工程，不引入 npm/pnpm 构建链。

**与权威文档的关系**：docx v2.0 §12 规划 TS + HTML/CSS 前端。本决策是用户明示的方向变更，构成对该节的偏离；docx 为二进制不做修改，本档即偏离凭证。`CLAUDE.md` 同步记录该偏离（实现阶段更新）。

**动机**：MyGo `ui` 包 v0.5.1 的能力（完整 Theme token、Sidebar/Badge/Meter/Collapsible/Toast、逐节点字体与颜色）足以承载一套有辨识度的原生设计；单二进制、零前端构建链与产品定位（本地开发者的桌面工具）一致。

**非目标**：不改 bridge/业务语义；不新增管理功能；不引入动画堆砌；不做多语言。

## 2. 设计原则

1. **状态是主角**。网关运行状态（running/degraded/stopped/failed）在任何页面都可见：边栏页脚常驻状态位 + 概览页首面板头。状态来自 `app.StateInfo` 实际服务状态（不变量：不是按钮状态）。
2. **丝印面板**。机器读数与人文标签分离：地址:端口、令牌、密钥尾 4、请求时间/状态码/耗时、配置片段一律等宽字体（Menlo）；标签、按钮、提示用系统字。数据像面板丝印，操作像人话。
3. **一个颜色想法**：青绿色 = 链路健康 = 品牌主色。`Success` 与 `Accent` 同色；琥珀专属降级/冷却警示，红专属故障/删除，弱灰专属停止/停用。品牌色不另起炉灶。
4. **安静的铬**。装饰即信息，否则删除：无渐变、无阴影堆叠、状态色只落在语义载体（LED、Badge、警示条、非 2xx 状态码）上。

## 3. 设计系统 — Theme token

### 3.1 构建方式

新增 `internal/desktop/theme.go`：`buildTheme(dark bool) *ui.Theme`——以 `ui.LightTheme()` / `ui.DarkTheme()` 为底拷贝，覆写下表字段（未列字段保持框架默认，保证未知控件不破相）。应用点：`view` 根部按 `c.Theme().Dark` 选边后 `c.SetTheme(...)`（首帧 `c.Theme()` 为系统主题，其 `Dark` 标志即系统外观）。

### 3.2 色板（19 个颜色 token 全部显式覆写）

| token | 浅色（仪器纸灰） | 深色（机架墨灰） |
|---|---|---|
| Background | `#F3F4F2` | `#16181D` |
| Surface | `#FFFFFF` | `#1E2128` |
| SurfaceHover | `#F0F2F1` | `#262A33` |
| SurfacePressed | `#E4E8E6` | `#2E333E` |
| Border | `#D5DAD6` | `#2A2E37` |
| Text | `#1C2320` | `#E8EAF0` |
| TextMuted | `#67736D` | `#9AA1B0` |
| Accent | `#0E7A5F` | `#45D0B4` |
| AccentHover | `#0B6550` | `#5FDFC6` |
| AccentPressed | `#08503F` | `#2FA891` |
| AccentText | `#FFFFFF` | `#0D1512` |
| Danger | `#C63D3D` | `#E5484D` |
| Warning | `#B97A0A` | `#F5B83D` |
| Success | = Accent | = Accent |
| Selection | `#C9E8DF` | `#1F4A41` |
| Focus | = Accent | = Accent |
| Inverse | `#1C2320` | `#E8EAF0` |
| InverseText | `#F3F4F2` | `#16181D` |
| Scrollbar | `#B9C1BB` | `#3A404C` |

### 3.3 度量与字体

- `Radius: 6`（仪表件小圆角，不做胶囊软糯感）；`Spacing: 4`（默认密度）；`FontSize: 13`（macOS 默认）；`Font: ""`（系统字）。
- 等宽读数：逐节点 `Font("Menlo")`，用于——监听地址、令牌值、密钥尾 4、请求表的时间/状态/耗时列、配置片段行。标签不用等宽。
- 语义色落点：LED 圆点与状态词按 `app.StateInfo.State` 映射——running=Accent、degraded=Warning、failed=Danger、stopped=TextMuted；Badge「启用」= Accent 面、停用 = TextMuted 文本；非 2xx 状态码按错误分类染 Danger/Warning；警示横幅 = Warning 文字 + Border 边框（不用色块填充）。

## 4. 布局架构 — 边栏 + 内容

```
┌──────────┬────────────────────────────┐
│ ▪ 概览     │  内容区（Padding 16，仅当前页）  │
│ ◦ 供应商   │                            │
│ ◦ 账号     │                            │
│ ◦ 模型     │                            │
│ ───────── │                            │
│ ● 运行中    │  ← 边栏页脚：状态灯+状态词（全局恒显）│
│   :17890  │                            │
└──────────┴────────────────────────────┘
```

- `ui.Sidebar`（宽 200）替代顶部按钮行；`u.page` 的 id（overview/providers/accounts/models）与分发逻辑不变，仅换渲染载体。
- 导航 4 项各配 16px 内联 SVG 线性图标（概览=面板格、供应商=插头、账号=钥匙、模型=路由箭头），`ui.ParseSVG` 内联定义于 `icons.go`。
- **边栏页脚 = 全局状态位**：LED 圆点（running 时 `Animate` 缓慢呼吸，全应用唯一动效时刻）+ 状态词 + 等宽端口。
- 首关教育提示（I4：任何页可见）改为内容区顶部横条，文案不变。
- 窗口尺寸 1080×720 不变。

## 5. 页面规格

### 5.1 概览（面板头）

1. **面板头**：LED + 加粗状态词（大号），右对齐等宽监听地址；降级原因行（Warning 色）条件渲染。
2. Divider。
3. 操作行：启动/停止按状态守卫（语义不变），「设置」改用 `ui.Collapsible` 收纳端口/超时表单与保存按钮。
4. 令牌行：标签 + 等宽掩码值 + 显示/复制/重新生成（两段确认语义保留）。
5. 配置片段块：等宽逐行展示、掩码不变量保留、复制按钮照旧。
6. 最近请求表（Grow）：六列不变；时间/状态/耗时三列等宽化；非 2xx 状态码染语义色。
7. **P0-B 预留槽位**：面板头与请求表之间预留「账号池状态」条位（每账号一枚迷你状态点+名称），本期只留布局注释不实现。

### 5.2 供应商 / 模型

- 页头行：页标题（加粗）+ 记数（TextMuted）。
- 表（Grow）：启用列改 Badge（供应商页）；模型页无启用列。
- 表单区丝印式两栏：左侧固定宽右对齐 TextMuted 标签、右侧字段；供应商页名称+协议一行两列、Base URL 独占一行、启用+允许私网一行；模型页公开名/上游名各占一行。
- 操作行与两段确认语义全部保留。

### 5.3 账号

- 同 5.2 骨架；页顶降级警示条改为 Warning 文字 + 边框横幅（文案不变）。
- 表六列不变；密钥尾 4、存储两列等宽化。
- 表单三行：名称+优先级一行两列、供应商一行、密钥独占一行；启用+允许降级一行。

### 5.4 公共约定

- 所有提示/结果文本（表单 msg、连通测试结果、设置保存结果）保持原字符串，前置状态点或状态词可选但不得改变可断言文本。
- 删除一律两段确认；破坏性主操作不新增颜色（mygo 按钮样式由主题决定，无逐实例覆盖——删除确认按钮靠文案「确认删除」承担重量）。
- 空表状态：表格区显示一行 TextMuted 引导文案（如「尚无供应商——先在上方表单创建」），不是空白。

## 6. 实现切分

1. `internal/desktop/theme.go`：token 表 + `buildTheme(dark bool)`；`view` 根部接入。
2. `internal/desktop/icons.go`：4 枚 SVG。
3. `internal/desktop/desktop.go`：view 改边栏骨架（page id、首关教育横条、边栏页脚状态位）。
4. 四页文件重排：`overview.go` / `providers.go` / `accounts.go` / `models.go`。
5. 测试迁移与新增（§7）；`go build ./... && go vet ./... && go test -race ./...` 全绿。
6. 文档：`CLAUDE.md` 两处更新（状态段落记录决策与 docx §12 偏离；常用命令段落删除 frontend/ 展望、指向本档与 theme.go）。

## 7. 测试策略

- **文案不变量**：重排只动布局与着色，不动任何可断言字符串（按钮词、标签、提示、表头）。现有 desktop 测试预期大体不破；破了的按语义更新并注明。
- 新增测试点：
  - `buildTheme` 双外观：深浅两套 Accent 不相等、Success==Accent、Danger/Warning 与 Accent 互异。
  - 边栏选择映射：selected id ↔ `u.page` 双向一致（含未知 id 落回 overview）。
- 涉及 UI 状态并发的测试保持 `go test -race ./internal/desktop` 必跑。
- 真实观感验收：`go tool mygo dev` 手动过一遍四页（浅/深两种系统外观），不在 CI 做。

## 8. 待验证项与已知限制（实现时逐一验证，遵循「不假定未验证 API」）

1. **SVG 图标着色**是否随主题 Text 色（`ui.ParseSVG` + SidebarItem 的实际渲染）；若不随主题，图标用固定中性色并记录。
2. **运行中切换系统外观**是否实时换肤：`SetTheme` 后 `c.Theme()` 返回自设主题，外观变化可能不触发重选——若不实时，记为已知限制（重启生效），不阻塞。
3. **`Font("Menlo")`** 在非 macOS 的回退行为；当前仅 darwin 一等公民，记录即可。
4. **ReduceMotion 偏好**是否存在可查询 API（`ui/preferences.go` 提及）；有则 LED 呼吸动画尊重之，无则保留并注释。
5. `ui.Collapsible` 在 Tester 下的行为（labels 收集）是否影响设置面板既有测试。

## 9. P0-B 预留

- 概览面板头下的「账号池状态」槽位（§5.1 第 7 条）：本期只留布局位与注释。
- 边栏不预留新导航项；P0-B 如需事件页再扩 id（`u.page` switch 天然可扩展）。
- 账号表已含优先级/启用列，冷却状态列留待 P0-B 设计其语义色（预计 Warning 族）。
