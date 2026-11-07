# 终审 Minor 清理批次实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 补齐「v0.8.3 组件利用批次」终审的四个 deferred minor：RowMore 死状态清理、menuIcon 失败记日志、zoomOne 漂移保护、抽屉菜单备份/还原选中落位用例。

**Architecture:** 纯清理与测试补强，零行为变化（除 menuIcon 失败分支新增日志输出）。

**Spec:** 上一批评审报告（2026-11-07，ledger 已删，结论摘录于本文件各任务）；用户确认「四个 minor 做成一个小批」。

## Global Constraints

- 每任务收尾 build/vet/相关包测试；批次收尾 `go test -race ./...` 全绿。
- 直接落 master，中文提交 + Co-Authored-By。

---

### Task 1: RowMore 死状态清理
**Files:** Modify `internal/desktop/state/state.go`（删 `RowMore` 字段）、`internal/desktop/pages/access/access.go`（`deleteConfirm` 的 `RowMore==DeleteID` 恒假判断与收起、`switchTab` 的 `RowMore=""`）。
**Interfaces:** 删除字段与全部残留读写（grep `RowMore` 清零）。无行为变化——既有 13 包测试即守卫。
- [ ] Step 1: 基线 `go test ./internal/desktop/...` 现绿。
- [ ] Step 2: 清理（grep 确认无引用残留）。
- [ ] Step 3: `go build ./... && go vet ./... && go test ./internal/desktop/...` PASS。
- [ ] Step 4: Commit `refactor: 清理 RowMore 死状态（菜单化后无写入者）`。

### Task 2: menuIcon 失败记日志
**Files:** Modify `internal/desktop/desktop.go`；Test `desktop_test.go`。
**Interfaces:** `menuIcon(svg *ui.SVG, log *slog.Logger)`——失败时 `log.Error`（nil 日志静默）并返回 nil；调用点传 `a.Log()`。
- [ ] Step 1: RED：`TestMenuIconFailureLogs`——零值 `&ui.SVG{}`（探针已证实返回 "the SVG has no size"）→ 返回 nil 且 slog TextHandler 缓冲含日志。
- [ ] Step 2: Run → FAIL（签名不存在）。
- [ ] Step 3: 实现并接线。
- [ ] Step 4: Run PASS；Commit `feat: menuIcon 失败路径记日志`。

### Task 3: zoomOne 漂移保护
**Files:** Modify `internal/desktop/zoom_test.go`。
**Interfaces:** `TestZoomSteps` 增 `if one != zoomOne { t.Fatalf }`——档位表调整使 1.0 挪位时，`newUIState` 默认值与 Cmd+0 复位指向错误档的问题被测试暴露。
- [ ] Step 1: 加断言（钉住，基线应绿；若红即发现真缺陷）。
- [ ] Step 2: Run PASS；Commit `test: zoomOne 与档位表漂移保护`。

### Task 4: 抽屉菜单备份/还原选中落位用例
**Files:** Test `internal/desktop/pages/access/claude/claude_test.go`。
**Interfaces:** `TestDrawerMenuBackupAndRestoreArm`：编辑态抽屉 → Click("更多") → Click("备份与恢复") 断言 `BackupOpen==true`；重开菜单 → Click("还原最近备份") 断言 `RestoreArm==true`。
- [ ] Step 1: 写用例（钉住，基线应绿；若红即发现真缺陷）。
- [ ] Step 2: Run PASS；Commit `test: 抽屉菜单备份/还原选中落位用例`。

### Task 5: 收尾
- [ ] `go build ./... && go vet ./... && go test ./... && go test -race ./...` 全绿；CLAUDE.md 组件利用段落补一句 minor 清理记录。
- [ ] Commit `docs: CLAUDE.md 记录 minor 清理批次`。
