# 主模型/回退模型归入接入实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:executing-plans（Native 内联，直接落 master）。

**Goal:** 用户裁定「主模型和回退模型不应该在设置页设置，应该在接入里设置」——设置页「模型与响应」卡撤下 `model`/`fallbackModel` 两字段（保留推理强度/思考开关）；接入抽屉新增「主模型」「回退模型」编辑行，写入接入文档顶层键（既有投影链路自动写进 settings.json，设置页自动只读）。**这是对需求 §8.8「`model` 键由设置区管理」的明示偏离（2026-11-07 用户实测裁定）**。

**Architecture:** 接入文档顶层键 `model`(string)/`fallbackModel`(string[]) 已由 `mergeClaudeLive` 投影到 live settings.json，且 `ManagedSetForClaude` 的 `StrongTopLevel` 已覆盖它们（设置页字段级只读）。抽屉经与角色映射同款的「行状态 ↔ DocJSON 双向同步」编辑；留空＝跟随网关（ANTHROPIC_MODEL）。值应为网关可路由的逻辑模型名。

**Spec:** 本文件（用户 2026-11-07 指令 + AskUserQuestion 原则「接入只配置模型和网关」）。

## Global Constraints
- 每任务 build/vet/相关包测试；收尾 `go test -race ./...`。
- 业务管道（归一化/投影/受管集合）不改，仅测试钉住 + UI。

---

### Task 1: bridge 钉住——顶层 model/fallbackModel 投影与受管只读
**Files:** Test `internal/bridge/accessconfigs_test.go`。
- [ ] 用例：Apply 含 `model`/`fallbackModel` 的接入文档 → live settings.json 含两键原值；`ManagedSetForClaude().StrongTopLevel` 含两键（钉住，基线应绿；红即发现投影缺失）。
- [ ] Commit `test: 钉住 model/fallbackModel 顶层投影与受管集合`。

### Task 2: 设置页撤下两字段
**Files:** Modify `claudecfg/form.go`（modelGroup）；Test `form_test.go`。
- [ ] RED：渲染 modelGroup 含「推理强度/始终开启思考」、不含「主模型/回退模型」。
- [ ] modelGroup 只剩 enumField(effortLevel)+boolField(alwaysThinkingEnabled)，标题改「推理与响应」、hint「推理强度与思考开关；主模型/回退模型在「接入」抽屉设置」。
- [ ] Commit `feat: 设置页撤下主模型/回退模型（归接入）`。

### Task 3: 抽屉加「主模型」「回退模型」行
**Files:** Modify `accesskit/roles.go`（或新 `modelkey.go`：`SyncModelKeysFromDoc(f)`/`PatchModelKeys(f)` 双向同步，模式同角色映射）+ `state.go`（`AccessForm.ModelKey string`、`FallbackModels []string`）+ `claude.go`（DrawerCard 在 modelSection 后渲染两行）。
**Interfaces:** 主模型＝TextInput（placeholder＝绑定逻辑模型名「跟随网关」；留空删键）；回退模型＝有序 chips（↑/×）+「添加」Select（逻辑模型 labels）。写回 DocJSON 顶层 `model`/`fallbackModel`；变更对比/受管键预览自动涵盖。
- [ ] RED：往返用例（doc→行状态→改→doc 含新值；留空删键）+ 抽屉渲染含「主模型」「回退模型」。
- [ ] Commit `feat: 接入抽屉新增主模型/回退模型（写入接入文档顶层键）`。

### Task 4: 收尾
- [ ] 全量 + `-race`；CLAUDE.md 记录偏离（model/fallbackModel 归属设置区→接入）；重启 dev 实例。Commit `docs: CLAUDE.md 记录模型键归属调整`。
