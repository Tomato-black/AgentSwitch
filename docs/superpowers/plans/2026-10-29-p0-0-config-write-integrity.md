# P0-0 配置写入一致性实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 让「仅保存」只落库、不写 Agent live 文件；「应用」才投影 live，且任何 live 写入前先备份；界面显示「待应用」。

**Architecture:** 在 `access_configs` 增加 `needs_apply`（migration v4，v3 是合并前旧版本号不可复用）。`AccessConfigs.Update` 只写库并标记 current 配置待应用；`RegenerateDerived` 拆成「只写持久文件」与「写持久文件 + 备份后写 live」两条路径，系统级变更（端口/令牌/逻辑模型改名/全局配置）仍即时更新 live，但遇到待应用配置时跳过 live 并返回可见警告，避免静默把「仅保存」的修改应用掉。

**Tech Stack:** Go 1.27、modernc.org/sqlite、MyGo v0.6.2；测试全部离线（`app.Open` + temp Home + Fake Provider）。

**Spec:** `docs/product/roadmap.md` P0-0；`docs/product/feature-gap-analysis.md` §A0；`docs/product/decisions.md` PD-2。

## Global Constraints

- **不自动 git commit**：工作区已有大量未提交改动；每个 Task 结束用 `git diff --stat` 记录改动，等用户确认后统一提交。
- **不新增第三方依赖**。
- **不改 gateway 三入站契约、不改接入文档 JSON 格式**。
- **schema 只用追加迁移**：本次新增 migration v4；不要复用 v3（`rejectLegacySchema` 把 v3 视为旧库）。
- **所有测试离线**，不访问真实上游。
- **不存明文密钥到日志/错误**。
- 并发相关测试最终跑 `-race`。

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/storage/storage.go` | `AccessConfig` 增加 `NeedsApply` |
| `internal/storage/repos.go` | `accessConfigCols` 增加 `needs_apply` |
| `internal/storage/accessconfigs_repo.go` | scan/Save/SetCurrent 支持 `needs_apply` |
| `internal/storage/db.go` | migration v4 + 旧库列形状守卫 |
| `internal/storage/storage_test.go` | 迁移版本断言、round-trip 测试 |
| `internal/bridge/access_configs.go` | View 字段、Update 标记待应用、拆分派生刷新、备份前置 |
| `internal/bridge/accessconfigs_test.go` | 保存/应用/备份/派生刷新测试 |
| `internal/desktop/accesskit/editor.go` | 仅保存后的「需应用」提示 |
| `internal/desktop/pages/access/access.go` | 配置行「待应用」徽章、按钮文案、删除文案 |
| `internal/desktop/pages/access/claudedesktop/desktop.go` | Desktop 配置行徽章/文案、保存提示 |
| `internal/desktop/pages/access/access_test.go` | UI 徽章测试 |
| `README.md` | 明确「仅保存」与「应用」的差异 |

---

### Task 1: 存储层 `needs_apply` 列与 round-trip

**Files:**
- Modify: `internal/storage/storage.go:67-77`
- Modify: `internal/storage/repos.go:74`
- Modify: `internal/storage/accessconfigs_repo.go:7-18,42-70,76-100`
- Modify: `internal/storage/db.go:78-94,140-230`
- Test: `internal/storage/storage_test.go:40-68`

**Interfaces:**
- Produces: `storage.AccessConfig.NeedsApply bool`
- Produces: `AccessConfigRepo` 读写 `needs_apply`；`SetCurrent` 把目标行的 `needs_apply` 清零
- Produces: migration v4（`schema_migrations` 期望 `[1 2 4]`）

- [x] **Step 1: 写失败测试（round-trip + 迁移版本）**

在 `internal/storage/storage_test.go` 增加：

```go
func TestAccessConfigNeedsApplyRoundTrip(t *testing.T) {
	s := tempStore(t)
	r := s.AccessConfigs()
	saved, err := r.Save(AccessConfig{
		AppType: "claude", Name: "n", LogicalModelID: "lm",
		Settings: "{}", NeedsApply: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.NeedsApply {
		t.Fatal("Get 应保留 NeedsApply=true")
	}
	if err := r.SetCurrent("claude", saved.ID); err != nil {
		t.Fatal(err)
	}
	cur, err := r.Current("claude")
	if err != nil {
		t.Fatal(err)
	}
	if cur.NeedsApply {
		t.Fatal("SetCurrent 应清除目标配置的 needs_apply")
	}
}
```

把 `TestMigrationIdempotent` 的期望从 `[1 2]` 改为 `[1 2 4]`：

```go
if rows.Err() != nil || len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 4 {
    t.Fatalf("schema_migrations = %v, want [1 2 4]", got)
}
```

- [x] **Step 2: 运行测试确认失败**

Run: `go test ./internal/storage -run 'TestAccessConfigNeedsApplyRoundTrip|TestMigrationIdempotent' -v`  
Expected: FAIL（`NeedsApply` 字段不存在 / 迁移版本仍为 `[1 2]`）。

- [x] **Step 3: 实现存储层**

`internal/storage/storage.go` 的 `AccessConfig` 增加字段：

```go
NeedsApply bool // current 配置有未投影到 Agent live 文件的修改
```

`internal/storage/repos.go`：

```go
const accessConfigCols = `id,app_type,name,logical_model_id,settings,is_current,needs_apply,sort_order,created_at,updated_at`
```

`internal/storage/accessconfigs_repo.go`：

```go
func scanAccessConfig(sc func(...any) error) (AccessConfig, error) {
	var c AccessConfig
	var cur, pending int
	if err := sc(&c.ID, &c.AppType, &c.Name, &c.LogicalModelID, &c.Settings,
		&cur, &pending, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return AccessConfig{}, err
	}
	c.IsCurrent = cur == 1
	c.NeedsApply = pending == 1
	if c.Settings == "" {
		c.Settings = "{}"
	}
	return c, nil
}
```

`Save` 的 SQL 改为：

```go
_, err = r.db.Exec(`INSERT INTO access_configs(id,app_type,name,logical_model_id,settings,is_current,needs_apply,sort_order,created_at,updated_at)
	VALUES(?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(id) DO UPDATE SET app_type=excluded.app_type, name=excluded.name,
		logical_model_id=excluded.logical_model_id, settings=excluded.settings,
		is_current=excluded.is_current, needs_apply=excluded.needs_apply,
		sort_order=excluded.sort_order, updated_at=excluded.updated_at`,
	c.ID, c.AppType, c.Name, c.LogicalModelID, c.Settings,
	boolInt(c.IsCurrent), boolInt(c.NeedsApply), c.SortOrder, c.CreatedAt, c.UpdatedAt)
```

`SetCurrent` 的目标行更新改为：

```go
if _, err := tx.Exec(`UPDATE access_configs SET is_current=1, needs_apply=0 WHERE id=?`, id); err != nil {
```

`internal/storage/db.go` 增加：

```go
// migrationV4 为 access_configs 增加 needs_apply：区分「仅保存」与「已应用」。
// v3 是合并单一 schema 前的旧版本号（rejectLegacySchema 拒绝），故从 4 起。
const migrationV4 = `
ALTER TABLE access_configs ADD COLUMN needs_apply INTEGER NOT NULL DEFAULT 0;
`
```

`migrations` 增加：

```go
{4, migrationV4},
```

在 `rejectLegacySchema` 的 access_configs `settings` 列检查之后增加：

```go
v4, err := hasRecordedVersion(db, 4)
if err != nil {
    return err
}
if v4 {
    hasNeedsApply, err := columnExists(db, "access_configs", "needs_apply")
    if err != nil {
        return err
    }
    if !hasNeedsApply {
        return errLegacyDB
    }
}
```

- [x] **Step 4: 运行测试确认通过**

Run: `go test ./internal/storage -v`  
Expected: PASS（全部 storage 测试）。

- [x] **Step 5: 记录改动（不 commit）**

Run: `git diff --stat internal/storage`  
Expected: 5 个 storage 文件有改动。

---

### Task 2: View 暴露 `NeedsApply`，`Update` 只落库并标记待应用

**Files:**
- Modify: `internal/bridge/access_configs.go:31-45,101-128,135-143,67-95`
- Test: `internal/bridge/accessconfigs_test.go`（新增测试）

**Interfaces:**
- Consumes: `storage.AccessConfig.NeedsApply`
- Produces: `AccessConfigView.NeedsApply bool`
- Produces: `AccessConfigs.Update` 不再写 live；current 配置内容变化时 `NeedsApply=true`

- [x] **Step 1: 写失败测试**

在 `internal/bridge/accessconfigs_test.go` 增加：

```go
func TestUpdateCurrentMarksNeedsApplyAndKeepsLive(t *testing.T) {
	a := tempApp(t)
	home := t.TempDir()
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "工作", "lm1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(livePath, []byte(`{"old":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}

	if err := acc.Update(v.ID, "工作", "lm1", `{"env":{"CUSTOM":"x"}}`); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("仅保存不得改写 live：before=%s after=%s", before, after)
	}
	list := acc.List("claude")
	if len(list) != 1 || !list[0].NeedsApply {
		t.Fatalf("current 配置内容变化后应标记待应用：%+v", list)
	}
}
```

- [x] **Step 2: 运行测试确认失败**

Run: `go test ./internal/bridge -run TestUpdateCurrentMarksNeedsApplyAndKeepsLive -v`  
Expected: FAIL（live 被改写 / `NeedsApply` 字段不存在）。

- [x] **Step 3: 实现 View 与 Update**

`AccessConfigView` 增加：

```go
NeedsApply bool // current 配置有未应用的修改
```

`view` 改为：

```go
v := AccessConfigView{ID: c.ID, Name: c.Name, AppType: c.AppType,
	LogicalModelID: c.LogicalModelID, Settings: c.Settings, IsCurrent: c.IsCurrent,
	NeedsApply: c.IsCurrent && c.NeedsApply}
```

`Update` 在保存前记录旧值，保存后设置标记：

```go
oldSettings, oldModel := c.Settings, c.LogicalModelID
...
c.Name, c.LogicalModelID = name, logicalModelID
c.Settings = raw
changed := oldSettings != raw || oldModel != logicalModelID
if c.IsCurrent {
    c.NeedsApply = c.NeedsApply || changed
} else {
    c.NeedsApply = false
}
if _, err := s.A.Store().AccessConfigs().Save(c); err != nil {
    return uniqueErr(err, "配置名已存在")
}
```

把 `Update` 末尾的 `s.RegenerateDerived()` 改为 `s.RegeneratePersistOnly()`（该方法在 Task 3 实现；本 Task 先加一个临时空实现会编译失败，因此 Task 2 与 Task 3 可合并执行，或先在 `access_configs.go` 加：

```go
func (s AccessConfigs) RegeneratePersistOnly() []error {
	return s.RegenerateDerived()
}
```

Task 3 再替换为真正的只持久文件实现）。`Create` 与 `Remove` 也改用 `RegeneratePersistOnly()`。

- [x] **Step 4: 运行测试确认通过**

Run: `go test ./internal/bridge -run TestUpdateCurrentMarksNeedsApplyAndKeepsLive -v`  
Expected: PASS。

- [x] **Step 5: 记录改动**

Run: `git diff --stat internal/bridge/access_configs.go internal/bridge/accessconfigs_test.go`

---

### Task 3: 拆分派生刷新；live 写入前备份；待应用时跳过 live

**Files:**
- Modify: `internal/bridge/access_configs.go:258-301`
- Test: `internal/bridge/accessconfigs_test.go`（新增两个测试）

**Interfaces:**
- Produces: `RegeneratePersistOnly() []error` 只写持久文件
- Produces: `RegenerateDerived() []error` 写持久文件 + 备份后写 live
- Produces: current 配置 `NeedsApply=true` 时，`RegenerateDerived` 跳过 live 并返回含「未应用」的错误

- [x] **Step 1: 写失败测试**

```go
func TestRegenerateDerivedSkipsLiveWhenPending(t *testing.T) {
	a := tempApp(t)
	home := t.TempDir()
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	livePath := filepath.Join(home, ".claude", "settings.json")
	os.WriteFile(livePath, []byte(`{"old":1}`), 0o600)
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(livePath)
	if err := acc.Update(v.ID, "工作", "lm1", `{"env":{"CUSTOM":"x"}}`); err != nil {
		t.Fatal(err)
	}
	errs := acc.RegenerateDerived()
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "未应用") {
		t.Fatalf("应返回待应用警告：%v", errs)
	}
	after, _ := os.ReadFile(livePath)
	if string(before) != string(after) {
		t.Fatal("待应用时 RegenerateDerived 不得改写 live")
	}
}

func TestRegenerateDerivedBacksUpBeforeLive(t *testing.T) {
	a := tempApp(t)
	home := t.TempDir()
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "old-name", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	livePath := filepath.Join(home, ".claude", "settings.json")
	os.WriteFile(livePath, []byte(`{"old":1}`), 0o600)
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(livePath)

	// 逻辑模型改名触发 RegenerateDerived（系统级变更应刷新 live）。
	lm, _ := a.Store().LogicalModels().Get("lm1")
	lm.Name = "new-name"
	if _, err := a.Store().LogicalModels().Save(lm); err != nil {
		t.Fatal(err)
	}
	if errs := (AccessConfigs{A: a}).RegenerateDerived(); len(errs) != 0 {
		t.Fatalf("系统级刷新不应失败：%v", errs)
	}
	after, _ := os.ReadFile(livePath)
	if !strings.Contains(string(after), "new-name") {
		t.Fatalf("live 应包含新模型名：%s", after)
	}

	backupDir := filepath.Join(home, ".agentswitch", "backups", "claude")
	ents, err := os.ReadDir(backupDir)
	if err != nil || len(ents) == 0 {
		t.Fatalf("应产生备份：%v", err)
	}
	latest := ents[len(ents)-1].Name()
	b, _ := os.ReadFile(filepath.Join(backupDir, latest))
	if string(b) != string(before) {
		t.Fatalf("最新备份应等于写入前 live：backup=%s before=%s", b, before)
	}
}
```

- [x] **Step 2: 运行测试确认失败**

Run: `go test ./internal/bridge -run 'TestRegenerateDerivedSkipsLiveWhenPending|TestRegenerateDerivedBacksUpBeforeLive' -v`  
Expected: FAIL（live 被改写 / 无备份）。

- [x] **Step 3: 重构派生刷新**

把 `RegenerateDerived` 改为：

```go
func (s AccessConfigs) RegeneratePersistOnly() []error { return s.regenerate(false) }
func (s AccessConfigs) RegenerateDerived() []error     { return s.regenerate(true) }

func (s AccessConfigs) regenerate(writeLive bool) []error {
	var errs []error
	fail := func(appType, stage string, err error) {
		if err == nil {
			return
		}
		errs = append(errs, fmt.Errorf("%s/%s: %w", appType, stage, err))
		s.A.Log().Error("access derived refresh failed", "app", appType, "stage", stage, "err", err)
	}
	for _, appType := range []string{"claude", "codex"} {
		cfgs, err := s.A.Store().AccessConfigs().ListByApp(appType)
		if err != nil {
			fail(appType, "list", err)
			continue
		}
		if len(cfgs) == 0 {
			continue
		}
		cur, _ := s.A.Store().AccessConfigs().Current(appType)
		liveBackedUp := false
		for _, c := range cfgs {
			env, doc, model, err := s.composeFor(c)
			if err != nil {
				fail(appType, "compose", err)
				continue
			}
			if _, perr := s.writePersistEnv(c, env, doc, model); perr != nil {
				fail(appType, "persist", perr)
			}
			if !writeLive || cur.ID != c.ID {
				continue
			}
			if c.NeedsApply {
				fail(appType, "live", fmt.Errorf("配置「%s」有未应用的修改，未刷新 Agent 文件；请点击应用", c.Name))
				continue
			}
			if !liveBackedUp {
				if berr := s.backupLive(appType); berr != nil {
					fail(appType, "backup", berr)
					continue
				}
				liveBackedUp = true
			}
			if appType == "claude" {
				err = s.writeClaudeLive(doc, env)
			} else {
				err = s.writeCodexLive(env, model, doc)
			}
			fail(appType, "live", err)
		}
	}
	return errs
}
```

确认调用点：
- `Create` / `Update` / `Remove`：`s.RegeneratePersistOnly()`
- `SaveGlobalConfig`：`s.RegenerateDerived()`（不变）
- `bridge.Gateway.Start`：`RegenerateDerived()`（不变）
- `logicalmodels.refreshDerived`：`RegenerateDerived()`（不变）

- [x] **Step 4: 运行测试确认通过**

Run: `go test ./internal/bridge -run 'TestRegenerateDerivedSkipsLiveWhenPending|TestRegenerateDerivedBacksUpBeforeLive|TestUpdateCurrentMarksNeedsApplyAndKeepsLive' -v`  
Expected: PASS。

- [x] **Step 5: 运行 bridge 全包回归**

Run: `go test ./internal/bridge`  
Expected: PASS。

- [x] **Step 6: 记录改动**

Run: `git diff --stat internal/bridge`

---

### Task 4: `Apply` 清除待应用，并验证备份是「保存前」状态

**Files:**
- Modify: `internal/bridge/access_configs.go:151-191`（如 SetCurrent 已清标记则无需改动，仅补测试）
- Test: `internal/bridge/accessconfigs_test.go`（新增测试）

**Interfaces:**
- Consumes: `needs_apply` 由 `SetCurrent` 清零
- Produces: 核心验收：`仅保存 → live 不变`；`保存并应用 → 最新备份 = 保存前 live`

- [x] **Step 1: 写失败测试**

```go
func TestApplyClearsNeedsApplyAndBackupIsPreSave(t *testing.T) {
	a := tempApp(t)
	home := t.TempDir()
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	livePath := filepath.Join(home, ".claude", "settings.json")
	os.WriteFile(livePath, []byte(`{"old":1}`), 0o600)
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(livePath)

	if err := acc.Update(v.ID, "工作", "lm1", `{"env":{"CUSTOM":"x"}}`); err != nil {
		t.Fatal(err)
	}
	if got := acc.List("claude"); len(got) != 1 || !got[0].NeedsApply {
		t.Fatalf("保存后应待应用：%+v", got)
	}
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(livePath)
	if !strings.Contains(string(after), "CUSTOM") {
		t.Fatalf("应用后 live 应包含新值：%s", after)
	}
	if got := acc.List("claude"); len(got) != 1 || got[0].NeedsApply {
		t.Fatalf("应用后不应待应用：%+v", got)
	}
	backupDir := filepath.Join(home, ".agentswitch", "backups", "claude")
	ents, err := os.ReadDir(backupDir)
	if err != nil || len(ents) == 0 {
		t.Fatalf("应有备份：%v", err)
	}
	latest := ents[len(ents)-1].Name()
	b, _ := os.ReadFile(filepath.Join(backupDir, latest))
	if string(b) != string(before) {
		t.Fatalf("最新备份应等于保存并应用前的 live：backup=%s before=%s", b, before)
	}
}
```

- [x] **Step 2: 运行测试确认失败**

Run: `go test ./internal/bridge -run TestApplyClearsNeedsApplyAndBackupIsPreSave -v`  
Expected: FAIL（应用后可能仍待应用，或备份是新状态）。

- [x] **Step 3: 如有必要修正 Apply**

确认 `SetCurrent` 已在目标行 `needs_apply=0`。如果测试仍失败，在 `Apply` 成功写 live 后、`SetCurrent` 前显式：

```go
c.NeedsApply = false
if _, err := s.A.Store().AccessConfigs().Save(c); err != nil {
    return err
}
```

注意：`Save(c)` 会更新 `updated_at`，可接受；优先依赖 `SetCurrent` 的 SQL 清零，避免多一次写。

- [x] **Step 4: 运行测试确认通过**

Run: `go test ./internal/bridge -run TestApplyClearsNeedsApplyAndBackupIsPreSave -v`  
Expected: PASS。

- [x] **Step 5: 记录改动**

Run: `git diff --stat internal/bridge`

---

### Task 5: UI 显示「待应用」与保存提示

**Files:**
- Modify: `internal/desktop/accesskit/editor.go:163-223`
- Modify: `internal/desktop/pages/access/access.go:315-390,394-427`
- Modify: `internal/desktop/pages/access/claudedesktop/desktop.go:198-222,226-277`
- Test: `internal/desktop/pages/access/access_test.go`（新增）
- Test: `internal/desktop/accesskit/roles_save_test.go`（新增/扩展）

**Interfaces:**
- Consumes: `AccessConfigView.NeedsApply`
- Produces: current 配置行显示「待应用」徽章；按钮在待应用时显示「应用」而非「重新应用」；仅保存后提示「点击『应用』后才会写入 Agent 文件」

- [x] **Step 1: 写失败测试（页面徽章）**

在 `internal/desktop/pages/access/access_test.go` 增加：

```go
func TestAccessRowShowsNeedsApplyBadge(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a}
	v, err := acc.Create("claude", "工作", "lm1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	if err := acc.Update(v.ID, "工作", "lm1", `{"env":{"CUSTOM":"x"}}`); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 900)
	if !tt.HasText("待应用") {
		t.Fatalf("应显示待应用徽章：%v", tt.Texts())
	}
}
```

- [x] **Step 2: 写失败测试（仅保存提示）**

在 `internal/desktop/accesskit/roles_save_test.go` 增加：

```go
func TestSaveOnlyOnCurrentShowsApplyHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"old":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{
		Open: true, Name: "cfg", ModelLabel: "my-code", DocJSON: `{"env":{}}`,
	}
	acc := bridge.AccessConfigs{A: a}
	saveFormWithApply(st, acc, true)
	// 找到当前配置并编辑它，再仅保存。
	list := acc.List("claude")
	if len(list) != 1 || !list[0].IsCurrent {
		t.Fatalf("前提不成立：%+v", list)
	}
	st.Access.Form = state.AccessForm{
		Open: true, EditID: list[0].ID, Name: "cfg", ModelLabel: "my-code",
		DocJSON: `{"env":{"CUSTOM":"x"}}`,
	}
	saveFormWithApply(st, acc, false)
	if !strings.Contains(st.Access.Msg, "点击「应用」") {
		t.Fatalf("仅保存应提示点击应用：%q", st.Access.Msg)
	}
}
```

- [x] **Step 3: 运行测试确认失败**

Run: `go test ./internal/desktop/pages/access ./internal/desktop/accesskit -run 'TestAccessRowShowsNeedsApplyBadge|TestSaveOnlyOnCurrentShowsApplyHint' -v`  
Expected: FAIL（无「待应用」文本 / 提示文案不含「点击「应用」」）。

- [x] **Step 4: 实现 UI**

`internal/desktop/pages/access/access.go` 的 `configRow` 第一行增加徽章：

```go
if badge := accesskit.RowBadge(v.IsCurrent); badge != "" {
    uikit.SoftBadge(c, t, s, "ok", badge)
}
if v.NeedsApply {
    uikit.SoftBadge(c, t, s, "warn", "待应用")
}
```

按钮文案改为：

```go
label := "应用"
if v.IsCurrent && !v.NeedsApply {
    label = "重新应用"
}
```

删除确认文案改为：

```go
msg := "确定删除接入配置「" + name + "」？Agent 仍在使用上次写入的网关配置。"
```

`internal/desktop/accesskit/editor.go` 的 `saveFormWithApply` 末尾改为：

```go
st.Access.Form = state.AccessForm{}
if apply && saved.ID != "" {
    st.Access.Msg = "已保存「" + name + "」，正在应用…"
    ApplyAsync(st, acc, saved)
    return
}
if saved.IsCurrent && saved.NeedsApply {
    st.Access.Msg = "已保存「" + name + "」；点击「应用」后才会写入 Agent 文件"
} else {
    st.Access.Msg = "已保存「" + name + "」"
}
```

`internal/desktop/pages/access/claudedesktop/desktop.go` 的 `ConfigRow` 同样增加徽章与按钮文案；`desktopSaveForm` 的保存消息改为：

```go
st.Access.Msg = "已保存「" + f.Name + "」"
if f.EditID != "" {
    for _, v := range acc.List("claude_desktop") {
        if v.ID == f.EditID && v.IsCurrent && v.NeedsApply {
            st.Access.Msg = "已保存「" + f.Name + "」；点击「应用」后才会写入 Claude Desktop 配置"
            break
        }
    }
}
```

- [x] **Step 5: 运行测试确认通过**

Run: `go test ./internal/desktop/pages/access ./internal/desktop/accesskit -v`  
Expected: PASS。

- [x] **Step 6: 记录改动**

Run: `git diff --stat internal/desktop`

---

### Task 6: 全量回归与安全清单

**Files:** 无新增；验证全部。

- [x] **Step 1: 构建**

Run: `go build ./...`  
Expected: exit 0。

- [x] **Step 2: 静态检查**

Run: `go vet ./...`  
Expected: exit 0。

- [x] **Step 3: 全量测试**

Run: `go test ./...`  
Expected: 全部 PASS。

- [x] **Step 4: 并发相关测试**

Run: `go test -race ./internal/storage ./internal/bridge ./internal/desktop/...`  
Expected: 全部 PASS。

- [x] **Step 5: 手工核对核心验收**

Run:

```bash
go test ./internal/bridge -run 'TestUpdateCurrentMarksNeedsApplyAndKeepsLive|TestApplyClearsNeedsApplyAndBackupIsPreSave|TestRegenerateDerivedSkipsLiveWhenPending|TestRegenerateDerivedBacksUpBeforeLive' -v
```

Expected: 4 个测试全部 PASS。

- [x] **Step 6: 记录改动**

Run: `git diff --stat`  
Expected: 只包含本计划的 storage/bridge/desktop/README 改动；不得出现 gateway 三入站契约或接入 JSON 格式改动。

---

### Task 7: 文档同步（只说已实现的事实）

**Files:**
- Modify: `README.md:70`（接入详情段）
- Modify: `docs/product/roadmap.md`（P0-0 状态改为「实施中」，如果本轮完成则标「已完成，待用户验收」）

- [x] **Step 1: 更新 README**

在「接入详情」段落补一句：

```markdown
「仅保存」只把配置保存到 AgentSwitch，不会改写 Agent 的配置文件；点「应用」才会把受管键写入 Agent 文件，写入前会自动备份。列表会显示「待应用」。
```

- [x] **Step 2: 更新路线图状态**

在 `docs/product/roadmap.md` P0-0 行末加：

```markdown
> 实施状态（2026-10-29）：已按本计划完成代码与测试，待用户验收。
```

- [x] **Step 3: 检查文档与代码一致**

Run: `grep -n '仅保存\|待应用\|写入前' README.md docs/product/roadmap.md`  
Expected: 文案与实际行为一致，不出现「仅保存会写 live」的旧描述。

---

## Self-Review

- **Spec coverage:** P0-0 的四个验收点都有对应测试：仅保存不改 live（Task 2）、备份等于保存前（Task 4）、待应用可见（Task 5）、系统刷新前备份（Task 3）。
- **Placeholder scan:** 无 TBD/TODO；每个代码步骤都有实现片段或明确命令。
- **Type consistency:** `NeedsApply` 在 storage/bridge/UI 三层同名；`RegeneratePersistOnly`/`RegenerateDerived` 在 Task 2/3 中签名一致。
- **Known limitation:** current 配置待应用时，系统级刷新（端口/令牌/逻辑模型改名）会跳过 live 并返回警告；用户需点击「应用」。这是刻意的安全取舍，避免静默应用「仅保存」的修改；后续 P1 可通过 `applied_settings` 快照消除该限制。


---

## 执行结果（2026-10-29）

- Task 1–7 全部完成；`go build ./...`、`go vet ./...`、`go test ./...` 通过。
- `go test -race ./internal/storage ./internal/bridge ./internal/desktop/...` 通过。
- 新增 6 个测试：storage round-trip、Update 待应用、派生刷新跳过/备份、Apply 备份验收、UI 徽章、仅保存提示。
- 未执行 git commit（工作区已有未提交改动，等待用户验收后统一处理）。
- 已知限制：current 配置待应用时，系统级刷新会跳过 live 并返回警告；用户需点击「应用」。后续可用 `applied_settings` 快照消除。
