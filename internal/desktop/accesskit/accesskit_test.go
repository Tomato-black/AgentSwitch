package accesskit

import (
	"encoding/json"
	"strings"
	"testing"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
)

// TestAccessTabTitles 覆盖接入页四个 tab 标题快照（三个受管 Agent +
// 连接指南——第三方系统照指南手工接入，AgentSwitch 不写其配置）。
func TestAccessTabTitles(t *testing.T) {
	if a, b, d, g := TabTitles(); a != "Claude Code" || b != "Codex" || d != "Claude Desktop" || g != "连接指南" {
		t.Fatal(a, b, d, g)
	}
}

// TestAccessConfigRowShowsCurrentBadge 覆盖「使用中」徽标纯函数。
func TestAccessConfigRowShowsCurrentBadge(t *testing.T) {
	if got := RowBadge(true); got != "使用中" {
		t.Fatal(got)
	}
	if RowBadge(false) != "" {
		t.Fatal("非 current 应无徽标")
	}
}

// TestAccessOverridesFromRows 覆盖覆盖项行→map：空键/空值都剔除（半填行
// 是误操作——评审裁定按计划语义收口）。
func TestAccessOverridesFromRows(t *testing.T) {
	got := OverridesFromRows([][2]string{{"K", "V"}, {"", "x"}, {"K2", ""}})
	if len(got) != 1 || got["K"] != "V" {
		t.Fatal(got)
	}
}

// TestAccessModelIDByLabel 覆盖模型名→ID 映射：命中含 disabled 项（编辑
// 旧配置回显不丢引用）；未命中空串。
func TestAccessModelIDByLabel(t *testing.T) {
	ms := []bridge.ModelView{{ID: "id1", Name: "m1", Enabled: true}, {ID: "id2", Name: "m2"}}
	if ModelIDByLabel(ms, "m2") != "id2" {
		t.Fatal("应命中含 disabled 项")
	}
	if ModelIDByLabel(ms, "nope") != "" {
		t.Fatal("未命中应空串")
	}
}

// TestAccessGlobalRowsOrdered 覆盖全局配置编辑行的键序稳定（字典序）。
func TestAccessGlobalRowsOrdered(t *testing.T) {
	rows := GlobalRows(map[string]string{"b": "2", "a": "1"})
	if rows[0][0] != "a" || rows[1][0] != "b" {
		t.Fatal(rows)
	}
}

func TestAccessDocSetEnv(t *testing.T) {
	raw, err := DocSetEnv(`{"env":{"A":"1"},"language":"zh-CN"}`, "B", "2")
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
	raw2, err := DocSetEnv(raw, "A", "")
	if err != nil {
		t.Fatal(err)
	}
	json.Unmarshal([]byte(raw2), &doc)
	if _, ok := doc["env"].(map[string]any)["A"]; ok {
		t.Fatal("空值应删键: " + raw2)
	}
	// 非法 JSON 报错
	if _, err := DocSetEnv(`{bad`, "A", "1"); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	// env 非对象（非 null）必须报错，不得静默清空（Minor#4）
	if _, err := DocSetEnv(`{"env":5}`, "A", "1"); err == nil ||
		!strings.Contains(err.Error(), "env 必须是 JSON 对象") {
		t.Fatalf("env 非对象应报错: %v", err)
	}
	// env:null 视为缺省
	if _, err := DocSetEnv(`{"env":null}`, "A", "1"); err != nil {
		t.Fatalf("env:null 应视为缺省: %v", err)
	}
}

func TestAccessDocEnvStrings(t *testing.T) {
	env, err := DocEnvStrings(`{"env":{"A":"1"}}`)
	if err != nil || env["A"] != "1" {
		t.Fatalf("%v %v", env, err)
	}
	if _, err := DocEnvStrings(`{"env":{"A":1}}`); err == nil {
		t.Fatal("非字符串值应报错（保留旧状态）")
	}
	if _, err := DocEnvStrings(`{bad`); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
	// env 非对象（非 null）必须报错，不得静默返回空表（Minor#4）
	if _, err := DocEnvStrings(`{"env":5}`); err == nil ||
		!strings.Contains(err.Error(), "env 必须是 JSON 对象") {
		t.Fatalf("env 非对象应报错: %v", err)
	}
	if env, err := DocEnvStrings(`{"env":null}`); err != nil || len(env) != 0 {
		t.Fatalf("env:null 应视为缺省: %v %v", env, err)
	}
}

