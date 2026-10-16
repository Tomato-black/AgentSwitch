package desktop

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/storage"
	"github.com/egoist/mygo/ui"
)

// testUIWithCreds 起一个带独立密钥存储的应用：Manager 以 0600 文件为
// Primary（无系统密钥环参与），Accounts 注入 KeychainAvailable=false（裁定 2
// 的测试注入缝）——不探测/不写真实钥匙环，且账号落库后端确定为 fallback-file
// （页顶降级警示条与"存储"列据此可断言）。
func testUIWithCreds(t *testing.T) (*uiState, *app.App, credential.Store) {
	t.Helper()
	a, err := app.Open(app.Options{
		DBPath: filepath.Join(t.TempDir(), "a.db"), LogPath: filepath.Join(t.TempDir(), "a.log"), Host: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	creds := &credential.Manager{
		Primary:       credential.NewFallbackFile(filepath.Join(t.TempDir(), "s.json")),
		AllowFallback: true,
	}
	svc := Services{
		Gateway: bridge.Gateway{A: a}, Providers: bridge.Providers{A: a},
		Accounts: bridge.Accounts{A: a, Creds: creds, KeychainAvailable: func() bool { return false }},
		Models:   bridge.Models{A: a},
	}
	return newUIState(svc, a), a, creds
}

// TestAccountsCreateFlow 覆盖账号页主路径：Atoi 报错入 msg（不落库、密钥保留
// 供重试）→ 修复后保存落库一行（优先级/启用/降级后端）、密钥输入清空、页顶
// 降级警示出现 → 载入所选回填且不带回密钥 → 更新携带新密钥走轮换（成功后密钥
// 同样清空、密钥本体已轮换）。KeyTail 尾 4 位按 spec §12.4 持久化契约断言。
func TestAccountsCreateFlow(t *testing.T) {
	u, a, creds := testUIWithCreds(t)
	if err := a.Store().Providers().Save(storage.Provider{
		ID: "p1", Name: "main", Protocol: "anthropic", BaseURL: "https://api.example.com", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	u.page = "accounts"
	tt := ui.NewTester(u.accountsView, 1000, 700)
	if !tt.HasText("账号") {
		t.Fatalf("texts %v", tt.Texts())
	}
	u.accForm.name = "acc1"
	u.accForm.providerName = "main" // 供应商下拉按名称选择，提交时映射回 ID
	u.accForm.apiKey = "sk-ant-secret1234"
	u.accForm.priorityInput = "abc" // 先走 Atoi 报错分支
	u.accForm.enabled = true
	u.accForm.allowFallback = true
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if u.accForm.msg == "" {
		t.Fatal("non-integer priority must surface in form msg")
	}
	if u.accForm.apiKey == "" {
		t.Fatal("failed submit must keep apiKey for retry")
	}
	if got := u.svc.Accounts.List(); len(got) != 0 {
		t.Fatalf("invalid form must not persist: %+v", got)
	}
	u.accForm.priorityInput = "10"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	got := u.svc.Accounts.List()
	if len(got) != 1 || got[0].Name != "acc1" {
		t.Fatalf("accounts %+v", got)
	}
	if got[0].Priority != 10 || !got[0].Enabled {
		t.Fatalf("row %+v", got[0])
	}
	// 裁定 2：KeychainAvailable 注入 false → 落库后端必为 fallback-file。
	if got[0].SecretBackend != "fallback-file" {
		t.Fatalf("backend %+v", got[0])
	}
	if u.accForm.apiKey != "" {
		t.Fatal("api key input must be cleared after save")
	}
	// 表单填的密钥本体确实由保存路径写入了存储（尾 4 位 1234）。
	sec, err := creds.Get(credential.AccountKey(got[0].ID))
	if err != nil || !strings.HasSuffix(string(sec), "1234") {
		t.Fatalf("stored secret %q %v", sec, err)
	}
	// 页顶降级警示条：保存发生在按钮行（视图底部），需再渲染一帧才可见。
	tt.Frame()
	if !tt.HasText("警告：凭据使用文件降级存储（未启用系统密钥环）") {
		t.Fatalf("fallback warning missing: %v", tt.Texts())
	}
	// 载入所选：回填名称/供应商/优先级/启用与 editID；任何密钥都不回显。
	u.accForm.apiKey = "sk-stale-echo-check"
	u.accSel = 0
	if err := tt.Click("载入所选"); err != nil {
		t.Fatal(err)
	}
	if u.accForm.editID == "" || u.accForm.name != "acc1" ||
		u.accForm.providerName != "main" || u.accForm.priorityInput != "10" {
		t.Fatalf("load selected %+v", u.accForm)
	}
	if u.accForm.apiKey != "" {
		t.Fatal("existing secret must not be echoed into the key input")
	}
	// 更新路径：新密钥先轮换（KeychainAvailable=false，代用户打开允许降级），
	// 再改名称/优先级；成功后表单复位、密钥清空。
	u.accForm.apiKey = "sk-ant-rotated-9999"
	u.accForm.allowFallback = true
	tt.Frame() // 载入后按钮文案 保存→更新，需再渲染一帧才能点到"更新"
	if err := tt.Click("更新"); err != nil {
		t.Fatal(err)
	}
	if u.accForm.msg != "" || u.accForm.apiKey != "" {
		t.Fatalf("update must reset form: %+v", u.accForm)
	}
	if sec, err := creds.Get(credential.AccountKey(got[0].ID)); err != nil || string(sec) != "sk-ant-rotated-9999" {
		t.Fatalf("rotate via update: %q %v", sec, err)
	}
	// 停用/启用：翻转所选行（accSel 仍指第 0 行）的启用位。
	if err := tt.Click("停用/启用"); err != nil {
		t.Fatal(err)
	}
	if u.svc.Accounts.List()[0].Enabled {
		t.Fatal("toggle must disable the selected row")
	}
	// 轮换密钥按钮（独立于更新分支的手动轮换）：载入 → 填新密钥 → 成功后
	// msg="密钥已轮换"、密钥输入清空、密钥本体已换。
	u.accSel = 0
	if err := tt.Click("载入所选"); err != nil {
		t.Fatal(err)
	}
	u.accForm.apiKey = "sk-ant-rotated-2222"
	u.accForm.allowFallback = true
	if err := tt.Click("轮换密钥"); err != nil {
		t.Fatal(err)
	}
	if u.accForm.msg != "密钥已轮换" || u.accForm.apiKey != "" {
		t.Fatalf("rotate button: %+v", u.accForm)
	}
	if sec, err := creds.Get(credential.AccountKey(got[0].ID)); err != nil || string(sec) != "sk-ant-rotated-2222" {
		t.Fatalf("rotate button secret: %q %v", sec, err)
	}
	// 删除两段式（模式同供应商页）：首击只置确认位，"确认删除"才落刀并复位。
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !u.accConfirmDelete || len(u.svc.Accounts.List()) != 1 {
		t.Fatalf("first delete click must only arm: armed=%v", u.accConfirmDelete)
	}
	// 载入所选必须清位（三页 confirm 清位）：武装后再载入，确认位落空，
	// 否则表单已是 B 行数据而"确认删除"仍会落刀。
	if err := tt.Click("载入所选"); err != nil {
		t.Fatal(err)
	}
	if u.accConfirmDelete {
		t.Fatal("loading another row must disarm the delete confirmation")
	}
	// 重新武装后确认删除才走原路径。
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !u.accConfirmDelete {
		t.Fatal("re-arm after disarm must succeed")
	}
	if err := tt.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	if u.accConfirmDelete || len(u.svc.Accounts.List()) != 0 {
		t.Fatalf("confirm must remove: armed=%v list=%+v", u.accConfirmDelete, u.svc.Accounts.List())
	}
	if u.accForm.editID != "" {
		t.Fatalf("form must reset after delete: %+v", u.accForm)
	}
	// KeyTail 尾 4 位（spec §12.4 持久化契约）：Create 返回的视图与后续 List
	// 回读同源（accounts.key_tail 列，Create/轮换时写入）——此处直调 Create
	// 断言返回视图的尾 4，List 回读由 bridge 的 TestAccountKeyTailVisibleInList
	// 覆盖。
	av, err := u.svc.Accounts.Create("acc-tail", "p1", "sk-ant-tail-5678", 2, true, true)
	if err != nil || av.KeyTail != "5678" {
		t.Fatalf("KeyTail %+v %v", av, err)
	}
}

// TestAccountsRequireProvider 覆盖 submitAccount 的无供应商分支：名称映射不到
// 任何供应商 ID 时报错入 msg、不落库（简报"请先在供应商页创建供应商"）。
func TestAccountsRequireProvider(t *testing.T) {
	u, _, _ := testUIWithCreds(t)
	u.page = "accounts"
	tt := ui.NewTester(u.accountsView, 1000, 700)
	u.accForm.name = "acc1"
	u.accForm.providerName = "main" // 尚无供应商，映射为空
	u.accForm.apiKey = "sk-ant-secret1234"
	u.accForm.priorityInput = "10"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if u.accForm.msg == "" {
		t.Fatal("missing provider error must surface in form msg")
	}
	if len(u.svc.Accounts.List()) != 0 {
		t.Fatal("must not persist without a provider")
	}
}

// TestAccountsTestButton 覆盖裁定 1 的账号页"测试"按钮（简报 UI 原缺，spec
// §9/§12.4 授权补落点）：同步探测回环上游（全离线，httptest 127.0.0.1），结果
// 进账号页专属 accTestMsg——失败文案 "stage 失败: err"、成功文案 "连通正常
// (stage)"，均不碰表单 msg；取 id 的两条路径（所选行 / 载入后 editID）都走到。
func TestAccountsTestButton(t *testing.T) {
	u, a, _ := testUIWithCreds(t)
	var upstream atomic.Int32
	upstream.Store(http.StatusUnauthorized)
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(upstream.Load()))
	}))
	t.Cleanup(up.Close)
	if err := a.Store().Providers().Save(storage.Provider{
		ID: "p1", Name: "main", Protocol: "anthropic", BaseURL: up.URL, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := u.svc.Accounts.Create("acc1", "p1", "sk-ant-secret1234", 1, true, true); err != nil {
		t.Fatal(err)
	}
	u.page = "accounts"
	tt := ui.NewTester(u.accountsView, 1000, 700)
	if !tt.HasText("测试") {
		t.Fatalf("test button missing: %v", tt.Texts())
	}
	// 未载入：id 取所选行。
	u.accSel = 0
	if err := tt.Click("测试"); err != nil {
		t.Fatal(err)
	}
	if u.accTestMsg != "auth 失败: upstream status 401" {
		t.Fatalf("accTestMsg %q", u.accTestMsg)
	}
	if u.accForm.msg != "" {
		t.Fatalf("probe must not touch form msg: %q", u.accForm.msg)
	}
	// 已载入且行未选：id 只能取 editID（证明第二条取 id 路径真的生效）。
	if err := tt.Click("载入所选"); err != nil {
		t.Fatal(err)
	}
	if u.accForm.editID == "" {
		t.Fatalf("load failed: %+v", u.accForm)
	}
	u.accSel = -1
	u.accTestMsg = ""
	if err := tt.Click("测试"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.accTestMsg, "auth 失败: ") {
		t.Fatalf("editID path accTestMsg %q", u.accTestMsg)
	}
	// 上游转 200 → 成功文案 "连通正常 (stage)"。
	upstream.Store(http.StatusOK)
	u.accTestMsg = ""
	if err := tt.Click("测试"); err != nil {
		t.Fatal(err)
	}
	if u.accTestMsg != "连通正常 (auth)" {
		t.Fatalf("ok accTestMsg %q", u.accTestMsg)
	}
}

// TestNavigationCoversAllPages 覆盖导航行四按钮与 view 分发：概览/供应商/
// 账号/模型均可切换（账号与模型 case 属本任务）。
func TestNavigationCoversAllPages(t *testing.T) {
	u, _, _ := testUIWithCreds(t)
	tt := ui.NewTester(u.view, 1000, 700)
	for _, b := range []string{"概览", "供应商", "账号", "模型"} {
		if !tt.HasText(b) {
			t.Fatalf("nav button %q missing: %v", b, tt.Texts())
		}
	}
	if err := tt.Click("账号"); err != nil {
		t.Fatal(err)
	}
	if u.page != "accounts" {
		t.Fatalf("page %q, want accounts", u.page)
	}
	if err := tt.Click("模型"); err != nil {
		t.Fatal(err)
	}
	if u.page != "models" {
		t.Fatalf("page %q, want models", u.page)
	}
}

// TestAccountsEmptyHint 覆盖空表引导（spec §5.4）。
func TestAccountsEmptyHint(t *testing.T) {
	u, _, _ := testUIWithCreds(t)
	u.page = "accounts"
	tt := ui.NewTester(u.accountsView, 1000, 700)
	if !tt.HasText("尚无账号") {
		t.Fatalf("empty hint missing: %v", tt.Texts())
	}
}
