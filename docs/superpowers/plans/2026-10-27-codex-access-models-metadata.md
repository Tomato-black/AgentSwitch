# Codex 接入多逻辑模型与元数据 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 Codex 接入配置支持有序多逻辑模型列表、逻辑模型元数据、子代理/自动审批、1M/压缩阈值与完整 config.toml 只读预览，同时保持 Codex 始终指向 AgentSwitch 网关。

**Architecture:** 逻辑模型是唯一事实源：`logical_models` 增元数据列（非破坏性 migration v2），Codex 接入配置的 `settings.models` 只存有序逻辑模型名。`writeCodexLive` 把「配置文档 + 逻辑模型元数据」投影成 `config.toml` 与 `agentswitch-model-catalog.json`；UI 只编辑逻辑模型列表与少量 Codex 客户端设置，不复制 cc-switch 的上游连接字段。

**Tech Stack:** Go 1.27、modernc.org/sqlite、pelletier/go-toml/v2、MyGo v0.6.2 原生视图。

**Spec:** `docs/superpowers/specs/2026-10-27-codex-access-models-metadata-design.md`

## Global Constraints

- 任务按 TDD：先写失败测试，跑失败，再写最小实现，跑通过；不要先写生产代码。
- 每个任务只做一个可独立验证的变更；复杂任务在本文件中已说明原因。
- 不提交 git，除非用户明确要求；计划中的 commit 步骤默认跳过。
- 生产代码不写日志或返回错误时包含令牌、完整 Prompt、Authorization。
- `~/.codex/auth.json` 仍然只读不写；Codex 只指向网关。
- 所有新增中文错误文案必须以 `请`/`需要`/`不能` 开头或包含明确动作，禁止裸英文。
- 现有 `go test ./...`、`go test -race ./internal/bridge ./internal/desktop ./internal/storage`、`go vet ./...`、`go build ./...` 必须保持通过。

---

## File Structure

**Storage**
- Modify: `internal/storage/storage.go`（`LogicalModel` 字段）
- Modify: `internal/storage/db.go`（migration v2、守卫）
- Modify: `internal/storage/repos.go`（列、扫描、序列化）
- Modify: `internal/storage/storage_test.go`（迁移/守卫/round-trip）

**Bridge**
- Modify: `internal/bridge/logicalmodels.go`（`ModelMeta`、`ModelView`、Create/Update 校验）
- Modify: `internal/bridge/accessconfigs.go`（Codex 文档解析、`writeCodexLive`、Project、Preview）
- Modify: `internal/bridge/codex_catalog.go`（显式模型列表 + 元数据 + auto-review）
- Create: `internal/bridge/codex_access_doc.go`（Codex 文档结构/校验辅助，避免 accessconfigs.go 继续膨胀）
- Tests: `internal/bridge/accessconfigs_test.go`、`codex_catalog_test.go`、新 `codex_access_doc_test.go`

**Desktop**
- Modify: `internal/desktop/logicalmodels.go`（元数据表单）
- Modify: `internal/desktop/access.go`（Codex 模型列表、高级设置、只读预览）
- Tests: `internal/desktop/logicalmodels_test.go`、`internal/desktop/access_test.go`

**Docs**
- Modify: `CLAUDE.md`、`README.md`、`CHANGELOG.md`
- Modify: `docs/superpowers/specs/2026-10-18-agent-access-config-design.md`、`2026-10-23-openai-responses-inbound-design.md`

---

### Task 1: Storage migration v2 + LogicalModel metadata

**Files:**
- Modify: `internal/storage/storage.go`（`LogicalModel`）
- Modify: `internal/storage/db.go`（migrationV2、migrations、rejectLegacySchema）
- Modify: `internal/storage/repos.go`（columns/scan/Save）
- Test: `internal/storage/storage_test.go`

**Interfaces:**
- Produces:
  ```go
  type LogicalModel struct {
      ID, Name, Description string
      DisplayName string
      ContextWindow int64
      ReasoningLevels []string
      DefaultReasoningLevel string
      Enabled bool
      CreatedAt, UpdatedAt int64
  }
  ```
- `logicalModelRepo.List/Get/GetByName/Save` 读写全部新字段。

- [ ] **Step 1: 写失败测试**（追加到 `storage_test.go`）

```go
func TestLogicalModelMetadataRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "agentswitch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	saved, err := s.LogicalModels().Save(LogicalModel{
		Name: "my-code", Enabled: true,
		DisplayName: "My Code", ContextWindow: 1000000,
		ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.LogicalModels().Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "My Code" || got.ContextWindow != 1000000 ||
		len(got.ReasoningLevels) != 2 || got.ReasoningLevels[1] != "high" ||
		got.DefaultReasoningLevel != "high" {
		t.Fatalf("metadata round trip = %+v", got)
	}
}
```

```go
func TestMigrationV2AddsLogicalModelColumns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(migrationV1); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
		INSERT INTO schema_migrations(version) VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("v1 库应就地升级到 v2: %v", err)
	}
	defer s.Close()
	for _, col := range []string{"display_name", "context_window", "reasoning_levels", "default_reasoning_level"} {
		ok, err := columnExists(s.(*dbStore).db, "logical_models", col)
		if err != nil || !ok {
			t.Fatalf("缺列 %s: %v", col, err)
		}
	}
}
```

