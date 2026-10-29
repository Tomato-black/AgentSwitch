# P1-7b 主备链一键加入实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 复制渠道后，把副本自动加入所有引用源渠道的逻辑模型目标链（追加到链尾），形成主备链；副本未填 Key 前不会成为候选。

**Architecture:** bridge `LogicalModels.AddCopyToReferencing(sourceID, copyID)` 扫描逻辑模型目标；对引用源渠道的模型追加同模型名的新目标（priority=当前最大值+1），去重；复制 UI 调用并提示加入了哪些模型。

**Tech Stack:** Go 1.27；测试离线。

**Spec:** `docs/product/roadmap.md` P1-7；`docs/product/feature-gap-analysis.md` A8/C2。

## Global Constraints

- 不自动 commit。
- 不复制 Key；副本无 Key 前 `buildCandidates` 会跳过，不影响现有路由。
- 已存在的 `(model, channel, model_name)` 目标不重复添加。
- 只追加，不调整现有目标顺序/优先级。

---

### Task 1: `AddCopyToReferencing`

**Files:**
- Modify: `internal/bridge/logicalmodels.go`
- Test: `internal/bridge/logicalmodels_test.go`

- [x] **Step 1: 测试**

```go
func TestAddCopyToReferencing(t *testing.T) {
	a := tempApp(t)
	c1, _ := (Channels{A: a}).Create("", "主", "https://api.example.com", "anthropic", "k", true, false)
	lm, _ := (LogicalModels{A: a}).Create("my-code", "", ModelMeta{},
		[]TargetSpec{{ChannelID: c1.ID, ModelName: "m1", Priority: 0}})
	c2, _ := (Channels{A: a}).Copy(c1.ID, false)
	names, err := (LogicalModels{A: a}).AddCopyToReferencing(c1.ID, c2.ID)
	if err != nil || len(names) != 1 || names[0] != lm.Name {
		t.Fatalf("names = %v, %v", names, err)
	}
	got, _ := a.Store().LogicalModelTargets().ListByModel(lm.ID)
	if len(got) != 2 || got[1].ChannelID != c2.ID || got[1].ModelName != "m1" {
		t.Fatalf("targets = %+v", got)
	}
	// 幂等：再次调用不重复添加。
	names, _ = (LogicalModels{A: a}).AddCopyToReferencing(c1.ID, c2.ID)
	if len(names) != 0 {
		t.Fatalf("second call = %v", names)
	}
}
```

- [x] **Step 2: 实现**

```go
func (s LogicalModels) AddCopyToReferencing(sourceID, copyID string) ([]string, error) {
	models, err := s.A.Store().LogicalModels().List()
	if err != nil { return nil, err }
	var updated []string
	for _, lm := range models {
		targets, err := s.A.Store().LogicalModelTargets().ListByModel(lm.ID)
		if err != nil { return nil, err }
		var src []storage.LogicalModelTarget
		maxPri := -1
		existing := map[string]bool{}
		for _, t := range targets {
			if t.Priority > maxPri { maxPri = t.Priority }
			existing[t.ChannelID+"\x00"+t.ModelName] = true
			if t.ChannelID == sourceID { src = append(src, t) }
		}
		if len(src) == 0 { continue }
		added := false
		for _, t := range src {
			key := copyID + "\x00" + t.ModelName
			if existing[key] { continue }
			maxPri++
			if _, err := s.A.Store().LogicalModelTargets().Save(storage.LogicalModelTarget{
				ID: storage.NewID(), LogicalModelID: lm.ID, ChannelID: copyID,
				ModelName: t.ModelName, Priority: maxPri,
			}); err != nil { return nil, err }
			existing[key] = true
			added = true
		}
		if added { updated = append(updated, lm.Name) }
	}
	return updated, nil
}
```

- [x] **Step 3: 运行通过**

---

### Task 2: 复制 UI 提示加入结果

**Files:**
- Modify: `internal/desktop/pages/channels/channels_list.go`
- Modify: `internal/desktop/pages/channels/channels_detail.go`
- Test: `internal/desktop/pages/channels/channels_test.go`

- [x] **Step 1: 实现**

列表复制成功后：

```go
models, _ := st.Svc.Logical.AddCopyToReferencing(ch.ID, v.ID)
msg := "已复制渠道「" + v.Name + "」（不含 Key）"
if len(models) > 0 {
	msg += "；已加入目标链：" + strings.Join(models, "、")
}
st.Channels.TestMsg = msg + "；请填写 Key"
```

详情页同样调用；`st.Svc.Logical` 为 nil 时跳过。

- [x] **Step 2: 测试**：已有逻辑模型引用源渠道时，复制后提示包含「已加入目标链」；目标数 2。

- [x] **Step 3: 全量回归** `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./internal/bridge ./internal/desktop/pages/channels`
- [x] README/roadmap 同步 P1-7b。

## Self-Review

- 只追加引用源渠道的模型；去重；副本无 Key 不参与候选。
- 已知限制：不询问用户，自动加入；如需不加入可在逻辑模型编辑页移除目标。


---

## 执行结果（2026-10-29）

- Task 1–2 完成；`go build ./...`、`go vet ./...`、`go test ./...` 通过。
- `go test -race ./internal/bridge ./internal/desktop/pages/channels` 通过。
- 新增测试：引用源渠道的模型自动追加副本目标、幂等、UI 提示。
- 未执行 git commit（等待用户验收）。
- 已知限制：自动加入，不弹窗询问；如需不加入可在逻辑模型编辑页移除目标；副本无 Key 前不会被路由到。
