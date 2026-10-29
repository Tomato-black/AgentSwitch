# P1-1a 备份列表与选择恢复实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 把「只还原最近一份备份」升级为「备份列表 + 选择任意一份恢复」，恢复前仍自动快照当前状态。

**Architecture:** bridge 在现有 `~/.agentswitch/backups/<appType>/<ts>-<slot>` 文件名协议上加 `ListBackups`/`RestoreBackup(appType, ts)`；`RestoreLatestBackup` 保留为便捷入口。接入页新增备份子页，列出时间/文件/大小并提供还原确认。

**Tech Stack:** Go 1.27、MyGo v0.6.2；测试离线。

**Spec:** `docs/product/roadmap.md` P1-1（备份列表部分）；`docs/product/feature-gap-analysis.md` A5/B7。

## Global Constraints

- 不自动 commit。
- 不改备份文件名格式（`<unixms>[-<n>]-<slot>`）。
- 恢复前必须先 `backupLive` 快照当前 live，确保恢复本身可撤销。
- 备份内容含令牌：不写日志、不回显内容。
- 只读目录；列表失败返回错误给 UI，不 panic。

---

### Task 1: bridge 备份列表与指定恢复

**Files:**
- Modify: `internal/bridge/access_live.go`
- Test: `internal/bridge/accessconfigs_test.go`

**Interfaces:**
- Produces: `bridge.BackupInfo{Timestamp int64; Files []string; SizeBytes int64}`
- Produces: `AccessConfigs.ListBackups(appType string) ([]BackupInfo, error)`
- Produces: `AccessConfigs.RestoreBackup(appType string, ts int64) error`

- [x] **Step 1: 写失败测试**

```go
func TestListBackupsAndRestoreSelected(t *testing.T) {
	a := tempApp(t)
	home := t.TempDir()
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	livePath := filepath.Join(home, ".claude", "settings.json")
	os.WriteFile(livePath, []byte(`{"v":1}`), 0o600)
	if err := acc.Apply(v.ID); err != nil { // 备份 {"v":1}，写 live
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err != nil { // 再备份上一版 live
		t.Fatal(err)
	}
	backups, err := acc.ListBackups("claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) < 2 {
		t.Fatalf("backups = %+v", backups)
	}
	// 最新在前；恢复最旧一份，应回到 {"v":1} 的备份内容。
	oldest := backups[len(backups)-1]
	if err := acc.RestoreBackup("claude", oldest.Timestamp); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(livePath)
	if !strings.Contains(string(b), `"v":1`) {
		t.Fatalf("restored live = %s", b)
	}
}
```

- [x] **Step 2: 运行失败**

Run: `go test ./internal/bridge -run TestListBackupsAndRestoreSelected -v`  
Expected: FAIL（方法不存在）。

- [x] **Step 3: 实现**

在 `access_live.go` 增加：

```go
type BackupInfo struct {
	Timestamp int64
	Files     []string
	SizeBytes int64
}

// ListBackups 按时间倒序列出该 Agent 的备份集（一次备份 = 一个 ts 组）。
func (s AccessConfigs) ListBackups(appType string) ([]BackupInfo, error) {
	if err := validateAccessAppType(appType); err != nil {
		return nil, err
	}
	home := s.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return nil, err
		}
	}
	bdir := filepath.Join(home, ".agentswitch", "backups", appType)
	ents, err := os.ReadDir(bdir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	byTS := map[int64]*BackupInfo{}
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		ts, slot, ok := parseBackupName(e.Name())
		if !ok {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			return nil, err
		}
		info := byTS[ts]
		if info == nil {
			info = &BackupInfo{Timestamp: ts}
			byTS[ts] = info
		}
		info.Files = append(info.Files, slot)
		info.SizeBytes += fi.Size()
	}
	out := make([]BackupInfo, 0, len(byTS))
	for _, info := range byTS {
		out = append(out, *info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Timestamp > out[j].Timestamp })
	return out, nil
}

// RestoreBackup 恢复指定时间戳的备份集；恢复前先快照当前 live。
func (s AccessConfigs) RestoreBackup(appType string, ts int64) error {
	if err := validateAccessAppType(appType); err != nil {
		return err
	}
	home := s.Home
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return err
		}
	}
	bdir := filepath.Join(home, ".agentswitch", "backups", appType)
	ents, err := os.ReadDir(bdir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("没有可恢复的备份")
		}
		return err
	}
	paths := map[string]string{}
	for _, slot := range s.liveSlots(home, appType) {
		paths[slot.name] = slot.path
	}
	// 先快照当前 live：让恢复动作本身可撤销。
	if err := s.backupLive(appType); err != nil {
		return err
	}
	restored := false
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		fileTS, slot, ok := parseBackupName(e.Name())
		if !ok || fileTS != ts {
			continue
		}
		dst, ok := paths[slot]
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(bdir, e.Name()))
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		if err := atomicWriteFile0600(dst, data); err != nil {
			return err
		}
		restored = true
	}
	if !restored {
		return fmt.Errorf("备份不存在或没有可恢复的文件")
	}
	return nil
}
```

把 `RestoreLatestBackup` 改为先 `ListBackups` 取 `[0].Timestamp`，再调用 `RestoreBackup`（保留旧 API 与测试）。

- [x] **Step 4: 运行通过**

Run: `go test ./internal/bridge -run 'TestListBackupsAndRestoreSelected|TestRestore' -v`  
Expected: PASS。

---

### Task 2: 接入页备份子页