- [ ] **Step 2: 跑失败测试**

Run: `go test ./internal/storage -run 'TestLogicalModelMetadataRoundTrip|TestMigrationV2AddsLogicalModelColumns' -v`
Expected: compile 失败（字段/列不存在）或 round-trip 失败。

- [ ] **Step 3: 实现**

`db.go`：

```go
const migrationV2 = `
ALTER TABLE logical_models ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE logical_models ADD COLUMN context_window INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logical_models ADD COLUMN reasoning_levels TEXT NOT NULL DEFAULT '';
ALTER TABLE logical_models ADD COLUMN default_reasoning_level TEXT NOT NULL DEFAULT '';
`

var migrations = []struct {
	version int64
	sql     string
}{
	{1, migrationV1},
	{2, migrationV2},
}
```

`rejectLegacySchema`：
- 去掉 `for _, legacyVersion := range []int64{2, 3}` 中 2 的拒绝；只拒绝 `3`。
- 若已记 v2，校验 `logical_models` 存在且有 `display_name` 列，否则 `errLegacyDB`。
- 更新 `TestOpenRejectsV3RecordedDB`：仍用 1,2,3，断言拒绝（3 仍拒）。
- 新增 `TestOpenRejectsV2RecordedWithoutColumns`（记 1,2 但 logical_models 无新列 → errLegacyDB）。

`storage.go` 加字段；`repos.go`：

```go
const logicalModelCols = `id,name,description,enabled,display_name,context_window,reasoning_levels,default_reasoning_level,created_at,updated_at`

func scanLogicalModel(sc func(...any) error) (LogicalModel, error) {
	var m LogicalModel
	var en int
	var levels string
	if err := sc(&m.ID, &m.Name, &m.Description, &en,
		&m.DisplayName, &m.ContextWindow, &levels, &m.DefaultReasoningLevel,
		&m.CreatedAt, &m.UpdatedAt); err != nil {
		return LogicalModel{}, err
	}
	m.Enabled = en == 1
	if levels != "" {
		if err := json.Unmarshal([]byte(levels), &m.ReasoningLevels); err != nil {
			return LogicalModel{}, err
		}
	}
	return m, nil
}
```

`Save` 序列化 `ReasoningLevels`：空 → `""`，否则 `json.Marshal`。`repos.go` 已引入 `encoding/json` 吗？没有则添加。

- [ ] **Step 4: 跑通过**

Run: `go test ./internal/storage -count=1`
Expected: PASS。

- [ ] **Step 5: 全量回归**

Run: `go test ./... -count=1`
Expected: PASS。

---

### Task 2: Bridge logical model metadata API

**Files:**
- Modify: `internal/bridge/logicalmodels.go`
- Test: `internal/bridge/logicalmodels_test.go`

**Interfaces:**
- Produces:
  ```go
  type ModelMeta struct {
      DisplayName string
      ContextWindow int64
      ReasoningLevels []string
      DefaultReasoningLevel string
  }
  type ModelView struct {
      ID, Name, Description string
      DisplayName string
      ContextWindow int64
      ReasoningLevels []string
      DefaultReasoningLevel string
      Enabled bool
      Targets []TargetView
  }
  func (s LogicalModels) Create(name, description string, meta ModelMeta, targets []TargetSpec) (ModelView, error)
  func (s LogicalModels) Update(id, name, description string, meta ModelMeta, enabled bool, targets []TargetSpec) error
  ```
- `validateModelMeta(meta ModelMeta) error`：白名单 `none,minimal,low,medium,high,xhigh,max`；默认等级必须在列表；`ContextWindow < 0` 拒绝。

- [ ] **Step 1: 写失败测试**

```go
func TestLogicalModelMetadataValidation(t *testing.T) {
	if err := validateModelMeta(ModelMeta{ReasoningLevels: []string{"low", "bogus"}}); err == nil {
		t.Fatal("未知思考等级应报错")
	}
	if err := validateModelMeta(ModelMeta{ReasoningLevels: []string{"low"}, DefaultReasoningLevel: "high"}); err == nil {
		t.Fatal("默认等级不在列表应报错")
	}
	if err := validateModelMeta(ModelMeta{ContextWindow: -1}); err == nil {
		t.Fatal("负上下文窗口应报错")
	}
	if err := validateModelMeta(ModelMeta{ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high"}); err != nil {
		t.Fatal(err)
	}
}

func TestLogicalModelCreatePersistsMeta(t *testing.T) {
	a := tempApp(t)
	ch, err := a.Store().Channels().Save(storage.Channel{
		Name: "c", Protocol: "anthropic", BaseURL: "http://u", CredentialRef: "ref",
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := LogicalModels{A: a}
	got, err := svc.Create("my-code", "", ModelMeta{
		DisplayName: "My Code", ContextWindow: 1000000,
		ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high",
	}, []TargetSpec{{ChannelID: ch.ID, ModelName: "up"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "My Code" || got.ContextWindow != 1000000 ||
		len(got.ReasoningLevels) != 2 || got.DefaultReasoningLevel != "high" {
		t.Fatalf("%+v", got)
	}
}
```

