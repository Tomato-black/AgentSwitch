// 外部测试包（package provider_test）：同时 import provider 侧护栏与 presets
// 数据（presets 依赖 provider，包内测试会成环——外部测试包是 Go 的正解）。
package provider_test

import (
	"encoding/json"
	"testing"

	"AgentSwitch/internal/presets"
)

// TestDefaultJSONAnthropicBasesCoveredByGuard 锁死 default.json 与护栏表的
// 数据关联（审查 minor 4）：内嵌默认表出现的每个 anthropic baseUrl 必须在
// TestAnthropicEndpointAllPresetBases 的字面量表中有对应断言——改 default.json
// 的端点而不同步护栏表会先红。避免 tautology：护栏表仍是手抄期望完整 URL。
func TestDefaultJSONAnthropicBasesCoveredByGuard(t *testing.T) {
	guard := map[string]bool{
		"https://api.xiaomimimo.com/anthropic":                 true,
		"https://dashscope.aliyuncs.com/apps/anthropic":        true,
		"https://api.deepseek.com/anthropic":                   true,
		"https://api.moonshot.cn/anthropic":                    true,
		"https://open.bigmodel.cn/api/anthropic":               true,
		"https://api.siliconflow.cn":                           true,
		"https://api.stepfun.com":                              true,
		"https://ark.cn-beijing.volces.com/api/compatible":     true,
		"https://api.anthropic.com":                            true,
		"https://openrouter.ai/api/v1":                         true,
		"https://ark.cn-beijing.volces.com/api/coding":         true,
		"https://api.kimi.com/coding/":                         true,
		"https://coding.dashscope.aliyuncs.com/apps/anthropic": true,
		"https://api.stepfun.com/step_plan":                    true,
		"https://token-plan-cn.xiaomimimo.com/anthropic":       true,
	}
	var table []struct {
		Protocols map[string]struct {
			BaseURL string `json:"baseUrl"`
		} `json:"protocols"`
	}
	if err := json.Unmarshal(presets.DefaultJSON(), &table); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, p := range table {
		if ep, ok := p.Protocols["anthropic"]; ok {
			n++
			if !guard[ep.BaseURL] {
				t.Errorf("default.json anthropic baseUrl %q 不在护栏表（TestAnthropicEndpointAllPresetBases）——改数据须同步护栏", ep.BaseURL)
			}
		}
	}
	if n == 0 {
		t.Fatal("default.json 无 anthropic 条目，解析路径可能错了")
	}
}
