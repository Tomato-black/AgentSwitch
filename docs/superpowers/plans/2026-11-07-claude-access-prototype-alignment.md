# 接入页 Claude 对齐原型实施计划（全局配置下线 + 接入抽屉 + 设置区重排）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 接入页 Claude 页签对齐原型 `docs/prototypes/claude-code-config-page.html`：删除 Claude 全局配置（UI + 合成层）、编辑接入改右侧抽屉（主页去掉实际配置列表卡）、设置区重排为「区块标题 + 文件条 + Segmented 层级 + 七卡卡片流」、页头改「Agent 接入」并把「保存设置」升为页头主操作、摘要卡补 meta 行/1M 徽章/网关停止态、表单控件升级（三态分段 + chip 列表）。

**Architecture:** 纯 UI/bridge 层重构，业务规则零变化。bridge 侧只动 `GlobalConfig`（claude 返回空）；UI 侧：`pages/access` 宿主做 app 分流（claude=摘要卡+抽屉+设置区，codex/desktop 维持现状），`pages/access/claude` 承载抽屉内容，`claudecfg` 承载设置区新结构；新增 `uikit.Drawer`（`ui.Overlay` + `ButtonBase` 遮罩 + `OverlayShortcut(Esc)`）。Codex / Claude Desktop / 连接指南页签不受影响。

**Tech Stack:** Go 1.27 + mygo v0.6.2（`ui.Overlay`、`ui.Segmented`、`Element.OverlayShortcut`、`c.Size()`）、uikit 既有 token/组件（`Card`/`SoftBadge`/`LedDot`/`RichDialog`）。

**Spec:** `docs/requirements/2026-11-07-claude-code-config-page.md`（需求）+ `docs/prototypes/claude-code-config-page.html`（视觉/交互基准）。**用户已确认范围：全面对齐（2026-11-07）。**

## 对需求文档的明示偏离（本期裁定）

1. **全局配置**：需求 §6.2.2 保留「抽屉内全局配置只读折叠区」；本次用户决策（2026-11-07）**Claude 全局配置彻底下线**——UI 卡删除、bridge 合成层对 claude 恒为空。Codex 不变。
2. **最后应用时间**：摘要卡不显示（bridge/存储无 LastApplied 数据源，本期不加表字段），记 P1。
3. **JSON 行号**：维持无行号（MyGo TextArea 限制，需求遗留项不变）。
4. **插件行开关**：维持 checkbox（原型的逐插件三态分段记 P1）。
5. **抽屉内不编辑接入文档 JSON**：按原型，抽屉只有 名称/默认模型+1M/角色映射/受管键只读预览；env 定制交给设置区。Codex 编辑器保留完整 JSON 文本域。

## Global Constraints

- MyGo 原生视图 + uikit token；不新增前端工程。
- 每任务收尾：`go build ./... && go vet ./... && go test <相关包>`；每批次收尾另跑 `go test -race ./...`。
- 令牌/密钥/Authorization 永不入日志、不入错误文案（bridge 不变量）。
- 子包不得反向 import 宿主：`pages/access/claude`、`claudecfg` 不得 import `pages/access`。
- Codex / Claude Desktop / 连接指南页签行为零变化（每批次跑既有接入测试守卫）。
- 提交直接落 master，中文提交说明，末尾 `Co-Authored-By: Claude Code <noreply@anthropic.com>`。

## Review Focus

1. **Codex 页签回归**：全局配置卡、实际配置列表卡、子页编辑器在 codex/desktop 页签必须原样（Task 2/7/9 改分流条件后跑 `go test ./internal/desktop/... ./internal/bridge/...`）。
2. **草稿保留语义**：抽屉关闭/遮罩/Esc 后 Claude 接入草稿不丢，摘要卡显示「有未保存的接入更改」，切页签弹确认（Task 7 `Draft` 字段 + `Dirty()` 语义）。
3. **保存阻断不丢**：保存按钮迁到页头后，scope 违规/受管键改动/JSON 非法的禁用原因与提示仍在设置区可见（Task 4/10）。
4. **全局配置下线不残留**：`access.global.claude` 有值时 PreviewDoc/PreviewClaudeLive/派生文件均不再合并（Task 1 bridge 测试钉死）。
5. **Overlay 可测性**：`ui.NewTester` 若看不到 `ui.Overlay` 内容，抽屉测试改为直接渲染 body 构建函数（Task 6 第一步先验证）。

