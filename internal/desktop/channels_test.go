package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestChannelsPageCreateAndList(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	if !tt.HasText("渠道") || !tt.HasText("尚无渠道") {
		t.Fatalf("texts %v", tt.Texts())
	}
	u.chForm.name = "main"
	u.chForm.baseURL = "https://api.example.com"
	if err := tt.Click("保存渠道"); err != nil {
		t.Fatal(err)
	}
	if got := u.svc.Channels.List(); len(got) != 1 || got[0].Name != "main" || got[0].KeyCount != 0 {
		t.Fatalf("channels %+v", got)
	}
	if u.chForm.name != "" {
		t.Fatal("form must reset after create")
	}
}

func TestChannelsTypePresetPrefillsBaseURL(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	// 模拟下拉切到 OpenAI 兼容（UI 内即 Select 值变更）
	u.chForm.typeID = "openai-compatible"
	if err := tt.Click("保存渠道"); err != nil {
		t.Fatal(err)
	}
	// 兼容类无默认 URL 且表单为空 → 报错留在表单
	if u.chForm.msg == "" {
		t.Fatalf("empty baseURL must surface: %v", u.chForm.msg)
	}
	u.chForm.baseURL = "https://o.example.com"
	if err := tt.Click("保存渠道"); err != nil {
		t.Fatal(err)
	}
	got := u.svc.Channels.List()
	if len(got) != 1 || got[0].Protocol != "openai" {
		t.Fatalf("channels %+v", got)
	}
}

func TestChannelsLoadSelectedAndDeleteConfirm(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	// 渠道表按 (priority, id) 排序——同优先级时行序随随机 id 摆动，
	// 故 a 用更低优先级钉死行序（行 0=a、行 1=b），断言才确定。
	u.chForm.name, u.chForm.baseURL, u.chForm.priorityInput = "a", "https://a.example.com", "1"
	_ = tt.Click("保存渠道")
	u.chForm.name, u.chForm.baseURL = "b", "https://b.example.com"
	_ = tt.Click("保存渠道")
	u.chSel = 0
	if err := tt.Click("载入所选"); err != nil {
		t.Fatal(err)
	}
	if u.chForm.editID == "" || u.chForm.name != "a" {
		t.Fatalf("load selected: %+v", u.chForm)
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !u.chConfirmDelete {
		t.Fatal("first click arms confirm")
	}
	u.chSel = 1
	_ = tt.Click("载入所选")
	if u.chConfirmDelete {
		t.Fatal("loading other row disarms confirm")
	}
}

func TestChannelsKeysBatchAdd(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	u.chForm.name, u.chForm.baseURL = "m", "https://m.example.com"
	_ = tt.Click("保存渠道")
	u.chSel = 0
	u.keyForm.name = "pool"
	u.keyForm.keysText = "sk-aaa11111\n\nsk-bbb22222\n"
	if err := tt.Click("导入密钥"); err != nil {
		t.Fatal(err)
	}
	d, err := u.svc.Channels.Get(u.svc.Channels.List()[0].ID)
	if err != nil || len(d.Keys) != 2 {
		t.Fatalf("keys %+v err %v", d.Keys, err)
	}
	if u.keyForm.keysText != "" || u.keyForm.name != "" {
		t.Fatal("key form must reset after import")
	}
}

func TestChannelsModelAddAndDuplicate(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	u.chForm.name, u.chForm.baseURL = "m", "https://m.example.com"
	_ = tt.Click("保存渠道")
	u.chModelForm.public, u.chModelForm.upstream = "pub", "up"
	_ = tt.Click("添加模型")
	d, _ := u.svc.Channels.Get(u.svc.Channels.List()[0].ID)
	if len(d.Models) != 1 || d.Models[0].PublicModel != "pub" {
		t.Fatalf("models %+v", d.Models)
	}
	u.chModelForm.public, u.chModelForm.upstream = "pub", "up2"
	_ = tt.Click("添加模型")
	if u.chModelForm.msg == "" {
		t.Fatal("duplicate must surface in model form msg")
	}
}

func TestChannelsNavSwitch(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 900, 600)
	if err := tt.Click("渠道"); err != nil {
		t.Fatal(err)
	}
	if u.page != "channels" {
		t.Fatalf("page %q", u.page)
	}
	if !tt.HasText("创建第一个渠道") && !tt.HasText("尚无渠道") {
		t.Fatalf("channels page hint missing: %v", tt.Texts())
	}
}

