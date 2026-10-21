# 接入配置完整 settings 文档化（cc-switch 式编辑器）实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **2026-10-21 修订**：本计划中「令牌遮蔽为 `__GATEWAY_TOKEN__`」的全局约束与
> `PreviewDoc`/桌面测试条目已被用户裁定取代——预览与编辑器默认渲染真实网关
> 令牌（保存路径仍剔除锁定键、令牌永不落库）。以 spec §3.4 修订版为准。

**Goal:** 接入配置从「env 覆盖项」升级为「完整 settings JSON 文档」，编辑器重做为 cc-switch 式「角色映射表 + 配置 JSON 双向同步」，网关三键锁定、令牌遮蔽、`[1M]` 后缀路由剥离。

**Architecture:** 删库重建（schema 合并单一 v1，`access_configs.settings` 列取代 `overrides`）；bridge 层新增文档解析/保存归一化/合成预览（`PreviewDoc` 令牌遮蔽）；写 live 升级为「env 合成合并 + 文档顶层键逐键写入」；router 入口剥离 `[1M]`；desktop 编辑器卡以 JSON 文本为唯一共享存储、表单为双向同步视图。

**Tech Stack:** Go 1.27.2、modernc.org/sqlite、MyGo（`github.com/egoist/mygo` v0.5.1，`ui.TextArea`/`ui.Select`/`ui.Checkbox`）。无新依赖。

**Spec:** `docs/superpowers/specs/2026-10-19-access-full-settings-ccswitch-design.md`（实现依据；执行者需同时阅读 spec 与本计划）

## Global Constraints

- Go 1.27.2；不新增任何依赖。
- 所有面向用户的错误文案为中文。
- 密钥/令牌永不落日志、永不直接展示于 UI（预览中遮蔽为 `"__GATEWAY_TOKEN__"`）。
- 文件写入：0600、同目录临时文件原子 rename；live 文件先读后写、解析失败绝不改动原文件。
- 测试全部离线（httptest Fake Provider），不打真实上游。
- 涉及并发/路由的测试带 `-race`。
- 每个任务结束时 `go build ./...` 与相关包测试全绿。
- 桥接方法审计行只记 id/app/actor/result，绝不记 env 值或令牌。
- MyGo UI API 事实（已核实 v0.5.1）：`Element.Changed() bool`、`Element.Clicked() bool`、`Element.Width(float32)`、`Element.Height(float32)`、`Element.Grow(float32)`、`Element.Placeholder(string)` 均存在；`ui.TextArea(c, *string) Element`、`ui.Select(c, *string, []string) Element`、`ui.Checkbox(c, *bool, string) Element`。

---

### Task 1: storage 删库重建——单一 v1 + access_configs.settings

**说明（复杂任务，超出 2–5 分钟默认）：** schema DDL、repo 列读写、`AccessConfig` 结构体与 bridge 引用点必须同落一个任务，否则树无法编译或测试全红；这是不可再拆的最小原子变更。bridge 层本任务只做**行为等价的机械适配**（对外签名不变），文档语义在后续任务引入。

**Files:**
- Modify: `internal/storage/db.go`（migrationV1 合并、删 migrationV2/V3、守卫扩展）
- Modify: `internal/storage/storage.go:58-68`（`AccessConfig` 字段）
- Modify: `internal/storage/repos.go:359-445`（scan/save/cols）
- Modify: `internal/bridge/accessconfigs.go`（`view`/`composeFor`/`Create`/`Update` 机械适配）
- Modify: `internal/storage/storage_test.go`、`internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: `storage.AccessConfig.Settings string`（整份 JSON 文本，默认 `"{}"`）；`bridge.accessDocEnvMap(settings string) (map[string]string, error)`（从文档提取 env 字符串 map）。
- 消费方：后续所有任务。

- [ ] **Step 1: 写失败测试（storage 守卫与 round-trip）**

在 `internal/storage/storage_test.go` 追加（沿用文件内既有 `TestOpenRejectsLegacySchema` 的建库方式——直接 `sql.Open("sqlite", ...)` 手工建旧库再调 `Open`；先读该测试的辅助写法照抄结构）：

```go
func TestOpenRejectsV3RecordedDB(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentswitch.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
		INSERT INTO schema_migrations(version) VALUES (1),(2),(3)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); !errors.Is(err, errLegacyDB) {
		t.Fatalf("v2/v3 记录库应拒绝，got %v", err)
	}
}

func TestOpenRejectsAccessConfigsMissingSettingsColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agentswitch.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟 v3 时代库：schema_migrations 只记 1，但 access_configs 是旧 overrides 形态
	if _, err := db.Exec(`CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY);
		INSERT INTO schema_migrations(version) VALUES (1);
		CREATE TABLE channels (id TEXT PRIMARY KEY, credential_ref TEXT NOT NULL);
		CREATE TABLE access_configs (id TEXT PRIMARY KEY, overrides TEXT NOT NULL DEFAULT '{}')`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); !errors.Is(err, errLegacyDB) {
		t.Fatalf("无 settings 列的 access_configs 应拒绝，got %v", err)
	}
}

func TestAccessConfigSettingsRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "agentswitch.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	doc := `{"env":{"A":"1"},"mcpServers":{"x":{"command":"c"}}}`
	saved, err := s.AccessConfigs().Save(AccessConfig{AppType: "claude", Name: "n", LogicalModelID: "lm", Settings: doc})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.AccessConfigs().Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Settings != doc {
		t.Fatalf("settings 应原样回读: %s", got.Settings)
	}
	// 空 Settings 存取归一化为 "{}"
	empty, err := s.AccessConfigs().Save(AccessConfig{AppType: "claude", Name: "e", LogicalModelID: "lm"})
	if err != nil {
		t.Fatal(err)
	}
	got2, _ := s.AccessConfigs().Get(empty.ID)
	if got2.Settings != "{}" {
		t.Fatalf("空 settings 应为 {}: %s", got2.Settings)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/storage -run 'TestOpenRejectsV3RecordedDB|TestOpenRejectsAccessConfigsMissingSettingsColumn|TestAccessConfigSettingsRoundTrip' -v`
Expected: FAIL（`errLegacyDB` 未拒绝 v3 / `Settings` 字段不存在）

- [ ] **Step 3: 改 schema——单一 v1 与守卫**

`internal/storage/db.go`：把 `migrationV1` 替换为合并后的单一 schema（原 V1+V2+V3 全量，`access_configs.overrides` 改 `settings`）：

```go
// migrationV1 是删库重建后的单一 schema（spec 2026-10-19-access-full-settings）：
// 合并原 v1/v2/v3，access_configs 以 settings 整份文档列取代 overrides。
// 用户决策（2026-10-19）：删库重建——旧库启动报错提示删一次库，不就地迁移。
const migrationV1 = `
CREATE TABLE channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  protocol TEXT NOT NULL,
  base_url TEXT NOT NULL,
  credential_ref TEXT NOT NULL,
  preset_id TEXT NOT NULL DEFAULT '',
  allow_private_upstream INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE channel_models (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  model_name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE(channel_id, model_name)
);
CREATE TABLE channel_secrets (
  credential_ref TEXT PRIMARY KEY,
  ciphertext BLOB NOT NULL
);
CREATE TABLE logical_models (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE logical_model_targets (
  id TEXT PRIMARY KEY,
  logical_model_id TEXT NOT NULL REFERENCES logical_models(id) ON DELETE CASCADE,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  model_name TEXT NOT NULL,
  priority INTEGER NOT NULL,
  UNIQUE(logical_model_id, channel_id, model_name)
);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
CREATE TABLE access_configs (
  id TEXT PRIMARY KEY,
  app_type TEXT NOT NULL,
  name TEXT NOT NULL,
  logical_model_id TEXT NOT NULL,
  settings TEXT NOT NULL DEFAULT '{}',
  is_current INTEGER NOT NULL DEFAULT 0,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_access_configs_app_name ON access_configs(app_type, name);
CREATE UNIQUE INDEX idx_access_configs_app_current ON access_configs(app_type) WHERE is_current = 1;
`
```

删除 `migrationV2`、`migrationV3` 常量；`migrations` 表只剩 `{1, migrationV1}`。

`rejectLegacySchema` 在既有检查后追加（放在 `hasCredRef` 检查之后）：

```go
	// 删库重建（2026-10-19）：v2/v3 记录的库与旧形态 access_configs 一律拒绝。
	for _, legacyVersion := range []int64{2, 3} {
		ok, err := hasRecordedVersion(db, legacyVersion)
		if err != nil {
			return err
		}
		if ok {
			return errLegacyDB
		}
	}
	hasAccess, err := tableExists(db, "access_configs")
	if err != nil {
		return err
	}
	if hasAccess {
		hasSettings, err := columnExists(db, "access_configs", "settings")
		if err != nil {
			return err
		}
		if !hasSettings {
			return errLegacyDB
		}
	}
	return nil
```

- [ ] **Step 4: 改结构体与 repo**

`internal/storage/storage.go:58-68`：

```go
type AccessConfig struct {
	ID             string
	AppType        string
	Name           string
	LogicalModelID string
	Settings       string // 整份 JSON 文档（spec 2026-10-19）；空读写归一化为 "{}"
	IsCurrent      bool
	SortOrder      int
	CreatedAt      int64
	UpdatedAt      int64
}
```

`internal/storage/repos.go` accessConfigRepo：`accessConfigCols` 中 `overrides` 改 `settings`；`scanAccessConfig`：

```go
func scanAccessConfig(sc func(...any) error) (AccessConfig, error) {
	var c AccessConfig
	var cur int
	if err := sc(&c.ID, &c.AppType, &c.Name, &c.LogicalModelID, &c.Settings, &cur, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return AccessConfig{}, err
	}
	c.IsCurrent = cur == 1
	if c.Settings == "" { // 写入侧保证合法 JSON；空串回读为 "{}"
		c.Settings = "{}"
	}
	return c, nil
}
```

`Save` 内删除 overrides 编组逻辑，改：

```go
	if c.Settings == "" {
		c.Settings = "{}"
	}
	_, err = r.db.Exec(`INSERT INTO access_configs(id,app_type,name,logical_model_id,settings,is_current,sort_order,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET app_type=excluded.app_type, name=excluded.name,
			logical_model_id=excluded.logical_model_id, settings=excluded.settings,
			is_current=excluded.is_current, sort_order=excluded.sort_order, updated_at=excluded.updated_at`,
		c.ID, c.AppType, c.Name, c.LogicalModelID, c.Settings, boolInt(c.IsCurrent), c.SortOrder, c.CreatedAt, c.UpdatedAt)
```

- [ ] **Step 5: bridge 机械适配（签名不变）**

`internal/bridge/accessconfigs.go` 新增两个小助手：

```go
// accessDocEnvMap 从整份 settings 文档提取 env 字符串 map（写入侧保证 env 值为
// 字符串；解析失败向上返回错误）。文档缺 env 视为空 map。
func accessDocEnvMap(settings string) (map[string]string, error) {
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if strings.TrimSpace(settings) == "" {
		return map[string]string{}, nil
	}
	if err := json.Unmarshal([]byte(settings), &doc); err != nil {
		return nil, err
	}
	if doc.Env == nil {
		return map[string]string{}, nil
	}
	return doc.Env, nil
}

// accessEnvDoc 把 env map 包装为最小文档 JSON（Task 7 前的过渡存法）。
func accessEnvDoc(env map[string]string) string {
	if env == nil {
		env = map[string]string{}
	}
	b, err := json.Marshal(struct {
		Env map[string]string `json:"env"`
	}{Env: env})
	if err != nil {
		return "{}" // map[string]string 不可能 marshal 失败；防御
	}
	return string(b)
}
```

机械替换（行为等价，`Create`/`Update` 对外签名保持 `overrides map[string]string`）：
- `composeFor`：`env, err := composeAccessEnv(c.AppType, ..., lm.Name, global, c.Overrides)` 改为先 `overrides, err := accessDocEnvMap(c.Settings)` 再传入；错误直接透传。
- `view()`：在解析 `LogicalModelName` 后加 `v.Overrides, _ = accessDocEnvMap(c.Settings)`（提取失败留空 map）。
- `Create`：`Save(storage.AccessConfig{... Overrides: overrides})` 改 `Settings: accessEnvDoc(overrides)`。
- `Update`：`c.Name, c.LogicalModelID, c.Overrides = ...` 改 `c.Name, c.LogicalModelID = ...; c.Settings = accessEnvDoc(overrides)`。
- `internal/bridge/accessconfigs_test.go` 中所有 `storage.AccessConfig{... Overrides: map[string]string{...}}` 字面量改为 `Settings: accessEnvDoc(map[string]string{...})`（`grep -n "Overrides" internal/bridge/accessconfigs_test.go` 逐个替换；测试函数名与断言不变）。

- [ ] **Step 6: 更新既有 storage 测试引用**

Run: `grep -n "Overrides" internal/storage/*.go internal/bridge/*.go`
把 storage 测试里 `Overrides: map[string]string{...}` 字面量改为 `Settings: accessEnvDoc(...)`（storage 包内测试无 accessEnvDoc——直接写 `Settings: `{"env":{"A":"1"}}`` 字面量）；`scan`/`Save` 相关断言同步改 `Settings`。

- [ ] **Step 7: 运行验证**

Run: `go build ./... && go test ./internal/storage ./internal/bridge -count=1`
Expected: 全部 PASS

- [ ] **Step 8: Commit**

```bash
git add internal/storage internal/bridge
git commit -m "feat(storage): 删库重建单一 v1——access_configs.settings 取代 overrides"
```

---

### Task 2: bridge 文档解析与校验 parseAccessDoc

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Modify: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: `parseAccessDoc(appType, raw string) (doc map[string]any, env map[string]string, err error)`——Task 3/5/6/7 消费。
- 错误文案（中文，测试钉死）：`"配置必须是 JSON 对象"`、`"env 必须是 JSON 对象"`、`"env 值必须是字符串: <key>"`、`"Codex 配置仅支持 env 键"`。

- [ ] **Step 1: 写失败测试**

`internal/bridge/accessconfigs_test.go` 追加：

```go
func TestParseAccessDocValidation(t *testing.T) {
	// 根非对象
	if _, _, err := parseAccessDoc("claude", "[]"); err == nil || !strings.Contains(err.Error(), "配置必须是 JSON 对象") {
		t.Fatalf("根非对象应报错: %v", err)
	}
	if _, _, err := parseAccessDoc("claude", "null"); err == nil || !strings.Contains(err.Error(), "配置必须是 JSON 对象") {
		t.Fatalf("null 应报错: %v", err)
	}
	// env 非对象
	if _, _, err := parseAccessDoc("claude", `{"env":[]}`); err == nil || !strings.Contains(err.Error(), "env 必须是 JSON 对象") {
		t.Fatalf("env 非对象应报错: %v", err)
	}
	// env 值非字符串
	if _, _, err := parseAccessDoc("claude", `{"env":{"A":1}}`); err == nil || !strings.Contains(err.Error(), "env 值必须是字符串") {
		t.Fatalf("env 值非字符串应报错: %v", err)
	}
	// codex 仅允许 env 键
	if _, _, err := parseAccessDoc("codex", `{"env":{},"mcpServers":{}}`); err == nil || !strings.Contains(err.Error(), "Codex 配置仅支持 env 键") {
		t.Fatalf("codex 顶层键应报错: %v", err)
	}
	// 非法 appType
	if _, _, err := parseAccessDoc("gemini", `{}`); err == nil {
		t.Fatal("非法 appType 应报错")
	}
}

func TestParseAccessDocValid(t *testing.T) {
	// 空串/空文档 → 空 env
	for _, raw := range []string{"", "  ", "{}", `{"env":null}`} {
		doc, env, err := parseAccessDoc("claude", raw)
		if err != nil || len(env) != 0 || doc == nil {
			t.Fatalf("raw=%q 应得空 env: doc=%v env=%v err=%v", raw, doc, env, err)
		}
	}
	// claude 允许任意顶层键
	doc, env, err := parseAccessDoc("claude", `{"env":{"A":"1"},"mcpServers":{"x":{"command":"c"}},"language":"zh-CN"}`)
	if err != nil {
		t.Fatal(err)
	}
	if env["A"] != "1" || doc["language"] != "zh-CN" {
		t.Fatalf("doc=%v env=%v", doc, env)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/bridge -run TestParseAccessDoc -v`
Expected: FAIL（`parseAccessDoc` 未定义）

- [ ] **Step 3: 最小实现**

`internal/bridge/accessconfigs.go`（放在 `validateAccessAppType` 之后）：

```go
// parseAccessDoc 解析接入配置文档（spec 2026-10-19 §5.2）：根必须是 JSON 对象
// （空串视为空对象）；env 缺省/null 视为空对象、必须是字符串值对象；Codex 仅
// 允许 env 键（其 live 是 TOML，顶层文档编辑仅 Claude 享受）。返回解析后的
// 文档与提取的 env 字符串 map。
func parseAccessDoc(appType, raw string) (map[string]any, map[string]string, error) {
	if err := validateAccessAppType(appType); err != nil {
		return nil, nil, err
	}
	doc := map[string]any{}
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
			return nil, nil, fmt.Errorf("配置必须是 JSON 对象：%v", err)
		}
		if doc == nil { // 合法 "null" 也拒绝：根必须是对象
			return nil, nil, fmt.Errorf("配置必须是 JSON 对象")
		}
	}
	env := map[string]string{}
	if v, ok := doc["env"]; ok && v != nil {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("env 必须是 JSON 对象")
		}
		for k, val := range m {
			s, ok := val.(string)
			if !ok {
				return nil, nil, fmt.Errorf("env 值必须是字符串: %s", k)
			}
			env[k] = s
		}
	}
	if appType == "codex" {
		for k := range doc {
			if k != "env" {
				return nil, nil, fmt.Errorf("Codex 配置仅支持 env 键")
			}
		}
	}
	return doc, env, nil
}
```

- [ ] **Step 4: 运行验证**

Run: `go test ./internal/bridge -run TestParseAccessDoc -v && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge
git commit -m "feat(bridge): 接入文档解析与校验 parseAccessDoc"
```

---

### Task 3: bridge 保存归一化 normalizeAccessEnv（锁定键 + 未定制剔除）

**Files:**
- Modify: `internal/bridge/accessconfigs.go`（`composeAccessEnv` 重构拆分 + `normalizeAccessEnv`）
- Modify: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: `baseAccessEnv(appType string, st Status, modelName string) (map[string]string, error)`、`lockedAccessEnv(appType string, st Status, modelName string) map[string]string`、`normalizeAccessEnv(appType string, st Status, modelName string, global, docEnv map[string]string) (map[string]string, error)`——Task 7 消费。`composeAccessEnv` 行为不变（重构为复用前两者）。

- [ ] **Step 1: 写失败测试**

```go
func TestNormalizeAccessEnvStripsLockedAndUnchanged(t *testing.T) {
	st := Status{Port: 17890, Token: "tok"}
	global := map[string]string{"HTTPS_PROXY": "http://p:1"}
	docEnv := map[string]string{
		"ANTHROPIC_BASE_URL":         "http://evil:9",   // 锁定键→剔除
		"ANTHROPIC_AUTH_TOKEN":       "steal",           // 锁定键→剔除
		"ANTHROPIC_MODEL":            "other",           // 锁定键→剔除
		"ANTHROPIC_SMALL_FAST_MODEL": "my-code",         // 与默认相同→剔除
		"HTTPS_PROXY":                "http://p:1",      // 与全局相同→剔除
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "fast",        // 定制→保留
		"API_TIMEOUT_MS":             "3000000",         // 新增→保留
	}
	got, err := normalizeAccessEnv("claude", st, "my-code", global, docEnv)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "fast", "API_TIMEOUT_MS": "3000000"}
	if len(got) != len(want) {
		t.Fatalf("got=%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("got=%v", got)
		}
	}
}

func TestNormalizeAccessEnvCodex(t *testing.T) {
	st := Status{Port: 17890, Token: "tok"}
	got, err := normalizeAccessEnv("codex", st, "my-code", nil, map[string]string{
		"OPENAI_BASE_URL": "http://evil/v1",
		"OPENAI_API_KEY":  "steal",
		"RUST_LOG":        "debug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["OPENAI_BASE_URL"]; ok {
		t.Fatal("OPENAI_BASE_URL 应剔除")
	}
	if _, ok := got["OPENAI_API_KEY"]; ok {
		t.Fatal("OPENAI_API_KEY 应剔除")
	}
	if got["RUST_LOG"] != "debug" {
		t.Fatalf("got=%v", got)
	}
}

func TestNormalizeAccessEnvRejectsBadInput(t *testing.T) {
	if _, err := normalizeAccessEnv("gemini", Status{}, "m", nil, nil); err == nil {
		t.Fatal("非法 appType 应报错")
	}
	if _, err := normalizeAccessEnv("claude", Status{}, "", nil, nil); err == nil {
		t.Fatal("空模型名应报错")
	}
}

func TestComposeAccessEnvBehaviorUnchangedAfterRefactor(t *testing.T) {
	// 重构后 composeAccessEnv 行为必须与旧实现一致：基础键+全局+docEnv+锁定回写
	st := Status{Port: 17890, Token: "tok"}
	got, err := composeAccessEnv("claude", st, "my-code",
		map[string]string{"HTTPS_PROXY": "http://p:1"},
		map[string]string{"ANTHROPIC_BASE_URL": "http://evil:9", "API_TIMEOUT_MS": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if got["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:17890" {
		t.Fatal(got)
	}
	if got["ANTHROPIC_AUTH_TOKEN"] != "tok" || got["ANTHROPIC_MODEL"] != "my-code" || got["ANTHROPIC_SMALL_FAST_MODEL"] != "my-code" {
		t.Fatal(got)
	}
	if got["HTTPS_PROXY"] != "http://p:1" || got["API_TIMEOUT_MS"] != "1" {
		t.Fatal(got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/bridge -run 'TestNormalize|TestComposeAccessEnvBehaviorUnchanged' -v`
Expected: FAIL（`normalizeAccessEnv` 未定义）

- [ ] **Step 3: 重构 composeAccessEnv 并实现 normalizeAccessEnv**

`internal/bridge/accessconfigs.go`：把现 `composeAccessEnv`（38-85 行）的 base/locked 两段抽成函数，原函数改为组合：

```go
// baseAccessEnv 返回网关派生基础键（claude 含 SMALL_FAST 默认值）。
func baseAccessEnv(appType string, st Status, modelName string) (map[string]string, error) {
	if modelName == "" {
		return nil, fmt.Errorf("请先选择逻辑模型")
	}
	if err := validateAccessAppType(appType); err != nil {
		return nil, err
	}
	switch appType {
	case "claude":
		return map[string]string{
			"ANTHROPIC_BASE_URL":         fmt.Sprintf("http://127.0.0.1:%d", st.Port),
			"ANTHROPIC_AUTH_TOKEN":       st.Token,
			"ANTHROPIC_MODEL":            modelName,
			"ANTHROPIC_SMALL_FAST_MODEL": modelName,
		}, nil
	case "codex":
		return map[string]string{
			"OPENAI_BASE_URL": fmt.Sprintf("http://127.0.0.1:%d/v1", st.Port),
			"OPENAI_API_KEY":  st.Token,
		}, nil
	}
	return nil, fmt.Errorf("不支持的接入类型: %q（仅支持 claude、codex）", appType)
}

// lockedAccessEnv 返回不可篡改基础键子集（composeAccessEnv 第 4 步回写、
// normalizeAccessEnv 保存剔除共用）。
func lockedAccessEnv(appType string, st Status, modelName string) map[string]string {
	base, err := baseAccessEnv(appType, st, modelName)
	if err != nil {
		return nil
	}
	switch appType {
	case "claude":
		return map[string]string{
			"ANTHROPIC_BASE_URL":   base["ANTHROPIC_BASE_URL"],
			"ANTHROPIC_AUTH_TOKEN": base["ANTHROPIC_AUTH_TOKEN"],
			"ANTHROPIC_MODEL":      base["ANTHROPIC_MODEL"],
		}
	case "codex":
		return base // codex 两键全锁
	}
	return nil
}

// composeAccessEnv 合成应写入 Agent 的 env：base → global → docEnv → 锁定回写。
// 语义与拆分前完全一致（测试钉死）；claude 的 ANTHROPIC_SMALL_FAST_MODEL 只作
// 默认值，允许被 docEnv 覆盖。
func composeAccessEnv(appType string, st Status, modelName string, global, docEnv map[string]string) (map[string]string, error) {
	base, err := baseAccessEnv(appType, st, modelName)
	if err != nil {
		return nil, err
	}
	locked := lockedAccessEnv(appType, st, modelName)
	env := make(map[string]string, len(base)+len(global)+len(docEnv))
	for k, v := range base { // 1. 网关派生基础键
		env[k] = v
	}
	for k, v := range global { // 2. 全局 env（access.global.<app_type>）
		env[k] = v
	}
	for k, v := range docEnv { // 3. 配置文档 env（最高优先，但动不了锁定键）
		env[k] = v
	}
	for k, v := range locked { // 4. 重新写回不可篡改基础键（恒定）
		env[k] = v
	}
	return env, nil
}

// normalizeAccessEnv 保存归一化（spec 2026-10-19 §5.2）：1) 锁定键恒剔除
//（网关自动生成，改了无效）；2) 值与「无文档层合成结果」（基础键+全局）相同
// 的键剔除——视为未定制、保持继承语义，防止编辑器展示的合成结果在直接保存时
// 把全局键与默认值冻结成覆盖项。
func normalizeAccessEnv(appType string, st Status, modelName string, global, docEnv map[string]string) (map[string]string, error) {
	base, err := baseAccessEnv(appType, st, modelName)
	if err != nil {
		return nil, err
	}
	baseline := make(map[string]string, len(base)+len(global))
	for k, v := range base {
		baseline[k] = v
	}
	for k, v := range global {
		baseline[k] = v
	}
	locked := lockedAccessEnv(appType, st, modelName)
	out := map[string]string{}
	for k, v := range docEnv {
		if _, isLocked := locked[k]; isLocked {
			continue
		}
		if bv, ok := baseline[k]; ok && bv == v {
			continue
		}
		out[k] = v
	}
	return out, nil
}
```

删除旧 `composeAccessEnv` 函数体（保留原注释精髓在新函数上）。`fmt` 已导入。

- [ ] **Step 4: 运行验证（含既有合成测试防回归）**

Run: `go test ./internal/bridge -run 'TestNormalize|TestComposeAccessEnv|TestComposeFor' -v && go build ./...`
Expected: 全部 PASS（旧 `TestComposeAccessEnvClaudeMergePrecedence`/`TestComposeAccessEnvBaseKeysImmutable`/`TestComposeAccessEnvRejectsUnknownAppType` 也必须仍绿）

- [ ] **Step 5: Commit**

```bash
git add internal/bridge
git commit -m "feat(bridge): 保存归一化 normalizeAccessEnv（锁定键+未定制剔除）"
```

---

### Task 4: gateway [1M] 后缀剥离

**Files:**
- Modify: `internal/gateway/handlers.go`（双入口 + `stripOneM` 助手）
- Modify: `internal/gateway/gateway_test.go`

**Interfaces:**
- Produces: `stripOneM(model string) string`（gateway 包内私有）；两个 handler 在 Parse 后立即 `model = stripOneM(model)`。
- 语义：尾部 `[1M]` 大小写不敏感剥离（`[1m]` 也算）；无后缀原样返回；剥离发生在 `rec.Model` 赋值、`Candidates`、`Resolve` 之前，候选匹配与上游改写都用剥离后名字。

- [ ] **Step 1: 写失败测试**

`internal/gateway/gateway_test.go` 追加（复用 `newGW`/`post`/`fakeUpstream` 基建）：

```go
func TestMessagesStripsOneMSuffix(t *testing.T) {
	var calls int
	var lastModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		m := map[string]any{}
		json.Unmarshal(b, &m)
		lastModel, _ = m["model"].(string)
		w.Write([]byte(`{"id":"ok","type":"message"}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	// pub-1[1M] 应命中公开模型 pub-1（Candidates 只对 pub-1 返回候选）
	for _, model := range []string{"pub-1[1M]", "pub-1[1m]"} {
		calls = 0
		resp := post(t, s, `{"model":"`+model+`","max_tokens":8,"messages":[{"role":"user","content":"hi"}]}`, "tok")
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("model=%s status %d body %s", model, resp.StatusCode, b)
		}
		if calls != 1 || lastModel != "up-1" {
			t.Fatalf("model=%s calls=%d lastModel=%s", model, calls, lastModel)
		}
	}
}

func TestOpenAIChatStripsOneMSuffix(t *testing.T) {
	var calls int
	var lastModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		b, _ := io.ReadAll(r.Body)
		m := map[string]any{}
		json.Unmarshal(b, &m)
		lastModel, _ = m["model"].(string)
		w.Write([]byte(`{"id":"chatcmpl-ok","object":"chat.completion","choices":[]}`))
	}))
	defer up.Close()
	s := newGW(t, up.URL, nil)
	req, _ := http.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"pub-1[1M]","messages":[{"role":"user","content":"hi"}]}`))
	req.Header.Set("Authorization", "Bearer tok")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, req)
	resp := w.Result()
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d body %s", resp.StatusCode, b)
	}
	if calls != 1 || lastModel != "up-1" {
		t.Fatalf("calls=%d lastModel=%s", calls, lastModel)
	}
}

func TestStripOneM(t *testing.T) {
	cases := map[string]string{
		"pub-1[1M]": "pub-1",
		"pub-1[1m]": "pub-1",
		"pub-1":     "pub-1",
		"[]":        "[]",
		"[1M]":      "", // 纯后缀也剥（随后未登记模型 404，行为明确）
		"a[1M]x":    "a[1M]x", // 非尾部不动
	}
	for in, want := range cases {
		if got := stripOneM(in); got != want {
			t.Fatalf("stripOneM(%q)=%q want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/gateway -run 'TestMessagesStripsOneM|TestOpenAIChatStripsOneM|TestStripOneM' -v`
Expected: FAIL（`stripOneM` 未定义；两个 handler 测试 404）

- [ ] **Step 3: 实现**

`internal/gateway/handlers.go` 顶部（`package gateway` 声明后）加：

```go
// stripOneM 剥离模型名尾部 "[1M]"（大小写不敏感，Claude Code 1M 上下文声明
// 字面量，spec 2026-10-19 §3.3）：接入配置角色行可带该标记，网关路由匹配前
// 剥离，使 my-code[1M] 命中逻辑模型 my-code。
func stripOneM(model string) string {
	if len(model) >= 4 && strings.EqualFold(model[len(model)-4:], "[1M]") {
		return model[:len(model)-4]
	}
	return model
}
```

Anthropic 入口：`cr, err := protocol.ParseChatRequest(body)` 成功后、`rec.Model, rec.Streaming = cr.Model, cr.Stream` 之前插入：

```go
	cr.Model = stripOneM(cr.Model)
```

OpenAI 入口：`oar, err := protocol.ParseOpenAIChatRequest(body)` 成功后同位置插入：

```go
	oar.Model = stripOneM(oar.Model)
```

（`strings` 已导入。）

- [ ] **Step 4: 运行验证（带 race）**

Run: `go test -race ./internal/gateway -count=1 && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway
git commit -m "feat(gateway): 路由匹配前剥离模型名尾部 [1M] 后缀（双入口）"
```

---

### Task 5: bridge 合成预览 PreviewDoc（令牌遮蔽）

**Files:**
- Modify: `internal/bridge/accessconfigs.go`
- Modify: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: `func (s AccessConfigs) PreviewDoc(appType, logicalModelID, settingsJSON string) (string, error)`——返回合成后整份文档 JSON（2 空格缩进），`ANTHROPIC_AUTH_TOKEN`/`OPENAI_API_KEY` 值为 `"__GATEWAY_TOKEN__"`。Task 8 消费。
- 常量：`gatewayTokenPlaceholder = "__GATEWAY_TOKEN__"`。

- [ ] **Step 1: 写失败测试**

```go
func TestPreviewDocMasksTokenAndComposes(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := acc.SaveGlobalConfig("claude", map[string]string{"HTTPS_PROXY": "http://p:1"}); err != nil {
		t.Fatal(err)
	}
	raw, err := acc.PreviewDoc("claude", "lm1", `{"env":{"API_TIMEOUT_MS":"1","ANTHROPIC_AUTH_TOKEN":"x"},"language":"zh-CN"}`)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	env := doc["env"].(map[string]any)
	if env["ANTHROPIC_AUTH_TOKEN"] != gatewayTokenPlaceholder {
		t.Fatalf("令牌应遮蔽: %v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if env["ANTHROPIC_MODEL"] != "my-code" || env["ANTHROPIC_BASE_URL"] == "" {
		t.Fatalf("锁定键应就位: %v", env)
	}
	if env["HTTPS_PROXY"] != "http://p:1" || env["API_TIMEOUT_MS"] != "1" {
		t.Fatalf("全局/文档 env 应合成: %v", env)
	}
	if doc["language"] != "zh-CN" {
		t.Fatalf("顶层键应保留: %v", doc)
	}
}

func TestPreviewDocErrors(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := acc.PreviewDoc("claude", "nope", `{}`); err == nil {
		t.Fatal("逻辑模型不存在应报错")
	}
	if _, err := acc.PreviewDoc("claude", "lm1", `{bad`); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/bridge -run TestPreviewDoc -v`
Expected: FAIL（方法未定义）

- [ ] **Step 3: 实现**

`internal/bridge/accessconfigs.go`（`composeFor` 之后）：

```go
// gatewayTokenPlaceholder 是合成预览中网关令牌的遮蔽占位符（spec 2026-10-19
// §3.4：UI 永不展示令牌真值；保存路径剔除锁定键，占位符永不落库）。
const gatewayTokenPlaceholder = "__GATEWAY_TOKEN__"

// PreviewDoc 返回「应用该文档后 Agent env 的样子」：base → global → 文档 env →
// 锁定回写，令牌键遮蔽为占位符；文档顶层键原样保留。编辑器打开与主模型切换时
// 调用。逻辑模型必须存在。
func (s AccessConfigs) PreviewDoc(appType, logicalModelID, settingsJSON string) (string, error) {
	lm, err := s.A.Store().LogicalModels().Get(logicalModelID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return "", fmt.Errorf("逻辑模型不存在")
		}
		return "", err
	}
	doc, env, err := parseAccessDoc(appType, settingsJSON)
	if err != nil {
		return "", err
	}
	global, err := s.GlobalConfig(appType)
	if err != nil {
		return "", err
	}
	st := Gateway{A: s.A}.Status()
	composed, err := composeAccessEnv(appType, st, lm.Name, global, env)
	if err != nil {
		return "", err
	}
	switch appType {
	case "claude":
		composed["ANTHROPIC_AUTH_TOKEN"] = gatewayTokenPlaceholder
	case "codex":
		composed["OPENAI_API_KEY"] = gatewayTokenPlaceholder
	}
	envAny := make(map[string]any, len(composed))
	for k, v := range composed {
		envAny[k] = v
	}
	doc["env"] = envAny
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
```

- [ ] **Step 4: 运行验证**

Run: `go test ./internal/bridge -run TestPreviewDoc -v && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge
git commit -m "feat(bridge): PreviewDoc 合成预览（令牌遮蔽）"
```

---

### Task 6: bridge 应用链路文档化——writeClaudeLive 顶层键 + 持久文件整份文档

**Files:**
- Modify: `internal/bridge/accessconfigs.go`（`composeFor`/`writeClaudeLive`/`writePersistEnv`/`Apply`/`RegenerateDerived`/`LaunchCommand`）
- Modify: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces（签名变更，同包内全部调用点本任务内更新）:
  - `composeFor(c storage.AccessConfig) (env map[string]string, doc map[string]any, model string, err error)`
  - `writeClaudeLive(doc map[string]any, env map[string]string) error`
  - `writePersistEnv(c storage.AccessConfig, env map[string]string, doc map[string]any) (claudePath, codexPath string, err error)`
- 语义：`writeClaudeLive` 把 doc 中 `env` 以外的顶层键逐键写入 live（同键替换）；live 中不在 doc 的键保留。Claude 持久文件 = 整份合成文档（doc 顶层键 + env 替换为合成结果）。

- [ ] **Step 1: 写失败测试**

```go
func TestClaudeLiveWritesDocTopLevelKeys(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"permissions":{"allow":["Bash"]},"language":"en-US","env":{"FOO":"bar"}}`), 0o600)
	doc := map[string]any{
		"language": "zh-CN",
		"mcpServers": map[string]any{"x": map[string]any{"command": "c"}},
	}
	if err := acc.writeClaudeLive(doc, map[string]string{"ANTHROPIC_MODEL": "new"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	var got map[string]any
	json.Unmarshal(b, &got)
	if got["language"] != "zh-CN" {
		t.Fatalf("文档顶层键应写入: %s", b)
	}
	if got["mcpServers"] == nil {
		t.Fatalf("文档新增顶层键应写入: %s", b)
	}
	if got["permissions"] == nil {
		t.Fatalf("非文档键应保留: %s", b)
	}
	if got["env"].(map[string]any)["FOO"] != "bar" {
		t.Fatalf("env 非受管键应保留: %s", b)
	}
}

func TestPersistFileIsFullDocForClaude(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.Create("claude", "工作", "lm1", `{"language":"zh-CN"}`); err != nil {
		t.Fatal(err)
	}
	list := acc.List("claude")
	cmd, err := acc.LaunchCommand(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "--settings") {
		t.Fatalf("启动命令应引用持久文件: %s", cmd)
	}
	b, err := os.ReadFile(filepath.Join(acc.Home, ".agentswitch", "access", "claude", list[0].ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["language"] != "zh-CN" {
		t.Fatalf("持久文件应为整份文档: %s", b)
	}
	env, ok := doc["env"].(map[string]any)
	if !ok || env["ANTHROPIC_MODEL"] != "my-code" || env["ANTHROPIC_BASE_URL"] == nil {
		t.Fatalf("持久文件 env 应合成: %s", b)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/bridge -run 'TestClaudeLiveWritesDocTopLevelKeys|TestPersistFileIsFullDocForClaude' -v`
Expected: FAIL（签名未变/持久文件仍只含 env）

- [ ] **Step 3: 实现**

1) `composeFor` 返回值加 doc（错误处理与注释保留）：

```go
func (s AccessConfigs) composeFor(c storage.AccessConfig) (map[string]string, map[string]any, string, error) {
	lm, err := s.A.Store().LogicalModels().Get(c.LogicalModelID)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return nil, nil, "", fmt.Errorf("绑定的逻辑模型不存在，请改绑")
		}
		return nil, nil, "", err
	}
	raw, err := s.A.Store().Settings().Get("access.global." + c.AppType)
	if err != nil {
		return nil, nil, "", err
	}
	var global map[string]string
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &global); err != nil {
			return nil, nil, "", err
		}
	}
	doc, docEnv, err := parseAccessDoc(c.AppType, c.Settings)
	if err != nil {
		return nil, nil, "", err
	}
	env, err := composeAccessEnv(c.AppType, Gateway{A: s.A}.Status(), lm.Name, global, docEnv)
	if err != nil {
		return nil, nil, "", err
	}
	return env, doc, lm.Name, nil
}
```

2) `writeClaudeLive` 签名改 `(doc map[string]any, env map[string]string)`，在确保 env 子对象存在之前插入顶层键写入（解析 root 成功后）：

```go
	// 文档顶层键逐键写入（同键替换；live 中不在文档的键保留——只增改不删除，
	// spec 2026-10-19 §4.1）。
	for k, v := range doc {
		if k == "env" {
			continue
		}
		root[k] = v
	}
```

3) `writePersistEnv` 签名加 `doc map[string]any`；claude 分支改为整份文档：

```go
	case "claude":
		docFull := make(map[string]any, len(doc)+1)
		for k, v := range doc {
			docFull[k] = v
		}
		envAny := make(map[string]any, len(env))
		for k, v := range env {
			envAny[k] = v
		}
		docFull["env"] = envAny
		b, err := json.MarshalIndent(docFull, "", "  ")
		if err != nil {
			return "", "", err
		}
		// ... 目录/原子写逻辑不变（写 b）
```

codex 分支忽略 doc 参数（注释说明）。

4) 调用点更新（同文件内）：`Apply`、`RegenerateDerived` 用四返回值 `env, doc, model, err := s.composeFor(c)`；`writeClaudeLive(doc, env)`、`writeCodexLive(env, model)` 不变；`writePersistEnv(c, env, doc)`；`LaunchCommand` 同理。（Task 1 的过渡助手 `accessDocEnvMap` 本任务不动——`view()` 的 Overrides 提取还在用；Task 10 随 `view.Overrides` 一并删除。）

- [ ] **Step 4: 运行验证**

Run: `go test ./internal/bridge -count=1 && go build ./...`
Expected: 全部 PASS（既有 Apply/备份/联动测试同步修签名引用）

- [ ] **Step 5: Commit**

```bash
git add internal/bridge
git commit -m "feat(bridge): 应用链路文档化——live 顶层键写入、持久文件整份文档"
```

---

### Task 7: bridge Create/Update 文档化签名 + view.Settings + desktop 机械适配

**Files:**
- Modify: `internal/bridge/accessconfigs.go`（`Create`/`Update`/`view`/`AccessConfigView` + 新 `prepareDoc`）
- Modify: `internal/bridge/accessconfigs_test.go`
- Modify: `internal/desktop/access.go`（`accessSaveForm` 机械适配，UI 不变）

**Interfaces:**
- Produces:
  - `Create(appType, name, logicalModelID, settingsJSON string) (AccessConfigView, error)`
  - `Update(id, name, logicalModelID, settingsJSON string) error`
  - `AccessConfigView.Settings string`（整份文档原文；`Overrides` 字段本任务暂留，Task 8 删除）
  - `prepareDoc(appType, modelName, settingsJSON string) (string, error)`（parse → normalize → doc["env"]=norm → MarshalIndent）
- desktop 消费：`accessSaveForm` 把旧键值行打包成 `{"env":{...}}` 传入（过渡，Task 8 换 UI）。

- [ ] **Step 1: 写失败测试（bridge 归一化入库存储）**

```go
func TestCreateNormalizesDocBeforeStore(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "工作", "lm1",
		`{"env":{"ANTHROPIC_AUTH_TOKEN":"steal","ANTHROPIC_MODEL":"other","API_TIMEOUT_MS":"1"},"language":"zh-CN"}`)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(v.Settings), &doc); err != nil {
		t.Fatal(err)
	}
	env := doc["env"].(map[string]any)
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatal("锁定键不应入库")
	}
	if _, ok := env["ANTHROPIC_MODEL"]; ok {
		t.Fatal("锁定键不应入库")
	}
	if env["API_TIMEOUT_MS"] != "1" {
		t.Fatalf("定制键应保留: %v", env)
	}
	if doc["language"] != "zh-CN" {
		t.Fatalf("顶层键应保留: %v", doc)
	}
	// codex 顶层键拒绝
	if _, err := acc.Create("codex", "x", "lm1", `{"env":{},"language":"zh-CN"}`); err == nil {
		t.Fatal("codex 顶层键应报错")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/bridge -run TestCreateNormalizesDocBeforeStore -v`
Expected: FAIL（旧签名收 map；错误路径或类型不匹配）

- [ ] **Step 3: 实现 bridge**

`prepareDoc`（`PreviewDoc` 之后）：

```go
// prepareDoc 解析+归一化接入文档并序列化（Create/Update 共用）：parseAccessDoc
// 校验 → normalizeAccessEnv 剔除锁定键与未定制键 → env 写回文档 → 2 空格缩进。
func (s AccessConfigs) prepareDoc(appType, modelName, settingsJSON string) (string, error) {
	doc, env, err := parseAccessDoc(appType, settingsJSON)
	if err != nil {
		return "", err
	}
	global, err := s.GlobalConfig(appType)
	if err != nil {
		return "", err
	}
	norm, err := normalizeAccessEnv(appType, Gateway{A: s.A}.Status(), modelName, global, env)
	if err != nil {
		return "", err
	}
	doc["env"] = norm
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}
```

`Create`：第四参改 `settingsJSON string`；校验 name/lm 后：

```go
	raw, err := s.prepareDoc(appType, lm.Name, settingsJSON)
	if err != nil {
		return AccessConfigView{}, err
	}
	id := storage.NewID()
	saved, err := s.A.Store().AccessConfigs().Save(storage.AccessConfig{
		ID: id, AppType: appType, Name: name, LogicalModelID: logicalModelID, Settings: raw,
	})
```

（lm Get 改用接收变量 `lm` 拿 `.Name`。）`Update` 同样改第四参并 `c.Settings = raw`。

`AccessConfigView` 加 `Settings string`；`view()` 加 `v.Settings = c.Settings`。`Overrides` 字段与提取本任务保留（旧 UI 还在用）。

- [ ] **Step 4: 更新既有测试签名 + desktop 机械适配**

- `internal/bridge/accessconfigs_test.go`：所有 `acc.Create("claude", 名, "lm1", map[string]string{...})` 改第四参为 `accessEnvDoc(map[string]string{...})`（Task 1 保留的助手）；`acc.Update(id, 名, "lm1", map...)` 同理；`TestAccessConfigsCRUDAndView` 断言不变。
- `internal/desktop/access.go` `accessSaveForm` 的保存段改为：

```go
	payload := struct {
		Env map[string]string `json:"env"`
	}{Env: accessOverridesFromRows(f.overrides)}
	b, err := json.Marshal(payload)
	if err != nil {
		f.msg = err.Error()
		return
	}
	settingsJSON := string(b)
	if f.editID != "" {
		if err := acc.Update(f.editID, f.name, modelID, settingsJSON); err != nil {
			f.msg = err.Error()
			return
		}
	} else {
		if _, err := acc.Create(u.accessApp, f.name, modelID, settingsJSON); err != nil {
			f.msg = err.Error()
			return
		}
	}
```

（`encoding/json` 加入 desktop/access.go 导入。UI 与表单状态不变。）

- [ ] **Step 5: 运行验证**

Run: `go test ./internal/bridge ./internal/desktop -count=1 && go build ./... && go vet ./...`
Expected: 全部 PASS

- [ ] **Step 6: Commit**

```bash
git add internal/bridge internal/desktop
git commit -m "feat(bridge): Create/Update 文档化签名（保存归一化入库）"
```

---

### Task 8: desktop 编辑器卡骨架重做——docJSON + PreviewDoc + TextArea

**Files:**
- Modify: `internal/desktop/access.go`（`accessForm` 结构、`accessPreloadForm`、`accessEditorCard`、`accessSaveForm`；删 `accessOverridesFromRows`/`coverToggleLabel`）
- Modify: `internal/desktop/access_test.go`（删旧 helpers 测试、加新纯函数测试）

**Interfaces:**
- Consumes: `acc.PreviewDoc(...)`（Task 5）、`acc.Create/Update(..., settingsJSON string)`（Task 7）、`AccessConfigView.Settings`（Task 7）。
- Produces:
  - `accessForm{open, editID, name, modelLabel, docJSON, msg string/bool}`（`overrides`/`showOverrides` 字段删除）
  - `accessDocSetEnv(raw, key, value string) (string, error)`（value=="" 删除键；2 空格缩进）
  - `accessDocEnvStrings(raw string) (map[string]string, error)`（env 非字符串值→error）
  - `accessParseOneM(v string) (string, bool)`（尾部 `[1M]` 大小写不敏感剥离）
- UI 行为：编辑器卡 = 名称 + 逻辑模型下拉 + 网关只读行 + 配置 JSON TextArea + 保存/取消；覆盖项折叠区删除。

- [ ] **Step 1: 写失败测试（纯函数）**

`internal/desktop/access_test.go` 追加（删除 `TestAccessOverridesFromRows*`/`TestCoverToggleLabel*` 等引用被删 helpers 的旧测试）：

```go
func TestAccessDocSetEnv(t *testing.T) {
	raw, err := accessDocSetEnv(`{"env":{"A":"1"},"language":"zh-CN"}`, "B", "2")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	json.Unmarshal([]byte(raw), &doc)
	env := doc["env"].(map[string]any)
	if env["A"] != "1" || env["B"] != "2" || doc["language"] != "zh-CN" {
		t.Fatal(raw)
	}
	// value=="" 删除键
	raw2, err := accessDocSetEnv(raw, "A", "")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw2), &doc)
	if _, ok := doc["env"].(map[string]any)["A"]; ok {
		t.Fatal("空值应删键: " + raw2)
	}
	// 非法 JSON 报错
	if _, err := accessDocSetEnv(`{bad`, "A", "1"); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestAccessDocEnvStrings(t *testing.T) {
	env, err := accessDocEnvStrings(`{"env":{"A":"1"}}`)
	if err != nil || env["A"] != "1" {
		t.Fatalf("%v %v", env, err)
	}
	if _, err := accessDocEnvStrings(`{"env":{"A":1}}`); err == nil {
		t.Fatal("非字符串值应报错（保留旧状态）")
	}
	if _, err := accessDocEnvStrings(`{bad`); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

func TestAccessParseOneM(t *testing.T) {
	cases := map[string][2]any{
		"my-code[1M]": {"my-code", true},
		"my-code[1m]": {"my-code", true},
		"my-code":     {"my-code", false},
	}
	for in, want := range cases {
		name, oneM := accessParseOneM(in)
		if name != want[0] || oneM != want[1] {
			t.Fatalf("accessParseOneM(%q)=%v,%v want %v", in, name, oneM, want)
		}
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/desktop -run 'TestAccessDoc|TestAccessParseOneM' -v`
Expected: FAIL（函数未定义；旧测试若引用已删符号则编译错——同 Step 1 一并删）

- [ ] **Step 3: 实现纯函数**

`internal/desktop/access.go`（替换 `accessOverridesFromRows`）：

```go
// accessDocSetEnv 解析 raw 文档并对 env 键打补丁（value=="" 删除该键），2 空格
// 缩进序列化。解析失败返回 error（调用方保留旧文本）。仅 UI 本地补丁用，不做
// appType 校验（保存校验在 bridge）。
func accessDocSetEnv(raw, key, value string) (string, error) {
	doc := map[string]any{}
	if trimmed := strings.TrimSpace(raw); trimmed != "" {
		if err := json.Unmarshal([]byte(trimmed), &doc); err != nil {
			return "", err
		}
		if doc == nil {
			return "", fmt.Errorf("配置必须是 JSON 对象")
		}
	}
	env := map[string]any{}
	if v, ok := doc["env"].(map[string]any); ok {
		env = v
	}
	if value == "" {
		delete(env, key)
	} else {
		env[key] = value
	}
	doc["env"] = env
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// accessDocEnvStrings 提取文档 env 字符串 map（角色行回填数据源）；env 值非
// 字符串/JSON 非法返回 error——调用方保留上一合法状态。
func accessDocEnvStrings(raw string) (map[string]string, error) {
	doc := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return nil, err
		}
		if doc == nil {
			return nil, fmt.Errorf("配置必须是 JSON 对象")
		}
	}
	out := map[string]string{}
	if v, ok := doc["env"].(map[string]any); ok {
		for k, val := range v {
			s, ok := val.(string)
			if !ok {
				return nil, fmt.Errorf("env 值必须是字符串: %s", k)
			}
			out[k] = s
		}
	}
	return out, nil
}

// accessParseOneM 剥离值尾部 "[1M]"（大小写不敏感）——1M 勾选状态回显。
func accessParseOneM(v string) (string, bool) {
	if len(v) >= 4 && strings.EqualFold(v[len(v)-4:], "[1M]") {
		return v[:len(v)-4], true
	}
	return v, false
}
```

`encoding/json`、`fmt` 加入导入。

- [ ] **Step 4: 重做表单状态与编辑器卡**

`accessForm` 改为：

```go
// accessForm 是接入配置新建/编辑表单状态（open 非空即编辑）。docJSON 是唯一
// 共享存储（spec 2026-10-19 §5.2）：编辑器显示 PreviewDoc 合成结果（令牌已
// 遮蔽），保存原文送 bridge 归一化。
type accessForm struct {
	open       bool
	editID     string // 非空 = 编辑该配置
	name       string
	modelLabel string // 选中的逻辑模型名（保存时映射回 ID）
	docJSON    string
	msg        string
}
```

`accessPreloadForm` 改为（替换整个函数）：

```go
// accessPreloadForm 打开编辑器：预填名称/模型，加载 PreviewDoc 合成预览
//（新建以 "{}" 为底；预览失败回退原文并内联报错）。
func (u *uiState) accessPreloadForm(acc bridge.AccessConfigs) {
	f := &u.accessForm
	var models []bridge.ModelView
	if u.svc.Logical != nil {
		models = u.svc.Logical.List()
	}
	if f.modelLabel == "" {
		if labels := accessModelLabels(models); len(labels) > 0 {
			f.modelLabel = labels[0]
		}
	}
	raw := "{}"
	if f.editID != "" {
		for _, v := range acc.List(u.accessApp) {
			if v.ID == f.editID {
				f.name = v.Name
				f.modelLabel = v.LogicalModelName
				raw = v.Settings
			}
		}
	}
	modelID := accessModelIDByLabel(models, f.modelLabel)
	preview, err := acc.PreviewDoc(u.accessApp, modelID, raw)
	if err != nil {
		f.docJSON = raw
		f.msg = "预览失败：" + err.Error()
		return
	}
	f.docJSON = preview
}
```

`accessEditorCard` 整体替换（加 `gateOK bool` 参数；调用点 `u.accessEditorCard(c, t, s, acc)` 同步改为传 `gateOK`）：

```go
// accessEditorCard 渲染新建/编辑表单（spec 2026-10-19 §5.1）：名称、逻辑模型
// 下拉（主模型，锁定键）、网关只读行、配置 JSON（TextArea，令牌已遮蔽）。
// 保存原文送 bridge（校验+归一化在 bridge）。
func (u *uiState) accessEditorCard(c *ui.Context, t *ui.Theme, s softPalette, acc bridge.AccessConfigs, gateOK bool) {
	card(c, t, func() {
		ui.Column(c).Gap(10).Children(func() {
			title := "新建配置"
			if u.accessForm.editID != "" {
				title = "编辑配置"
			}
			ui.Text(c, title).Bold()

			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
				ui.TextInput(c, &u.accessForm.name).Grow(1)
			})

			var models []bridge.ModelView
			if u.svc.Logical != nil {
				models = u.svc.Logical.List()
			}
			labels := accessModelLabels(models)
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "逻辑模型:").Width(96).TextColor(t.TextMuted)
				if len(labels) == 0 {
					ui.Text(c, "还没有启用的逻辑模型——先在「逻辑模型」页创建并启用").TextColor(t.TextMuted)
				} else {
					prev := u.accessForm.modelLabel
					sel := ui.Select(c, &u.accessForm.modelLabel, labels)
					if sel.Changed() && u.accessForm.modelLabel != prev {
						// 主模型切换：重新取合成预览（锁定键随之刷新）
						modelID := accessModelIDByLabel(models, u.accessForm.modelLabel)
						if preview, err := acc.PreviewDoc(u.accessApp, modelID, u.accessForm.docJSON); err == nil {
							u.accessForm.docJSON = preview
						}
					}
				}
			})

			st := u.svc.Gateway.Status()
			ui.Text(c, fmt.Sprintf("网关地址 http://127.0.0.1:%d · 令牌自动生成（不在本页展示）", st.Port)).TextColor(t.TextMuted)

			ui.Text(c, "配置 JSON（最终写入 Agent 的完整文档；网关地址/令牌/模型三键自动锁定）").TextColor(t.TextMuted)
			ui.TextArea(c, &u.accessForm.docJSON).Height(320).Grow(1)
			ui.Text(c, "与全局/默认值相同的键保存时自动视为未定制（保持继承）；要覆盖请改成不同值").TextColor(t.TextMuted)

			if u.accessForm.msg != "" {
				ui.Text(c, u.accessForm.msg).TextColor(t.Danger)
			}

			ui.Row(c).Gap(8).Children(func() {
				if !gateOK {
					ui.Text(c, "网关未运行——保存后需启动网关才能「应用」").TextColor(t.TextMuted)
				}
				if pillButton(c, t, "保存").Clicked() {
					u.accessSaveForm(acc, models)
				}
				if softButton(c, t, s, "gray", "取消").Clicked() {
					u.accessForm = accessForm{}
				}
			})
		})
	})
}
```

`accessSaveForm` 替换（不再经 overrides 行）：

```go
// accessSaveForm 校验并保存表单；文档原文送 bridge（parse+归一化错误内联）。
func (u *uiState) accessSaveForm(acc bridge.AccessConfigs, models []bridge.ModelView) {
	f := &u.accessForm
	if strings.TrimSpace(f.name) == "" {
		f.msg = "请填写配置名"
		return
	}
	modelID := accessModelIDByLabel(models, f.modelLabel)
	if modelID == "" {
		f.msg = "请选择逻辑模型"
		return
	}
	settingsJSON := strings.TrimSpace(f.docJSON)
	if settingsJSON == "" {
		settingsJSON = "{}"
	}
	if f.editID != "" {
		if err := acc.Update(f.editID, f.name, modelID, settingsJSON); err != nil {
			f.msg = err.Error()
			return
		}
	} else {
		if _, err := acc.Create(u.accessApp, f.name, modelID, settingsJSON); err != nil {
			f.msg = err.Error()
			return
		}
	}
	u.accessMsg = "已保存「" + f.name + "」"
	u.accessForm = accessForm{}
}
```

删除 `coverToggleLabel` 函数与编辑卡内覆盖项折叠区渲染（已被整体替换覆盖）。`fmt` 加入 desktop/access.go 导入（若尚未有）。

- [ ] **Step 5: 运行验证**

Run: `go test ./internal/desktop ./internal/bridge -count=1 && go build ./... && go vet ./...`
Expected: PASS（`grep -rn "accessOverridesFromRows\|coverToggleLabel\|showOverrides" internal/` 应为空）

- [ ] **Step 6: Commit**

```bash
git add internal/desktop
git commit -m "feat(desktop): 接入编辑器卡骨架重做——docJSON+PreviewDoc+TextArea"
```

---

### Task 9: desktop 模型映射表（角色行 + 1M + 双向同步）

**Files:**
- Modify: `internal/desktop/access.go`（`accessForm` 角色状态、角色表渲染、同步接线）
- Modify: `internal/desktop/access_test.go`

**Interfaces:**
- Consumes: `accessDocSetEnv`/`accessDocEnvStrings`/`accessParseOneM`（Task 8）、`acc.PreviewDoc`（Task 5）。
- Produces:
  - `accessClaudeRoles []accessRole{label, envKey, nameKey}`（5 角色）
  - `accessRoleFollow = "（跟随主模型）"`（下拉空值显示）
  - `accessForm` 新增 `roleModels/roleNames []string`、`roleOneM []bool`
  - `accessSyncRolesFromDoc()`（JSON→表单回填）、`accessPatchRole(i int)`（表单→JSON 补丁）

- [ ] **Step 1: 写失败测试（回填/补丁 round-trip + uiState 级）**

```go
func TestAccessRoleRoundTrip(t *testing.T) {
	// 补丁：选模型+1M+显示名
	raw, err := accessDocSetEnv(`{"env":{}}`, "ANTHROPIC_DEFAULT_SONNET_MODEL", "fast[1M]")
	if err != nil {
		t.Fatal(err)
	}
	raw, err = accessDocSetEnv(raw, "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME", "极速")
	if err != nil {
		t.Fatal(err)
	}
	env, err := accessDocEnvStrings(raw)
	if err != nil {
		t.Fatal(err)
	}
	name, oneM := accessParseOneM(env["ANTHROPIC_DEFAULT_SONNET_MODEL"])
	if name != "fast" || !oneM || env["ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"] != "极速" {
		t.Fatal(raw)
	}
	// 回填：Subagent 无显示名键；空键 = 跟随主模型
	env2, err := accessDocEnvStrings(`{"env":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	name2, oneM2 := accessParseOneM(env2["CLAUDE_CODE_SUBAGENT_MODEL"])
	if name2 != "" || oneM2 {
		t.Fatal("空值应回落跟随主模型")
	}
}

func TestAccessSyncRolesAndPatch(t *testing.T) {
	u := &uiState{}
	u.accessApp = "claude"
	u.accessForm = accessForm{docJSON: `{"env":{"ANTHROPIC_DEFAULT_HAIKU_MODEL":"fast[1M]"}}`}
	u.accessSyncRolesFromDoc()
	i := accessRoleIndexByEnvKey("ANTHROPIC_DEFAULT_HAIKU_MODEL")
	if u.accessForm.roleModels[i] != "fast" || !u.accessForm.roleOneM[i] {
		t.Fatalf("%v %v", u.accessForm.roleModels, u.accessForm.roleOneM)
	}
	u.accessForm.roleOneM[i] = false
	u.accessPatchRole(i)
	env, err := accessDocEnvStrings(u.accessForm.docJSON)
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "fast" {
		t.Fatal(u.accessForm.docJSON)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/desktop -run 'TestAccessRoleRoundTrip|TestAccessSyncRolesAndPatch' -v`
Expected: 编译 FAIL——`accessSyncRolesFromDoc`/`accessPatchRole`/`accessRoleIndexByEnvKey` 未定义（`TestAccessRoleRoundTrip` 引用的纯函数在 Task 8 已就位，它本身可绿，但包内编译错导致整体红）

- [ ] **Step 3: 实现**

`access.go`：

```go
// accessRole 是 Claude 模型映射一行（spec 2026-10-19 §3.2）：角色 → 文档 env
// 键。nameKey 为空的角色无「显示名称」列（Subagent 不进 /model 菜单）。
type accessRole struct{ label, envKey, nameKey string }

// accessClaudeRoles 是 5 角色固定顺序（与 cc-switch 对齐）。
var accessClaudeRoles = []accessRole{
	{"Sonnet", "ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"},
	{"Opus", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL_NAME"},
	{"Fable", "ANTHROPIC_DEFAULT_FABLE_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL_NAME"},
	{"Haiku", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME"},
	{"Subagent", "CLAUDE_CODE_SUBAGENT_MODEL", ""},
}

// accessRoleFollow 是角色下拉「未设置」的显示值（回落主模型）。
const accessRoleFollow = "（跟随主模型）"

// accessRoleIndexByEnvKey 返回角色下标（测试与补丁用）。
func accessRoleIndexByEnvKey(envKey string) int {
	for i, r := range accessClaudeRoles {
		if r.envKey == envKey {
			return i
		}
	}
	return -1
}

// accessSyncRolesFromDoc JSON→表单回填：解析成功才覆盖角色行（失败保留旧状态，
// cc-switch 同款「编辑到一半不闪烁」）。
func (u *uiState) accessSyncRolesFromDoc() {
	env, err := accessDocEnvStrings(u.accessForm.docJSON)
	if err != nil {
		return
	}
	f := &u.accessForm
	for len(f.roleModels) < len(accessClaudeRoles) {
		f.roleModels = append(f.roleModels, accessRoleFollow)
		f.roleNames = append(f.roleNames, "")
		f.roleOneM = append(f.roleOneM, false)
	}
	for i, r := range accessClaudeRoles {
		name, oneM := accessParseOneM(env[r.envKey])
		if name == "" {
			f.roleModels[i] = accessRoleFollow
		} else {
			f.roleModels[i] = name
		}
		f.roleOneM[i] = oneM
		if r.nameKey != "" {
			f.roleNames[i] = env[r.nameKey]
		}
	}
}

// accessPatchRole 表单→JSON 补丁：角色行变化写回文档 env（空/跟随=删键；
// 1M=值尾拼 "[1M]" 字面量）。
func (u *uiState) accessPatchRole(i int) {
	r := accessClaudeRoles[i]
	f := &u.accessForm
	val := f.roleModels[i]
	if val == accessRoleFollow {
		val = ""
	}
	if val != "" && f.roleOneM[i] {
		val += "[1M]"
	}
	if raw, err := accessDocSetEnv(f.docJSON, r.envKey, val); err == nil {
		f.docJSON = raw
	}
	if r.nameKey != "" {
		if raw, err := accessDocSetEnv(f.docJSON, r.nameKey, strings.TrimSpace(f.roleNames[i])); err == nil {
			f.docJSON = raw
		}
	}
}
```

`accessEditorCard`：在「逻辑模型」行之后、网关只读行之前插入角色表（仅 Claude tab）：

```go
			if u.accessApp == "claude" {
				ui.Text(c, "模型映射").Bold()
				ui.Text(c, "显示名称只影响 /model 菜单；1M 只是给 Claude Code 的上下文能力声明").TextColor(t.TextMuted)
				u.accessSyncRolesFromDoc() // 打开/回填后对齐（解析失败静默保留）
				for i, r := range accessClaudeRoles {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, r.label).Width(80).TextColor(t.TextMuted)
						if r.nameKey != "" {
							before := u.accessForm.roleNames[i]
							nameIn := ui.TextInput(c, &u.accessForm.roleNames[i]).Width(180).Placeholder("显示名称")
							if nameIn.Changed() && u.accessForm.roleNames[i] != before {
								u.accessPatchRole(i)
							}
						}
						opts := append([]string{accessRoleFollow}, labels...)
						before := u.accessForm.roleModels[i]
						sel := ui.Select(c, &u.accessForm.roleModels[i], opts)
						if sel.Changed() && u.accessForm.roleModels[i] != before {
							u.accessPatchRole(i)
						}
						beforeB := u.accessForm.roleOneM[i]
						cb := ui.Checkbox(c, &u.accessForm.roleOneM[i], "1M")
						if cb.Changed() && u.accessForm.roleOneM[i] != beforeB {
							u.accessPatchRole(i)
						}
					})
				}
			}
```

配置 JSON 区的 TextArea 后加回填接线：

```go
			ta := ui.TextArea(c, &u.accessForm.docJSON).Height(320).Grow(1)
			if ta.Changed() {
				u.accessSyncRolesFromDoc()
			}
```

- [ ] **Step 4: 运行验证**

Run: `go test ./internal/desktop -count=1 && go build ./... && go vet ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/desktop
git commit -m "feat(desktop): 模型映射表——角色行与配置 JSON 双向同步"
```

---

### Task 10: 文档收尾 + 全量验证

**Files:**
- Modify: `CLAUDE.md`（项目状态段落中接入配置描述）
- Modify: `docs/superpowers/specs/2026-10-18-agent-access-config-design.md`（头部标注取代关系）
- Modify: `internal/bridge/accessconfigs.go`（删除过渡残留 `AccessConfigView.Overrides` 与 `accessDocEnvMap`）

**Interfaces:** 删除 `AccessConfigView.Overrides`（Task 8 后已无消费方）与过渡助手 `accessDocEnvMap`。

- [ ] **Step 1: 清理 bridge 过渡残留**

- `AccessConfigView` 删 `Overrides` 字段；`view()` 删 `v.Overrides, _ = accessDocEnvMap(c.Settings)` 一行。
- 删 `accessDocEnvMap` 函数（`grep -rn "accessDocEnvMap\|\.Overrides" internal/ --include="*.go"` 确认无引用；bridge 测试若断言过 `v.Overrides` 同步删除断言）。
- `accessEnvDoc` 若已只剩测试引用（Task 7 后生产路径改走 `prepareDoc`），把它从 `accessconfigs.go` 移入 `accessconfigs_test.go`。

Run: `go build ./... && go test ./internal/bridge ./internal/desktop -count=1`
Expected: PASS

- [ ] **Step 2: 更新 CLAUDE.md**

在「项目状态与权威文档」的接入配置段落（2026-10-18 段）末尾追加：

> **接入文档化（2026-10-19）**：接入配置升级为整份 settings JSON 文档（`access_configs.settings` 列取代 `overrides`，**删库重建**——schema 合并单一 v1，旧库启动报错需删一次 `agentswitch.db`）；编辑器为 cc-switch 式「角色映射表 + 配置 JSON 双向同步」，网关三键（`ANTHROPIC_BASE_URL/AUTH_TOKEN/MODEL`，Codex 为 `OPENAI_BASE_URL/API_KEY`）锁定、保存归一化剔除未定制键、预览令牌遮蔽；网关路由匹配前剥离模型名尾部 `[1M]`——设计见 `docs/superpowers/specs/2026-10-19-access-full-settings-ccswitch-design.md`，旧「overrides 覆盖项」表述作废。

- [ ] **Step 3: 标注旧 spec**

`docs/superpowers/specs/2026-10-18-agent-access-config-design.md` 文件头部（标题下方）加：

> **状态（2026-10-19）：存储形态（overrides）、合成输入与 Claude 持久文件格式已被 `2026-10-19-access-full-settings-ccswitch-design.md` 取代；其余机制（应用投影、备份、令牌轮换联动、启动命令/打开终端、全局 env 层）仍有效。**

- [ ] **Step 4: 全量验证**

```bash
grep -rn "Overrides" internal/ --include="*.go" | grep -v _test || true
go build ./... && go vet ./... && go test ./... -count=1
go test -race ./... -count=1
```

Expected: grep 无输出（测试文件中亦应无 `AccessConfig.Overrides` 残留）；build/vet/test 全绿；`-race` 全绿。

- [ ] **Step 5: Commit**

```bash
git add CLAUDE.md internal/bridge docs/superpowers/specs/2026-10-18-agent-access-config-design.md
git commit -m "chore: 接入文档化收尾——清理过渡残留与文档标注"
```