---

### Task 1: bridge——Claude 全局配置合成层下线

**Files:**
- Modify: `internal/bridge/access_configs.go:204-248`（`GlobalConfig`/`SaveGlobalConfig`）
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: `GlobalConfig("claude")` 恒返回 `(map[string]string{}, nil)`；`SaveGlobalConfig("claude", …)` 返回中文错误。`composeFor`/`PreviewDoc`/`prepareDoc`/`PreviewClaudeLive` 自动跟随（~~都经 `GlobalConfig`~~——**终审订正**：`composeFor`/`PreviewClaudeLive`/`ManagedSetForClaude` 曾直读 settings 键绕行，已在修复轮统一收口到 `globalEnv`，见 bridge 回归测试 `TestGlobalConfigClaudeRetiredComposePaths`）。

- [ ] **Step 1: 写失败测试**

在 `accessconfigs_test.go` 新增 `TestGlobalConfigClaudeRetired`：写入 settings 键 `access.global.claude` 后，`GlobalConfig("claude")` 返回空 map；`PreviewDoc("claude", …)` 合成结果 env 不含该键；`SaveGlobalConfig("claude", …)` 返回错误；同键写 `access.global.codex` 后 `GlobalConfig("codex")` 正常返回。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run 'TestGlobalConfigClaudeRetired' -v`
Expected: FAIL（当前 claude global 仍参与合并）。

- [ ] **Step 3: 实现**

`GlobalConfig` 开头加：`if appType == "claude" { return map[string]string{}, nil }`（注释注明 2026-11-07 用户决策与原因）；`SaveGlobalConfig` 开头加：`if appType == "claude" { return fmt.Errorf("Claude 全局配置已下线；环境变量请在 Claude Code 设置区管理") }`。grep `access.global.claude` 修掉既有用例中对 claude 合并的断言（改为 codex 或断言为空）。

- [ ] **Step 4: 跑包内全部测试**

Run: `go test ./internal/bridge`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/access_configs.go internal/bridge/accessconfigs_test.go
git commit -m "feat: Claude 全局配置下线——合成层恒为空、保存拒绝（Codex 不变）"
```

### Task 2: UI——全局配置卡仅 Codex 渲染

**Files:**
- Modify: `internal/desktop/pages/access/access.go:44-70,97-159,289-301,572-585`（卡片条件、`reloadGlobal` 调用、页头副标题）
- Test: `internal/desktop/pages/access/access_test.go`、`internal/desktop/access_test.go`

**Interfaces:**
- Consumes: Task 1 的 bridge 行为。
- Produces: claude 页签不渲染「全局配置」卡、不加载 `st.Access.Glob`；codex 页签行为不变。

- [ ] **Step 1: 写失败测试**

`access_test.go` 新增：claude 页签渲染不含文本「全局配置」；codex 页签含「全局配置」与「保存全局配置」。

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop/pages/access -run TestClaudeTab -v`（命名以现有测试风格为准）
Expected: FAIL。

- [ ] **Step 3: 实现**

`access.go` 全局配置卡条件 `st.Access.App != "claude_desktop"` → `st.Access.App == "codex"`；`reloadGlobal` 调用点（首帧 `if st.Access.Glob == nil` 与 `switchTab`）条件同步收紧为 codex；页头副标题「为每个 Agent 管理网关接入、全局配置与实际配置」→「为每个 Agent 管理网关接入与实际配置」；包注释里「全局配置」表述同步修订。

- [ ] **Step 4: 跑桌面层测试**

Run: `go test ./internal/desktop/...`
Expected: PASS（修掉断言 claude 全局配置卡的既有用例）。

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/pages/access/access.go internal/desktop/pages/access/access_test.go internal/desktop/access_test.go
git commit -m "feat: 接入页 Claude 页签移除全局配置卡（Codex 保留）"
```

