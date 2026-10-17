package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/telemetry"
)

// TestNewPageChipPrefillsBaseURL pin 新建页类型 chips（取代 Select）：
// 点选 chip 即写 typeID 并预填该预设默认 BaseURL。label 取 provider.Presets()
// 实际值（Anthropic 官方）。
func TestNewPageChipPrefillsBaseURL(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.channelNewView, 1080, 720)
	if err := tt.Click("Anthropic 官方"); err != nil {
		t.Fatal(err)
	}
	if u.chForm.baseURL != "https://api.anthropic.com" {
		t.Fatalf("chip 预填 %q", u.chForm.baseURL)
	}
}

// TestNewPageCreateAndKeys pin 新建页创建+密钥随建随导：填表单与批量密钥，
// 点「创建渠道」→ 落回列表，渠道带 2 枚密钥。
func TestNewPageCreateAndKeys(t *testing.T) {
	u, _ := testUI(t)
	u.chForm.name, u.chForm.baseURL, u.chForm.priorityInput = "main", "https://a.example.com", "10"
	u.keyForm.keysText = "sk-a\nsk-b"
	tt := ui.NewTester(u.channelNewView, 1080, 720)
	if err := tt.Click("创建渠道"); err != nil {
		t.Fatal(err)
	}
	if u.page != "channels" {
		t.Fatalf("page %q", u.page)
	}
	d, err := u.svc.Channels.Get(u.svc.Channels.List()[0].ID)
	if err != nil || len(d.Keys) != 2 {
		t.Fatalf("keys %v err %v", d.Keys, err)
	}
}

// TestUpdateFromDetailKeepsFields pin 详情页「保存渠道」Update 全字段回写：
// 打开详情即回显（字段须等于渠道现值），改名保存后其余字段不丢。
func TestUpdateFromDetailKeepsFields(t *testing.T) {
	u, _ := testUI(t)
	v, _ := u.svc.Channels.Create("anthropic-official", "main", "https://a.example.com", 10, true, true)
	u.openChannelDetail(v.ID)
	ui.NewTester(u.channelDetailView, 1080, 720) // 打开详情即回显
	if u.chForm.name != "main" || u.chForm.baseURL != "https://a.example.com" || !u.chForm.allowPrivate {
		t.Fatalf("详情回显丢字段: %+v", u.chForm)
	}
	u.chForm.name = "renamed"
	tt := ui.NewTester(u.channelDetailView, 1080, 720)
	if err := tt.Click("保存渠道"); err != nil {
		t.Fatal(err)
	}
	got := u.svc.Channels.List()[0]
	if got.Name != "renamed" || got.BaseURL != "https://a.example.com" || got.Priority != 10 || !got.AllowPrivateUpstream {
		t.Fatalf("update 丢字段: %+v", got)
	}
}

// TestNewPageAddKeysFailKeepsOneChannel pin 密钥随建随导失败分支（审查
// round 1）：Create 成功后 AddKeys 失败——渠道保留且只建一份（表单复位防
// 重点击克隆同名渠道），指路提示走列表信息位（chTestMsg）明示「已创建、
// 去详情密钥池重试」，并落回列表。
func TestNewPageAddKeysFailKeepsOneChannel(t *testing.T) {
	u, _ := testUI(t)
	u.chForm.name, u.chForm.baseURL, u.chForm.priorityInput = "main", "https://a.example.com", "10"
	u.keyForm.keysText = "\n  \n" // 非空但无有效行 → AddKeys 失败
	tt := ui.NewTester(u.channelNewView, 1080, 720)
	if err := tt.Click("创建渠道"); err != nil {
		t.Fatal(err)
	}
	if u.page != "channels" {
		t.Fatalf("page %q", u.page)
	}
	got := u.svc.Channels.List()
	if len(got) != 1 || got[0].Name != "main" {
		t.Fatalf("渠道须保留且不重复: %+v", got)
	}
	d, err := u.svc.Channels.Get(got[0].ID)
	if err != nil || len(d.Keys) != 0 {
		t.Fatalf("keys %v err %v", d.Keys, err)
	}
	if !strings.Contains(u.chTestMsg, "渠道已创建") || !strings.Contains(u.chTestMsg, "密钥导入失败") ||
		!strings.Contains(u.chTestMsg, "密钥池") {
		t.Fatalf("须指路密钥失败提示: %q", u.chTestMsg)
	}
	if u.chForm.name != "" || u.keyForm.keysText != "" {
		t.Fatalf("表单须复位防重创建: name=%q keys=%q", u.chForm.name, u.keyForm.keysText)
	}
	ttl := ui.NewTester(u.channelsView, 1080, 720)
	if !ttl.HasText("渠道已创建") {
		t.Fatalf("列表须显示指路提示: %v", ttl.Texts())
	}
}

