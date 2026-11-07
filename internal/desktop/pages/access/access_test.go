package access

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/pages/access/claude"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uitest"
	"AgentSwitch/internal/storage"
)

// TestTabDirtyExcludesClaudeCfg（2026-11-07 拆页）：设置区拆独立页后，
// ClaudeCfg 脏不再阻断接入页页签切换（由设置页的侧栏守卫承担）。
func TestTabDirtyIncludesClaudeCfg(t *testing.T) {
	st, _ := uitest.New(t)
	st.Access.App = "claude"
	if tabDirty(st) {
		t.Fatal("clean claude config must not block tab switch")
	}
	st.ClaudeCfg.Dirty = true
	if tabDirty(st) {
		t.Fatal("拆页后 ClaudeCfg 脏不应计入接入页签切换（侧栏守卫承担）")
	}
}

// TestAccessRepreviewForm 覆盖基线变化后的表单重预览（Important#1）：按当前
// 表单模型重新合成、成功清空旧消息、失败内联「预览失败：」。
func TestAccessRepreviewForm(t *testing.T) {
	st, a := uitest.New(t)
	for _, lm := range []storage.LogicalModel{
		{ID: "lm1", Name: "model-a", Enabled: true},
		{ID: "lm2", Name: "model-b", Enabled: true},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{
		Open:       true,
		ModelLabel: "model-b",
		DocJSON:    `{"env":{"ANTHROPIC_SMALL_FAST_MODEL":"model-a","CUSTOM":"x"}}`,
		Msg:        "旧文案",
	}
	// 主模型切换处理器的顺序：先按旧模型派生值重定位，再重预览。
	rebased, err := bridge.RebaseAccessDoc(st.Access.App, st.Access.Form.DocJSON,
		map[string]string{"ANTHROPIC_SMALL_FAST_MODEL": "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	st.Access.Form.DocJSON = rebased
	accesskit.RepreviewForm(st, configs(st))
	if st.Access.Form.Msg != "" {
		t.Fatalf("成功应清空 Msg: %q", st.Access.Form.Msg)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(st.Access.Form.DocJSON), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Env["ANTHROPIC_MODEL"] != "model-b" ||
		doc.Env["ANTHROPIC_SMALL_FAST_MODEL"] != "model-b" ||
		doc.Env["CUSTOM"] != "x" {
		t.Fatalf("预览未按当前模型合成: %s", st.Access.Form.DocJSON)
	}
	// 未知模型名 → PreviewDoc 报错 → 内联提示
	st.Access.Form.ModelLabel = "missing"
	accesskit.RepreviewForm(st, configs(st))
	if !strings.HasPrefix(st.Access.Form.Msg, "预览失败：") {
		t.Fatalf("失败应内联报错: %q", st.Access.Form.Msg)
	}
}

// TestAccessPreloadFormRendersGatewayToken 回归（2026-10-21 用户裁定）：编辑器
// 打开时默认渲染真实网关令牌，不再显示 __GATEWAY_TOKEN__ 遮蔽占位符。
func TestAccessPreloadFormRendersGatewayToken(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true}
	preloadForm(st, configs(st))
	token := st.Svc.Gateway.Status().Token
	if token == "" {
		t.Fatal("前提不成立：网关令牌应已生成")
	}
	if !strings.Contains(st.Access.Form.DocJSON, token) {
		t.Fatalf("编辑器应默认渲染真实令牌: %s", st.Access.Form.DocJSON)
	}
	if strings.Contains(st.Access.Form.DocJSON, "__GATEWAY_TOKEN__") {
		t.Fatalf("编辑器不应再出现遮蔽占位符: %s", st.Access.Form.DocJSON)
	}
}

// TestAccessCodexEditorShowsModelMetaEditor：Codex 编辑器渲染「模型元数据」
// 编辑区（2026-10-28 自逻辑模型页迁入）；Claude 编辑器不出现该区块。
func TestAccessCodexEditorShowsModelMetaPreview(t *testing.T) {
	st, a := uitest.New(t)
	a.Store().LogicalModels().Save(storage.LogicalModel{
		ID: "lm1", Name: "my-code", DisplayName: "My Code",
		ContextWindow: 1000000, ReasoningLevels: []string{"low", "high"},
		DefaultReasoningLevel: "high", Enabled: true,
	})
	st.Page = "access"
	st.Access.App = "codex"
	st.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	for _, want := range []string{"模型元数据", "保存配置并「应用」后生效"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	tt = ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if tt.HasText("模型元数据") || tt.HasText("保存配置并「应用」后生效") {
		t.Fatal("Claude 编辑器不应出现 Codex 元数据编辑区")
	}
}

// TestAccessGlobalConfigAppearsInEditorPreview：全局配置在列表页保存后，
// 再打开编辑器时预览应包含全局 env 并生成 Codex config.toml 预览。
func TestAccessGlobalConfigAppearsInEditorPreview(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	template := `{"models":[{"slug":"gpt-5","base_instructions":"test",` +
		`"model_messages":{"instructions_template":"test"}}]}`
	if err := os.WriteFile(filepath.Join(dir, "cc-switch-model-catalog.json"), []byte(template), 0o600); err != nil {
		t.Fatal(err)
	}
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Page = "access"
	st.Access.App = "codex"
	st.Access.Glob = [][2]string{{"HTTPS_PROXY", "http://p:1"}}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 1200)
	if err := tt.Click("保存全局配置"); err != nil {
		t.Fatal(err)
	}
	if st.Access.GlobMsg != "已保存" {
		t.Fatalf("全局配置保存失败: %q", st.Access.GlobMsg)
	}
	st.Access.Form = state.AccessForm{Open: true, CodexModels: []string{"my-code"}}
	preloadForm(st, configs(st))
	if !strings.Contains(st.Access.Form.DocJSON, "HTTPS_PROXY") {
		t.Fatalf("编辑器预览应合并全局 env: %q", st.Access.Form.DocJSON)
	}
	if !strings.Contains(st.Access.Form.CodexPreview, "model_catalog_json") {
		t.Fatalf("Codex 应生成 config.toml 预览: %q msg=%q", st.Access.Form.CodexPreview, st.Access.Form.Msg)
	}
}

// TestAccessDeleteRequiresConfirm 覆盖删除二次确认：行内「删除」只武装，
// 弹窗「取消」保留配置，「确认删除」才执行并给出结果提示。
func TestAccessDeleteRequiresConfirm(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a}
	if _, err := acc.Create("codex", "cfg", "lm1", "{}"); err != nil {
		t.Fatal(err)
	}
	// 列表卡用例走 codex（claude 的删除入口在接入抽屉，2026-11-07）。
	st.Access.App = "codex"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 800)
	// 次级操作收纳在「更多」菜单：菜单关闭时项不可见。
	if tt.HasText("删除") {
		t.Fatalf("删除项应在「更多」菜单里: %v", tt.Texts())
	}
	if err := tt.Click("更多"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// 菜单语义（2026-11-07 组件升级）：网关停止时菜单项仍显示（框架置灰），
	// 与旧「展开行隐藏按钮」不同。
	for _, item := range []string{"复制启动命令", "打开终端", "删除"} {
		if !tt.HasText(item) {
			t.Fatalf("菜单缺少 %q: %v", item, tt.Texts())
		}
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("删除接入配置") {
		t.Fatalf("确认弹窗未出现: %v", tt.Texts())
	}
	if got := acc.List("codex"); len(got) != 1 {
		t.Fatalf("武装不应删除: %+v", got)
	}
	// 取消：保留并收起。
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if got := acc.List("codex"); len(got) != 1 {
		t.Fatalf("取消后应保留: %+v", got)
	}
	// 再次武装并确认删除（菜单选中后即关闭，需重开）。
	if err := tt.Click("更多"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("删除接入配置") {
		t.Fatalf("弹窗未重现: %v", tt.Texts())
	}
	if err := tt.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	if got := acc.List("codex"); len(got) != 0 {
		t.Fatalf("确认后应删除: %+v", got)
	}
	if !strings.Contains(st.Access.Msg, "已删除") {
		t.Fatalf("结果提示缺失: %q", st.Access.Msg)
	}
}

// TestEditorBackDirtyGuard 覆盖编辑器独立子页的返回确认：有未保存修改时
// 返回只武装 NavArm（弹窗由壳层渲染），确认后才关闭并清空。编辑子页仅剩
// codex / claude_desktop 使用（claude 已改为右侧抽屉，2026-11-07）。
func TestEditorBackDirtyGuard(t *testing.T) {
	st, _ := uitest.New(t)
	st.Page = "access"
	st.Access.App = "codex"
	st.Access.Form = state.AccessForm{Open: true, Name: "draft", DocJSON: `{}`}
	st.Access.Form.Baseline = st.Access.Form.Signature("codex")
	st.Access.Form.Name = "draft-changed" // 未保存修改
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if !tt.HasText("← 返回接入") {
		t.Fatalf("editor subpage missing back: %v", tt.Texts())
	}
	if err := tt.Click("← 返回接入"); err != nil {
		t.Fatal(err)
	}
	if !st.NavArm || !st.Access.Form.Open {
		t.Fatalf("dirty back must arm confirm and keep editor: nav=%v form=%+v", st.NavArm, st.Access.Form)
	}
	// 模拟「继续编辑」：取消确认。
	st.NavArm = false
	if !st.Access.Form.Open || st.Access.Form.Name != "draft-changed" {
		t.Fatalf("continue editing must keep draft: %+v", st.Access.Form)
	}
	// 再次返回并确认：丢弃草稿回列表。
	if err := tt.Click("← 返回接入"); err != nil {
		t.Fatal(err)
	}
	st.Navigate(st.NavTo, nil)
	if st.Access.Form.Open {
		t.Fatalf("confirm must close editor: %+v", st.Access.Form)
	}
	tt.Frame()
	if !tt.HasText("Claude Code") || !tt.HasText("连接指南") {
		t.Fatalf("应回到带页签的列表: %v", tt.Texts())
	}
	// 干净状态点页签直接切换（框架 Tabs）。
	st.Access.App = "claude"
	tt2 := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if err := tt2.Click("Codex"); err != nil {
		t.Fatal(err)
	}
	if st.Access.App != "codex" {
		t.Fatalf("干净状态应直接切换: %q", st.Access.App)
	}
}

// TestRestoreBackupConfirm：点「还原最近备份」弹确认；取消收起；无备份时
// 确认后给出中文失败提示（HOME 指向临时目录，不触碰真实用户文件）。
func TestRestoreBackupConfirm(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, _ := uitest.New(t)
	// 列表卡还原入口走 codex（claude 的入口在接入抽屉，2026-11-07）。
	st.Access.App = "codex"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 800)
	if err := tt.Click("还原最近备份"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("覆盖当前 Agent 配置文件") {
		t.Fatalf("确认弹窗未出现: %v", tt.Texts())
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if st.Access.RestoreArm {
		t.Fatal("取消应收起弹窗")
	}
	if err := tt.Click("还原最近备份"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("还原"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Access.Msg, "还原失败") {
		t.Fatalf("无备份应报错: %q", st.Access.Msg)
	}
}

func TestAccessRowShowsNeedsApplyBadge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a, Home: home}
	v, err := acc.Create("claude", "工作", "lm1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"old":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	if err := acc.Update(v.ID, "工作", "lm1", `{"env":{"CUSTOM":"x"}}`); err != nil {
		t.Fatal(err)
	}
	// 2026-11-07：claude 列表行已移除，「待应用」徽章出现在摘要卡。
	st.Page = "access"
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 900)
	if !tt.HasText("待应用") {
		t.Fatalf("应显示待应用徽章：%v", tt.Texts())
	}
}

func TestAccessBackupPageListsAndConfirms(t *testing.T) {
	st, _ := uitest.New(t)
	st.Access.App = "claude"
	st.Access.BackupOpen = true
	st.Access.Backups = []bridge.BackupInfo{{Timestamp: 1793253937554, Files: []string{"settings.json"}, SizeBytes: 2048}}
	tt := ui.NewTester(func(c *ui.Context) { backupPage(c, st, configs(st)) }, 1000, 800)
	if !tt.HasText("备份与恢复") || !tt.HasText("settings.json") {
		t.Fatalf("backup page = %v", tt.Texts())
	}
	if !tt.HasText("还原") {
		t.Fatal("restore button missing")
	}
}

func TestAccessEmptyStateWithoutModelsOffersLogical(t *testing.T) {
	st, _ := uitest.New(t)
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 900)
	if !tt.HasText("去逻辑模型") {
		t.Fatalf("logical action missing: %v", tt.Texts())
	}
}

// TestGlobalConfigCardCodexOnly（2026-11-07 用户决策）：Claude 全局配置下线，
// claude 页签不再渲染全局配置卡；codex 页签保留编辑与保存入口。
func TestGlobalConfigCardCodexOnly(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Page = "access"
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if tt.HasText("全局配置") {
		t.Fatalf("claude 页签不应再渲染全局配置卡: %v", tt.Texts())
	}
	st.Access.App = "codex"
	tt = ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	for _, want := range []string{"全局配置", "保存全局配置"} {
		if !tt.HasText(want) {
			t.Fatalf("codex 页签缺少 %q: %v", want, tt.Texts())
		}
	}
}

// TestPageHeaderAgentAccess（2026-11-07 原型对齐 + 用户裁定修订）：页头标题
// 「Agent 接入」、claude 副标题点明同页管理设置；「刷新/保存设置」是 Claude
// 设置区专属动作，渲染在设置区区块头而非全页签共享的页头——codex 页签不得
// 出现「保存设置」。
func TestPageHeaderAgentAccess(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Page = "access"
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	for _, want := range []string{"Agent 接入", "并在同一页管理它的设置", "打开设置页"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	for _, unwanted := range []string{"保存设置", "用户级"} {
		if tt.HasText(unwanted) {
			t.Fatalf("claude 页签不应再含设置区 %q: %v", unwanted, tt.Texts())
		}
	}
	st.Access.App = "codex"
	tt = ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if !tt.HasText("为每个 Agent 管理网关接入与实际配置") {
		t.Fatalf("codex 副标题缺失: %v", tt.Texts())
	}
	if tt.HasText("保存设置") {
		t.Fatalf("保存设置是 Claude 设置区专属动作，codex 页签不应出现: %v", tt.Texts())
	}
}

// TestSummaryCardMetaRow（2026-11-07 原型对齐）：摘要卡 meta 行含网关状态、
// 配置名、模型与 1M 徽章；网关停止时状态徽章为「网关已停止」。
func TestSummaryCardMetaRow(t *testing.T) {
	st, a := uitest.New(t)
	home := t.TempDir()
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a, Home: home}
	if _, err := acc.Create("claude", "工作", "lm1", `{"env":{"ANTHROPIC_MODEL":"my-code[1M]"}}`); err != nil {
		t.Fatal(err)
	}
	v := acc.List("claude")[0]
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	st.Page = "access"
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	for _, want := range []string{"网关已停止", "配置 工作", "模型 my-code", "1M 上下文"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
}

// TestClaudeEditorOpensAsDrawer（2026-11-07 原型对齐）：Claude 页签的编辑
// 接入是右侧抽屉——主页面（摘要卡/设置区）不被替换，抽屉标题「接入配置」；
// 点关闭后抽屉收起且草稿标记保留。
func TestClaudeEditorOpensAsDrawer(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Page = "access"
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	for _, want := range []string{"Claude Code 接入", "Claude Code 设置", "接入配置", "名称:", "保存并应用"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
	if tt.HasText("← 返回接入") {
		t.Fatalf("claude 不应再走编辑子页: %v", tt.Texts())
	}
	// Esc 关闭（页面级有多处同名「关闭」文本——三态分段段名——不能用文本点击；
	// Esc 路径同时覆盖宿主的 open 写回与草稿标记）。
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if st.Access.Form.Open {
		t.Fatal("close must close the drawer")
	}
	if !st.Access.Form.Draft {
		t.Fatal("close must keep the draft flag")
	}
}

// TestClaudeDrawerHooksPreload：抽屉切换回调走宿主 preloadForm——EditID 换为
// 目标配置并回填名称/文档；NewDraft 清空 EditID。
func TestClaudeDrawerHooksPreload(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	acc := configs(st)
	if _, err := acc.Create("claude", "工作", "lm1", "{}"); err != nil {
		t.Fatal(err)
	}
	v2, err := acc.Create("claude", "备用", "lm1", "{}")
	if err != nil {
		t.Fatal(err)
	}
	h := claudeDrawerHooks(st, acc)
	h.SwitchTo(v2.ID)
	if st.Access.Form.EditID != v2.ID || st.Access.Form.Name != "备用" || !st.Access.Form.Open {
		t.Fatalf("SwitchTo must preload the target config: %+v", st.Access.Form)
	}
	h.NewDraft()
	if st.Access.Form.EditID != "" || st.Access.Form.Name != "" || !st.Access.Form.Open {
		t.Fatalf("NewDraft must reset to a fresh draft: %+v", st.Access.Form)
	}
}

// TestClaudeTabDropsConfigListCard（2026-11-07 原型对齐）：claude 页签结构为
// 摘要卡 → 设置区，不再渲染「实际配置」列表卡（配置管理在抽屉内）；
// codex 页签保留列表卡。设置区受管键行提供「打开接入」入口。
func TestClaudeTabDropsConfigListCard(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Page = "access"
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	// 「备份与恢复」在设置区仍存在（claudecfg 备份面板），不在排除之列。
	for _, unwanted := range []string{"实际配置", "＋ 新建配置"} {
		if tt.HasText(unwanted) {
			t.Fatalf("claude 页签不应再渲染 %q: %v", unwanted, tt.Texts())
		}
	}
	st.Access.App = "codex"
	tt = ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if !tt.HasText("实际配置") || !tt.HasText("＋ 新建配置") {
		t.Fatalf("codex 页签应保留列表卡: %v", tt.Texts())
	}
}

// TestClaudeTabShowsAccessMessages（终审 Critical#1）：claude 页签必须渲染
// st.Access.Msg——应用失败/应用阻断提示/删除与还原结果此前随列表卡移除而
// 丢失，主操作失败时用户得不到任何解释。
func TestClaudeTabShowsAccessMessages(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Page = "access"
	st.Access.App = "claude"
	st.Access.Msg = "应用失败：示例错误"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if !tt.HasText("应用失败：示例错误") {
		t.Fatalf("st.Access.Msg 在 claude 页签不可见: %v", tt.Texts())
	}
}

// TestReenterKeepsDraft（终审 Important#3）：草稿保留后点「编辑接入」/「打开
// 接入」不得静默丢弃草稿——同目标直接恢复草稿；不同目标武装切换确认，
// 确认后才切换并加载。
func TestReenterKeepsDraft(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	acc := configs(st)
	v1, err := acc.Create("claude", "工作", "lm1", "{}")
	if err != nil {
		t.Fatal(err)
	}
	v2, err := acc.Create("claude", "备用", "lm1", "{}")
	if err != nil {
		t.Fatal(err)
	}
	// 草稿：编辑 v1 有未保存修改，抽屉已关闭（Draft 置位）。
	st.Access.Form = state.AccessForm{Open: false, Draft: true, EditID: v1.ID, Name: "工作改名"}
	st.Access.Form.Baseline = st.Access.Form.Signature("claude") + "-old"

	// 同目标：恢复草稿，不清表单。
	openAccessEditor(st, acc, v1.ID)
	if !st.Access.Form.Open || st.Access.Form.Name != "工作改名" {
		t.Fatalf("同目标重进必须恢复草稿: %+v", st.Access.Form)
	}

	// 不同目标：武装切换确认，确认前不清草稿。
	st.Access.Form.Open = false
	openAccessEditor(st, acc, v2.ID)
	if !st.Access.DrawerSwitchArm || st.Access.DrawerSwitchID != v2.ID {
		t.Fatalf("不同目标必须武装切换确认: arm=%v id=%q", st.Access.DrawerSwitchArm, st.Access.DrawerSwitchID)
	}
	if st.Access.Form.Name != "工作改名" {
		t.Fatal("确认前不得清草稿")
	}

	// 确认后切换并加载目标配置。
	claude.ConfirmDrawerSwitch(st, claudeDrawerHooks(st, acc))
	if st.Access.DrawerSwitchArm || st.Access.Form.EditID != v2.ID || st.Access.Form.Name != "备用" {
		t.Fatalf("确认后应切换并加载目标: %+v", st.Access.Form)
	}
}

// TestCodexConfigFilterSearchField（2026-11-07 组件升级）：codex 配置过滤框
// 是 ui.SearchField——聚焦输入绑定 + Esc 清空（TextInput 无此行为，为控件
// 级判别）；过滤语义不变（无匹配显示「没有匹配的配置」）。搜索框仅在
// 配置 > 8 个时渲染，测试造 9 个。
func TestCodexConfigFilterSearchField(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := configs(st)
	for i := 0; i < 9; i++ {
		if _, err := acc.Create("codex", fmt.Sprintf("配置%d", i), "lm1", "{}"); err != nil {
			t.Fatal(err)
		}
	}
	st.Page = "access"
	st.Access.App = "codex"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 900)
	// 控件级判别：SearchField 的输入件带读屏 search 态（TextInput 无）；
	// codex 页签全局配置行为空行时它是唯一文本输入件。
	field, ok := tt.AccessByRole(ui.RoleTextField, "")
	if !ok {
		t.Fatal("codex 过滤输入件缺失")
	}
	if !field.Search {
		t.Fatal("过滤框应为 ui.SearchField（读屏 search 态缺失）")
	}
	// 过滤语义不变：无匹配提示。
	st.Access.Filter = "不存在"
	tt = ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 900)
	if !tt.HasText("没有匹配的配置") {
		t.Fatalf("无匹配提示缺失: %v", tt.Texts())
	}
}

// TestClaudeTabSlimmedAfterSplit（2026-11-07 拆页）：claude 页签不再渲染设置区
// 主体（用户级/视图切换等），摘要卡提供「打开设置页」跨页入口；ClaudeCfg
// 脏不再阻断接入页页签切换（脏守卫由设置页承担）。
func TestClaudeTabSlimmedAfterSplit(t *testing.T) {
	st, a := uitest.New(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	st.Page = "access"
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if !tt.HasText("Claude Code 接入") || !tt.HasText("打开设置页") {
		t.Fatalf("摘要卡应提供设置页入口: %v", tt.Texts())
	}
	for _, unwanted := range []string{"用户级", "项目级", "企业托管", "合并预览", "模型与响应"} {
		if tt.HasText(unwanted) {
			t.Fatalf("claude 页签不应再含设置区 %q: %v", unwanted, tt.Texts())
		}
	}
	// 页签切换脏守卫不再计入 ClaudeCfg（设置页离开时由侧栏守卫确认）。
	st.ClaudeCfg.Dirty = true
	if tabDirty(st) {
		t.Fatal("ClaudeCfg 脏不应阻断接入页签切换")
	}
}
