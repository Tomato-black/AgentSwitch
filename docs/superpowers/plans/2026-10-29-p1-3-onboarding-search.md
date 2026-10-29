# P1-3 首次上手闭环与搜索实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 空库默认落到渠道页；未完成「渠道→逻辑模型→接入」时显示三步清单；空态随数据变化并可跳转；渠道/逻辑模型/接入/预设支持搜索。

**Architecture:** bridge 增加 `Setup()` 计数；`state.New` 按数据选默认页；`shell.go` 渲染三步清单；各列表页增加客户端过滤字符串；预设网格增加搜索框。无 schema 变更。

**Tech Stack:** Go 1.27、MyGo v0.6.2；测试离线。

**Spec:** `docs/product/roadmap.md` P1-3；`docs/product/feature-gap-analysis.md` B12/B14。

## Global Constraints

- 不自动 commit。
- 不新增向导页/多步弹窗；只做清单与空态跳转。
- 搜索为客户端过滤；>8 项才显示（预设始终显示）。
- 不改路由/协议/接入文档格式。

---

### Task 1: bridge Setup 计数 + 默认页

**Files:**
- Modify: `internal/bridge/bridge.go`
- Modify: `internal/desktop/state/state.go`
- Test: `internal/bridge/bridge_test.go`、`internal/desktop/state/state_test.go`

**Interfaces:**
- Produces: `bridge.SetupStatus{Channels, LogicalModels, AppliedAccess int}`
- Produces: `Gateway.Setup() SetupStatus`
- Produces: `state.New` 空库默认 `Page="channels"`

- [x] **Step 1: 写失败测试**

```go
func TestGatewaySetup(t *testing.T) {
	a := tempApp(t)
	g := Gateway{A: a}
	if s := g.Setup(); s.Channels != 0 || s.LogicalModels != 0 || s.AppliedAccess != 0 {
		t.Fatalf("empty setup = %+v", s)
	}
	// 建渠道 + 逻辑模型 + 应用接入后计数递增（复用已有夹具）。
}
```

- [x] **Step 2: 实现**

```go
type SetupStatus struct {
	Channels, LogicalModels, AppliedAccess int
}

func (g Gateway) Setup() SetupStatus {
	var s SetupStatus
	if chs, err := g.A.Store().Channels().List(); err == nil {
		s.Channels = len(chs)
	}
	if lms, err := g.A.Store().LogicalModels().List(); err == nil {
		s.LogicalModels = len(lms)
	}
	for _, app := range []string{"claude", "codex", "claude_desktop"} {
		if _, err := g.A.Store().AccessConfigs().Current(app); err == nil {
			s.AppliedAccess++
		}
	}
	return s
}
```

`state.New` 改为：

```go
page := "logical"
if len(svc.Channels.List()) == 0 {
	page = "channels"
}
return &State{Svc: svc, Recent: svc.Gateway.Recent(), Page: page}
```

- [x] **Step 3: 运行通过** `go test ./internal/bridge -run TestGatewaySetup`、`go test ./internal/desktop/state`

---

### Task 2: 三步清单

**Files:**
- Modify: `internal/desktop/shell.go`
- Test: `internal/desktop/shell_test.go`

- [x] **Step 1: 写失败测试**

```go
func TestSetupChecklistRendersWhenIncomplete(t *testing.T) {
	st, _ := uitest.New(t)
	u := &uiState{State: st}
	tt := ui.NewTester(func(c *ui.Context) { setupChecklist(c, u) }, 1000, 400)
	for _, want := range []string{"开始使用", "渠道", "逻辑模型", "接入"} {
		if !tt.HasText(want) { t.Fatalf("missing %q: %v", want, tt.Texts()) }
	}
}
```

- [x] **Step 2: 实现**

`shell.go` 增加：

```go
func setupChecklist(c *ui.Context, u *uiState) {
	s := u.Svc.Gateway.Setup()
	if s.Channels > 0 && s.LogicalModels > 0 && s.AppliedAccess > 0 {
		return
	}
	t := c.Theme()
	uikit.Card(c, t, func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "开始使用").Bold()
			ui.Text(c, "按顺序完成三步；完成后此卡片自动消失").TextColor(t.TextMuted)
			step := func(done bool, label, page string) {
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					mark := "○"
					if done { mark = "✓" }
					ui.Text(c, mark+" "+label)
					ui.Box(c).Grow(1)
					if !done {
						if uikit.SoftButton(c, t, uikit.Softs(t), "gray", "去完成").Clicked() {
							u.Go(page, nil)
						}
					}
				})
			}
			step(s.Channels > 0, "渠道", "channels")
			step(s.LogicalModels > 0, "逻辑模型", "logical")
			step(s.AppliedAccess > 0, "接入", "access")
		})
	})
}
```

