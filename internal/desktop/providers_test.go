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
	if tt.HasText("网关状态") {
		t.Fatalf("overview leaked into providers page: %v", tt.Texts())
	}
	if err := tt.Click("概览"); err != nil {
		t.Fatal(err)
	}
	if u.page != "overview" {
		t.Fatalf("page = %q, want overview", u.page)
	}
	if !tt.HasText("网关状态") {
		t.Fatalf("overview view missing: %v", tt.Texts())
	}
}
