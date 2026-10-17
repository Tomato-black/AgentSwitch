package bridge

import (
	"testing"

	"AgentSwitch/internal/presets"
)

// TestPresetsBridgeList 桥接列表与 presets.All 一一对应、字段透传。
func TestPresetsBridgeList(t *testing.T) {
	all := presets.All()
	got := Presets{}.List()
	if len(got) != len(all) {
		t.Fatalf("List() len=%d, want %d", len(got), len(all))
	}
	for i, v := range got {
		p := all[i]
		if v.ID != p.ID || v.DisplayName != p.DisplayName || v.Protocol != p.Protocol ||
			v.BaseURL != p.BaseURL || v.DocURL != p.DocURL {
			t.Errorf("item %d mismatch:\n got %+v\nwant %+v", i, v, p)
		}
		if len(v.DefaultModels) != len(p.DefaultModels) {
			t.Errorf("item %d (%s) models len=%d want %d", i, v.ID, len(v.DefaultModels), len(p.DefaultModels))
			continue
		}
		for j, m := range v.DefaultModels {
			if m != p.DefaultModels[j] {
				t.Errorf("item %d (%s) model[%d]=%q want %q", i, v.ID, j, m, p.DefaultModels[j])
			}
		}
	}
}
