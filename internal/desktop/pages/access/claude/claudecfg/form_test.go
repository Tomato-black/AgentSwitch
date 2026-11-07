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
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/desktop/uitest"
)

func formTestState(t *testing.T) *state.State {
	t.Helper()
	st, _ := uitest.New(t)
	cf := &st.ClaudeCfg
	cf.Loaded = true
	cf.Layer = "user"
	cf.Sub = "shared"
	cf.Doc = map[string]any{}
	cf.Baseline = map[string]any{}
	return st
}

func renderForm(st *state.State, width, height int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		FormSection(c, st)
	}, width, height)
}

func TestFormSectionRendersSevenGroups(t *testing.T) {
	st := formTestState(t)
	st.ClaudeCfg.Doc = map[string]any{
		"model":                 "opus",
		"fallbackModel":         []any{"sonnet"},
		"effortLevel":           "high",
		"alwaysThinkingEnabled": true,
		"env": map[string]any{
			"ANTHROPIC_BASE_URL":         "http://127.0.0.1:17890",
			"ANTHROPIC_AUTH_TOKEN":       "secret-token-value",
			"ANTHROPIC_MODEL":            "my-code",
			"ANTHROPIC_SMALL_FAST_MODEL": "my-code",
			"MY_FLAG":                    "1",
		},
		"permissions": map[string]any{
			"defaultMode": "default",
			"allow":       []any{"Bash(go test:*)"},
		},
		"statusLine": map[string]any{
			"type":    "command",
			"command": "~/.claude/statusline.sh",
			"padding": json.Number("0"),
		},
		"enableAllProjectMcpServers": true,
		"enabledMcpjsonServers":      []any{"fs"},
		"enabledPlugins":             map[string]any{"foo@market": true},
		"skillOverrides":             map[string]any{"claude-api": "off"},
		"autoCompactEnabled":         true,
		"autoMemoryEnabled":          false,
		"cleanupPeriodDays":          json.Number("45"),
	}
	st.ClaudeCfg.Baseline = cloneDoc(st.ClaudeCfg.Doc)

	tt := renderForm(st, 1200, 4000)
	for _, want := range []string{
		"模型与响应", "环境变量", "权限", "状态栏", "MCP 与插件", "Skills", "记忆与上下文",
		"ANTHROPIC_BASE_URL", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_MODEL",
		"由 AgentSwitch 接入管理", "危险模式确认", "启用全部项目 MCP 服务器",
		"foo@market", "清理周期",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	// 接入投影键默认折叠，只常显核心三键。
	if tt.HasText("ANTHROPIC_SMALL_FAST_MODEL") {
		t.Fatalf("projected managed keys must stay collapsed: %v", tt.Texts())
	}
	// 敏感值遮蔽。
	if !tt.HasText("••") {
		t.Fatalf("managed secret must be masked, got %v", tt.Texts())
	}
	if !tt.HasText("类型") || !tt.HasText("命令") || !tt.HasText("内边距") {
		t.Fatalf("statusLine subfields must render: %v", tt.Texts())
	}
}

func TestFormSectionExpandsProjectedManagedEnv(t *testing.T) {
	st := formTestState(t)
	st.ClaudeCfg.EnvExpanded = true
	tt := renderForm(st, 1200, 5000)
	if !tt.HasText("ANTHROPIC_SMALL_FAST_MODEL") {
		t.Fatalf("expanded managed keys missing: %v", tt.Texts())
	}
	if !tt.HasText("收起接入投影键") {
		t.Fatalf("collapse action missing: %v", tt.Texts())
	}
}

func TestFormSectionProjectSharedDisablesDangerMode(t *testing.T) {
	st := formTestState(t)
	st.ClaudeCfg.Layer = "project"
	st.ClaudeCfg.Sub = "shared"
	tt := renderForm(st, 1200, 4000)
	if !tt.HasText("项目共享不能设置危险模式确认") {
		t.Fatalf("project-shared danger mode must be disabled with reason: %v", tt.Texts())
	}
}

func TestFormInheritedKeyShowsSourceAndOverrideDoesNotCopy(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.MergeEntries = map[string]corecfg.MergedEntry{
		"model": {Path: "model", Value: "opus", Source: corecfg.ScopeManaged},
	}
	tt := renderForm(st, 1200, 4000)
	if !tt.HasText("继承自下层") || !tt.HasText("来源：企业托管") {
		t.Fatalf("inherited source missing: %v", tt.Texts())
	}
	if !tt.HasText("opus") {
		t.Fatalf("inherited value missing: %v", tt.Texts())
	}
	if err := tt.Click("覆盖"); err != nil {
		t.Fatalf("click override: %v", err)
	}
	tt.Frame()
	if v, ok := cf.Doc["model"]; ok {
		t.Fatalf("override must not write inherited value, got %v", v)
	}
	if cf.Dirty {
		t.Fatal("override alone must not dirty the document")
	}
	if !cf.Overrides["model"] {
		t.Fatalf("override marker missing: %v", cf.Overrides)
	}
}

func TestDangerModeTriStateWritesDoc(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg

	setDangerChoice(cf, dangerSkip)
	if v, _ := cf.Doc["skipDangerousModePermissionPrompt"].(bool); !v {
		t.Fatalf("skip choice must write true: %v", cf.Doc)
	}
	if !cf.Dirty {
		t.Fatal("skip choice must dirty document")
	}

	setDangerChoice(cf, dangerShow)
	if v, _ := cf.Doc["skipDangerousModePermissionPrompt"].(bool); v {
		t.Fatalf("show choice must write false: %v", cf.Doc)
	}

	setDangerChoice(cf, dangerInherit)
	if _, ok := cf.Doc["skipDangerousModePermissionPrompt"]; ok {
		t.Fatalf("inherit choice must delete key: %v", cf.Doc)
	}
	if cf.Dirty {
		t.Fatal("deleting back to baseline must clear dirty")
	}
}

func TestStatusLineObjectEdits(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg

	setStatusLineField(cf, "type", "command")
	setStatusLineField(cf, "command", "~/.claude/statusline.sh")
	obj, ok := cf.Doc["statusLine"].(map[string]any)
	if !ok {
		t.Fatalf("statusLine object missing: %v", cf.Doc)
	}
	if obj["type"] != "command" || obj["command"] != "~/.claude/statusline.sh" {
		t.Fatalf("statusLine fields = %v", obj)
	}
	if !cf.Dirty {
		t.Fatal("statusLine edit must dirty document")
	}

	setStatusLineField(cf, "command", "")
	obj = cf.Doc["statusLine"].(map[string]any)
	if _, ok := obj["command"]; ok {
		t.Fatalf("empty field must delete: %v", obj)
	}

	setStatusLineField(cf, "type", "")
	if _, ok := cf.Doc["statusLine"]; ok {
		t.Fatalf("empty statusLine must be removed: %v", cf.Doc)
	}
}

func TestMemoryIntegerField(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg

	setIntField(cf, "cleanupPeriodDays", "60")
	if got, _ := cf.Doc["cleanupPeriodDays"].(int); got != 60 {
		t.Fatalf("cleanupPeriodDays = %v", cf.Doc["cleanupPeriodDays"])
	}
	if !cf.Dirty {
		t.Fatal("memory edit must dirty document")
	}

	setIntField(cf, "cleanupPeriodDays", "abc")
	if cf.FormMsg == "" {
		t.Fatal("invalid integer must set form message")
	}
	if got, _ := cf.Doc["cleanupPeriodDays"].(int); got != 60 {
		t.Fatalf("invalid integer must not change doc: %v", cf.Doc["cleanupPeriodDays"])
	}

	setIntField(cf, "cleanupPeriodDays", "")
	if _, ok := cf.Doc["cleanupPeriodDays"]; ok {
		t.Fatalf("empty integer must delete key: %v", cf.Doc)
	}
	if cf.Dirty {
		t.Fatal("deleting back to baseline must clear dirty")
	}
}

func TestListFieldWritesStringArray(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg

	setListField(cf, "permissions.allow", "Bash(go test:*), Read, ")
	got, _ := corecfg.Get(cf.Doc, "permissions.allow")
	arr, ok := got.([]any)
	if !ok || len(arr) != 2 || arr[0] != "Bash(go test:*)" || arr[1] != "Read" {
		t.Fatalf("permissions.allow = %#v", got)
	}

	setListField(cf, "permissions.allow", "")
	if _, ok := corecfg.Get(cf.Doc, "permissions.allow"); ok {
		t.Fatalf("empty list must delete key: %v", cf.Doc)
	}
}

func TestPluginToggleWritesObjectBool(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg

	setPluginEnabled(cf, "bar@market", true)
	setPluginEnabled(cf, "foo@market", false)
	m, ok := cf.Doc["enabledPlugins"].(map[string]any)
	if !ok {
		t.Fatalf("enabledPlugins missing: %v", cf.Doc)
	}
	if m["bar@market"] != true || m["foo@market"] != false {
		t.Fatalf("enabledPlugins = %v", m)
	}
}

// TestFieldBlockedByStrongManagedTopLevel 回归（I4）：接入投影的强受管顶层键
// （含子路径）在表单中只读，理由指明去接入页修改。
func TestFieldBlockedByStrongManagedTopLevel(t *testing.T) {
	st := formTestState(t)
	st.ClaudeCfg.Managed = corecfg.ManagedSet{StrongTopLevel: []string{"statusLine", "permissions"}}
	for _, path := range []string{"statusLine", "statusLine.command", "permissions", "permissions.allow"} {
		blocked, reason := fieldBlocked(&st.ClaudeCfg, path)
		if !blocked {
			t.Fatalf("%s must be blocked by managed projection", path)
		}
		if !strings.Contains(reason, "接入投影") || !strings.Contains(reason, "接入") {
			t.Fatalf("reason must point to the access page: %q", reason)
		}
	}
	// 非受管键不受影响。
	if blocked, _ := fieldBlocked(&st.ClaudeCfg, "model"); blocked {
		t.Fatal("unrelated key must stay editable")
	}

	// 渲染层面：状态栏组显示锁定理由，不出现输入框。
	st.ClaudeCfg.Doc = map[string]any{"model": "opus"}
	tt := renderForm(st, 1200, 3000)
	if !tt.HasText("接入投影") {
		t.Fatalf("blocked reason must render: %v", tt.Texts())
	}
	if tt.HasText("静态文本") {
		t.Fatalf("statusLine inputs must not render for managed keys: %v", tt.Texts())
	}
}

// TestCustomEnvRowShowsWeakManagedBadge 回归（I4）：弱受管 env 键的自定义行
// 显示「接入应用可能覆盖」警告徽章（仅该行，非弱受管行不带）。
func TestCustomEnvRowShowsWeakManagedBadge(t *testing.T) {
	st := formTestState(t)
	cf := &st.ClaudeCfg
	cf.Doc = map[string]any{"env": map[string]any{"HTTPS_PROXY": "http://proxy", "FREE_KEY": "x"}}
	cf.Baseline = cloneDoc(cf.Doc)
	cf.Managed = corecfg.ManagedSet{WeakEnv: []string{"env.HTTPS_PROXY"}}
	tt := renderForm(st, 1200, 3000)
	if !tt.HasText("接入应用可能覆盖") {
		t.Fatalf("weak-managed warning badge missing: %v", tt.Texts())
	}
	count := 0
	for _, text := range tt.Texts() {
		if text == "接入应用可能覆盖" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("badge must appear exactly once (only the weak row): %d in %v", count, tt.Texts())
	}
}

// TestBoolFieldTriStateSegmented（2026-11-07 原型对齐）：布尔字段渲染
// 继承/开启/关闭三态分段——缺键默认「继承」，选「关闭」写入 false 并变脏，
// 切回「继承」删除键。
func TestBoolFieldTriStateSegmented(t *testing.T) {
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
	load(st, bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: home})
	cf := &st.ClaudeCfg
	if _, ok := corecfg.Get(cf.Doc, "alwaysThinkingEnabled"); ok {
		t.Fatal("前置不成立：文件不应含该键")
	}

	// 三态分段渲染且缺键时选「继承」。
	tt := ui.NewTester(func(c *ui.Context) { boolField(c, c.Theme(), uikit.Softs(c.Theme()), cf, "alwaysThinkingEnabled", "始终开启思考", "") }, 1000, 400)
	for _, want := range []string{"继承", "开启", "关闭"} {
		if !tt.HasText(want) {
			t.Fatalf("tri-state segmented missing %q: %v", want, tt.Texts())
		}
	}
	if _, ok := corecfg.Get(cf.Doc, "alwaysThinkingEnabled"); ok {
		t.Fatal("渲染不得写入继承键")
	}

	// 直接走写路径：setBoolChoice 写 false / 删除。
	setBoolChoice(cf, "alwaysThinkingEnabled", false)
	if v, _ := corecfg.Get(cf.Doc, "alwaysThinkingEnabled"); v != false {
		t.Fatalf("关闭应写入 false: %v", v)
	}
	if !cf.Dirty {
		t.Fatal("写入必须变脏")
	}
	setBoolChoice(cf, "alwaysThinkingEnabled", true)
	if v, _ := corecfg.Get(cf.Doc, "alwaysThinkingEnabled"); v != true {
		t.Fatalf("开启应写入 true: %v", v)
	}
	setBoolChoice(cf, "alwaysThinkingEnabled", 0) // 任何非法值=继承
	if _, ok := corecfg.Get(cf.Doc, "alwaysThinkingEnabled"); ok {
		t.Fatal("继承必须删除键")
	}
}
