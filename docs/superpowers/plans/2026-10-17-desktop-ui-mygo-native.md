# 桌面 UI「交换机面板」设计系统实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 按 spec 把 `internal/desktop` 的四页 UI 从默认样式重排为「交换机面板」设计系统：品牌 Theme token、边栏导航、面板头状态读数、丝印表单，并更新 CLAUDE.md 记录「全部 MyGo 原生、废弃 frontend/」决策。

**Architecture:** 纯 UI 层改造——新增 `theme.go`（token + 状态视觉词汇 + LED）与 `icons.go`（内联 SVG），`desktop.go` 的 `view` 改为 SetTheme + Sidebar 骨架（`u.page` id 不变），四个页面文件重排布局；bridge/业务语义零改动。所有提示文案、按钮词、表头保持原字符串（测试文案不变量）。

**Tech Stack:** Go 1.27.2、mygo v0.5.1（`ui` 包：Theme/Sidebar/Badge/Collapsible/Box/ParseSVG/Tester）、现有离线测试（`ui.NewTester`）。

**Spec:** `docs/superpowers/specs/2026-10-17-desktop-ui-mygo-native-design.md`

## Global Constraints

- 只动 `internal/desktop` 与 `CLAUDE.md`；不碰 gateway/protocol/router/provider/bridge 的任何文件与签名。
- **文案不变量**：以下字符串逐字保留，任何任务不得改动——按钮词：`启动网关`/`停止网关`/`保存设置`/`显示`/`隐藏`/`复制令牌`/`重新生成令牌`/`确认重新生成`/`复制配置片段`/`继续关闭窗口`/`取消`/`保存`/`更新`/`载入所选`/`删除`/`确认删除`/`停用/启用`/`轮换密钥`/`测试`；提示与标签：`访问令牌`/`日志:`/`ANTHROPIC_BASE_URL`/`***`/`窗口关闭后应用将驻留托盘，网关继续运行。`/`警告：凭据使用文件降级存储（未启用系统密钥环）`/`降级原因: `/`密钥已轮换`/`连通正常 (`/` 失败: `/`请先在供应商页创建供应商`/`优先级必须是整数`/`端口/超时必须是整数`/`已保存`/`已保存；重启网关后生效`；表头六列/四列/两列标题不变。
- 令牌掩码不变量：未点「显示」时，令牌真值不得出现在任何展示文本中（复制路径除外，同现状）。
- `buildTheme` 色值必须与 spec §3.2 表逐字一致；`Success`/`Focus` 与 `Accent` 同色。
- 机器读数一律 `Font(monoFont)`（`monoFont = "Menlo"`）；标签不用等宽。
- 测试全部离线；涉及 UI 状态的包跑 `go test -race ./internal/desktop`。
- 对 spec 的两处已知偏离（均为实现必需，不回改 spec）：① `buildTheme` 签名为 `(dark bool, textScale float32)`——自设主题必须自己接入系统文字缩放，否则可访问性回退；② 表单标签左对齐固定宽（spec 写右对齐）——文字对齐 API 未经验证，不假定。

## Review Focus

按 spec 隐含但单任务测试盖不全、最容易咬到人的五类输入（已各配钉子测试或人工验收步骤）：

1. **令牌掩码回归**：重排中令牌行/配置片段若把真值渲染进展示文本，密钥即泄漏 → 由 Task 4 保留运行的 `TestOverviewTokenAndSnippet` 钉住（原样保留，禁止弱化断言）。
2. **导航点击撞名**：`Click` 命中第一个同文案 label——「供应商」「账号」「模型」既是边栏项又是表头/表单标签，边栏必须先于页面内容构建，否则导航测试点错目标 → 由 Task 3 的 `TestNavigationSwitchesPage`/`TestNavigationCoversAllPages` 迁移版钉住。
3. **TextScale 未接**：`buildTheme` 若忽略 `Preferences().TextScale`，系统大字号的用户看到 13px 死字 → Task 1 的 `TestBuildThemeTextScale` 钉住（含 0 值守卫）。
4. **未知状态串**：状态机未来新增状态值时 `stateWord` 必须原样回显、`stateColor` 落弱灰，不得 panic 或空串 → Task 1 的 `TestStateWordAndColor` 钉住。
5. **LED 呼吸与图标着色只在真机可见**：`Loop` 呼吸表现、`currentColor` 图标是否随主题，Tester 断言不了 → Task 7 的 `go tool mygo dev` 人工验收清单钉住；呼吸异常则按代码内注释退化为静态点。

---

### Task 1: theme.go — token 表、状态视觉词汇、LED 组件

**Files:**
- Create: `internal/desktop/theme.go`
- Test: `internal/desktop/theme_test.go`

**Interfaces:**
- Consumes: `ui.LightTheme()/ui.DarkTheme()/ui.Hex/ui.Box/ui.EaseInOut`（mygo v0.5.1）。
- Produces（后续任务依赖的准确签名）：
  - `const monoFont = "Menlo"`
  - `func buildTheme(dark bool, textScale float32) *ui.Theme`
  - `func stateWord(state string) string`
  - `func stateColor(t *ui.Theme, state string) ui.Color`
  - `func ledDot(c *ui.Context, t *ui.Theme, state string)`

- [ ] **Step 1: 写失败测试**

创建 `internal/desktop/theme_test.go`：

```go
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestBuildThemeTokens 覆盖 spec §3.2：双外观主色锚点、Success==Accent
//（一个颜色想法）、语义色互异、度量常量。
func TestBuildThemeTokens(t *testing.T) {
	light, dark := buildTheme(false, 1), buildTheme(true, 1)
	if light.Accent != ui.Hex("#0E7A5F") {
		t.Fatalf("light accent %v", light.Accent)
	}
	if dark.Accent != ui.Hex("#45D0B4") {
		t.Fatalf("dark accent %v", dark.Accent)
	}
	if light.Success != light.Accent || dark.Success != dark.Accent {
		t.Fatal("Success must equal Accent (spec §3.2: one color idea)")
	}
	if light.Focus != light.Accent || dark.Focus != dark.Accent {
		t.Fatal("Focus must equal Accent")
	}
	for _, th := range []*ui.Theme{light, dark} {
		if th.Danger == th.Accent || th.Warning == th.Accent || th.TextMuted == th.Accent {
			t.Fatal("Danger/Warning/TextMuted must differ from Accent")
		}
		if th.Radius != 6 || th.Spacing != 4 {
			t.Fatalf("metrics %v %v", th.Radius, th.Spacing)
		}
	}
	if light.Background == dark.Background {
		t.Fatal("appearances must differ")
	}
}

// TestBuildThemeTextScale 覆盖文字缩放接入（Review Focus 3）：自设主题必须
// 自己乘 TextScale，<=0 守卫为 1。
func TestBuildThemeTextScale(t *testing.T) {
	if got := buildTheme(false, 1.25).FontSize; got != 16.25 {
		t.Fatalf("FontSize %v, want 16.25", got)
	}
	if got := buildTheme(true, 0).FontSize; got != 13 {
		t.Fatalf("FontSize %v, want guard-to-1 => 13", got)
	}
}

// TestStateWordAndColor 覆盖状态视觉词汇（Review Focus 4）：四态映射 +
// 未知状态原样回显、落弱灰，四态颜色互异。
func TestStateWordAndColor(t *testing.T) {
	th := buildTheme(true, 1)
	for state, want := range map[string]string{
		"running": "运行中", "degraded": "降级", "failed": "故障", "stopped": "已停止",
	} {
		if got := stateWord(state); got != want {
			t.Fatalf("stateWord(%q)=%q want %q", state, got, want)
		}
	}
	if got := stateWord("hibernating"); got != "hibernating" {
		t.Fatalf("unknown state must echo raw, got %q", got)
	}
	if stateColor(th, "hibernating") != th.TextMuted {
		t.Fatal("unknown state must fall back to TextMuted")
	}
	cols := map[string]ui.Color{
		"running": th.Accent, "degraded": th.Warning, "failed": th.Danger, "stopped": th.TextMuted,
	}
	seen := map[ui.Color]string{}
	for state, col := range cols {
		if stateColor(th, state) != col {
			t.Fatalf("stateColor(%q)", state)
		}
		if prev, dup := seen[col]; dup {
			t.Fatalf("color collision %s vs %s", prev, state)
		}
		seen[col] = state
	}
}

// TestLedDotRenders 冒烟：LED 组件在 Tester 帧内可构建不 panic（点本身
// 无文本，Texts 为空即正常）。
func TestLedDotRenders(t *testing.T) {
	th := buildTheme(false, 1)
	ui.NewTester(func(c *ui.Context) { ledDot(c, th, "running") }, 40, 40).Frame()
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestBuildTheme|TestStateWord|TestLedDot' -v`
Expected: 编译失败（`undefined: buildTheme` 等）。

