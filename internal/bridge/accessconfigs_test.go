package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/storage"
)

// seedLogicalModel 在测试库中建一个启用的逻辑模型：写 config.toml 的目录
// 自 2026-10-27 起按显式模型解析，模型必须存在且启用。
func seedLogicalModel(t *testing.T, a *app.App, name string) {
	t.Helper()
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{Name: name, Enabled: true}); err != nil {
		t.Fatal(err)
	}
}

func TestClaudeLiveProjectionPreservesAndMerges(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"allow":["Bash"]},"env":{"FOO":"bar","ANTHROPIC_MODEL":"old"}}`), 0o600)
	if err := acc.writeClaudeLive(nil, map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:17890", "ANTHROPIC_MODEL": "new", "HTTPS_PROXY": "http://p:1"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	var got map[string]any
	json.Unmarshal(b, &got)
	env := got["env"].(map[string]any)
	if env["ANTHROPIC_MODEL"] != "new" || env["FOO"] != "bar" {
		t.Fatal(string(b))
	}
	if env["HTTPS_PROXY"] != "http://p:1" {
		t.Fatal("合成 env 非基础键（全局配置）应写入 live 文件: " + string(b))
	}
	if got["permissions"] == nil {
		t.Fatal("非受管键丢失")
	}
}

func TestClaudeLiveProjectionInvalidJSONAborts(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{broken`), 0o600)
	if err := acc.writeClaudeLive(nil, map[string]string{"ANTHROPIC_MODEL": "x"}); err == nil {
		t.Fatal("非法 JSON 应中止")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(b) != "{broken" {
		t.Fatal("失败后不得改动原文件")
	}
}

func TestClaudeLiveWritesDocTopLevelKeys(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"),
		[]byte(`{"permissions":{"allow":["Bash"]},"language":"en-US","env":{"FOO":"bar"}}`), 0o600)
	doc := map[string]any{
		"language":   "zh-CN",
		"mcpServers": map[string]any{"x": map[string]any{"command": "c"}},
	}
	if err := acc.writeClaudeLive(doc, map[string]string{"ANTHROPIC_MODEL": "new"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	var got map[string]any
	json.Unmarshal(b, &got)
	if got["language"] != "zh-CN" {
		t.Fatalf("文档顶层键应写入: %s", b)
	}
	if got["mcpServers"] == nil {
		t.Fatalf("文档新增顶层键应写入: %s", b)
	}
	if got["permissions"] == nil {
		t.Fatalf("非文档键应保留: %s", b)
	}
	if got["env"].(map[string]any)["FOO"] != "bar" {
		t.Fatalf("env 非受管键应保留: %s", b)
	}
}

func TestCodexLiveProjectionPreservesAndMerges(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "my-code")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("preferred_auth_method = \"apikey\"\n\n[mcp_servers.foo]\ncommand = \"mcp\"\n"), 0o600)
	if err := acc.writeCodexLive(map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "tok"}, "my-code", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	var got map[string]any
	if err := toml.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "my-code" || got["model_provider"] != "agentswitch" {
		t.Fatal(string(b))
	}
	if got["mcp_servers"] == nil {
		t.Fatal("非受管段丢失")
	}
	mp := got["model_providers"].(map[string]any)["agentswitch"].(map[string]any)
	// Codex CLI 0.143+ 已移除 Chat Completions 线路，仅支持 responses；
	// provider 的 name 为必填键（非空），缺失会报 provider name must not be empty。
	if mp["name"] != "agentswitch" {
		t.Fatal(mp)
	}
	if mp["wire_api"] != "responses" || mp["requires_openai_auth"] != false {
		t.Fatal(mp)
	}
	if mp["base_url"] != "http://127.0.0.1:17890/v1" {
		t.Fatal(mp)
	}
	// Codex 0.149 起自定义 provider 不再从 auth.json 继承凭据：第三方 Key
	// 必须写在 provider 表的 experimental_bearer_token（桌面版同样生效）。
	if mp["experimental_bearer_token"] != "tok" {
		t.Fatal(mp)
	}
	if _, err := os.Stat(filepath.Join(dir, "auth.json")); !os.IsNotExist(err) {
		t.Fatal("不应创建 auth.json（官方 ChatGPT 登录文件由 Codex 自己维护）")
	}
}

// Codex 第三方 Key 只写 config.toml；auth.json 是官方 ChatGPT 登录文件，
// 接入配置绝不改写它（2026-10-27 修订）。
func TestCodexLiveLeavesAuthJSONUntouched(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "m")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	os.MkdirAll(dir, 0o755)
	original := `{"tokens":{"access_token":"official"},"OPENAI_API_KEY":"user-key"}`
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte(original), 0o600)
	if err := acc.writeCodexLive(map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "tok"}, "m", nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("auth.json 不应被改写: %s", got)
	}
}

func TestCodexLiveProjectionInvalidTOMLAborts(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "m")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = "), 0o600)
	err := acc.writeCodexLive(map[string]string{"OPENAI_API_KEY": "x"}, "m", nil)
	if err == nil || !strings.Contains(err.Error(), "config.toml 解析失败") {
		t.Fatalf("非法 TOML 应报解析失败: %v", err)
	}
	// 解析必须先于模型解析：即使模型未登记，也应以 TOML 解析错误中止，
	// 而不是先报「模型不存在」（否则会在坏配置上继续走模型/目录生成）。
	err = acc.writeCodexLive(map[string]string{"OPENAI_API_KEY": "x"}, "ghost", nil)
	if err == nil || !strings.Contains(err.Error(), "config.toml 解析失败") ||
		strings.Contains(err.Error(), "不存在") {
		t.Fatalf("TOML 解析必须先于模型解析: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if string(b) != "model = " {
		t.Fatal("失败后不得改动")
	}
}

// auth.json 不再读也不写：即使它本身非法，也不应阻塞接入配置写入（登录
// 文件由 Codex 自己维护），且内容保持原样。
func TestCodexLiveIgnoresInvalidAuthJSON(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "m")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{broken"), 0o600)
	if err := acc.writeCodexLive(map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "x"}, "m", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	var got map[string]any
	if err := toml.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["model"] != "m" {
		t.Fatalf("config.toml 应写入: %s", b)
	}
	ab, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if string(ab) != "{broken" {
		t.Fatalf("auth.json 不应被改动: %s", ab)
	}
}

// TestProjectCodexBaseManagedKeys 钉死基础受管键（spec 2026-10-27 §4.3、§5）：
// 默认模型、提供方、目录引用与两个网关兼容项固定写入。
func TestProjectCodexBaseManagedKeys(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "my-code")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 预置冲突的受管键与一个未知用户键：受管键必须被强制覆盖，未知键保留。
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(
		"model = \"old\"\ndisable_response_storage = false\nweb_search = \"live\"\ncustom_user_key = \"keep\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := acc.writeCodexLive(map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "tok"},
		"my-code", map[string]any{"models": []any{"my-code"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	var cfg map[string]any
	if err := toml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["model"] != "my-code" || cfg["model_provider"] != "agentswitch" ||
		cfg["model_catalog_json"] != codexModelCatalogFileName ||
		cfg["disable_response_storage"] != true || cfg["web_search"] != "disabled" {
		t.Fatalf("受管键应覆盖冲突值: %v", cfg)
	}
	if cfg["custom_user_key"] != "keep" {
		t.Fatalf("未知用户键应保留: %v", cfg)
	}
}

// TestProjectCodexUsesExplicitModelList 钉死投影使用文档显式模型列表：只列
// 一个已启用模型时目录只含该模型（不再回退为「全部启用模型」）。
func TestProjectCodexUsesExplicitModelList(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "my-code")
	seedLogicalModel(t, a, "other")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if err := acc.writeCodexLive(map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "tok"},
		"my-code", map[string]any{"models": []any{"my-code"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(acc.Home, ".codex", codexModelCatalogFileName))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(b, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0]["slug"] != "my-code" {
		t.Fatalf("显式模型列表未生效（应只含 my-code）: %s", b)
	}
}

// TestProjectCodexCatalogAutoReview 钉死目录自动审批覆盖取自文档
// codex.auto_review_model（Task 6 交接：投影路径必须传入该值）。
func TestProjectCodexCatalogAutoReview(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "my-code")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	doc := map[string]any{
		"models": []any{"my-code"},
		"codex":  map[string]any{"auto_review_model": "my-code"},
	}
	if err := acc.writeCodexLive(map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "tok"},
		"my-code", doc); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(acc.Home, ".codex", codexModelCatalogFileName))
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(b, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 1 || catalog.Models[0]["auto_review_model_override"] != "my-code" {
		t.Fatalf("auto_review_model_override 未写入: %s", b)
	}
}

