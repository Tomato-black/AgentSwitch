package desktop

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/storage"
)

// TestAccessTabTitles 覆盖接入页三个 Agent tab 标题快照。
func TestAccessTabTitles(t *testing.T) {
	if a, b, d := accessTabTitles(); a != "Claude Code" || b != "Codex" || d != "Claude Desktop" {
		t.Fatal(a, b, d)
	}
}

// TestAccessConfigRowShowsCurrentBadge 覆盖「使用中」徽标纯函数。
func TestAccessConfigRowShowsCurrentBadge(t *testing.T) {
	if got := accessRowBadge(true); got != "使用中" {
		t.Fatal(got)
	}
	if accessRowBadge(false) != "" {
		t.Fatal("非 current 应无徽标")
	}
}

// TestAccessOverridesFromRows 覆盖覆盖项行→map：空键/空值都剔除（半填行
// 是误操作——评审裁定按计划语义收口）。
func TestAccessOverridesFromRows(t *testing.T) {
	got := accessOverridesFromRows([][2]string{{"K", "V"}, {"", "x"}, {"K2", ""}})
	if len(got) != 1 || got["K"] != "V" {
		t.Fatal(got)
	}
}

// TestAccessModelIDByLabel 覆盖模型名→ID 映射：命中含 disabled 项（编辑
// 旧配置回显不丢引用）；未命中空串。
func TestAccessModelIDByLabel(t *testing.T) {
	ms := []bridge.ModelView{{ID: "id1", Name: "m1", Enabled: true}, {ID: "id2", Name: "m2"}}
	if accessModelIDByLabel(ms, "m2") != "id2" {
		t.Fatal("应命中含 disabled 项")
	}
	if accessModelIDByLabel(ms, "nope") != "" {
		t.Fatal("未命中应空串")
	}
}

