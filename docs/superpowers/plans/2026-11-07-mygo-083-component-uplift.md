# MyGo v0.8.3 组件升级利用批次实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用 v0.7–v0.8.3 新原语优化四处现有功能：次级操作「更多」展开行 → `ui.PopoverMenuButton` 菜单（codex 行 / desktop 行 / Claude 抽屉）、两处裸搜索框 → `ui.SearchField`、全局浏览器式缩放（`c.Shortcut` + `c.SetZoom`）、托盘菜单图标（`mygo.NewMenuIconSVG` 复用 uikit SVG）。

**Architecture:** 纯 UI 层替换与接线，业务逻辑（应用/删除/备份/过滤/缩放步进值）不动。菜单项 Danger/Disabled 用框架语义；SVG 图标走 `ui.SVG` 天然满足的 `SVGImage` 接口。

**Tech Stack:** mygo v0.8.3：`ui.PopoverMenuButton(c, label, func(m *ui.Menu))`、`m.Item(x).Chosen()/.Disabled(b)/.Danger()/.Icon(MenuIcon)`、`ui.SearchField(c, &q).Placeholder(...)`、`c.Shortcut(ui.Cmd, key)` + `c.SetZoom(z)`、`mygo.NewMenuIconSVG(svg, template)`。

**Spec:** 本文件即需求（2026-11-07 用户确认「都做」；分析依据 mygo v0.7.0–v0.8.3 release notes 与 v0.8.3 源码核实）。

## Global Constraints

- 每任务收尾 `go build ./... && go vet ./... && go test <相关包>`；批次收尾 `go test -race ./...` 全绿。
- 视觉遵循 uikit token；不改业务文案与交互语义（删除仍两段式确认、网关门禁不变）。
- 直接落 master，中文提交说明 + Co-Authored-By。

---

### Task 1: codex 配置行「更多」→ 菜单按钮

**Files:** Modify `internal/desktop/pages/access/access.go`（`configRow`）；Test `access_test.go`（`TestAccessDeleteRequiresConfirm` 等）。
**Interfaces:** Produces: `configRow` 的「更多/收起」展开行替换为 `ui.PopoverMenuButton(c, "更多", func(m *ui.Menu))`，菜单项：复制启动命令（`Disabled(!gateOK)`）、打开终端（同）、删除（`.Danger()`，武装既有 `deleteConfirm`）。`RowMore` 展开态逻辑随行删除（codex 行不再用）。
- [ ] Step 1: 更新 `TestAccessDeleteRequiresConfirm`：Click("更多") 打开菜单 → Click("删除") 武装 → 弹窗「确认删除」流程不变（先跑 RED：旧「更多」展开逻辑下新交互断言失败）。
- [ ] Step 2: Run `go test ./internal/desktop/pages/access -run TestAccessDeleteRequiresConfirm -v` → FAIL。
- [ ] Step 3: 实现菜单替换；删除 RowMore 分支。
- [ ] Step 4: Run `go test ./internal/desktop/...` → PASS（修掉引用「更多」展开的其他断言）。
- [ ] Step 5: Commit `feat: codex 配置行「更多」改 PopoverMenuButton 菜单`。

### Task 2: Claude Desktop 配置行「更多」→ 菜单按钮

**Files:** Modify `internal/desktop/pages/access/claudedesktop/desktop.go`（`ConfigRow`）；Test 该包测试。
**Interfaces:** 同 Task 1；菜单仅一项：删除（Danger）。
- [ ] Step 1: 更新/新增用例：Click("更多") → Click("删除") 武装 `DeleteArm`（先 RED）。
- [ ] Step 2: Run `go test ./internal/desktop/pages/access/claudedesktop -run TestConfigRow -v` → FAIL。
- [ ] Step 3: 实现替换。
- [ ] Step 4: Run `go test ./internal/desktop/...` → PASS。
- [ ] Step 5: Commit `feat: Claude Desktop 配置行「更多」改菜单按钮`。

### Task 3: Claude 抽屉次级操作收进「更多」菜单

**Files:** Modify `internal/desktop/pages/access/claude/claude.go`（`DrawerCard` 次级操作行）；Test `claude_test.go`。
**Interfaces:** 次级操作五枚平铺按钮（复制启动命令/打开终端/备份与恢复/还原最近备份/删除）→ 页脚行首 `ui.PopoverMenuButton(c, "更多", …)`（复制启动命令/打开终端 `Disabled(!gateOK)`；删除 `.Danger()`）；页脚只剩 更多 | 弹性 | 关闭 | 仅保存 | 保存并应用。
- [ ] Step 1: 更新 `TestDrawerCardConfigManagementRenders`：Click("更多") 后断言五个菜单项文本与页脚四元素（先 RED）。
- [ ] Step 2: Run `go test ./internal/desktop/pages/access/claude -run TestDrawer -v` → FAIL。
- [ ] Step 3: 实现替换。
- [ ] Step 4: Run `go test ./internal/desktop/...` → PASS。
- [ ] Step 5: Commit `feat: 接入抽屉次级操作收进「更多」菜单`。