func TestBackupLiveCopiesAndRotates(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"a":1}`), 0o600)
	for i := 0; i < 12; i++ {
		if err := acc.backupLive("claude"); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond) // 时间戳文件名去重
	}
	bdir := filepath.Join(acc.Home, ".agentswitch", "backups", "claude")
	ents, _ := os.ReadDir(bdir)
	if len(ents) != 10 {
		t.Fatalf("备份份数 = %d，应为 10", len(ents))
	}
	b, err := os.ReadFile(filepath.Join(bdir, ents[0].Name()))
	if err != nil || string(b) != `{"a":1}` {
		t.Fatalf("%v %s", err, b)
	}
}

// Codex 接入不再写 auth.json，备份清单只应包含 config.toml（2026-10-27 修订）。
func TestLiveBackupFilesCodexOnlyConfigTOML(t *testing.T) {
	files := liveBackupFiles("codex")
	if len(files) != 1 || files[0] != filepath.Join(".codex", "config.toml") {
		t.Fatal(files)
	}
}

func TestBackupLiveSkipsMissingSource(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	if err := acc.backupLive("claude"); err != nil {
		t.Fatal(err)
	} // 无源文件不报错、不建目录
	if _, err := os.Stat(filepath.Join(acc.Home, ".agentswitch", "backups", "claude")); !os.IsNotExist(err) {
		t.Fatal("不应创建备份目录")
	}
}

func TestComposeAccessEnvClaudeMergePrecedence(t *testing.T) {
	st := Status{Port: 17890, Token: "tok123"}
	env, err := composeAccessEnv("claude", st, "my-code",
		map[string]string{"HTTPS_PROXY": "http://p:1"},
		map[string]string{"ANTHROPIC_SMALL_FAST_MODEL": "fast"})
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:17890" {
		t.Fatal(env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "tok123" {
		t.Fatal("token 未带入")
	}
	if env["ANTHROPIC_MODEL"] != "my-code" || env["ANTHROPIC_SMALL_FAST_MODEL"] != "fast" {
		t.Fatal(env)
	}
	if env["HTTPS_PROXY"] != "http://p:1" {
		t.Fatal("全局 env 未合并")
	}
}

func TestComposeAccessEnvBaseKeysImmutable(t *testing.T) {
	st := Status{Port: 17890, Token: "tok123"}
	env, err := composeAccessEnv("claude", st, "m",
		map[string]string{"ANTHROPIC_BASE_URL": "http://evil:1"},
		map[string]string{"ANTHROPIC_AUTH_TOKEN": "x", "ANTHROPIC_MODEL": "evil"})
	if err != nil {
		t.Fatal(err)
	}
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:17890" {
		t.Fatal("基础键被全局覆盖")
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "tok123" || env["ANTHROPIC_MODEL"] != "m" {
		t.Fatal("基础键被 overrides 覆盖")
	}
}

func TestComposeAccessEnvRejectsUnknownAppType(t *testing.T) {
	if _, err := composeAccessEnv("gemini", Status{}, "m", nil, nil); err == nil {
		t.Fatal("白名单外 appType 应报错")
	}
}

func TestNormalizeAccessEnvStripsLockedAndUnchanged(t *testing.T) {
	st := Status{Port: 17890, Token: "tok"}
	global := map[string]string{"HTTPS_PROXY": "http://p:1"}
	docEnv := map[string]string{
		"ANTHROPIC_BASE_URL":             "http://evil:9", // 锁定键→剔除
		"ANTHROPIC_AUTH_TOKEN":           "steal",         // 锁定键→剔除
		"ANTHROPIC_MODEL":                "other",         // 锁定键→剔除
		"ANTHROPIC_SMALL_FAST_MODEL":     "my-code",       // 与默认相同→剔除
		"HTTPS_PROXY":                    "http://p:1",    // 与全局相同→剔除
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "fast",          // 定制→保留
		"API_TIMEOUT_MS":                 "3000000",       // 新增→保留
	}
	got, err := normalizeAccessEnv("claude", st, "my-code", global, docEnv)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"ANTHROPIC_DEFAULT_SONNET_MODEL": "fast", "API_TIMEOUT_MS": "3000000"}
	if len(got) != len(want) {
		t.Fatalf("got=%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("got=%v", got)
		}
	}
}

func TestNormalizeAccessEnvCodex(t *testing.T) {
	st := Status{Port: 17890, Token: "tok"}
	got, err := normalizeAccessEnv("codex", st, "my-code", nil, map[string]string{
		"OPENAI_BASE_URL": "http://evil/v1",
		"OPENAI_API_KEY":  "steal",
		"RUST_LOG":        "debug",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := got["OPENAI_BASE_URL"]; ok {
		t.Fatal("OPENAI_BASE_URL 应剔除")
	}
	if _, ok := got["OPENAI_API_KEY"]; ok {
		t.Fatal("OPENAI_API_KEY 应剔除")
	}
	if got["RUST_LOG"] != "debug" {
		t.Fatalf("got=%v", got)
	}
}

func TestNormalizeAccessEnvRejectsBadInput(t *testing.T) {
	if _, err := normalizeAccessEnv("gemini", Status{}, "m", nil, nil); err == nil {
		t.Fatal("非法 appType 应报错")
	}
	if _, err := normalizeAccessEnv("claude", Status{}, "", nil, nil); err == nil {
		t.Fatal("空模型名应报错")
	}
}

func TestComposeAccessEnvBehaviorUnchangedAfterRefactor(t *testing.T) {
	// 重构后 composeAccessEnv 行为必须与旧实现一致：基础键+全局+docEnv+锁定回写
	st := Status{Port: 17890, Token: "tok"}
	got, err := composeAccessEnv("claude", st, "my-code",
		map[string]string{"HTTPS_PROXY": "http://p:1"},
		map[string]string{"ANTHROPIC_BASE_URL": "http://evil:9", "API_TIMEOUT_MS": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if got["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:17890" {
		t.Fatal(got)
	}
	if got["ANTHROPIC_AUTH_TOKEN"] != "tok" || got["ANTHROPIC_MODEL"] != "my-code" || got["ANTHROPIC_SMALL_FAST_MODEL"] != "my-code" {
		t.Fatal(got)
	}
	if got["HTTPS_PROXY"] != "http://p:1" || got["API_TIMEOUT_MS"] != "1" {
		t.Fatal(got)
	}
}

func TestComposeForCodexResolvesModelAndErrorsOnMissing(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	// 建一个逻辑模型（经 storage 直插，避开 bridge 依赖环）
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_ = a.Store().Settings().Set("access.global.codex", `{"HTTPS_PROXY":"http://p:1"}`)
	env, _, model, err := acc.composeFor(storage.AccessConfig{AppType: "codex", LogicalModelID: "lm1"})
	if err != nil {
		t.Fatal(err)
	}
	if model != "my-code" {
		t.Fatal(model)
	}
	if env["OPENAI_BASE_URL"] == "" || env["OPENAI_API_KEY"] == "" || env["HTTPS_PROXY"] != "http://p:1" {
		t.Fatal(env)
	}
	if _, _, _, err := acc.composeFor(storage.AccessConfig{AppType: "codex", LogicalModelID: "missing"}); err == nil {
		t.Fatal("缺失逻辑模型应报错")
	}
}

func TestPrepareCodexResolvesModelsAndValidates(t *testing.T) {
	a := tempApp(t)
	for _, lm := range []storage.LogicalModel{
		{ID: "lm1", Name: "my-code", Enabled: true},
		{ID: "lm2", Name: "test", Enabled: true},
		{ID: "lm3", Name: "off", Enabled: false},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}

	// 旧 env-only 配置：保存时写入回退列表 [bound + 启用模型]；
	// 锁定 env 键仍被剔除。
	raw, err := acc.prepareDoc("codex", "my-code", `{"env":{"RUST_LOG":"debug","OPENAI_API_KEY":"steal"}}`)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	models, ok := doc["models"].([]any)
	if !ok || len(models) != 2 || models[0] != "my-code" || models[1] != "test" {
		t.Fatalf("fallback models 应写回文档: %s", raw)
	}
	env, _ := doc["env"].(map[string]any)
	if env["RUST_LOG"] != "debug" {
		t.Fatalf("自定义 env 应保留: %s", raw)
	}
	if _, locked := env["OPENAI_API_KEY"]; locked {
		t.Fatalf("锁定 env 键应剔除: %s", raw)
	}

	// 显式列表去空白后保留顺序，codex 字段归一化。
	raw, err = acc.prepareDoc("codex", "my-code",
		`{"models":[" my-code ","test"],"codex":{"subagent_model":" test ","subagent_reasoning_effort":" high ","auto_review_model":""}}`)
	if err != nil {
		t.Fatal(err)
	}
	doc = nil
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	models, _ = doc["models"].([]any)
	if len(models) != 2 || models[0] != "my-code" || models[1] != "test" {
		t.Fatalf("%s", raw)
	}
	codex, _ := doc["codex"].(map[string]any)
	if codex["subagent_model"] != "test" || codex["subagent_reasoning_effort"] != "high" || codex["auto_review_model"] != "" {
		t.Fatalf("codex 字段应去空白: %s", raw)
	}

	cases := []struct {
		raw  string
		want string
	}{
		{`{"models":["test","my-code"]}`, "第一个模型必须是绑定的默认模型"},
		{`{"models":["my-code","off"]}`, "已停用"},
		{`{"models":["my-code","ghost"]}`, "不存在"},
		{`{"codex":{"subagent_model":"ghost"}}`, "子代理模型 ghost 不在模型列表中"},
		{`{"codex":{"auto_review_model":"ghost"}}`, "自动审批模型 ghost 不在模型列表中"},
	}
	for _, tc := range cases {
		if _, err := acc.prepareDoc("codex", "my-code", tc.raw); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err=%v，应包含 %q", tc.raw, err, tc.want)
		}
	}
}

func TestAccessConfigsCRUDAndView(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "工作", "lm1", accessEnvDoc(map[string]string{"A": "1"}))
	if err != nil {
		t.Fatal(err)
	}
	if v.LogicalModelName != "my-code" {
		t.Fatal(v.LogicalModelName)
	}
	// Create 落库的是整份 settings 文档：view 须取回文档、composeFor 须能
	// 从文档提取 env（Task 1 机械适配的行为等价性）。
	var viewDoc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(v.Settings), &viewDoc); err != nil || viewDoc.Env["A"] != "1" {
		t.Fatalf("view 文档未回读: %q %v", v.Settings, err)
	}
	stored, err := a.Store().AccessConfigs().Get(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	env, _, _, err := acc.composeFor(stored)
	if err != nil || env["A"] != "1" {
		t.Fatalf("composeFor 应取回文档 env 覆盖项: %v %v", env, err)
	}
	if _, err := acc.Create("claude", "工作", "lm1", ""); err == nil {
		t.Fatal("重名应报中文错误")
	}
	if err := acc.Update(v.ID, "个人", "lm1", ""); err != nil {
		t.Fatal(err)
	}
	if len(acc.List("claude")) != 1 || acc.List("claude")[0].Name != "个人" {
		t.Fatal(acc.List("claude"))
	}
	acc.Remove(v.ID)
	if len(acc.List("claude")) != 0 {
		t.Fatal("删除未生效")
	}
}

func TestCreateNormalizesDocBeforeStore(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "工作", "lm1",
		`{"env":{"ANTHROPIC_AUTH_TOKEN":"steal","ANTHROPIC_MODEL":"other","API_TIMEOUT_MS":"1"},"language":"zh-CN"}`)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(v.Settings), &doc); err != nil {
		t.Fatal(err)
	}
	env := doc["env"].(map[string]any)
	if _, ok := env["ANTHROPIC_AUTH_TOKEN"]; ok {
		t.Fatal("锁定键不应入库")
	}
	if _, ok := env["ANTHROPIC_MODEL"]; ok {
		t.Fatal("锁定键不应入库")
	}
	if env["API_TIMEOUT_MS"] != "1" {
		t.Fatalf("定制键应保留: %v", env)
	}
	if doc["language"] != "zh-CN" {
		t.Fatalf("顶层键应保留: %v", doc)
	}
	// codex 顶层键拒绝
	if _, err := acc.Create("codex", "x", "lm1", `{"env":{},"language":"zh-CN"}`); err == nil {
		t.Fatal("codex 顶层键应报错")
	}
}

func TestAccessConfigsGlobalConfig(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if err := acc.SaveGlobalConfig("codex", map[string]string{"K": "V"}); err != nil {
		t.Fatal(err)
	}
	g, err := acc.GlobalConfig("codex")
	if err != nil || g["K"] != "V" {
		t.Fatalf("%v %v", g, err)
	}
	if err := acc.SaveGlobalConfig("gemini", nil); err == nil {
		t.Fatal("白名单外 appType 应报错")
	}
}

// TestGlobalConfigClaudeRetired 覆盖 2026-11-07 用户决策：Claude 全局配置下线
// （Claude Code 设置中心接管 env 管理）——残留的 access.global.claude 不再
// 参与合成，保存直接拒绝；codex 全局配置不受影响。
func TestGlobalConfigClaudeRetired(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	// 残留键直写 settings（模拟下线前的存量数据）。
	if err := a.Store().Settings().Set("access.global.claude", `{"HTTPS_PROXY":"http://p:1"}`); err != nil {
		t.Fatal(err)
	}
	g, err := acc.GlobalConfig("claude")
	if err != nil || len(g) != 0 {
		t.Fatalf("claude 全局配置应恒为空: %v %v", g, err)
	}
	raw, err := acc.PreviewDoc("claude", "lm1", `{"env":{"CUSTOM":"x"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "HTTPS_PROXY") {
		t.Fatalf("claude 预览不得合并全局配置: %s", raw)
	}
	if err := acc.SaveGlobalConfig("claude", map[string]string{"K": "V"}); err == nil {
		t.Fatal("claude 全局配置保存应拒绝")
	}
	// codex 全局配置不受影响。
	if err := acc.SaveGlobalConfig("codex", map[string]string{"K": "V"}); err != nil {
		t.Fatal(err)
	}
	if g, err := acc.GlobalConfig("codex"); err != nil || g["K"] != "V" {
		t.Fatalf("codex 全局配置应正常: %v %v", g, err)
	}
}

