package bridge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"AgentSwitch/internal/storage"
)

// TestMain 用固定模板替换真实 Codex 可执行文件调用：离线测试不依赖本机安装。
func TestMain(m *testing.M) {
	codexModelCatalogTemplateLoader = func() (map[string]any, error) {
		return testCodexCatalogTemplate(), nil
	}
	os.Exit(m.Run())
}

func testCodexCatalogTemplate() map[string]any {
	return map[string]any{
		"slug":                  "template",
		"display_name":          "Template",
		"description":           "template",
		"base_instructions":     "You are a test coding agent.",
		"shell_type":            "shell_command",
		"visibility":            "list",
		"priority":              9999,
		"upgrade":               "gpt-next",
		"supported_in_api":      true,
		"support_verbosity":     true,
		"experimental_tools":    []any{},
		"truncation_policy":     map[string]any{"limit": 10000, "mode": "tokens"},
		"model_messages":        map[string]any{"instructions_template": "You are a test coding agent."},
		"context_window":        128000,
		"max_context_window":    128000,
		"default_verbosity":     "low",
		"apply_patch_tool_type": "freeform",
	}
}

// TestCodexCatalogIncludesEnabledModelsBoundFirst 钉死模型目录内容：绑定模型
// 排第一（默认），其余启用的逻辑模型按名排序，禁用的不出现；条目克隆模板并
// 抹掉 upgrade，避免桌面版提示升级到 GPT 官方模型。
func TestCodexCatalogIncludesEnabledModelsBoundFirst(t *testing.T) {
	a := tempApp(t)
	for _, lm := range []storage.LogicalModel{
		{ID: "lm-bound", Name: "my-code", Enabled: true},
		{ID: "lm-test", Name: "test", Enabled: true},
		{ID: "lm-off", Name: "off", Enabled: false},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	if err := acc.writeCodexLive(map[string]string{
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "tok"}, "my-code"); err != nil {
		t.Fatal(err)
	}

	cfgBytes, err := os.ReadFile(filepath.Join(acc.Home, ".codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err := toml.Unmarshal(cfgBytes, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["model_catalog_json"] != codexModelCatalogFileName {
		t.Fatalf("model_catalog_json = %v", cfg["model_catalog_json"])
	}

	catalogPath := filepath.Join(acc.Home, ".codex", codexModelCatalogFileName)
	catalogBytes, err := os.ReadFile(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	var catalog struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(catalogBytes, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Models) != 2 {
		t.Fatalf("目录条目 = %d，应为 2: %s", len(catalog.Models), catalogBytes)
	}
	if catalog.Models[0]["slug"] != "my-code" || catalog.Models[1]["slug"] != "test" {
		t.Fatalf("绑定模型应排第一: %s", catalogBytes)
	}
	for i, m := range catalog.Models {
		if m["display_name"] != m["slug"] {
			t.Fatalf("display_name 未替换: %v", m)
		}
		if m["visibility"] != "list" {
			t.Fatalf("visibility 未设置为 list: %v", m)
		}
		if _, ok := m["upgrade"]; ok {
			t.Fatalf("upgrade 应被抹掉: %v", m)
		}
		if m["base_instructions"] != "You are a test coding agent." {
			t.Fatalf("模板字段未克隆: %v", m)
		}
		wantPriority := float64(1000 + i)
		if m["priority"] != wantPriority {
			t.Fatalf("priority = %v，应为 %v", m["priority"], wantPriority)
		}
	}
	fi, err := os.Stat(catalogPath)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("目录文件权限 = %v，应为 0600", fi.Mode().Perm())
	}
}

// TestCodexCatalogFailureAbortsWithoutWriting 模板不可用时必须中止，config.toml
// 保持原样且不留下半份目录文件。
func TestCodexCatalogFailureAbortsWithoutWriting(t *testing.T) {
	acc := AccessConfigs{A: tempApp(t), Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := "model = \"keep\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	old := codexModelCatalogTemplateLoader
	codexModelCatalogTemplateLoader = func() (map[string]any, error) {
		return nil, errors.New("no template")
	}
	defer func() { codexModelCatalogTemplateLoader = old }()

	if err := acc.writeCodexLive(map[string]string{"OPENAI_API_KEY": "x"}, "m"); err == nil {
		t.Fatal("模板缺失应报错")
	}
	got, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sentinel {
		t.Fatalf("失败后不得改动 config.toml: %s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, codexModelCatalogFileName)); !os.IsNotExist(err) {
		t.Fatal("失败后不应留下目录文件")
	}
}

// TestParseCodexCatalogTemplatePicksEntryWithInstructions 覆盖内置模型输出解析：
// 跳过没有提示词字段的条目（如 codex-auto-review），取第一条可用模板。
func TestParseCodexCatalogTemplatePicksEntryWithInstructions(t *testing.T) {
	raw := []byte(`{"models":[{"slug":"no-instructions"},{"slug":"ok","base_instructions":"hi"}]}`)
	entry, err := parseCodexCatalogTemplate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if entry["slug"] != "ok" {
		t.Fatalf("应选带提示词的条目: %v", entry)
	}
}

// TestLiveCodexCatalogTemplate 是可选的本机集成检查：真实定位 Codex 可执行
// 文件并跑 `debug models --bundled`。常规测试不依赖本机安装，默认跳过。
func TestLiveCodexCatalogTemplate(t *testing.T) {
	if os.Getenv("AGENTSWITCH_LIVE_CODEX") != "1" {
		t.Skip("set AGENTSWITCH_LIVE_CODEX=1 to run")
	}
	entry, err := loadCodexModelCatalogTemplate()
	if err != nil {
		t.Fatal(err)
	}
	if !codexCatalogEntryHasInstructions(entry) {
		t.Fatalf("模板缺少提示词: %v", entry)
	}
	t.Logf("template slug=%v display=%v", entry["slug"], entry["display_name"])
}

// TestLoadLocalCodexCatalogTemplate 覆盖没有 Codex 可执行文件时的本地兜底：
// 复用已有目录（cc-switch 或上一次 AgentSwitch 生成的）的第一条模板。
func TestLoadLocalCodexCatalogTemplate(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"models":[{"slug":"old","base_instructions":"from local catalog"}]}`)
	if err := os.WriteFile(filepath.Join(dir, "cc-switch-model-catalog.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	entry, err := loadLocalCodexCatalogTemplate(home)
	if err != nil {
		t.Fatal(err)
	}
	if entry["base_instructions"] != "from local catalog" {
		t.Fatalf("本地目录模板未读到: %v", entry)
	}
}