- [ ] **Step 3: 实现 theme.go**

```go
// theme.go 是「交换机面板」设计系统的 token 表与状态视觉词汇。
// spec：docs/superpowers/specs/2026-10-17-desktop-ui-mygo-native-design.md §2/§3。
// 已知偏离 spec §3.1：buildTheme 增加 textScale 入参——框架默认主题随系统
// 文字缩放（Preferences.TextScale），自设主题必须自己接上，否则可访问性回退。
package desktop

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// monoFont 是机器读数（地址、令牌、密钥尾、耗时等）的等宽字体族；
// 非 macOS 的回退行为见 spec §8.3（当前仅 darwin 一等公民）。
const monoFont = "Menlo"

// buildTheme 按 dark 构造品牌主题：以框架主题为底、覆写 spec §3.2 全部
// 19 个颜色 token 与度量（未列字段保持框架默认）。textScale<=0 视为 1。
func buildTheme(dark bool, textScale float32) *ui.Theme {
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
	}
	if textScale <= 0 {
		textScale = 1
	}
	if dark {
		t.Background = ui.Hex("#16181D")
		t.Surface = ui.Hex("#1E2128")
		t.SurfaceHover = ui.Hex("#262A33")
		t.SurfacePressed = ui.Hex("#2E333E")
		t.Border = ui.Hex("#2A2E37")
		t.Text = ui.Hex("#E8EAF0")
		t.TextMuted = ui.Hex("#9AA1B0")
		t.Accent = ui.Hex("#45D0B4")
		t.AccentHover = ui.Hex("#5FDFC6")
		t.AccentPressed = ui.Hex("#2FA891")
		t.AccentText = ui.Hex("#0D1512")
		t.Danger = ui.Hex("#E5484D")
		t.Warning = ui.Hex("#F5B83D")
		t.Selection = ui.Hex("#1F4A41")
		t.Inverse = ui.Hex("#E8EAF0")
		t.InverseText = ui.Hex("#16181D")
		t.Scrollbar = ui.Hex("#3A404C")
	} else {
		t.Background = ui.Hex("#F3F4F2")
		t.Surface = ui.Hex("#FFFFFF")
		t.SurfaceHover = ui.Hex("#F0F2F1")
		t.SurfacePressed = ui.Hex("#E4E8E6")
		t.Border = ui.Hex("#D5DAD6")
		t.Text = ui.Hex("#1C2320")
		t.TextMuted = ui.Hex("#67736D")
		t.Accent = ui.Hex("#0E7A5F")
		t.AccentHover = ui.Hex("#0B6550")
		t.AccentPressed = ui.Hex("#08503F")
		t.AccentText = ui.Hex("#FFFFFF")
		t.Danger = ui.Hex("#C63D3D")
		t.Warning = ui.Hex("#B97A0A")
		t.Selection = ui.Hex("#C9E8DF")
		t.Inverse = ui.Hex("#1C2320")
		t.InverseText = ui.Hex("#F3F4F2")
		t.Scrollbar = ui.Hex("#B9C1BB")
	}
	// 一个颜色想法（spec §2.3）：品牌青就是链路健康色。
	t.Success = t.Accent
	t.Focus = t.Accent
	t.Radius = 6
	t.Spacing = 4
	t.FontSize = 13 * textScale
	return t
}

// stateWord 把网关状态译成面板状态词；未知状态原样回显（Review Focus 4）。
func stateWord(state string) string {
	switch state {
	case "running":
		return "运行中"
	case "degraded":
		return "降级"
	case "failed":
		return "故障"
	case "stopped":
		return "已停止"
	default:
		return state
	}
}

// stateColor 给状态配 LED 色：running=健康青（=Accent），degraded=琥珀，
// failed=红，stopped/未知=弱灰。
func stateColor(t *ui.Theme, state string) ui.Color {
	switch state {
	case "running":
		return t.Accent
	case "degraded":
		return t.Warning
	case "failed":
		return t.Danger
	default:
		return t.TextMuted
	}
}

// ledDot 画状态 LED：10px 圆点，色由 stateColor 决定；running 时用 Loop
// 做缓慢呼吸。preferences.go 语义：Animate 在 ReduceMotion 下直达目标，
// 而 Loop「shows that something is going on」继续走——呼吸恰属后者，无需
// 分支。人工验收若呼吸表现异常，删掉 Loop 块退化为静态点（spec §8.4）。
func ledDot(c *ui.Context, t *ui.Theme, state string) {
	b := ui.Box(c).Width(10).Height(10).Radius(5).Background(stateColor(t, state))
	if state == "running" {
		v := b.Loop("gw-led", 2100*time.Millisecond, ui.EaseInOut)
		b.Opacity(0.55 + 0.45*v)
	}
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run 'TestBuildTheme|TestStateWord|TestLedDot' -v`
Expected: 全部 PASS。

- [ ] **Step 5: 全包回归（其余测试不受影响）**

Run: `go test ./internal/desktop`
Expected: PASS（新文件未接入 view，无行为变化）。

- [ ] **Step 6: Commit**

```bash
git add internal/desktop/theme.go internal/desktop/theme_test.go
git commit -m "feat(desktop): 交换机面板设计系统 token 表与状态视觉词汇（theme.go）"
```

---

### Task 2: icons.go — 四枚内联 SVG 导航图标

**Files:**
- Create: `internal/desktop/icons.go`
- Test: `internal/desktop/icons_test.go`