### Task 3: 侧栏导航改名「Agent 接入」

**Files:**
- Modify: `internal/desktop/shell.go:27`
- Test: `internal/desktop/shell_test.go`、`internal/desktop/smoke_test.go`

- [ ] **Step 1: 写失败测试**：shell 测试断言导航含「Agent 接入」且不含旧「接入」（注意「Agent 接入」包含子串「接入」，断言用完整标签列表比较）。
- [ ] **Step 2: Run** `go test ./internal/desktop -run TestShell -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：`navItems` label「接入」→「Agent 接入」；更新引用该标签的测试。
- [ ] **Step 4: Run** `go test ./internal/desktop` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: 侧栏导航「接入」改名「Agent 接入」"`

### Task 4: 页头「Agent 接入」+ 右侧动作区（刷新/保存设置）

**Files:**
- Modify: `internal/desktop/pages/access/claude/claudecfg/view.go:292-315`（`header` 拆出动作导出）、`internal/desktop/pages/access/access.go:69-71`
- Test: `internal/desktop/pages/access/claude/claudecfg/view_test.go`、`internal/desktop/pages/access/access_test.go`

**Interfaces:**
- Produces: `claudecfg.HeaderActions(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State)`——渲染「刷新」（走既有 `guardReload`）与「保存设置」（橙胶囊 `PillButton`，`canSave` 逻辑原样搬入：`Loaded && Layer != "managed" && Dirty && saveBlockReason()==""`；点击置 `SaveArm`）。设置区卡内 `header()` 不再渲染刷新/保存按钮。

- [ ] **Step 1: 写失败测试**：claude 页签整页渲染（`access.Page`）含页头标题「Agent 接入」；codex 页签页头无「保存设置」按钮文本。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access -run TestPageHeader -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：`access.go` `PageHeader` 标题改「Agent 接入」；claude 页签副标题「把 Claude Code 接到 AgentSwitch，并在同一页管理它的设置」，其他页签「为每个 Agent 管理网关接入与实际配置」；`Actions` 仅 claude 页签渲染时调 `claudecfg.HeaderActions`。`claudecfg.header()` 收敛为区块标题行（标题+受管徽章，见 Task 10 再改结构，本任务只把按钮迁走）。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: 页头改「Agent 接入」并把保存设置升为页头主操作"`

### Task 5: 摘要卡 meta 行对齐原型

**Files:**
- Modify: `internal/desktop/pages/access/access.go:364-433`（`claudeSummaryCard`）、`internal/desktop/accesskit/helpers.go`（新增 1M 探测 helper）
- Test: `internal/desktop/pages/access/access_test.go`

**Interfaces:**
- Produces: `accesskit.MainOneMDeclared(settingsJSON string) bool`——解析接入文档 env 的 `ANTHROPIC_MODEL`，尾部 `[1M]`（`EqualFold`）即真，解析失败为假。摘要卡第二行改为：`uikit.LedDot`（gateway 状态）+ 状态词 + 等宽地址 + 「配置 X」+「模型 Y」+（1M 时）`SoftBadge "act" "1M 上下文"`。状态徽章序：网关停止→danger「网关已停止」（此时应用按钮本就禁用）；`Form.Dirty`→warn「有未保存的接入更改」；`NeedsApply`→warn「待应用」；无配置→gray「尚未应用」；否则 ok「已应用」。

- [ ] **Step 1: 写失败测试**：摘要卡渲染含 LedDot 后的状态文本、mono 地址、「1M 上下文」徽章（settings JSON 造 `[1M]`）；网关停止时含「网关已停止」。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access -run TestSummaryCard -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：按 Interfaces 重排 `claudeSummaryCard`（说明文案与操作按钮保留现状；`Form.Dirty` 依赖 Task 7 前的现有 `Dirty()`，本任务先用 `Form.Open` 判定，Task 7 引入 `Draft` 后自动增强——实现时直接写 `st.Access.Form.Dirty("claude")`，编译期已可用）。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: Claude 接入摘要卡补 LED/地址/1M 徽章与网关停止态"`

