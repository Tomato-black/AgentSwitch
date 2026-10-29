# P1-1b 配置导出/导入实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 导出/导入渠道、逻辑模型、接入配置与全局 env，默认不含渠道 Key；导入前预览冲突，只新增不覆盖，导入后渠道需重新填 Key。

**Architecture:** bridge 新增 `config_transfer.go`：版本化 JSON（version=1），导出直接用 storage repo 组装；导入用现有 `Channels.Create`/`LogicalModels.Create`/`AccessConfigs.Create`/`SaveGlobalConfig`，按名称映射，冲突跳过。设置页新增「数据」卡：导出走 `mygo.Dialog.Save`，导入走 `mygo.Dialog.Open` + 预览确认。

**Tech Stack:** Go 1.27、MyGo Dialog、encoding/json；测试离线。

**Spec:** `docs/product/roadmap.md` P1-1；`docs/product/decisions.md` PD-4。

## Global Constraints

- 不自动 commit。
- **默认不导出渠道 Key**；导入后渠道 `HasKey=false`，需用户重填。
- 不覆盖已有同名渠道/逻辑模型/接入配置；冲突跳过并报告。
- 不导入网关端口/超时/令牌（机器相关/秘密）。
- 导出文件 0600；接入配置可能含自定义 env，UI 明确提示检查。
- 导入后不自动应用接入配置（不写 live）。

---

### Task 1: bridge 导出/预览/导入

**Files:**
- Create: `internal/bridge/config_transfer.go`
- Test: `internal/bridge/config_transfer_test.go`

**Interfaces:**
- Produces: `ExportSummary{Path string; Channels, LogicalModels, AccessConfigs int}`
- Produces: `ImportPreview{Version int; Channels, LogicalModels, AccessConfigs int; Conflicts, Warnings []string}`
- Produces: `ImportSummary{AddedChannels, AddedLogicalModels, AddedAccessConfigs int; Skipped, Errors []string}`
- Produces: `Gateway.ExportConfig(path string) (ExportSummary, error)`
- Produces: `Gateway.PreviewImport(path string) (ImportPreview, error)`
- Produces: `Gateway.ApplyImport(path string) (ImportSummary, error)`

- [x] **Step 1: 写失败测试**

```go
func TestExportImportRoundTrip(t *testing.T) {
	src := tempApp(t)
	// 建渠道（带 Key）、逻辑模型/目标、接入配置。
	ch, err := (Channels{A: src}).Create("", "主渠道", "https://api.example.com", "anthropic", "sk-secret", true, false)
	if err != nil { t.Fatal(err) }
	lm, err := (LogicalModels{A: src}).Create("my-code", "", ModelMeta{}, []TargetSpec{{ChannelID: ch.ID, ModelName: "m1", Priority: 0}})
	if err != nil { t.Fatal(err) }
	if _, err := (AccessConfigs{A: src}).Create("claude", "工作", lm.ID, "{}"); err != nil { t.Fatal(err) }

	dir := t.TempDir()
	path := filepath.Join(dir, "cfg.json")
	sum, err := (Gateway{A: src}).ExportConfig(path)
	if err != nil || sum.Channels != 1 || sum.LogicalModels != 1 || sum.AccessConfigs != 1 {
		t.Fatalf("export = %+v, %v", sum, err)
	}
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "sk-secret") {
		t.Fatal("export must not contain channel key")
	}

	dst := tempApp(t)
	pre, err := (Gateway{A: dst}).PreviewImport(path)
	if err != nil || pre.Channels != 1 || len(pre.Conflicts) != 0 {
		t.Fatalf("preview = %+v, %v", pre, err)
	}
	res, err := (Gateway{A: dst}).ApplyImport(path)
	if err != nil || res.AddedChannels != 1 || res.AddedLogicalModels != 1 || res.AddedAccessConfigs != 1 {
		t.Fatalf("import = %+v, %v", res, err)
	}
	chs := (Channels{A: dst}).List()
	if len(chs) != 1 || chs[0].HasKey {
		t.Fatalf("imported channels = %+v", chs)
	}
	if got := (AccessConfigs{A: dst}).List("claude"); len(got) != 1 || got[0].IsCurrent {
		t.Fatalf("imported access = %+v", got)
	}
}
```

- [x] **Step 2: 运行失败**（方法不存在）

- [x] **Step 3: 实现 `config_transfer.go`**

核心类型与流程：

