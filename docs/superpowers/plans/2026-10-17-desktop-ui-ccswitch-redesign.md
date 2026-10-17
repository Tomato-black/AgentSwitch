# 桌面 UI cc-switch 风格重设计 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按 spec 把 `internal/desktop` 三页 + 侧栏重做为 cc-switch v7 风格（中性灰 + 橙主操作 + 卡片流），渠道页重构为「卡片列表 + 整页详情 + 整页新建」，明暗双主题，安全语义零丢失。

**Architecture:** 纯 UI 层重构：theme.go 换 token 表并新增包内软色板；新建 widgets.go 承载视觉词汇（卡片/软徽章/软按钮/类型 tile）；desktop.go 弃用 `ui.Sidebar` 改自绘并扩页面路由；overview/channels/models 三页按 spec §5 重排。storage schema 与 bridge API 签名零改动（渠道启停用已有 `Channels.SetEnabled`）。

**Tech Stack:** Go 1.27.2、mygo v0.5.1（`github.com/egoist/mygo/ui`）、modernc.org/sqlite（不触碰）。

**Spec:** `docs/superpowers/specs/2026-10-17-desktop-ui-ccswitch-redesign-design.md`（token 值、软色板、页面构成、§8 不变量均以它为准；本计划只补「落到哪个文件、什么签名、哪个测试」）。

## Global Constraints

- MyGo 控件签名一律以 `$(go env GOMODCACHE)/github.com/egoist/mygo@v0.5.1/ui` 源码为准，禁止凭记忆。
- 测试全部离线（`testUI(t)` + `ui.NewTester`）；每任务收尾 `go test ./internal/desktop -v` 全绿，最终 `go test -race ./internal/desktop` 全绿。
- UI 文案中文；机器读数（地址、令牌、密钥尾、状态码、耗时、配置片段、上游模型名）一律 `monoFont`（Menlo）。
- 密钥/令牌不得明文上屏：默认掩码 `***`；「复制片段」写真实令牌到剪贴板、展示恒掩码（spec §8.1）。
- 破坏性确认一律 `ui.AlertDialog(c, open, 标题, 正文, "确认…", "取消")`——取消居末位（末位按钮为 Accent 且吃 Enter，feedback.go:195-207），返回值 `==0` 为确认。
- 不改 `internal/bridge`、`internal/storage` 的任何签名与 schema。
- 每任务一 commit，信息用 `feat/fix/refactor(desktop): …` 前缀。

## Review Focus

1. **令牌掩码不变量**：接入卡默认只显 `***`，点「显示」才出真值；复制片段永远写真值、展示行恒掩码——Task 4 `TestOverviewTokenMasking`。
2. **确认位绑定渠道**：密钥/模型删除确认武装后切到另一渠道详情，确认弹窗不得落刀原渠道——Task 6 `TestDetailConfirmDisarmOnChannelSwitch`。
3. **状态守卫**：「启动网关」仅 stopped/failed 可用、「停止网关」仅 running/degraded——Task 4 `TestOverviewStartStopGuards`。
4. **渠道启停不丢字段**：卡片 Switch 只翻 Enabled，Name/BaseURL/Priority/AllowPrivate 原样——Task 5 `TestChannelCardToggleKeepsFields`。
5. **模型开关只切本行**：同名对外模型多渠道时，翻一行另一行不动——Task 8 `TestModelsToggleOnlyTargetRow`。

---

### Task 1: theme.go 新 token 表 + 软色板 + 状态色分离

**Files:**
- Modify: `internal/desktop/theme.go`
- Test: `internal/desktop/theme_test.go`

**Interfaces:**
- Consumes: 现有 `buildTheme(dark bool, textScale float32) *ui.Theme`、`stateWord`、`stateColor`、`ledDot`、`monoFont`。
- Produces（后续所有任务依赖）:
  - `buildTheme` 签名不变，token 值按下表。
  - `type softPalette struct { Act, ActText, Ok, OkText, Warn, WarnText, Danger, DangerText, Gray, GrayText, Link ui.Color }`
  - `func softs(t *ui.Theme) softPalette`（按 `t.Dark` 取边）
  - `func selectedColor(t *ui.Theme) ui.Color`（应用级选中灰，非 Theme.Selection）
  - `func sidebarColor(t *ui.Theme) ui.Color`（浅 `#F5F5F7` / 深 `#232327`）
  - `stateColor`: running 改返回 `t.Success`（degraded/cooling=Warning、failed=Danger、其余 TextMuted 不变）。

- [ ] **Step 1: 重写 theme_test.go 断言（先失败）**

保留 `TestBuildThemeTextScale` 原样；重写 `TestBuildThemeTokens`：