在 `view` 的内容列、`u.pageView(c)` 之前调用 `setupChecklist(c, u)`。

- [x] **Step 3: 运行通过** `go test ./internal/desktop -run TestSetupChecklist`

---

### Task 3: 动态空态与跳转

**Files:**
- Modify: `internal/desktop/pages/logical/logical_view.go`
- Modify: `internal/desktop/pages/access/access.go`
- Test: `internal/desktop/pages/logical/logical_test.go`、`internal/desktop/pages/access/access_test.go`

- [x] **Step 1: 逻辑模型空态按渠道有无切换**

```go
chans := st.Svc.Channels.List()
...
if len(models) == 0 {
	if len(chans) > 0 {
		uikit.EmptyState(c, t, uikit.EmptyStateOpts{
			Title: "还没有逻辑模型", Hint: "把已建渠道挂进逻辑模型",
			ActionLabel: "＋ 新建逻辑模型", OnAction: func() { openNew(st) },
		})
	} else {
		uikit.EmptyState(... 现有「去添加渠道」...)
	}
}
```

逻辑模型编辑页无渠道时增加按钮「去添加渠道」→ `st.Go("channels", nil)`。

- [x] **Step 2: 接入空态增加跳转**

若 `len(st.Svc.Logical.List()) == 0`，空态 `ActionLabel: "去逻辑模型"` → `st.Go("logical", nil)`；否则保持「点击下方新建配置」。

- [x] **Step 3: 测试**：空库渲染逻辑页出现「去添加渠道」；有渠道无模型出现「新建逻辑模型」；无模型接入页出现「去逻辑模型」。

---

### Task 4: 搜索/过滤

**Files:**
- Modify: `internal/desktop/state/state.go`（ChannelState/LogicalState/AccessState 增加 Filter/PresetFilter）
- Modify: `channels_list.go`、`channels_new.go`、`logical_view.go`、`access.go`
- Test: `channels_test.go`、`logical_test.go`、`access_test.go`

- [x] **Step 1: 渠道列表搜索**

`channels_list.go` 在列表上方：

```go
if len(chans) > 8 {
	ui.TextInput(c, &st.Channels.Filter).Grow(1).Placeholder("搜索渠道：名称 / 协议 / Base URL")
}
```

循环前过滤：

```go
filter := strings.ToLower(strings.TrimSpace(st.Channels.Filter))
visible := chans[:0:0]
for _, ch := range chans {
	if filter == "" || strings.Contains(strings.ToLower(ch.Name+" "+ch.Protocol+" "+ch.BaseURL), filter) {
		visible = append(visible, ch)
	}
}
```

用 `visible` 渲染；为空且 filter 非空显示「没有匹配的渠道」。

- [x] **Step 2: 逻辑模型搜索** 同上（名称/描述/目标链摘要）；>8 项显示。
- [x] **Step 3: 接入配置搜索** 按名称/模型摘要过滤；>8 项显示。
- [x] **Step 4: 预设搜索**

`channels_new.go` 在预设网格前：

```go
ui.TextInput(c, &st.Channels.PresetFilter).Grow(1).Placeholder("搜索预设：名称 / ID")
```

过滤 `presets.All()`（DisplayName/ID 子串，忽略大小写）；无匹配显示提示。

- [x] **Step 5: 测试**：预置搜索过滤；逻辑模型搜索过滤；渠道搜索过滤。

---

### Task 5: 全量回归与文档

- [x] `go build ./...`、`go vet ./...`、`go test ./...`
- [x] `go test -race ./internal/desktop/... ./internal/bridge`
- [x] README/roadmap 同步 P1-3 状态。

## Self-Review

- **Spec coverage:** 默认页、三步清单、动态空态、跳转、搜索（列表/预设）。
- **Placeholder scan:** 无 TBD/TODO。
- **Known limitation:** 搜索为客户端内存过滤；未做批量/排序；三步清单按数据自动消失，不做持久化「已读」。


---

## 执行结果（2026-10-29）

- Task 1–5 全部完成；`go build ./...`、`go vet ./...`、`go test ./...` 通过。
- `go test -race ./internal/desktop/... ./internal/bridge` 通过。
- 新增测试：Setup 计数/默认页、三步清单、动态空态、列表/预设搜索。
- 未执行 git commit（等待用户验收）。
- 已知限制：搜索为客户端内存过滤；未做排序/批量；三步清单按数据自动消失，不做持久化「已读」。