func TestApplyWritesLiveAndSetsCurrent(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "工作", "lm1", "")
	if err != nil {
		t.Fatal(err)
	}
	// 控制器裁定：Apply 前先预置旧 settings.json——空 Home 上无源文件不建
	// 备份目录（Task 8 契约），预置后「应用前应有备份」断言才有意义。
	if err := os.MkdirAll(filepath.Join(acc.Home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acc.Home, ".claude", "settings.json"), []byte(`{"old":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(acc.Home, ".claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "ANTHROPIC_MODEL") || !strings.Contains(string(b), "my-code") {
		t.Fatal(string(b))
	}
	cur, err := a.Store().AccessConfigs().Current("claude")
	if err != nil || cur.ID != v.ID {
		t.Fatalf("%v %v", cur, err)
	}
	if _, err := os.Stat(filepath.Join(acc.Home, ".agentswitch", "backups", "claude")); err != nil {
		t.Fatal("应用前应有备份")
	}
}

func TestRemoveCurrentKeepsLiveUntouched(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(acc.Home, ".claude", "settings.json"))
	acc.Remove(v.ID)
	after, _ := os.ReadFile(filepath.Join(acc.Home, ".claude", "settings.json"))
	if string(before) != string(after) {
		t.Fatal("删除使用中配置不得改动 live 文件")
	}
}

// 失败路径不产生半状态：绑定逻辑模型缺失时 Apply 即停——不写 live、不置 current。
func TestApplyMissingLogicalModelLeavesNoHalfState(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "工作", "lm1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store().LogicalModels().Delete("lm1"); err != nil {
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err == nil {
		t.Fatal("绑定逻辑模型缺失应报错")
	}
	if _, err := os.Stat(filepath.Join(acc.Home, ".claude", "settings.json")); !os.IsNotExist(err) {
		t.Fatal("失败不得写 live")
	}
	if _, err := a.Store().AccessConfigs().Current("claude"); err == nil {
		t.Fatal("失败不得置 current")
	}
}

// TestPersistFileIsFullDocForClaude 钉死 claude 持久文件 = 整份合成文档
// （doc 顶层键 + env 合成结果），不再只是 {"env":{...}}。Create 的文档化签名
// 是 Task 7 的事，这里直接入库存整份文档，只测 writePersistEnv 语义。
func TestPersistFileIsFullDocForClaude(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store().AccessConfigs().Save(storage.AccessConfig{
		ID: "cfg1", AppType: "claude", Name: "工作", LogicalModelID: "lm1",
		Settings: `{"language":"zh-CN","env":{}}`,
	}); err != nil {
		t.Fatal(err)
	}
	list := acc.List("claude")
	cmd, err := acc.LaunchCommand(list[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "--settings") {
		t.Fatalf("启动命令应引用持久文件: %s", cmd)
	}
	b, err := os.ReadFile(filepath.Join(acc.Home, ".agentswitch", "access", "claude", list[0].ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["language"] != "zh-CN" {
		t.Fatalf("持久文件应为整份文档: %s", b)
	}
	env, ok := doc["env"].(map[string]any)
	if !ok || env["ANTHROPIC_MODEL"] != "my-code" || env["ANTHROPIC_BASE_URL"] == nil {
		t.Fatalf("持久文件 env 应合成: %s", b)
	}
}

func TestWritePersistAndLaunchCommand(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cv, _ := acc.Create("claude", "工作", "lm1", "")
	cmd, err := acc.LaunchCommand(cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(acc.Home, ".agentswitch", "access", "claude", cv.ID+".json")
	if !strings.Contains(cmd, p) || !strings.HasPrefix(cmd, "claude --settings ") {
		t.Fatal(cmd)
	}
	b, _ := os.ReadFile(p)
	var payload struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Env["ANTHROPIC_MODEL"] != "my-code" || payload.Env["ANTHROPIC_AUTH_TOKEN"] == "" {
		t.Fatal(string(b))
	}
	fi, _ := os.Stat(p)
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("权限 = %v", fi.Mode().Perm())
	}

	xv, _ := acc.Create("codex", "x", "lm1", "")
	cmd2, err := acc.LaunchCommand(xv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd2, "codex -m \"my-code\"") || !strings.Contains(cmd2, ".env") {
		t.Fatal(cmd2)
	}
}

func TestOpenTerminalAssemblesScript(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	oldPlatform := accessPlatform
	accessPlatform = "darwin"
	defer func() { accessPlatform = oldPlatform }()
	var gotName string
	var gotArgs []string
	old := openTerminalExec
	openTerminalExec = func(name string, args ...string) error {
		gotName, gotArgs = name, args
		return nil
	}
	defer func() { openTerminalExec = old }()
	if err := acc.OpenTerminal(v.ID); err != nil {
		t.Fatal(err)
	}
	if gotName != "osascript" || len(gotArgs) != 2 || gotArgs[0] != "-e" {
		t.Fatalf("终端命令 = %q %v", gotName, gotArgs)
	}
	if !strings.Contains(gotArgs[1], `tell application "Terminal" to do script "claude --settings`) {
		t.Fatal(gotArgs[1])
	}
	if err := acc.OpenTerminal("missing"); err == nil {
		t.Fatal("不存在的配置应报错")
	}
}

