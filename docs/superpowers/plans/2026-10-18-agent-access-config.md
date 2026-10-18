# Agent 接入配置管理 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 接入页从「片段复制」升级为「多配置管理 + 应用即写入 Agent 真实配置文件 + 持久文件/启动命令/打开终端」。

**Architecture:** storage 新增 `access_configs` 表（migration v3）承载每 Agent（claude/codex）的多实际配置与 `is_current`；全局配置存 settings KV。bridge 新增 `AccessConfigs`：合成 env（网关基础键 > 全局 > 覆盖项）→ 备份 → 受管键投影写入 `~/.claude/settings.json` 或 `~/.codex/config.toml`+`auth.json`；同时为每个配置生成持久文件并给出启动命令。desktop 重写接入页。旧 `bridge.Access` 片段生成删除。

**Tech Stack:** Go 1.27.2、modernc.org/sqlite、MyGo v0.5.1（原生视图）、新增依赖 `github.com/pelletier/go-toml/v2`（Codex config.toml 读写；语义保留、注释可丢——用户已批准）。

**Spec:** `docs/superpowers/specs/2026-10-18-agent-access-config-design.md`（计划论据均出自该文档；执行者两份都读）。

## Global Constraints

- Go 1.27.2；依赖只允许新增 `github.com/pelletier/go-toml/v2`，其余不加。
- 涉及并发/状态更新的测试必须 `go test -race ./...` 口径通过；每任务验证命令写明，最后统一全量。
- 所有用户可见文案（错误、提示、按钮）用中文；日志审计行沿用 `actor="ui"` `result="ok"` 惯例。
- **网关令牌（GatewayToken）绝不写入日志**；含令牌的生成文件权限 0600。
- `app_type` 合法值仅 `"claude"` 与 `"codex"`（白名单校验）。
- 桥接与文件路径全部经 `AccessConfigs.Home`（默认 `os.UserHomeDir()`，测试注入 `t.TempDir()`）——不得在业务代码里直接散写 `~`。
- 遵守 CLAUDE.md 任务纪律：勿改已发布 migration 语句；`internal/router` 不受波及。

## Review Focus

以下五类失败模式最可能咬到用户，各由所属任务的测试钉住：

1. **基础键被全局/覆盖项篡改**（如 overrides 写 `ANTHROPIC_BASE_URL` 指向外部地址，流量绕过网关）→ Task 4 `TestComposeAccessEnvBaseKeysImmutable`。
2. **非法 live 文件被静默重建**（用户 settings.json 手工改坏，应用后被覆盖丢失线索）→ Task 7 `TestClaudeLiveProjectionInvalidJSONAborts`、Task 9 `TestCodexLiveProjectionInvalidTOMLAborts`。
3. **同 app 出现两个「使用中」**（is_current 非原子更新）→ Task 3 `TestAccessConfigRepoSetCurrentExclusive`。
4. **删除使用中配置后 live 文件被清空/回滚**（应保持最后状态）→ Task 10 `TestRemoveCurrentKeepsLiveUntouched`。
5. **令牌轮换后文件不刷新**（旧令牌残留，Agent 401）→ Task 12 `TestGatewayRegenerateTokenRefreshesDerivedFiles`。

---

### Task 1: storage——migration v3 + AccessConfig 类型 + Repo 接口

**Files:**
- Modify: `internal/storage/db.go`（migrations 列表尾部追加 v3；仿 migrationV2 写法）
- Modify: `internal/storage/storage.go`（类型 + 接口 + Store 方法）
- Test: `internal/storage/storage_test.go`

**Interfaces:**
- Produces: `storage.AccessConfig` 结构体；`storage.AccessConfigRepo` 接口；`Store.AccessConfigs()`。Task 2 实现，后续全部消费。

- [ ] **Step 1: 写失败测试**

```go
func TestMigrationV3CreatesAccessConfigs(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "v3.db"))
	if err != nil { t.Fatal(err) }
	defer s.Close()
	if s.AccessConfigs() == nil { t.Fatal("AccessConfigs() = nil") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/storage -run TestMigrationV3CreatesAccessConfigs -v`
Expected: FAIL（`s.AccessConfigs undefined`）

- [ ] **Step 3: 实现**

`db.go` 追加（仿 v2）：

