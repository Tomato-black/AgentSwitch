# Claude Code 配置中心实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在「Agent 接入」页的 Claude Code 页签内落地分层 settings 查看/编辑、合并预览、安全写入、危险模式确认与 Skills 开关。

**Architecture:** 新增纯逻辑包 `internal/claudecfg`（路径、键注册表、严格 JSON、patch/diff、合并、scope、敏感值、Skills 状态）；`internal/bridge/claude_config.go` 提供读写/备份/项目/Skills 桥接；`internal/desktop/pages/access/claude/claudecfg` 作为嵌套 UI 子包挂到 Claude 页签，不新增导航项。写入统一走 `claudeSettingsMu`、备份分桶、冲突检测与原子写。

**Tech Stack:** Go 1.27.2、MyGo v0.6.2 原生 UI、`encoding/json`（UseNumber）、标准库 `os`/`path/filepath`/`crypto/sha256`；测试全部离线临时目录。

**Spec:** `docs/superpowers/specs/2026-11-07-claude-code-config-page-design.md`

## Global Constraints

- 不修改 `frontend/`；全部 UI 用 MyGo 原生视图。
- 不新增侧栏导航；唯一入口「Agent 接入」。
- API Key / 网关令牌永不落日志、错误、事件；备份内容 0600。
- 写入必须：持 `claudeSettingsMu` → 重读比对 Hash → 备份成功 → 只 patch 目标键 → 原子写；失败保留原文件。
- 受管键：13 个接入投影 env 强只读；接入文档顶层键只读；其余合成 env 弱警告；设置保存强制恢复强受管键原值。
- Skills：`skillOverrides` 四态 + 继承；插件/作者/托管锁定不可写；未知 override 保留。
- 不真实调用 Claude Code；全部测试用临时 HOME 和 fake 数据。
- 每个任务先写失败测试，再实现；并发相关测试带 `-race`。

## Review Focus

- 项目本地路径在不同 Git 形态（非 Git、家目录仓库、worktree）下写错文件 — 测试必须断言实际解析路径与候选提示。
- 合并预览与 Claude Code 实际层级不一致（项目级 `auto/bypassPermissions`、`skipDangerousModePermissionPrompt` OR、`skillOverrides` 逐键）— 每类特殊规则都要有表驱动测试。
- 接入 Apply 与设置保存并发写同一 `settings.json` — 用 `-race` 测串行化与冲突检测。
- 插件来源 Skill 被误写 override — 测试必须断言插件锁定行不产生 `skillOverrides` 写入。
- JSON 明文密钥/项目共享密钥泄露 — 测试敏感路径遮蔽与项目共享警告。

---

## 文件结构

```
internal/claudecfg/
├─ paths.go / paths_test.go
├─ keys.go / keys_test.go
├─ doc.go / doc_test.go
├─ merge.go / merge_test.go
├─ scope.go / scope_test.go
├─ sensitive.go / sensitive_test.go
├─ skills.go / skills_test.go
└─ managed.go / managed_test.go

internal/bridge/
├─ claude_config.go / claude_config_test.go
├─ claude_projects.go / claude_projects_test.go
└─ claude_skills.go / claude_skills_test.go

internal/desktop/state/state.go         新增 ClaudeCfg 状态
internal/desktop/pages/access/claude/claude.go  挂载设置区
internal/desktop/pages/access/claude/claudecfg/
├─ view.go        骨架/层级/项目/视图切换
├─ form.go        七组表单
├─ jsonview.go    JSON 视图
├─ mergeview.go   合并预览
├─ save.go        保存确认/结果
└─ skills.go      Skills 卡片
```

---

### Task 1: claudecfg 路径解析

**Files:**
- Create: `internal/claudecfg/paths.go`
- Test: `internal/claudecfg/paths_test.go`

**Interfaces:**
- Produces: `type ScopeKind string`（`user/project/local/managed`）；`type Env struct{ Home, GOOS string }`；`type Resolved struct{ Kind ScopeKind; File, Display, Project, Note string; Exists bool; Candidates []string }`；`func Resolve(kind ScopeKind, project string, env Env) (Resolved, error)`。

- [ ] **Step 1: 写失败测试**：用户级返回 `Home/.claude/settings.json`；项目共享返回 `<project>/.claude/settings.json`；Git 仓库子目录解析到仓库根 `.claude/settings.local.json` 且 `Candidates` 同时含项目目录路径；非 Git 回退项目目录；`GOOS=windows` 的 managed 路径。
- [ ] **Step 2: 运行** `go test ./internal/claudecfg -run TestResolve -v`，预期 FAIL（包/函数不存在）。
- [ ] **Step 3: 实现** `Resolve`：向上查找 `.git`（目录或文件）；`filepath.EvalSymlinks` 对已存在文件求真实路径；`Display` 将 Home 前缀替换为 `~`；家目录仓库回退并写 `Note`。
- [ ] **Step 4: 运行** 同命令，预期 PASS；再跑 `go test ./internal/claudecfg -race`。

