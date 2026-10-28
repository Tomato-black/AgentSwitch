package access

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uitest"
	"AgentSwitch/internal/storage"
)

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

// TestAccessCodexEditorShowsModelMetaPreview：模型列表与高级设置之间渲染
// 只读「将写入 Codex 的模型」元数据预览；Claude 编辑器不出现该区块。
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
	for _, want := range []string{"将写入 Codex 的模型", "My Code", "1000000", "low/high"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true, ModelLabel: "my-code", DocJSON: `{"env":{}}`}
	tt = ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if tt.HasText("将写入 Codex 的模型") {
		t.Fatal("Claude 编辑器不应出现 Codex 模型预览")
	}
}

// TestAccessGlobalSaveRefreshesCodexPreview：Codex 编辑器打开时保存全局配置，
// 除按新基线重预览 JSON 文档外，也要刷新只读 config.toml 预览（否则预览会
// 停留在保存前的模型解析结果）。
func TestAccessGlobalSaveRefreshesCodexPreview(t *testing.T) {
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
	st.Access.Form = state.AccessForm{
		Open: true, ModelLabel: "my-code", CodexModels: []string{"my-code"},
		DocJSON: `{"models":["my-code"],"env":{}}`,
	}
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1200, 4000)
	if err := tt.Click("保存全局配置"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Access.Form.CodexPreview, "model_catalog_json") {
		t.Fatalf("保存全局配置后应刷新 config.toml 预览: %q msg=%q",
			st.Access.Form.CodexPreview, st.Access.Form.Msg)
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
	if _, err := acc.Create("claude", "cfg", "lm1", "{}"); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 800)
	// 次级操作收纳在「更多」：删除按钮展开后才可见。
	if tt.HasText("删除") {
		t.Fatalf("删除按钮应在「更多」里: %v", tt.Texts())
	}
	if err := tt.Click("更多"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("删除接入配置") {
		t.Fatalf("确认弹窗未出现: %v", tt.Texts())
	}
	if got := acc.List("claude"); len(got) != 1 {
		t.Fatalf("武装不应删除: %+v", got)
	}
	// 取消：保留并收起。
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if got := acc.List("claude"); len(got) != 1 {
		t.Fatalf("取消后应保留: %+v", got)
	}
	// 再次武装并确认删除（「更多」仍处于展开态）。
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("删除接入配置") {
		t.Fatalf("弹窗未重现: %v", tt.Texts())
	}
	if err := tt.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	if got := acc.List("claude"); len(got) != 0 {
		t.Fatalf("确认后应删除: %+v", got)
	}
	if !strings.Contains(st.Access.Msg, "已删除") {
		t.Fatalf("结果提示缺失: %q", st.Access.Msg)
	}
}

// TestTabSwitchDirtyGuard 覆盖页签切换脏检查：有未保存编辑时先弹确认，
// 「继续编辑」留在原页签、「放弃并切换」才切换并清空草稿；干净时直接切换。
func TestTabSwitchDirtyGuard(t *testing.T) {
	st, _ := uitest.New(t)
	st.Access.App = "claude"
	st.Access.Form = state.AccessForm{Open: true}
	st.Access.Form.Baseline = st.Access.Form.Signature("claude")
	st.Access.Form.Name = "draft" // 未保存修改
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if err := tt.Click("Codex"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("未保存的编辑") {
		t.Fatalf("确认弹窗未出现: %v", tt.Texts())
	}
	if st.Access.App != "claude" {
		t.Fatalf("确认前不应切换: %q", st.Access.App)
	}
	if err := tt.Click("继续编辑"); err != nil {
		t.Fatal(err)
	}
	if st.Access.App != "claude" || st.Access.Form.Name != "draft" {
		t.Fatalf("继续编辑应保留草稿: %q %q", st.Access.App, st.Access.Form.Name)
	}
	// 再次切换并放弃：切换成功且草稿清空。
	if err := tt.Click("Codex"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("放弃并切换"); err != nil {
		t.Fatal(err)
	}
	if st.Access.App != "codex" || st.Access.Form.Open {
		t.Fatalf("放弃后应切换并清空: %q %+v", st.Access.App, st.Access.Form)
	}
	// 干净状态下直接切换。
	st.Access.App = "claude"
	tt2 := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 900)
	if err := tt2.Click("连接指南"); err != nil {
		t.Fatal(err)
	}
	if st.Access.App != "guide" {
		t.Fatalf("干净状态应直接切换: %q", st.Access.App)
	}
}

// TestRestoreBackupConfirm：点「还原最近备份」弹确认；取消收起；无备份时
// 确认后给出中文失败提示（HOME 指向临时目录，不触碰真实用户文件）。
func TestRestoreBackupConfirm(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	st, _ := uitest.New(t)
	st.Access.App = "claude"
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
