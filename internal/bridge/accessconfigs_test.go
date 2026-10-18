package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"AgentSwitch/internal/storage"
)

func TestClaudeLiveProjectionPreservesAndMerges(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"permissions":{"allow":["Bash"]},"env":{"FOO":"bar","ANTHROPIC_MODEL":"old"}}`), 0o600)
	if err := acc.writeClaudeLive(map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:17890", "ANTHROPIC_MODEL": "new", "HTTPS_PROXY": "http://p:1"}); err != nil {
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
	if err := acc.writeClaudeLive(map[string]string{"ANTHROPIC_MODEL": "x"}); err == nil {
		t.Fatal("非法 JSON 应中止")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(b) != "{broken" {
		t.Fatal("失败后不得改动原文件")
	}
}

func TestCodexLiveProjectionPreservesAndMerges(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("preferred_auth_method = \"apikey\"\n\n[mcp_servers.foo]\ncommand = \"mcp\"\n"), 0o600)
	if err := acc.writeCodexLive(map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "tok"}, "my-code"); err != nil {
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
	if mp["wire_api"] != "chat" || mp["requires_openai_auth"] != false {
		t.Fatal(mp)
	}
	if mp["base_url"] != "http://127.0.0.1:17890/v1" {
		t.Fatal(mp)
	}
	ab, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	var auth map[string]any
	json.Unmarshal(ab, &auth)
	if auth["OPENAI_API_KEY"] != "tok" {
		t.Fatal(string(ab))
	}
}

func TestCodexLiveProjectionInvalidTOMLAborts(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = "), 0o600)
	if err := acc.writeCodexLive(map[string]string{"OPENAI_API_KEY": "x"}, "m"); err == nil {
		t.Fatal("非法 TOML 应中止")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if string(b) != "model = " {
		t.Fatal("失败后不得改动")
	}
}

// 双文件一致性：auth.json 非法时 config.toml 也绝不落盘（一败俱不写）。
func TestCodexLiveProjectionInvalidAuthJSONAbortsBoth(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte("model = \"keep\"\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "auth.json"), []byte("{broken"), 0o600)
	if err := acc.writeCodexLive(map[string]string{"OPENAI_API_KEY": "x"}, "m"); err == nil {
		t.Fatal("非法 auth.json 应中止")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if string(b) != "model = \"keep\"\n" {
		t.Fatal("config.toml 失败后不得改动")
	}
	ab, _ := os.ReadFile(filepath.Join(dir, "auth.json"))
	if string(ab) != "{broken" {
		t.Fatal("auth.json 失败后不得改动")
	}
}

func TestBackupLiveCopiesAndRotates(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".claude")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"a":1}`), 0o600)
	for i := 0; i < 12; i++ {
		if err := acc.backupLive("claude"); err != nil { t.Fatal(err) }
		time.Sleep(2 * time.Millisecond) // 时间戳文件名去重
	}
	bdir := filepath.Join(acc.Home, ".agentswitch", "backups", "claude")
	ents, _ := os.ReadDir(bdir)
	if len(ents) != 10 { t.Fatalf("备份份数 = %d，应为 10", len(ents)) }
	b, err := os.ReadFile(filepath.Join(bdir, ents[0].Name()))
	if err != nil || string(b) != `{"a":1}` { t.Fatalf("%v %s", err, b) }
}

func TestBackupLiveSkipsMissingSource(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	if err := acc.backupLive("claude"); err != nil { t.Fatal(err) } // 无源文件不报错、不建目录
	if _, err := os.Stat(filepath.Join(acc.Home, ".agentswitch", "backups", "claude")); !os.IsNotExist(err) { t.Fatal("不应创建备份目录") }
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

func TestComposeForCodexResolvesModelAndErrorsOnMissing(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	// 建一个逻辑模型（经 storage 直插，避开 bridge 依赖环）
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	_ = a.Store().Settings().Set("access.global.codex", `{"HTTPS_PROXY":"http://p:1"}`)
	env, model, err := acc.composeFor(storage.AccessConfig{AppType: "codex", LogicalModelID: "lm1"})
	if err != nil {
		t.Fatal(err)
	}
	if model != "my-code" {
		t.Fatal(model)
	}
	if env["OPENAI_BASE_URL"] == "" || env["OPENAI_API_KEY"] == "" || env["HTTPS_PROXY"] != "http://p:1" {
		t.Fatal(env)
	}
	if _, _, err := acc.composeFor(storage.AccessConfig{AppType: "codex", LogicalModelID: "missing"}); err == nil {
		t.Fatal("缺失逻辑模型应报错")
	}
}

func TestAccessConfigsCRUDAndView(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "工作", "lm1", map[string]string{"A": "1"})
	if err != nil {
		t.Fatal(err)
	}
	if v.LogicalModelName != "my-code" {
		t.Fatal(v.LogicalModelName)
	}
	if _, err := acc.Create("claude", "工作", "lm1", nil); err == nil {
		t.Fatal("重名应报中文错误")
	}
	if err := acc.Update(v.ID, "个人", "lm1", nil); err != nil {
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

func TestAccessConfigsGlobalConfig(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if err := acc.SaveGlobalConfig("claude", map[string]string{"K": "V"}); err != nil {
		t.Fatal(err)
	}
	g, err := acc.GlobalConfig("claude")
	if err != nil || g["K"] != "V" {
		t.Fatalf("%v %v", g, err)
	}
	if err := acc.SaveGlobalConfig("gemini", nil); err == nil {
		t.Fatal("白名单外 appType 应报错")
	}
}