- [ ] **Step 2: 跑失败**

Run: `go test ./internal/bridge -run 'TestLogicalModelMetadataValidation|TestLogicalModelCreatePersistsMeta' -v`
Expected: compile 失败（ModelMeta/签名不存在）。

- [ ] **Step 3: 实现**

- 加 `ModelMeta`、`validateModelMeta`。
- `Create`/`Update` 加 `meta` 参数，`Save` 前校验并写入。
- `view` 映射元数据。
- 更新所有调用点（UI、测试）——此任务先改 bridge 测试；UI 在 Task 3 改，编译允许暂时失败，因此本任务需同时把 `internal/desktop/logicalmodels.go` 的调用改为 `bridge.ModelMeta{}` 占位，保持编译通过。

- [ ] **Step 4: 跑通过**

Run: `go test ./internal/bridge ./internal/desktop -run 'TestLogicalModel' -count=1`
Expected: PASS。

---

### Task 3: Desktop logical model metadata form

**Files:**
- Modify: `internal/desktop/logicalmodels.go`
- Test: `internal/desktop/logicalmodels_test.go`

**Interfaces:**
- `lmForm` 新增：`displayName string`、`contextWindow string`、`reasoningLevels string`、`defaultReasoningLevel string`。
- `func (f lmForm) meta() (bridge.ModelMeta, error)`：解析逗号分隔等级、去空格/去空项/去重，校验默认等级。
- `openLogicalEdit` 回填；`saveLogicalModel` 传 `meta()`；编辑页新增输入行。

- [ ] **Step 1: 写失败测试**

```go
func TestLmFormMetaParsesLevels(t *testing.T) {
	f := lmForm{reasoningLevels: " low, high ,low ", defaultReasoningLevel: "high"}
	meta, err := f.meta()
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.ReasoningLevels) != 2 || meta.ReasoningLevels[0] != "low" ||
		meta.ReasoningLevels[1] != "high" || meta.DefaultReasoningLevel != "high" {
		t.Fatalf("%+v", meta)
	}
	if _, err := (lmForm{reasoningLevels: "bogus"}).meta(); err == nil {
		t.Fatal("未知等级应报错")
	}
	if _, err := (lmForm{contextWindow: "abc"}).meta(); err == nil {
		t.Fatal("非数字上下文窗口应报错")
	}
}
```

- [ ] **Step 2: 跑失败**

Run: `go test ./internal/desktop -run TestLmFormMetaParsesLevels -v`
Expected: FAIL（meta 不存在）。

- [ ] **Step 3: 实现**

- 编辑页「基本信息」卡加：
  - 显示名 `TextInput`
  - 上下文窗口 `TextInput` + 「1M」按钮（点击设为 `"1000000"`）
  - 思考等级 `TextInput`（占位 `low,medium,high,xhigh`）
  - 默认等级 `Select`（选项由已填等级解析 + 空串「模板默认」）
- `meta()` 用 `strconv.ParseInt`，空串 = 0。
- 默认等级为空时允许；非空必须在等级列表中。
- 目标链逻辑不变。

- [ ] **Step 4: 跑通过**

Run: `go test ./internal/desktop -run 'TestLmForm|TestLogical' -count=1`
Expected: PASS。

---

### Task 4: Codex access doc structure (models/codex)

**Files:**
- Create: `internal/bridge/codex_access_doc.go`
- Modify: `internal/bridge/accessconfigs.go`（`parseAccessDoc` 放行键）
- Test: `internal/bridge/codex_access_doc_test.go`

**Interfaces:**
- Produces:
  ```go
  type codexAccessSettings struct {
      Models []string
      SubagentModel string
      SubagentReasoningEffort string
      AutoReviewModel string
  }
  func parseCodexSettings(doc map[string]any) (codexAccessSettings, error)
  func codexSettingsFromDoc(doc map[string]any) codexAccessSettings
  func codexModelsFromDoc(doc map[string]any) ([]string, bool)
  ```
- `parseAccessDoc("codex", raw)` 允许顶层 `env`、`models`、`codex`；其他键报 `Codex 配置仅支持 env、models、codex 键`。
- 结构校验：`models` 必须为字符串数组、非空、无重复；`codex` 只允许三个键，值必须是字符串；`subagent_reasoning_effort` 白名单同 Task 2。

- [ ] **Step 1: 写失败测试**