```go
const migrationV3 = `
CREATE TABLE access_configs (
  id TEXT PRIMARY KEY,
  app_type TEXT NOT NULL,
  name TEXT NOT NULL,
  logical_model_id TEXT NOT NULL,
  overrides TEXT NOT NULL DEFAULT '{}',
  is_current INTEGER NOT NULL DEFAULT 0,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_access_configs_app_name ON access_configs(app_type, name);
CREATE UNIQUE INDEX idx_access_configs_app_current ON access_configs(app_type) WHERE is_current = 1;
`
```

`migrations` 追加 `{3, migrationV3}`。

`storage.go` 追加：

```go
type AccessConfig struct {
	ID             string
	AppType            string
	Name               string
	LogicalModelID     string
	Overrides          map[string]string
	IsCurrent          bool
	SortOrder          int
	CreatedAt, UpdatedAt int64
}
type AccessConfigRepo interface {
	ListByApp(appType string) ([]AccessConfig, error)
	Get(id string) (AccessConfig, error)
	Save(c AccessConfig) (AccessConfig, error)
	Delete(id string) error
	SetCurrent(appType, id string) error // id="" 仅清空该 app 的 current；否则事务内清 app 全部再置 id（id 必须存在）
	Current(appType string) (AccessConfig, error) // 无 → ErrNotFound
}
```

`Store` 接口加 `AccessConfigs() AccessConfigRepo`。`dbStore` 加桩方法 `func (s *dbStore) AccessConfigs() AccessConfigRepo { return &accessConfigRepo{s.db} }`（repo 类型 Task 2 才有实现体——本任务允许先放 `panic("TODO")` 桩，Step 4 只需编译过）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/storage -run TestMigrationV3CreatesAccessConfigs -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/db.go internal/storage/storage.go internal/storage/storage_test.go
git commit -m "feat(storage): migration v3 建 access_configs 与 Repo 接口"
```

---

### Task 2: storage——repo Save/Get/ListByApp/Delete

**Files:**
- Modify: `internal/storage/repos.go`（实现 accessConfigRepo）
- Test: `internal/storage/storage_test.go`

**Interfaces:**
- Consumes: Task 1 的 `AccessConfig`/`AccessConfigRepo`。
- Produces: Save 的行为契约：`ID==""` 生成 NewID；同 (app_type,name) UNIQUE 冲突时 error 冒泡；`Overrides` 为 nil 时存 `{}`；Get 未命中返回 `ErrNotFound`。

- [ ] **Step 1: 写失败测试**

```go
func TestAccessConfigRepoSaveGetListDelete(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "a.db"))
	defer s.Close()
	r := s.AccessConfigs()
	saved, err := r.Save(AccessConfig{AppType: "claude", Name: "工作", LogicalModelID: "lm1",
		Overrides: map[string]string{"HTTPS_PROXY": "http://127.0.0.1:7890"}})
	if err != nil { t.Fatal(err) }
	if saved.ID == "" || saved.CreatedAt == 0 { t.Fatal("ID/CreatedAt 未填充") }
	got, err := r.Get(saved.ID)
	if err != nil { t.Fatal(err) }
	if got.Overrides["HTTPS_PROXY"] != "http://127.0.0.1:7890" { t.Fatalf("overrides 未回读: %#v", got.Overrides) }
	if _, err := r.Save(AccessConfig{AppType: "claude", Name: "工作", LogicalModelID: "lm2"}); err == nil {
		t.Fatal("同 app 同名应 UNIQUE 冲突")
	}
	list, err := r.ListByApp("claude")
	if err != nil || len(list) != 1 { t.Fatalf("ListByApp = %v, %v", list, err) }
	if err := r.Delete(saved.ID); err != nil { t.Fatal(err) }
	if _, err := r.Get(saved.ID); !errors.Is(err, ErrNotFound) { t.Fatalf("Get after delete = %v", err) }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/storage -run TestAccessConfigRepoSaveGetListDelete -v`
Expected: FAIL（nil map 调用或未实现）

- [ ] **Step 3: 实现**

`repos.go` 仿 `channelRepo` 写 `accessConfigRepo`：列序 `id,app_type,name,logical_model_id,overrides,is_current,sort_order,created_at,updated_at`；`overrides` 经 `json.Marshal/Unmarshal`；`ListByApp` 按 `sort_order ASC, name ASC`；Save 复用 channelRepo 的 insert-or-update 模式（含 CreatedAt 保持）。错误不做友好化（bridge 层做）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/storage -run TestAccessConfigRepoSaveGetListDelete -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/repos.go internal/storage/storage_test.go
git commit -m "feat(storage): access_configs repo 基础读写"
```

---

### Task 3: storage——SetCurrent 原子性 + Current

**Files:**
- Modify: `internal/storage/repos.go`
- Test: `internal/storage/storage_test.go`

**Interfaces:**
- Produces: `SetCurrent(appType, id)` 事务语义：同 app_type 既有 current 全清后置新 id；`id==""` 只清空。`Current` 未命中返回 `ErrNotFound`。

- [ ] **Step 1: 写失败测试**

```go
func TestAccessConfigRepoSetCurrentExclusive(t *testing.T) {
	s, _ := Open(filepath.Join(t.TempDir(), "a.db"))
	defer s.Close()
	r := s.AccessConfigs()
	a, _ := r.Save(AccessConfig{AppType: "claude", Name: "A", LogicalModelID: "lm1"})
	b, _ := r.Save(AccessConfig{AppType: "claude", Name: "B", LogicalModelID: "lm1"})
	c, _ := r.Save(AccessConfig{AppType: "codex", Name: "C", LogicalModelID: "lm1"})
	if err := r.SetCurrent("claude", a.ID); err != nil { t.Fatal(err) }
	if err := r.SetCurrent("claude", b.ID); err != nil { t.Fatal(err) }
	cur, err := r.Current("claude")
	if err != nil || cur.ID != b.ID { t.Fatalf("Current = %v, %v", cur, err) }
	all, _ := r.ListByApp("claude")
	n := 0
	for _, x := range all { if x.IsCurrent { n++ } }
	if n != 1 { t.Fatalf("is_current 行数 = %d，应为 1", n) }
	if cur, _ := r.Current("codex"); cur.ID != c.ID { t.Fatal("codex current 不应被 claude 操作波及") }
	if err := r.SetCurrent("claude", ""); err != nil { t.Fatal(err) }
	if _, err := r.Current("claude"); !errors.Is(err, ErrNotFound) { t.Fatal("清空后 Current 应 ErrNotFound") }
	if err := r.SetCurrent("claude", "nonexistent"); err == nil { t.Fatal("不存在的 id 应报错") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/storage -run TestAccessConfigRepoSetCurrentExclusive -v`
Expected: FAIL（未实现）

- [ ] **Step 3: 实现**

`SetCurrent`：`Begin` 事务内 `UPDATE access_configs SET is_current=0 WHERE app_type=?`；id 非空时先 `SELECT` 校验存在（不存在回滚返回 error），再 `UPDATE ... SET is_current=1 WHERE id=?`；提交。`Current`：`SELECT ... WHERE app_type=? AND is_current=1`，`sql.ErrNoRows` → `ErrNotFound`。

