# Claude Code 设置拆独立页 + 选中态加强实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 用户实测反馈三连——①「接入只配置模型和网关」：Claude Code 设置区（含权限表单）从接入页拆出为独立侧栏页；② 设置区全部选中态加强（Segmented/布尔三态/视图页签）；③ 顺带解决接入页拥挤。**这是对 2026-11-07「合并为唯一入口」决策（需求痛点 4）的实测后部分推翻**，记入 CLAUDE.md。

**Architecture:** `claudecfg.Section` 升格为整页 `Page`（页头 + 滚动主体），侧栏第五项分发；接入页只留摘要卡 + 抽屉，受管键行「打开接入」跨页跳转。选中态：自建 `uikit.Segmented`（SegmentedBase 封装，选中段软橙底+粗体）替换全部 `ui.Segmented` 调用；视图页签 TabsBase 自绘加强。根因：主题浅色 `Background==Surface==#FFFFFF`，框架 Segmented 选中面 = `t.Background` → 白上加白不可见。

**Tech Stack:** `ui.SegmentedBase/Segment`、`ui.TabsBase/Tab`、既有 `st.Go` 脏守卫（`claudeCfgDirty` 改按 `s.Page` 判定）。

**Spec:** 本文件（用户 2026-11-07 实测反馈 + AskUserQuestion 裁定：拆独立侧栏页 / 选中态全加强）。

## Global Constraints

- 每任务 build/vet/相关包测试；批次收尾 `go test -race ./...` 全绿。
- 直接落 master；CLAUDE.md 记录决策推翻。
- 业务逻辑（保存/校验/受管键/脏守卫语义）零变化，只动布局/导航/样式。

---

### Task 1: uikit.Segmented 强选中态封装
**Files:** Create `internal/desktop/uikit/segmented.go` + `segmented_test.go`。
**Interfaces:** `func Segmented(c *ui.Context, t *ui.Theme, s SoftPalette, selected *int, labels ...string) ui.Element`——SegmentedBase：轨道 `Background(t.SurfaceHover) Radius(t.Radius) Padding(Space(0.5))`；未选中段透明底 `TextMuted`；选中段 `Background(s.Act) TextColor(s.ActText) FontWeight(600) Radius(t.Radius-2)`。`Changed()` 语义与 ui.Segmented 一致。
- [ ] RED：测试渲染两段、改选中值后选中段文本仍全量在树、Changed 语义（直接驱动 selected 变量断言段样式构建不炸）。
- [ ] 实现并 GREEN；Commit `feat: uikit.Segmented 强选中态（软橙底+粗体）`。

### Task 2: 替换三处 Segmented 调用
**Files:** Modify `claudecfg/view.go`（层级、共享/本地）、`claudecfg/form.go`（boolField）。
- [ ] 基线绿 → 替换 → `go test ./internal/desktop/...` PASS（既有用例断言段文本不变）。
- [ ] Commit `refactor: 设置区分段控件统一换 uikit.Segmented`。

### Task 3: 视图页签加强
**Files:** Modify `claudecfg/view.go`（viewTabs）；可选 `uikit`。
**Interfaces:** TabsBase 自绘：选中 = `TextColor(t.Accent) FontWeight(600)` + 底部 3px `t.Accent` 线（DrawOver 画线）；未选中 `TextMuted`、hover 变 Text。
- [ ] RED：`TestSectionRendersClaudeSettings` 维持文本断言 + 新增选中态文本仍存在（样式不可断言，语义不变即钉住）→ 实现替换。
- [ ] Commit `feat: 设置区视图页签选中态加强`。

### Task 4: claudecfg 拆独立页
**Files:** Modify `claudecfg/view.go`（Section→Page 重构）。
**Interfaces:** `func Page(c *ui.Context, st *state.State)`——`ui.Column Fill`：`uikit.PageHeader{Title:"Claude Code 设置", Subtitle:"分层管理 Claude Code 自身设置；接入投影键只读", Meta: 受管 13 键徽章, Actions: 刷新(SoftButton gray)+保存设置(PillButton, canSave 语义不变)}` + `ui.Scroll`（fileStrip/layerSelector/…/内容卡片流，即现 Section 主体）。旧 `Section` 与其区块头删除。
- [ ] RED：新增 `TestPageRendersClaudeSettings`（Page 级：页头三元素 + 主体既有断言）；`TestSectionRenders*` 等迁移到 Page。
- [ ] 实现并 GREEN；Commit `feat: Claude Code 设置拆独立页（Page 升格）`。

### Task 5: 侧栏导航与脏守卫
**Files:** Modify `internal/desktop/shell.go`（navItems 加 `{"claude-settings","Claude Code 设置",uikit.IconConfig?——用现有最近图标 IconSettings 或新增}`）、`desktop.go`（pageView 分发：先注入 `st.ClaudeCfg.OpenAccess` 再调 `claudecfg.Page`）、`state/state.go`（`claudeCfgDirty` 改 `s.Page == "claude-settings"`；`discardDrafts` 相应保留/清 ClaudeCfg 的语义复核）。
- [ ] RED：shell 测试 nav 五项 + smoke 新页关键内容；`TestNavDirtyConfirm` 类用例覆盖新页脏守卫。
- [ ] 实现并 GREEN；Commit `feat: 侧栏新增 Claude Code 设置页`。

### Task 6: 接入页瘦身与跨页跳转
**Files:** Modify `access.go`（删 Section 挂载与 claudeOpenAccess；导出 `OpenAccessDrawer(st)`＝`st.Go("access", openAccessEditor(current))`；摘要卡文案改「权限、状态栏、Skills 在侧栏『Claude Code 设置』页」+ gray 按钮「打开设置页」`st.Go("claude-settings",nil)`；switchTab 不再重置 ClaudeCfg；tabDirty 去 ClaudeCfg 项）、`claudecfg`（受管键行按钮文案不变，回调语义改跨页）。
- [ ] RED：接入页 claude 页签不再含「用户级/项目级」等设置区文本；摘要卡含「打开设置页」；codex 页不受影响。
- [ ] 实现并 GREEN；Commit `feat: 接入页瘦身——设置区迁出，跨页跳转接线`。

### Task 7: 收尾
- [ ] 全量 + `-race`；CLAUDE.md：记录「合并决策实测后部分推翻（接入只配模型与网关；设置独立侧栏页）」+ 选中态根因与修复。
- [ ] Commit `docs: CLAUDE.md 记录设置页拆分决策`；重建并原子重启 dev 实例。