**Interfaces:**
- Consumes: `ui.ParseSVG([]byte) (*ui.SVG, error)`。
- Produces: `var iconOverview, iconProviders, iconAccounts, iconModels *ui.SVG`（Task 3 的 `ui.SidebarItem(c, id, icon, label)` 消费）。

- [ ] **Step 1: 写失败测试**

创建 `internal/desktop/icons_test.go`：

```go
package desktop

import "testing"

// TestIconsParsed 冒烟：包级 mustSVG 在 init 解析失败即 panic，测试能跑
// 到这里即四枚图标解析成功；再显式断言非 nil 防将来改为惰性构造时漏检。
func TestIconsParsed(t *testing.T) {
	for name, ic := range map[string]*ui.SVG{
		"overview": iconOverview, "providers": iconProviders,
		"accounts": iconAccounts, "models": iconModels,
	} {
		if ic == nil {
			t.Fatalf("icon %s is nil", name)
		}
	}
}
```

注意 import 需补 `"github.com/egoist/mygo/ui"`。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run TestIconsParsed -v`
Expected: 编译失败（`undefined: iconOverview`）。

- [ ] **Step 3: 实现 icons.go**

16px 线性图标，`stroke="currentColor"` 跟随主题文字色（spec §8.1 待验证项：若 `go tool mygo dev` 下图标不着色，把 `currentColor` 换成深浅两套都可见的中性色 `#9AA1B0` 并在此注释记录）。

```go
// icons.go 是边栏导航的四枚 16px 内联线性图标（概览=面板格、供应商=插头、
// 账号=钥匙、模型=分支路由），ui.ParseSVG 内联定义，色随主题文字
//（spec §4；§8.1 待验证：currentColor 若不着色，退化为固定中性色）。
package desktop

import "github.com/egoist/mygo/ui"

func mustSVG(data string) *ui.SVG {
	s, err := ui.ParseSVG([]byte(data))
	if err != nil {
		panic("desktop: bad icon svg: " + err.Error())
	}
	return s
}

const svgHead = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" width="16" height="16" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">`

var (
	iconOverview = mustSVG(svgHead +
		`<rect x="1.5" y="1.5" width="5.5" height="5.5" rx="1"/><rect x="9" y="1.5" width="5.5" height="5.5" rx="1"/>` +
		`<rect x="1.5" y="9" width="5.5" height="5.5" rx="1"/><rect x="9" y="9" width="5.5" height="5.5" rx="1"/></svg>`)
	iconProviders = mustSVG(svgHead +
		`<path d="M5.5 1.5v4M10.5 1.5v4M4 5.5h8v2.5a4 4 0 0 1-8 0V5.5zM8 12v2.5"/></svg>`)
	iconAccounts = mustSVG(svgHead +
		`<circle cx="5" cy="11" r="3"/><path d="M7.3 8.7L14 2M10.5 5.5l2.2 2.2"/></svg>`)
	iconModels = mustSVG(svgHead +
		`<circle cx="8" cy="8" r="2.2"/><path d="M8 1.5v4.3M8 10.2v4.3M1.5 8h4.3M10.2 8h4.3"/></svg>`)
)
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run TestIconsParsed -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/icons.go internal/desktop/icons_test.go
git commit -m "feat(desktop): 边栏导航四枚内联 SVG 图标"
```

---

### Task 3: view 骨架 — SetTheme + 边栏导航 + 页脚状态位 + 首关横条

**Files:**
- Modify: `internal/desktop/desktop.go`（`view` 函数整体重写；新增 `sidebar`/`sidebarFooter` 两个方法；`openWindow` 不动）
- Test: `internal/desktop/desktop_test.go`（两处断言迁移 + 一个新测试）

**Interfaces:**
- Consumes: Task 1 的 `buildTheme/stateWord/stateColor/ledDot/monoFont`；Task 2 的四枚图标；`ui.Sidebar/SidebarItem/Divider/Box`。
- Produces: `view(c)` 根结构 = `SetTheme` → `Row[Sidebar 列, 内容列]`；`u.page` 的四个合法 id 不变（`overview/providers/accounts/models`），未识别 id 仍落 overview。页面分发抽为 `pageView(c *ui.Context)`（Task 4-6 只重排各 `xxxView`，不碰分发）。

- [ ] **Step 1: 迁移失败测试**

`desktop_test.go` 三处改动（其余不动）：

① `TestOverviewShowsState` 的断言换状态词（原始英文 `stopped` 改为面板词，状态词在边栏页脚恒显，本测试改后即钉住页脚）：

```go
func TestOverviewShowsState(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("已停止") {
		t.Fatalf("texts %v", tt.Texts())
	}
}
```

② `TestNavigationSwitchesPage` 的 overview 存在性/泄漏探针从 `网关状态` 换成 `访问令牌`（概览重排后不再有该文案；令牌区是概览独有标记）：

```go
	if err := tt.Click("供应商"); err != nil {
		t.Fatal(err)
	}
	if u.page != "providers" {
		t.Fatalf("page = %q, want providers", u.page)
	}
	if tt.HasText("访问令牌") {
		t.Fatalf("overview leaked into providers page: %v", tt.Texts())
	}
	if err := tt.Click("概览"); err != nil {
		t.Fatal(err)
	}
	if u.page != "overview" {
		t.Fatalf("page = %q, want overview", u.page)
	}
	if !tt.HasText("访问令牌") {
		t.Fatalf("overview view missing: %v", tt.Texts())
	}