### Task 6: uikit.Drawer 右侧抽屉组件

**Files:**
- Create: `internal/desktop/uikit/drawer.go`
- Test: `internal/desktop/uikit/widgets_test.go`（或新建 `drawer_test.go`）

**Interfaces:**
- Produces: `func Drawer(c *ui.Context, t *ui.Theme, open *bool, title, subtitle string, body func())`。行为：关闭态零渲染；打开时 `ui.Overlay` 内先画遮罩（`ui.ButtonBase` 铺满 `Absolute().Left(0).Top(0).Right(0).Bottom(0)`、`Background(ui.RGBA(24,24,27,0.28))`，`Clicked()` 置 `*open=false`），再画面板（`ui.Box` `Absolute().Top(0).Right(0).Bottom(0)`，宽 `min(560, 窗宽-48)`（`c.Size()`），`Surface` 底 + 1px Border）；面板内：标题行（title Bold + subtitle muted + 右侧「关闭」`SoftButton gray`）+ `ui.Scroll` 包 body；`panel.OverlayShortcut(0, ui.KeyEscape)` 置 `*open=false`。关闭只翻 `*open`，不触碰调用方状态（草稿保留由调用方语义承载）。

- [ ] **Step 1: 写失败测试**

```go
func TestDrawerOpenRenders(t *testing.T) {
    open := true
    tt := ui.NewTester(func(c *ui.Context) {
        Drawer(c, c.Theme(), &open, "接入配置", "写入受管键", func() { ui.Text(c, "抽屉正文") })
    }, 1000, 1600)
    if !tt.HasText("接入配置") || !tt.HasText("抽屉正文") { t.Fatalf("drawer body missing: %v", tt.Texts()) }
}
```

同时验证 Review Focus #5：若 `HasText` 看不到 Overlay 内容，则测试改为直接调用 body 与标题行构建函数，并在提交说明记录该限制。

- [ ] **Step 2: Run** `go test ./internal/desktop/uikit -run TestDrawer -v` **Expected: FAIL（未定义 Drawer）**
- [ ] **Step 3: 实现**：按 Interfaces 写 `drawer.go`（含包注释：右侧抽屉、Esc/遮罩关闭、内容滚动）。
- [ ] **Step 4: Run** `go test ./internal/desktop/uikit` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: uikit 新增右侧抽屉 Drawer（Overlay+遮罩+Esc）"`

### Task 7: Claude 编辑器从子页迁到抽屉（形态切换）

**Files:**
- Modify: `internal/desktop/state/state.go:173-201,233-236`（`AccessForm.Draft` + `Dirty()`）、`internal/desktop/pages/access/access.go:57-64,264-289`（editorPage 分流 + 抽屉挂载）、`internal/desktop/pages/access/claude/claude.go`（`EditorCard` 改造为抽屉 body）
- Test: `internal/desktop/pages/access/access_test.go`、`internal/desktop/pages/access/claude/claude_test.go`

**Interfaces:**
- Produces:
  - `AccessForm.Draft bool`——抽屉关闭但草稿仍在时为真；`Dirty(app)` 改为 `(f.Open || f.Draft) && f.Signature(app) != f.Baseline`。
  - `claude.DrawerCard(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, gateOK bool)`——抽屉正文：名称行（`FormRow`）+ 默认模型+1M（`modelSection`）+ 角色映射（`rolesSection`）+「将写入 ~/.claude/settings.json（只读）」+ `ClaudePreview` 只读 TextArea + 变更对比 + Msg + 底部操作（`PillButton`「保存并应用」/ `SoftButton`「仅保存」/ `SoftButton`「关闭」，复用 `accesskit.saveFormWithApply`；关闭＝`Open=false` 且 `Draft = Signature != Baseline`）。**不渲染**配置 JSON 编辑 TextArea 与 `docErr` 分支（无手编入口后文档恒合法）。
  - `access.go`：editorPage 分支条件改为 `st.Access.App != "claude" && …`；claude 页在主内容渲染后挂载：