### Task 4: 两处搜索框 → ui.SearchField

**Files:** Modify `internal/desktop/pages/access/access.go`（codex 过滤框）、`internal/desktop/pages/access/claude/claudecfg/mergeview.go`（键路径筛选）；Test 两包。
**Interfaces:** `ui.TextInput(...).Placeholder(x)` → `ui.SearchField(c, &q).Placeholder(x)`（同文案同绑定；获得放大镜/一键清除/Esc 清空/读屏 search 角色）。
- [ ] Step 1: 新增交互用例：codex 页签 `st.Access.Filter="不存在"` 渲染含「没有匹配的配置」；合并预览 `MergeQuery="model"` 只剩含 model 键行（先 RED：现 TextInput 下应已通过者改为断言 SearchField 行为——若基线即绿则记录为钉住测试）。
- [ ] Step 2: Run 两包 -run TestSearch → 观察记录。
- [ ] Step 3: 实现替换。
- [ ] Step 4: Run `go test ./internal/desktop/...` → PASS。
- [ ] Step 5: Commit `feat: 搜索框换 ui.SearchField（放大镜/清除/读屏角色）`。

### Task 5: 全局浏览器式缩放（Cmd/Ctrl ±/0）

**Files:** Create `internal/desktop/zoom.go`（步进表与夹取）+ `zoom_test.go`；Modify 根视图（`shell.go` 的 view）与 `uiState`（`Zoom int`）。
**Interfaces:** Produces: `zoomSteps []float32{0.5,…,3}`（同 gallery 浏览器档位）、`zoomAt(i int) float32`（越界夹取）、`zoomStep(i, delta int) int`（夹取步进）。根视图每帧：`Cmd/Ctrl+Equal(+Shift)` 步进放大、`Cmd/Ctrl+Minus` 缩小、`Cmd/Ctrl+0` 复位（`c.Shortcut`），随后 `c.SetZoom(zoomAt(u.Zoom))`。
- [ ] Step 1: 写 `zoom_test.go`：档位表含 1.0、两端夹取、步进/复位语义（RED：函数未定义）。
- [ ] Step 2: Run `go test ./internal/desktop -run TestZoom -v` → FAIL。
- [ ] Step 3: 实现 `zoom.go` + 根视图接线 + `uiState.Zoom`。
- [ ] Step 4: 新增接线用例：`tt.Key(ui.Cmd, ui.KeyEqual)` 后 `u.Zoom==1`；`Key(ui.Cmd, ui.Key0)` 复位（Tester 事件到 Shortcut 通道）。Run → PASS。
- [ ] Step 5: Run `go test ./internal/desktop/...` → PASS；Commit `feat: 全局浏览器式缩放（Cmd/Ctrl ±/0）`。

### Task 6: 托盘菜单图标

**Files:** Modify `internal/desktop/desktop.go`（托盘 `mygo.NewMenu`）；Test `internal/desktop` 包。
**Interfaces:** 「打开主窗口」项加 `Icon: menuIcon(uikit.IconAccess)`——包内 helper `menuIcon(svg *ui.SVG) *mygo.MenuIcon`（`mygo.NewMenuIconSVG(svg, true)`，错误时 nil 降级纯文字并记日志）。
- [ ] Step 1: 写用例：`menuIcon(uikit.IconAccess)` 返回非 nil（RED）。
- [ ] Step 2: Run `go test ./internal/desktop -run TestMenuIcon -v` → FAIL。
- [ ] Step 3: 实现 helper 与菜单项接线。
- [ ] Step 4: Run `go test ./internal/desktop` → PASS。
- [ ] Step 5: Commit `feat: 托盘菜单项加图标（复用 uikit SVG）`。

### Task 7: 文档收尾与全量验证

- [ ] Step 1: `go build ./... && go vet ./... && go test ./... && go test -race ./...` 全绿。
- [ ] Step 2: CLAUDE.md 项目状态段追加「v0.8.3 组件利用（2026-11-07）」记录（菜单化/SearchField/缩放/托盘图标 + mygo-automation 工具备注）。
- [ ] Step 3: Commit `docs: CLAUDE.md 记录 mygo v0.8.3 组件利用批次`。

## 批次划分与回归门

| 批次 | 任务 | 收尾 |
|---|---|---|
| A 菜单化 | 1–3 | `go test ./internal/desktop/...` + `-race` |
| B 搜索/缩放/托盘 | 4–6 | 同上 |
| C 收尾 | 7 | 全量 + `-race` |
