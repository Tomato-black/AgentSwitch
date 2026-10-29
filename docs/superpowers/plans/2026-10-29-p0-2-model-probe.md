# P0-2 逻辑模型端到端测试实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [x]`) syntax for tracking.

**Goal:** 在逻辑模型列表提供「测试模型」：按目标链逐个发送最小请求，显示每个目标的成功/失败、耗时、错误分类与下一步；不修改生产冷却/统计，不泄露密钥。

**Architecture:** app 层复用 `candidates()` 装配目标链，直接调用 `provider.Adapter.Do` 发 `max_tokens=1` 的最小请求；结果类型放 app，bridge 用类型别名暴露给桌面；逻辑模型页异步执行并渲染每个目标的结果。

**Tech Stack:** Go 1.27、MyGo v0.6.2、provider.Adapter；测试用 httptest 上游。

**Spec:** `docs/product/roadmap.md` P0-2；`docs/product/feature-gap-analysis.md` A2/B10。

## Global Constraints

- **不自动 commit**。
- **不改 gateway 三入站契约**；测试走 provider 直连，不经过 HTTP 入站。
- **不修改生产冷却/统计**：测试请求不写 `request_history`、不触发 cooldown。
- **不泄露密钥**：错误只含分类/状态/下一步，不含 Authorization/Key。
- **明确费用提示**：UI 文案说明会发送最小请求，可能产生少量费用。
- 每目标 20s 超时；父 context 取消立即停止。

---

### Task 1: app 探测类型与 `ProbeLogicalModel`

**Files:**
- Create: `internal/app/probe.go`
- Test: `internal/app/app_test.go`

**Interfaces:**
- Produces: `app.ModelProbeTarget{ChannelID, ChannelName, Model, OK, Status, DurationMs, ErrorClass, NextStep, Cooling}`
- Produces: `app.ModelProbeResult{Model, OK, Hit, Summary, Targets}`
- Produces: `App.ProbeLogicalModel(ctx, logicalName) (ModelProbeResult, error)`

- [x] **Step 1: 写失败测试**

在 `internal/app/app_test.go` 增加：

```go
func TestProbeLogicalModelOK(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer up.Close()
	a := newTestApp(t, up.URL, true)
	res, err := a.ProbeLogicalModel(context.Background(), "alias")
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || len(res.Targets) != 1 || !res.Targets[0].OK || res.Targets[0].Status != 200 {
		t.Fatalf("probe = %+v", res)
	}
	if res.Hit == "" {
		t.Fatal("success should set Hit")
	}
}

func TestProbeLogicalModelAuthNextStep(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"invalid api key"}}`))
	}))
	defer up.Close()
	a := newTestApp(t, up.URL, true)
	res, err := a.ProbeLogicalModel(context.Background(), "alias")
	if err != nil {
		t.Fatal(err)
	}
	if res.OK || len(res.Targets) != 1 || res.Targets[0].OK {
		t.Fatalf("probe = %+v", res)
	}
	if !strings.Contains(res.Targets[0].NextStep, "API Key") {
		t.Fatalf("next step = %q", res.Targets[0].NextStep)
	}
}
```

- [x] **Step 2: 运行失败**

Run: `go test ./internal/app -run 'TestProbeLogicalModelOK|TestProbeLogicalModelAuthNextStep' -v`  
Expected: FAIL（`ProbeLogicalModel` 不存在）。

- [x] **Step 3: 实现 `internal/app/probe.go`**

```go
package app

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
)

type ModelProbeTarget struct {
	ChannelID, ChannelName, Model string
	OK                            bool
	Status                        int
	DurationMs                    int64
	ErrorClass                    string
	NextStep                      string
	Cooling                       bool
}

type ModelProbeResult struct {
	Model   string
	OK      bool
	Hit     string
	Summary string
	Targets []ModelProbeTarget
}

// ProbeLogicalModel 按目标链依次发送最小请求，返回每个目标的结果。
// 不修改冷却/统计；不写 request_history。
func (a *App) ProbeLogicalModel(ctx context.Context, logicalName string) (ModelProbeResult, error) {
	cands, err := a.candidates(logicalName)
	if err != nil {
		return ModelProbeResult{}, err
	}
	res := ModelProbeResult{Model: logicalName}
	if len(cands) == 0 {
		res.Summary = "没有可用目标：请确认逻辑模型已启用、渠道已启用且配置了 Key"
		return res, nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Priority != cands[j].Priority {
			return cands[i].Priority < cands[j].Priority
		}
		return cands[i].ChannelID < cands[j].ChannelID
	})
	for _, c := range cands {
		t, err := a.probeTarget(ctx, c)
		if err != nil {
			return res, err
		}
		res.Targets = append(res.Targets, t)
		if t.OK && !res.OK {
			res.OK = true
			res.Hit = c.ChannelName + "·" + c.UpstreamModel
		}
	}
	if res.OK {
		res.Summary = "至少一个目标可用；首次命中 " + res.Hit
	} else {
		res.Summary = "所有目标均失败；请按每条提示处理"
	}
	return res, nil
}