```go
open := st.Access.Form.Open
uikit.Drawer(c, t, &open, "接入配置", "写入受管键到 ~/.claude/settings.json（写前备份）", func() {
    claude.DrawerCard(c, t, s, st, acc, gateOK)
})
st.Access.Form.Open = open
```

- [ ] **Step 1: 写失败测试**：`Form.Open=true` 且 claude 页签时，`access.Page` 仍渲染摘要卡文本（主页面不被编辑子页替换）且含抽屉标题「接入配置」；关闭语义测试——构造 `Form{Open:true, Draft:false, Name:"x"}`、`Baseline` 不含 x，经 DrawerCard 关闭按钮路径后 `Open==false && Draft==true`（若关闭按钮不可在 Tester 中点击，则把关闭回调提为导出小函数 `claude.CloseDrawer(st)` 直接断言）。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access/... -run TestDrawer -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：按 Interfaces 改三处；`editorPage` 与 `CloseEditor` 保留（codex/desktop 用）；`access.go` 包注释同步。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**（更新断言「编辑配置」子页标题的既有 claude 用例）
- [ ] **Step 5: Commit** `git commit -m "feat: Claude 接入编辑器改为右侧抽屉（草稿保留语义）"`

### Task 8: 抽屉内配置选择 / 新建 / 删除 / 次级操作

**Files:**
- Modify: `internal/desktop/pages/access/access.go`（挂载点传 hooks）、`internal/desktop/pages/access/claude/claude.go`
- Test: `internal/desktop/pages/access/claude/claude_test.go`、`internal/desktop/pages/access/access_test.go`

**Interfaces:**
- Produces: `claude.DrawerCard` 顶部新增两行——
  1. 配置行：`ui.Select`（选项＝`acc.List("claude")` 的名称，当前值＝编辑中配置名或当前应用配置名）+「新建」`SoftButton gray`。Select `Changed()` 且与当前不同：若 `Form.Dirty` 先武装确认框（新增 `st.Access.DrawerSwitchArm bool` + `DrawerSwitchID string`，`uikit.ConfirmDialog`「放弃未保存的接入修改？」取消默认），确认后 `st.Access.Form = state.AccessForm{Open:true, EditID: id}` + `preloadForm`；「新建」同理置空 `EditID`。
  2. 次级操作行（`EditID` 非空时）：「复制启动命令」（`acc.LaunchCommand`→剪贴板+Toast）、「打开终端」（`acc.OpenTerminal`）、「备份与恢复」（`st.Access.BackupOpen=true`）、「还原最近备份」（`st.Access.RestoreArm=true`），均 `gateOK` 守卫前两项；「删除」`SoftButton danger` 武装既有 `deleteConfirm`（`DeleteID=EditID`）。
- 挂载点：`access.go` 中 claude 的 `preloadForm`/`deleteConfirm` 已在宿主包内，Select 切换与新建通过闭包注入 `DrawerCard`（`DrawerCard` 新增 `hooks claude.DrawerHooks{SwitchTo func(id string); NewDraft func()}`，宿主实现；保持 claude 子包不 import 宿主）。

- [ ] **Step 1: 写失败测试**：抽屉含「新建」「备份与恢复」「还原最近备份」「删除」文本；hooks 注入后 `SwitchTo("id2")` 把 `Form.EditID` 换为 id2 且名称回填。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access/... -run TestDrawer -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：按 Interfaces；确认框文案与既有「未保存的编辑」风格一致。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: 接入抽屉支持配置切换/新建/删除与备份等次级操作"`

### Task 9: 主页移除 Claude 实际配置列表卡 + 受管键行「打开接入」