### Task 2: 键注册表与受管集合

**Files:**
- Create: `internal/claudecfg/keys.go`, `internal/claudecfg/managed.go`
- Test: `internal/claudecfg/keys_test.go`, `internal/claudecfg/managed_test.go`

**Interfaces:**
- Produces: `KeySpec{Path, Kind, Scopes, Merge, Apply, Group, Sensitive, Enum, Default}`；`Lookup(path) (KeySpec,bool)`；`UserOnly`, `NoProjectShared`, `ManagedOnly`, `AccessManagedEnv`；`ManagedSet{StrongEnv, StrongTopLevel, WeakEnv []string; Source map[string]string}`；`ComputeManagedSet(configs []map[string]any, composedEnv map[string]string) ManagedSet`。

- [ ] **Step 1: 失败测试**：`AccessManagedEnv` 恰好 13 个；`skipDangerousModePermissionPrompt` 默认 `true`、`Merge=or`、不在 `NoProjectShared` 之外；`skillOverrides` 所有层允许、`Merge=skill`；项目共享禁止 4 键；`ComputeManagedSet` 把接入文档 `permissions` 放进 `StrongTopLevel`，把 `HTTPS_PROXY` 放进 `WeakEnv`。
- [ ] **Step 2: 运行** `go test ./internal/claudecfg -run 'TestKey|TestManaged' -v`，预期 FAIL。
- [ ] **Step 3: 实现** 注册表（附录 A 的键；`MergeOr` 新增枚举）；`ComputeManagedSet` 去重、排序、`env.` 前缀路径。
- [ ] **Step 4: 运行** 同命令 PASS。

### Task 3: 严格 JSON 文档与 diff/patch

**Files:**
- Create: `internal/claudecfg/doc.go`
- Test: `internal/claudecfg/doc_test.go`

**Interfaces:**
- Produces: `type Doc struct{ Raw, Original map[string]any; Hash string; MTime time.Time }`；`ParseDoc([]byte) (*Doc,error)`；`(*Doc).Marshal() ([]byte,error)`；`Diff(original,current map[string]any) []string`；`ApplyPatch(base map[string]any, changed []string, current map[string]any) map[string]any`；`Get/Set/Delete`。
- 错误类型：`type ParseError struct{ Line, Col int; Msg string }`，`Error() string`。

- [ ] **Step 1: 失败测试**：拒绝注释/尾逗号并给出行列；`UseNumber` 保留大整数；`Marshal` 2 空格 + 末尾换行；`Diff` 只返回叶子路径（含数组整体）；`ApplyPatch` 只改目标叶子、保留未知键。
- [ ] **Step 2: 运行** `go test ./internal/claudecfg -run 'TestParse|TestDiff|TestPatch' -v`，预期 FAIL。
- [ ] **Step 3: 实现**；错误偏移用 `json.SyntaxError.Offset` 计算行列；超过 1 MiB 返回 `ErrTooLarge`。
- [ ] **Step 4: 运行** PASS。

### Task 4: 合并引擎

**Files:**
- Create: `internal/claudecfg/merge.go`
- Test: `internal/claudecfg/merge_test.go`

**Interfaces:**
- Produces: `type Layer struct{ Kind ScopeKind; File string; Doc map[string]any }`；`type Contribution struct{ Layer ScopeKind; File string; Value any; Effective bool }`；`type MergedEntry struct{ Path string; Value any; Kind MergeKind; Source ScopeKind; Contributions []Contribution }`；`func Merge(layers []Layer) (map[string]any, map[string]MergedEntry)`。

- [ ] **Step 1: 失败测试**：层顺序 user→project→local→managed；`env` 逐变量；`permissions.allow` 并集去重；`hooks` 累加；`enabledPlugins` 逐键；`skillOverrides` 逐键；`fallbackModel` 整值替换；项目级 `auto` 不生效并回落；`skipDangerousModePermissionPrompt` 任一层 true 即 true。
- [ ] **Step 2: 运行** `go test ./internal/claudecfg -run TestMerge -v`，预期 FAIL。
- [ ] **Step 3: 实现** 叶子路径收集 + 每路径策略；贡献列表低→高；`Effective` 标记胜出项。
- [ ] **Step 4: 运行** PASS；`-race`。

### Task 5: Scope 校验与敏感值

**Files:**
- Create: `internal/claudecfg/scope.go`, `internal/claudecfg/sensitive.go`
- Test: `internal/claudecfg/scope_test.go`, `internal/claudecfg/sensitive_test.go`