// TestChannelsPageCreateAndList 原列表内嵌表单创建路径改写为整页新建
// （Task 7）：空态引导 → 「＋ 添加渠道」进新建页 → 「创建渠道」落库并复位
// 表单。空态文案随 Task 5 改为「还没有渠道」（TestChannelsNavSwitch 同款
// 适配）；断言本体保留不删。
func TestChannelsPageCreateAndList(t *testing.T) {
	u, _ := testUI(t)
	u.page = "channels"
	tt := ui.NewTester(u.channelsView, 1000, 700)
	if !tt.HasText("渠道") || !tt.HasText("还没有渠道") {
		t.Fatalf("texts %v", tt.Texts())
	}
	if err := tt.Click("＋ 添加渠道"); err != nil {
		t.Fatal(err)
	}
	if u.page != "channel-new" {
		t.Fatalf("page %q", u.page)
	}
	u.chForm.name = "main"
	u.chForm.baseURL = "https://api.example.com"
	ttn := ui.NewTester(u.channelNewView, 1000, 700)
	if err := ttn.Click("创建渠道"); err != nil {
		t.Fatal(err)
	}
	if got := u.svc.Channels.List(); len(got) != 1 || got[0].Name != "main" || got[0].KeyCount != 0 {
		t.Fatalf("channels %+v", got)
	}
	if u.chForm.name != "" {
		t.Fatal("form must reset after create")
	}
}

// TestChannelsTypePresetPrefillsBaseURL 原类型预填语义改写到新建页 chips：
// 点「OpenAI 兼容」chip 预填空 BaseURL（兼容类无默认值）→ 空 URL 提交报错
// 留表单 → 填 URL 后创建成功且 Protocol=openai。断言本体保留不删。
func TestChannelsTypePresetPrefillsBaseURL(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.channelNewView, 1000, 700)
	if err := tt.Click("OpenAI 兼容"); err != nil {
		t.Fatal(err)
	}
	if u.chForm.baseURL != "" {
		t.Fatalf("compatible preset must clear baseURL: %q", u.chForm.baseURL)
	}
	if err := tt.Click("创建渠道"); err != nil {
		t.Fatal(err)
	}
	// 兼容类无默认 URL 且表单为空 → 报错留在表单
	if u.chForm.msg == "" {
		t.Fatalf("empty baseURL must surface: %v", u.chForm.msg)
	}
	u.chForm.baseURL = "https://o.example.com"
	if err := tt.Click("创建渠道"); err != nil {
		t.Fatal(err)
	}
	got := u.svc.Channels.List()
	if len(got) != 1 || got[0].Protocol != "openai" {
		t.Fatalf("channels %+v", got)
	}
}