```go
const configExportVersion = 1

type ExportSummary struct {
	Path                                  string
	Channels, LogicalModels, AccessConfigs int
}

type ImportPreview struct {
	Version                                  int
	Channels, LogicalModels, AccessConfigs   int
	Conflicts, Warnings                      []string
}

type ImportSummary struct {
	AddedChannels, AddedLogicalModels, AddedAccessConfigs int
	Skipped, Errors                                       []string
}

type exportBundle struct {
	Version        int                       `json:"version"`
	ExportedAt     time.Time                 `json:"exported_at"`
	AppVersion     string                    `json:"app_version"`
	ContainsSecrets bool                     `json:"contains_secrets"`
	Channels       []exportChannel           `json:"channels"`
	LogicalModels  []exportLogicalModel      `json:"logical_models"`
	AccessConfigs  []exportAccessConfig      `json:"access_configs"`
	GlobalEnv      map[string]map[string]string `json:"global_env,omitempty"`
}
```

`exportChannel{Name,Protocol,BaseURL,PresetID string; Enabled,AllowPrivate bool; Models []string}`；`exportLogicalModel{Name,Description,DisplayName string; ContextWindow int64; ReasoningLevels []string; DefaultReasoningLevel string; Enabled bool; Targets []exportTarget}`；`exportTarget{Channel,Model string; Priority int}`；`exportAccessConfig{AppType,Name,LogicalModel string; Settings json.RawMessage}`。

导出：
- `Channels().List()`；每个渠道 `ChannelModels().ListByChannel` 收集模型名。
- `LogicalModels().List()`；每个模型 `LogicalModelTargets().ListByModel`，渠道 ID→名称。
- 三个 appType 的 `AccessConfigs().ListByApp`，逻辑模型 ID→名称；`Settings` 作为 `json.RawMessage`（非法则 `{}`）。
- `Settings().Get("access.global.<app>")` 解析为 map；空则省略。
- `json.MarshalIndent`，`os.MkdirAll(dir,0700)`，`os.WriteFile(path,b,0600)`；path 为空时默认 `~/.agentswitch/exports/agentswitch-config-<unix>.json`。

预览：
- 读文件、`json.Unmarshal`；`Version != configExportVersion` 报错。
- 列出同名渠道/逻辑模型/`app+name` 接入配置作为 `Conflicts`。
- `Warnings`：有渠道→「渠道 Key 不在导出文件中，导入后需重新填写」；有接入配置→「接入配置可能包含自定义 env，请检查后再导入」。

导入：
- 重新解析；先建渠道（冲突跳过），`Channels.Create("", name, base, protocol, "", enabled, allowPrivate)`；`SetSuggestions`；记录 name→新 ID。
- 建逻辑模型：`Targets` 按渠道名映射；任一渠道缺失→`Errors` 并跳过该模型；无目标→跳过。`LogicalModels.Create(name, desc, ModelMeta{...}, specs)`；记录 name→新 ID。
- 建接入配置：逻辑模型名映射；缺失→`Errors` 跳过；`AccessConfigs.Create(app, name, id, string(Settings))`。
- `GlobalEnv`：`SaveGlobalConfig(app, env)`（每个 app 一次）。
- 返回计数/跳过/错误。

- [x] **Step 4: 运行通过** `go test ./internal/bridge -run TestExportImportRoundTrip -v`

---

### Task 2: 设置页「数据」卡

**Files:**
- Modify: `internal/desktop/state/state.go`（SettingsState）
- Modify: `internal/desktop/pages/settings/settings.go`
- Test: `internal/desktop/pages/settings/settings_test.go`

**Interfaces:**
- Consumes: `Gateway.ExportConfig/PreviewImport/ApplyImport`
- Produces: `SettingsState.DataMsg`、`ImportPreview *bridge.ImportPreview`、`ImportPath`、`ImportArm`

- [x] **Step 1: 写失败测试（预览渲染）**

```go
func TestSettingsImportPreviewRenders(t *testing.T) {
	st, _ := uitest.New(t)
	st.Settings.ImportPreview = &bridge.ImportPreview{
		Version: 1, Channels: 2, LogicalModels: 1, AccessConfigs: 1,
		Conflicts: []string{"渠道「主渠道」已存在"},
		Warnings:  []string{"渠道 Key 不在导出文件中，导入后需重新填写"},
	}
	st.Settings.ImportArm = true
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 1000)
	for _, want := range []string{"导入预览", "已存在", "重新填写", "确认导入"} {
		if !tt.HasText(want) { t.Fatalf("missing %q: %v", want, tt.Texts()) }
	}
}
```

- [x] **Step 2: 运行失败**

- [x] **Step 3: 实现**

`SettingsState` 增加：

```go
DataMsg       string
ImportPreview *bridge.ImportPreview
ImportPath    string
ImportArm     bool
```

`settings.go` 在「关于」卡前增加「数据」卡：

