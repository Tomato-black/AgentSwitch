package bridge

import (
	"reflect"
	"testing"

	"AgentSwitch/internal/presets"
)

// TestPresetsBridgeList 桥接列表与 presets.All 一一对应、字段透传（双协议
// Protocols 表整表相等）。
func TestPresetsBridgeList(t *testing.T) {
	presets.Load([]byte(`[
  {"id":"qwen","displayName":"阿里千问","docUrl":"https://bailian.console.aliyun.com/",
   "protocols":{
     "openai":{"baseUrl":"https://dashscope.aliyuncs.com/compatible-mode/v1","defaultModels":["qwen3.8-max"]},
     "anthropic":{"baseUrl":"https://dashscope.aliyuncs.com/apps/anthropic","defaultModels":["qwen3.8-max"]}}}
]`), "")
	all := presets.All()
	got := Presets{}.List()
	if len(got) != len(all) {
		t.Fatalf("List() len=%d, want %d", len(got), len(all))
	}
	for i, v := range got {
		p := all[i]
		if v.ID != p.ID || v.DisplayName != p.DisplayName || v.DocURL != p.DocURL {
			t.Errorf("item %d mismatch:\n got %+v\nwant %+v", i, v, p)
		}
		if !reflect.DeepEqual(v.Protocols, p.Protocols) {
			t.Errorf("item %d (%s) protocols:\n got %+v\nwant %+v", i, v.ID, v.Protocols, p.Protocols)
		}
	}
}