```go
func TestParseCodexSettings(t *testing.T) {
	doc := map[string]any{
		"models": []any{"my-code", "test"},
		"codex": map[string]any{
			"subagent_model": "test",
			"subagent_reasoning_effort": "high",
			"auto_review_model": "my-code",
		},
	}
	got, err := parseCodexSettings(doc)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Models) != 2 || got.Models[1] != "test" ||
		got.SubagentModel != "test" || got.AutoReviewModel != "my-code" {
		t.Fatalf("%+v", got)
	}
}

func TestParseCodexSettingsRejectsBadShapes(t *testing.T) {
	cases := []map[string]any{
		{"models": []any{"a", "a"}},
		{"models": []any{}},
		{"codex": map[string]any{"unknown": "x"}},
		{"codex": map[string]any{"subagent_reasoning_effort": "bogus"}},
	}
	for _, doc := range cases {
		if _, err := parseCodexSettings(doc); err == nil {
			t.Fatalf("应拒绝: %#v", doc)
		}
	}
}
```

- [ ] **Step 2: 跑失败**

Run: `go test ./internal/bridge -run 'TestParseCodexSettings' -v`
Expected: compile 失败。

- [ ] **Step 3: 实现**

`codex_access_doc.go` 提供解析/提取函数；`parseAccessDoc` 的 Codex 分支改为：

```go
if appType == "codex" {
	for k := range doc {
		if k != "env" && k != "models" && k != "codex" {
			return nil, nil, fmt.Errorf("Codex 配置仅支持 env、models、codex 键")
		}
	}
	if _, err := parseCodexSettings(doc); err != nil {
		return nil, nil, err
	}
}
```

- [ ] **Step 4: 跑通过**

Run: `go test ./internal/bridge -run 'TestParseCodexSettings' -count=1`
Expected: PASS。

---

### Task 5: Codex doc semantic validation + save fallback

**Files:**
- Modify: `internal/bridge/codex_access_doc.go`
- Modify: `internal/bridge/accessconfigs.go`（`prepareDoc`）
- Test: `internal/bridge/codex_access_doc_test.go` / `accessconfigs_test.go`

**Interfaces:**
- Produces:
  ```go
  func (s AccessConfigs) resolveCodexModels(bound string, settings codexAccessSettings) ([]string, error)
  ```
  规则：
  - `settings.Models` 为空 → `[bound] + 所有启用逻辑模型（按名排序，去重）`；
  - 非空 → 每个模型必须存在且 `Enabled`；第一个必须是 `bound`；
  - 返回有序、去重后的列表。
- `prepareDoc` 对 Codex：解析 → 归一化 env → `resolveCodexModels` → 把结果写回 `doc["models"]` → 校验 subagent/auto-review 必须在列表中 → 序列化。

- [ ] **Step 1: 写失败测试**

```go
func TestResolveCodexModelsFallbackAndValidation(t *testing.T) {
	a := tempApp(t)
	for _, lm := range []storage.LogicalModel{
		{ID: "lm1", Name: "my-code", Enabled: true},
		{ID: "lm2", Name: "test", Enabled: true},
		{ID: "lm3", Name: "off", Enabled: false},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	got, err := acc.resolveCodexModels("my-code", codexAccessSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "my-code" || got[1] != "test" {
		t.Fatalf("%v", got)
	}
	if _, err := acc.resolveCodexModels("my-code", codexAccessSettings{Models: []string{"test"}}); err == nil {
		t.Fatal("第一项不是绑定的默认模型应报错")
	}
	if _, err := acc.resolveCodexModels("my-code", codexAccessSettings{Models: []string{"my-code", "off"}}); err == nil {
		t.Fatal("停用模型应报错")
	}
}
```

- [ ] **Step 2: 跑失败**

Run: `go test ./internal/bridge -run TestResolveCodexModels -v`
Expected: compile 失败。

- [ ] **Step 3: 实现**

- `resolveCodexModels` 查询 `LogicalModels().List()`。
- `prepareDoc` Codex 分支：
  ```go
  if appType == "codex" {
      settings, err := parseCodexSettings(doc)
      if err != nil { return "", err }
      models, err := s.resolveCodexModels(modelName, settings)
      if err != nil { return "", err }
      doc["models"] = toAnyStrings(models)
      if settings.SubagentModel != "" && !slices.Contains(models, settings.SubagentModel) { error }
      if settings.AutoReviewModel != "" && !slices.Contains(models, settings.AutoReviewModel) { error }
      // env 归一化仍走原逻辑
  }
  ```
- 注意：`prepareDoc` 当前先 `parseAccessDoc`、再处理 Desktop、再 global/normalize。Codex 分支放在 parse 后。

- [ ] **Step 4: 跑通过**

Run: `go test ./internal/bridge -run 'TestResolveCodexModels|TestPrepare' -count=1`
Expected: PASS。

---

### Task 6: Catalog from explicit models + metadata + auto-review

**Files:**
- Modify: `internal/bridge/codex_catalog.go`
- Test: `internal/bridge/codex_catalog_test.go`

**Interfaces:**
- Produces:
  ```go
  type codexCatalogModel struct {
      Name string
      DisplayName string
      Description string
      ContextWindow int64
      ReasoningLevels []string
      DefaultReasoningLevel string
  }
  func (s AccessConfigs) codexCatalogModels(names []string) ([]codexCatalogModel, error)
  func buildCodexModelCatalog(template map[string]any, models []codexCatalogModel, autoReviewModel string) ([]byte, error)
  ```