**Files:**
- Modify: `internal/desktop/pages/access/access.go:161-247,249-251`（列表卡条件收窄）、`internal/desktop/pages/access/claude/claudecfg/form.go:144-166`（`managedEnvRow` 加按钮）、`internal/desktop/state/state.go`（`ClaudeCfgState.OpenAccess func()`）
- Test: `internal/desktop/pages/access/access_test.go`、`internal/desktop/pages/access/claude/claudecfg/form_test.go`

**Interfaces:**
- Produces: 实际配置列表卡仅 `codex`/`claude_desktop` 渲染；claude 页签结构＝摘要卡 → 设置区。`ClaudeCfgState.OpenAccess func()`——`access.go` 每帧挂载设置区前注入（打开当前配置抽屉：`Form = AccessForm{Open:true, EditID: 当前配置 ID}` + `preloadForm`）；`managedEnvRow` 在「由 AgentSwitch 接入管理」旁渲染 `SoftButton gray "打开接入"`，`OpenAccess != nil` 时点击调用。

- [ ] **Step 1: 写失败测试**：claude 页签不含「实际配置」卡标题与「＋ 新建配置」；设置区表单含「打开接入」按钮文本；codex 页签仍含「实际配置」。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access/... -run TestClaudeTab -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：按 Interfaces；`backupPage` 子页保留（抽屉按钮已覆盖入口）。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: Claude 页签移除实际配置列表卡，受管键行提供「打开接入」"`

### Task 10: 设置区区块标题出卡 + 当前文件条

**Files:**
- Modify: `internal/desktop/pages/access/claude/claudecfg/view.go:20-65,292-315,372-388`
- Test: `internal/desktop/pages/access/claude/claudecfg/view_test.go`

**Interfaces:**
- Produces: `Section` 不再整体包一张 `uikit.Card`；改为：
  1. 区块头（卡外）：`Text "Claude Code 设置" Bold FontSize+1` + 说明「分层管理 Claude Code 自身设置；接入投影键只读」+ `SoftBadge gray "受管 13 键"`；
  2. 文件条：`ui.Box Radius(10) Background(t.SurfaceHover) Padding(8,10)` 内 mono 路径 + spacer + 状态徽章（`Dirty`→warn「有未保存修改」；`BrokenFile`→danger「JSON 错误」；`Layer=="managed"`→gray「只读」；`!Exists`→gray「文件不存在」；否则 ok「已保存」）；
  3. 其余（警告条/结果条/备份面板/视图切换/内容）按原顺序平铺，后续任务逐个成卡。
  `header()`（旧标题行）删除——职责已被页头（Task 4）与本任务区块头取代；`saveBlockReason` 提示挪到文件条下方 Feedback（逻辑不变）。

- [ ] **Step 1: 写失败测试**：断言「已保存」「有未保存修改」徽章随 `Dirty`/`Exists` 切换；既有 `TestSectionRendersClaudeSettings` 文本断言保持通过。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access/claude/claudecfg -run TestSection -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：按 Interfaces。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: 设置区区块标题出卡并新增当前文件状态条"`

### Task 11: 层级 Segmented + 项目上下文卡 + 托管来源列表

**Files:**
- Modify: `internal/desktop/pages/access/claude/claudecfg/view.go:317-370`（`layerSelector`）
- Test: `internal/desktop/pages/access/claude/claudecfg/view_test.go`

**Interfaces:**
- Produces:
  - 层级切换：`ui.Tabs` → `ui.Segmented(c, &idx, "用户级", "项目级", "企业托管")`（`Changed()` 语义同现状，仍走 `switchLayer` 脏守卫）。
  - 项目级上下文卡：`uikit.Card` 包项目 `ui.Select` +「选择目录…」手动输入行（TextInput+打开按钮，保留现状）+ 共享/本地 `ui.Segmented`（「共享 settings.json」「本地 settings.local.json」）+ path-line（`ui.Box Radius(8) Background(t.SurfaceHover)` mono 实际路径 `cf.File`）+ 既有说明文案。
  - 企业托管：上下文卡改为托管来源列表——遍历 `cf.Managed.Source`（mono 来源路径 + 说明）+「托管配置只读，本页不写入」；`cf.Violations` 展示保留。

