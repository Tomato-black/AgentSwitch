package claudecfg

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/desktop/uitest"
)

func TestSectionRendersClaudeSettings(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	st.ClaudeCfg.Home = home
	tt := ui.NewTester(func(c *ui.Context) {
		Page(c, st)
	}, 1000, 1600)
	for _, want := range []string{
		"Claude Code 设置", "受管 13 键", "用户级", "项目级", "企业托管",
		"表单", "JSON", "合并预览", "推理与响应", "权限", "Skills",
		"危险模式确认",
		// 2026-11-07 用户裁定：刷新/保存设置是设置区（Claude 专属）动作，
		// 收回区块头渲染，不进全页签共享的页头。
		"刷新", "保存设置",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

// TestLoadNumericJSONNotDirty 回归（C1）：cloneDoc 曾走 json.Marshal 往返，
// json.Number 变 float64 后与 Baseline 恒不等，含数字的文件零编辑即脏。
func TestLoadNumericJSONNotDirty(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	st.ClaudeCfg.Home = home
	file := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"cleanupPeriodDays":30,"statusLine":{"padding":0}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: home}
	load(st, cfgs)
	cf := &st.ClaudeCfg
	if !cf.Loaded {
		t.Fatalf("load failed: %s", cf.Msg)
	}
	if cf.Dirty {
		t.Fatal("file containing numbers must load clean without user edits")
	}
	// 编辑一个无关字符串键后，Diff 必须只列出该键（数字键不得串脏）。
	setString(cf.Doc, "model", "opus")
	RecomputeDirty(cf)
	if !cf.Dirty {
		t.Fatal("string edit must dirty the document")
	}
	changed := corecfg.Diff(cf.Baseline, cf.Doc)
	if len(changed) != 1 || changed[0] != "model" {
		t.Fatalf("diff = %v, want [model]", changed)
	}
}

// TestCloneDocPreservesJSONNumber：深拷贝不得把 json.Number 变 float64。
func TestCloneDocPreservesJSONNumber(t *testing.T) {
	src := map[string]any{
		"cleanupPeriodDays": mustJSONNumber(t, "30"),
		"statusLine":        map[string]any{"padding": mustJSONNumber(t, "0")},
		"nested":            []any{mustJSONNumber(t, "1.50")},
	}
	got := cloneDoc(src)
	if _, ok := got["cleanupPeriodDays"].(json.Number); !ok {
		t.Fatalf("top-level number flattened: %#v", got["cleanupPeriodDays"])
	}
	obj, _ := got["statusLine"].(map[string]any)
	if _, ok := obj["padding"].(json.Number); !ok {
		t.Fatalf("nested number flattened: %#v", obj)
	}
	arr, _ := got["nested"].([]any)
	if _, ok := arr[0].(json.Number); !ok {
		t.Fatalf("slice number flattened: %#v", arr)
	}
	// 拷贝必须独立于源文档。
	src["statusLine"].(map[string]any)["padding"] = mustJSONNumber(t, "9")
	if obj["padding"].(json.Number).String() != "0" {
		t.Fatal("clone must not alias source maps")
	}
}

func mustJSONNumber(t *testing.T, s string) json.Number {
	t.Helper()
	return json.Number(s)
}

// TestPrettyPreservesJSONNumberLiterals：JSON 视图的 pretty() 序列化必须原样
// 输出 json.Number 字面量（含超出 float64 精度的大整数），不得走 float 往返。
func TestPrettyPreservesJSONNumberLiterals(t *testing.T) {
	doc := map[string]any{
		"cleanupPeriodDays": mustJSONNumber(t, "30"),
		"big":               mustJSONNumber(t, "12345678901234567890"),
	}
	want := "{\n  \"big\": 12345678901234567890,\n  \"cleanupPeriodDays\": 30\n}"
	if got := pretty(doc); got != want {
		t.Fatalf("pretty flattened numbers: %s", got)
	}
}

// TestRefreshGuardsDirtyEdits 回归（I1）：有未保存修改时点「刷新」不得静默
// 丢弃——先弹「放弃未保存的修改？」，继续编辑为默认焦点；确认放弃才重载。
func TestRefreshGuardsDirtyEdits(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	st.ClaudeCfg.Home = home
	file := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: home}
	load(st, cfgs)
	setString(st.ClaudeCfg.Doc, "model", "opus")
	RecomputeDirty(&st.ClaudeCfg)

	tt := ui.NewTester(func(c *ui.Context) {
		Page(c, st)
	}, 1000, 1600)
	if err := tt.Click("刷新"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !st.ClaudeCfg.ReloadArm {
		t.Fatal("dirty refresh must arm the confirm dialog")
	}
	for _, want := range []string{"放弃未保存的修改？", "继续编辑", "放弃修改"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in reload dialog: %v", want, tt.Texts())
		}
	}
	if !tt.Focused("继续编辑") {
		t.Fatal("继续编辑 must be the default-focused (safe) action")
	}

	// 磁盘文件被外部改写；确认放弃后应重载为磁盘内容。
	if err := os.WriteFile(file, []byte(`{"model":"haiku"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("放弃修改"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st.ClaudeCfg.ReloadArm {
		t.Fatal("confirm must close the dialog")
	}
	if st.ClaudeCfg.Dirty {
		t.Fatal("reload after discard must be clean")
	}
	if got := stringAt(st.ClaudeCfg.Doc, "model"); got != "haiku" {
		t.Fatalf("reload must read disk content: %q", got)
	}
}

// TestRefreshCleanReloadsImmediately：干净时点「刷新」直接重载，不弹确认框。
func TestRefreshCleanReloadsImmediately(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	st.ClaudeCfg.Home = home
	file := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: home}
	load(st, cfgs)

	tt := ui.NewTester(func(c *ui.Context) {
		Page(c, st)
	}, 1000, 1600)
	if err := tt.Click("刷新"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st.ClaudeCfg.ReloadArm {
		t.Fatal("clean refresh must not arm the dialog")
	}
}

// TestBrokenJSONReadOnlyRepairState 回归（I5）：损坏文件进入只读修复态——
// JSON 视图显示磁盘原文与行列错误、表单替换为修复卡、保存被阻断；
// 「用空配置重建」经危险确认后以空对象覆盖并解除修复态。
func TestBrokenJSONReadOnlyRepairState(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	st.ClaudeCfg.Home = home
	raw := "{\n  \"model\": \"sonnet\",\n  BROKEN\n"
	file := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: home}
	load(st, cfgs)
	cf := &st.ClaudeCfg

	if !cf.Loaded || !cf.BrokenFile || cf.ParseErr == "" {
		t.Fatalf("broken file must enter repair state: loaded=%v broken=%v err=%q", cf.Loaded, cf.BrokenFile, cf.ParseErr)
	}
	if !strings.Contains(cf.ParseErr, "第 ") || !strings.Contains(cf.ParseErr, " 行第 ") {
		t.Fatalf("parse error must carry line/col: %q", cf.ParseErr)
	}
	if cf.JSONText != raw {
		t.Fatalf("JSON view must show raw disk text, got %q", cf.JSONText)
	}
	if cf.Dirty {
		t.Fatal("broken load must not be dirty")
	}
	if saveBlockReason(cf) == "" {
		t.Fatal("save must be blocked while the file is broken")
	}

	tt := ui.NewTester(func(c *ui.Context) {
		Page(c, st)
	}, 1000, 1600)
	for _, want := range []string{
		"文件不是合法 JSON，表单已停用", "备份与恢复", "用空配置重建", "JSON 错误",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in repair state: %v", want, tt.Texts())
		}
	}
	// 表单停用：修复卡替换七组表单。
	if tt.HasText("记忆与上下文") {
		t.Fatalf("form groups must be replaced by the repair card: %v", tt.Texts())
	}

	// 危险确认 → 空对象重建。
	if err := tt.Click("用空配置重建"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !cf.RebuildArm {
		t.Fatal("rebuild must arm the danger confirm")
	}
	if !tt.Focused("取消") {
		t.Fatal("cancel must stay the default-focused action")
	}
	if err := tt.Click("重建"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	doc, err := corecfg.ParseDoc(mustRead(t, file))
	if err != nil {
		t.Fatalf("rebuilt file must parse: %v", err)
	}
	if len(doc.Raw) != 0 {
		t.Fatalf("rebuilt file must be {}: %v", doc.Raw)
	}
	if cf.BrokenFile || cf.ParseErr != "" {
		t.Fatalf("repair state must clear after rebuild: broken=%v err=%q", cf.BrokenFile, cf.ParseErr)
	}
	if !cf.ResultVisible {
		t.Fatal("rebuild must show the result bar")
	}
}

// TestBrokenJSONRawTextSurvivesViewSwitch：修复态下切到 JSON 视图不得用
// pretty(空文档) 覆盖磁盘原文。
func TestBrokenJSONRawTextSurvivesViewSwitch(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	st.ClaudeCfg.Home = home
	raw := `{"model": "sonnet", OOPS}`
	file := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: home}
	load(st, cfgs)
	cf := &st.ClaudeCfg

	tt := ui.NewTester(func(c *ui.Context) {
		viewTabs(c, c.Theme(), uikit.Softs(c.Theme()), st, cfgs)
		jsonView(c, c.Theme(), st)
	}, 1000, 1600)
	if err := tt.Click("JSON"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if cf.View != "json" || cf.JSONText != raw {
		t.Fatalf("raw text must survive view switch: view=%q text=%q", cf.View, cf.JSONText)
	}
}

// TestFileStripStatusBadges（2026-11-07 原型对齐）：文件条状态徽章随文档状态
// 切换——干净已存在文件显示「已保存」，变脏显示「有未保存修改」，文件不
// 存在显示「文件不存在」。
func TestFileStripStatusBadges(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	st.ClaudeCfg.Home = home
	file := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: home}
	load(st, cfgs)
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 1600)
	if !tt.HasText("已保存") {
		t.Fatalf("clean file must show 已保存: %v", tt.Texts())
	}
	setString(st.ClaudeCfg.Doc, "model", "opus")
	RecomputeDirty(&st.ClaudeCfg)
	tt = ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 1600)
	if !tt.HasText("有未保存修改") {
		t.Fatalf("dirty doc must show 有未保存修改: %v", tt.Texts())
	}
}

// TestLayerContextCards（2026-11-07 原型对齐）：项目层上下文卡含共享/本地
// 分段与 mono 实际路径条；托管层含托管文件路径与只读说明。
func TestLayerContextCards(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	st.ClaudeCfg.Home = home
	proj := filepath.Join(home, "work", "demo")
	if err := os.MkdirAll(filepath.Join(proj, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(proj, ".claude", "settings.json")
	if err := os.WriteFile(shared, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st.ClaudeCfg.Layer = "project"
	st.ClaudeCfg.Sub = "shared"
	st.ClaudeCfg.Project = proj
	st.ClaudeCfg.Projects = []bridge.ClaudeProject{{Path: proj}}
	st.ClaudeCfg.Loaded = false
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 1600)
	for _, want := range []string{"共享 settings.json", "本地 settings.local.json", "settings.json"} {
		if !tt.HasText(want) {
			t.Fatalf("project context missing %q: %v", want, tt.Texts())
		}
	}
	st.ClaudeCfg.Layer = "managed"
	st.ClaudeCfg.Loaded = false
	tt = ui.NewTester(func(c *ui.Context) { Page(c, st) }, 1000, 1600)
	if !tt.HasText("托管配置只读") || !tt.HasText("managed-settings.json") {
		t.Fatalf("managed context missing source path/readonly note: %v", tt.Texts())
	}
}

// TestPageRendersClaudeSettings（2026-11-07 拆页决策）：Claude Code 设置独立页
// ——页头（标题/副标题/受管徽章/刷新/保存设置）+ 滚动主体（文件条/层级/视图
// 切换/七卡内容）。
func TestPageRendersClaudeSettings(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	st.ClaudeCfg.Home = home
	tt := ui.NewTester(func(c *ui.Context) {
		Page(c, st)
	}, 1000, 1600)
	for _, want := range []string{
		"Claude Code 设置", "分层管理 Claude Code 自身设置；接入投影键只读", "受管 13 键",
		"刷新", "保存设置", "用户级", "项目级", "企业托管", "表单", "JSON", "合并预览",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}
