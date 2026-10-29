# P0-1 请求/路由历史持久化实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 请求/路由记录持久化到 SQLite，重启后设置页仍能看到最近请求与尝试链；提供筛选与诊断导出；不阻塞网关热路径、不存 prompt/response 正文。

**Architecture:** 新增 `request_history` 表（migration v5）。gateway 仍只通过 `OnRecord` 回调上报；app 层用有界 channel + 单写协程异步落库，启动时把最近 100 条灌回内存环，并清理超过保留期的记录（最终默认 7 天）。bridge 暴露查询/导出；设置页复用现有「最近请求」列表，增加筛选与「导出诊断」。

**Tech Stack:** Go 1.27、modernc.org/sqlite、MyGo v0.6.2；测试全部离线。

**Spec:** `docs/product/roadmap.md` P0-1；`docs/product/feature-gap-analysis.md` A1；`docs/product/decisions.md` PD-3/PD-18。

## Global Constraints

- **不自动 git commit**；工作区已有未提交改动。
- **不改 gateway 三入站契约**；只通过 `app` 层 OnRecord 落库。
- **不存 prompt/response 正文、Authorization、密钥**；只存 `telemetry.RequestRecord` 已有脱敏字段。
- **不阻塞热路径**：有界 channel + 非阻塞入队；队列满丢弃并计数/日志。
- **默认保留 7 天**（2026-10-29 用户决定）；每次启动清理一次。
- 测试离线；最终跑 `-race`。

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/storage/storage.go` | `RequestHistoryRecord`/`RequestAttempt`/`RequestHistoryRepo` |
| `internal/storage/request_history_repo.go` | 新表读写（Add/ListRecent/DeleteOlderThan） |
| `internal/storage/db.go` | migration v5 + 旧库守卫 + `RequestHistory()` |
| `internal/storage/storage_test.go` | 迁移版本 `[1 2 4 5]`、repo round-trip |
| `internal/app/app.go` | 异步 writer、启动灌入 ring、Close flush、History/ExportHistory |
| `internal/app/app_test.go` | 灌入/持久化/导出测试 |
| `internal/bridge/bridge.go` | `Gateway.History`/`ExportHistory` |
| `internal/bridge/bridge_test.go` | bridge 查询/导出测试 |
| `internal/desktop/state/state.go` | `SettingsState.HistoryFilter` |
| `internal/desktop/pages/settings/settings.go` | 筛选、导出按钮、日期 |
| `internal/desktop/pages/settings/settings_test.go` | 筛选/导出 UI 测试 |

---

### Task 1: storage 新增 `request_history`

**Files:**
- Modify: `internal/storage/storage.go`
- Modify: `internal/storage/db.go`
- Create: `internal/storage/request_history_repo.go`
- Test: `internal/storage/storage_test.go`

**Interfaces:**
- Produces: `storage.RequestHistoryRecord{ID, StartedAt, Model, UpstreamModel, Channel, Account, Status, DurationMs, FailoverCount, Streaming, ErrorClass, Attempts}`
- Produces: `storage.RequestAttempt{Channel, Model, Outcome, Reason, DurationMs}`
- Produces: `Store.RequestHistory() RequestHistoryRepo`
- Produces: `RequestHistoryRepo.Add/ListRecent(limit)/DeleteOlderThan(cutoffMs)`

- [x] **Step 1: 写失败测试**

在 `internal/storage/storage_test.go` 增加：

```go
func TestRequestHistoryRepoRoundTripAndRetention(t *testing.T) {
	s := tempStore(t)
	r := s.RequestHistory()
	old := RequestHistoryRecord{
		ID: "old", StartedAt: 1000, Model: "m", Channel: "c",
		Status: 200, DurationMs: 12, FailoverCount: 1, Streaming: true,
		Attempts: []RequestAttempt{{Channel: "c", Model: "u", Outcome: "ok", DurationMs: 12}},
	}
	if err := r.Add(old); err != nil {
		t.Fatal(err)
	}
	if err := r.Add(RequestHistoryRecord{ID: "new", StartedAt: 2000, Model: "m2", Channel: "c2", Status: 429}); err != nil {
		t.Fatal(err)
	}
	got, err := r.ListRecent(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "new" || got[1].ID != "old" {
		t.Fatalf("ListRecent = %+v", got)
	}
	if !got[1].Streaming || len(got[1].Attempts) != 1 || got[1].Attempts[0].Outcome != "ok" {
		t.Fatalf("attempts round-trip = %+v", got[1])
	}
	n, err := r.DeleteOlderThan(1500)
	if err != nil || n != 1 {
		t.Fatalf("DeleteOlderThan = %d, %v", n, err)
	}
	got, _ = r.ListRecent(10)
	if len(got) != 1 || got[0].ID != "new" {
		t.Fatalf("after delete = %+v", got)
	}
}
```

迁移版本断言改为 `[1 2 4 5]`（`TestMigrationIdempotent`）。

- [x] **Step 2: 运行失败**

Run: `go test ./internal/storage -run 'TestRequestHistoryRepoRoundTripAndRetention|TestMigrationIdempotent' -v`  
Expected: FAIL（`RequestHistory` 不存在 / 版本仍为 `[1 2 4]`）。

- [x] **Step 3: 实现**

`internal/storage/storage.go` 增加类型与接口：

```go
type RequestAttempt struct {
	Channel, Model, Outcome, Reason string
	DurationMs                      int64
}

type RequestHistoryRecord struct {
	ID            string
	StartedAt     int64
	Model         string
	UpstreamModel string
	Channel       string
	Account       string
	Status        int
	DurationMs    int64
	FailoverCount int
	Streaming     bool
	ErrorClass    string
	Attempts      []RequestAttempt
}

type RequestHistoryRepo interface {
	Add(rec RequestHistoryRecord) error
	ListRecent(limit int) ([]RequestHistoryRecord, error)
	DeleteOlderThan(cutoffMs int64) (int64, error)
}
```

`Store` 增加 `RequestHistory() RequestHistoryRepo`。

`internal/storage/db.go` 增加：

```go
const migrationV5 = `
CREATE TABLE request_history (
  id TEXT PRIMARY KEY,
  started_at INTEGER NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  upstream_model TEXT NOT NULL DEFAULT '',
  channel TEXT NOT NULL DEFAULT '',
  account TEXT NOT NULL DEFAULT '',
  status INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  failover_count INTEGER NOT NULL DEFAULT 0,
  streaming INTEGER NOT NULL DEFAULT 0,
  error_class TEXT NOT NULL DEFAULT '',
  attempts_json TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX idx_request_history_started_at ON request_history(started_at DESC);
`
```

`migrations` 增加 `{5, migrationV5}`；`dbStore` 增加：

```go
func (s *dbStore) RequestHistory() RequestHistoryRepo { return &requestHistoryRepo{db: s.db} }
```

`rejectLegacySchema` 增加 v5 守卫：已记 5 时 `request_history` 表必须存在。

新建 `internal/storage/request_history_repo.go`：

```go
package storage

import (
	"encoding/json"
	"database/sql"
)

type requestHistoryRepo struct{ db *sql.DB }

const requestHistoryCols = `id,started_at,model,upstream_model,channel,account,status,duration_ms,failover_count,streaming,error_class,attempts_json`

func (r *requestHistoryRepo) Add(rec RequestHistoryRecord) error {
	attempts, err := json.Marshal(rec.Attempts)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`INSERT INTO request_history(`+requestHistoryCols+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO NOTHING`,
		rec.ID, rec.StartedAt, rec.Model, rec.UpstreamModel, rec.Channel, rec.Account,
		rec.Status, rec.DurationMs, rec.FailoverCount, boolInt(rec.Streaming),
		rec.ErrorClass, string(attempts))
	return err
}

func (r *requestHistoryRepo) ListRecent(limit int) ([]RequestHistoryRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.Query(`SELECT `+requestHistoryCols+`
		FROM request_history ORDER BY started_at DESC, rowid DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestHistoryRecord
	for rows.Next() {
		var rec RequestHistoryRecord
		var streaming int
		var attempts string
		if err := rows.Scan(&rec.ID, &rec.StartedAt, &rec.Model, &rec.UpstreamModel,
			&rec.Channel, &rec.Account, &rec.Status, &rec.DurationMs,
			&rec.FailoverCount, &streaming, &rec.ErrorClass, &attempts); err != nil {
			return nil, err
		}
		rec.Streaming = streaming == 1
		if attempts != "" {
			_ = json.Unmarshal([]byte(attempts), &rec.Attempts)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *requestHistoryRepo) DeleteOlderThan(cutoffMs int64) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM request_history WHERE started_at < ?`, cutoffMs)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
```

- [x] **Step 4: 运行通过**

Run: `go test ./internal/storage`  
Expected: PASS。

---

### Task 2: app 异步落库、启动灌入 ring、Close flush

**Files:**
- Modify: `internal/app/app.go`
- Test: `internal/app/app_test.go`

**Interfaces:**
- Produces: `App.History(limit int) []telemetry.RequestRecord`
- Produces: `App.ExportHistory(dir string) (string, error)`
- Produces: `App.StartGateway` 的 `OnRecord` 同时入队持久化

- [x] **Step 1: 写失败测试**

在 `internal/app/app_test.go` 增加：

```go
func TestHistoryPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h.db")
	a, err := Open(Options{DBPath: path, LogPath: filepath.Join(t.TempDir(), "h.log"), Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	rec := telemetry.RequestRecord{
		ID: "r1", StartedAt: time.Now(), Model: "m", Channel: "c", Status: 200,
		DurationMs: 12, FailoverCount: 1,
		Attempts: []telemetry.AttemptRecord{{Channel: "c", Model: "u", Outcome: "ok", DurationMs: 12}},
	}
	a.enqueueRecord(rec)
	a.stopHistory()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}

	b, err := Open(Options{DBPath: path, LogPath: filepath.Join(t.TempDir(), "h2.log"), Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	got := b.Recent()
	if len(got) != 1 || got[0].ID != "r1" || len(got[0].Attempts) != 1 {
		t.Fatalf("Recent after reopen = %+v", got)
	}
}
```

- [x] **Step 2: 运行失败**

Run: `go test ./internal/app -run TestHistoryPersistsAcrossReopen -v`  
Expected: FAIL（`enqueueRecord/stopHistory` 不存在或记录未持久化）。

- [x] **Step 3: 实现**

`App` 增加字段：

```go
historyCh    chan telemetry.RequestRecord
historyWG    sync.WaitGroup
historyOnce  sync.Once
```

`Open` 在 `a.setState(...)` 之前调用 `a.startHistory()`。

`StartGateway` 的 `OnRecord` 回调第一行加：

```go
a.enqueueRecord(rec)
```

新增方法：

```go
func (a *App) startHistory() {
	a.historyCh = make(chan telemetry.RequestRecord, 256)
	a.historyWG.Add(1)
	go func() {
		defer a.historyWG.Done()
		for rec := range a.historyCh {
			if err := a.store.RequestHistory().Add(historyToStorage(rec)); err != nil {
				a.log.Warn("request history persist failed", "request_id", rec.ID, "err", err.Error())
			}
		}
	}()
	if recs, err := a.store.RequestHistory().ListRecent(100); err == nil {
		for i := len(recs) - 1; i >= 0; i-- {
			a.ring.Add(historyFromStorage(recs[i]))
		}
	}
	_, _ = a.store.RequestHistory().DeleteOlderThan(time.Now().Add(-7 * 24 * time.Hour).UnixMilli())
}

func (a *App) enqueueRecord(rec telemetry.RequestRecord) {
	if a.historyCh == nil {
		return
	}
	select {
	case a.historyCh <- rec:
	default:
		a.log.Warn("request history queue full; dropping record", "request_id", rec.ID)
	}
}

func (a *App) stopHistory() {
	a.historyOnce.Do(func() {
		if a.historyCh != nil {
			close(a.historyCh)
		}
		a.historyWG.Wait()
	})
}

func (a *App) History(limit int) []telemetry.RequestRecord {
	recs, err := a.store.RequestHistory().ListRecent(limit)
	if err != nil {
		return nil
	}
	out := make([]telemetry.RequestRecord, 0, len(recs))
	for _, r := range recs {
		out = append(out, historyFromStorage(r))
	}
	return out
}

func (a *App) ExportHistory(dir string) (string, error) {
	recs := a.History(10000)
	payload := struct {
		ExportedAt time.Time                `json:"exported_at"`
		Version    string                   `json:"version"`
		Records    []telemetry.RequestRecord `json:"records"`
	}{time.Now(), a.opts.Version, recs}
	b, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, fmt.Sprintf("request-history-%d.json", time.Now().UnixMilli()))
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func historyToStorage(rec telemetry.RequestRecord) storage.RequestHistoryRecord {
	attempts := make([]storage.RequestAttempt, 0, len(rec.Attempts))
	for _, a := range rec.Attempts {
		attempts = append(attempts, storage.RequestAttempt{
			Channel: a.Channel, Model: a.Model, Outcome: a.Outcome,
			Reason: a.Reason, DurationMs: a.DurationMs,
		})
	}
	return storage.RequestHistoryRecord{
		ID: rec.ID, StartedAt: rec.StartedAt.UnixMilli(), Model: rec.Model,
		UpstreamModel: rec.UpstreamModel, Channel: rec.Channel, Account: rec.Account,
		Status: rec.Status, DurationMs: rec.DurationMs, FailoverCount: rec.FailoverCount,
		Streaming: rec.Streaming, ErrorClass: rec.ErrorClass, Attempts: attempts,
	}
}

func historyFromStorage(rec storage.RequestHistoryRecord) telemetry.RequestRecord {
	attempts := make([]telemetry.AttemptRecord, 0, len(rec.Attempts))
	for _, a := range rec.Attempts {
		attempts = append(attempts, telemetry.AttemptRecord{
			Channel: a.Channel, Model: a.Model, Outcome: a.Outcome,
			Reason: a.Reason, DurationMs: a.DurationMs,
		})
	}
	return telemetry.RequestRecord{
		ID: rec.ID, StartedAt: time.UnixMilli(rec.StartedAt), Model: rec.Model,
		UpstreamModel: rec.UpstreamModel, Channel: rec.Channel, Account: rec.Account,
		Status: rec.Status, DurationMs: rec.DurationMs, FailoverCount: rec.FailoverCount,
		Streaming: rec.Streaming, ErrorClass: rec.ErrorClass, Attempts: attempts,
	}
}
```

`Close` 在关闭 store 前调用 `a.stopHistory()`。

- [x] **Step 4: 运行通过**

Run: `go test ./internal/app -run TestHistoryPersistsAcrossReopen -v`  
Expected: PASS；再跑 `go test ./internal/app`。

---

### Task 3: bridge 暴露查询与诊断导出

**Files:**
- Modify: `internal/bridge/bridge.go`
- Test: `internal/bridge/bridge_test.go`

**Interfaces:**
- Produces: `Gateway.History(limit int) []telemetry.RequestRecord`
- Produces: `Gateway.ExportHistory() (string, error)`

- [x] **Step 1: 写失败测试**

```go
func TestGatewayHistoryAndExport(t *testing.T) {
	a := tempApp(t)
	g := Gateway{A: a}
	if _, err := a.Store().RequestHistory().Add(storage.RequestHistoryRecord{
		ID: "r1", StartedAt: time.Now().UnixMilli(), Model: "m", Channel: "c", Status: 200,
	}); err != nil {
		t.Fatal(err)
	}
	got := g.History(10)
	if len(got) != 1 || got[0].ID != "r1" {
		t.Fatalf("History = %+v", got)
	}
	t.Setenv("HOME", t.TempDir())
	path, err := g.ExportHistory()
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"r1"`) {
		t.Fatalf("export = %s", b)
	}
	if strings.Contains(string(b), "Authorization") || strings.Contains(string(b), "sk-") {
		t.Fatal("export must not contain secrets")
	}
}
```

- [x] **Step 2: 运行失败**

Run: `go test ./internal/bridge -run TestGatewayHistoryAndExport -v`  
Expected: FAIL（方法不存在）。

- [x] **Step 3: 实现**

`internal/bridge/bridge.go` 增加：

```go
func (g Gateway) History(limit int) []telemetry.RequestRecord { return g.A.History(limit) }

func (g Gateway) ExportHistory() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return g.A.ExportHistory(filepath.Join(home, ".agentswitch", "exports"))
}
```

补 `os`、`path/filepath` 导入。

- [x] **Step 4: 运行通过**

Run: `go test ./internal/bridge -run TestGatewayHistoryAndExport -v`  
Expected: PASS。

---

### Task 4: 设置页筛选 + 导出诊断

**Files:**
- Modify: `internal/desktop/state/state.go:210-220`
- Modify: `internal/desktop/pages/settings/settings.go:119-170`
- Test: `internal/desktop/pages/settings/settings_test.go`

**Interfaces:**
- Consumes: `st.Recent`（启动时已灌入持久历史）、`Gateway.ExportHistory`
- Produces: `SettingsState.HistoryFilter string`

- [x] **Step 1: 写失败测试（筛选）**

在 `settings_test.go` 增加：

```go
func TestSettingsHistoryFilter(t *testing.T) {
	st, _ := uitest.New(t)
	st.Recent = []telemetry.RequestRecord{
		{ID: "a", Model: "my-code", Channel: "main", Status: 200},
		{ID: "b", Model: "other", Channel: "backup", Status: 429},
	}
	st.Settings.HistoryFilter = "main"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 1000)
	if !tt.HasText("my-code") || tt.HasText("other") {
		t.Fatalf("筛选未生效：%v", tt.Texts())
	}
}
```

- [x] **Step 2: 运行失败**

Run: `go test ./internal/desktop/pages/settings -run TestSettingsHistoryFilter -v`  
Expected: FAIL（无筛选输入 / 未过滤）。

- [x] **Step 3: 实现**

`state.SettingsState` 增加 `HistoryFilter string`。

`settings.go` 在「最近请求」前增加筛选行：

```go
ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
	ui.Text(c, "最近请求").Bold()
	ui.TextInput(c, &st.Settings.HistoryFilter).Grow(1).
		Placeholder("筛选：模型 / 渠道 / 状态")
	if uikit.SoftButton(c, t, s, "gray", "导出诊断").Clicked() {
		path, err := st.Svc.Gateway.ExportHistory()
		if err != nil {
			st.Settings.Msg = "导出诊断失败：" + err.Error()
		} else {
			mygo.Clipboard.Write(transfer.TextData(path))
			st.Settings.Msg = "诊断已导出（路径已复制）：" + path
		}
	}
})
```

把 `for _, r := range st.Recent[:shown]` 改为先用筛选：

```go
filter := strings.TrimSpace(st.Settings.HistoryFilter)
for _, r := range st.Recent {
	if filter != "" && !strings.Contains(r.Model, filter) &&
		!strings.Contains(r.Channel, filter) &&
		!strings.Contains(fmt.Sprint(r.Status), filter) {
		continue
	}
	...
}
```

`recentLine` 时间改为 `r.StartedAt.Format("01-02 15:04:05")`。

- [x] **Step 4: 运行通过**

Run: `go test ./internal/desktop/pages/settings -v`  
Expected: PASS。

---

### Task 5: 全量回归

- [x] `go build ./...`
- [x] `go vet ./...`
- [x] `go test ./...`
- [x] `go test -race ./internal/storage ./internal/app ./internal/bridge ./internal/desktop/...`
- [x] 手工核对：导出的 JSON 不含 `Authorization`/`sk-`/prompt 正文；重启应用后设置页仍显示历史。

---

## Self-Review

- **Spec coverage:** 持久化（Task 1/2）、重启可见（Task 2 灌入 ring）、筛选（Task 4）、导出（Task 2/3/4）、不阻塞热路径（有界 channel）、不存正文（只存 RequestRecord 字段）。
- **Placeholder scan:** 无 TBD/TODO。
- **Type consistency:** `storage.RequestHistoryRecord`/`RequestAttempt` 在 app 转换中字段一一对应；`Gateway.History` 返回 `telemetry.RequestRecord`，与现有 UI 一致。
- **Known limitation:** 默认只灌入最近 100 条到内存环；更早历史可通过 `ExportHistory` 查看，后续 P1 再做独立历史视图与时间筛选。


---

## 执行结果（2026-10-29）

- Task 1–5 全部完成；`go build ./...`、`go vet ./...`、`go test ./...` 通过。
- `go test -race ./internal/storage ./internal/app ./internal/bridge ./internal/desktop/...` 通过。
- 新增测试：storage round-trip/retention、app 重启存活、bridge 查询/导出、设置页筛选。
- 未执行 git commit（等待用户验收）。
- 已知限制：设置页只展示最近 100 条（启动灌入内存环）；更早历史通过「导出诊断」JSON 查看；时间/状态筛选后续 P1 再扩展。