- [ ] **Step 1: 写失败测试**：项目层渲染含共享/本地分段文本与 mono 路径条；托管层渲染含 `cf.Managed.Source` 中的来源路径文本。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access/claude/claudecfg -run TestLayer -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：按 Interfaces。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: 设置区层级改 Segmented 并按原型重排项目/托管上下文卡"`

### Task 12: 表单七组拆独立卡片（含 JSON/合并预览包卡）

**Files:**
- Modify: `internal/desktop/pages/access/claude/claudecfg/form.go:23-40,74-81`、`skills.go:22-30`、`view.go:44-56`
- Test: `internal/desktop/pages/access/claude/claudecfg/form_test.go`、`view_test.go`

**Interfaces:**
- Produces: `formText` 替换为 `groupCard(c, t, title, hint string, body func())`——`uikit.Card` + `uikit.SectionTitle(title, hint)` + body；`FormSection` 七组各一卡；`SkillsCard` 自包卡（去内部 `formText`）；JSON/合并预览视图各包一张卡（标题「JSON 编辑」/「合并预览（只读）」）。分组内容与行为零变化。

- [ ] **Step 1: 跑基线确认现绿**（纯重构任务，既有断言即验收）

Run: `go test ./internal/desktop/pages/access/claude/claudecfg`
Expected: PASS。

- [ ] **Step 2: 实现**：按 Interfaces 把 `formText` 换成 `groupCard`，七组与两个视图各自包卡。
- [ ] **Step 3: Run** `go test ./internal/desktop/...` **Expected: PASS**（文本断言全部不変——本任务只动容器层级）
- [ ] **Step 5: Commit** `git commit -m "refactor: 设置区表单七组拆为独立卡片流"`

### Task 13: bool 字段改三态 Segmented

**Files:**
- Modify: `internal/desktop/pages/access/claude/claudecfg/form.go:584-610`（`boolField`）
- Test: `internal/desktop/pages/access/claude/claudecfg/form_test.go`

**Interfaces:**
- Produces: `boolField` 渲染 `ui.Segmented` 三段「继承 / 开启 / 关闭」：当前层缺键＝继承；选开启/关闭写 `corecfg.Set`；选继承 `corecfg.Delete`。缺键时不再走 `inheritedRow`+「覆盖」（三态本身表达继承，继承值以 hint 行展示：有 `MergeEntries` 时显示「下层：<值>（<来源>）」）。`fieldBlocked` 分支不变。

- [ ] **Step 1: 写失败测试**：缺键文档渲染三段且默认「继承」；选「关闭」后 `Doc[path]==false` 且 `Dirty`；切回「继承」删除键。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access/claude/claudecfg -run TestBool -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：按 Interfaces。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: 设置区布尔字段改三态分段（继承/开启/关闭）"`

### Task 14: 列表字段改 chip 编辑器

**Files:**
- Modify: `internal/desktop/pages/access/claude/claudecfg/form.go:647-669`（`listField`）、`internal/desktop/state/state.go`（`ClaudeCfgState.ChipInputs map[string]string`）
- Test: `internal/desktop/pages/access/claude/claudecfg/form_test.go`

**Interfaces:**
- Produces: `listField` 渲染：chip 流（`ui.Row().Wrap()`；每 chip＝`ui.Box Border(1,t.Border) Radius(8)` 内 mono 文本 + 「×」`SoftButton danger`）+ 空时「未设置」muted；添加行＝`TextInput`（绑定 `cf.ChipInputs[path]`）+「添加」`SoftButton gray`（trim 非空追加、同层重复 warn 不阻断追加但提示）；`fallbackModel` 额外每 chip 带「↑」（与前一元素交换）。写回经新 helper `setChipList(cf, path, items []string)`（空列表删除键；复用 `RecomputeDirty`）。`fieldBlocked` 分支不变；缺键+未覆盖时仍先 `inheritedRow`。

