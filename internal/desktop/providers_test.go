package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestProvidersFormAndCreate 覆盖供应商页的新建保存路径：Create 只走
// ValidateBaseURL 纯解析（无网络），断言 store 落一条 anthropic 记录。
func TestProvidersFormAndCreate(t *testing.T) {
	u, _ := testUI(t)
	u.page = "providers"
	tt := ui.NewTester(u.providersView, 1000, 700)
	if !tt.HasText("供应商") {
		t.Fatalf("texts %v", tt.Texts())
	}
	u.provForm.name = "main"
	u.provForm.protocol = "anthropic"
	u.provForm.baseURL = "https://api.example.com"
	u.provForm.enabled = true
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	// Create 会走网络校验？不会：ValidateBaseURL 是纯解析。List 应出现
	if got := u.svc.Providers.List(); len(got) != 1 || got[0].Name != "main" {
		t.Fatalf("providers %+v", got)
	}
}

// TestProvidersRejectBadProtocol 覆盖协议错误透传：Create 拒非 anthropic，
// 错误必须落到表单 msg（而非静默丢弃）。
func TestProvidersRejectBadProtocol(t *testing.T) {
	u, _ := testUI(t)
	u.page = "providers"
	tt := ui.NewTester(u.providersView, 1000, 700)
	u.provForm.name = "x"
	u.provForm.protocol = "openai"
	u.provForm.baseURL = "https://x.example.com"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if u.provForm.msg == "" {
		t.Fatal("protocol error must surface in form msg")
	}
}

// TestProvidersLoadSelectedWiresRowAndClearsConfirm 覆盖分支门①：provList 必须
// 预挂 Selected 指针（mygo ui.ListState.Selected 为 nil 时行点击不可选，"载入
// 所选/测试/删除/更新"全成死按钮）——手动置 provSel 后"载入所选"须回填表单；
// 以及三页 confirm 清位：A 行武装删除确认、载入 B 行后确认位必须落空，防
// "确认删除"落刀到 B。
func TestProvidersLoadSelectedWiresRowAndClearsConfirm(t *testing.T) {
	u, _ := testUI(t)
	u.page = "providers"
	tt := ui.NewTester(u.providersView, 1000, 700)
	if !tt.HasText("供应商") {
		t.Fatalf("texts %v", tt.Texts())
	}
	for _, name := range []string{"main", "second"} {
		u.provForm = provFormData{name: name, protocol: "anthropic",
			baseURL: "https://api.example.com", enabled: true}
		if err := tt.Click("保存"); err != nil {
			t.Fatal(err)
		}
	}
	if got := u.svc.Providers.List(); len(got) != 2 {
		t.Fatalf("providers %+v", got)
	}
	// 行选中接线：provSel 指向第 0 行 → 载入所选回填 editID 与 name。
	u.provSel = 0
	if err := tt.Click("载入所选"); err != nil {
		t.Fatal(err)
	}
	if u.provForm.editID == "" || u.provForm.name != "main" {
		t.Fatalf("load selected (row wiring): %+v", u.provForm)
	}
	// A 行武装删除确认。
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !u.provConfirmDelete {
		t.Fatal("first delete click must only arm the confirmation")
	}
	// 载入 B 行 → 表单换 B 且确认位清空（否则"确认删除"落刀 B）。
	u.provSel = 1
	if err := tt.Click("载入所选"); err != nil {
		t.Fatal(err)
	}
	if u.provForm.name != "second" || u.provForm.editID == "" {
		t.Fatalf("load row B: %+v", u.provForm)
	}
	if u.provConfirmDelete {
		t.Fatal("loading another row must disarm the delete confirmation")
	}
}

// TestNavigationSwitchesPage 覆盖 view 顶部导航行：概览/供应商按钮切换
// u.page 并分发到对应页面（四按钮全量覆盖见 TestNavigationCoversAllPages）。
func TestNavigationSwitchesPage(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 900, 600)
	if !tt.HasText("概览") || !tt.HasText("供应商") {
		t.Fatalf("nav buttons missing: %v", tt.Texts())
	}
	if err := tt.Click("供应商"); err != nil {
		t.Fatal(err)
	}
	if u.page != "providers" {
		t.Fatalf("page = %q, want providers", u.page)
	}
	if tt.HasText("访问令牌") {
		t.Fatalf("overview leaked into providers page: %v", tt.Texts())
	}
	if err := tt.Click("概览"); err != nil {
		t.Fatal(err)
	}
	if u.page != "overview" {
		t.Fatalf("page = %q, want overview", u.page)
	}
	if !tt.HasText("访问令牌") {
		t.Fatalf("overview view missing: %v", tt.Texts())
	}
}

// TestProvidersEmptyHint 覆盖空表引导（spec §5.4）。
func TestProvidersEmptyHint(t *testing.T) {
	u, _ := testUI(t)
	u.page = "providers"
	tt := ui.NewTester(u.providersView, 1000, 700)
	if !tt.HasText("尚无供应商") {
		t.Fatalf("empty hint missing: %v", tt.Texts())
	}
}
