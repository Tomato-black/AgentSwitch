package bridge

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"AgentSwitch/internal/storage"
)

// --- Task 5：Apply 投影与别名持久化 ---

// TestWriteClaudeDesktopLiveMergesManagedKeys：Desktop 3P 网关模式需要写
// 4 个文件（参照 cc-switch）——两个配置文件设 deploymentMode=3p、
// configLibrary 下的 profile 文件放网关五键、_meta.json 登记 appliedId；
// 普通配置的 MCP 等键原样保留。
func TestWriteClaudeDesktopLiveMergesManagedKeys(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	p := desktopPathsFor(acc.Home)
	os.MkdirAll(filepath.Dir(p.normalConfig), 0o755)
	os.WriteFile(p.normalConfig, []byte(`{"mcpServers":{"x":{"command":"c"}}}`), 0o600)
	plan := desktopPlan{Routes: []DesktopRoute{
		{Role: "sonnet", RouteID: "claude-sonnet-5", Model: "my-code", OneM: true},
		{Role: "haiku", RouteID: "claude-haiku-4-5", Model: "mimo"},
	}, Aliases: map[string]string{}}
	if err := acc.writeClaudeDesktopLive(plan, "http://127.0.0.1:17890", "new-token"); err != nil {
		t.Fatal(err)
	}
	readJSON := func(path string) map[string]any {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("读 %s: %v", path, err)
		}
		var got map[string]any
		if err := json.Unmarshal(b, &got); err != nil {
			t.Fatalf("解析 %s: %v", path, err)
		}
		return got
	}
	// 1+2: 两个配置文件只设 deploymentMode，其余键保留。
	for _, path := range []string{p.normalConfig, p.threepConfig} {
		got := readJSON(path)
		if got["deploymentMode"] != "3p" {
			t.Fatalf("deploymentMode 未设（%s）: %v", path, got)
		}
	}
	if readJSON(p.normalConfig)["mcpServers"] == nil {
		t.Fatal("普通配置的 mcpServers 必须保留")
	}
	// 3: profile 文件 = 网关 profile 五键 + 网关出口/模式开关。
	prof := readJSON(p.profile)
	if prof["inferenceProvider"] != "gateway" || prof["inferenceGatewayAuthScheme"] != "bearer" {
		t.Fatalf("profile 受管键: %v", prof)
	}
	if prof["inferenceGatewayBaseUrl"] != "http://127.0.0.1:17890" || prof["inferenceGatewayApiKey"] != "new-token" {
		t.Fatalf("profile 网关键: %v", prof)
	}
	if prof["disableDeploymentModeChooser"] != true {
		t.Fatalf("disableDeploymentModeChooser: %v", prof)
	}
	models, _ := prof["inferenceModels"].([]any)
	if len(models) != 2 {
		t.Fatalf("inferenceModels: %v", prof)
	}
	m0, _ := models[0].(map[string]any)
	if m0["name"] != "claude-sonnet-5" || m0["supports1m"] != true || m0["prefer1m"] != true {
		t.Fatalf("oneM 角色应为对象形状: %v", models[0])
	}
	if models[1] != "claude-haiku-4-5" {
		t.Fatalf("非 1M 角色应为纯字符串名: %v", models[1])
	}
	// 4: _meta.json 登记 appliedId 与 entries。
	meta := readJSON(p.meta)
	if meta["appliedId"] != desktopProfileID {
		t.Fatalf("appliedId: %v", meta)
	}
	entries, _ := meta["entries"].([]any)
	found := false
	for _, e := range entries {
		if m, ok := e.(map[string]any); ok && m["id"] == desktopProfileID {
			found = true
		}
	}
	if !found {
		t.Fatalf("entries 缺 profile: %v", meta)
	}
	// 0600 权限
	fi, _ := os.Stat(p.profile)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("perm %v, want 0600", fi.Mode().Perm())
	}
}

