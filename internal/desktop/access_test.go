package desktop

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// TestAccessTabTitles 覆盖接入页两个 Agent tab 标题快照。
func TestAccessTabTitles(t *testing.T) {
	if a, b := accessTabTitles(); a != "Claude Code" || b != "Codex CLI" {
		t.Fatal(a, b)
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
	for _, want := range []string{"Claude Code", "Codex CLI", "全局配置", "实际配置", "还没有配置", "网关未运行"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("当前逻辑模型") {
		t.Fatalf("旧片段页元素泄漏: %v", tt.Texts())
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
