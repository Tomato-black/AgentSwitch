package bridge

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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
		"OPENAI_BASE_URL": "http://127.0.0.1:17890/v1", "OPENAI_API_KEY": "tok"}, "my-code", nil); err != nil {
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
	a := tempApp(t)
	seedLogicalModel(t, a, "m")
	acc := AccessConfigs{A: a, Home: t.TempDir()}
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

	if err := acc.writeCodexLive(map[string]string{"OPENAI_API_KEY": "x"}, "m", nil); err == nil {
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

// TestBuildCodexCatalogUsesMetadata 钉死目录条目的元数据投影：显示名、
// 描述、上下文窗口、思考等级、默认等级与自动审批覆盖（spec §4.4）。
func TestBuildCodexCatalogUsesMetadata(t *testing.T) {
	b, err := buildCodexModelCatalog(testCodexCatalogTemplate(), []codexCatalogModel{
		{Name: "my-code", DisplayName: "My Code", Description: "主模型", ContextWindow: 1000000,
			ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high"},
		{Name: "test"},
	}, "my-code")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Models) != 2 {
		t.Fatalf("目录条目 = %d，应为 2: %s", len(doc.Models), b)
	}
	m := doc.Models[0]
	if m["display_name"] != "My Code" || m["description"] != "主模型" ||
		m["context_window"] != float64(1000000) ||
		m["max_context_window"] != float64(1000000) ||
		m["default_reasoning_level"] != "high" ||
		m["auto_review_model_override"] != "my-code" {
		t.Fatalf("%v", m)
	}
	levels, ok := m["supported_reasoning_levels"].([]any)
	if !ok || len(levels) != 2 {
		t.Fatalf("思考等级 = %v", m["supported_reasoning_levels"])
	}
	if lv, _ := levels[1].(map[string]any); lv["effort"] != "high" || lv["description"] != "high" {
		t.Fatalf("思考等级条目 = %v", lv)
	}
	if doc.Models[1]["display_name"] != "test" {
		t.Fatalf("%v", doc.Models[1])
	}
	if doc.Models[1]["auto_review_model_override"] != "my-code" {
		t.Fatalf("每一行都应写自动审批覆盖: %v", doc.Models[1])
	}
}

// TestBuildCodexCatalogDefaults 覆盖缺省值：显示名/描述回退、默认思考等级
// 取列表首项；未声明的窗口/思考等级保留模板值；模板遗留的升级/引导/自动
// 审批字段必须清除，避免桌面版提示升级到网关不认识的模型。
func TestBuildCodexCatalogDefaults(t *testing.T) {
	tpl := testCodexCatalogTemplate()
	tpl["upgrade_info"] = map[string]any{"message": "upgrade"}
	tpl["availability_nux"] = map[string]any{"message": "nux"}
	tpl["auto_review_model_override"] = "stale-template-model"
	tpl["supported_reasoning_levels"] = []any{
		map[string]any{"effort": "template", "description": "template"},
	}
	tpl["default_reasoning_level"] = "template"
	b, err := buildCodexModelCatalog(tpl, []codexCatalogModel{
		{Name: "plain", ReasoningLevels: []string{"low", "medium"}},
		{Name: "no-levels"},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Models []map[string]any `json:"models"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	m := doc.Models[0]
	if m["display_name"] != "plain" || m["description"] != "AgentSwitch 逻辑模型：plain" {
		t.Fatalf("%v", m)
	}
	if m["default_reasoning_level"] != "low" {
		t.Fatalf("默认等级应回退到首项: %v", m)
	}
	if m["context_window"] != float64(128000) || m["max_context_window"] != float64(128000) {
		t.Fatalf("未声明窗口时应保留模板值: %v", m)
	}
	for _, k := range []string{"upgrade", "upgrade_info", "availability_nux", "auto_review_model_override"} {
		if _, ok := m[k]; ok {
			t.Fatalf("%s 不应出现: %v", k, m)
		}
	}
	// 未声明思考等级时沿用模板（spec §4.4：非空才替换）。
	bare := doc.Models[1]
	levels, ok := bare["supported_reasoning_levels"].([]any)
	if !ok || len(levels) != 1 {
		t.Fatalf("思考等级应保留模板值: %v", bare)
	}
	if lv, _ := levels[0].(map[string]any); lv["effort"] != "template" {
		t.Fatalf("思考等级条目 = %v", lv)
	}
	if bare["default_reasoning_level"] != "template" {
		t.Fatalf("默认等级应保留模板值: %v", bare)
	}
}

// TestCodexCatalogModelsResolvesMetadataAndDedupes 覆盖从存储解析目录元数据：
// 保持传入顺序、按去空白后的名字防御性去重、携带显示名/窗口/思考等级。
func TestCodexCatalogModelsResolvesMetadataAndDedupes(t *testing.T) {
	a := tempApp(t)
	for _, lm := range []storage.LogicalModel{
		{ID: "lm-a", Name: "my-code", DisplayName: "My Code", Description: "主模型",
			ContextWindow: 1000000, ReasoningLevels: []string{"low", "high"},
			DefaultReasoningLevel: "high", Enabled: true},
		{ID: "lm-b", Name: "test", Enabled: true},
	} {
		if _, err := a.Store().LogicalModels().Save(lm); err != nil {
			t.Fatal(err)
		}
	}
	acc := AccessConfigs{A: a}
	got, err := acc.codexCatalogModels([]string{"test", "my-code", " test "}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "test" || got[1].Name != "my-code" {
		t.Fatalf("顺序/去重错误: %+v", got)
	}
	if got[1].DisplayName != "My Code" || got[1].Description != "主模型" ||
		got[1].ContextWindow != 1000000 || got[1].DefaultReasoningLevel != "high" ||
		len(got[1].ReasoningLevels) != 2 || got[1].ReasoningLevels[1] != "high" {
		t.Fatalf("元数据未透传: %+v", got[1])
	}
	if got[0].DisplayName != "" || got[0].ContextWindow != 0 || len(got[0].ReasoningLevels) != 0 {
		t.Fatalf("未配置元数据应为空: %+v", got[0])
	}
}

// TestCodexCatalogModelsRejectsMissingOrDisabled 覆盖校验：模型不存在或已停用
// 时报中文错误并指明模型名（目录不得为不可用模型生成条目）。
func TestCodexCatalogModelsRejectsMissingOrDisabled(t *testing.T) {
	a := tempApp(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{
		ID: "lm-off", Name: "off", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a}
	if _, err := acc.codexCatalogModels([]string{"ghost"}, nil); err == nil ||
		!strings.Contains(err.Error(), "ghost") || !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("缺失模型错误 = %v", err)
	}
	if _, err := acc.codexCatalogModels([]string{"off"}, nil); err == nil ||
		!strings.Contains(err.Error(), "off") || !strings.Contains(err.Error(), "已停用") {
		t.Fatalf("停用模型错误 = %v", err)
	}
	if _, err := acc.codexCatalogModels([]string{" "}, nil); err == nil ||
		!strings.Contains(err.Error(), "不能为空") || !strings.Contains(err.Error(), "请移除后重试") {
		t.Fatalf("空模型名错误 = %v", err)
	}
}

// TestCodexCatalogRejectsDisabledBound 钉死绑定/默认模型必须启用：停用时
// writeCodexLive 中止并在错误里指明模型名与「已停用」，不改动已有
// config.toml、也不留下目录文件（避免生成指向不可用模型的目录）。
func TestCodexCatalogRejectsDisabledBound(t *testing.T) {
	a := tempApp(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{
		ID: "lm-off", Name: "off", Enabled: false}); err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a, Home: t.TempDir()}
	dir := filepath.Join(acc.Home, ".codex")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := "model = \"keep\"\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(sentinel), 0o600); err != nil {
		t.Fatal(err)
	}

	err := acc.writeCodexLive(map[string]string{"OPENAI_API_KEY": "x"}, "off", nil)
	if err == nil || !strings.Contains(err.Error(), "off") || !strings.Contains(err.Error(), "已停用") {
		t.Fatalf("停用绑定模型错误 = %v", err)
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

// TestCodexCatalogModelsMetaOverrides：接入文档 model_meta 按字段覆盖逻辑
// 模型元数据（未覆盖字段保持库值），非法取值报错。
func TestCodexCatalogModelsMetaOverrides(t *testing.T) {
	a := tempApp(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{
		ID: "lm1", Name: "my-code", Enabled: true,
		DisplayName: "Base", ContextWindow: 1000,
		ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "low",
	}); err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a}
	models, err := acc.codexCatalogModels([]string{"my-code"}, map[string]map[string]any{
		"my-code": {"context_window": json.Number("2000000"), "display_name": "Per Config"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 {
		t.Fatalf("%+v", models)
	}
	m := models[0]
	if m.DisplayName != "Per Config" || m.ContextWindow != 2000000 {
		t.Fatalf("override not applied: %+v", m)
	}
	if len(m.ReasoningLevels) != 2 || m.DefaultReasoningLevel != "low" {
		t.Fatalf("non-overridden fields must keep logical meta: %+v", m)
	}
	if _, err := acc.codexCatalogModels([]string{"my-code"}, map[string]map[string]any{
		"my-code": {"reasoning_levels": []any{"bogus"}},
	}); err == nil {
		t.Fatal("invalid level override must fail")
	}
	if _, err := codexModelMetaFromDoc(map[string]any{"model_meta": []any{}}); err == nil {
		t.Fatal("model_meta non-object must fail")
	}
}

// TestCodexDocModelMetaSaveValidation：model_meta 是合法顶层键；坏结构/坏
// 等级在保存时即被拒绝（不等到应用）。
func TestCodexDocModelMetaSaveValidation(t *testing.T) {
	a := tempApp(t)
	if _, err := a.Store().LogicalModels().Save(storage.LogicalModel{ID: "lm1", Name: "my-code", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	acc := AccessConfigs{A: a}
	good := `{"models":["my-code"],"model_meta":{"my-code":{"display_name":"Per","context_window":2000000,"reasoning_levels":["low","high"],"default_reasoning_level":"high"}}}`
	if _, err := acc.Create("codex", "cfg", "lm1", good); err != nil {
		t.Fatal(err)
	}
	badLevel := `{"models":["my-code"],"model_meta":{"my-code":{"reasoning_levels":["bogus"]}}}`
	if _, err := acc.Create("codex", "cfg2", "lm1", badLevel); err == nil {
		t.Fatal("invalid reasoning level must be rejected on save")
	}
	badType := `{"models":["my-code"],"model_meta":[]}`
	if _, err := acc.Create("codex", "cfg3", "lm1", badType); err == nil {
		t.Fatal("non-object model_meta must be rejected")
	}
}

// TestCodexTemplateCacheLoadsOnce 覆盖 P0-2 的模板缓存：首次加载后进程内
// 复用，且返回深拷贝（调用方修改不污染缓存）。
func TestCodexTemplateCacheLoadsOnce(t *testing.T) {
	resetCodexTemplateCache()
	old := codexTemplateSource
	calls := 0
	codexTemplateSource = func() (map[string]any, error) {
		calls++
		return testCodexCatalogTemplate(), nil
	}
	defer func() {
		codexTemplateSource = old
		resetCodexTemplateCache()
	}()
	m1, err := loadCodexModelCatalogTemplate()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadCodexModelCatalogTemplate(); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("template loaded %d times, want 1 (cache miss only)", calls)
	}
	m1["slug"] = "mutated"
	m3, err := loadCodexModelCatalogTemplate()
	if err != nil {
		t.Fatal(err)
	}
	if m3["slug"] == "mutated" {
		t.Fatal("cache must return a deep copy")
	}
}