**Interfaces:**
- Produces: `type Violation struct{ Path, Reason string; Present, Changed bool }`；`Validate(doc map[string]any, target ScopeKind, base map[string]any, managed ManagedSet) []Violation`；`IsSensitivePath(path string) bool`；`MaskSecret(s string) string`；`ContainsSecretLike(doc map[string]any) bool`。

- [ ] **Step 1: 失败测试**：用户级写 30 键不违规；项目共享写 4 键违规；项目本地写 `skipDangerous` 不违规；项目级 `auto/bypass` 警告；改强受管键 `Changed=true`；`TOKEN/KEY/SECRET/PASSWORD/AUTH` 路径敏感；项目共享疑似密钥检测。
- [ ] **Step 2: 运行** `go test ./internal/claudecfg -run 'TestScope|TestSensitive' -v`，预期 FAIL。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 运行** PASS。

### Task 6: Skills 状态与锁定

**Files:**
- Create: `internal/claudecfg/skills.go`
- Test: `internal/claudecfg/skills_test.go`

**Interfaces:**
- Produces: `type SkillState string`（`on/name-only/user-invocable-only/off`）；`type SkillSource string`（`built-in/user/project/plugin/unknown`）；`type Skill struct{ Name, Description, PluginID string; Source SkillSource; LockedBy string }`；`ResolveSkill(skill Skill, layers []Layer, pluginEnabled map[string]bool) (SkillState, string)`。

- [ ] **Step 1: 失败测试**：缺省 on；用户级 off；项目本地 off 覆盖项目共享 on；插件来源锁定且插件关闭→off；作者锁定最高 user-invocable-only；托管 off 锁定；`__proto__` 返回 locked `reserved-name`；未知 override 保留。
- [ ] **Step 2: 运行** `go test ./internal/claudecfg -run TestSkill -v`，预期 FAIL。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 运行** PASS。

### Task 7: Bridge 读取与 ManagedSet

**Files:**
- Create: `internal/bridge/claude_config.go`, `internal/bridge/claude_config_test.go`
- Modify: `internal/bridge/access_live.go`（导出/复用 `atomicWriteFile0600`，如已同包可直接用）

**Interfaces:**
- Produces: `type ClaudeConfigs struct{ A *app.App; Home string }`；`ReadLayerRequest`；`LayerView`；`func (s ClaudeConfigs) ReadClaudeConfigLayer(req) (LayerView,error)`；`func (s ClaudeConfigs) ClaudeConfigOverview() (ClaudeConfigOverview,error)`；`func (s ClaudeConfigs) ManagedSetForClaude() (claudecfg.ManagedSet,error)`。

- [ ] **Step 1: 失败测试**：临时 HOME 读用户级/项目共享/本地；不存在空态；坏 JSON 返回解析错误；ManagedSet 13 强 env + 接入文档顶层键 + 全局 env 弱键；`CLAUDE_CONFIG_DIR` 警告。
- [ ] **Step 2: 运行** `go test ./internal/bridge -run TestClaudeConfigRead -v`，预期 FAIL。
- [ ] **Step 3: 实现** 读取、路径解析、ManagedSet 计算。
- [ ] **Step 4: 运行** PASS。

### Task 8: Bridge 保存、备份、冲突

**Files:**
- Modify: `internal/bridge/claude_config.go`, `internal/bridge/claude_config_test.go`

**Interfaces:**
- Produces: `SaveClaudeConfigRequest`；`SaveResult`；`func (s ClaudeConfigs) SaveClaudeConfigLayer(req) (SaveResult,error)`；`BackupInfo`；`ListClaudeConfigBackups`；`RestoreClaudeConfigBackup`。

- [ ] **Step 1: 失败测试**：保存只改目标叶子；备份 0600、10 份保留、meta 绑定目标路径；Hash 冲突返回 `ConflictError`；改强受管键返回 `ErrManagedKey`；写入失败保留原文件；并发保存 `-race` 串行。
- [ ] **Step 2: 运行** `go test ./internal/bridge -run 'TestClaudeSave|TestClaudeBackup' -v`，预期 FAIL。
- [ ] **Step 3: 实现** `var claudeSettingsMu sync.Mutex`；备份桶 `<hash16>/<ts>-<filename>` + `meta.json`。
- [ ] **Step 4: 运行** PASS + `-race`。

### Task 9: 项目列表与 Skills 发现

**Files:**
- Create: `internal/bridge/claude_projects.go`, `internal/bridge/claude_skills.go`
- Test: `internal/bridge/claude_projects_test.go`, `internal/bridge/claude_skills_test.go`