- `buildCodexModelCatalog`：
  - 条目顺序 = models 顺序；`priority = 1000+i`。
  - `slug` = Name；`display_name` = DisplayName 空则 Name；`description` = Description 空则 `AgentSwitch 逻辑模型：<Name>`。
  - `ContextWindow > 0` → `context_window`/`max_context_window`。
  - `ReasoningLevels` 非空 → `supported_reasoning_levels = [{"effort": lvl, "description": lvl}]`；`default_reasoning_level` = DefaultReasoningLevel 或列表第一项。
  - `autoReviewModel != ""` → 每行 `auto_review_model_override`。
  - 删除 `upgrade`/`upgrade_info`/`availability_nux`。

- [ ] **Step 1: 写失败测试**

```go
func TestBuildCodexCatalogUsesMetadata(t *testing.T) {
	tpl := testCodexCatalogTemplate()
	b, err := buildCodexModelCatalog(tpl, []codexCatalogModel{
		{Name: "my-code", DisplayName: "My Code", Description: "主模型", ContextWindow: 1000000,
			ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high"},
		{Name: "test"},
	}, "my-code")
	if err != nil { t.Fatal(err) }
	var doc struct{ Models []map[string]any `json:"models"` }
	json.Unmarshal(b, &doc)
	m := doc.Models[0]
	if m["display_name"] != "My Code" || m["description"] != "主模型" ||
		m["context_window"] != float64(1000000) ||
		m["default_reasoning_level"] != "high" ||
		m["auto_review_model_override"] != "my-code" {
		t.Fatalf("%v", m)
	}
	levels := m["supported_reasoning_levels"].([]any)
	if len(levels) != 2 || levels[1].(map[string]any)["effort"] != "high" { t.Fatal(levels) }
	if doc.Models[1]["display_name"] != "test" { t.Fatal(doc.Models[1]) }
}
```

- [ ] **Step 2: 跑失败**

Run: `go test ./internal/bridge -run TestBuildCodexCatalogUsesMetadata -v`
Expected: compile 失败（签名不匹配）。

- [ ] **Step 3: 实现**

- 把现有 `buildCodexModelCatalog(template, []string)` 改为新签名；更新调用点（Task 7 会改）。
- `codexCatalogModels` 查询 `LogicalModels().List()`，按名索引；缺失/停用报错。
- 保留 `cloneCodexCatalogEntry`。
- 测试模板的 `testCodexCatalogTemplate` 已存在。

- [ ] **Step 4: 跑通过**

Run: `go test ./internal/bridge -run 'TestBuildCodexCatalog|TestCodexCatalog' -count=1`
Expected: PASS。

---

### Task 7A: Project base managed keys + write

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces:
  ```go
  func (s AccessConfigs) projectCodex(env map[string]string, model string, doc map[string]any) (cfgBytes, catalogBytes []byte, err error)
  func (s AccessConfigs) writeCodexLive(env map[string]string, model string, doc map[string]any) error
  ```
- `projectCodex`：解析 doc → models（缺失走 fallback）→ `codexCatalogModels` → `buildCodexModelCatalog`；读现有 config.toml，设置 `model`、`model_provider`、`model_catalog_json`、`disable_response_storage=true`、`web_search="disabled"`、provider 表。
- `writeCodexLive` 先写目录再写 config。

- [ ] **Step 1: 写失败测试**

```go
func TestProjectCodexBaseManagedKeys(t *testing.T) {
	a := tempApp(t)
	a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if err := acc.writeCodexLive(map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "tok"},
		"my-code", map[string]any{"models": []any{"my-code"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(acc.Home, ".codex", "config.toml"))
	var cfg map[string]any
	toml.Unmarshal(b, &cfg)
	if cfg["model"] != "my-code" || cfg["model_provider"] != "agentswitch" ||
		cfg["model_catalog_json"] != codexModelCatalogFileName ||
		cfg["disable_response_storage"] != true || cfg["web_search"] != "disabled" {
		t.Fatalf("%v", cfg)
	}
}
```

- [ ] **Step 2: 跑失败**：`go test ./internal/bridge -run TestProjectCodexBaseManagedKeys -v` → compile 失败。

- [ ] **Step 3: 实现**：抽出 `projectCodex`；`writeCodexLive` 调它；更新 `Apply`/`RegenerateDerived` 调用；所有直接测试调用补 `nil` doc。

- [ ] **Step 4: 跑通过**：`go test ./internal/bridge -count=1`。

---

### Task 7B: 1M context + compact threshold

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- `projectCodex` 在解析出 `catalogModels` 后，取 `catalogModels[0].ContextWindow`：
  - `>0` → `cfg["model_context_window"]=w`，`cfg["model_auto_compact_token_limit"]=w*9/10`；
  - `==0` → 不写也不删这两个键。

- [ ] **Step 1: 写失败测试**

