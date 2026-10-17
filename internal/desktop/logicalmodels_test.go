package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
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
	f := lmForm{targets: []bridge.TargetSpec{{ChannelID: "c1", ModelName: "m1"}}}
	err := f.validate()
	if err == nil || !strings.Contains(err.Error(), "模型名称") {
		t.Fatalf("empty name: %v", err)
	}
	f.name = "  "
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "模型名称") {
		t.Fatalf("blank name: %v", err)
	}
	f.name = "my-code"
	f.targets = nil
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "目标") {
		t.Fatalf("no targets: %v", err)
	}
	f.targets = []bridge.TargetSpec{{ChannelID: "", ModelName: "m1"}}
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "渠道") {
		t.Fatalf("missing channel: %v", err)
	}
	f.targets = []bridge.TargetSpec{{ChannelID: "c1", ModelName: " "}}
	if err := f.validate(); err == nil || !strings.Contains(err.Error(), "模型名") {
		t.Fatalf("missing model: %v", err)
	}
	f.targets = []bridge.TargetSpec{{ChannelID: "c1", ModelName: "m1"}, {ChannelID: "c2", ModelName: "m2"}}
	if err := f.validate(); err != nil {
		t.Fatalf("complete form: %v", err)
	}
}

// TestLogicalEditViewRenders 覆盖编辑页渲染薄壳：新建态出现名称/描述/目标
// 行/添加目标/保存且无 TODO 占位；校验失败错误进表单信息位。
func TestLogicalEditViewRenders(t *testing.T) {
	u, _ := testUI(t)
	u.openLogicalNew()
	tt := ui.NewTester(u.view, 900, 700)
	for _, want := range []string{"新建逻辑模型", "名称:", "描述:", "目标链", "添加目标", "保存"} {
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
	if u.page != "logical-edit" {
		t.Fatalf("page = %q, must stay on edit on validation failure", u.page)
	}
}

// TestLogicalSaveReturnsToList 覆盖保存成功回列表（简报「保存成功后
// u.page=logical」）：目标链按下标重排 Priority 后落库。
func TestLogicalSaveReturnsToList(t *testing.T) {
	u, _ := testUI(t)
	ch, err := u.svc.Channels.Create("", "渠道A", "https://api.example.com", "openai", "sk-1", true, false)
	if err != nil {
		t.Fatal(err)
	}
	u.openLogicalNew()
	u.lmEdit.name = "my-code"
	u.lmEdit.targets = []bridge.TargetSpec{
		{ChannelID: ch.ID, ModelName: "m2", Priority: 9},
		{ChannelID: ch.ID, ModelName: "m1", Priority: 9},
	}
	u.saveLogicalModel()
	if u.page != "logical" {
		t.Fatalf("page = %q, want logical (msg=%q)", u.page, u.lmEdit.msg)
	}
	m, err := u.svc.Logical.Get(u.svc.Logical.List()[0].ID)
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