**Interfaces:**
- Produces: `ClaudeProject`；`func (s ClaudeConfigs) ListClaudeProjects() ([]ClaudeProject,error)`；`SkillView`；`func (s ClaudeConfigs) ListClaudeSkills(project string) ([]SkillView,error)`。

- [ ] **Step 1: 失败测试**：`~/.claude.json` 的 `projects` map 解析、数组旧形态兼容、坏文件不阻塞；`~/.claude/skills/*/SKILL.md` 与项目 `.claude/skills` 发现；插件来源锁定；未知 override 出现在结果。
- [ ] **Step 2: 运行** `go test ./internal/bridge -run 'TestClaudeProjects|TestClaudeSkills' -v`，预期 FAIL。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 运行** PASS。

### Task 10: 桌面状态与设置区骨架

**Files:**
- Modify: `internal/desktop/state/state.go`
- Create: `internal/desktop/pages/access/claude/claudecfg/view.go`
- Modify: `internal/desktop/pages/access/claude/claude.go`
- Test: `internal/desktop/pages/access/claude/claudecfg/view_test.go`

**Interfaces:**
- Produces: `state.ClaudeCfg`；`claudecfg.Section(c, st)`；`claudecfg.Prepare(st, cfgs)`。

- [ ] **Step 1: 失败测试**：渲染层级页签/保存按钮；切换层级写 `st.ClaudeCfg.Layer`；设置区挂载后接入摘要仍在。
- [ ] **Step 2: 运行** `go test ./internal/desktop/pages/access/claude/... -v`，预期 FAIL。
- [ ] **Step 3: 实现** 骨架、状态字段、`claude.go` 在 `EditorCard` 后调用。
- [ ] **Step 4: 运行** PASS。

### Task 11: 表单七组（模型/环境/权限/状态栏/MCP/Skills/记忆）

**Files:**
- Create: `internal/desktop/pages/access/claude/claudecfg/form.go`, `skills.go`
- Test: same package tests

**Interfaces:**
- Consumes: `LayerView`, `ManagedSet`, `SkillView`。
- Produces: `FormSection(c, st)`；`SkillsCard(c, st)`。

- [ ] **Step 1: 失败测试**：主模型/回退/强度；环境变量受管只读 + 折叠；危险模式确认默认 true + 项目共享禁用；状态栏对象；插件/MCP；Skills 四态/锁定/未知；记忆数值。
- [ ] **Step 2: 运行** `go test ./internal/desktop/pages/access/claude/... -v`，预期 FAIL。
- [ ] **Step 3: 实现** 最小表单；每项变更写 `st.ClaudeCfg.Doc`、`RecomputeDirty`。
- [ ] **Step 4: 运行** PASS。

### Task 12: JSON 视图与合并预览

**Files:**
- Create: `internal/desktop/pages/access/claude/claudecfg/jsonview.go`, `mergeview.go`
- Test: same package tests

- [ ] **Step 1: 失败测试**：JSON 语法错误行/列；非法 `skillOverrides` 阻断；合并预览显示来源/合并方式/权限顺序/OR/Skills 锁定。
- [ ] **Step 2: 运行** 预期 FAIL。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 运行** PASS。

### Task 13: 保存确认、结果条、冲突与备份恢复

**Files:**
- Create: `internal/desktop/pages/access/claude/claudecfg/save.go`
- Modify: `internal/desktop/uikit/widgets.go`（新增 `RichDialog`）
- Test: same package + uikit tests

- [ ] **Step 1: 失败测试**：保存确认列出路径/变更键/备份/生效范围；冲突三动作；结果条三态；恢复只作用于正确目标。
- [ ] **Step 2: 运行** 预期 FAIL。
- [ ] **Step 3: 实现**。
- [ ] **Step 4: 运行** PASS。

### Task 14: 接入抽屉/内联与设置区互斥

**Files:**
- Modify: `internal/desktop/pages/access/claude/claude.go`, `internal/desktop/pages/access/access.go`
- Test: access page tests

- [ ] **Step 1: 失败测试**：摘要卡可见；点「编辑接入」展开同页面板；设置 dirty 时应用接入弹「先处理未保存设置」；应用后设置区刷新。
- [ ] **Step 2: 运行** 预期 FAIL。
- [ ] **Step 3: 实现**（MyGo 无原生 drawer，先用同页可展开面板/子视图，保持不跳导航）。
- [ ] **Step 4: 运行** PASS。

### Task 15: 最终验收

- [ ] `go build ./...`
- [ ] `go vet ./...`
- [ ] `go test ./...`
- [ ] `go test -race ./internal/claudecfg ./internal/bridge ./internal/desktop/...`
- [ ] 按需求 §10 验收清单逐项核对。
