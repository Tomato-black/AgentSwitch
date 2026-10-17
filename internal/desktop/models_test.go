package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// aggFixture 造两渠道各一枚跨渠道同名的公开模型：聚合页的语义正是
// 「公开模型 × 渠道」展平——同名 pub-1 出现两行、渠道列可区分。
func aggFixture(t *testing.T) *uiState {
	u, _ := testUI(t)
	if _, err := u.svc.Channels.Create("anthropic-compatible", "a", "https://a.example.com", 10, true, false); err != nil {
		t.Fatal(err)
	}
	c2, err := u.svc.Channels.Create("openai-compatible", "b", "https://b.example.com", 20, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.Channels.AddModel(u.svc.Channels.List()[0].ID, "pub-1", "up-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.Channels.AddModel(c2.ID, "pub-1", "up-b"); err != nil {
		t.Fatal(err)
	}
	return u
}

// TestModelsAggregateShowsCrossChannel 覆盖聚合页主形态（spec §8 决策 7 /
// §5.4）：跨渠道展平（pub-1 两行）+ 页头统计行；编辑入口由来源渠道 chip
// 承载——点 chip 跳该渠道详情（决策 7「增删改在渠道页」的新交互落点）。
func TestModelsAggregateShowsCrossChannel(t *testing.T) {
	u := aggFixture(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 1000, 700)
	if !tt.HasText("模型") || !tt.HasText("pub-1") {
		t.Fatalf("texts %v", tt.Texts())
	}
	// 页头统计行（spec §5.4 格式）——钉住「N 条渠道模型 · 来自 M 渠道 · 停用 X」，
	// 防退化成裸计数。
	if !tt.HasText("2 条渠道模型 · 来自 2 渠道 · 停用 0") {
		t.Fatalf("header stats missing: %v", tt.Texts())
	}
	// 来源渠道 chip = 编辑入口（决策 7 新落点）：点渠道 a 的 chip 进详情。
	if err := tt.Click("a"); err != nil {
		t.Fatal(err)
	}
	if u.page != "channel-detail" {
		t.Fatalf("chip click must open channel detail, page=%q", u.page)
	}
	want := ""
	for _, ch := range u.svc.Channels.List() {
		if ch.Name == "a" {
			want = ch.ID
		}
	}
	if want == "" || u.chDetailID != want {
		t.Fatalf("chDetailID=%q want %q", u.chDetailID, want)
	}
}

// TestModelsAggregateToggleEnabled 覆盖行内启停：点行内 Switch 翻转该行
// ChannelModels.Enabled——聚合页唯一写操作（只读 + 每行开关）。
func TestModelsAggregateToggleEnabled(t *testing.T) {
	u := aggFixture(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 1000, 700)
	if err := tt.Click("启用 a 的 pub-1"); err != nil {
		t.Fatal(err)
	}
	rows := bridge.ModelsAgg{A: u.svc.Channels.A}.ListAll()
	for _, r := range rows {
		if r.ChannelName == "a" && r.Enabled {
			t.Fatalf("toggle failed: %+v", r)
		}
		if r.ChannelName == "b" && !r.Enabled {
			t.Fatalf("b 行不得被波及: %+v", r)
		}
	}
}

// TestModelsAggregateEmptyHint 覆盖空表引导（spec §5.4）：无渠道模型时
// 指引到渠道页添加，而非旧全局别名表单。
func TestModelsAggregateEmptyHint(t *testing.T) {
	u, _ := testUI(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 1000, 700)
	if !tt.HasText("尚无模型") {
		t.Fatalf("texts %v", tt.Texts())
	}
}

func TestSortModelRows(t *testing.T) {
	rows := []bridge.ModelAggregateView{{PublicModel: "b", ChannelName: "z"}, {PublicModel: "a", ChannelName: "y"}, {PublicModel: "a", ChannelName: "x"}}
	sortModelRows(rows)
	if rows[0].ChannelName != "x" || rows[2].PublicModel != "b" {
		t.Fatal("分组排序")
	}
}
func TestModelsToggleOnlyTargetRow(t *testing.T) {
	u, _ := testUI(t)
	c1, _ := u.svc.Channels.Create("anthropic-official", "c1", "https://1.example.com", 10, true, false)
	c2, _ := u.svc.Channels.Create("anthropic-official", "c2", "https://2.example.com", 20, true, false)
	m1, _ := u.svc.Channels.AddModel(c1.ID, "same-model", "up-1")
	m2, _ := u.svc.Channels.AddModel(c2.ID, "same-model", "up-2")
	u.toggleModelRow(m2.ID, false)
	rows := bridge.ModelsAgg{A: u.svc.Channels.A}.ListAll()
	for _, r := range rows {
		if r.ID == m1.ID && !r.Enabled {
			t.Fatal("c1 行不得被波及")
		}
		if r.ID == m2.ID && r.Enabled {
			t.Fatal("c2 行须已停用")
		}
	}
}
func TestModelsSearchFilter(t *testing.T) {
	u, _ := testUI(t)
	c1, _ := u.svc.Channels.Create("anthropic-official", "c1", "https://1.example.com", 10, true, false)
	u.svc.Channels.AddModel(c1.ID, "glm-4.7", "glm-4.7")
	u.svc.Channels.AddModel(c1.ID, "kimi-k2", "kimi-k2")
	u.modelQuery = "glm"
	tt := ui.NewTester(u.modelsView, 1080, 720)
	if !tt.HasText("glm-4.7") || tt.HasText("kimi-k2") {
		t.Fatalf("过滤失效: %v", tt.Texts())
	}
}

// TestModelsChipOpensRowOwnChannel 覆盖来源渠道 chip 跳转目标 = 本行
// ChannelID（review fix）：渠道名可重复（Create/Update 不查重），按名取
// 首个会开错渠道。同名两渠道「dup」——B 优先级低（10）故在 List() 序
// （priority ASC）里靠前；alpha 行属 A。点排序后首行（alpha）的 chip
// 必须开 A 的详情，若按名首命中会错开 B。
func TestModelsChipOpensRowOwnChannel(t *testing.T) {
	u, _ := testUI(t)
	a, _ := u.svc.Channels.Create("anthropic-official", "dup", "https://a.example.com", 20, true, false)
	b, _ := u.svc.Channels.Create("anthropic-official", "dup", "https://b.example.com", 10, true, false)
	u.svc.Channels.AddModel(a.ID, "alpha", "up-a")
	u.svc.Channels.AddModel(b.ID, "beta", "up-b")
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 1080, 720)
	// 首个「dup」label = 排序后首行（alpha，属 A）的来源渠道 chip。
	if err := tt.Click("dup"); err != nil {
		t.Fatal(err)
	}
	if u.chDetailID != a.ID {
		t.Fatalf("chip must open row's own channel: chDetailID=%q want a=%q (name-first-hit would be b=%q)",
			u.chDetailID, a.ID, b.ID)
	}
}

// TestModelsChipNoJumpOnMissingChannel 覆盖「查不到不跳」：行指向的渠道已
// 不存在（孤儿行）时保持原页，不得落进空详情。
func TestModelsChipNoJumpOnMissingChannel(t *testing.T) {
	u, _ := testUI(t)
	u.page = "models"
	u.openModelChannel("no-such-channel")
	if u.page != "models" {
		t.Fatalf("missing channel must not navigate, page=%q", u.page)
	}
}