```go
func TestProjectCodexContextWindow(t *testing.T) {
	a := tempApp(t)
	a.Store().LogicalModels().Save(storage.LogicalModel{
		ID: "lm1", Name: "my-code", Enabled: true, ContextWindow: 1000000,
	})
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if err := acc.writeCodexLive(map[string]string{"OPENAI_BASE_URL": "u", "OPENAI_API_KEY": "t"},
		"my-code", map[string]any{"models": []any{"my-code"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(acc.Home, ".codex", "config.toml"))
	var cfg map[string]any
	toml.Unmarshal(b, &cfg)
	if cfg["model_context_window"] != int64(1000000) ||
		cfg["model_auto_compact_token_limit"] != int64(900000) {
		t.Fatalf("%v", cfg)
	}
}
```

- [ ] **Step 2: 跑失败**：`go test ./internal/bridge -run TestProjectCodexContextWindow -v`。

- [ ] **Step 3: 实现**：在 `projectCodex` 写顶层窗口；`ContextWindow==0` 分支不触碰现有键。

- [ ] **Step 4: 跑通过**：`go test ./internal/bridge -run TestProjectCodexContextWindow -count=1`。

---

### Task 7C: [agents] subagent model/effort

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- `settings := codexSettingsFromDoc(doc)`；`settings.SubagentModel/Effort` 非空写 `cfg["agents"].default_subagent_model` / `default_subagent_reasoning_effort`；为空删除对应键（保留 `[agents]` 其他键；表空则删表）。

- [ ] **Step 1: 写失败测试**

```go
func TestProjectCodexSubagentKeys(t *testing.T) {
	a := tempApp(t)
	a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	doc := map[string]any{"models": []any{"my-code"}, "codex": map[string]any{
		"subagent_model": "my-code", "subagent_reasoning_effort": "high",
	}}
	if err := acc.writeCodexLive(map[string]string{"OPENAI_BASE_URL": "u", "OPENAI_API_KEY": "t"},
		"my-code", doc); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(acc.Home, ".codex", "config.toml"))
	var cfg map[string]any
	toml.Unmarshal(b, &cfg)
	agents := cfg["agents"].(map[string]any)
	if agents["default_subagent_model"] != "my-code" ||
		agents["default_subagent_reasoning_effort"] != "high" {
		t.Fatalf("%v", agents)
	}
}
```

- [ ] **Step 2: 跑失败**：`go test ./internal/bridge -run TestProjectCodexSubagentKeys -v`。

- [ ] **Step 3: 实现**：写/删 `[agents]` 两个受管键。

- [ ] **Step 4: 跑通过**：`go test ./internal/bridge -run TestProjectCodexSubagentKeys -count=1`。

---

### Task 8: PreviewCodexConfig bridge method

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces:
  ```go
  func (s AccessConfigs) PreviewCodexConfig(logicalModelID, settingsJSON string) (string, error)
  ```
- 逻辑：Get 逻辑模型 → `parseAccessDoc("codex")` → `GlobalConfig` → `composeAccessEnv` → `projectCodex` → 返回 `string(cfgBytes)`；不写盘。
- 错误中文：逻辑模型不存在、文档非法、模板缺失。

- [ ] **Step 1: 写失败测试**

```go
func TestPreviewCodexConfigDoesNotWrite(t *testing.T) {
	a := tempApp(t)
	lm, _ := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	got, err := acc.PreviewCodexConfig(lm.ID, `{"models":["my-code"]}`)
	if err != nil { t.Fatal(err) }
	if !strings.Contains(got, `model = "my-code"`) || !strings.Contains(got, "model_catalog_json") {
		t.Fatalf("%s", got)
	}
	if _, err := os.Stat(filepath.Join(acc.Home, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatal("预览不应写 live 文件")
	}
}
```

- [ ] **Step 2: 跑失败**

Run: `go test ./internal/bridge -run TestPreviewCodexConfigDoesNotWrite -v`
Expected: FAIL/compile。

- [ ] **Step 3: 实现**

- `PreviewCodexConfig` 按上述流程；`projectCodex` 读现有文件（不存在建空）。
- 令牌用 `Gateway{A:s.A}.Status().Token`（composeAccessEnv 已带）。
- 不写目录、不写 config。

- [ ] **Step 4: 跑通过**

Run: `go test ./internal/bridge -run TestPreviewCodexConfig -count=1`
Expected: PASS。

---

### Task 9: Desktop Codex model list helpers

**Files:**
- Modify: `internal/desktop/access.go`
- Test: `internal/desktop/access_test.go`

**Interfaces:**
- `accessForm` 新增 `codexModels []string`、`subagentModel string`、`subagentEffort string`、`autoReviewModel string`、`codexPreview string`。
- Produces:
  ```go
  func accessDocSetModels(raw string, models []string) (string, error)
  func accessDocSetCodex(raw string, subagentModel, subagentEffort, autoReview string) (string, error)
  func (u *uiState) accessSyncCodexFromDoc(models []bridge.ModelView)
  func (u *uiState) accessPatchCodexModels()
  func (u *uiState) accessPatchCodexSettings()
  ```
