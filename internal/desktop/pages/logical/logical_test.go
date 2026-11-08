package logical

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uitest"
	"AgentSwitch/internal/storage"
)

// TestLogicalCardShowsTargetChain 覆盖目标链摘要纯函数：两目标按序拼成
// `渠道A·model → 渠道B·model`（spec §6.2 摘要格式）。渲染薄壳不测字符串细节。
func TestLogicalCardShowsTargetChain(t *testing.T) {
	m := bridge.ModelView{Name: "my-code", Targets: []bridge.TargetView{
		{ChannelName: "渠道A", ModelName: "m1", Priority: 0},
		{ChannelName: "渠道B", ModelName: "m2", Priority: 1},
	}}
	got := formatTargetChain(m)
	want := "渠道A·m1 → 渠道B·m2"
	if got != want {
		t.Fatalf("formatTargetChain() = %q, want %q", got, want)
	}
}

// TestFormatTargetChainEmpty 覆盖空链：无目标 → `（无目标）`。
func TestFormatTargetChainEmpty(t *testing.T) {
	if got := formatTargetChain(bridge.ModelView{Name: "empty"}); got != "（无目标）" {
		t.Fatalf("formatTargetChain() = %q, want （无目标）", got)
	}
}

// TestTargetMoveUpDown 覆盖目标链排序纯函数：上移/下移换位，首行上移与
// 末行下移（边界）以及越界下标都不动（spec §6.2「上下移」）。
func TestTargetMoveUpDown(t *testing.T) {
	ts := []bridge.TargetSpec{
		{ChannelID: "c1", ModelName: "m1"},
		{ChannelID: "c2", ModelName: "m2"},
		{ChannelID: "c3", ModelName: "m3"},
	}
	moveTarget(ts, 1, -1) // 中行上移：与首行换位
	if ts[0].ChannelID != "c2" || ts[1].ChannelID != "c1" || ts[2].ChannelID != "c3" {
		t.Fatalf("after up: %v %v %v", ts[0].ChannelID, ts[1].ChannelID, ts[2].ChannelID)
	}
	moveTarget(ts, 0, +1) // 换位后的首行下移，复原顺序
	if ts[0].ChannelID != "c1" || ts[1].ChannelID != "c2" || ts[2].ChannelID != "c3" {
		t.Fatalf("after down: %v %v %v", ts[0].ChannelID, ts[1].ChannelID, ts[2].ChannelID)
	}
	moveTarget(ts, 0, -1) // 边界：首行上移不动
	moveTarget(ts, 2, +1) // 边界：末行下移不动
	if ts[0].ChannelID != "c1" || ts[1].ChannelID != "c2" || ts[2].ChannelID != "c3" {
		t.Fatalf("boundary must be no-op: %v %v %v", ts[0].ChannelID, ts[1].ChannelID, ts[2].ChannelID)
	}
	moveTarget(ts, -1, +1) // 越界下标不动
	moveTarget(ts, 3, -1)
	if ts[0].ChannelID != "c1" || ts[2].ChannelID != "c3" {
		t.Fatalf("out-of-range must be no-op: %v %v %v", ts[0].ChannelID, ts[1].ChannelID, ts[2].ChannelID)
	}
	empty := []bridge.TargetSpec{}
	moveTarget(empty, 0, +1) // 空链不动、不 panic
}

// TestEditValidationEmptyName 覆盖保存前校验：名称空白报「需要填写模型
// 名称」；至少一个目标且每行渠道/模型名齐备才放行（校验函数返回错误信息，
// 表单信息位展示）。
func TestEditValidationEmptyName(t *testing.T) {
	f := state.LMForm{Targets: []bridge.TargetSpec{{ChannelID: "c1", ModelName: "m1"}}}
	err := validate(f)
	if err == nil || !strings.Contains(err.Error(), "模型名称") {
		t.Fatalf("empty name: %v", err)
	}
	f.Name = "  "
	if err := validate(f); err == nil || !strings.Contains(err.Error(), "模型名称") {
		t.Fatalf("blank name: %v", err)
	}
	f.Name = "my-code"
	f.Targets = nil
	if err := validate(f); err == nil || !strings.Contains(err.Error(), "目标") {
		t.Fatalf("no targets: %v", err)
	}
	f.Targets = []bridge.TargetSpec{{ChannelID: "", ModelName: "m1"}}
	if err := validate(f); err == nil || !strings.Contains(err.Error(), "渠道") {
		t.Fatalf("missing channel: %v", err)
	}
	f.Targets = []bridge.TargetSpec{{ChannelID: "c1", ModelName: " "}}
	if err := validate(f); err == nil || !strings.Contains(err.Error(), "模型名") {
		t.Fatalf("missing model: %v", err)
	}
	f.Targets = []bridge.TargetSpec{{ChannelID: "c1", ModelName: "m1"}, {ChannelID: "c2", ModelName: "m2"}}
	if err := validate(f); err != nil {
		t.Fatalf("complete form: %v", err)
	}
}

