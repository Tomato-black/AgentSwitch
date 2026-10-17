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

// TestModelsAggregateShowsCrossChannel 覆盖聚合页主形态（spec §8 决策 7）：
// 跨渠道展平（pub-1 两行）、渠道列承载编辑入口指向（"渠道" 列名）。
func TestModelsAggregateShowsCrossChannel(t *testing.T) {
	u := aggFixture(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 1000, 700)
	if !tt.HasText("模型") || !tt.HasText("pub-1") {
		t.Fatalf("texts %v", tt.Texts())
	}
	// 聚合页提示编辑入口在渠道页（决策 7 的文案承载）
	if !tt.HasText("渠道") {
		t.Fatalf("channel column missing: %v", tt.Texts())
	}
}

// TestModelsAggregateToggleEnabled 覆盖行内启停：选中行 + 「停用/启用」
// 翻转 ChannelModels.Enabled——聚合页唯一写操作（只读 + 启停）。
func TestModelsAggregateToggleEnabled(t *testing.T) {
	u := aggFixture(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 1000, 700)
	u.modelSel = 0
	if err := tt.Click("停用/启用"); err != nil {
		t.Fatal(err)
	}
	rows := bridge.ModelsAgg{A: u.svc.Channels.A}.ListAll()
	if rows[0].Enabled {
		t.Fatalf("toggle failed: %+v", rows[0])
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