// --- 2026-10-26 Windows 适配：启动器 / 启动命令 / 终端分派 ---

// TestWindowsLauncherForClaude 钉死 Claude 的 .cmd 内容：引用 settings JSON，
// 转发额外参数，CRLF 行尾。
func TestWindowsLauncherForClaude(t *testing.T) {
	settings := `C:\Users\me\.agentswitch\access\claude\cfg1.json`
	b, err := writeWindowsLauncher("claude", map[string]string{"ANTHROPIC_MODEL": "my-code"}, "my-code", settings)
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	want := "@echo off\r\nsetlocal\r\nclaude --settings \"" + settings + "\" %*\r\n"
	if got != want {
		t.Fatalf("launcher = %q, want %q", got, want)
	}
}

// TestWindowsLauncherForCodex 钉死 Codex 的 .cmd：env 按字典序 set，% 转义为
// %%，最后以当前逻辑模型启动。
func TestWindowsLauncherForCodex(t *testing.T) {
	env := map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890",
		"OPENAI_API_KEY":  "tok%en",
	}
	b, err := writeWindowsLauncher("codex", env, "my-code", "")
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	want := "@echo off\r\nsetlocal\r\n" +
		"set \"OPENAI_API_KEY=tok%%en\"\r\n" +
		"set \"OPENAI_BASE_URL=http://127.0.0.1:17890\"\r\n" +
		"codex -m \"my-code\" %*\r\n"
	if got != want {
		t.Fatalf("launcher = %q, want %q", got, want)
	}
}

// TestWindowsLauncherRejectsUnsafeValues 覆盖批处理注入面：值/模型含双引号
// 或换行时拒绝生成（行格式无法安全表达）。
func TestWindowsLauncherRejectsUnsafeValues(t *testing.T) {
	if _, err := writeWindowsLauncher("codex", map[string]string{"K": `a"b`}, "m", ""); err == nil {
		t.Fatal("值含双引号应报错")
	}
	if _, err := writeWindowsLauncher("codex", map[string]string{"K": "a\nb"}, "m", ""); err == nil {
		t.Fatal("值含换行应报错")
	}
	if _, err := writeWindowsLauncher("codex", map[string]string{"K": "v"}, `a"b`, ""); err == nil {
		t.Fatal("模型含双引号应报错")
	}
	if _, err := writeWindowsLauncher("codex", map[string]string{"K": "v"}, "a%b", ""); err != nil {
		t.Fatalf("模型含 %% 应转义而非报错: %v", err)
	}
}

// TestLaunchCommandForWindows 钉死展示命令：经 cmd /c call 引用 .cmd，
// 在 cmd 与 PowerShell 中都可直接执行。
func TestLaunchCommandForWindows(t *testing.T) {
	f := persistFiles{launcher: `C:\Users\me\.agentswitch\access\claude\cfg1.cmd`}
	cmd, err := launchCommandFor("windows", "claude", f, "my-code")
	if err != nil {
		t.Fatal(err)
	}
	want := `cmd /c call "` + f.launcher + `"`
	if cmd != want {
		t.Fatalf("命令 = %q, want %q", cmd, want)
	}
}

// TestLaunchCommandWindowsWritesLauncher 是跨平台集成测试：替换平台后
// LaunchCommand 必须落 .cmd 并返回 cmd /c call 命令（claude 与 codex）。
func TestLaunchCommandWindowsWritesLauncher(t *testing.T) {
	oldPlatform := accessPlatform
	accessPlatform = "windows"
	defer func() { accessPlatform = oldPlatform }()

	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cv, _ := acc.Create("claude", "工作", "lm1", "")
	cmd, err := acc.LaunchCommand(cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	lc := filepath.Join(acc.Home, ".agentswitch", "access", "claude", cv.ID+".cmd")
	if cmd != `cmd /c call "`+lc+`"` {
		t.Fatalf("claude 命令 = %q", cmd)
	}
	b, err := os.ReadFile(lc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "claude --settings ") || !strings.Contains(string(b), cv.ID+".json") {
		t.Fatalf("claude 启动器 = %q", b)
	}

	xv, _ := acc.Create("codex", "x", "lm1", "")
	cmd2, err := acc.LaunchCommand(xv.ID)
	if err != nil {
		t.Fatal(err)
	}
	lx := filepath.Join(acc.Home, ".agentswitch", "access", "codex", xv.ID+".cmd")
	if cmd2 != `cmd /c call "`+lx+`"` {
		t.Fatalf("codex 命令 = %q", cmd2)
	}
	b2, err := os.ReadFile(lx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b2), `codex -m "my-code"`) || !strings.Contains(string(b2), "OPENAI_BASE_URL") {
		t.Fatalf("codex 启动器 = %q", b2)
	}
}