```go
uikit.Card(c, t, func() {
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "数据").Bold()
		ui.Text(c, "导出的配置默认不含渠道 Key；导入只新增，不覆盖同名项。").TextColor(t.TextMuted)
		ui.Row(c).Gap(8).Children(func() {
			if uikit.SoftButton(c, t, s, "gray", "导出配置").Clicked() {
				home, _ := os.UserHomeDir()
				path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
					Title: "导出配置", DefaultPath: filepath.Join(home, ".agentswitch", "exports", "agentswitch-config.json"),
					Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
				})
				if err != nil { st.Settings.DataMsg = "导出失败：" + err.Error() } else if path != "" {
					sum, err := st.Svc.Gateway.ExportConfig(path)
					if err != nil { st.Settings.DataMsg = "导出失败：" + err.Error() } else {
						st.Settings.DataMsg = fmt.Sprintf("已导出 %d 个渠道、%d 个逻辑模型、%d 个接入配置：%s", sum.Channels, sum.LogicalModels, sum.AccessConfigs, sum.Path)
					}
				}
			}
			if uikit.SoftButton(c, t, s, "gray", "导入配置").Clicked() {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
					Title: "导入配置", Filters: []mygo.FileFilter{{Name: "JSON", Extensions: []string{"json"}}},
				})
				if err == nil && len(paths) > 0 {
					pre, err := st.Svc.Gateway.PreviewImport(paths[0])
					if err != nil { st.Settings.DataMsg = "导入预览失败：" + err.Error() } else {
						st.Settings.ImportPreview = &pre
						st.Settings.ImportPath = paths[0]
						st.Settings.ImportArm = true
						st.Settings.DataMsg = ""
					}
				}
			}
		})
		uikit.Feedback(c, t, uikit.InferFeedback(st.Settings.DataMsg), st.Settings.DataMsg)
		if st.Settings.ImportArm && st.Settings.ImportPreview != nil {
			p := st.Settings.ImportPreview
			ui.Text(c, "导入预览").Bold()
			ui.Textf(c, "渠道 %d、逻辑模型 %d、接入配置 %d", p.Channels, p.LogicalModels, p.AccessConfigs)
			for _, cft := range p.Conflicts { ui.Text(c, "冲突："+cft).TextColor(t.Warning) }
			for _, w := range p.Warnings { ui.Text(c, w).TextColor(t.TextMuted) }
			ui.Row(c).Gap(8).Children(func() {
				if uikit.SoftButton(c, t, s, "gray", "取消导入").Clicked() {
					st.Settings.ImportArm = false; st.Settings.ImportPreview = nil; st.Settings.ImportPath = ""
				}
				if uikit.SoftButton(c, t, s, "act", "确认导入").Clicked() {
					res, err := st.Svc.Gateway.ApplyImport(st.Settings.ImportPath)
					if err != nil { st.Settings.DataMsg = "导入失败：" + err.Error() } else {
						st.Settings.DataMsg = fmt.Sprintf("已导入 %d 个渠道、%d 个逻辑模型、%d 个接入配置；渠道 Key 需重新填写", res.AddedChannels, res.AddedLogicalModels, res.AddedAccessConfigs)
					}
					st.Settings.ImportArm = false; st.Settings.ImportPreview = nil; st.Settings.ImportPath = ""
				}
			})
		}
	})
})
```

补 `os`、`path/filepath`、`AgentSwitch/internal/bridge` 导入。`mygo.FileFilter` 类型名按 MyGo v0.6.2 实际导出（`mygo.FileFilter`）。

- [x] **Step 4: 运行通过** `go test ./internal/desktop/pages/settings -v`

---

### Task 3: 全量回归与文档

- [x] `go build ./...`
- [x] `go vet ./...`
- [x] `go test ./...`
- [x] `go test -race ./internal/bridge ./internal/desktop/pages/settings`
- [x] README 增加导出/导入说明；roadmap P1-1b 标完成。

## Self-Review

- **Spec coverage:** 默认不含 Key、版本化、冲突跳过、预览、导入后不自动应用。
- **Placeholder scan:** 无 TBD/TODO。
- **Known limitation:** 未实现密码加密导出（含 Key）；导出文件可能含接入配置自定义 env；导入不覆盖同名项（需手动删除后重导）。


---

## 执行结果（2026-10-29）

- Task 1–3 全部完成；`go build ./...`、`go vet ./...`、`go test ./...` 通过。
- `go test -race ./internal/bridge ./internal/desktop/pages/settings` 通过。
- 新增测试：配置导出/预览/导入 round-trip、重复导入冲突跳过、设置页导入预览渲染。
- 未执行 git commit（等待用户验收）。
- 已知限制：未实现密码加密导出（含渠道 Key）；导出文件可能含接入配置自定义 env；导入不覆盖同名项。
