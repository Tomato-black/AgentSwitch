# 接入编辑改独立子页 + 恢复 Claude 列表实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans（Native 内联，直接落 master）。

**Goal:** 用户实测裁定——Claude 设置已拆独立页，接入编辑**不再用抽屉**，改**独立编辑子页**（与 codex/desktop 一致）；**「更多」菜单在列表行上**，不在详情里。即：Claude 页签 = 摘要卡 + 配置列表卡（行含 应用/编辑/更多菜单），编辑走 editorPage 子页。**这是对原型抽屉方案（2026-11-07 §6.2.2）的明示推翻**——原抽屉动机（同页容纳设置区）已随设置拆页消失。

**Architecture:** 复用既有 editorPage 分发与 codex 列表行模式；claude.DrawerCard → claude.EditorCard（页内容，去抽屉页脚菜单与 hooks）；删抽屉机制（uikit.Drawer、AccessForm.Draft、DrawerSwitchArm/ID、drawerSelect/ConfirmDrawerSwitch、openAccessEditor 复杂守卫）；「打开接入」退化为纯导航。

**Spec:** 本文件（用户 2026-11-07 实测指令）。

## Global Constraints
- 每任务 build/vet/相关包测试；收尾 `go test -race ./...` 全绿。
- 编辑内容（模型/角色/模型选择/预览/diff/保存）功能不变；删除仍两段式；网关门禁不变。

---

### Task 1: claude 编辑器改子页（DrawerCard→EditorCard）
**Files:** Modify `claude.go`（改名为 EditorCard：去 DrawerHooks/drawerSelect/「更多」菜单；页脚=网关提示+取消/仅保存/保存并应用）、`access.go`（editorPage 加回 claude 分支；删抽屉挂载块）。
- [ ] RED：`Form.Open` claude 渲染 editorPage（含「← 返回接入」、无抽屉标题）。
- [ ] Commit `feat: Claude 接入编辑器改独立子页`。

### Task 2: 恢复 Claude 配置列表卡（「更多」在行上）
**Files:** Modify `access.go`（claude 页签渲染列表卡——复用 configRow：应用/编辑/更多菜单[复制启动命令/打开终端/删除]；卡头=备份与恢复/还原最近备份/新建）；摘要卡「编辑接入」开子页。
- [ ] RED：claude 页含「实际配置」卡与行级「更多」菜单（点开含复制启动命令/删除）。
- [ ] Commit `feat: 恢复 Claude 配置列表卡（更多菜单在行上）`。

### Task 3: 删抽屉机制
**Files:** Delete `uikit/drawer.go`+测试；`state.go` 删 Draft/DrawerSwitchArm/DrawerSwitchID（Dirty 回归 Open&&sig!=baseline）；`access.go` 删 drawerSwitchConfirm/claudeDrawerHooks/openAccessEditor→简化；`OpenAccessDrawer`→纯导航 `st.Go("access", nil)`；shell.go 注入简化。
- [ ] RED：Dirty 新语义用例；全仓无 drawer 残留。
- [ ] Commit `refactor: 移除抽屉机制（Drawer/Draft/切换确认）`。

### Task 4: 收尾
- [ ] 全量+race；CLAUDE.md 记录（抽屉方案推翻、列表+子页回归）；重启实例。