func TestWriteClaudeDesktopLiveInvalidJSONAborts(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	path := desktopPathsFor(acc.Home).normalConfig
	os.MkdirAll(filepath.Dir(path), 0o755)
	os.WriteFile(path, []byte(`{broken`), 0o600)
	err := acc.writeClaudeDesktopLive(desktopPlan{}, "http://x", "tok")
	if err == nil {
		t.Fatal("非法 JSON 应中止")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "{broken" {
		t.Fatal("失败后不得改动原文件")
	}
}

func TestDesktopApplyEndToEnd(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	lm, err := a.Store().LogicalModels().Save(storage.LogicalModel{Name: "my-code", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude_desktop", "desktop 默认", lm.ID,
		`{"sonnet":{"model":"my-code","oneM":true}}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	// Desktop profile 文件（3P 网关模式核心）
	b, err := os.ReadFile(desktopPathsFor(acc.Home).profile)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	json.Unmarshal(b, &got)
	st := Gateway{A: a}.Status()
	if got["inferenceGatewayBaseUrl"] != fmt.Sprintf("http://127.0.0.1:%d", st.Port) {
		t.Fatalf("baseUrl 应随设置端口: %s", b)
	}
	if got["inferenceGatewayApiKey"] != st.Token {
		t.Fatalf("apiKey 应为网关令牌")
	}
	// 两个配置文件与 _meta 登记
	for _, path := range []string{desktopPathsFor(acc.Home).normalConfig, desktopPathsFor(acc.Home).threepConfig} {
		mb, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		json.Unmarshal(mb, &m)
		if m["deploymentMode"] != "3p" {
			t.Fatalf("deploymentMode（%s）: %s", path, mb)
		}
	}
	mb, _ := os.ReadFile(desktopPathsFor(acc.Home).meta)
	var meta map[string]any
	json.Unmarshal(mb, &meta)
	if meta["appliedId"] != desktopProfileID {
		t.Fatalf("_meta appliedId: %s", mb)
	}
	// 别名持久化
	al := storage.DesktopAliases(a.Store())
	if al["claude-sonnet-5"] != "my-code" {
		t.Fatalf("别名表: %v", al)
	}
	// 备份：改前无文件则不生成；再 Apply 一次也不崩
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	// current 标记
	cur, err := a.Store().AccessConfigs().Current("claude_desktop")
	if err != nil || cur.ID != v.ID {
		t.Fatalf("SetCurrent: %v %v", cur, err)
	}
}

func TestDesktopApplyRejectsMissingModel(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	lm, _ := a.Store().LogicalModels().Save(storage.LogicalModel{Name: "my-code", Enabled: true})
	v, err := acc.Create("claude_desktop", "bad", lm.ID, `{"sonnet":{"model":"my-code"}}`)
	if err != nil {
		t.Fatal(err)
	}
	// 删除绑定的逻辑模型后 Apply 必须报「改绑」
	if err := a.Store().LogicalModels().Delete(lm.ID); err != nil {
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err == nil || !strings.Contains(err.Error(), "改绑") {
		t.Fatalf("引用缺失模型应报改绑: %v", err)
	}
	if _, err := os.Stat(desktopPathsFor(acc.Home).profile); err == nil {
		t.Fatal("失败不得写 Desktop 配置")
	}
}

// --- Task 4：claude_desktop 文档解析与角色合成 ---

func TestParseDesktopAccessDocRoles(t *testing.T) {
	doc, env, err := parseAccessDoc("claude_desktop",
		`{"sonnet":{"model":"my-code","oneM":true},"opus":{"model":"mimo"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(env) != 0 {
		t.Fatalf("desktop 文档无 env 层: %v", env)
	}
	if doc["sonnet"] == nil || doc["opus"] == nil {
		t.Fatalf("角色应保留: %v", doc)
	}
}

func TestParseDesktopAccessDocEmptyOK(t *testing.T) {
	for _, raw := range []string{"", "{}", "null"} {
		if _, _, err := parseAccessDoc("claude_desktop", raw); err != nil {
			t.Fatalf("%q 应允许: %v", raw, err)
		}
	}
}

func TestParseDesktopAccessDocRejectsBadInput(t *testing.T) {
	cases := map[string]string{
		"未知角色":     `{"gpt":{"model":"x"}}`,
		"model非字符串": `{"sonnet":{"model":1}}`,
		"值非对象":     `{"sonnet":"my-code"}`,
		"env键禁止":   `{"env":{"A":"b"}}`,
		"oneM非布尔":  `{"sonnet":{"model":"x","oneM":"yes"}}`,
	}
	for name, raw := range cases {
		if _, _, err := parseAccessDoc("claude_desktop", raw); err == nil {
			t.Fatalf("%s 应拒绝: %s", name, raw)
		}
	}
}

func TestComposeDesktopRoutes(t *testing.T) {
	routes, aliases, err := composeDesktopRoutes(
		`{"sonnet":{"model":"my-code","oneM":true},"haiku":{"model":"mimo"},"fable":{}}`)
	if err != nil {
		t.Fatal(err)
	}
	// fable 空对象 = 未启用；顺序 sonnet → haiku（文档序无关，按角色固定序）。
	if len(routes) != 2 {
		t.Fatalf("routes = %v", routes)
	}
	if routes[0].RouteID != "claude-sonnet-5" || routes[0].Model != "my-code" || !routes[0].OneM {
		t.Fatalf("sonnet route: %+v", routes[0])
	}
	if routes[1].RouteID != "claude-haiku-4-5" || routes[1].Model != "mimo" || routes[1].OneM {
		t.Fatalf("haiku route: %+v", routes[1])
	}
	if aliases["claude-sonnet-5"] != "my-code" || aliases["claude-haiku-4-5"] != "mimo" {
		t.Fatalf("aliases: %v", aliases)
	}
	if _, ok := aliases["claude-fable-5"]; ok {
		t.Fatal("未启用角色不得进别名表")
	}
}

func TestComposeDesktopRoutesRequiresLogicalModel(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	_ = acc // 保证 tempApp 链接（存在性校验在 Apply 路径）
	// 引用空模型名必须拒绝（结构校验不依赖存储）。
	if _, _, err := composeDesktopRoutes(`{"sonnet":{"model":""}}`); err == nil {
		t.Fatal("空 model 应拒绝")
	}
}

func TestValidateDesktopRouteIDJSONRoundTrip(t *testing.T) {
	// inferenceModels 形状在写入侧生成，这里 pin 角色→routeID 常量表本身。
	b, _ := json.Marshal(desktopRoleRouteIDs)
	var m map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"sonnet": "claude-sonnet-5", "opus": "claude-opus-5",
		"haiku": "claude-haiku-4-5", "fable": "claude-fable-5"}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("role %s: got %q want %q", k, m[k], v)
		}
	}
}
