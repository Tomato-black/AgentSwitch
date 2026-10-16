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
