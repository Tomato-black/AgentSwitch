# 接入页模型配置全下拉 + 显示名称自动填充实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans（Native 内联，直接落 master）。

**Goal:** 用户指令「接入页面模型相关配置都做成下拉选，显示名称根据下拉选的模型名称填充，支持手动修改」——①「Claude 模型选择」的主模型 TextInput 改下拉（含「（跟随网关）」哨兵→删 model 键）；②角色映射行选模型后，显示名称自动填充为所选模型名（用户手动改过则不覆盖，保持 TextInput 可改）；③默认模型/角色/回退链已是下拉，不动。

**Spec:** 本文件（用户 2026-11-07 指令）。

## Global Constraints
- 每任务 build/vet/相关包测试；收尾 `go test -race ./...`。
- 写回文档路径（PatchModelKey/PatchRole）不变。

---

### Task 1: 主模型改下拉
**Files:** Modify `claude.go` modelKeysSection。
- [ ] RED：EditorCard 渲染含「（跟随网关）」选项文本；选择映射写回（Select 值↔ModelKey 空/值）。
- [ ] Commit `feat: 接入主模型改下拉（跟随网关哨兵）`。

### Task 2: 角色显示名称自动填充
**Files:** Modify `claude.go` rolesSection（模型下拉 Changed 时：显示名为空或等于任一逻辑模型名——自动填充痕迹——则覆盖为新模型名；「跟随主模型」则清空显示名）。
- [ ] RED：选模型 → RoleNames[i]==模型名 且文档 NAME 键写入；手动改名后再换模型不覆盖；跟随主模型清空。
- [ ] Commit `feat: 角色显示名称随模型下拉自动填充（手动修改优先）`。

### Task 3: 收尾
- [ ] 全量+race；CLAUDE.md 记录；重启实例。