// TestOpenTerminalWindowsStartsLauncher 钉死 Windows 的终端拉起参数：
// cmd /c start "" <launcher.cmd>（start 的空标题参数不可省）。
func TestOpenTerminalWindowsStartsLauncher(t *testing.T) {
	oldPlatform := accessPlatform
	accessPlatform = "windows"
	defer func() { accessPlatform = oldPlatform }()

	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	var gotName string
	var gotArgs []string
	old := openTerminalExec
	openTerminalExec = func(name string, args ...string) error {
		gotName, gotArgs = name, args
		return nil
	}
	defer func() { openTerminalExec = old }()
	if err := acc.OpenTerminal(v.ID); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(acc.Home, ".agentswitch", "access", "claude", v.ID+".cmd")
	if gotName != "cmd" || len(gotArgs) != 4 || gotArgs[0] != "/c" || gotArgs[1] != "start" || gotArgs[2] != "" || gotArgs[3] != launcher {
		t.Fatalf("终端命令 = %q %v", gotName, gotArgs)
	}
}

// TestOpenTerminalUnsupportedPlatform 钉死非 macOS/Windows 的明确错误。
func TestOpenTerminalUnsupportedPlatform(t *testing.T) {
	oldPlatform := accessPlatform
	accessPlatform = "linux"
	defer func() { accessPlatform = oldPlatform }()

	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	if err := acc.OpenTerminal(v.ID); err == nil || !strings.Contains(err.Error(), "不支持") {
		t.Fatalf("Linux 打开终端应明确报不支持: %v", err)
	}
}

// TestLaunchCommandEscapesShellMetachars 覆盖 Task 11 评审 Important：
// 模型名含 ' $ ` " \ 时启动命令必须已 shell 转义（贴给用户执行的命令，
// 不得破损或注入）。
func TestLaunchCommandEscapesShellMetachars(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "a'b$(id)", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	xv, _ := acc.Create("codex", "x", "lm1", "")
	cmd, err := acc.LaunchCommand(xv.ID)
	if err != nil {
		t.Fatal(err)
	}
	// 外层单引号内 ' 转义为 '\''，$ 转义为 \$——完整转义片段必须在场。
	if !strings.Contains(cmd, `a'\''b\$(id)`) {
		t.Fatalf("模型名未正确 shell 转义: %s", cmd)
	}
}

// TestWritePersistRejectsNewlineValue 覆盖 .env 值含换行的中止分支
// （行格式无法安全表达；Task 11 评审 Minor 补测）。
func TestWritePersistRejectsNewlineValue(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store().Settings().Set("access.global.codex", `{"K":"a\nb"}`); err != nil {
		t.Fatal(err)
	}
	xv, _ := acc.Create("codex", "x", "lm1", "")
	if _, err := acc.LaunchCommand(xv.ID); err == nil {
		t.Fatal("换行值应报错中止")
	}
	if _, err := os.Stat(filepath.Join(acc.Home, ".agentswitch", "access", "codex", xv.ID+".env")); !os.IsNotExist(err) {
		t.Fatal("报错后不得落盘")
	}
}