// TestLogicalSavePreservesExistingMeta：元数据已移至接入页维护，逻辑模型
// 编辑页保存时不得清空既有 Codex 元数据（显示名/窗口/思考等级）。
func TestLogicalSavePreservesExistingMeta(t *testing.T) {
	st, _ := uitest.New(t)
	ch, err := st.Svc.Channels.Create("", "渠道A", "https://api.example.com", "openai", "sk-1", true, false)
	if err != nil {
		t.Fatal(err)
	}
	m, err := st.Svc.Logical.Create("my-code", "", bridge.ModelMeta{
		DisplayName: "My Code", ContextWindow: 1000000,
		ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high",
	}, []bridge.TargetSpec{{ChannelID: ch.ID, ModelName: "m1", Priority: 0}})
	if err != nil {
		t.Fatal(err)
	}
	openEdit(st, m.ID)
	if st.Logical.Edit.Name != "my-code" || len(st.Logical.Edit.Targets) != 1 {
		t.Fatalf("backfill = %+v", st.Logical.Edit)
	}
	save(st)
	got, err := st.Svc.Logical.Get(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "My Code" || got.ContextWindow != 1000000 ||
		len(got.ReasoningLevels) != 2 || got.DefaultReasoningLevel != "high" {
		t.Fatalf("save must preserve metadata: %+v", got)
	}
}

// TestLogicalEditViewRenders 覆盖编辑页渲染薄壳：新建态出现名称/描述/目标
// 行/添加目标/保存且无 TODO 占位；校验失败错误进表单信息位。
func TestLogicalEditViewRenders(t *testing.T) {
	st, _ := uitest.New(t)
	openNew(st)
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 900, 700)
	for _, want := range []string{
		"新建逻辑模型", "名称:", "描述:", "目标链", "添加目标", "保存",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("TODO") {
		t.Fatalf("placeholder leaked: %v", tt.Texts())
	}
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("需要填写模型名称") {
		t.Fatalf("validation msg missing: %v", tt.Texts())
	}
	if st.Page != "logical-edit" {
		t.Fatalf("page = %q, must stay on edit on validation failure", st.Page)
	}
}

// TestLogicalSaveReturnsToList 覆盖保存成功回列表：目标链按下标重排
// Priority 后落库。
func TestLogicalSaveReturnsToList(t *testing.T) {
	st, _ := uitest.New(t)
	ch, err := st.Svc.Channels.Create("", "渠道A", "https://api.example.com", "openai", "sk-1", true, false)
	if err != nil {
		t.Fatal(err)
	}
	openNew(st)
	st.Logical.Edit.Name = "my-code"
	st.Logical.Edit.Targets = []bridge.TargetSpec{
		{ChannelID: ch.ID, ModelName: "m2", Priority: 9},
		{ChannelID: ch.ID, ModelName: "m1", Priority: 9},
	}
	save(st)
	if st.Page != "logical" {
		t.Fatalf("page = %q, want logical (msg=%q)", st.Page, st.Logical.Edit.Msg)
	}
	m, err := st.Svc.Logical.Get(st.Svc.Logical.List()[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "my-code" || len(m.Targets) != 2 {
		t.Fatalf("saved %q with %d targets", m.Name, len(m.Targets))
	}
	if m.Targets[0].ModelName != "m2" || m.Targets[0].Priority != 0 ||
		m.Targets[1].ModelName != "m1" || m.Targets[1].Priority != 1 {
		t.Fatalf("targets not renumbered: %+v", m.Targets)
	}
}

// TestHasDanglingTarget 覆盖失效目标检测：渠道被删后 ChannelName 解析为空
// 才算失效；正常目标与空渠道 id 不算。
func TestHasDanglingTarget(t *testing.T) {
	if hasDanglingTarget(bridge.ModelView{Targets: []bridge.TargetView{
		{ChannelID: "c1", ChannelName: "渠道A"},
	}}) {
		t.Fatal("正常目标不应标记失效")
	}
	if !hasDanglingTarget(bridge.ModelView{Targets: []bridge.TargetView{
		{ChannelID: "c1", ChannelName: "渠道A"},
		{ChannelID: "gone", ChannelName: ""},
	}}) {
		t.Fatal("渠道已删的目标应标记失效")
	}
	if hasDanglingTarget(bridge.ModelView{Targets: []bridge.TargetView{{ChannelID: "", ChannelName: ""}}}) {
		t.Fatal("空渠道 id 属于未填完的行，不算失效")
	}
}

// TestDeleteShowsAccessRefs 覆盖删除确认的引用提示：绑定该逻辑模型的
// 接入配置应列在弹窗文案中。
func TestDeleteShowsAccessRefs(t *testing.T) {
	st, a := uitest.New(t)
	ch, err := st.Svc.Channels.Create("", "渠道A", "https://api.example.com", "openai", "sk-1", true, false)
	if err != nil {
		t.Fatal(err)
	}
	lm, err := st.Svc.Logical.Create("my-code", "", bridge.ModelMeta{}, []bridge.TargetSpec{{ChannelID: ch.ID, ModelName: "m1"}})
	if err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a}
	if _, err := acc.Create("claude", "工作配置", lm.ID, "{}"); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 800)
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"删除逻辑模型", "仍被接入配置引用", "工作配置", "Claude Code"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

// TestLogicalEmptyStateCTA：空态「去添加渠道」直达渠道页（首次配置旅程
// 顺序：渠道 → 逻辑模型 → 接入）。
func TestLogicalEmptyStateCTA(t *testing.T) {
	st, _ := uitest.New(t)
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 900, 700)
	if !tt.HasText("去添加渠道") {
		t.Fatalf("missing CTA: %v", tt.Texts())
	}
	if err := tt.Click("去添加渠道"); err != nil {
		t.Fatal(err)
	}
	if st.Page != "channels" || st.Channels.View != "" {
		t.Fatalf("page=%q view=%q", st.Page, st.Channels.View)
	}
}

// TestTargetIssue 覆盖 P1-10：渠道被删 → 目标失效；渠道存在但停用/无 Key
// → 渠道不可用；健康目标不提示。
func TestTargetIssue(t *testing.T) {
	if got := targetIssue(bridge.ModelView{Targets: []bridge.TargetView{{ChannelID: "c1", ChannelName: "A", Healthy: true}}}); got != "" {
		t.Fatalf("healthy target issue = %q", got)
	}
	if got := targetIssue(bridge.ModelView{Targets: []bridge.TargetView{{ChannelID: "c1", ChannelName: "", Healthy: false}}}); got != "目标失效" {
		t.Fatalf("deleted channel issue = %q", got)
	}
	if got := targetIssue(bridge.ModelView{Targets: []bridge.TargetView{{ChannelID: "c1", ChannelName: "A", Healthy: false}}}); got != "渠道不可用" {
		t.Fatalf("unhealthy channel issue = %q", got)
	}
}

// TestModelCooldown 覆盖冷却徽章：任一目标渠道冷却中→「冷却中」；任一
// 半开探测中→「恢复中」（优先）；无关渠道→空。
func TestModelCooldown(t *testing.T) {
	m := bridge.ModelView{Targets: []bridge.TargetView{{ChannelID: "c1"}, {ChannelID: "c2"}}}
	if got := modelCooldown(m, map[string]bridge.CooldownView{"c2": {RemainingSeconds: 20}}); got != "冷却中" {
		t.Fatalf("c2 cooling = %q", got)
	}
	if got := modelCooldown(m, map[string]bridge.CooldownView{
		"c1": {RemainingSeconds: 0, HalfOpen: true}, "c2": {RemainingSeconds: 20},
	}); got != "恢复中" {
		t.Fatalf("half-open must win = %q", got)
	}
	if got := modelCooldown(m, map[string]bridge.CooldownView{"c3": {RemainingSeconds: 20}}); got != "" {
		t.Fatalf("unrelated cooling = %q", got)
	}
}

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

func TestLogicalEmptyStateWithChannelsOffersCreate(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := (bridge.Channels{A: a}).Create("", "主渠道", "https://api.example.com", "anthropic", "", true, false); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 900)
	if !tt.HasText("把已建渠道挂进逻辑模型") {
		t.Fatalf("dynamic empty state missing: %v", tt.Texts())
	}
}

func TestLogicalEmptyStateWithoutChannelsOffersChannel(t *testing.T) {
	st, _ := uitest.New(t)
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 900)
	if !tt.HasText("去添加渠道") {
		t.Fatalf("channel action missing: %v", tt.Texts())
	}
}

func TestLogicalListSearchFilters(t *testing.T) {
	st, a := uitest.New(t)
	for i := 1; i <= 9; i++ {
		name := fmt.Sprintf("model-%d", i)
		if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{Name: name, Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	st.Logical.Filter = "model-9"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 900)
	if !tt.HasText("model-9") || tt.HasText("model-1") {
		t.Fatalf("logical filter failed: %v", tt.Texts())
	}
}