- `accessSyncCodexFromDoc`：读 `docJSON.models`；缺失 → `[modelLabel] + enabled labels`；同时回填 `subagentModel/subagentEffort/autoReviewModel`。
- `accessPatchCodexModels`：把 `codexModels` 写回 `docJSON.models`，并令 `modelLabel = codexModels[0]`。

- [ ] **Step 1: 写失败测试**

```go
func TestAccessDocSetModelsRoundTrip(t *testing.T) {
	raw, err := accessDocSetModels(`{"env":{}}`, []string{"my-code", "test"})
	if err != nil { t.Fatal(err) }
	doc := map[string]any{}
	json.Unmarshal([]byte(raw), &doc)
	got := doc["models"].([]any)
	if len(got) != 2 || got[0] != "my-code" { t.Fatal(raw) }
}

func TestAccessSyncCodexFallback(t *testing.T) {
	u := &uiState{}
	u.accessForm = accessForm{docJSON: `{"env":{}}`, modelLabel: "my-code"}
	u.accessSyncCodexFromDoc([]bridge.ModelView{
		{Name: "my-code", Enabled: true}, {Name: "test", Enabled: true}, {Name: "off"},
	})
	if len(u.accessForm.codexModels) != 2 || u.accessForm.codexModels[0] != "my-code" {
		t.Fatalf("%v", u.accessForm.codexModels)
	}
}
```

- [ ] **Step 2: 跑失败**

Run: `go test ./internal/desktop -run 'TestAccessDocSetModels|TestAccessSyncCodex' -v`
Expected: compile 失败。

- [ ] **Step 3: 实现**

- 复用 `accessDocSetEnv` 的 JSON 解析/序列化模式。
- `accessDocSetCodex` 只写非空字段；全空时删除 `codex` 键。
- `accessSyncCodexFromDoc` 解析失败保留旧状态（同角色回填模式）。

- [ ] **Step 4: 跑通过**

Run: `go test ./internal/desktop -run 'TestAccessDocSetModels|TestAccessSyncCodex' -count=1`
Expected: PASS。

---

### Task 10A: Desktop Codex model list editor

**Files:**
- Modify: `internal/desktop/access.go`
- Test: `internal/desktop/access_test.go`

**Interfaces:**
- Codex 编辑器不再显示单一「逻辑模型」下拉，改为模型列表（第一行默认）。
- 保存：`modelID` 取 `codexModels[0]` 对应 ID；空列表报错。

- [ ] **Step 1: 写失败测试**

```go
func TestAccessCodexEditorShowsModelList(t *testing.T) {
	u, a := testUI(t)
	a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	u.page = "access"
	u.accessApp = "codex"
	u.accessForm = accessForm{open: true, modelLabel: "my-code", docJSON: `{"env":{}}`}
	tt := ui.NewTester(u.view, 1000, 900)
	for _, want := range []string{"默认", "模型列表"} {
		if !tt.HasText(want) { t.Fatalf("missing %q in %v", want, tt.Texts()) }
	}
}
```

- [ ] **Step 2: 跑失败**：`go test ./internal/desktop -run TestAccessCodexEditorShowsModelList -v` → FAIL。

- [ ] **Step 3: 实现**
  - `accessEditorCard` 按 `u.accessApp` 分支：`claude` 保留现有单选 + 角色映射；`codex` 渲染模型列表行（Select + 上移/下移/删除），底部添加；第一行加「默认」badge。
  - 列表变化调用 `accessPatchCodexModels`；保存时 `accessSaveForm` 对 Codex 用 `codexModels[0]`。

- [ ] **Step 4: 跑通过**：`go test ./internal/desktop -run 'TestAccessCodex|TestAccessPage' -count=1`。

---

### Task 10B: Desktop Codex advanced settings

**Files:**
- Modify: `internal/desktop/access.go`
- Test: `internal/desktop/access_test.go`

**Interfaces:**
- 高级设置卡：子代理模型下拉（「跟随主模型」+ 模型名）、子代理推理强度下拉（默认/low/medium/high/xhigh）、自动审批模型下拉（「主模型自己审批」+ 模型名）。
- 变化调用 `accessPatchCodexSettings`，再 `accessRefreshCodexPreview(acc)`。

- [ ] **Step 1: 写失败测试**

```go
func TestAccessCodexEditorShowsAdvancedSettings(t *testing.T) {
	u, a := testUI(t)
	a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	u.page = "access"
	u.accessApp = "codex"
	u.accessForm = accessForm{open: true, modelLabel: "my-code", docJSON: `{"env":{}}`}
	tt := ui.NewTester(u.view, 1000, 900)
	for _, want := range []string{"子代理模型", "子代理推理强度", "自动审批模型"} {
		if !tt.HasText(want) { t.Fatalf("missing %q in %v", want, tt.Texts()) }
	}
}
```

- [ ] **Step 2: 跑失败**：`go test ./internal/desktop -run TestAccessCodexEditorShowsAdvancedSettings -v`。

- [ ] **Step 3: 实现**：渲染三个下拉，绑定 `subagentModel/subagentEffort/autoReviewModel`；空值使用「跟随主模型」/「默认」/「主模型自己审批」。