- [ ] **Step 1: 写失败测试**：`permissions.allow` 含 2 项的文档渲染 2 个 chip 文本；添加行输入+追加后 `Doc` 数组长 3 且脏；×后回 2；fallbackModel ↑交换顺序断言。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access/claude/claudecfg -run TestChip -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：按 Interfaces。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: 设置区列表字段改 chip 编辑器（含 fallbackModel 排序）"`

### Task 15: 环境变量卡列头与布局微调

**Files:**
- Modify: `internal/desktop/pages/access/claude/claudecfg/form.go:106-142`（`envGroup`）
- Test: `internal/desktop/pages/access/claude/claudecfg/form_test.go`

**Interfaces:**
- Produces: `envGroup` 卡内首行渲染列头（mono 小字「变量」「值」「来源 / 操作」，宽度与行对齐：320/弹性/固定），其余行为零变化（Task 12 已包卡）。

- [ ] **Step 1: 写失败测试**：断言列头文本出现。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access/claude/claudecfg -run TestEnv -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：按 Interfaces。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: 环境变量卡补列头对齐原型表格布局"`

### Task 16: 合并预览权限求值三段卡

**Files:**
- Modify: `internal/desktop/pages/access/claude/claudecfg/mergeview.go:177-231`（`permissionCard`）
- Test: `internal/desktop/pages/access/claude/claudecfg/mergeview_test.go`

**Interfaces:**
- Produces: `permissionCard` 改三段式：`ui.Row` 三个等宽 `ui.Box Border(1,t.Border) Radius(9)`——「1 拒绝 N 条」（danger 软底头部）→「2 询问 N 条」（warn）→「3 允许 N 条」（ok），段间 Text「→」；下方固定说明「deny 任意层压过 allow 任意层；首个匹配决定」。`defaultMode` 不生效警告与 OR 提示保留。

- [ ] **Step 1: 写失败测试**：断言「1 拒绝」「2 询问」「3 允许」与各段计数文本。
- [ ] **Step 2: Run** `go test ./internal/desktop/pages/access/claude/claudecfg -run TestMerge -v` **Expected: FAIL**
- [ ] **Step 3: 实现**：按 Interfaces。
- [ ] **Step 4: Run** `go test ./internal/desktop/...` **Expected: PASS**
- [ ] **Step 5: Commit** `git commit -m "feat: 合并预览权限求值改三段式卡片"`

### Task 17: 文档收尾与全量验证

**Files:**
- Modify: `CLAUDE.md`（项目状态段追加 2026-11-07 对齐记录：全局配置下线决策与 spec 偏离、接入抽屉、设置区结构、页头/导航改名、P1 遗留清单）

- [ ] **Step 1: 全量验证**

Run: `go build ./... && go vet ./... && go test ./... && go test -race ./...`
Expected: 全部 PASS。

- [ ] **Step 2: 更新 CLAUDE.md**（含 P1 遗留：最后应用时间数据源、插件行三态、项目选择器搜索/最近排序、抽屉窄窗动画、`access.global.claude` 残留键清理提示）。

- [ ] **Step 3: Commit** `git commit -m "docs: CLAUDE.md 记录 Claude 接入页原型对齐（v0.2.0 UI 批次）"`

---

## 批次划分与回归门

| 批次 | 任务 | 收尾命令 |
|---|---|---|
| 1 全局配置下线 | 1–2 | `go build ./... && go vet ./... && go test ./... && go test -race ./internal/bridge ./internal/desktop/...` |
| 2 页头/导航/摘要卡 | 3–5 | 同上 |
| 3 接入抽屉 | 6–9 | 同上 |
| 4 设置区结构 | 10–12 | 同上 |
| 5 表单控件与打磨 | 13–16 | 同上 |
| 6 收尾 | 17 | 全量 + `-race` |

每批完成在 master 直接提交（用户惯例），批次间可独立回滚（git revert 单批次提交区间）。
