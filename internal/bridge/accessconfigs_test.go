package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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
	// Create 落库的是整份 settings 文档：view 须取回覆盖项、composeFor 须能
	// 从文档提取 env（Task 1 机械适配的行为等价性）。
	if v.Overrides["A"] != "1" {
		t.Fatalf("view 覆盖项未回读: %#v", v.Overrides)
	}
	stored, err := a.Store().AccessConfigs().Get(v.ID)
	if err != nil {
		t.Fatal(err)
	}
	env, _, err := acc.composeFor(stored)
	if err != nil || env["A"] != "1" {
		t.Fatalf("composeFor 应取回文档 env 覆盖项: %v %v", env, err)
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

func TestApplyWritesLiveAndSetsCurrent(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "工作", "lm1", nil)
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
	v, _ := acc.Create("claude", "工作", "lm1", nil)
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
	v, err := acc.Create("claude", "工作", "lm1", nil)
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

func TestWritePersistAndLaunchCommand(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cv, _ := acc.Create("claude", "工作", "lm1", nil)
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

	xv, _ := acc.Create("codex", "x", "lm1", nil)
	cmd2, err := acc.LaunchCommand(xv.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd2, "codex -m \"my-code\"") || !strings.Contains(cmd2, ".env") {
		t.Fatal(cmd2)
	}
}

func TestGatewayRegenerateTokenRefreshesDerivedFiles(t *testing.T) {
	a := tempApp(t)
	// 钩子在 Gateway 内构造 AccessConfigs（Home=""），经 os.UserHomeDir 解析；
	// t.Setenv 让两边解析到同一临时目录，避免触碰真实用户目录。
	home := t.TempDir()
	t.Setenv("HOME", home)
	acc := AccessConfigs{A: a, Home: home}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", nil)
	if err := acc.Apply(v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.LaunchCommand(v.ID); err != nil { // 持久文件由 LaunchCommand 生成
		t.Fatal(err)
	}
	oldTok, _ := a.Store().Settings().Get("auth.gateway_token")
	p := filepath.Join(acc.Home, ".agentswitch", "access", "claude", v.ID+".json")
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), oldTok) {
		t.Fatal("前置：旧令牌应在持久文件中")
	}
	if _, err := (Gateway{A: a}).RegenerateToken(); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(p)
	newTok, _ := a.Store().Settings().Get("auth.gateway_token")
	if !strings.Contains(string(b2), newTok) || strings.Contains(string(b2), oldTok) {
		t.Fatal("持久文件未刷新令牌")
	}
	live, _ := os.ReadFile(filepath.Join(acc.Home, ".claude", "settings.json"))
	if !strings.Contains(string(live), newTok) {
		t.Fatal("live 文件未刷新令牌")
	}
}

func TestOpenTerminalAssemblesScript(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	v, _ := acc.Create("claude", "工作", "lm1", nil)
	var got string
	old := openTerminalExec
	openTerminalExec = func(script string) error { got = script; return nil }
	defer func() { openTerminalExec = old }()
	if err := acc.OpenTerminal(v.ID); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, `tell application "Terminal" to do script "claude --settings`) {
		t.Fatal(got)
	}
	if err := acc.OpenTerminal("missing"); err == nil {
		t.Fatal("不存在的配置应报错")
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
	xv, _ := acc.Create("codex", "x", "lm1", nil)
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
	xv, _ := acc.Create("codex", "x", "lm1", nil)
	if _, err := acc.LaunchCommand(xv.ID); err == nil {
		t.Fatal("换行值应报错中止")
	}
	if _, err := os.Stat(filepath.Join(acc.Home, ".agentswitch", "access", "codex", xv.ID+".env")); !os.IsNotExist(err) {
		t.Fatal("报错后不得落盘")
	}
}

// TestSaveGlobalConfigRefreshesDerived 覆盖终审 Important#2：全局配置保存
// 后 live 与全部持久文件同步刷新（验收场景④——无需令牌轮换）。
func TestSaveGlobalConfigRefreshesDerived(t *testing.T) {
	a := tempApp(t)
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "m", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	cur, _ := acc.Create("claude", "工作", "lm1", nil)
	other, _ := acc.Create("claude", "备用", "lm1", nil)
	if err := acc.Apply(cur.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.LaunchCommand(cur.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.LaunchCommand(other.ID); err != nil {
		t.Fatal(err)
	}
	if err := acc.SaveGlobalConfig("claude", map[string]string{"HTTPS_PROXY": "http://p:1"}); err != nil {
		t.Fatal(err)
	}
	live, _ := os.ReadFile(filepath.Join(acc.Home, ".claude", "settings.json"))
	if !strings.Contains(string(live), "http://p:1") {
		t.Fatal("live 未刷新全局 env")
	}
	for _, id := range []string{cur.ID, other.ID} {
		b, _ := os.ReadFile(filepath.Join(acc.Home, ".agentswitch", "access", "claude", id+".json"))
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
	xv, _ := acc.Create("codex", "x", "lm1", nil)
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