```

（测试其余部分——HasText("概览")/HasText("供应商")、点边栏项切 `u.page`——不变：`Tester.Click` 点第一个同文案 label，边栏先于内容构建即命中导航，Review Focus 2。）

③ 新增未知页回退测试（钉住分发默认分支）：

```go
// TestUnknownPageFallsBackToOverview 覆盖分发默认分支：未识别的 u.page
// 落回概览（边栏 id 与 u.page 集合一致性的兜底）。
func TestUnknownPageFallsBackToOverview(t *testing.T) {
	u, _ := testUI(t)
	u.page = "bogus"
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("访问令牌") {
		t.Fatalf("unknown page must fall back to overview: %v", tt.Texts())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestOverviewShowsState|TestNavigation|TestUnknownPage' -v`
Expected: `TestOverviewShowsState` FAIL（无「已停止」文本）、`TestUnknownPageFallsBackToOverview` 编译失败；`TestNavigationSwitchesPage` 在旧布局下仍 PASS（`访问令牌` 现存）。

- [ ] **Step 3: 重写 view 骨架**

`desktop.go` 中把整个 `view` 方法（现 94-132 行的导航行 + 首关块 + switch 分发）替换为：

```go
// view 是窗口根：按系统外观选品牌主题，左边栏（导航 + 页脚全局状态位）、
// 右内容列（首关横条 + 当前页）。SetTheme 必须在任何子件构建前调用
//（api_gen.go：SetTheme makes the frame use t）。u.page 直接充当 Sidebar
// 的选中 id（四个 id 与导航项一致），点选即切页，无需 Changed 处理。
func (u *uiState) view(c *ui.Context) {
	c.SetTheme(buildTheme(c.Theme().Dark, c.Preferences().TextScale))
	t := c.Theme()
	ui.Row(c).Gap(0).Grow(1).Children(func() {
		// 左列：边栏 + 分隔线 + 页脚状态位（状态是主角：任何页可见）
		ui.Column(c).Gap(0).Children(func() {
			ui.Sidebar(c, &u.page, func() {
				ui.SidebarItem(c, "overview", iconOverview, "概览")
				ui.SidebarItem(c, "providers", iconProviders, "供应商")
				ui.SidebarItem(c, "accounts", iconAccounts, "账号")
				ui.SidebarItem(c, "models", iconModels, "模型")
			}).Width(200).Grow(1)
			ui.Divider(c)
			u.sidebarFooter(c, t)
		})
		// 右列：首关横条（全页可见，I4）+ 当前页
		ui.Column(c).Gap(12).Padding(16).Grow(1).Children(func() {
			if u.closeNotice && !u.closeConfirmed {
				ui.Row(c).Gap(8).Padding(8).Border(1, t.Border).Children(func() {
					ui.Text(c, "窗口关闭后应用将驻留托盘，网关继续运行。").Grow(1)
					if ui.PrimaryButton(c, "继续关闭窗口").Clicked() {
						u.closeConfirmed = true
						if w := mygo.FocusedWindow(); w != nil {
							w.Close()
						}
					}
					if ui.Button(c, "取消").Clicked() {
						u.closeNotice = false
					}
				})
			}
			u.pageView(c)
		})
	})
}

// pageView 按 u.page 分发页面；未识别的 page 落到概览。
func (u *uiState) pageView(c *ui.Context) {
	switch u.page {
	case "providers":
		u.providersView(c)
	case "accounts":
		u.accountsView(c)
	case "models":
		u.modelsView(c)
	default:
		u.overviewView(c)
	}
}

// sidebarFooter 是边栏页脚的全局状态位：LED + 状态词 + 等宽地址。
// 地址空（未启动过）显示「未监听」。
func (u *uiState) sidebarFooter(c *ui.Context, t *ui.Theme) {
	state := string(u.status.State)
	ui.Row(c).Gap(8).Padding(12).Children(func() {
		ledDot(c, t, state)
		ui.Column(c).Gap(2).Children(func() {
			ui.Text(c, stateWord(state))
			addr := u.status.Addr
			if addr == "" {
				addr = "未监听"
			}
			ui.Text(c, addr).Font(monoFont).TextColor(t.TextMuted)
		})
	})
}
```

注意：`ui` 包 import 不变；首关横条文案与两按钮行为逐字保留（`TestCloseNoticeBlocksFirstClose` 不动、必须继续 PASS）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run 'TestOverviewShowsState|TestNavigation|TestUnknownPage|TestCloseNotice|TestNeedsReopen' -v`
Expected: 全部 PASS。

- [ ] **Step 5: 全包回归**

Run: `go test -race ./internal/desktop`
Expected: PASS（overview 等页面仍旧布局，仅外骨架换了）。

- [ ] **Step 6: Commit**

```bash
git add internal/desktop/desktop.go internal/desktop/desktop_test.go
git commit -m "feat(desktop): view 改边栏骨架——SetTheme 品牌主题、页脚全局状态位、首关横条"
```

---

### Task 4: overview.go — 面板头、设置折叠面板、等宽请求表

**Files:**
- Modify: `internal/desktop/overview.go`（`overviewView` 整体重写；`defaultSnippetPort` 常量与掩码逻辑保留）
- Modify: `internal/desktop/desktop.go`（`uiState` 结构体加一个字段 `prevSettingsOpen bool`）
- Test: `internal/desktop/overview_test.go`（新增两个测试；既有四个测试全部原样保留）

**Interfaces:**
- Consumes: Task 1 的 `stateWord/stateColor/ledDot/monoFont/buildTheme`；`ui.Collapsible(c, label, open *bool, fn)`——`open` 直接用 `&u.settingsOpen`。
- Produces: 概览页布局（Task 7 人工验收对象）；P0-B 槽位注释（spec §9）。

- [ ] **Step 1: 写失败测试**

`overview_test.go` 末尾追加：

```go
// TestSettingsCollapsibleOpens 覆盖设置折叠面板：点「设置」展开（原独立
// 按钮改 Collapsible），打开瞬间回显当前端口/超时（原「设置」按钮做的事，
// 改为帧内 prev/cur 检测）。
func TestSettingsCollapsibleOpens(t *testing.T) {
	u, a := testUI(t)
	st, _ := a.CurrentSettings()
	tt := ui.NewTester(u.overviewView, 900, 700)
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !u.settingsOpen {
		t.Fatal("collapsible must open on click")
	}
	if !tt.HasText("端口:") || !tt.HasText(strconv.Itoa(st.Port)) {
		t.Fatalf("settings echo missing: %v", tt.Texts())
	}
}

// TestOverviewEmptyRequestsHint 覆盖空请求表引导（spec §5.4：空态是邀请，
// 不是空白）。
func TestOverviewEmptyRequestsHint(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.overviewView, 900, 700)
	if !tt.HasText("暂无请求") {
		t.Fatalf("empty hint missing: %v", tt.Texts())
	}
}
```

（`strconv` 需加入测试文件 import。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestSettingsCollapsible|TestOverviewEmpty' -v`
Expected: FAIL——旧布局「设置」按钮存在但展开面板无回显逻辑差异……实际上旧代码点「设置」也回显。此步预期：`TestOverviewEmptyRequestsHint` FAIL（无「暂无请求」）；`TestSettingsCollapsibleOpens` 旧布局可能 PASS——允许（它钉住的是迁移后行为）；以空态测试红为准。

- [ ] **Step 3: 重写 overviewView**

`overview.go` 的 `overviewView` 整体替换为（`defaultSnippetPort`、复制/掩码逻辑逐字保留）：

```go
// overviewView 是概览页（面板头形态，spec §5.1）：LED+状态词+等宽地址的
// 面板头、操作行、设置折叠面板、令牌行、配置片段、P0-B 槽位注释与最近
// 请求表。首关横条在 view 层（desktop.go），不在本页。
func (u *uiState) overviewView(c *ui.Context) {
	t := c.Theme()
	gs := u.svc.Gateway.Status() // 每帧取一次：设置回显与片段端口共用
	state := string(u.status.State)
	ui.Column(c).Gap(12).Children(func() {
		// 面板头：LED + 大号状态词 + 右对齐等宽地址
		ui.Row(c).Gap(10).Children(func() {
			ledDot(c, t, state)
			ui.Text(c, stateWord(state)).Bold().FontSize(t.FontSize + 4)
			ui.Box(c).Grow(1)
			if u.status.Addr != "" {
				ui.Text(c, u.status.Addr).Font(monoFont).TextColor(t.TextMuted)
			}
		})
		if u.status.DegradedReason != "" {
			ui.Textf(c, "降级原因: %s", u.status.DegradedReason).TextColor(t.Warning)
		}
		// 日志路径提示（spec §17 可诊断性）：恒显
		ui.Textf(c, "日志: %s", u.logPath).TextColor(t.TextMuted)
		ui.Divider(c)

		// 操作行：启动/停止按状态守卫（语义与放行集合不变）
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, "启动网关").Clicked() &&
				(state == "stopped" || state == "failed") {
				go func() { _ = u.svc.Gateway.Start() }()
			}
			if ui.Button(c, "停止网关").Clicked() &&
				(state == "running" || state == "degraded") {
				go func() { _ = u.svc.Gateway.Stop() }()
			}
		})

		// 设置折叠面板：open 即 settingsOpen；打开瞬间回显当前值——原
		// 「设置」按钮的行为改为 prev/cur 帧内检测（Collapsible 无打开钩子）。
		if u.settingsOpen && !u.prevSettingsOpen {
			u.portInput = strconv.Itoa(gs.Port)
			u.timeoutInput = strconv.Itoa(gs.TimeoutSeconds)
			u.settingsMsg = ""
		}
		u.prevSettingsOpen = u.settingsOpen
		ui.Collapsible(c, "设置", &u.settingsOpen, func() {
			ui.Column(c).Gap(8).Children(func() {
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "端口:").Width(96).TextColor(t.TextMuted)
					ui.TextInput(c, &u.portInput)
				})
				ui.Row(c).Gap(12).Children(func() {
					ui.Text(c, "请求超时（秒）:").Width(96).TextColor(t.TextMuted)
					ui.TextInput(c, &u.timeoutInput)
				})
				ui.Row(c).Gap(8).Children(func() {
					if ui.PrimaryButton(c, "保存设置").Clicked() {
						port, err1 := strconv.Atoi(u.portInput)
						to, err2 := strconv.Atoi(u.timeoutInput)
						if err1 != nil || err2 != nil {
							u.settingsMsg = "端口/超时必须是整数"
						} else {
							restart, err := u.svc.Gateway.SaveSettings(port, to)
							if err != nil {
								u.settingsMsg = err.Error()
							} else if restart {
								u.settingsMsg = "已保存；重启网关后生效"
							} else {
								u.settingsMsg = "已保存"
							}
						}
					}
					if u.settingsMsg != "" {
						ui.Text(c, u.settingsMsg)
					}
				})
			})
		})

		// 访问令牌行：掩码/显示切换、复制、二次确认重新生成（语义不变）。
		// 显示/隐藏是同一个按钮换 label（原代码写法）——不能双按钮同帧
		// 都构建，否则 Tester 的 Click("显示") 在 showToken=true 时仍命中。
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "访问令牌:")
			masked := "***"
			if u.showToken {
				masked = u.token
			}
			ui.Text(c, masked).Font(monoFont).Grow(1)
			label := "显示"
			if u.showToken {
				label = "隐藏"
			}
			if ui.Button(c, label).Clicked() {
				u.showToken = !u.showToken
			}
			if ui.Button(c, "复制令牌").Clicked() {
				mygo.Clipboard.Write(transfer.TextData(u.token))
			}
			if ui.Button(c, "重新生成令牌").Clicked() {
				u.confirmRegen = !u.confirmRegen
			}
			if u.confirmRegen {
				if ui.Button(c, "确认重新生成").Clicked() {
					if tok, err := u.svc.Gateway.RegenerateToken(); err == nil {
						u.token = tok
						u.showToken = false
					}
					u.confirmRegen = false
				}
			}
		})
```

继续（片段块 + P0-B 槽位 + 请求表）：

```go
		// Claude Code 配置片段：端口用当前实际值；展示与复制分离、未点
		// 「显示」时掩码不变量照旧（Review Focus 1——掩码逻辑逐字保留）。
		ui.Text(c, "Claude Code 配置片段:")
		port := gs.Port
		if port <= 0 {
			port = defaultSnippetPort
		}
		base := fmt.Sprintf("export ANTHROPIC_BASE_URL=http://127.0.0.1:%d", port)
		snippet := base + "\nexport ANTHROPIC_AUTH_TOKEN=" + u.token // 复制用：真实片段
		shownTok := "***"                                            // 展示默认掩码
		if u.showToken {
			shownTok = u.token
		}
		display := base + "\nexport ANTHROPIC_AUTH_TOKEN=" + shownTok
		for _, line := range strings.Split(display, "\n") {
			ui.Text(c, line).Font(monoFont)
		}
		if ui.Button(c, "复制配置片段").Clicked() {
			mygo.Clipboard.Write(transfer.TextData(snippet))
		}

		// P0-B 槽位（spec §9）：账号池状态条预留位——每账号一枚迷你状态点
		// +名称。本期只留位不实现，接入点在此 Divider 之后。
		ui.Divider(c)

		// 最近请求：六列不变；时间/状态/耗时等宽化，非 2xx 状态染语义色
		if len(u.recent) == 0 {
			ui.Text(c, "暂无请求——Agent 接入后此处显示最近 100 条").TextColor(t.TextMuted)
		}
		ui.Text(c, "最近请求:")
		cols := []ui.TableColumn{
			{Title: "时间"}, {Title: "模型"}, {Title: "账号"},
			{Title: "状态"}, {Title: "耗时(ms)"}, {Title: "错误"},
		}
		ui.Table(c, &u.table, cols, len(u.recent), func(row, col int) {
			rec := u.recent[row]
			switch col {
			case 0:
				ui.Text(c, rec.StartedAt.Format("15:04:05")).SingleLine().Font(monoFont)
			case 1:
				ui.Text(c, rec.Model).SingleLine()
			case 2:
				ui.Text(c, rec.Account).SingleLine()
			case 3:
				e := ui.Text(c, strconv.Itoa(rec.Status)).SingleLine().Font(monoFont)
				switch {
				case rec.Status == 0 || rec.Status >= 500:
					e.TextColor(t.Danger)
				case rec.Status >= 400:
					e.TextColor(t.Warning)
				}
			case 4:
				ui.Text(c, strconv.FormatInt(rec.DurationMs, 10)).SingleLine().Font(monoFont)
			case 5:
				ui.Text(c, rec.ErrorClass).SingleLine()
			}
		}).Grow(1)
	})
}
```

import 面不变（`fmt/strconv/strings` + mygo/transfer/ui 均已用）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run 'TestOverview|TestSettings' -v`
Expected: 全部 PASS（含原有 `TestOverviewTokenAndSnippet`/`TestOverviewRecentTableRenders`/`TestOverviewStartButtonAllowedFromFailed`/`TestOverviewShowsLogPath`——掩码、表头、Failed 放行、日志路径四条不变量原样）。

- [ ] **Step 5: 全包回归**

Run: `go test -race ./internal/desktop`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/desktop/overview.go internal/desktop/overview_test.go internal/desktop/desktop.go
git commit -m "feat(desktop): 概览面板头——LED 状态读数、设置折叠面板、等宽请求表与空态引导"
```

---

### Task 5: providers.go + models.go — 页头记数、启用徽章、丝印表单、空态

**Files:**
- Modify: `internal/desktop/providers.go`（`providersView` 重排；`resetProvForm` 不动）
- Modify: `internal/desktop/models.go`（`modelsView` 重排；表单复位逻辑不动）
- Test: `internal/desktop/providers_test.go`、`internal/desktop/models_test.go`（各新增一个空态测试；既有测试全部原样保留）

**Interfaces:**
- Consumes: Task 1 的 `monoFont`；`ui.Badge(c, text)`（独立胶囊文本，可落在表格单元格）。
- Produces: 供应商/模型页布局（页头行模式被 Task 6 账号页复用：标题 Bold + TextMuted 记数，不用间隔点连接）。

- [ ] **Step 1: 写失败测试**

`providers_test.go` 末尾追加：

```go
// TestProvidersEmptyHint 覆盖空表引导（spec §5.4）。
func TestProvidersEmptyHint(t *testing.T) {
	u, _ := testUI(t)
	u.page = "providers"
	tt := ui.NewTester(u.providersView, 1000, 700)
	if !tt.HasText("尚无供应商") {
		t.Fatalf("empty hint missing: %v", tt.Texts())
	}
}
```

`models_test.go` 末尾追加：

```go
// TestModelsEmptyHint 覆盖空表引导（spec §5.4）。
func TestModelsEmptyHint(t *testing.T) {
	u, _, _ := testUIWithCreds(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 900, 600)
	if !tt.HasText("尚无模型别名") {
		t.Fatalf("empty hint missing: %v", tt.Texts())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestProvidersEmpty|TestModelsEmpty' -v`
Expected: 两测 FAIL（无空态文案）。

- [ ] **Step 3: 重排 providersView**

`providers.go` 的 `providersView` 整体替换为（Create/Update/载入/删除/测试的全部语义与文案不变，仅布局重排）：

```go
// providersView 是供应商页（spec §5.2）：页头记数、供应商表（启用列
// Badge）、丝印式表单（固定宽弱色标签 + 字段同行）与操作行。
func (u *uiState) providersView(c *ui.Context) {
	t := c.Theme()
	provs := u.svc.Providers.List()
	ui.Column(c).Gap(12).Children(func() {
		// 页头：标题 + TextMuted 记数（不用间隔点连接）
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "供应商").Bold().FontSize(t.FontSize + 2)
			ui.Text(c, strconv.Itoa(len(provs))).TextColor(t.TextMuted)
		})
		if len(provs) == 0 {
			ui.Text(c, "尚无供应商——在下方表单创建第一个").TextColor(t.TextMuted)
		}
		cols := []ui.TableColumn{{Title: "名称"}, {Title: "协议"}, {Title: "Base URL"}, {Title: "启用"}}
		ui.Table(c, &u.provList, cols, len(provs), func(row, col int) {
			p := provs[row]
			switch col {
			case 0:
				ui.Text(c, p.Name).SingleLine()
			case 1:
				ui.Text(c, p.Protocol).SingleLine()
			case 2:
				ui.Text(c, p.BaseURL).SingleLine().Font(monoFont)
			case 3:
				if p.Enabled {
					ui.Badge(c, "启用")
				} else {
					ui.Text(c, "停用").TextColor(t.TextMuted)
				}
			}
		}).Grow(1)
		ui.Divider(c)

		// 表单：丝印式两栏（spec §5.2；已知偏离：标签左对齐固定宽，
		// 文字右对齐 API 未验证不假定）
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.provForm.name).Grow(1)
			ui.Text(c, "协议:").Width(96).TextColor(t.TextMuted)
			ui.Select(c, &u.provForm.protocol, []string{"anthropic"})
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "Base URL:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.provForm.baseURL).Grow(1)
		})
		ui.Row(c).Gap(16).Children(func() {
			ui.Text(c, "启用").TextColor(t.TextMuted)
			ui.Switch(c, &u.provForm.enabled)
			ui.Text(c, "允许私网上游").TextColor(t.TextMuted)
			ui.Switch(c, &u.provForm.allowPrivate)
		})
		if u.provForm.msg != "" {
			ui.Text(c, u.provForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			saveLabel := "保存"
			if u.provForm.editID != "" {
				saveLabel = "更新"
			}
			if ui.PrimaryButton(c, saveLabel).Clicked() {
				if u.provForm.editID == "" {
					if _, err := u.svc.Providers.Create(u.provForm.name, u.provForm.protocol,
						u.provForm.baseURL, u.provForm.enabled, u.provForm.allowPrivate); err != nil {
						u.provForm.msg = err.Error()
					} else {
						u.resetProvForm()
					}
				} else {
					if err := u.svc.Providers.Update(u.provForm.editID, u.provForm.name,
						u.provForm.baseURL, u.provForm.enabled, u.provForm.allowPrivate); err != nil {
						u.provForm.msg = err.Error()
					} else {
						u.resetProvForm()
					}
				}
			}
			if ui.Button(c, "载入所选").Clicked() {
				if sel := u.provList.Selected; sel != nil && *sel >= 0 && *sel < len(provs) {
					p := provs[*sel]
					u.provForm = provFormData{name: p.Name, protocol: p.Protocol, baseURL: p.BaseURL,
						enabled: p.Enabled, allowPrivate: p.AllowPrivateUpstream, editID: p.ID}
					u.provTestMsg = ""
					u.provConfirmDelete = false
				}
			}
			if ui.Button(c, "删除").Clicked() && u.provForm.editID != "" {
				u.provConfirmDelete = true
			}
			if u.provConfirmDelete {
				if ui.Button(c, "确认删除").Clicked() {
					if err := u.svc.Providers.Remove(u.provForm.editID); err != nil {
						u.provForm.msg = err.Error()
					} else {
						u.resetProvForm()
					}
					u.provConfirmDelete = false
				}
			}
			if ui.Button(c, "测试连通").Clicked() && u.provForm.editID != "" {
				res := u.svc.Providers.Test(u.provForm.editID)
				if res.OK {
					u.provTestMsg = "连通正常 (" + res.Stage + ")"
				} else {
					u.provTestMsg = res.Stage + " 失败: " + res.Error
				}
			}
		})
		if u.provTestMsg != "" {
			ui.Text(c, u.provTestMsg)
		}
	})
}
```

注意：`providers.go` 需新增 import `"strconv"`。

- [ ] **Step 4: 重排 modelsView**

`models.go` 的 `modelsView` 整体替换为：

```go
// modelsView 是模型别名页（spec §5.2）：页头记数、两列表（上游名等宽）、
// 丝印表单与操作行（保存、载入所选、删除两段式确认）。
func (u *uiState) modelsView(c *ui.Context) {
	t := c.Theme()
	models := u.svc.Models.List()
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "模型别名").Bold().FontSize(t.FontSize + 2)
			ui.Text(c, strconv.Itoa(len(models))).TextColor(t.TextMuted)
		})
		ui.Text(c, "Agent 请求名 → 上游模型名").TextColor(t.TextMuted)
		if len(models) == 0 {
			ui.Text(c, "尚无模型别名——在下方表单添加").TextColor(t.TextMuted)
		}
		cols := []ui.TableColumn{{Title: "公开模型名"}, {Title: "上游模型名"}}
		ui.Table(c, &u.modelList, cols, len(models), func(row, col int) {
			m := models[row]
			if col == 0 {
				ui.Text(c, m.PublicModel).SingleLine()
			} else {
				ui.Text(c, m.UpstreamModel).SingleLine().Font(monoFont)
			}
		}).Grow(1)
		ui.Divider(c)

		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "公开模型名:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.modelForm.public).Grow(1)
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "上游模型名:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.modelForm.upstream).Grow(1)
		})
		if u.modelForm.msg != "" {
			ui.Text(c, u.modelForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, "保存").Clicked() {
				if u.modelForm.editID == "" {
					if _, err := u.svc.Models.Create(u.modelForm.public, u.modelForm.upstream); err != nil {
						u.modelForm.msg = err.Error()
					} else {
						u.modelForm = modelFormData{}
					}
				} else {
					if err := u.svc.Models.Update(u.modelForm.editID, u.modelForm.public, u.modelForm.upstream); err != nil {
						u.modelForm.msg = err.Error()
					} else {
						u.modelForm = modelFormData{}
					}
				}
			}
			if ui.Button(c, "载入所选").Clicked() {
				if sel := u.modelList.Selected; sel != nil && *sel >= 0 && *sel < len(models) {
					m := models[*sel]
					u.modelForm = modelFormData{public: m.PublicModel, upstream: m.UpstreamModel, editID: m.ID}
					u.modelConfirmDelete = false
				}
			}
			if ui.Button(c, "删除").Clicked() && u.modelForm.editID != "" {
				u.modelConfirmDelete = true
			}
			if u.modelConfirmDelete {
				if ui.Button(c, "确认删除").Clicked() {
					if err := u.svc.Models.Remove(u.modelForm.editID); err != nil {
						u.modelForm.msg = err.Error()
					} else {
						u.modelForm = modelFormData{}
					}
					u.modelConfirmDelete = false
				}
			}
		})
	})
}
```

注意：原页首「模型别名（Agent 请求名 → 上游模型名）」一行拆为页头 + 弱色副题两节点（`HasText("模型")` 仍命中；模型表头列断言在 overview，不受影响）。`models.go` 需新增 import `"strconv"`。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/desktop -run 'TestProviders|TestModels' -v`
Expected: 全部 PASS（新建/错误透传/载入清位/两段确认四条既有不变量 + 两个新空态）。

- [ ] **Step 6: 全包回归**

Run: `go test -race ./internal/desktop`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/desktop/providers.go internal/desktop/providers_test.go internal/desktop/models.go internal/desktop/models_test.go
git commit -m "feat(desktop): 供应商/模型页丝印重排——页头记数、启用徽章、空态引导"
```

---

### Task 6: accounts.go — 降级横幅、丝印表单、等宽尾列

**Files:**
- Modify: `internal/desktop/accounts.go`（`accountsView` 重排；`submitAccount`/`resetAccForm`/`credentialFallbackInUse`/`provNameOf` 不动）
- Test: `internal/desktop/accounts_test.go`（新增一个空态测试；既有测试全部原样保留）

**Interfaces:**
- Consumes: Task 1 的 `monoFont`；Task 5 的页头行模式；`credentialFallbackInUse`（本文件既有）。
- Produces: 账号页布局（Task 7 人工验收对象）。

- [ ] **Step 1: 写失败测试**

`accounts_test.go` 末尾追加：

```go
// TestAccountsEmptyHint 覆盖空表引导（spec §5.4）。
func TestAccountsEmptyHint(t *testing.T) {
	u, _, _ := testUIWithCreds(t)
	u.page = "accounts"
	tt := ui.NewTester(u.accountsView, 1000, 700)
	if !tt.HasText("尚无账号") {
		t.Fatalf("empty hint missing: %v", tt.Texts())
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run TestAccountsEmptyHint -v`
Expected: FAIL。

- [ ] **Step 3: 重排 accountsView**

`accounts.go` 的 `accountsView` 整体替换为（保存/轮换/停用/测试/删除的全部语义与文案不变）：

```go
// accountsView 是账号页（spec §5.3）：页顶降级警示横幅（Warning 文字 +
// 边框，文案不变）、六列表（密钥尾4/存储等宽）、丝印表单与操作行。
func (u *uiState) accountsView(c *ui.Context) {
	t := c.Theme()
	accounts := u.svc.Accounts.List()
	provs := u.svc.Providers.List()
	provNames := make([]string, 0, len(provs))
	provByID := map[string]string{}
	for _, p := range provs {
		provNames = append(provNames, p.Name)
		provByID[p.Name] = p.ID
	}
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "账号").Bold().FontSize(t.FontSize + 2)
			ui.Text(c, strconv.Itoa(len(accounts))).TextColor(t.TextMuted)
		})
		// 降级警示横幅：任一账号密钥落 fallback-file 即页顶常驻（文案逐字保留）
		if credentialFallbackInUse(accounts) {
			ui.Row(c).Gap(8).Padding(8).Border(1, t.Warning).Children(func() {
				ui.Text(c, "警告：凭据使用文件降级存储（未启用系统密钥环）").TextColor(t.Warning).Grow(1)
			})
		}
		if len(accounts) == 0 {
			ui.Text(c, "尚无账号——在下方表单添加第一个").TextColor(t.TextMuted)
		}
		cols := []ui.TableColumn{{Title: "名称"}, {Title: "供应商"}, {Title: "优先级"},
			{Title: "启用"}, {Title: "密钥尾4"}, {Title: "存储"}}
		ui.Table(c, &u.accList, cols, len(accounts), func(row, col int) {
			a := accounts[row]
			switch col {
			case 0:
				ui.Text(c, a.Name).SingleLine()
			case 1:
				ui.Text(c, provNameOf(provs, a.ProviderID)).SingleLine()
			case 2:
				ui.Text(c, strconv.Itoa(a.Priority)).SingleLine().Font(monoFont)
			case 3:
				if a.Enabled {
					ui.Badge(c, "启用")
				} else {
					ui.Text(c, "停用").TextColor(t.TextMuted)
				}
			case 4:
				ui.Text(c, a.KeyTail).SingleLine().Font(monoFont)
			case 5:
				ui.Text(c, a.SecretBackend).SingleLine().Font(monoFont)
			}
		}).Grow(1)
		ui.Divider(c)

		// 表单：三行丝印（名称+优先级一行；供应商一行；密钥独占）
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "名称:").Width(120).TextColor(t.TextMuted)
			ui.TextInput(c, &u.accForm.name).Grow(1)
			ui.Text(c, "优先级（数字越小越优先）:").Width(160).TextColor(t.TextMuted)
			ui.TextInput(c, &u.accForm.priorityInput).Grow(1)
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "供应商:").Width(120).TextColor(t.TextMuted)
			ui.Select(c, &u.accForm.providerName, provNames).Grow(1)
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "API Key（保存后不可回显）:").Width(160).TextColor(t.TextMuted)
			ui.TextInput(c, &u.accForm.apiKey).Grow(1)
		})
		ui.Row(c).Gap(16).Children(func() {
			ui.Text(c, "启用").TextColor(t.TextMuted)
			ui.Switch(c, &u.accForm.enabled)
			ui.Text(c, "允许文件降级").TextColor(t.TextMuted)
			ui.Switch(c, &u.accForm.allowFallback)
		})
		if u.accForm.msg != "" {
			ui.Text(c, u.accForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			label := "保存"
			if u.accForm.editID != "" {
				label = "更新"
			}
			if ui.PrimaryButton(c, label).Clicked() {
				u.submitAccount(provByID)
			}
			if ui.Button(c, "载入所选").Clicked() {
				if sel := u.accList.Selected; sel != nil && *sel >= 0 && *sel < len(accounts) {
					a := accounts[*sel]
					u.accForm = accFormData{name: a.Name, providerID: a.ProviderID,
						providerName:  provNameOf(provs, a.ProviderID),
						priorityInput: strconv.Itoa(a.Priority),
						enabled:       a.Enabled, editID: a.ID}
					u.accTestMsg = ""
					u.accConfirmDelete = false
				}
			}
			if ui.Button(c, "停用/启用").Clicked() {
				if sel := u.accList.Selected; sel != nil && *sel >= 0 && *sel < len(accounts) {
					a := accounts[*sel]
					_ = u.svc.Accounts.SetEnabled(a.ID, !a.Enabled)
				}
			}
			if ui.Button(c, "轮换密钥").Clicked() && u.accForm.editID != "" && u.accForm.apiKey != "" {
				if err := u.svc.Accounts.RotateSecret(u.accForm.editID, u.accForm.apiKey, u.accForm.allowFallback); err != nil {
					u.accForm.msg = err.Error()
				} else {
					u.accForm.msg = "密钥已轮换"
					u.accForm.apiKey = ""
				}
			}
			if ui.Button(c, "测试").Clicked() {
				id := u.accForm.editID
				if id == "" {
					if sel := u.accList.Selected; sel != nil && *sel >= 0 && *sel < len(accounts) {
						id = accounts[*sel].ID
					}
				}
				if id != "" {
					res := u.svc.Accounts.Test(id)
					if res.OK {
						u.accTestMsg = "连通正常 (" + res.Stage + ")"
					} else {
						u.accTestMsg = res.Stage + " 失败: " + res.Error
					}
				}
			}
			if ui.Button(c, "删除").Clicked() && u.accForm.editID != "" {
				u.accConfirmDelete = true
			}
			if u.accConfirmDelete {
				if ui.Button(c, "确认删除").Clicked() {
					if err := u.svc.Accounts.Remove(u.accForm.editID); err != nil {
						u.accForm.msg = err.Error()
					} else {
						u.resetAccForm()
					}
					u.accConfirmDelete = false
				}
			}
		})
		if u.accTestMsg != "" {
			ui.Text(c, u.accTestMsg)
		}
	})
}
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -run TestAccounts -v`
Expected: 全部 PASS（`TestAccountsCreateFlow` 长链路含横幅断言/更新/轮换/两段删除原样；`TestAccountsRequireProvider`/`TestAccountsTestButton`/新空态）。

- [ ] **Step 5: 全包回归 + 全仓验证**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: 全绿。

- [ ] **Step 6: Commit**

```bash
git add internal/desktop/accounts.go internal/desktop/accounts_test.go
git commit -m "feat(desktop): 账号页丝印重排——降级警示横幅、等宽密钥尾列、空态引导"
```

---

### Task 7: CLAUDE.md 决策留档 + 人工观感验收

**Files:**
- Modify: `CLAUDE.md`（三处）

**Interfaces:**
- Consumes: Task 1-6 全部落地。
- Produces: 文档与代码一致（spec §1 的 CLAUDE.md 更新承诺兑现）。

- [ ] **Step 1: CLAUDE.md 三处编辑**

① 项目状态段落——定位原句：

```
相对 v2.0 目标结构（§12）的落地差异：管理面以进程内 `bridge`（`mygo.Bind` 桌面桥接、无独立管理端口）替代 `controlapi`；UI 为 MyGo 原生视图（`internal/desktop`），未建 `frontend/` 工程；migrations 内嵌在 `internal/storage`。
```

替换为：

```
相对 v2.0 目标结构（§12）的落地差异：管理面以进程内 `bridge`（`mygo.Bind` 桌面桥接、无独立管理端口）替代 `controlapi`；migrations 内嵌在 `internal/storage`。**UI 决策（2026-10-17）**：后续全部使用 MyGo 原生视图（`internal/desktop`），`frontend/` TS 工程方案**废弃**——这是对 docx §12 前端规划的明示偏离（docx 不改，以 `docs/superpowers/specs/2026-10-17-desktop-ui-mygo-native-design.md` 为凭证）；桌面 UI 设计系统（「交换机面板」）见该 spec 与 `internal/desktop/theme.go`。
```

② 目标结构行——定位原句：

```
目标结构：`cmd/{agentswitch,agentswitch-gateway}`、`internal/{app,gateway,controlapi,protocol,provider,router,account,config,credential,storage,telemetry,desktop}`、`frontend/`、`migrations/`、`tests/{fixtures,fakeprovider,integration}`。
```

替换为：

```
目标结构：`cmd/{agentswitch,agentswitch-gateway}`、`internal/{app,gateway,controlapi,protocol,provider,router,account,config,credential,storage,telemetry,desktop}`、`migrations/`、`tests/{fixtures,fakeprovider,integration}`；`frontend/` 不再建设（UI 决策 2026-10-17）。
```

③ 常用命令段落——定位原句：

```
前端（`frontend/` + `mygo.config.ts`，见文档 §12）尚未建立，npm/pnpm 命令待引入后再补充，不要臆造。MyGo 项目模板与打包命令以选定版本的官方仓库/文档为准（文档明确要求不假定未经验证的框架 API）。
```

替换为：

```
无独立前端工程（UI 决策 2026-10-17：全部 MyGo 原生视图，设计 token 在 `internal/desktop/theme.go`）。MyGo 项目模板与打包命令以选定版本的官方仓库/文档为准（文档明确要求不假定未经验证的框架 API）。
```

- [ ] **Step 2: 全仓最终验证**

Run: `go build ./... && go vet ./... && go test -race ./...`
Expected: 全绿。

- [ ] **Step 3: 人工观感验收（Review Focus 5，不在 CI）**

Run: `go tool mygo dev`，逐项确认并记录到 PR/提交说明：
- 浅色与深色系统外观各过一遍四页（macOS 系统设置切换外观）；应用运行中切换外观，记录是否实时换肤——不实时则记为已知限制（重启生效，spec §8.2）；
- 边栏四枚图标可见且随主题着色——不着色则按 `icons.go` 注释把 `currentColor` 换 `#9AA1B0` 并重跑全测；
- 运行网关后 LED 呼吸正常、停止后为弱灰静态点——呼吸异常则删 `ledDot` 的 `Loop` 块（代码内注释已给退化路径）；
- 请求表时间/状态/耗时、地址、令牌、密钥尾 4 呈等宽；表单标签固定宽弱色；
- 设置折叠面板展开回显端口/超时，保存提示正常；
- 空表引导文案在四个页面分别出现。

- [ ] **Step 4: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: CLAUDE.md 记录 UI 决策——全部 MyGo 原生、frontend/ 废弃（偏离 docx §12）"
```