// TestSaveGlobalConfigRefreshesDerived 覆盖终审 Important#2：全局配置保存
// 后 live 与全部持久文件同步刷新（验收场景④——无需令牌轮换）。claude 全局
// 配置已下线（2026-11-07），本用例改走 codex；live 断言 config.toml 中的
// 模型目录键（codex live 不投影任意 env），env 落持久文件。
func TestSaveGlobalConfigRefreshesDerived(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cur, _ := acc.Create("codex", "工作", "lm1", "")
	other, _ := acc.Create("codex", "备用", "lm1", "")
	if err := acc.Apply(cur.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.LaunchCommand(cur.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.LaunchCommand(other.ID); err != nil {
		t.Fatal(err)
	}
	if err := acc.SaveGlobalConfig("codex", map[string]string{"HTTPS_PROXY": "http://p:1"}); err != nil {
		t.Fatal(err)
	}
	live, _ := os.ReadFile(filepath.Join(acc.Home, ".codex", "config.toml"))
	if !strings.Contains(string(live), "m") {
		t.Fatal("live 未刷新")
	}
	for _, id := range []string{cur.ID, other.ID} {
		b, _ := os.ReadFile(filepath.Join(acc.Home, ".agentswitch", "access", "codex", id+".env"))
		if !strings.Contains(string(b), "http://p:1") {
			t.Fatalf("持久文件 %s 未刷新全局 env", id)
		}
	}
}

// TestWritePersistQuotesEnvValues 覆盖终审 Important#4：.env 值单引号包裹，
// 空格/单引号不再被 source 的 shell 解释或截断。
func TestWritePersistQuotesEnvValues(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := a.Store().Settings().Set("access.global.codex", `{"A":"x y","B":"it's"}`); err != nil {
		t.Fatal(err)
	}
	xv, _ := acc.Create("codex", "x", "lm1", "")
	b, err := os.ReadFile(filepath.Join(acc.Home, ".agentswitch", "access", "codex", xv.ID+".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "A='x y'") {
		t.Fatalf("含空格值未加引号: %s", b)
	}
	if !strings.Contains(string(b), `B='it'\''s'`) {
		t.Fatalf("单引号未转义: %s", b)
	}
}

// TestBackupFilesAre0600 覆盖终审 Minor#7：备份含令牌，写入强制 0600。
func TestBackupFilesAre0600(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"a":1}`), 0o644) // 源 0644 也收紧
	if err := acc.backupLive("claude"); err != nil {
		t.Fatal(err)
	}
	bdir := filepath.Join(acc.Home, ".agentswitch", "backups", "claude")
	ents, _ := os.ReadDir(bdir)
	if len(ents) != 1 {
		t.Fatal(ents)
	}
	fi, err := os.Stat(filepath.Join(bdir, ents[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("备份权限 = %v，应 0600", fi.Mode().Perm())
	}
}

func TestParseAccessDocValidation(t *testing.T) {
	// 根非对象
	if _, _, err := parseAccessDoc("claude", "[]"); err == nil || !strings.Contains(err.Error(), "配置必须是 JSON 对象") {
		t.Fatalf("根非对象应报错: %v", err)
	}
	if _, _, err := parseAccessDoc("claude", "null"); err == nil || !strings.Contains(err.Error(), "配置必须是 JSON 对象") {
		t.Fatalf("null 应报错: %v", err)
	}
	// env 非对象
	if _, _, err := parseAccessDoc("claude", `{"env":[]}`); err == nil || !strings.Contains(err.Error(), "env 必须是 JSON 对象") {
		t.Fatalf("env 非对象应报错: %v", err)
	}
	// env 值非字符串
	if _, _, err := parseAccessDoc("claude", `{"env":{"A":1}}`); err == nil || !strings.Contains(err.Error(), "env 值必须是字符串") {
		t.Fatalf("env 值非字符串应报错: %v", err)
	}
	// codex 仅允许 env、models、codex 键
	if _, _, err := parseAccessDoc("codex", `{"env":{},"mcpServers":{}}`); err == nil || !strings.Contains(err.Error(), "Codex 配置仅支持 env、models、codex、model_meta 键") {
		t.Fatalf("codex 顶层键应报错: %v", err)
	}
	// 非法 appType
	if _, _, err := parseAccessDoc("gemini", `{}`); err == nil {
		t.Fatal("非法 appType 应报错")
	}
}

func TestParseAccessDocValid(t *testing.T) {
	// 空串/空文档 → 空 env
	for _, raw := range []string{"", "  ", "{}", `{"env":null}`} {
		doc, env, err := parseAccessDoc("claude", raw)
		if err != nil || len(env) != 0 || doc == nil {
			t.Fatalf("raw=%q 应得空 env: doc=%v env=%v err=%v", raw, doc, env, err)
		}
	}
	// claude 允许任意顶层键
	doc, env, err := parseAccessDoc("claude", `{"env":{"A":"1"},"mcpServers":{"x":{"command":"c"}},"language":"zh-CN"}`)
	if err != nil {
		t.Fatal(err)
	}
	if env["A"] != "1" || doc["language"] != "zh-CN" {
		t.Fatalf("doc=%v env=%v", doc, env)
	}
}

func TestPreviewDocRendersTokenAndComposes(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	raw, err := acc.PreviewDoc("claude", "lm1", `{"env":{"API_TIMEOUT_MS":"1","ANTHROPIC_AUTH_TOKEN":"x"},"language":"zh-CN"}`)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	env := doc["env"].(map[string]any)
	wantToken := Gateway{A: a}.Status().Token
	if wantToken == "" {
		t.Fatal("前提不成立：网关令牌应已生成")
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != wantToken {
		t.Fatalf("预览应渲染真实网关令牌: %v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if strings.Contains(raw, "__GATEWAY_TOKEN__") {
		t.Fatalf("预览不应再出现遮蔽占位符: %s", raw)
	}
	if env["ANTHROPIC_MODEL"] != "my-code" || env["ANTHROPIC_BASE_URL"] == "" {
		t.Fatalf("锁定键应就位: %v", env)
	}
	if env["API_TIMEOUT_MS"] != "1" {
		t.Fatalf("文档 env 应合成: %v", env)
	}
	if doc["language"] != "zh-CN" {
		t.Fatalf("顶层键应保留: %v", doc)
	}
}

func TestPreviewDocErrors(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.PreviewDoc("claude", "nope", `{}`); err == nil {
		t.Fatal("逻辑模型不存在应报错")
	}
	if _, err := acc.PreviewDoc("claude", "lm1", `{bad`); err == nil || !strings.Contains(err.Error(), "配置 JSON 格式错误") {
		t.Fatalf("非法 JSON 应报解析错误: %v", err)
	}
}

// TestPreviewCodexConfigDoesNotWrite 钉死只读预览：返回最终 config.toml 文本
// （含默认模型与目录引用），但不创建/改写 <home>/.codex 下的任何文件。
func TestPreviewCodexConfigDoesNotWrite(t *testing.T) {
	a := tempApp(t)
	lm, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	got, err := acc.PreviewCodexConfig(lm.ID, `{"models":["my-code"]}`)
	if err != nil {
		t.Fatal(err)
	}
	// go-toml v2 对普通字符串输出单引号字面量，按解析结果断言（同既有测试）。
	var cfg map[string]any
	if err := toml.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["model"] != "my-code" || cfg["model_catalog_json"] != codexModelCatalogFileName {
		t.Fatalf("%s", got)
	}
	if _, err := os.Stat(filepath.Join(acc.Home, ".codex")); !os.IsNotExist(err) {
		t.Fatal("预览不应创建 .codex 目录")
	}
	if _, err := os.Stat(filepath.Join(acc.Home, ".codex", "config.toml")); !os.IsNotExist(err) {
		t.Fatal("预览不应写 live 文件")
	}
	if _, err := os.Stat(filepath.Join(acc.Home, ".codex", codexModelCatalogFileName)); !os.IsNotExist(err) {
		t.Fatal("预览不应写模型目录文件")
	}
}

// TestPreviewCodexConfigMergesUserKeysAndToken 钉死预览内容：现有 config.toml
// 的用户键原样保留、受管键就位、provider 表带真实网关令牌；预览后原文件
// 逐字节不变（不写盘）。
func TestPreviewCodexConfigMergesUserKeysAndToken(t *testing.T) {
	a := tempApp(t)
	lm, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	original := "preferred_auth_method = \"apikey\"\n\n[mcp_servers.foo]\ncommand = \"mcp\"\n"
	cfgPath := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(cfgPath, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := acc.PreviewCodexConfig(lm.ID, `{"models":["my-code"]}`)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := toml.Unmarshal([]byte(got), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["preferred_auth_method"] != "apikey" || cfg["mcp_servers"] == nil {
		t.Fatalf("用户键应保留: %s", got)
	}
	if cfg["model"] != "my-code" || cfg["model_provider"] != "agentswitch" ||
		cfg["model_catalog_json"] != codexModelCatalogFileName ||
		cfg["disable_response_storage"] != true || cfg["web_search"] != "disabled" {
		t.Fatalf("受管键应就位: %s", got)
	}
	mp := cfg["model_providers"].(map[string]any)["agentswitch"].(map[string]any)
	wantToken := Gateway{A: a}.Status().Token
	if wantToken == "" {
		t.Fatal("前提不成立：网关令牌应已生成")
	}
	if mp["experimental_bearer_token"] != wantToken || mp["wire_api"] != "responses" {
		t.Fatalf("provider 表应带真实网关令牌与 responses 线路: %v", mp)
	}
	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != original {
		t.Fatalf("预览不应改写现有 config.toml: %s", after)
	}
}

// TestPreviewCodexConfigErrors 钉死中文错误：逻辑模型不存在、配置 JSON 非法、
// 模型列表语义非法（首项不是绑定模型）均在预览阶段返回，不落盘。
func TestPreviewCodexConfigErrors(t *testing.T) {
	a := tempApp(t)
	lm, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm2", Name: "other", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := acc.PreviewCodexConfig("nope", `{}`); err == nil || !strings.Contains(err.Error(), "逻辑模型不存在") {
		t.Fatalf("逻辑模型不存在应报中文错误: %v", err)
	}
	if _, err := acc.PreviewCodexConfig(lm.ID, `{bad`); err == nil || !strings.Contains(err.Error(), "配置 JSON 格式错误") {
		t.Fatalf("非法 JSON 应报解析错误: %v", err)
	}
	if _, err := acc.PreviewCodexConfig(lm.ID, `{"models":["other"]}`); err == nil ||
		!strings.Contains(err.Error(), "第一个模型必须是绑定的默认模型") {
		t.Fatalf("首项非绑定模型应报中文错误: %v", err)
	}
}

// TestRebaseAccessDoc 覆盖 Important#1 的合成基线重定位纯函数：值等于旧基线
// 的继承键删除（切换模型/更新全局后不再冻结）、不同值与其他键原样保留；
// 解析失败返回 error（调用方保留原文）。
func TestRebaseAccessDoc(t *testing.T) {
	in := `{"env":{"A":"1","B":"2","C":"keep"},"language":"zh-CN"}`
	got, err := RebaseAccessDoc("claude", in, map[string]string{"A": "1", "B": "different", "D": "x"})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(got), &doc); err != nil {
		t.Fatal(err)
	}
	env, ok := doc["env"].(map[string]any)
	if !ok {
		t.Fatalf("env 非对象: %s", got)
	}
	if _, still := env["A"]; still {
		t.Fatalf("等于旧基线的键应删除: %s", got)
	}
	if env["B"] != "2" || env["C"] != "keep" {
		t.Fatalf("不同值/其他键应保留: %s", got)
	}
	if doc["language"] != "zh-CN" {
		t.Fatalf("顶层键应保留: %s", got)
	}
	if _, err := RebaseAccessDoc("claude", `{bad`, map[string]string{"A": "1"}); err == nil {
		t.Fatal("非法 JSON 应返回 error")
	}
}

// TestPreviewRebaseSaveRoundTrip 覆盖 Important#1/#2 端到端回归：旧预览里的
// 派生值（SMALL_FAST=旧模型、全局 HTTPS_PROXY=旧值）在切换模型/更新全局后
// 经重定位不再被保存归一化冻结；真正的定制键 CUSTOM 保留。claude 全局配置
// 已下线（2026-11-07），全局重定位场景改走 codex，claude 段只守模型切换。
func TestPreviewRebaseSaveRoundTrip(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	for _, lm := range []storage.LogicalModel{
		{ID: "lm1", Name: "model-a", Enabled: true},
		{ID: "lm2", Name: "model-b", Enabled: true},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	previewA, err := acc.PreviewDoc("claude", "lm1", `{"env":{"CUSTOM":"x"}}`)
	if err != nil {
		t.Fatal(err)
	}
	// 主模型 A → B：按旧模型的 SMALL_FAST 派生值重定位。
	rebased, err := RebaseAccessDoc("claude", previewA,
		map[string]string{"ANTHROPIC_SMALL_FAST_MODEL": "model-a"})
	if err != nil {
		t.Fatal(err)
	}
	previewB, err := acc.PreviewDoc("claude", "lm2", rebased)
	if err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "cfg", "lm2", previewB)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := a.Store().AccessConfigs().Get(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(stored.Settings), &doc); err != nil {
		t.Fatalf("存储文档非法: %v: %s", err, stored.Settings)
	}
	if _, frozen := doc.Env["ANTHROPIC_SMALL_FAST_MODEL"]; frozen {
		t.Fatalf("旧模型派生值被冻结: %s", stored.Settings)
	}
	if doc.Env["CUSTOM"] != "x" {
		t.Fatalf("定制键丢失: %s", stored.Settings)
	}

	// 全局 HTTPS_PROXY p1 → p2（codex 仍保留全局配置）：按保存前的全局值重定位，
	// 旧全局派生值不得在保存归一化中被冻结；定制键 CUSTOM2 保留。
	if err := acc.SaveGlobalConfig("codex", map[string]string{"HTTPS_PROXY": "p1"}); err != nil {
		t.Fatal(err)
	}
	codexPreview, err := acc.PreviewDoc("codex", "lm1", `{"env":{"CUSTOM2":"y"}}`)
	if err != nil {
		t.Fatal(err)
	}
	if err := acc.SaveGlobalConfig("codex", map[string]string{"HTTPS_PROXY": "p2"}); err != nil {
		t.Fatal(err)
	}
	codexRebased, err := RebaseAccessDoc("codex", codexPreview, map[string]string{"HTTPS_PROXY": "p1"})
	if err != nil {
		t.Fatal(err)
	}
	cv, err := acc.Create("codex", "cfg", "lm1", codexRebased)
	if err != nil {
		t.Fatal(err)
	}
	codexStored, err := a.Store().AccessConfigs().Get(cv.ID)
	if err != nil {
		t.Fatal(err)
	}
	var codexDoc struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(codexStored.Settings), &codexDoc); err != nil {
		t.Fatalf("存储文档非法: %v: %s", err, codexStored.Settings)
	}
	if _, frozen := codexDoc.Env["HTTPS_PROXY"]; frozen {
		t.Fatalf("旧全局值被冻结: %s", codexStored.Settings)
	}
	if codexDoc.Env["CUSTOM2"] != "y" {
		t.Fatalf("定制键丢失: %s", codexStored.Settings)
	}
}

// TestParseAccessDocPreservesLargeIntegers 覆盖 Minor#3：UseNumber 解码后
// 大整数往返（parse + marshal）保持原值，不丢精度。
func TestParseAccessDocPreservesLargeIntegers(t *testing.T) {
	doc, _, err := parseAccessDoc("claude", `{"threads":12345678901234567890}`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "12345678901234567890") {
		t.Fatalf("大整数精度丢失: %s", b)
	}
	// 尾随垃圾/多余 JSON 值拒绝。
	for _, raw := range []string{`{} {}`, `{"a":1}x`} {
		if _, _, err := parseAccessDoc("claude", raw); err == nil ||
			!strings.Contains(err.Error(), "配置 JSON 格式错误") {
			t.Fatalf("raw=%q 应报格式错误: %v", raw, err)
		}
	}
}

// 主模型 1M 声明（2026-10-21）：文档 env 的 ANTHROPIC_MODEL 允许
// 「绑定模型名[1M]」——合成保留后缀（小写规范为 [1M]），基名不匹配回写纯名。
func TestComposeAccessEnvMainModelOneM(t *testing.T) {
	st := Status{Port: 17890, Token: "tok"}
	cases := []struct{ doc, want string }{
		{"my-code[1M]", "my-code[1M]"},
		{"my-code[1m]", "my-code[1M]"},
		{"my-code", "my-code"},
		{"other[1M]", "my-code"},
	}
	for _, c := range cases {
		env, err := composeAccessEnv("claude", st, "my-code", nil,
			map[string]string{"ANTHROPIC_MODEL": c.doc})
		if err != nil {
			t.Fatal(err)
		}
		if env["ANTHROPIC_MODEL"] != c.want {
			t.Fatalf("doc=%q got=%q want=%q", c.doc, env["ANTHROPIC_MODEL"], c.want)
		}
	}
}

// 保存归一化：半锁定主模型保留「绑定模型名[1M]」（规范为 [1M]），纯名视作
// 未定制剔除，基名不匹配剔除。
func TestNormalizeAccessEnvKeepsMainModelOneM(t *testing.T) {
	st := Status{Port: 17890, Token: "tok"}
	got, err := normalizeAccessEnv("claude", st, "my-code", nil,
		map[string]string{"ANTHROPIC_MODEL": "my-code[1m]"})
	if err != nil {
		t.Fatal(err)
	}
	if got["ANTHROPIC_MODEL"] != "my-code[1M]" {
		t.Fatalf("got=%v", got)
	}
	for _, v := range []string{"my-code", "other[1M]"} {
		got, err := normalizeAccessEnv("claude", st, "my-code", nil,
			map[string]string{"ANTHROPIC_MODEL": v})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got["ANTHROPIC_MODEL"]; ok {
			t.Fatalf("doc=%q 应剔除，got=%v", v, got)
		}
	}
}

// accessEnvDoc 把 env map 包装为最小文档 JSON（构造 Create/Update 最小文档入参）。
func accessEnvDoc(env map[string]string) string {
	if env == nil {
		env = map[string]string{}
	}
	b, err := json.Marshal(struct {
		Env map[string]string `json:"env"`
	}{Env: env})
	if err != nil {
		return "{}" // map[string]string 不可能 marshal 失败；防御
	}
	return string(b)
}

// 默认模型的 ContextWindow>0 时投影 1M 上下文与 90% 自动压缩阈值到顶层
// （spec 2026-10-27 §4.5）：模型名取 models[0]，两者均为整数。
func TestProjectCodexContextWindow(t *testing.T) {
	a := tempApp(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{
		ID: "lm1", Name: "my-code", Enabled: true, ContextWindow: 1000000,
	}); err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if err := acc.writeCodexLive(map[string]string{"OPENAI_BASE_URL": "u", "OPENAI_API_KEY": "t"},
		"my-code", map[string]any{"models": []any{"my-code"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(acc.Home, ".codex", "config.toml"))
	var cfg map[string]any
	if err := toml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["model_context_window"] != int64(1000000) ||
		cfg["model_auto_compact_token_limit"] != int64(900000) {
		t.Fatalf("%v", cfg)
	}
}

// [agents] 子代理受管键（spec 2026-10-27 §4.3）：subagent_model/effort 非空
// 时分别写 default_subagent_model / default_subagent_reasoning_effort。
func TestProjectCodexSubagentKeys(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "my-code")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	doc := map[string]any{"models": []any{"my-code"}, "codex": map[string]any{
		"subagent_model": "my-code", "subagent_reasoning_effort": "high",
	}}
	if err := acc.writeCodexLive(map[string]string{"OPENAI_BASE_URL": "u", "OPENAI_API_KEY": "t"},
		"my-code", doc); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(acc.Home, ".codex", "config.toml"))
	var cfg map[string]any
	if err := toml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	agents, ok := cfg["agents"].(map[string]any)
	if !ok {
		t.Fatalf("agents 表缺失: %s", b)
	}
	if agents["default_subagent_model"] != "my-code" ||
		agents["default_subagent_reasoning_effort"] != "high" {
		t.Fatalf("%v", agents)
	}
}

// 空值删除两个受管键，但保留 [agents] 下的其他用户键（spec 2026-10-27 §4.3）。
func TestProjectCodexSubagentEmptyPreservesOtherAgentsKeys(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "my-code")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(
		"[agents]\ndefault_subagent_model = \"old\"\ndefault_subagent_reasoning_effort = \"low\"\nuser_key = \"keep\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := acc.writeCodexLive(map[string]string{"OPENAI_BASE_URL": "u", "OPENAI_API_KEY": "t"},
		"my-code", map[string]any{"models": []any{"my-code"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	var cfg map[string]any
	if err := toml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	agents, ok := cfg["agents"].(map[string]any)
	if !ok {
		t.Fatalf("agents 表不应被删除（仍有用户键）: %s", b)
	}
	if _, ok := agents["default_subagent_model"]; ok {
		t.Fatalf("default_subagent_model 应删除: %v", agents)
	}
	if _, ok := agents["default_subagent_reasoning_effort"]; ok {
		t.Fatalf("default_subagent_reasoning_effort 应删除: %v", agents)
	}
	if agents["user_key"] != "keep" {
		t.Fatalf("其他 agents 键应保留: %v", agents)
	}
}

// 删除受管键后 [agents] 变空则整表删除，不留下空表。
func TestProjectCodexSubagentEmptyTableRemoved(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "my-code")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(
		"[agents]\ndefault_subagent_model = \"old\"\ndefault_subagent_reasoning_effort = \"low\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := acc.writeCodexLive(map[string]string{"OPENAI_BASE_URL": "u", "OPENAI_API_KEY": "t"},
		"my-code", map[string]any{"models": []any{"my-code"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	var cfg map[string]any
	if err := toml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg["agents"]; ok {
		t.Fatalf("空 agents 表应删除: %s", b)
	}
}

// [agents] 已存在但不是表时报中文错误，不静默覆盖用户配置。
func TestProjectCodexSubagentNonTableErrors(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "my-code")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte("agents = \"x\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := acc.writeCodexLive(map[string]string{"OPENAI_BASE_URL": "u", "OPENAI_API_KEY": "t"},
		"my-code", map[string]any{"models": []any{"my-code"}})
	if err == nil || !strings.Contains(err.Error(), "agents 不是表") {
		t.Fatalf("应返回中文表结构错误: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if string(b) != "agents = \"x\"\n" {
		t.Fatalf("失败后不得改动原文件: %s", b)
	}
}

// 投影是唯一 live 写路径，可能读到旧库文档：子代理模型不在解析后的模型
// 列表中时报中文错误，绝不把无效值写进 config.toml。
func TestProjectCodexSubagentInvalidModelErrors(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "my-code")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	doc := map[string]any{"models": []any{"my-code"}, "codex": map[string]any{
		"subagent_model": "ghost",
	}}
	err := acc.writeCodexLive(map[string]string{"OPENAI_BASE_URL": "u", "OPENAI_API_KEY": "t"},
		"my-code", doc)
	if err == nil || !strings.Contains(err.Error(), "子代理模型 ghost 不在模型列表中") {
		t.Fatalf("应返回中文模型校验错误: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(acc.Home, ".codex", "config.toml")); !os.IsNotExist(statErr) {
		t.Fatalf("校验失败不应写 config.toml: %v", statErr)
	}
}

// 思考等级不在白名单时报中文错误，不把 Codex 不认识的毒值写进 [agents]。
func TestProjectCodexSubagentInvalidEffortErrors(t *testing.T) {
	a := tempApp(t)
	seedLogicalModel(t, a, "my-code")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	doc := map[string]any{"models": []any{"my-code"}, "codex": map[string]any{
		"subagent_reasoning_effort": "bogus",
	}}
	err := acc.writeCodexLive(map[string]string{"OPENAI_BASE_URL": "u", "OPENAI_API_KEY": "t"},
		"my-code", doc)
	if err == nil || !strings.Contains(err.Error(), "未知思考等级") {
		t.Fatalf("应返回中文思考等级错误: %v", err)
	}
}

// 默认模型 ContextWindow==0 表示未声明：不写也不删这两个键，保留用户已有
// 设置（spec 2026-10-27 §4.5；禁止把 0 写成 0 或清空用户值）。
func TestProjectCodexContextWindowZeroPreservesExisting(t *testing.T) {
	a := tempApp(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{
		ID: "lm1", Name: "my-code", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(
		"model_context_window = 200000\nmodel_auto_compact_token_limit = 180000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := acc.writeCodexLive(map[string]string{"OPENAI_BASE_URL": "u", "OPENAI_API_KEY": "t"},
		"my-code", map[string]any{"models": []any{"my-code"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	var cfg map[string]any
	if err := toml.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["model_context_window"] != int64(200000) ||
		cfg["model_auto_compact_token_limit"] != int64(180000) {
		t.Fatalf("%v", cfg)
	}
}

func TestUpdateCurrentMarksNeedsApplyAndKeepsLive(t *testing.T) {
	a := tempApp(t)
	home := t.TempDir()
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "工作", "lm1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	livePath := filepath.Join(home, ".claude", "settings.json")
	if err := os.WriteFile(livePath, []byte(`{"old":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}

	if err := acc.Update(v.ID, "工作", "lm1", `{"env":{"CUSTOM":"x"}}`); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(livePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("仅保存不得改写 live：before=%s after=%s", before, after)
	}
	list := acc.List("claude")
	if len(list) != 1 || !list[0].NeedsApply {
		t.Fatalf("current 配置内容变化后应标记待应用：%+v", list)
	}
}

func TestRegenerateDerivedSkipsLiveWhenPending(t *testing.T) {
	a := tempApp(t)
	home := t.TempDir()
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	livePath := filepath.Join(home, ".claude", "settings.json")
	os.WriteFile(livePath, []byte(`{"old":1}`), 0o600)
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(livePath)
	if err := acc.Update(v.ID, "工作", "lm1", `{"env":{"CUSTOM":"x"}}`); err != nil {
		t.Fatal(err)
	}
	errs := acc.RegenerateDerived()
	if len(errs) == 0 || !strings.Contains(errs[0].Error(), "未应用") {
		t.Fatalf("应返回待应用警告：%v", errs)
	}
	after, _ := os.ReadFile(livePath)
	if string(before) != string(after) {
		t.Fatal("待应用时 RegenerateDerived 不得改写 live")
	}
}

func TestRegenerateDerivedBacksUpBeforeLive(t *testing.T) {
	a := tempApp(t)
	home := t.TempDir()
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "old-name", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	livePath := filepath.Join(home, ".claude", "settings.json")
	os.WriteFile(livePath, []byte(`{"old":1}`), 0o600)
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(livePath)

	lm, _ := a.Store().LogicalModels().Get("lm1")
	lm.Name = "new-name"
	if _, err := a.Store().LogicalModels().Save(lm); err != nil {
		t.Fatal(err)
	}
	if errs := acc.RegenerateDerived(); len(errs) != 0 {
		t.Fatalf("系统级刷新不应失败：%v", errs)
	}
	after, _ := os.ReadFile(livePath)
	if !strings.Contains(string(after), "new-name") {
		t.Fatalf("live 应包含新模型名：%s", after)
	}

	backupDir := filepath.Join(home, ".agentswitch", "backups", "claude")
	ents, err := os.ReadDir(backupDir)
	if err != nil || len(ents) == 0 {
		t.Fatalf("应产生备份：%v", err)
	}
	latest := ents[len(ents)-1].Name()
	b, _ := os.ReadFile(filepath.Join(backupDir, latest))
	if string(b) != string(before) {
		t.Fatalf("最新备份应等于写入前 live：backup=%s before=%s", b, before)
	}
}

func TestApplyClearsNeedsApplyAndBackupIsPreSave(t *testing.T) {
	a := tempApp(t)
	home := t.TempDir()
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	livePath := filepath.Join(home, ".claude", "settings.json")
	os.WriteFile(livePath, []byte(`{"old":1}`), 0o600)
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(livePath)

	if err := acc.Update(v.ID, "工作", "lm1", `{"env":{"CUSTOM":"x"}}`); err != nil {
		t.Fatal(err)
	}
	if got := acc.List("claude"); len(got) != 1 || !got[0].NeedsApply {
		t.Fatalf("保存后应待应用：%+v", got)
	}
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(livePath)
	if !strings.Contains(string(after), "CUSTOM") {
		t.Fatalf("应用后 live 应包含新值：%s", after)
	}
	if got := acc.List("claude"); len(got) != 1 || got[0].NeedsApply {
		t.Fatalf("应用后不应待应用：%+v", got)
	}
	backupDir := filepath.Join(home, ".agentswitch", "backups", "claude")
	ents, err := os.ReadDir(backupDir)
	if err != nil || len(ents) == 0 {
		t.Fatalf("应有备份：%v", err)
	}
	latest := ents[len(ents)-1].Name()
	b, _ := os.ReadFile(filepath.Join(backupDir, latest))
	if string(b) != string(before) {
		t.Fatalf("最新备份应等于保存并应用前的 live：backup=%s before=%s", b, before)
	}
}

func TestListBackupsAndRestoreSelected(t *testing.T) {
	a := tempApp(t)
	home := t.TempDir()
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", "")
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	livePath := filepath.Join(home, ".claude", "settings.json")
	os.WriteFile(livePath, []byte(`{"v":1}`), 0o600)
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	backups, err := acc.ListBackups("claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) < 2 {
		t.Fatalf("backups = %+v", backups)
	}
	oldest := backups[len(backups)-1]
	if err := acc.RestoreBackup("claude", oldest.Timestamp); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(livePath)
	if !strings.Contains(string(b), `"v":1`) {
		t.Fatalf("restored live = %s", b)
	}
}