- [ ] **Step 4: 跑通过**：`go test ./internal/desktop -run TestAccessCodexEditorShowsAdvancedSettings -count=1`。

---

### Task 11: Desktop read-only config.toml preview

**Files:**
- Modify: `internal/desktop/access.go`
- Test: `internal/desktop/access_test.go`

**Interfaces:**
- `func (u *uiState) accessRefreshCodexPreview(acc bridge.AccessConfigs)`：用 `codexModels[0]` 映射 ID，调 `PreviewCodexConfig`，成功写 `codexPreview`，失败写 `msg`。
- 编辑器 Codex 分支渲染「将写入的 config.toml（只读）」+ `ui.TextArea(c, &u.accessForm.codexPreview).Height(240).ReadOnly(true)`。

- [ ] **Step 1: 写失败测试**

```go
func TestAccessCodexPreviewRendersManagedKeys(t *testing.T) {
	u, a := testUI(t)
	a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	u.page = "access"; u.accessApp = "codex"
	u.accessForm = accessForm{open: true, modelLabel: "my-code", codexModels: []string{"my-code"}, docJSON: `{"env":{},"models":["my-code"]}`}
	u.accessRefreshCodexPreview(u.accessSvc())
	if !strings.Contains(u.accessForm.codexPreview, "model_catalog_json") ||
		!strings.Contains(u.accessForm.codexPreview, "disable_response_storage") {
		t.Fatalf("%s", u.accessForm.codexPreview)
	}
}
```

- [ ] **Step 2: 跑失败**

Run: `go test ./internal/desktop -run TestAccessCodexPreview -v`
Expected: compile 失败。

- [ ] **Step 3: 实现**

- 预览在打开编辑/列表变化/高级设置变化时刷新。
- 文本预览不包含用户其他配置？Spec 要求完整 config.toml；`PreviewCodexConfig` 基于当前文件合并，故包含。
- 令牌真实显示（现有 JSON 编辑器同款）。

- [ ] **Step 4: 跑通过**

Run: `go test ./internal/desktop -run TestAccessCodexPreview -count=1`
Expected: PASS。

---

### Task 12: Update existing tests/fixtures and error paths

**Files:**
- Modify: `internal/bridge/accessconfigs_test.go`、`codex_catalog_test.go`、`internal/desktop/access_test.go`、`internal/gateway/codex_e2e_test.go`
- Test: all

- [ ] **Step 1: 更新旧断言**
  - `writeCodexLive` 调用加 `nil` doc。
  - Catalog 测试改用 `codexCatalogModel`/新签名。
  - `ModelView` 初始化加元数据字段的零值不破坏。
  - UI 旧「逻辑模型:」断言改为 Codex 模型列表；Claude 保持。
  - e2e 夹具补 `model_catalog_json` 与 `disable_response_storage`/`web_search`。
- [ ] **Step 2: 跑全量**

Run: `go test ./... -count=1`
Expected: PASS。

---

### Task 13: Docs and spec revisions

**Files:**
- Modify: `CLAUDE.md`（Codex 桌面版段落）
- Modify: `README.md`（接入详情/功能特性）
- Modify: `CHANGELOG.md`（Codex 多模型/元数据/高级设置）
- Modify: `docs/superpowers/specs/2026-10-18-agent-access-config-design.md`（Codex 行/修订注）
- Modify: `docs/superpowers/specs/2026-10-23-openai-responses-inbound-design.md`（§3.5）
- Test: docs diff 自查

- [ ] **Step 1: 更新文档**
  - 模型列表存每个 Codex 接入配置，第一项默认；元数据在逻辑模型页；migration v2；子代理/自动审批；1M；兼容键；只读 config.toml。
- [ ] **Step 2: 检查无旧表述**
  - `grep -rn "auth.json\\|Codex CLI\\|单个逻辑模型" README.md CLAUDE.md docs/superpowers/specs/2026-10-*.md`，按需修正。

---

### Task 14: Full verification and package/install

- [ ] **Step 1: 全量验证**

```bash
go test ./... -count=1
go test -race ./internal/bridge ./internal/desktop ./internal/storage -count=1
go vet ./...
go build ./...
```

- [ ] **Step 2: 打包安装（仅用户要求时）**

```bash
go tool mygo build
sh build/linux-amd64/install.sh
sh scripts/linux-desktop.sh
```

不重启 AgentSwitch（正在承载会话）。

---

## Self-Review

- **Spec coverage**：模型列表、元数据、1M/压缩、子代理、自动审批、web_search/disable_response_storage、只读 config.toml、migration v2 均有任务。
- **Placeholder scan**：无 TBD；测试/实现步骤均给出可执行片段。
- **Type consistency**：`ModelMeta`、`codexAccessSettings`、`codexCatalogModel`、`writeCodexLive(env, model, doc)`、`PreviewCodexConfig` 在任务间一致。
- **Known implementation risk**：Task 2 会临时改 UI 调用点；Task 7 会改 `writeCodexLive` 签名并需要更新所有调用；执行时按任务顺序，保持每步可编译。
