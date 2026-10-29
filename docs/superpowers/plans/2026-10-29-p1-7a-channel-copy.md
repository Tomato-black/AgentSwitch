# P1-7a 渠道复制实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 在渠道列表/详情复制渠道（Base URL、协议、建议模型、启停、私网设置），默认不复制 Key；复制后提示填写新 Key。

**Architecture:** bridge `Channels.Copy(id, copyKey)` 复用 `Create` + `SetSuggestions`；名称自动生成「原名 副本 / 副本 2」；UI 列表行与详情页增加「复制」按钮。

**Tech Stack:** Go 1.27、MyGo；测试离线。

**Spec:** `docs/product/roadmap.md` P1-7；`docs/product/feature-gap-analysis.md` A8/C2。

## Global Constraints

- 不自动 commit。
- 默认不复制 Key；`copyKey=false` 时新渠道 `HasKey=false`。
- 不改渠道 schema；不复制 live 接入配置。
- 名称冲突自动加序号；不覆盖已有渠道。

---

### Task 1: bridge `Channels.Copy`

**Files:**
- Modify: `internal/bridge/channels.go`
- Test: `internal/bridge/channels_test.go`

- [x] **Step 1: 写失败测试**

```go
func TestChannelsCopyWithoutKey(t *testing.T) {
	a := tempApp(t)
	ch, err := (Channels{A: a}).Create("", "主渠道", "https://api.example.com", "anthropic", "sk-secret", true, false)
	if err != nil { t.Fatal(err) }
	if err := (Channels{A: a}).SetSuggestions(ch.ID, []string{"m1", "m2"}); err != nil { t.Fatal(err) }
	cp, err := (Channels{A: a}).Copy(ch.ID, false)
	if err != nil { t.Fatal(err) }
	if cp.Name == ch.Name || cp.HasKey {
		t.Fatalf("copy = %+v", cp)
	}
	if got := (Channels{A: a}).Suggestions(cp.ID); len(got) != 2 {
		t.Fatalf("suggestions = %v", got)
	}
	// 再复制一次，名称自动去重。
	cp2, err := (Channels{A: a}).Copy(ch.ID, false)
	if err != nil || cp2.Name == cp.Name {
		t.Fatalf("second copy = %+v, %v", cp2, err)
	}
}

func TestChannelsCopyWithKey(t *testing.T) {
	a := tempApp(t)
	ch, _ := (Channels{A: a}).Create("", "主渠道", "https://api.example.com", "anthropic", "sk-secret", true, false)
	cp, err := (Channels{A: a}).Copy(ch.ID, true)
	if err != nil || !cp.HasKey {
		t.Fatalf("copy with key = %+v, %v", cp, err)
	}
}
```

- [x] **Step 2: 实现**

```go
func (s Channels) Copy(id string, copyKey bool) (ChannelView, error) {
	src, err := s.A.Store().Channels().Get(id)
	if err != nil {
		return ChannelView{}, fmt.Errorf("渠道不存在")
	}
	key := ""
	if copyKey {
		if k, err := s.secret(src); err == nil {
			key = k
		}
	}
	names := map[string]bool{}
	if chs, err := s.A.Store().Channels().List(); err == nil {
		for _, c := range chs {
			names[c.Name] = true
		}
	}
	base := src.Name + " 副本"
	name := base
	for i := 2; names[name]; i++ {
		name = fmt.Sprintf("%s %d", base, i)
	}
	v, err := s.Create(src.PresetID, name, src.BaseURL, src.Protocol, key, src.Enabled, src.AllowPrivateUpstream)
	if err != nil {
		return ChannelView{}, err
	}
	sugg, _ := s.A.Store().ChannelModels().Suggestions(src.ID)
	if len(sugg) > 0 {
		if err := s.SetSuggestions(v.ID, sugg); err != nil {
			return v, err
		}
	}
	s.A.Log().Info("channel copied", "from", id, "to", v.ID, "with_key", copyKey, "actor", "ui", "result", "ok")
	return v, nil
}
```

- [x] **Step 3: 运行通过** `go test ./internal/bridge -run TestChannelsCopy`

---

### Task 2: 渠道列表/详情「复制」

**Files:**
- Modify: `internal/desktop/pages/channels/channels_list.go`
- Modify: `internal/desktop/pages/channels/channels_detail.go`
- Test: `internal/desktop/pages/channels/channels_test.go`

- [x] **Step 1: 列表行增加「复制」**

在 `channelCard` 行内启停按钮旁：

```go
if uikit.SoftButton(c, t, s, "gray", "复制").Clicked() {
	v, err := st.Svc.Channels.Copy(ch.ID, false)
	if err != nil {
		st.Channels.TestMsg = "复制失败：" + err.Error()
	} else {
		st.Channels.TestMsg = "已复制渠道「" + v.Name + "」（不含 Key）；请在详情中填写 Key"
	}
}
```

- [x] **Step 2: 详情页增加「复制渠道」**

在保存/删除操作行增加：

```go
if uikit.SoftButton(c, t, s, "gray", "复制渠道").Clicked() {
	v, err := st.Svc.Channels.Copy(d.ID, false)
	if err != nil {
		st.Channels.TestMsg = "复制失败：" + err.Error()
	} else {
		openDetail(st, v.ID)
		st.Channels.TestMsg = "已复制渠道（不含 Key）；请填写新 Key 后保存"
	}
}
```

- [x] **Step 3: UI 测试**

```go
func TestChannelListCopy(t *testing.T) {
	st, a := uitest.New(t)
	ch, _ := (bridge.Channels{A: a}).Create("", "主渠道", "https://api.example.com", "anthropic", "sk-secret", true, false)
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 900)
	if err := tt.Click("复制"); err != nil { t.Fatal(err) }
	if !strings.Contains(st.Channels.TestMsg, "不含 Key") { t.Fatalf("msg = %q", st.Channels.TestMsg) }
	if len((bridge.Channels{A: a}).List()) != 2 { t.Fatal("copy not created") }
	_ = ch
}
```

- [x] **Step 4: 运行通过** `go test ./internal/desktop/pages/channels -run TestChannelListCopy`

---

### Task 3: 全量回归与文档

- [x] `go build ./...`、`go vet ./...`、`go test ./...`
- [x] `go test -race ./internal/bridge ./internal/desktop/pages/channels`
- [x] README/roadmap 同步 P1-7a；主备链一键加入留 P1-7b。

## Self-Review

- **Spec coverage:** 复制字段、默认不含 Key、建议模型、名称去重、列表/详情入口。
- **Placeholder scan:** 无 TBD/TODO。
- **Known limitation:** 不复制 Key（需手动填）；不自动加入逻辑模型目标链（P1-7b）。


---

## 执行结果（2026-10-29）

- Task 1–3 全部完成；`go build ./...`、`go vet ./...`、`go test ./...` 通过。
- `go test -race ./internal/bridge ./internal/desktop/pages/channels` 通过。
- 新增测试：复制不含 Key/含 Key、名称去重、建议模型复制、列表复制入口。
- 未执行 git commit（等待用户验收）。
- 范围说明：P1-7a 只做渠道复制；主备链一键加入（P1-7b）待做。