func (a *App) probeTarget(ctx context.Context, c router.Candidate) (ModelProbeTarget, error) {
	t := ModelProbeTarget{
		ChannelID: c.ChannelID, ChannelName: c.ChannelName, Model: c.UpstreamModel,
		Cooling: !c.CoolingUntil.IsZero() && c.CoolingUntil.After(time.Now()),
	}
	if err := ctx.Err(); err != nil {
		return t, err
	}
	key, err := a.creds.Get(c.SecretKey)
	if err != nil {
		t.ErrorClass = "credential"
		t.NextStep = "渠道密钥不可读；请到渠道页重新保存 Key"
		return t, nil
	}
	ad, ok := a.adapters[c.Protocol]
	if !ok {
		t.ErrorClass = "protocol"
		t.NextStep = "渠道协议不受支持；请改为 anthropic 或 openai"
		return t, nil
	}
	body := []byte(fmt.Sprintf(`{"model":%q,"max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`, c.UpstreamModel))
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := time.Now()
	r, err := ad.Do(reqCtx, provider.DoRequest{
		BaseURL: c.BaseURL, AllowPrivate: c.AllowPrivate, APIKey: string(key),
		UpstreamModel: c.UpstreamModel, RawBody: body,
	})
	t.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		if ctx.Err() != nil {
			return t, ctx.Err()
		}
		t.ErrorClass = string(provider.ClassConnect)
		t.NextStep = probeNextStep(t.ErrorClass)
		return t, nil
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	t.Status = r.StatusCode
	if r.StatusCode >= 200 && r.StatusCode < 300 {
		t.OK = true
		return t, nil
	}
	t.ErrorClass = string(provider.Classify(r.StatusCode, r.RetryAfterSec, b))
	t.NextStep = probeNextStep(t.ErrorClass)
	return t, nil
}

func probeNextStep(class string) string {
	switch class {
	case string(provider.ClassAuth):
		return "检查渠道 API Key 是否正确、是否有该模型权限"
	case string(provider.ClassEndpoint):
		return "检查渠道 Base URL/协议，或到渠道页「拉取模型」确认模型名"
	case string(provider.ClassRateLimit):
		return "上游限流：等待后重试，或在逻辑模型目标链加入备用渠道"
	case string(provider.ClassQuota):
		return "额度不足：更换 Key/渠道，或等待额度恢复"
	case string(provider.ClassServer):
		return "上游 5xx：稍后重试，或把备用渠道前移"
	case string(provider.ClassParam):
		return "请求被上游拒绝：检查渠道模型名是否受支持"
	case string(provider.ClassConnect):
		return "连接失败：检查 Base URL、网络/代理与「允许私网」设置"
	default:
		return "查看渠道详情与设置页最近请求"
	}
}
```

补 `strings` 导入到 `app_test.go`。

- [x] **Step 4: 运行通过**

Run: `go test ./internal/app -run 'TestProbeLogicalModelOK|TestProbeLogicalModelAuthNextStep' -v`  
Expected: PASS。

---

### Task 2: bridge 暴露测试方法

**Files:**
- Modify: `internal/bridge/bridge.go`
- Test: `internal/bridge/bridge_test.go`

- [x] **Step 1: 写失败测试**

```go
func TestGatewayTestLogicalModel(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte(`{}`))
	}))
	defer up.Close()
	a := tempApp(t)
	// 实际测试内联创建 channel（含加密 Key）/逻辑模型/目标，未抽 helper。
	res, err := (Gateway{A: a}).TestLogicalModel(context.Background(), "my-code")
	if err != nil || !res.OK || len(res.Targets) != 1 {
		t.Fatalf("probe = %+v, %v", res, err)
	}
}
```

`seedProbeRoute` 可在测试文件中内联创建 channel（带加密 key）、逻辑模型、目标；参考 `TestApplyWritesLiveAndSetsCurrent` 的建法。

- [x] **Step 2: 运行失败**（方法不存在）

- [x] **Step 3: 实现**

```go
type ModelProbeTarget = app.ModelProbeTarget
type ModelProbeResult = app.ModelProbeResult