- [ ] **Step 4: 跑测试确认通过 + 并发口径**

Run: `go test -race ./internal/storage -run TestAccessConfigRepoSetCurrentExclusive -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/repos.go internal/storage/storage_test.go
git commit -m "feat(storage): access_configs SetCurrent 事务原子切换"
```

---

### Task 4: bridge——composeAccessEnv（claude）与基础键不可覆盖

**Files:**
- Create: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Consumes: `bridge.Status`（`Gateway.Status()` 返回值，字段 `Port int`、`Token string`——已在 `bridge.go` 存在）。
- Produces: `type AccessConfigs struct { A *app.App; Home string }`；纯函数 `composeAccessEnv(appType string, st Status, modelName string, global, overrides map[string]string) (map[string]string, error)`——Task 5/10 复用。`Home` 为 `""` 时业务方法内回落 `os.UserHomeDir()`（本任务只建结构体，不实现路径方法）。

- [ ] **Step 1: 写失败测试**

```go
func TestComposeAccessEnvClaudeMergePrecedence(t *testing.T) {
	st := Status{Port: 17890, Token: "tok123"}
	env, err := composeAccessEnv("claude", st, "my-code",
		map[string]string{"HTTPS_PROXY": "http://p:1"},
		map[string]string{"ANTHROPIC_SMALL_FAST_MODEL": "fast"})
	if err != nil { t.Fatal(err) }
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:17890" { t.Fatal(env["ANTHROPIC_BASE_URL"]) }
	if env["ANTHROPIC_AUTH_TOKEN"] != "tok123" { t.Fatal("token 未带入") }
	if env["ANTHROPIC_MODEL"] != "my-code" || env["ANTHROPIC_SMALL_FAST_MODEL"] != "fast" { t.Fatal(env) }
	if env["HTTPS_PROXY"] != "http://p:1" { t.Fatal("全局 env 未合并") }
}

func TestComposeAccessEnvBaseKeysImmutable(t *testing.T) {
	st := Status{Port: 17890, Token: "tok123"}
	env, err := composeAccessEnv("claude", st, "m",
		map[string]string{"ANTHROPIC_BASE_URL": "http://evil:1"},
		map[string]string{"ANTHROPIC_AUTH_TOKEN": "x", "ANTHROPIC_MODEL": "evil"})
	if err != nil { t.Fatal(err) }
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:17890" { t.Fatal("基础键被全局覆盖") }
	if env["ANTHROPIC_AUTH_TOKEN"] != "tok123" || env["ANTHROPIC_MODEL"] != "m" { t.Fatal("基础键被 overrides 覆盖") }
}

func TestComposeAccessEnvRejectsUnknownAppType(t *testing.T) {
	if _, err := composeAccessEnv("gemini", Status{}, "m", nil, nil); err == nil { t.Fatal("白名单外 appType 应报错") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run 'TestComposeAccessEnv' -v`
Expected: FAIL（undefined: composeAccessEnv）

- [ ] **Step 3: 实现**