```go
light, dark := buildTheme(false, 1), buildTheme(true, 1)
if light.Accent != ui.Hex("#F97316") || dark.Accent != ui.Hex("#F97316") { t.Fatal("accent") }
if light.Success != ui.Hex("#16A34A") || dark.Success != ui.Hex("#22C55E") { t.Fatal("success") }
if light.Success == light.Accent { t.Fatal("绿橙必须分离（spec §1）") }
if light.Focus != light.Accent { t.Fatal("light focus") }
if dark.Focus != ui.Hex("#D8783A") { t.Fatal("dark focus") }
if light.Radius != 8 { t.Fatal("radius 8（卡片 12 由 Box 显式盖）") }
if selectedColor(light) != ui.Hex("#E4E4EA") || selectedColor(dark) != ui.Hex("#3A3A40") { t.Fatal("selected") }
if sidebarColor(light) != ui.Hex("#F5F5F7") || sidebarColor(dark) != ui.Hex("#232327") { t.Fatal("sidebar") }
sl, sd := softs(light), softs(dark)
if sl.Act != ui.Hex("#FFF1E6") || sl.ActText != ui.Hex("#C2410C") { t.Fatal("soft act light") }
if sd.Link != ui.Hex("#5AA9F8") { t.Fatal("soft link dark") }
for _, s := range []softPalette{sl, sd} {
    if s.Act == s.ActText || s.Ok == s.OkText || s.Warn == s.WarnText || s.Danger == s.DangerText { t.Fatal("soft fg/bg 互异") }
}
```

`TestStateWordAndColor` 中 running 断言改 `stateColor(th,"running") != th.Accent && == th.Success`；新增 `TestSelectionStillVisible`：`light.Selection != selectedColor(light)`（框架 Selection 只管文本选区，必须保持暖色 `#FFDDBB`/`#4A3423`）。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestBuildTheme|TestStateWord|TestSelection' -v`
Expected: FAIL（token 值还是旧青色系）

- [ ] **Step 3: 实现**

`theme.go`：按 spec §3.2/§3.3 表逐字段覆写（19 token + Selection 暖色）；新增 `softPalette/softs/selectedColor/sidebarColor`（全部值照 spec 表，硬编码 `ui.Hex`）；`stateColor` running 分支改 `t.Success`；文件头注释改指新 spec（含「Success 与 Accent 分离」偏离旧档 §2.3 的说明）。`ledDot` 不动（呼吸语义不变）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run 'TestBuildTheme|TestStateWord|TestSelection|TestBuildThemeTextScale' -v`
Expected: PASS 全绿

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/theme.go internal/desktop/theme_test.go
git commit -m "feat(desktop): cc-switch 风格 token 表——橙主操作/独立状态绿/软色板/应用级选中灰"
```

---

### Task 2: widgets.go 视觉词汇（卡片/软徽章/软按钮/胶囊主钮/类型 tile）

**Files:**
- Create: `internal/desktop/widgets.go`
- Test: `internal/desktop/widgets_test.go`

**Interfaces:**
- Consumes: Task 1 的 `softPalette/softs/selectedColor`。
- Produces:
  - `func card(c *ui.Context, t *ui.Theme, fn func())` — `ui.Box(c).Radius(12).Background(t.Surface).Border(1, t.Border).Padding(14)` + `Children(fn)`。
  - `func softBadge(c *ui.Context, t *ui.Theme, s softPalette, kind, label string)` — kind ∈ `"act"|"ok"|"warn"|"danger"|"gray"`，未知 kind 落 gray；`Radius(999)` 底 + `FontSize(t.FontSize-2)` 文字。
  - `func pillButton(c *ui.Context, t *ui.Theme, label string) ui.Element` — Accent 实心胶囊主钮（`ui.ButtonBase` 之上 `Radius(999).Background(t.Accent)` + AccentText 文字；`Clicked()` 可用）。
  - `func softButton(c *ui.Context, t *ui.Theme, s softPalette, kind, label string) ui.Element` — 软底按钮（同 ButtonBase 路子，底=对应 soft 色、字=对应 text 色）；危险动作用 kind="danger"。
  - `func tile(c *ui.Context, t *ui.Theme, s softPalette, name string, size float32)` — 圆角方块（Radius=size*0.25）+ 居中缩写（name 前 2 字符）。
  - `func tileSoft(name string) string` — 返回 soft kind：含 "anthropic"/"claude" → "act"，含 "openai"/"gpt" → "ok"，否则 `fnv32a(name)%3` 轮转 `"link"`(蓝,以 Gray 底+Link 字呈现)/紫/青三组固定色（包内私有，≤5 组防彩虹）。

- [ ] **Step 1: 写失败测试**

```go
func TestTileSoftDeterministic(t *testing.T) {
    if tileSoft("anthropic-official") != "act" || tileSoft("openai-compatible") != "ok" { t.Fatal("preset 映射") }
    if tileSoft("GLM") != tileSoft("GLM") { t.Fatal("must be deterministic") }
}
func TestWidgetsRender(t *testing.T) {
    u, _ := testUI(t)
    tt := ui.NewTester(func(c *ui.Context) {
        th := c.Theme(); s := softs(th)
        card(c, th, func() {
            softBadge(c, th, s, "ok", "启用")
            softBadge(c, th, s, "warn", "冷却 1 把")
            softBadge(c, th, s, "nonsense", "中性")
            softButton(c, th, s, "danger", "停止网关")
            pillButton(c, th, "＋ 添加渠道")
            tile(c, th, s, "PackyCode", 40)
        })
    }, 600, 400)
    for _, want := range []string{"启用", "冷却 1 把", "中性", "停止网关", "＋ 添加渠道", "Pa"} {
        if !tt.HasText(want) { t.Fatalf("missing %q in %v", want, tt.Texts()) }
    }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestTileSoft|TestWidgetsRender' -v`
