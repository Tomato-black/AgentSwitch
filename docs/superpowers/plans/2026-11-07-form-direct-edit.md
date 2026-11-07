# 设置表单缺键直接可编辑实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans（Native 内联，直接落 master）。

**Goal:** 用户实测「权限模块无法设置」——根因是缺键字段的「继承自下层 + 覆盖按钮」两步门槛：用户看不到输入控件，感知为不能设置。改为**缺键直接渲染编辑控件**（空值起步，首次变更写当前层、继承值不抄入），继承信息降为控件下方只读提示行「下层：值（来源）」；删除「覆盖」按钮与 `Overrides` 机制。

**Spec:** 本文件（用户 2026-11-07 实测指令；对需求 §6.6「继承行 + 覆盖按钮」的明示简化——bool 三态已是此形态，本批对齐其余字段）。

## Global Constraints
- 每任务 build/vet/相关包测试；收尾 `go test -race ./...`。
- 继承值不得写进当前层（保持 leaf-patch 语义）；fieldBlocked（scope/受管）不变。

---

### Task 1: 通用字段缺键直接渲染 + 继承提示行
**Files:** Modify `form.go`（stringField/enumField/intField/listField：去掉 inheritedRow 前置与 Overrides 判断；控件下方 `MergeEntries` 有值时渲染「下层：值（来源）」muted 提示；删 inheritedRow）。
- [ ] RED：TestPermissionRuleAddViaUI 去掉覆盖步骤（直接输入+添加）；TestInheritedRowsShowFieldLabels 改语义为「继承提示行 + 直接控件」。
- [ ] Commit `feat: 表单缺键直接渲染编辑控件（去覆盖按钮）`。

### Task 2: statusLine 同处理 + 删 Overrides 机制
**Files:** Modify `form.go`（statusLineGroup）；`state.go` 删 `Overrides` 字段（ensureFormState/引用清理）。
- [ ] 全量绿；Commit `refactor: 移除 Overrides 覆盖机制`。

### Task 3: 收尾
- [ ] 全量+race；CLAUDE.md 记录；重启实例。