`accessconfigs.go`：`AccessConfigs` 结构体 + `composeAccessEnv`。语义（spec「配置合成规则」）：先置基础键（claude 四键 / codex 两键：`OPENAI_BASE_URL=http://127.0.0.1:<port>/v1`、`OPENAI_API_KEY=<token>`），再覆盖 global，再覆盖 overrides，**最后重新写回基础键**（保证不可篡改）。appType 不在 {claude,codex} 返回中文错误。modelName 为空返回错误「请先选择逻辑模型」。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bridge -run 'TestComposeAccessEnv' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/accessconfigs_test.go
git commit -m "feat(bridge): 接入配置 env 合成与基础键不可篡改"
```

---

### Task 5: bridge——逻辑模型名解析 + codex 合成出口

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: `func (s AccessConfigs) composeFor(c storage.AccessConfig) (env map[string]string, model string, err error)`——读 `s.A.Store().LogicalModels().Get(c.LogicalModelID)` 取 Name（未命中返回中文错误「绑定的逻辑模型不存在，请改绑」），读 settings `access.global.<appType>` 解析 JSON 为 global，调 `composeAccessEnv`。codex 时 `model` 返回逻辑模型名（供 config.toml `model` 键与启动命令），env 不含模型键。

- [ ] **Step 1: 写失败测试**

```go
func TestComposeForCodexResolvesModelAndErrorsOnMissing(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	// 建一个逻辑模型（经 storage 直插，避开 bridge 依赖环）
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil { t.Fatal(err) }
	_ = a.Store().Settings().Set("access.global.codex", `{"HTTPS_PROXY":"http://p:1"}`)
	env, model, err := acc.composeFor(storage.AccessConfig{AppType: "codex", LogicalModelID: "lm1"})
	if err != nil { t.Fatal(err) }
	if model != "my-code" { t.Fatal(model) }
	if env["OPENAI_BASE_URL"] == "" || env["OPENAI_API_KEY"] == "" || env["HTTPS_PROXY"] != "http://p:1" { t.Fatal(env) }
	if _, _, err := acc.composeFor(storage.AccessConfig{AppType: "codex", LogicalModelID: "missing"}); err == nil {
		t.Fatal("缺失逻辑模型应报错")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run TestComposeForCodex -v`
Expected: FAIL（undefined: composeFor）

- [ ] **Step 3: 实现**

见 Interfaces。global 读取失败/JSON 非法直接返回错误；Settings Get 缺失返回 `("", nil)` → 视为无全局配置。global JSON 解到 `map[string]string`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bridge -run TestComposeForCodex -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/accessconfigs_test.go
git commit -m "feat(bridge): composeFor 解析逻辑模型名与全局配置"
```

---

### Task 6: bridge——CRUD + 全局配置读写

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces（UI 消费的视图与方法，签名即接线契约）:

```go
type AccessConfigView struct {
	ID, Name, AppType, LogicalModelID, LogicalModelName string
	Overrides map[string]string
	IsCurrent bool
}
func (s AccessConfigs) List(appType string) []AccessConfigView
func (s AccessConfigs) Create(appType, name, logicalModelID string, overrides map[string]string) (AccessConfigView, error)
func (s AccessConfigs) Update(id, name, logicalModelID string, overrides map[string]string) error
func (s AccessConfigs) Remove(id string)
func (s AccessConfigs) GlobalConfig(appType string) (map[string]string, error)
func (s AccessConfigs) SaveGlobalConfig(appType string, env map[string]string) error
```

- [ ] **Step 1: 写失败测试**

```go
func TestAccessConfigsCRUDAndView(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil { t.Fatal(err) }
	v, err := acc.Create("claude", "工作", "lm1", map[string]string{"A": "1"})
	if err != nil { t.Fatal(err) }
	if v.LogicalModelName != "my-code" { t.Fatal(v.LogicalModelName) }
	if _, err := acc.Create("claude", "工作", "lm1", nil); err == nil { t.Fatal("重名应报中文错误") }
	if err := acc.Update(v.ID, "个人", "lm1", nil); err != nil { t.Fatal(err) }
	if len(acc.List("claude")) != 1 || acc.List("claude")[0].Name != "个人" { t.Fatal(acc.List("claude")) }
	acc.Remove(v.ID)
	if len(acc.List("claude")) != 0 { t.Fatal("删除未生效") }
}

func TestAccessConfigsGlobalConfig(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if err := acc.SaveGlobalConfig("claude", map[string]string{"K": "V"}); err != nil { t.Fatal(err) }
	g, err := acc.GlobalConfig("claude")
	if err != nil || g["K"] != "V" { t.Fatalf("%v %v", g, err) }
	if err := acc.SaveGlobalConfig("gemini", nil); err == nil { t.Fatal("白名单外 appType 应报错") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run 'TestAccessConfigsCRUD|TestAccessConfigsGlobal' -v`
Expected: FAIL（undefined）

- [ ] **Step 3: 实现**

仿 `Channels` 服务：Create 校验 appType 白名单、name 非空、logicalModelID 存在；name UNIQUE 冲突译成「配置名已存在」；审计行 `acc.A.Log().Info("access config created", "id", id, "app", appType, "actor", "ui", "result", "ok")`（**绝不记录 env 值与 token**）。`Remove` 直接删行（is_current 行删除后 current 自然消失——部分唯一索引允许）。Global 存 settings 键 `"access.global."+appType`。Update 语义：`Remove`+`Create` 之和不做——直接 repo.Save 保 ID。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bridge -run 'TestAccessConfigsCRUD|TestAccessConfigsGlobal' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/accessconfigs_test.go
git commit -m "feat(bridge): 接入配置 CRUD 与全局配置"
```

---

### Task 7: bridge——Claude live 投影（settings.json 受管键合并）

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: `func (s AccessConfigs) writeClaudeLive(env map[string]string) error`——写 `<home>/.claude/settings.json`：文件不存在建空对象；存在则解析为 `map[string]any`，**解析失败返回错误不写**；确保 `env` 子对象存在；只替换四个受管键；其余键值原样保留；0600 写回（临时文件+rename 原子写）。

- [ ] **Step 1: 写失败测试**

```go
func TestClaudeLiveProjectionPreservesAndMerges(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"allow":["Bash"]},"env":{"FOO":"bar","ANTHROPIC_MODEL":"old"}}`), 0o600)
	if err := acc.writeClaudeLive(map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:17890", "ANTHROPIC_MODEL": "new"}); err != nil { t.Fatal(err) }
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	var got map[string]any
	json.Unmarshal(b, &got)
	env := got["env"].(map[string]any)
	if env["ANTHROPIC_MODEL"] != "new" || env["FOO"] != "bar" { t.Fatal(string(b)) }
	if got["permissions"] == nil { t.Fatal("非受管键丢失") }
}

func TestClaudeLiveProjectionInvalidJSONAborts(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{broken`), 0o600)
	if err := acc.writeClaudeLive(map[string]string{"ANTHROPIC_MODEL": "x"}); err == nil { t.Fatal("非法 JSON 应中止") }
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(b) != "{broken" { t.Fatal("失败后不得改动原文件") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run TestClaudeLive -v`
Expected: FAIL（undefined）

- [ ] **Step 3: 实现**

见 Interfaces。`os.MkdirAll(dir, 0o755)` 建目录；写入用 `os.CreateTemp` 同目录 → chmod 0600 → write → close → `os.Rename`。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bridge -run TestClaudeLive -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/accessconfigs_test.go
git commit -m "feat(bridge): claude settings.json 受管键投影"
```

---

### Task 8: bridge——应用前备份与滚动清理

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: `func (s AccessConfigs) backupLive(appType string)`——对 claude 备份 `settings.json`、codex 备份 `config.toml`+`auth.json` 到 `<home>/.agentswitch/backups/<appType>/<UnixMilli>-<文件名>`；源文件不存在则跳过该文件；每 appType 目录只保留最新 10 份（按文件名时间戳前缀倒序删多余）。任何备份失败**中止后续写入由调用方负责**——故签名改为 `func (s AccessConfigs) backupLive(appType string) error`。

- [ ] **Step 1: 写失败测试**

```go
func TestBackupLiveCopiesAndRotates(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"a":1}`), 0o600)
	for i := 0; i < 12; i++ {
		if err := acc.backupLive("claude"); err != nil { t.Fatal(err) }
		time.Sleep(2 * time.Millisecond) // 时间戳文件名去重
	}
	bdir := filepath.Join(acc.Home, ".agentswitch", "backups", "claude")
	ents, _ := os.ReadDir(bdir)
	if len(ents) != 10 { t.Fatalf("备份份数 = %d，应为 10", len(ents)) }
	b, err := os.ReadFile(filepath.Join(bdir, ents[0].Name()))
	if err != nil || string(b) != `{"a":1}` { t.Fatalf("%v %s", err, b) }
}