// TestChannelsLoadSelectedAndDeleteConfirm 「载入所选」语义随旧表单退役改写
// 为打开详情回显（原 loading other row = 切换渠道重载另一行现值；保存回写
// 另由 TestUpdateFromDetailKeepsFields 钉住）；列表删除两段式平移到卡片
// AlertDialog 路径：⋯菜单「删除」首击只武装不落刀，「取消」不删（确认落刀
// 见 TestChannelListCardsAndDelete——断言本体保留不删，只平移）。
func TestChannelsLoadSelectedAndDeleteConfirm(t *testing.T) {
	u, _ := testUI(t)
	// 渠道表按 (priority, id) 排序——同优先级时行序随随机 id 摆动，
	// 故 a 用更低优先级钉死行序（行 0=a、行 1=b），断言才确定。
	a, _ := u.svc.Channels.Create("anthropic-official", "a", "https://a.example.com", 1, true, true)
	b, _ := u.svc.Channels.Create("anthropic-official", "b", "https://b.example.com", 2, true, false)
	// 载入所选 → 打开详情：表单字段须等于渠道现值
	u.openChannelDetail(a.ID)
	ui.NewTester(u.channelDetailView, 1080, 720)
	if u.chForm.editID != a.ID || u.chForm.name != "a" || u.chForm.baseURL != "https://a.example.com" ||
		u.chForm.priorityInput != "1" || !u.chForm.allowPrivate {
		t.Fatalf("load detail a: %+v", u.chForm)
	}
	// 切到另一行（原「载入所选」另一行）：回显 b 现值
	u.openChannelDetail(b.ID)
	ui.NewTester(u.channelDetailView, 1080, 720)
	if u.chForm.editID != b.ID || u.chForm.name != "b" || u.chForm.baseURL != "https://b.example.com" ||
		u.chForm.priorityInput != "2" || u.chForm.allowPrivate {
		t.Fatalf("load detail b: %+v", u.chForm)
	}
	// 列表删除两段式：首击武装不落刀，「取消」不删
	tt := ui.NewTester(u.channelsView, 1080, 720)
	if err := tt.Click("⋯"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("删除"); err != nil {
		t.Fatal(err)
	}
	if !u.chDeleteArm || u.chDeleteID == "" {
		t.Fatal("first click arms confirm")
	}
	if len(u.svc.Channels.List()) != 2 {
		t.Fatal("arm must not delete")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if len(u.svc.Channels.List()) != 2 {
		t.Fatal("cancel must not delete")
	}
}

// TestChannelsKeysBatchAdd 经详情页密钥池段驱动批量导入（Task 6 路径）：
// openChannelDetail 锚定渠道 → 「＋ 批量导入」开表单 → 导入密钥；空行跳过、
// 导入后表单复位语义与旧列表页一致。
func TestChannelsKeysBatchAdd(t *testing.T) {
	u, _ := testUI(t)
	v, _ := u.svc.Channels.Create("anthropic-official", "m", "https://m.example.com", 10, true, false)
	u.openChannelDetail(v.ID)
	u.chDetailSeg = 1 // 密钥池段
	tt := ui.NewTester(u.channelDetailView, 1080, 720)
	if err := tt.Click("＋ 批量导入"); err != nil {
		t.Fatal(err)
	}
	u.keyForm.name = "pool"
	u.keyForm.keysText = "sk-aaa11111\n\nsk-bbb22222\n"
	if err := tt.Click("导入密钥"); err != nil {
		t.Fatal(err)
	}
	d, err := u.svc.Channels.Get(v.ID)
	if err != nil || len(d.Keys) != 2 {
		t.Fatalf("keys %+v err %v", d.Keys, err)
	}
	if u.keyForm.keysText != "" || u.keyForm.name != "" {
		t.Fatal("key form must reset after import")
	}
}

// TestChannelsModelAddAndDuplicate 经详情页渠道模型段驱动：添加模型成功，
// 同名公开模型重复添加须把可读错误留在 chModelForm.msg。
func TestChannelsModelAddAndDuplicate(t *testing.T) {
	u, _ := testUI(t)
	v, _ := u.svc.Channels.Create("anthropic-official", "m", "https://m.example.com", 10, true, false)
	u.openChannelDetail(v.ID)
	u.chDetailSeg = 2 // 渠道模型段
	tt := ui.NewTester(u.channelDetailView, 1080, 720)
	u.chModelForm.public, u.chModelForm.upstream = "pub", "up"
	if err := tt.Click("添加模型"); err != nil {
		t.Fatal(err)
	}
	d, _ := u.svc.Channels.Get(v.ID)
	if len(d.Models) != 1 || d.Models[0].PublicModel != "pub" {
		t.Fatalf("models %+v", d.Models)
	}
	u.chModelForm.public, u.chModelForm.upstream = "pub", "up2"
	if err := tt.Click("添加模型"); err != nil {
		t.Fatal(err)
	}
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
// 两段确认」）：行尾 ⋯ 菜单「删除」首击仅武装——不落刀、列表长度不变；
// AlertDialog「确认删除模型」才调 RemoveModel，长度减一（取消居末位，==0 落刀）。
func TestChannelsModelDeleteTwoStage(t *testing.T) {
	u, _ := testUI(t)
	v, _ := u.svc.Channels.Create("anthropic-official", "m", "https://m.example.com", 10, true, false)
	u.openChannelDetail(v.ID)
	u.chDetailSeg = 2 // 渠道模型段
	tt := ui.NewTester(u.channelDetailView, 1080, 720)
	u.chModelForm.public, u.chModelForm.upstream = "pub1", "up1"
	_ = tt.Click("添加模型")
	u.chModelForm.public, u.chModelForm.upstream = "pub2", "up2"
	_ = tt.Click("添加模型")
	// 行尾 ⋯ 菜单「删除」：武装确认（AlertDialog 随即弹出），不落刀。
	if err := tt.Click("⋯"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("删除"); err != nil {
		t.Fatal(err)
	}
	if !u.chModelConfirmDel {
		t.Fatal("first click must arm the confirmation")
	}
	d, _ := u.svc.Channels.Get(v.ID)
	if len(d.Models) != 2 {
		t.Fatalf("arm must not delete: %+v", d.Models)
	}
	if err := tt.Click("确认删除模型"); err != nil {
		t.Fatal(err)
	}
	d, _ = u.svc.Channels.Get(v.ID)
	if len(d.Models) != 1 || d.Models[0].PublicModel != "pub2" {
		t.Fatalf("confirm must remove the selected row: %+v", d.Models)
	}
	if u.chModelConfirmDel {
		t.Fatal("confirm must disarm")
	}
}

// TestChannelsKeyConfirmBoundToChannel pin 审查 Finding 2：确认位绑定渠道
// ——A 详情武装删除密钥后 openChannelDetail 切到 B，B 下不得渲染「确认删除密钥」
// （owner 不匹配视同未武装；渠道变更检测另清子选中与确认位），
// B 的密钥数保持 0，A 语境的操作提示一并清除。
func TestChannelsKeyConfirmBoundToChannel(t *testing.T) {
	u, _ := testUI(t)
	a, _ := u.svc.Channels.Create("anthropic-official", "a", "https://a.example.com", 10, true, false)
	b, _ := u.svc.Channels.Create("anthropic-official", "b", "https://b.example.com", 20, true, false)
	u.openChannelDetail(a.ID)
	u.chDetailSeg = 1 // 密钥池段
	tt := ui.NewTester(u.channelDetailView, 1080, 720)
	// A 渠道导一枚密钥（结果提示属 A 语境）。
	if err := tt.Click("＋ 批量导入"); err != nil {
		t.Fatal(err)
	}
	u.keyForm.name = "k"
	u.keyForm.keysText = "sk-aaa11111"
	if err := tt.Click("导入密钥"); err != nil {
		t.Fatal(err)
	}
	if u.keyForm.msg == "" {
		t.Fatal("import must leave a result msg on channel A")
	}
	// 行尾 ⋯ 菜单「删除」：武装确认（绑定 A）。
	if err := tt.Click("⋯"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("删除"); err != nil {
		t.Fatal(err)
	}
	if !u.keyConfirmDelete || !tt.HasText("确认删除密钥") {
		t.Fatalf("arm on channel A: armed=%v", u.keyConfirmDelete)
	}
	// 切到 B 详情：确认钮消失，B 的密钥数不变；A 语境的操作提示一并清除
	//（Task 8 复审 Finding：提示残留跨渠道误导）。
	u.openChannelDetail(b.ID)
	tt = ui.NewTester(u.channelDetailView, 1080, 720)
	if tt.HasText("确认删除密钥") {
		t.Fatalf("confirm must not render for another channel: %v", tt.Texts())
	}
	if u.keyForm.msg != "" || u.chModelForm.msg != "" || u.chTestMsg != "" {
		t.Fatalf("switching channels must clear stale msgs: key=%q model=%q test=%q",
			u.keyForm.msg, u.chModelForm.msg, u.chTestMsg)
	}
	d, _ := u.svc.Channels.Get(b.ID)
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

func TestDetailSectionsRender(t *testing.T) {
	u, _ := testUI(t)
	v, _ := u.svc.Channels.Create("anthropic-official", "main", "https://a.example.com", 10, true, false)
	u.svc.Channels.AddKeys(v.ID, "k1", "sk-test-1234", false)
	u.openChannelDetail(v.ID)
	tt := ui.NewTester(u.channelDetailView, 1080, 720)
	for _, want := range []string{"连接配置", "密钥池 1", "渠道模型 0", "保存渠道", "连通测试"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
}
func TestDetailConfirmDisarmOnChannelSwitch(t *testing.T) {
	u, _ := testUI(t)
	v1, _ := u.svc.Channels.Create("anthropic-official", "c1", "https://1.example.com", 10, true, false)
	v2, _ := u.svc.Channels.Create("anthropic-official", "c2", "https://2.example.com", 20, true, false)
	u.openChannelDetail(v1.ID)
	u.keyConfirmDelete, u.confirmOwnerID = true, v1.ID // 模拟在 c1 武装
	u.openChannelDetail(v2.ID)                         // 切渠道必须清位
	if u.keyConfirmDelete || u.confirmOwnerID != "" {
		t.Fatal("切换后确认位必须解除")
	}
	tt := ui.NewTester(u.channelDetailView, 1080, 720)
	if tt.HasText("确认删除密钥") {
		t.Fatal("未重新武装不得出现确认钮")
	}
}
func TestDetailTypeChangeConfirm(t *testing.T) {
	u, _ := testUI(t)
	v, _ := u.svc.Channels.Create("anthropic-official", "c1", "https://api.anthropic.com", 10, true, false)
	u.openChannelDetail(v.ID)
	tt := ui.NewTester(u.channelDetailView, 1080, 720)
	u.chForm.typeID = "openai-compatible" // 模拟 Select 变更
	tt = ui.NewTester(u.channelDetailView, 1080, 720)
	if !u.chTypeResetConfirm {
		t.Fatal("已保存渠道换类型必须武装确认")
	}
	if err := tt.Click("确认更换"); err != nil {
		t.Fatal(err)
	}
	if u.chForm.baseURL == "https://api.anthropic.com" {
		t.Fatal("确认后须预填新预设默认 URL")
	}
}
