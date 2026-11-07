package access

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/uitest"
)

// TestClaudeSummaryCardVisible 覆盖合并页摘要卡：Claude 页签首屏即可见
// 接入状态，无需展开配置列表。
func TestClaudeSummaryCardVisible(t *testing.T) {
	st, _ := uitest.New(t)
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) {
		Page(c, st)
	}, 1000, 800)
	for _, want := range []string{"Claude Code 接入", "尚未应用", "Claude Code 设置"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

// TestApplyAccessBlockedWhenSettingsDirty 覆盖接入/设置互斥：设置区有未
// 保存修改时，应用接入被阻断并提示先处理设置，ApplyTask 不启动。
func TestApplyAccessBlockedWhenSettingsDirty(t *testing.T) {
	st, a := uitest.New(t)
	st.Access.App = "claude"
	st.ClaudeCfg.Dirty = true
	acc := bridge.AccessConfigs{A: a, Home: t.TempDir()}
	accesskit.ApplyAsync(st, acc, bridge.AccessConfigView{ID: "cfg-1", Name: "主配置"})
	if st.Access.ApplyTask.Running {
		t.Fatal("dirty settings must block the apply task")
	}
	if !strings.Contains(st.Access.Msg, "未保存") {
		t.Fatalf("block message missing: %q", st.Access.Msg)
	}
}

// TestApplyAccessAllowedWhenSettingsClean 覆盖干净状态下应用照常启动
// （异步任务进入 Running）。
func TestApplyAccessAllowedWhenSettingsClean(t *testing.T) {
	st, a := uitest.New(t)
	st.Access.App = "claude"
	acc := bridge.AccessConfigs{A: a, Home: t.TempDir()}
	accesskit.ApplyAsync(st, acc, bridge.AccessConfigView{ID: "cfg-1", Name: "主配置"})
	if !st.Access.ApplyTask.Running {
		t.Fatalf("clean settings must allow apply, msg=%q", st.Access.Msg)
	}
}