func TestBackupLiveSkipsMissingSource(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	if err := acc.backupLive("claude"); err != nil { t.Fatal(err) } // 无源文件不报错、不建目录
	if _, err := os.Stat(filepath.Join(acc.Home, ".agentswitch", "backups", "claude")); !os.IsNotExist(err) { t.Fatal("不应创建备份目录") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run TestBackupLive -v`
Expected: FAIL（undefined）

- [ ] **Step 3: 实现**

见 Interfaces。文件名 `strconv.FormatInt(nowMS(),10)+"-"+原名`。滚动：ReadDir 后按名排序（时间戳前缀等长，字典序即时序）从旧到新删到剩 10。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bridge -run TestBackupLive -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/accessconfigs_test.go
git commit -m "feat(bridge): 应用前 live 文件备份与滚动清理"
```

---

### Task 9: bridge——Codex live 投影（config.toml + auth.json）

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Dependencies:** `go get github.com/pelletier/go-toml/v2`（本任务 Step 3 内执行并随代码提交 go.mod/go.sum）。

**Interfaces:**
- Produces: `func (s AccessConfigs) writeCodexLive(env map[string]string, model string) error`——
  - `~/.codex/config.toml`：不存在建空；存在解析 `map[string]any`，**解析失败中止**；设置 `model=<model>`、`model_provider="agentswitch"`、`model_providers.agentswitch = {base_url: env["OPENAI_BASE_URL"], wire_api: "chat", requires_openai_auth: false}`；其余键语义保留；0600 原子写。
  - `~/.codex/auth.json`：不存在建空对象；解析失败中止；设置 `OPENAI_API_KEY = env["OPENAI_API_KEY"]`；其余键保留；0600 原子写。

- [ ] **Step 1: 写失败测试**

```go
func TestCodexLiveProjectionPreservesAndMerges(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("preferred_auth_method = \"apikey\"\n\n[mcp_servers.foo]\ncommand = \"mcp\"\n"), 0o600)
	if err := acc.writeCodexLive(map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "tok"}, "my-code"); err != nil { t.Fatal(err) }
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	var got map[string]any
	if err := toml.Unmarshal(b, &got); err != nil { t.Fatal(err) }
	if got["model"] != "my-code" || got["model_provider"] != "agentswitch" { t.Fatal(string(b)) }
	if got["mcp_servers"] == nil { t.Fatal("非受管段丢失") }
	mp := got["model_providers"].(map[string]any)["agentswitch"].(map[string]any)
	if mp["wire_api"] != "chat" || mp["requires_openai_auth"] != false { t.Fatal(mp) }
	ab, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	var auth map[string]any
	json.Unmarshal(ab, &auth)
	if auth["OPENAI_API_KEY"] != "tok" { t.Fatal(string(ab)) }
}

func TestCodexLiveProjectionInvalidTOMLAborts(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = "), 0o600)
	if err := acc.writeCodexLive(map[string]string{"OPENAI_API_KEY": "x"}, "m"); err == nil { t.Fatal("非法 TOML 应中止") }
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if string(b) != "model = " { t.Fatal("失败后不得改动") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run TestCodexLive -v`
Expected: FAIL（undefined）

- [ ] **Step 3: 实现**

`go get github.com/pelletier/go-toml/v2` 后实现 writeCodexLive（两个文件各自读-改-写；auth.json 先于 config.toml 写，或反之——选「任一失败即中止且不写第二个」的顺序：先写 config.toml 前先完成两个文件的解析与合并，**两个文件都解析成功后才落盘**）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bridge -run TestCodexLive -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/accessconfigs_test.go go.mod go.sum
git commit -m "feat(bridge): codex config.toml/auth.json 受管键投影"
```

---

### Task 10: bridge——Apply 与 Remove 语义

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: `func (s AccessConfigs) Apply(id string) error`——Get → composeFor → `backupLive` → 按 appType 写 live → `SetCurrent`；任一步失败即停。审计 `access config applied`。
  `Remove`（Task 6 已建，本任务补语义）：删行即可；**不触碰任何 live 文件**。

- [ ] **Step 1: 写失败测试**

```go
func TestApplyWritesLiveAndSetsCurrent(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil { t.Fatal(err) }
	v, err := acc.Create("claude", "工作", "lm1", nil)
	if err != nil { t.Fatal(err) }
	if err := acc.Apply(v.ID); err != nil { t.Fatal(err) }
	b, err := os.ReadFile(filepath.Join(acc.Home, ".claude", "settings.json"))
	if err != nil { t.Fatal(err) }
	if !strings.Contains(string(b), "ANTHROPIC_MODEL") || !strings.Contains(string(b), "my-code") { t.Fatal(string(b)) }
	cur, err := a.Store().AccessConfigs().Current("claude")
	if err != nil || cur.ID != v.ID { t.Fatalf("%v %v", cur, err) }
	if _, err := os.Stat(filepath.Join(acc.Home, ".agentswitch", "backups", "claude")); err != nil { t.Fatal("应用前应有备份") }
}

func TestRemoveCurrentKeepsLiveUntouched(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil { t.Fatal(err) }
	v, _ := acc.Create("claude", "工作", "lm1", nil)
	if err := acc.Apply(v.ID); err != nil { t.Fatal(err) }
	before, _ := os.ReadFile(filepath.Join(acc.Home, ".claude", "settings.json"))
	acc.Remove(v.ID)
	after, _ := os.ReadFile(filepath.Join(acc.Home, ".claude", "settings.json"))
	if string(before) != string(after) { t.Fatal("删除使用中配置不得改动 live 文件") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run 'TestApply|TestRemoveCurrent' -v`
Expected: FAIL（undefined: Apply）

- [ ] **Step 3: 实现**

见 Interfaces。Apply 中 composeFor 的逻辑模型缺失错误直接透传。备份失败同样中止（不写 live、不置 current）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bridge -run 'TestApply|TestRemoveCurrent' -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/accessconfigs_test.go
git commit -m "feat(bridge): Apply 合成-备份-写入-current 一步完成"
```

---

### Task 11: bridge——持久文件生成与 LaunchCommand

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces:
  - `func (s AccessConfigs) writePersist(c storage.AccessConfig) (claudePath, codexPath string, err error)`——按 appType 只生成对应一种：claude → `<home>/.agentswitch/access/claude/<id>.json`，内容 `{"env":{合成结果}}`，0600；codex → `<home>/.agentswitch/access/codex/<id>.env`，内容 `KEY=VALUE` 行（键按字典序；**任一值含换行符即报错中止**——`.env` 行格式无法安全表达），0600。返回该 appType 对应路径、另一种为空串。
  - `func (s AccessConfigs) LaunchCommand(id string) (string, error)`——Get → 确保持久文件存在（不存在即生成；存在则**先重写刷新**再返回）→ 返回展示命令：
    - claude：`claude --settings "<绝对路径>"`
    - codex：`sh -c 'set -a; . "<绝对路径>"; set +a; exec codex -m "<model>"'`

- [ ] **Step 1: 写失败测试**

```go
func TestWritePersistAndLaunchCommand(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil { t.Fatal(err) }
	cv, _ := acc.Create("claude", "工作", "lm1", nil)
	cmd, err := acc.LaunchCommand(cv.ID)
	if err != nil { t.Fatal(err) }
	p := filepath.Join(acc.Home, ".agentswitch", "access", "claude", cv.ID+".json")
	if !strings.Contains(cmd, p) || !strings.HasPrefix(cmd, "claude --settings ") { t.Fatal(cmd) }
	b, _ := os.ReadFile(p)
	var payload struct{ Env map[string]string `json:"env"` }
	if err := json.Unmarshal(b, &payload); err != nil { t.Fatal(err) }
	if payload.Env["ANTHROPIC_MODEL"] != "my-code" || payload.Env["ANTHROPIC_AUTH_TOKEN"] == "" { t.Fatal(string(b)) }
	fi, _ := os.Stat(p); if fi.Mode().Perm() != 0o600 { t.Fatalf("权限 = %v", fi.Mode().Perm()) }

	xv, _ := acc.Create("codex", "x", "lm1", nil)
	cmd2, err := acc.LaunchCommand(xv.ID)
	if err != nil { t.Fatal(err) }
	if !strings.Contains(cmd2, "codex -m \"my-code\"") || !strings.Contains(cmd2, ".env") { t.Fatal(cmd2) }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run TestWritePersistAndLaunchCommand -v`
Expected: FAIL（undefined）

- [ ] **Step 3: 实现**

见 Interfaces。LaunchCommand 每次调用都重写持久文件（保证令牌/模型最新——与 spec「令牌联动」一致，任务 12 的批量版复用 writePersist）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bridge -run TestWritePersistAndLaunchCommand -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/accessconfigs_test.go
git commit -m "feat(bridge): 持久配置文件生成与启动命令"
```

---

### Task 12: bridge——令牌/端口变更联动 RegenerateDerived

**Files:**
- Modify: `internal/bridge/accessconfigs.go`（新增 `RegenerateDerived`）
- Modify: `internal/bridge/bridge.go`（`Gateway.RegenerateToken`、`Gateway.SaveSettings` 成功后挂钩）
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Consumes: Task 10 `Apply`、Task 11 `writePersist`。
- Produces: `func (s AccessConfigs) RegenerateDerived()`——遍历两个 appType 的全部配置重写持久文件；对 `Current(appType)` 命中的配置额外重写 live（失败只记 Error 日志，不 panic）。`bridge.Gateway.RegenerateToken` 成功后、`SaveSettings` 返回 `restart==true`（端口变更）后调用 `AccessConfigs{A: g.A}.RegenerateDerived()`。

- [ ] **Step 1: 写失败测试**

```go
func TestGatewayRegenerateTokenRefreshesDerivedFiles(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil { t.Fatal(err) }
	v, _ := acc.Create("claude", "工作", "lm1", nil)
	if err := acc.Apply(v.ID); err != nil { t.Fatal(err) }
	oldTok, _ := a.Store().Settings().Get("auth.gateway_token")
	p := filepath.Join(acc.Home, ".agentswitch", "access", "claude", v.ID+".json")
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), oldTok) { t.Fatal("前置：旧令牌应在持久文件中") }
	if _, err := (Gateway{A: a}).RegenerateToken(); err != nil { t.Fatal(err) }
	b2, _ := os.ReadFile(p)
	newTok, _ := a.Store().Settings().Get("auth.gateway_token")
	if !strings.Contains(string(b2), newTok) || strings.Contains(string(b2), oldTok) { t.Fatal("持久文件未刷新令牌") }
	live, _ := os.ReadFile(filepath.Join(acc.Home, ".claude", "settings.json"))
	if !strings.Contains(string(live), newTok) { t.Fatal("live 文件未刷新令牌") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run TestGatewayRegenerateTokenRefreshesDerivedFiles -v`
Expected: FAIL（RegenerateToken 不触发刷新）

- [ ] **Step 3: 实现**

见 Interfaces。挂钩处各一行调用；`RegenerateDerived` 内部错误走 `s.A.Log().Error("access derived refresh failed", "app", appType, "err", err)`（err 可能含路径，**不得含 token 值**——实现时留意 fmt 错误串不带 env）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test -race ./internal/bridge -run TestGatewayRegenerateTokenRefreshesDerivedFiles -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/bridge.go internal/bridge/accessconfigs_test.go
git commit -m "feat(bridge): 令牌/端口变更自动刷新接入派生文件"
```

---

### Task 13: bridge——OpenTerminal（macOS）

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: 包级变量 `var openTerminalExec = func(script string) error`（默认实现 `exec.Command("osascript", "-e", script).Run()`）；`func (s AccessConfigs) OpenTerminal(id string) error`——Get → `LaunchCommand(id)`（顺带刷新持久文件）→ 组装 osascript 脚本 `tell application "Terminal" to do script "<cmd 转义 \" 与 \>"` → `openTerminalExec`。测试替换该变量断言脚本内容，不真开终端。

- [ ] **Step 1: 写失败测试**

```go
func TestOpenTerminalAssemblesScript(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil { t.Fatal(err) }
	v, _ := acc.Create("claude", "工作", "lm1", nil)
	var got string
	old := openTerminalExec
	openTerminalExec = func(script string) error { got = script; return nil }
	defer func() { openTerminalExec = old }()
	if err := acc.OpenTerminal(v.ID); err != nil { t.Fatal(err) }
	if !strings.Contains(got, `tell application "Terminal" to do script "claude --settings`) { t.Fatal(got) }
	if err := acc.OpenTerminal("missing"); err == nil { t.Fatal("不存在的配置应报错") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/bridge -run TestOpenTerminalAssemblesScript -v`
Expected: FAIL（undefined）

- [ ] **Step 3: 实现**

见 Interfaces。cmd 内双引号转义为 `\"`（AppleScript 语义）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/bridge -run TestOpenTerminalAssemblesScript -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/accessconfigs.go internal/bridge/accessconfigs_test.go
git commit -m "feat(bridge): 用此配置打开终端（osascript）"
```

---

### Task 14: desktop——接入页骨架：Agent Tab + 配置列表 + 全局配置卡

**Files:**
- Modify: `internal/desktop/access.go`（整体重写；`snippetBg`/`maskSnippetLines`/`pickAccessModel`/`accessModelKey` 等旧助手删除——若其他文件引用则一并处理，预期无）
- Modify: `internal/desktop/shell.go`（如需 uiState 字段挂载则同步）
- Test: `internal/desktop/access_test.go`（重写旧用例中被删助手的测试）

**Interfaces:**
- Consumes: Task 6 `AccessConfigs.List/Create/GlobalConfig/SaveGlobalConfig`、`AccessConfigView`。
- Produces: `uiState` 增加字段 `accessApp string`（tab 记忆，内存态即可，默认 `"claude"`）与 `accessGlobalDraft map[string]string` 等编辑态字段（实现者按现有 channels 页编辑态惯例取名，保持本文件内自洽）。`accessPage` 结构：

```
Column:
  Tab 行（两枚 softButton："Claude Code" / "Codex CLI"，选中态区分，点击换 accessApp）
  全局配置卡：键值行列表（每行两个单行输入 + 删除钮）+「添加一项」+「保存」→ SaveGlobalConfig
  配置列表卡：遍历 List(accessApp)；每行 = 名称（IsCurrent 加「使用中」徽标文本）+ 逻辑模型名 + 按钮「应用」「编辑」「复制启动命令」+ ⋯菜单（「用此配置打开终端」「删除」）
  空列表提示：「还没有配置——点击下方新建」；无 current 提示条：「当前没有配置被应用，Agent 仍使用其原配置文件」
  底部「新建配置」按钮（打开编辑态）
```

- [ ] **Step 1: 写失败测试（纯助手层）**

```go
func TestAccessTabTitles(t *testing.T) {
	if a, b := accessTabTitles(); a != "Claude Code" || b != "Codex CLI" { t.Fatal(a, b) }
}

func TestAccessConfigRowShowsCurrentBadge(t *testing.T) {
	got := accessRowBadge(true)
	if got != "使用中" { t.Fatal(got) }
	if accessRowBadge(false) != "" { t.Fatal("非 current 应无徽标") }
}
```

（`accessTabTitles`/`accessRowBadge` 为本任务新增纯助手；其余 UI 组合代码不设单测，走 `go build` + 现有 desktop 测试不回归。）

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestAccessTabTitles|TestAccessConfigRow' -v`
Expected: FAIL（undefined）

- [ ] **Step 3: 实现**

重写 `accessPage` 见 Interfaces 的结构。数据经 `acc := bridge.AccessConfigs{A: u.svc.Gateway.A}` 每帧构造（与旧页同惯例）。按钮行为（本任务接线）：应用 → `acc.Apply(id)`；复制启动命令 → `acc.LaunchCommand(id)` → `mygo.Clipboard.Write`；打开终端 → `acc.OpenTerminal(id)`；删除 → `acc.Remove(id)`；全局保存 → `acc.SaveGlobalConfig`。错误经现有页面级错误展示惯例内联红字（若有）或 `A.Log().Error`。UI 控件复用 `card`/`softButton`/`ui.Column`/`ui.Text`/`ui.Select`/`monoFont` 等现有助手。

- [ ] **Step 4: 跑测试确认通过 + 全 desktop 回归**

Run: `go test ./internal/desktop -v && go build ./...`
Expected: PASS（含既有 desktop 测试；`internal/desktop/access_test.go` 中旧助手用例随删除同步移除）

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/access.go internal/desktop/access_test.go internal/desktop/shell.go
git commit -m "feat(desktop): 接入页改版——agent tab 与配置列表"
```

---

### Task 15: desktop——配置编辑器（新建/编辑）与错误提示

**Files:**
- Modify: `internal/desktop/access.go`
- Test: `internal/desktop/access_test.go`

**Interfaces:**
- Consumes: Task 6 `Create/Update`、`bridge.LogicalModels.List()`（下拉数据源）。
- Produces: 编辑态结构（本文件私有）：`editing bool`、`editID string`（空=新建）、`editName string`、`editModelID string`、`editOverrides [][2]string`、`editErr string`。编辑器卡渲染在列表卡下方：名称输入、逻辑模型 `ui.Select`（选项=enabled 模型名，映射回 ID）、覆盖项键值行（默认折叠——一个「高级：覆盖项」开关文本，点击展开）、`保存`/`取消`。保存 → Create/Update → 成功清编辑态，失败 `editErr` 显示中文错误。纯助手：

```go
func accessOverridesFromRows(rows [][2]string) map[string]string // 剔除空键/空值
func accessModelIDByLabel(models []bridge.ModelView, label string) string
```

- [ ] **Step 1: 写失败测试**

```go
func TestAccessOverridesFromRows(t *testing.T) {
	got := accessOverridesFromRows([][2]string{{"K", "V"}, {"", "x"}, {"K2", ""}})
	if len(got) != 1 || got["K"] != "V" { t.Fatal(got) }
}

func TestAccessModelIDByLabel(t *testing.T) {
	ms := []bridge.ModelView{{ID: "id1", Name: "m1", Enabled: true}, {ID: "id2", Name: "m2"}}
	if accessModelIDByLabel(ms, "m2") != "id2" { t.Fatal("应命中含 disabled 项") }
	if accessModelIDByLabel(ms, "nope") != "" { t.Fatal("未命中应空串") }
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/desktop -run 'TestAccessOverrides|TestAccessModelID' -v`
Expected: FAIL（undefined）

- [ ] **Step 3: 实现**

见 Interfaces；编辑器 UI 组合仿 channels 页表单惯例。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/desktop -v && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/access.go internal/desktop/access_test.go
git commit -m "feat(desktop): 接入配置编辑器表单"
```

---

### Task 16: 清理旧片段代码 + 文档更新 + 全量验证

**Files:**
- Delete: `internal/bridge/access.go`、`internal/bridge/access_test.go`
- Modify: `internal/desktop/settings.go`（若引用了将删内容则处理——预期无；执行时 grep 确认）
- Modify: `CLAUDE.md`（「当前形态」段落补一句接入页能力与 access_configs；删除片段页描述若有）
- Modify: `README.md`（首次使用流程中的「复制环境变量」步骤改为「接入页新建并应用配置」）
- Test: 全量

- [ ] **Step 1: 确认无残留引用**

Run: `grep -rn "bridge.Access\|AccessSnippet\|ClaudeCodeSnippet\|CodexSnippet\|maskSnippetLines\|pickAccessModel\|accessModelKey" internal/ --include='*.go' | grep -v accessconfigs`
Expected: 仅剩将删文件自身（含测试）

- [ ] **Step 2: 删除并修复编译**

删除 `internal/bridge/access.go` 与 `access_test.go`；`go build ./...` 通过。

- [ ] **Step 3: 更新 CLAUDE.md 与 README.md**

CLAUDE.md 项目状态段追加：「接入页（2026-10-18）：每 Agent（Claude Code/Codex CLI）多实际配置 + 全局配置，应用即受管键投影写入 agent 真实文件（spec `docs/superpowers/specs/2026-10-18-agent-access-config-design.md`）；`access_configs` 表 migration v3。」README 首次使用流程同步改为新接入步骤。

- [ ] **Step 4: 全量验证**

Run: `go vet ./... && go build ./... && go test -race ./...`
Expected: 全部通过，无新增告警

**手动验收（spec 验收场景，需本机已装对应 Agent；Codex 未装则记录跳过原因）：** ①UI 新建 claude 配置并应用 → 检查 `~/.claude/settings.json` 四受管键与其余键保留；②「复制启动命令」在干净终端跑通；③codex 配置同样走一遍（config.toml+auth.json+启动命令）；④改全局配置重应用后 live/持久文件含全局 env；⑤重新生成令牌后两类文件自动刷新。

- [ ] **Step 5: Commit**

```bash
git add -A
git commit -m "refactor: 删除旧接入片段页，接入配置管理落地"
```

---

## 依赖关系

- Task 1→2→3（storage 内部顺序）；Task 4→5→6→(7,8,9)→10→11→12→13（bridge 依赖链；7/8/9 相互独立可换序，10 需要 4–9 全部就位）。
- Task 14→15（desktop）；Task 16 收尾依赖 13 与 15。
- 每个任务仅依赖前序任务的 Interfaces 块所列产物，可独立执行与验证。