// TestAccessDocSetModelsRoundTrip：Codex 文档 models 键写为有序字符串数组；
// 空列表删除键（保存/应用时回退绑定模型 + 启用模型）；其他键原样保留。
func TestAccessDocSetModelsRoundTrip(t *testing.T) {
	raw, err := DocSetModels(`{"env":{"A":"1"}}`, []string{"my-code", "test"})
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	got, ok := doc["models"].([]any)
	if !ok || len(got) != 2 || got[0] != "my-code" || got[1] != "test" {
		t.Fatalf("models 未按序写入: %s", raw)
	}
	if doc["env"].(map[string]any)["A"] != "1" {
		t.Fatalf("其他键被破坏: %s", raw)
	}
	empty, err := DocSetModels(raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	doc = map[string]any{}
	if err := json.Unmarshal([]byte(empty), &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc["models"]; ok {
		t.Fatalf("空列表应删除 models: %s", empty)
	}
	if _, err := DocSetModels(`{bad`, []string{"x"}); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

// TestAccessDocSetCodexRoundTrip：codex 对象只写非空字段；全空删除 codex
// 键；其他顶层键保留。
func TestAccessDocSetCodexRoundTrip(t *testing.T) {
	raw, err := DocSetCodex(`{"models":["my-code"]}`, "test", "high", "my-code")
	if err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	codex, ok := doc["codex"].(map[string]any)
	if !ok || codex["subagent_model"] != "test" ||
		codex["subagent_reasoning_effort"] != "high" ||
		codex["auto_review_model"] != "my-code" {
		t.Fatalf("codex 字段未写入: %s", raw)
	}
	// 空字段不落键（去空白后视为空）。
	raw, err = DocSetCodex(raw, " test ", "", "")
	if err != nil {
		t.Fatal(err)
	}
	doc = map[string]any{}
	json.Unmarshal([]byte(raw), &doc)
	codex = doc["codex"].(map[string]any)
	if _, ok := codex["subagent_reasoning_effort"]; ok {
		t.Fatalf("空字段不应落键: %s", raw)
	}
	if _, ok := codex["auto_review_model"]; ok {
		t.Fatalf("空字段不应落键: %s", raw)
	}
	// 全空删除 codex，models 保留。
	raw, err = DocSetCodex(raw, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	doc = map[string]any{}
	json.Unmarshal([]byte(raw), &doc)
	if _, ok := doc["codex"]; ok {
		t.Fatalf("全空应删除 codex: %s", raw)
	}
	if _, ok := doc["models"]; !ok {
		t.Fatalf("其他顶层键应保留: %s", raw)
	}
	if _, err := DocSetCodex(`{bad`, "", "", ""); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

// TestModelSummary：Codex 多模型显示「首模型 等 N 个」；单模型/非 Codex/
// 非法文档回退绑定模型名。
func TestModelSummary(t *testing.T) {
	two := `{"models":["m-a","m-b"],"env":{}}`
	if got := ModelSummary("codex", two, "bound"); got != "m-a 等 2 个" {
		t.Fatalf("multi = %q", got)
	}
	if got := ModelSummary("codex", `{"models":["only"],"env":{}}`, "bound"); got != "only" {
		t.Fatalf("single = %q", got)
	}
	if got := ModelSummary("codex", `{bad`, "bound"); got != "bound" {
		t.Fatalf("invalid = %q", got)
	}
	if got := ModelSummary("claude", two, "bound"); got != "bound" {
		t.Fatalf("non-codex = %q", got)
	}
}

// TestModelMetaDocRoundTrip：model_meta 写入/读取往返；空字段省略，清空
// 后删除条目与空的 model_meta 键；部分字段只覆盖改过的键。
func TestModelMetaDocRoundTrip(t *testing.T) {
	raw, err := DocSetModelMeta(`{"env":{}}`, "m1", ModelMetaDoc{
		DisplayName: "X", ContextWindow: 2000000,
		ReasoningLevels: []string{"low", "high"}, DefaultReasoningLevel: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	doc, err := DocObject(raw)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := DocModelMeta(doc, "m1")
	if !ok || got.DisplayName != "X" || got.ContextWindow != 2000000 ||
		len(got.ReasoningLevels) != 2 || got.DefaultReasoningLevel != "high" {
		t.Fatalf("round trip = %+v", got)
	}
	// 部分覆盖：只写窗口，其余零值。
	raw2, _ := DocSetModelMeta(raw, "m2", ModelMetaDoc{ContextWindow: 1000})
	doc2, _ := DocObject(raw2)
	if m2, ok := DocModelMeta(doc2, "m2"); !ok || m2.ContextWindow != 1000 || m2.DisplayName != "" {
		t.Fatalf("partial = %+v", m2)
	}
	// 清空 m2：仅剩 m1，条目删除。
	raw3, _ := DocSetModelMeta(raw2, "m2", ModelMetaDoc{})
	doc3, _ := DocObject(raw3)
	if _, ok := DocModelMeta(doc3, "m2"); ok {
		t.Fatal("empty entry must be removed")
	}
	// 清空全部：model_meta 键删除。
	raw4, _ := DocSetModelMeta(raw3, "m1", ModelMetaDoc{})
	doc4, _ := DocObject(raw4)
	if _, exists := doc4["model_meta"]; exists {
		t.Fatal("empty model_meta must be removed")
	}
}

// TestValidateGlobalRows 覆盖全局配置行校验：半填或空行都拦截，完整行放行。
func TestValidateGlobalRows(t *testing.T) {
	if err := ValidateGlobalRows(nil); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGlobalRows([][2]string{{"HTTPS_PROXY", "http://p:1"}}); err != nil {
		t.Fatal(err)
	}
	for _, rows := range [][][2]string{
		{{"KEY", ""}},
		{{"", "value"}},
		{{"", ""}},
	} {
		if err := ValidateGlobalRows(rows); err == nil {
			t.Fatalf("rows %v must be rejected", rows)
		}
	}
}

// TestModelKeySyncPatchRoundTrip（2026-11-07 模型键归接入）：顶层
// model/fallbackModel 行状态 ↔ 文档双向同步——回填、写回、留空删键。
func TestModelKeySyncPatchRoundTrip(t *testing.T) {
	f := &state.AccessForm{DocJSON: `{"env":{},"model":"opus","fallbackModel":["a","b"]}`}
	SyncModelKeysFromDoc(f)
	if f.ModelKey != "opus" || len(f.FallbackModels) != 2 || f.FallbackModels[0] != "a" || f.FallbackModels[1] != "b" {
		t.Fatalf("回填错误: %+v", f)
	}
	PatchModelKey(f, "my-code")
	if !strings.Contains(f.DocJSON, `"model": "my-code"`) {
		t.Fatalf("主模型未写回: %s", f.DocJSON)
	}
	PatchFallbackModels(f, []string{"b", "a"})
	if !strings.Contains(f.DocJSON, `"fallbackModel": [`) {
		t.Fatalf("回退链未写回: %s", f.DocJSON)
	}
	// 回填校验改写后的文档。
	SyncModelKeysFromDoc(f)
	if f.ModelKey != "my-code" || f.FallbackModels[0] != "b" {
		t.Fatalf("改写后回填错误: %+v", f)
	}
	// 留空删键（跟随网关）。
	PatchModelKey(f, "")
	PatchFallbackModels(f, nil)
	SyncModelKeysFromDoc(f)
	if f.ModelKey != "" || len(f.FallbackModels) != 0 {
		t.Fatalf("空值应删键并回填为空: %q %v", f.ModelKey, f.FallbackModels)
	}
	if strings.Contains(f.DocJSON, "fallbackModel") {
		t.Fatalf("空链应删键: %s", f.DocJSON)
	}
}