func (g Gateway) TestLogicalModel(ctx context.Context, logicalName string) (ModelProbeResult, error) {
	return g.A.ProbeLogicalModel(ctx, logicalName)
}
```

- [x] **Step 4: 运行通过** `go test ./internal/bridge -run TestGatewayTestLogicalModel -v`

---

### Task 3: 逻辑模型页「测试模型」

**Files:**
- Modify: `internal/desktop/state/state.go`（LogicalState）
- Modify: `internal/desktop/pages/logical/logical_view.go`
- Test: `internal/desktop/pages/logical/logical_test.go`

- [x] **Step 1: 写失败测试（结果渲染）**

```go
func TestLogicalPageShowsProbeResult(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Logical.TestResults = map[string]bridge.ModelProbeResult{
		"lm1": {
			Model: "my-code", OK: true, Hit: "main·m1",
			Summary: "至少一个目标可用；首次命中 main·m1",
			Targets: []bridge.ModelProbeTarget{{ChannelName: "main", Model: "m1", OK: true, Status: 200, DurationMs: 12}},
		},
	}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 900)
	if !tt.HasText("至少一个目标可用") || !tt.HasText("✓") {
		t.Fatalf("probe result missing: %v", tt.Texts())
	}
}
```

补 `storage` 导入。

- [x] **Step 2: 运行失败**

- [x] **Step 3: 实现**

`LogicalState` 增加：

```go
TestTask    Async
TestModelID string
TestResults map[string]bridge.ModelProbeResult
TestMsg     string
```

`logical_view.go` 在行内操作区增加「测试模型」：

```go
if st.Logical.TestTask.Running && st.Logical.TestModelID == model.ID {
    uikit.BusyRow(c, t, st.Logical.TestTask.Label, nil)
} else if uikit.SoftButton(c, t, s, "gray", "测试模型").Clicked() {
    id, name := model.ID, model.Name
    st.Logical.TestModelID = id
    state.RunAsync(st, &st.Logical.TestTask, "正在测试（会发送最小请求，可能产生少量费用）…",
        func(ctx context.Context) (bridge.ModelProbeResult, error) {
            return st.Svc.Gateway.TestLogicalModel(ctx, name)
        }, func(res bridge.ModelProbeResult, err error) {
            st.Logical.TestModelID = ""
            if err != nil {
                st.Logical.TestMsg = "测试失败：" + err.Error()
                return
            }
            if st.Logical.TestResults == nil {
                st.Logical.TestResults = map[string]bridge.ModelProbeResult{}
            }
            st.Logical.TestResults[id] = res
            st.Logical.TestMsg = ""
        })
}
```

在卡片下方渲染：

```go
if res, ok := st.Logical.TestResults[model.ID]; ok {
    probeResultView(c, t, res)
}
```

新增：

```go
func probeResultView(c *ui.Context, t *ui.Theme, res bridge.ModelProbeResult) {
	ui.Column(c).Gap(2).Children(func() {
		color := t.Danger
		if res.OK {
			color = t.Success
		}
		ui.Text(c, res.Summary).TextColor(color).SingleLine()
		for _, tg := range res.Targets {
			prefix, col := "✗", t.Danger
			if tg.OK {
				prefix, col = "✓", t.Success
			}
			line := fmt.Sprintf("%s %s·%s %dms", prefix, tg.ChannelName, tg.Model, tg.DurationMs)
			if tg.ErrorClass != "" {
				line += " · " + tg.ErrorClass
			}
			if tg.NextStep != "" {
				line += " · " + tg.NextStep
			}
			if tg.Cooling {
				line += " · 冷却中"
			}
			ui.Text(c, line).TextColor(col).SingleLine()
		}
	})
}
```

页面头部下方加费用提示：

```go
ui.Text(c, "「测试模型」会按目标链发送最小请求，可能产生少量费用；不会修改冷却或请求统计").TextColor(t.TextMuted)
```

- [x] **Step 4: 运行通过** `go test ./internal/desktop/pages/logical -v`

---

### Task 4: 全量回归与文档

- [x] `go build ./...`
- [x] `go vet ./...`
- [x] `go test ./...`
- [x] `go test -race ./internal/app ./internal/bridge ./internal/desktop/pages/logical`
- [x] README「逻辑模型」段补充「测试模型」；roadmap P0-2 标完成。

## Self-Review

- **Spec coverage:** 端到端测试（app probe）、每目标结果（Targets）、错误下一步（probeNextStep）、费用提示（UI）、不污染冷却/统计（直接 adapter）。
- **Placeholder scan:** 无 TBD/TODO。
- **Type consistency:** app 类型经 bridge 别名暴露；UI 使用 `bridge.ModelProbeResult`，与 bridge 方法返回一致。
- **Known limitation:** 测试请求会真实消耗少量额度；冷却中的目标仍会测试并在结果标注「冷却中」；不做定时健康检查（P2）。


---

## 执行结果（2026-10-29）

- Task 1–4 全部完成；`go build ./...`、`go vet ./...`、`go test ./...` 通过。
- `go test -race ./internal/app ./internal/bridge ./internal/desktop/pages/logical` 通过。
- 新增测试：app 探测成功/401 下一步、bridge 方法、逻辑模型页结果渲染。
- 未执行 git commit（等待用户验收）。
- 已知限制：测试会真实消耗少量额度；冷却中的目标仍会测试并标注；不做定时健康检查（P2）。