// TestChannelsModelDeleteTwoStage pin 审查 Finding 1（全局约束「删除一律
// 两段确认」，简报模型段代码漏了）：「删除模型」首击仅武装——不落刀、
// 列表长度不变；「确认删除模型」才调 RemoveModel，长度减一。
func TestChannelsModelDeleteTwoStage(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	u.chForm.name, u.chForm.baseURL = "m", "https://m.example.com"
	_ = tt.Click("保存渠道") // 创建即自动选中，详情面板立即可用
	u.chModelForm.public, u.chModelForm.upstream = "pub1", "up1"
	_ = tt.Click("添加模型")
	u.chModelForm.public, u.chModelForm.upstream = "pub2", "up2"
	_ = tt.Click("添加模型")
	u.chModelSel = 0
	if err := tt.Click("删除模型"); err != nil {
		t.Fatal(err)
	}
	if !u.chModelConfirmDel {
		t.Fatal("first click must arm the confirmation")
	}
	d, _ := u.svc.Channels.Get(u.svc.Channels.List()[0].ID)
	if len(d.Models) != 2 {
		t.Fatalf("arm must not delete: %+v", d.Models)
	}
	if err := tt.Click("确认删除模型"); err != nil {
		t.Fatal(err)
	}
	d, _ = u.svc.Channels.Get(u.svc.Channels.List()[0].ID)
	if len(d.Models) != 1 || d.Models[0].PublicModel != "pub2" {
		t.Fatalf("confirm must remove the selected row: %+v", d.Models)
	}
	if u.chModelConfirmDel {
		t.Fatal("confirm must disarm")
	}
}

// TestChannelsKeyConfirmBoundToChannel pin 审查 Finding 2：确认位绑定渠道
// ——A 行武装删除密钥后切到 B 行，B 面板下不得渲染「确认删除密钥」
// （owner 不匹配视同未武装；渠道变更检测另清子选中与确认位），
// B 的密钥数保持 0。
func TestChannelsKeyConfirmBoundToChannel(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	// a 优先级更低，钉死表序（行 0=a、行 1=b，同 TestChannelsLoadSelectedAndDeleteConfirm）。
	u.chForm.name, u.chForm.baseURL, u.chForm.priorityInput = "a", "https://a.example.com", "1"
	_ = tt.Click("保存渠道")
	u.chForm.name, u.chForm.baseURL = "b", "https://b.example.com"
	_ = tt.Click("保存渠道")
	// 创建 b 后自动选中行 1——详情此刻是 b；显式选回行 0 让详情回到 a，
	// 密钥才导进 a（细节面板跟随行选中）。
	u.chSel = 0
	tt.Frame()
	// A 渠道导一枚密钥，选中并武装删除确认。
	u.keyForm.name = "k"
	u.keyForm.keysText = "sk-aaa11111"
	if err := tt.Click("导入密钥"); err != nil {
		t.Fatal(err)
	}
	u.chKeySel = 0
	if err := tt.Click("删除密钥"); err != nil {
		t.Fatal(err)
	}
	if !u.keyConfirmDelete || !tt.HasText("确认删除密钥") {
		t.Fatalf("arm on channel A: armed=%v", u.keyConfirmDelete)
	}
	// 切到 B 行：确认钮消失，B 的密钥数不变。
	u.chSel = 1
	tt.Frame()
	if tt.HasText("确认删除密钥") {
		t.Fatalf("confirm must not render for another channel: %v", tt.Texts())
	}
	d, _ := u.svc.Channels.Get(u.svc.Channels.List()[1].ID)
	if len(d.Keys) != 0 {
		t.Fatalf("channel B keys must be untouched: %+v", d.Keys)
	}
}
