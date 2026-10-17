package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/telemetry"
)

// moved: Task 7（列表态移除内嵌创建表单，创建走整页新建；Task 7 落地后
// 经新页面路径重接线，断言本体保留不删）
func TestChannelsPageCreateAndList(t *testing.T) {
	t.Skip("moved: Task 7")
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

// moved: Task 7（类型预填逻辑随创建表单迁往整页新建页；断言本体保留不删）
func TestChannelsTypePresetPrefillsBaseURL(t *testing.T) {
	t.Skip("moved: Task 7")
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

// moved: Task 7（「载入所选」与两段式按钮删除随表单迁走；列表删除改
// AlertDialog——新覆盖见 TestChannelListCardsAndDelete。断言本体保留不删）
func TestChannelsLoadSelectedAndDeleteConfirm(t *testing.T) {
	t.Skip("moved: Task 7")
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

// moved: Task 7（密钥面板随详情迁出列表页；Task 6 改经详情页路径驱动，
// Task 7 收口。断言本体保留不删）
func TestChannelsKeysBatchAdd(t *testing.T) {
	t.Skip("moved: Task 7")
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

// moved: Task 7（模型面板随详情迁出列表页；断言本体保留不删）
func TestChannelsModelAddAndDuplicate(t *testing.T) {
	t.Skip("moved: Task 7")
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
	// 空态文案随 Task 5 卡片列表改版为「还没有渠道」（spec §5.2 空态引导），
	// 语义不变：空渠道时页面上必须有一句引导。
	if !tt.HasText("还没有渠道") {
		t.Fatalf("channels page hint missing: %v", tt.Texts())
	}
}

// TestChannelsModelDeleteTwoStage pin 审查 Finding 1（全局约束「删除一律
// 两段确认」，简报模型段代码漏了）：「删除模型」首击仅武装——不落刀、
// 列表长度不变；「确认删除模型」才调 RemoveModel，长度减一。
// moved: Task 7（模型两段确认随详情迁出列表页，Task 6 改 AlertDialog；
// 断言本体保留不删）
func TestChannelsModelDeleteTwoStage(t *testing.T) {
	t.Skip("moved: Task 7")
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
// moved: Task 7（密钥确认位绑定随详情迁出列表页，Task 6 平移
// confirmOwnerID 机制；断言本体保留不删）
func TestChannelsKeyConfirmBoundToChannel(t *testing.T) {
	t.Skip("moved: Task 7")
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
	if u.keyForm.msg == "" {
		t.Fatal("import must leave a result msg on channel A")
	}
	u.chKeySel = 0
	if err := tt.Click("删除密钥"); err != nil {
		t.Fatal(err)
	}
	if !u.keyConfirmDelete || !tt.HasText("确认删除密钥") {
		t.Fatalf("arm on channel A: armed=%v", u.keyConfirmDelete)
	}
	// 切到 B 行：确认钮消失，B 的密钥数不变；A 语境的操作提示一并清除
	//（Task 8 复审 Finding：提示残留跨渠道误导）。
	u.chSel = 1
	tt.Frame()
	if tt.HasText("确认删除密钥") {
		t.Fatalf("confirm must not render for another channel: %v", tt.Texts())
	}
	if u.keyForm.msg != "" || u.chModelForm.msg != "" || u.chTestMsg != "" {
		t.Fatalf("switching channels must clear stale msgs: key=%q model=%q test=%q",
			u.keyForm.msg, u.chModelForm.msg, u.chTestMsg)
	}
	d, _ := u.svc.Channels.Get(u.svc.Channels.List()[1].ID)
	if len(d.Keys) != 0 {
		t.Fatalf("channel B keys must be untouched: %+v", d.Keys)
	}
}

func TestChannelStats(t *testing.T) {
	recs := []telemetry.RequestRecord{{Channel: "main", Status: 200}, {Channel: "main", Status: 500}, {Channel: "other", Status: 200}}
	ok, fail := channelStats(recs, "main")
	if ok != 1 || fail != 1 {
		t.Fatalf("%d %d", ok, fail)
	}
}
func TestChannelCardToggleKeepsFields(t *testing.T) {
	u, _ := testUI(t)
	v, err := u.svc.Channels.Create("anthropic-official", "main", "https://a.example.com", 10, true, true)
	if err != nil {
		t.Fatal(err)
	}
	ui.NewTester(u.channelsView, 1080, 720)
	u.toggleChannelEnabled(v.ID, false)
	got := u.svc.Channels.List()[0]
	if got.Enabled || got.BaseURL != "https://a.example.com" || got.Priority != 10 || !got.AllowPrivateUpstream {
		t.Fatalf("toggle 丢字段: %+v", got)
	}
}
func TestChannelListCardsAndDelete(t *testing.T) {
	u, _ := testUI(t)
	v, _ := u.svc.Channels.Create("anthropic-official", "main", "https://a.example.com", 10, true, false)
	tt := ui.NewTester(u.channelsView, 1080, 720)
	if !tt.HasText("首选") || !tt.HasText("https://a.example.com") {
		t.Fatalf("%v", tt.Texts())
	}
	u.chDeleteArm, u.chDeleteID = true, v.ID
	tt = ui.NewTester(u.channelsView, 1080, 720)
	if err := tt.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	if len(u.svc.Channels.List()) != 0 {
		t.Fatal("确认后须删除")
	}
}
func TestChannelCardOpensDetail(t *testing.T) {
	u, _ := testUI(t)
	v, _ := u.svc.Channels.Create("anthropic-official", "main", "https://a.example.com", 10, true, false)
	tt := ui.NewTester(u.channelsView, 1080, 720)
	if err := tt.Click("main"); err != nil {
		t.Fatal(err)
	}
	if u.page != "channel-detail" || u.chDetailID != v.ID {
		t.Fatalf("%q %q", u.page, u.chDetailID)
	}
}