// TestAccessPageRenders 覆盖接入页骨架：两 tab、全局配置卡、实际配置卡、
// 空态引导与网关未运行提示在场；旧片段页元素（当前逻辑模型选择器）不再出现。
func TestAccessPageRenders(t *testing.T) {
	u, _ := testUI(t)
	u.page = "access"
	tt := ui.NewTester(u.view, 900, 600)
	for _, want := range []string{"Claude Code", "Codex", "全局配置", "实际配置", "还没有配置", "网关未运行"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("当前逻辑模型") {
		t.Fatalf("旧片段页元素泄漏: %v", tt.Texts())
	}
}

// TestAccessCodexPageMentionsDesktop 覆盖 Codex 页签的桌面版说明：Codex CLI /
// 桌面版 / IDE 插件共用同一份 ~/.codex/config.toml，应用后需完全退出重启。
func TestAccessCodexPageMentionsDesktop(t *testing.T) {
	u, _ := testUI(t)
	u.page = "access"
	u.accessApp = "codex"
	tt := ui.NewTester(u.view, 900, 600)
	for _, want := range []string{"桌面版", "~/.codex/config.toml", "完全退出", "模型选择器", "新建对话"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

// TestAccessPageScrollsWhenEditorOpen 回归：编辑器打开后接入页内容超出视口，
// 页面必须可纵向滚动到底部的保存按钮（此前缺 Scroll 容器，底部内容不可达）。
func TestAccessPageScrollsWhenEditorOpen(t *testing.T) {
	u, _ := testUI(t)
	u.page = "access"
	u.accessForm = accessForm{open: true}
	tt := ui.NewTester(u.view, 900, 600)
	before, ok := tt.Find("保存")
	if !ok {
		t.Fatalf("编辑器卡未渲染：%v", tt.Texts())
	}
	if before.Y < 600 {
		t.Fatalf("前置条件不成立：保存按钮已在视口内 y=%v", before.Y)
	}
	tt.Scroll(450, 300, 0, 1000)
	after, ok := tt.Find("保存")
	if !ok {
		t.Fatal("滚动后保存按钮从标签树消失")
	}
	if after.Y >= 600 {
		t.Fatalf("接入页不可滚动：保存按钮滚动前后 y=%v → %v", before.Y, after.Y)
	}
}

// TestAccessGlobalRowsOrdered 覆盖全局配置编辑行的键序稳定（字典序）。
func TestAccessGlobalRowsOrdered(t *testing.T) {
	rows := accessGlobalRows(map[string]string{"b": "2", "a": "1"})
	if rows[0][0] != "a" || rows[1][0] != "b" {
		t.Fatal(rows)
	}
}

func TestAccessDocSetEnv(t *testing.T) {
	raw, err := accessDocSetEnv(`{"env":{"A":"1"},"language":"zh-CN"}`, "B", "2")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	json.Unmarshal([]byte(raw), &doc)
	env := doc["env"].(map[string]any)
	if env["A"] != "1" || env["B"] != "2" || doc["language"] != "zh-CN" {
		t.Fatal(raw)
	}
	// value=="" 删除键
	raw2, err := accessDocSetEnv(raw, "A", "")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw2), &doc)
	if _, ok := doc["env"].(map[string]any)["A"]; ok {
		t.Fatal("空值应删键: " + raw2)
	}
	// 非法 JSON 报错
	if _, err := accessDocSetEnv(`{bad`, "A", "1"); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	// env 非对象（非 null）必须报错，不得静默清空（Minor#4）
	if _, err := accessDocSetEnv(`{"env":5}`, "A", "1"); err == nil ||
		!strings.Contains(err.Error(), "env 必须是 JSON 对象") {
		t.Fatalf("env 非对象应报错: %v", err)
	}
	// env:null 视为缺省
	if _, err := accessDocSetEnv(`{"env":null}`, "A", "1"); err != nil {
		t.Fatalf("env:null 应视为缺省: %v", err)
	}
}

func TestAccessDocEnvStrings(t *testing.T) {
	env, err := accessDocEnvStrings(`{"env":{"A":"1"}}`)
	if err != nil || env["A"] != "1" {
		t.Fatalf("%v %v", env, err)
	}
	if _, err := accessDocEnvStrings(`{"env":{"A":1}}`); err == nil {
		t.Fatal("非字符串值应报错（保留旧状态）")
	}
	if _, err := accessDocEnvStrings(`{bad`); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	// env 非对象（非 null）必须报错，不得静默返回空表（Minor#4）
	if _, err := accessDocEnvStrings(`{"env":5}`); err == nil ||
		!strings.Contains(err.Error(), "env 必须是 JSON 对象") {
		t.Fatalf("env 非对象应报错: %v", err)
	}
	if env, err := accessDocEnvStrings(`{"env":null}`); err != nil || len(env) != 0 {
		t.Fatalf("env:null 应视为缺省: %v %v", env, err)
	}
}

// TestAccessRepreviewForm 覆盖基线变化后的表单重预览（Important#1）：按当前
// 表单模型重新合成、成功清空旧消息、失败内联「预览失败：」。
func TestAccessRepreviewForm(t *testing.T) {
	u, a := testUI(t)
	for _, lm := range []storage.LogicalModel{
		{ID: "lm1", Name: "model-a", Enabled: true},
		{ID: "lm2", Name: "model-b", Enabled: true},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	u.accessApp = "claude"
	u.accessForm = accessForm{
		open:       true,
		modelLabel: "model-b",
		docJSON:    `{"env":{"ANTHROPIC_SMALL_FAST_MODEL":"model-a","CUSTOM":"x"}}`,
		msg:        "旧文案",
	}
	// 主模型切换处理器的顺序：先按旧模型派生值重定位，再重预览。
	rebased, err := bridge.RebaseAccessDoc(u.accessApp, u.accessForm.docJSON,
		map[string]string{"ANTHROPIC_SMALL_FAST_MODEL": "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	u.accessForm.docJSON = rebased
	u.accessRepreviewForm(u.accessSvc())
	if u.accessForm.msg != "" {
		t.Fatalf("成功应清空 msg: %q", u.accessForm.msg)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(u.accessForm.docJSON), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Env["ANTHROPIC_MODEL"] != "model-b" ||
		doc.Env["ANTHROPIC_SMALL_FAST_MODEL"] != "model-b" ||
		doc.Env["CUSTOM"] != "x" {
		t.Fatalf("预览未按当前模型合成: %s", u.accessForm.docJSON)
	}
	// 未知模型名 → PreviewDoc 报错 → 内联提示
	u.accessForm.modelLabel = "missing"
	u.accessRepreviewForm(u.accessSvc())
	if !strings.HasPrefix(u.accessForm.msg, "预览失败：") {
		t.Fatalf("失败应内联报错: %q", u.accessForm.msg)
	}
}

// TestAccessPreloadFormRendersGatewayToken 回归（2026-10-21 用户裁定）：编辑器
// 打开时默认渲染真实网关令牌，不再显示 __GATEWAY_TOKEN__ 遮蔽占位符。
func TestAccessPreloadFormRendersGatewayToken(t *testing.T) {
	u, a := testUI(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	u.accessApp = "claude"
	u.accessForm = accessForm{open: true}
	u.accessPreloadForm(u.accessSvc())
	token := u.svc.Gateway.Status().Token
	if token == "" {
		t.Fatal("前提不成立：网关令牌应已生成")
	}
	if !strings.Contains(u.accessForm.docJSON, token) {
		t.Fatalf("编辑器应默认渲染真实令牌: %s", u.accessForm.docJSON)
	}
	if strings.Contains(u.accessForm.docJSON, "__GATEWAY_TOKEN__") {
		t.Fatalf("编辑器不应再出现遮蔽占位符: %s", u.accessForm.docJSON)
	}
}

func TestAccessParseOneM(t *testing.T) {
	cases := map[string][2]any{
		"my-code[1M]": {"my-code", true},
		"my-code[1m]": {"my-code", true},
		"my-code":     {"my-code", false},
	}
	for in, want := range cases {
		name, oneM := accessParseOneM(in)
		if name != want[0] || oneM != want[1] {
			t.Fatalf("accessParseOneM(%q)=%v,%v want %v", in, name, oneM, want)
		}
	}
}

func TestAccessRoleRoundTrip(t *testing.T) {
	// 补丁：选模型+1M+显示名
	raw, err := accessDocSetEnv(`{"env":{}}`, "ANTHROPIC_DEFAULT_SONNET_MODEL", "fast[1M]")
	if err != nil {
		t.Fatal(err)
	}
	raw, err = accessDocSetEnv(raw, "ANTHROPIC_DEFAULT_SONNET_MODEL_NAME", "极速")
	if err != nil {
		t.Fatal(err)
	}
	env, err := accessDocEnvStrings(raw)
	if err != nil {
		t.Fatal(err)
	}
	name, oneM := accessParseOneM(env["ANTHROPIC_DEFAULT_SONNET_MODEL"])
	if name != "fast" || !oneM || env["ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"] != "极速" {
		t.Fatal(raw)
	}
	// 回填：Subagent 无显示名键；空键 = 跟随主模型
	env2, err := accessDocEnvStrings(`{"env":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	name2, oneM2 := accessParseOneM(env2["CLAUDE_CODE_SUBAGENT_MODEL"])
	if name2 != "" || oneM2 {
		t.Fatal("空值应回落跟随主模型")
	}
}

// 主模型 1M（2026-10-21）：文档 env 的 ANTHROPIC_MODEL 带 [1M] 时回填勾选；
// 勾选/取消把「逻辑模型名[1M]」/纯名写回文档（保存归一化再剔除纯名）。
func TestAccessMainModelOneMPatchAndSync(t *testing.T) {
	u := &uiState{}
	u.accessApp = "claude"
	u.accessForm = accessForm{modelLabel: "my-code", docJSON: `{"env":{"ANTHROPIC_MODEL":"my-code[1M]"}}`}
	u.accessSyncRolesFromDoc()
	if !u.accessForm.mainOneM {
		t.Fatal("应从文档回填 1M 勾选")
	}
	u.accessForm.mainOneM = false
	u.accessPatchMainModel()
	env, err := accessDocEnvStrings(u.accessForm.docJSON)
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_MODEL"] != "my-code" {
		t.Fatal(u.accessForm.docJSON)
	}
	u.accessForm.mainOneM = true
	u.accessPatchMainModel()
	env, err = accessDocEnvStrings(u.accessForm.docJSON)
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_MODEL"] != "my-code[1M]" {
		t.Fatal(u.accessForm.docJSON)
	}
}

func TestAccessSyncRolesAndPatch(t *testing.T) {
	u := &uiState{}
	u.accessApp = "claude"
	u.accessForm = accessForm{docJSON: `{"env":{"ANTHROPIC_DEFAULT_HAIKU_MODEL":"fast[1M]"}}`}
	u.accessSyncRolesFromDoc()
	i := accessRoleIndexByEnvKey("ANTHROPIC_DEFAULT_HAIKU_MODEL")
	if u.accessForm.roleModels[i] != "fast" || !u.accessForm.roleOneM[i] {
		t.Fatalf("%v %v", u.accessForm.roleModels, u.accessForm.roleOneM)
	}
	u.accessForm.roleOneM[i] = false
	u.accessPatchRole(i)
	env, err := accessDocEnvStrings(u.accessForm.docJSON)
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != "fast" {
		t.Fatal(u.accessForm.docJSON)
	}
}

// TestDesktopFormDocRoundTrip：表单行 ↔ 角色映射文档双向转换（UI 层唯一
// 业务逻辑；空角色不落键、oneM 只在启用时生效）。
func TestDesktopFormDocRoundTrip(t *testing.T) {
	f := desktopForm{models: [4]string{"my-code", "", "mimo", ""}, oneM: [4]bool{true, false, false, false}}
	raw, err := f.desktopDocFromForm()
	if err != nil {
		t.Fatal(err)
	}
	u := &uiState{}
	u.desktopFormFromDoc(raw)
	if u.desktopForm.models[0] != "my-code" || !u.desktopForm.oneM[0] {
		t.Fatalf("sonnet 回填: %+v", u.desktopForm)
	}
	if u.desktopForm.models[1] != "" || u.desktopForm.models[3] != "" {
		t.Fatalf("未启用角色不得回填: %+v", u.desktopForm)
	}
	if u.desktopForm.models[2] != "mimo" || u.desktopForm.oneM[2] {
		t.Fatalf("haiku 回填: %+v", u.desktopForm)
	}
	// 全空必须拒绝保存语义（docFromForm 报错）。
	empty := desktopForm{}
	if _, err := empty.desktopDocFromForm(); err == nil {
		t.Fatal("全空角色应报错")
	}
}