**Files:**
- Modify: `internal/desktop/state/state.go`（AccessState）
- Modify: `internal/desktop/pages/access/access.go`
- Create: `internal/desktop/pages/access/backup.go`
- Test: `internal/desktop/pages/access/access_test.go`

**Interfaces:**
- Consumes: `AccessConfigs.ListBackups`/`RestoreBackup`
- Produces: `AccessState.BackupOpen`、`Backups`、`BackupMsg`、`RestoreTS`、`RestoreArm`

- [x] **Step 1: 写失败测试**

```go
func TestAccessBackupPageListsAndConfirms(t *testing.T) {
	st, _ := uitest.New(t)
	st.Access.App = "claude"
	st.Access.BackupOpen = true
	st.Access.Backups = []bridge.BackupInfo{{Timestamp: 1793253937554, Files: []string{"settings.json"}, SizeBytes: 2048}}
	tt := ui.NewTester(func(c *ui.Context) { backupPage(c, st, configs(st)) }, 1000, 800)
	if !tt.HasText("备份与恢复") || !tt.HasText("settings.json") {
		t.Fatalf("backup page = %v", tt.Texts())
	}
	if !tt.HasText("还原") {
		t.Fatal("restore button missing")
	}
}
```

- [x] **Step 2: 运行失败**

- [x] **Step 3: 实现**

`AccessState` 增加：

```go
BackupOpen bool
Backups    []bridge.BackupInfo
BackupMsg  string
RestoreTS  int64
RestoreArm bool
```

`access.go`：

- 在编辑器早退之后增加：

```go
if st.Access.BackupOpen {
	backupPage(c, st, acc)
	return
}
```

- 把「还原最近备份」按钮改为：

```go
if uikit.SoftButton(c, t, s, "gray", "备份与恢复").Clicked() {
	st.Access.BackupOpen = true
	st.Access.BackupMsg = ""
	st.Access.Backups, _ = acc.ListBackups(st.Access.App)
}
```

新建 `backup.go`：

```go
package access

import (
	"fmt"
	"time"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

func backupPage(c *ui.Context, st *state.State, acc bridge.AccessConfigs) {
	t := c.Theme()
	s := uikit.Softs(t)
	ui.Column(c).Gap(12).Grow(1).Children(func() {
		uikit.PageHeader(c, t, uikit.PageHeaderOpts{
			Title: "备份与恢复", Subtitle: "选择一份历史备份覆盖当前 Agent 配置",
			BackLabel: "← 返回接入",
			OnBack: func() {
				st.Access.BackupOpen = false
				st.Access.RestoreArm = false
				st.Access.BackupMsg = ""
			},
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(10).Children(func() {
				uikit.Feedback(c, t, uikit.InferFeedback(st.Access.BackupMsg), st.Access.BackupMsg)
				if len(st.Access.Backups) == 0 {
					uikit.EmptyStateBare(c, t, uikit.EmptyStateOpts{Title: "还没有备份", Hint: "应用配置时会自动备份当前文件"})
				}
				for _, b := range st.Access.Backups {
					info := b
					uikit.Card(c, t, func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, time.UnixMilli(info.Timestamp).Format("2006-01-02 15:04:05")).Bold()
							ui.Text(c, fmt.Sprintf("%d 个文件 · %d B", len(info.Files), info.SizeBytes)).TextColor(t.TextMuted).Grow(1)
							if uikit.SoftButton(c, t, s, "danger", "还原").Clicked() {
								st.Access.RestoreTS = info.Timestamp
								st.Access.RestoreArm = true
							}
						})
						ui.Text(c, strings.Join(info.Files, "、")).TextColor(t.TextMuted).SingleLine()
					})
				}
			})
		})
	})
	if st.Access.RestoreArm {
		uikit.ConfirmDialog(c, t, uikit.ConfirmOpts{
			Open: &st.Access.RestoreArm, Title: "还原备份",
			Message: "将用所选备份覆盖当前 Agent 配置文件（覆盖前会先备份当前状态，可再次还原）。",
			CancelLabel: "取消", ConfirmLabel: "还原", Danger: true,
			OnConfirm: func() {
				if err := acc.RestoreBackup(st.Access.App, st.Access.RestoreTS); err != nil {
					st.Access.BackupMsg = "还原失败：" + err.Error()
				} else {
					st.Access.BackupMsg = "已还原所选备份"
					st.Access.Backups, _ = acc.ListBackups(st.Access.App)
				}
			},
		})
	}
}
```

补 `strings` 导入。

- [x] **Step 4: 运行通过** `go test ./internal/desktop/pages/access -v`

---

### Task 3: 全量回归

- [x] `go build ./...`
- [x] `go vet ./...`
- [x] `go test ./...`
- [x] `go test -race ./internal/bridge ./internal/desktop/pages/access`
- [x] 更新 README「备份」描述与 roadmap P1-1 状态。

## Self-Review

- **Spec coverage:** 列表、时间/大小、选择恢复、恢复前快照、无备份空态。
- **Placeholder scan:** 无 TBD/TODO。
- **Type consistency:** `bridge.BackupInfo` 由 bridge 定义、UI 直接使用；`RestoreLatestBackup` 保留旧行为。


---

## 执行结果（2026-10-29）

- Task 1–3 全部完成；`go build ./...`、`go vet ./...`、`go test ./...` 通过。
- `go test -race ./internal/bridge ./internal/desktop/pages/access` 通过。
- 新增测试：备份列表/选择恢复、接入页备份子页渲染。
- 未执行 git commit（等待用户验收）。
- 范围说明：本计划只完成 P1-1a（备份列表/选择恢复）；配置导出/导入/迁移（P1-1b）待单独计划。