Expected: FAIL（`tileSoft`/`card` 未定义）

- [ ] **Step 3: 实现 widgets.go**

按 Produces 签名实现。两个按钮 helper 的底座是 `ui.ButtonBase(c)`（api_gen.go 实有），样式节点方法（Radius/Background/Children）与 `Clicked()` 用法以 api_gen.go 与 widgets.go 源码为准；`softButton` 的紫/青两组固定色与 tile 轮转色同为包内私有常量。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run 'TestTileSoft|TestWidgetsRender' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/widgets.go internal/desktop/widgets_test.go
git add docs/superpowers/plans/2026-10-17-desktop-ui-ccswitch-redesign.md
git commit -m "feat(desktop): 视觉词汇 helpers——card/softBadge/softButton/pillButton/tile"
```

---

### Task 3: desktop.go 自绘侧栏 + 页面路由辅助

**Files:**
- Modify: `internal/desktop/desktop.go`
- Test: `internal/desktop/desktop_test.go`

**Interfaces:**
- Consumes: Task 1 `sidebarColor/selectedColor/softs`；icons.go 现有 `iconOverview/iconChannels/iconModels`。
- Produces:
  - `func (u *uiState) navSel() string` — 页 id 归一："channel-detail"/"channel-new" → "channels"，其余原样。
  - `view()` 左列改为自绘侧栏（`ui.Sidebar` 调用删除）：品牌行「AgentSwitch」、导航项（`ui.ButtonBase` 底，高 30、Radius 8，选中 `selectedColor` 底、hover 用 `t.SurfaceHover`，激活项图标 Accent 色）、底部 `sidebarFooter` 原样搬入；侧栏列 `Background(sidebarColor(t))`、宽 200。
  - 首关横条改 card 样式（语义 closeNotice/closeConfirmed 不动）。
  - `pageView` 本任务不改分发集合（"channel-detail"/"channel-new" 两个 case 由 Task 6/7 各自追加）。

- [ ] **Step 1: 写失败测试**

```go
func TestNavSelMapping(t *testing.T) {
    u, _ := testUI(t)
    for page, want := range map[string]string{
        "overview": "overview", "channels": "channels", "models": "models",
        "channel-detail": "channels", "channel-new": "channels",
    } {
        u.page = page
        if got := u.navSel(); got != want { t.Fatalf("navSel(%q)=%q want %q", page, got, want) }
    }
}
func TestSidebarSwitchesPages(t *testing.T) {
    u, _ := testUI(t)
    tt := ui.NewTester(u.view, 1080, 720)
    if err := tt.Click("渠道"); err != nil { t.Fatal(err) }
    if u.page != "channels" { t.Fatalf("page %q", u.page) }
    if err := tt.Click("概览"); err != nil { t.Fatal(err) }
    if u.page != "overview" { t.Fatalf("page %q", u.page) }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestNavSel|TestSidebarSwitches' -v`
Expected: FAIL（`navSel` 未定义；旧 view 无可点「渠道」文字钮）

- [ ] **Step 3: 实现**

desktop.go：删 `ui.Sidebar/SidebarItem` 调用，写自绘侧栏列；`navSel()` 纯函数；导航点击直接 `u.page = id`（原注释「点选即切页」语义保留）。页脚状态位代码原样迁移。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run 'TestNavSel|TestSidebarSwitches' -v`
Expected: PASS（其余既有测试允许在本任务内因 view 结构调整同步小改，但不许删断言）

- [ ] **Step 5: 全量回归 + Commit**

Run: `go test ./internal/desktop -v` → 全绿

```bash
git add internal/desktop/desktop.go internal/desktop/desktop_test.go
git commit -m "feat(desktop): 自绘侧栏（灰底选中）+ navSel 页面归一"
```

---

### Task 4: 概览页四卡

**Files:**
- Modify: `internal/desktop/overview.go`
- Modify: `internal/desktop/overview_test.go`
- Modify: `internal/desktop/desktop.go`（uiState 删 `settingsOpen/prevSettingsOpen` 两字段，Collapsible 弃用）

**Interfaces:**
- Consumes: Task 1 全部、Task 2 `card/softBadge/softButton/pillButton`。
- Produces:
  - `func recentStats(recs []telemetry.RequestRecord) (total, failed int)` — failed = `Status==0 || Status>=400`。
  - 概览四卡布局（spec §5.3）：状态 hero（LED+状态词+mono `http://127.0.0.1:port · 超时 Ns`+日志路径 mono 行+右侧「近 N 条 · M 失败」gray 徽章(total>0 才显)+启动(pillButton, stopped/failed)/停止(softButton danger, running/degraded)）；接入卡（片段代码块 + 显示/复制片段/重新生成——重新生成走 AlertDialog）；密钥池+设置并排两小卡（设置常开：端口/超时/保存，回显与 SaveSettings 语义照旧）；最近请求卡（列：时间/模型/渠道/密钥/状态/耗时/错误——「渠道/密钥」列 = `rec.Channel+"/"+rec.Account` mono；状态码 2xx=Success、4xx=Warning、5xx/0=Danger）。
  - 降级横幅：`DegradedReason != ""` 时 hero 卡下 Warning 色文字条。
  - AlertDialog 确认重生成：`if i := ui.AlertDialog(c, &u.confirmRegen, "重新生成访问令牌", "旧令牌立即失效，接入方需更新配置。", "确认重新生成", "取消"); i == 0 { …原重生成逻辑… }`（`confirmRegen` 语义从「两段按钮位」改为「弹窗 open 位」）。

- [ ] **Step 1: 写失败测试（改写 overview_test.go）**

保留/改写要点：

```go
func TestRecentStats(t *testing.T) {
    recs := []telemetry.RequestRecord{{Status: 200}, {Status: 429}, {Status: 0}, {Status: 502}}
    total, failed := recentStats(recs)
    if total != 4 || failed != 3 { t.Fatalf("%d %d", total, failed) }
}
func TestOverviewHeroAndCards(t *testing.T) { /* NewTester(u.overviewView): HasText "运行中"/"接入 Claude Code"/"密钥池"/"设置"/"最近请求"/"日志:" */ }
func TestOverviewTokenMasking(t *testing.T) {
    u, _ := testUI(t); tt := ui.NewTester(u.overviewView, 1080, 720)
    if tt.HasText(u.token) { t.Fatal("默认不得明文") }
    if err := tt.Click("显示"); err != nil { t.Fatal(err) }
    if !tt.HasText(u.token) { t.Fatal("显示后须明文") }
}
func TestOverviewRegenConfirm(t *testing.T) {
    u, _ := testUI(t); old := u.token; tt := ui.NewTester(u.overviewView, 1080, 720)
    if err := tt.Click("重新生成"); err != nil { t.Fatal(err) }
    if u.token != old { t.Fatal("未确认不得换令牌") }
    if err := tt.Click("确认重新生成"); err != nil { t.Fatal(err) }
    if u.token == old { t.Fatal("确认后须换令牌") }
}
func TestOverviewStartStopGuards(t *testing.T) {
    u, _ := testUI(t)
    u.status.State = "running"
    tt := ui.NewTester(u.overviewView, 1080, 720)
    if tt.HasText("启动网关") { t.Fatal("running 不得出现启动钮") }
    if !tt.HasText("停止网关") { t.Fatal("running 须有停止钮") }
    u.status.State = "stopped"
    tt = ui.NewTester(u.overviewView, 1080, 720)
    if !tt.HasText("启动网关") || tt.HasText("停止网关") { t.Fatal("stopped 守卫") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestRecentStats|TestOverview' -v`
Expected: FAIL（`recentStats` 未定义/旧布局文案缺失）

- [ ] **Step 3: 实现 overview.go 四卡**

照 spec §5.3 重排；「复制配置片段」沿用现 clipboard 写真实片段逻辑；掩码行、`showToken` 单按钮换 label 原样保留；设置保存的回显改为「打开页面即回显 `gs.Port/gs.TimeoutSeconds`」仅当输入框为空时（原 Collapsible 打开瞬间回显逻辑等价替换，保存语义不动）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run 'TestRecentStats|TestOverview' -v` → PASS；
Run: `go test ./internal/desktop -v` → 全绿（其它页测试若被 uiState 字段删除波及，同步小改）

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/overview.go internal/desktop/overview_test.go internal/desktop/desktop.go
git commit -m "feat(desktop): 概览四卡——状态hero/接入/池+设置/最近请求，AlertDialog 重生成确认"
```

---

### Task 5: 渠道列表页（卡片流）

**Files:**
- Modify: `internal/desktop/channels.go`（`channelsView` 重写为列表态；表单态代码挪 Task 6/7，本任务先保留原表单函数不删）
- Modify: `internal/desktop/channels_test.go`

**Interfaces:**
- Consumes: Task 2 helpers；`u.recent`；`u.svc.Gateway.PoolStatus()`；`bridge.ChannelView`（含 `CoolingCount`——以 bridge 源码为准，若无则冷却徽章仅在 `PoolStatus()` 含该渠道时显示）。
- Produces:
  - `func (u *uiState) openChannelDetail(id string)` — `u.page="channel-detail"; u.chDetailID=id; u.chDetailSeg=0`。
  - `func (u *uiState) toggleChannelEnabled(id string, on bool)` — `u.svc.Channels.SetEnabled`。
  - uiState 新字段：`chDetailID string`、`chDetailSeg int`、`chDeleteArm bool`、`chDeleteID string`。
  - 列表态 UI：页头（「渠道」粗体 + muted `N 个渠道 · M 个启用 · 密钥池 a/b 可用`，a=ΣKeyCount−len(PoolStatus())、b=ΣKeyCount；右侧 `pillButton("＋ 添加渠道")` → `resetChannelForm(); u.page="channel-new"`）；渠道卡 = `card` 内：`tile(ch.Type+" "+ch.Name)` + 名称粗体 + 徽章行（「首选」=启用且优先级为启用渠道最小，softBadge act；启用/已停用 ok/gray；`CoolingCount>0` → 「冷却 n 把」warn）+ BaseURL（`softs.Link` 色）+ meta 行 `优先级 N · M 把密钥 · 近 100 条 a✓ b✗`（`channelStats`；a+b==0 不显统计段）+ 右侧 Switch（`on := ch.Enabled; if ui.Switch(c,&on).Changed() { u.toggleChannelEnabled(ch.ID,on) }`）+ `ui.MenuButton(c,"⋯",…)`（测试连通/编辑/删除）。卡体 `ui.ButtonBase` 底，点击 → `openChannelDetail(ch.ID)`。
  - 删除确认：`ui.AlertDialog(c, &u.chDeleteArm, "删除渠道", ch.Name, "确认删除", "取消")==0 → Remove(ch.ID)`；`chDeleteID` 记武装对象，弹窗仅对 `chDeleteID` 匹配的卡渲染。
  - `func channelStats(recs []telemetry.RequestRecord, channel string) (ok, fail int)` — ok=2xx、fail=`Status==0||>=400`，按 `rec.Channel` 过滤。
  - 空态：「还没有渠道」+ `pillButton` 引导。

- [ ] **Step 1: 写失败测试**

```go
func TestChannelStats(t *testing.T) {
    recs := []telemetry.RequestRecord{{Channel: "main", Status: 200}, {Channel: "main", Status: 500}, {Channel: "other", Status: 200}}
    ok, fail := channelStats(recs, "main")
    if ok != 1 || fail != 1 { t.Fatalf("%d %d", ok, fail) }
}
func TestChannelCardToggleKeepsFields(t *testing.T) {
    u, _ := testUI(t)
    v, err := u.svc.Channels.Create("anthropic-official", "main", "https://a.example.com", 10, true, true)
    if err != nil { t.Fatal(err) }
    tt := ui.NewTester(u.channelsView, 1080, 720)
    u.toggleChannelEnabled(v.ID, false)
    got := u.svc.Channels.List()[0]
    if got.Enabled || got.BaseURL != "https://a.example.com" || got.Priority != 10 || !got.AllowPrivateUpstream {
        t.Fatalf("toggle 丢字段: %+v", got)
    }
}
func TestChannelListCardsAndDelete(t *testing.T) {
    u, _ := testUI(t)
    v, _ := u.svc.Channels.Create("anthropic-official", "main", "https://a.example.com", 10, true, false)
    tt := ui.NewTester(u.channelsView, 1080, 720)
    if !tt.HasText("首选") || !tt.HasText("https://a.example.com") { t.Fatalf("%v", tt.Texts()) }
    u.chDeleteArm, u.chDeleteID = true, v.ID
    tt = ui.NewTester(u.channelsView, 1080, 720)
    if err := tt.Click("确认删除"); err != nil { t.Fatal(err) }
    if len(u.svc.Channels.List()) != 0 { t.Fatal("确认后须删除") }
}
func TestChannelCardOpensDetail(t *testing.T) {
    u, _ := testUI(t)
    v, _ := u.svc.Channels.Create("anthropic-official", "main", "https://a.example.com", 10, true, false)
    tt := ui.NewTester(u.channelsView, 1080, 720)
    if err := tt.Click("main"); err != nil { t.Fatal(err) }
    if u.page != "channel-detail" || u.chDetailID != v.ID { t.Fatalf("%q %q", u.page, u.chDetailID) }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestChannelStats|TestChannelCard|TestChannelList' -v`
Expected: FAIL

- [ ] **Step 3: 实现列表态**

`channelsView` 重写；「编辑」菜单项与卡体点击都走 `openChannelDetail`；「测试连通」结果写 `u.chTestMsg`（详情页头也复用同一信息位）。原表单创建路径函数（`submitChannel/loadChannel/resetChannelForm/labelsOf/resolveTypeID`）本任务不删——Task 6/7 迁移后再清理。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run 'TestChannel' -v` → PASS（旧创建类测试若因列表态移除表单而挂，**移入 Task 7 一步中改写**，本任务先注释标记 `// moved: Task 7`，不许直接删文件）

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/channels.go internal/desktop/channels_test.go
git commit -m "feat(desktop): 渠道卡片列表——首选/启停 Switch/⋯菜单/统计 meta/AlertDialog 删除"
```

---

### Task 6: 渠道详情页（Segmented 三段）

**Files:**
- Modify: `internal/desktop/channels.go`
- Modify: `internal/desktop/desktop.go`（pageView 加 `"channel-detail"` case）
- Modify: `internal/desktop/channels_test.go`

**Interfaces:**
- Consumes: Task 5 的 `chDetailID/chDetailSeg` 与全部 bridge 渠道方法；Task 2 helpers。
- Produces:
  - `func segmented(c *ui.Context, t *ui.Theme, selected *int, labels []string)` — `ui.SegmentedBase(c, selected, len(labels))`，Track `Background(t.SurfacePressed).Radius(8)`，选中片 `Background(selectedColor(t))`（浅色框架默认白片在白底隐身，toggle.go:45-54，故必须自组）。
  - `func (u *uiState) channelDetailView(c *ui.Context)` — `Get(chDetailID)` 失败或渠道已删 → `u.page="channels"` 兜底返回列表。
  - uiState 新字段：`keyFormOpen bool`、`chTypeResetConfirm bool`、`prevDetailID string`（原 `prevChannelID` 语义改挂此字段）。
  - 页头：返回钮（`ui.Button("返回列表")` → `u.page="channels"`）+ tile + 名称 + 徽章 + `softButton(act,"连通测试")` + `pillButton("保存渠道")`。
  - Segmented 三段标签：`连接配置` / `密钥池 N` / `渠道模型 M`（N/M 实时数量）。
  - 连接配置段 = 现表单字段全量（类型 Select+labelsOf、名称、Base URL、优先级、启用、允许私网、msg）；**类型变更确认**：`editID!=""` 且 Select 值变化（`prevType` 检测改挂详情态）→ `AlertDialog("更换类型","Base URL 将重置为该类型预设默认值。","确认更换","取消")`，确认→`baseURL=presetOf(id).DefaultBaseURL`，取消→`typeID` 回退为 `detail.Channel.Type`。
  - 密钥池段：工具行（「＋ 批量导入」`softButton(act)` 切 `keyFormOpen`；开时显 名称/TextArea/允许文件降级/导入密钥——语义照旧）+ 密钥表（名称/尾4/存储/状态/优先级/⋯MenuButton：停用启用、测试、删除）+ per-key 统计列「24h」= `keyStats(recs, chName, key.Name)`（`Status` 口径同 channelStats；标题注明「近 100 条」）。
  - 渠道模型段 = 现模型列全量 + 行尾 MenuButton。
  - 两处删除确认改 AlertDialog，`confirmOwnerID` 绑定机制原样保留：弹窗渲染条件 `u.xxxConfirm && u.confirmOwnerID == detail.Channel.ID`。
  - `prevDetailID != u.chDetailID` 时清：子表选中、两个确认位、confirmOwnerID、三处提示（原 prevChannelID 块平移）。

- [ ] **Step 1: 写失败测试**

```go
func TestDetailSectionsRender(t *testing.T) {
    u, _ := testUI(t)
    v, _ := u.svc.Channels.Create("anthropic-official", "main", "https://a.example.com", 10, true, false)
    u.svc.Channels.AddKeys(v.ID, "k1", "sk-test-1234", false)
    u.openChannelDetail(v.ID)
    tt := ui.NewTester(u.channelDetailView, 1080, 720)
    for _, want := range []string{"连接配置", "密钥池 1", "渠道模型 0", "保存渠道", "连通测试"} {
        if !tt.HasText(want) { t.Fatalf("missing %q: %v", want, tt.Texts()) }
    }
}
func TestDetailConfirmDisarmOnChannelSwitch(t *testing.T) {
    u, _ := testUI(t)
    v1, _ := u.svc.Channels.Create("anthropic-official", "c1", "https://1.example.com", 10, true, false)
    v2, _ := u.svc.Channels.Create("anthropic-official", "c2", "https://2.example.com", 20, true, false)
    u.openChannelDetail(v1.ID)
    u.keyConfirmDelete, u.confirmOwnerID = true, v1.ID // 模拟在 c1 武装
    u.openChannelDetail(v2.ID)                         // 切渠道必须清位
    if u.keyConfirmDelete || u.confirmOwnerID != "" { t.Fatal("切换后确认位必须解除") }
    tt := ui.NewTester(u.channelDetailView, 1080, 720)
    if tt.HasText("确认删除密钥") { t.Fatal("未重新武装不得出现确认钮") }
}
func TestDetailTypeChangeConfirm(t *testing.T) {
    u, _ := testUI(t)
    v, _ := u.svc.Channels.Create("anthropic-official", "c1", "https://api.anthropic.com", 10, true, false)
    u.openChannelDetail(v.ID)
    tt := ui.NewTester(u.channelDetailView, 1080, 720)
    u.chForm.typeID = "openai-compatible" // 模拟 Select 变更
    tt = ui.NewTester(u.channelDetailView, 1080, 720)
    if !u.chTypeResetConfirm { t.Fatal("已保存渠道换类型必须武装确认") }
    if err := tt.Click("确认更换"); err != nil { t.Fatal(err) }
    if u.chForm.baseURL == "https://api.anthropic.com" { t.Fatal("确认后须预填新预设默认 URL") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestDetail' -v`
Expected: FAIL（`channelDetailView` 未定义）

- [ ] **Step 3: 实现**

按 Produces 重排；现密钥/模型两列的按钮行逻辑全部迁入对应段（按钮文案改菜单项/软按钮，语义零变化）；`keyStats` 顺手实现；desktop.go `pageView` 补 case。原 master-detail 的「详情跟随表格行选中」代码路径删除（详情已锚定 `chDetailID`）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run 'TestDetail|TestChannelsKeys|TestModelAdd' -v` → PASS（旧密钥/模型流程测试改经详情页路径驱动：`openChannelDetail` + `tt.Click`）

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/channels.go internal/desktop/desktop.go internal/desktop/channels_test.go
git commit -m "feat(desktop): 渠道详情页——Segmented 三段/类型更换确认/确认位绑定平移"
```

---

### Task 7: 新建渠道页（类型 chips + 表单 + 密钥批量）

**Files:**
- Modify: `internal/desktop/channels.go`（迁走并删除旧表单残留：`submitChannel` 的 Update 分支改由详情页「保存渠道」调用，Create 分支并入本页）
- Modify: `internal/desktop/desktop.go`（pageView 加 `"channel-new"` case）
- Modify: `internal/desktop/channels_test.go`

**Interfaces:**
- Consumes: Task 2 helpers；`provider.Presets()/LookupPreset`；Task 5 `resetChannelForm/selectChannel`。
- Produces:
  - `func (u *uiState) channelNewView(c *ui.Context)` — 返回钮（→`u.page="channels"`）+「添加渠道」标题 + chips 行（`provider.Presets()` 全量；选中 chip = Accent 实心白字，未选 = soft gray；点击 → `u.chForm.typeID=id; u.chForm.baseURL=presetOf(id).DefaultBaseURL`——**取代类型 Select**，本页无「更换类型确认」）+ 表单（名称/Base URL/优先级/启用/允许私网，与连接配置段同款控件）+ 密钥批量 TextArea（复用 `u.keyForm.keysText`）+ `pillButton("创建渠道")`。
  - `func (u *uiState) createFromNewPage()` — 校验优先级整数 → `Create(...)` → 成功后若 `keyForm.keysText != ""` 则 `AddKeys(v.ID, chForm.name, keysText, keyForm.allowFallback)`（失败写 `chForm.msg` 但渠道保留）→ `resetChannelForm()` → `u.page="channels"; u.selectChannel(v.ID)`。
  - 详情页「保存渠道」：`Update(u.chDetailID, …)` 全字段回写（原 `submitChannel` Update 分支逻辑，msg 提示不变）。

- [ ] **Step 1: 写失败测试**

```go
func TestNewPageChipPrefillsBaseURL(t *testing.T) {
    u, _ := testUI(t)
    tt := ui.NewTester(u.channelNewView, 1080, 720)
    if err := tt.Click("Claude 官方"); err != nil { t.Fatal(err) } // 以 Presets() 实际 label 为准
    if u.chForm.baseURL != "https://api.anthropic.com" { t.Fatalf("chip 预填 %q", u.chForm.baseURL) }
}
func TestNewPageCreateAndKeys(t *testing.T) {
    u, _ := testUI(t)
    u.chForm.name, u.chForm.baseURL, u.chForm.priorityInput = "main", "https://a.example.com", "10"
    u.keyForm.keysText = "sk-a\nsk-b"
    tt := ui.NewTester(u.channelNewView, 1080, 720)
    if err := tt.Click("创建渠道"); err != nil { t.Fatal(err) }
    if u.page != "channels" { t.Fatalf("page %q", u.page) }
    d, err := u.svc.Channels.Get(u.svc.Channels.List()[0].ID)
    if err != nil || len(d.Keys) != 2 { t.Fatalf("keys %v err %v", d.Keys, err) }
}
func TestUpdateFromDetailKeepsFields(t *testing.T) { /* 经详情页改名称→保存→List 读回断言全字段 */ }
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestNewPage|TestUpdateFromDetail' -v`
Expected: FAIL

- [ ] **Step 3: 实现**

`createFromNewPage` + `channelNewView`；详情页保存按钮接 Update 分支；删除 `submitChannel` 中已迁移的死代码与 `loadChannel`（详情页不再「载入所选」，打开详情即 `Get` 回显——在 Step 1 给 `TestUpdateFromDetailKeepsFields` 写明：打开详情时表单字段须等于渠道现值）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -v` → 全绿（Task 5 遗留的 `// moved: Task 7` 测试在本步完成改写或删除）

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/channels.go internal/desktop/desktop.go internal/desktop/channels_test.go
git commit -m "feat(desktop): 新建渠道页——类型 chips/批量密钥随建随导/详情页 Update 保存"
```

---

### Task 8: 模型页（搜索 + 每行开关）

**Files:**
- Modify: `internal/desktop/models.go`
- Modify: `internal/desktop/models_test.go`

**Interfaces:**
- Consumes: Task 2 `softBadge/softButton`；Task 5 `openChannelDetail`；`bridge.ModelsAgg.ListAll/SetEnabled`。
- Produces:
  - uiState 新字段：`modelQuery string`。
  - `func sortModelRows(rows []bridge.ModelAggregateView)` — 按 `PublicModel` 升序、同组内 `ChannelName` 升序（相邻归组）。
  - `func filterModelRows(rows []bridge.ModelAggregateView, q string) []bridge.ModelAggregateView` — q 忽略大小写包含于 PublicModel/UpstreamModel/ChannelName 任一；空 q 原样。
  - `func (u *uiState) toggleModelRow(id string, on bool)` — `ModelsAgg.SetEnabled(id, on)`。
  - UI：页头（「模型」+ muted `N 条渠道模型 · 来自 M 渠道 · 停用 X`（去重渠道计数）+ 右 `ui.SearchField(c, &u.modelQuery)`）；表列：启用/对外模型/来源渠道/上游映射——开关列 = `on := m.Enabled; if ui.Switch(c,&on).Changed() { u.toggleModelRow(m.ID,on) }`；来源渠道 = softBadge gray，包 `ui.ButtonBase` 点击 → 按 ChannelName 查 `List()` 得 id 后 `openChannelDetail`（查不到不跳）；上游映射 mono。原「停用/启用」按钮行删除。

- [ ] **Step 1: 写失败测试**

```go
func TestSortModelRows(t *testing.T) {
    rows := []bridge.ModelAggregateView{{PublicModel: "b", ChannelName: "z"}, {PublicModel: "a", ChannelName: "y"}, {PublicModel: "a", ChannelName: "x"}}
    sortModelRows(rows)
    if rows[0].ChannelName != "x" || rows[2].PublicModel != "b" { t.Fatal("分组排序") }
}
func TestModelsToggleOnlyTargetRow(t *testing.T) {
    u, _ := testUI(t)
    c1, _ := u.svc.Channels.Create("anthropic-official", "c1", "https://1.example.com", 10, true, false)
    c2, _ := u.svc.Channels.Create("anthropic-official", "c2", "https://2.example.com", 20, true, false)
    m1, _ := u.svc.Channels.AddModel(c1.ID, "same-model", "up-1")
    m2, _ := u.svc.Channels.AddModel(c2.ID, "same-model", "up-2")
    u.toggleModelRow(m2.ID, false)
    rows := bridge.ModelsAgg{A: u.svc.Channels.A}.ListAll()
    for _, r := range rows {
        if r.ID == m1.ID && !r.Enabled { t.Fatal("c1 行不得被波及") }
        if r.ID == m2.ID && r.Enabled { t.Fatal("c2 行须已停用") }
    }
}
func TestModelsSearchFilter(t *testing.T) {
    // 建两渠道两模型；u.modelQuery="glm" 后 NewTester(modelsView) 只剩匹配行文案
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestSortModel|TestModels' -v`
Expected: FAIL

- [ ] **Step 3: 实现 models.go**

按 Produces 重排；渲染顺序 = filter(sort(ListAll()))；表行内交互若 `ui.Switch` 在 Table cell 内点击异常（实现时验证），回退方案：弃 `ui.Table` 改用 card 行列表（每行 Row：Switch+文本+chip+mono），行为语义不变。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -v` → 全绿

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/models.go internal/desktop/models_test.go
git commit -m "feat(desktop): 模型页——搜索/分组排序/行内 Switch 只切本行"
```

---

### Task 9: 收尾——文档指称同步 + 全量验证

**Files:**
- Modify: `CLAUDE.md`
- Modify: `docs/superpowers/specs/2026-10-17-desktop-ui-mygo-native-design.md`（顶部加取代注记）
- Modify: `internal/desktop/theme.go`（头注释复核指新 spec）

- [ ] **Step 1: CLAUDE.md 同步**

「UI 决策（2026-10-17）」段补一句：视觉系统由 `2026-10-17-desktop-ui-ccswitch-redesign-design.md` 取代（橙主操作/绿橙分离/卡片流/列表+详情 IA）；「桌面 UI 设计系统（交换机面板）见该 spec 与 theme.go」的指称改指新 spec。

- [ ] **Step 2: 旧 spec 顶部加注**

> 注：本文档的视觉系统（§2 原则 3、§3 token、§5 页面视觉）已被 `2026-10-17-desktop-ui-ccswitch-redesign-design.md` 取代；交互语义与安全不变量仍有效。

- [ ] **Step 3: 全量验证**

Run: `go vet ./...` → 无输出
Run: `go test ./...` → 全绿
Run: `go test -race ./internal/desktop` → 全绿
Run: `go build ./...` → 无错误

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md docs/superpowers/specs/2026-10-17-desktop-ui-mygo-native-design.md internal/desktop/theme.go
git commit -m "docs: UI 重设计落地——CLAUDE.md/旧 spec 指称同步"
```

- [ ] **Step 5: 人工验收提示（非阻塞）**

`go tool mygo dev` 起真窗口过一遍：明暗两套外观、渠道增删改测全流程、令牌显示/复制、首关教育、托盘重开窗口。发现问题走常规修复流程，不带病提交。
