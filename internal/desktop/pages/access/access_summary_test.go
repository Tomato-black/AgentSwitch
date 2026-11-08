package access

import (
	"net"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/uitest"
)

// TestClaudeSummaryCardVisible 覆盖合并页摘要卡：Claude 页签首屏即可见
// 接入状态，无需展开配置列表。测试夹具网关为停止态——状态徽章按优先级
// 显示「网关已停止」（2026-11-07 原型对齐）。
func TestClaudeSummaryCardVisible(t *testing.T) {
	st, _ := uitest.New(t)
	st.Access.App = "claude"
	tt := ui.NewTester(func(c *ui.Context) {
		Page(c, st)
	}, 1000, 800)
	for _, want := range []string{"Claude Code 接入", "网关已停止", "尚未选择接入配置", "Claude Code 设置"} {
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
	if st.Access.ApplyTask.IsRunning() {
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
	if !st.Access.ApplyTask.IsRunning() {
		t.Fatalf("clean settings must allow apply, msg=%q", st.Access.Msg)
	}
}

// TestApplyAccessConfirmDialog 覆盖 D4：摘要卡「应用接入」先弹确认框
//（目标文件与受管键说明在场），取消不启动任务，确认走 ApplyAsync。
func TestApplyAccessConfirmDialog(t *testing.T) {
	st, a := uitest.New(t)
	st.Access.App = "claude"
	// 探测空闲端口注入：不与本机在跑的实例抢 17890。
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cur, err := a.CurrentSettings()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SaveSettings(port, cur.RequestTimeoutSeconds); err != nil {
		t.Fatal(err)
	}
	if err := a.StartGateway(); err != nil {
		t.Fatal(err)
	}
	acc := bridge.AccessConfigs{A: a, Home: t.TempDir()}
	ch, err := (bridge.Channels{A: a}).Create("", "主渠道", "https://api.example.com", "anthropic", "sk-x", true, false)
	if err != nil {
		t.Fatal(err)
	}
	lm, err := (bridge.LogicalModels{A: a}).Create("my-code", "", bridge.ModelMeta{},
		[]bridge.TargetSpec{{ChannelID: ch.ID, ModelName: "m1"}})
	if err != nil {
		t.Fatal(err)
	}
	v, err := acc.Create("claude", "主配置", lm.ID, "{}")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Store().AccessConfigs().SetCurrent("claude", v.ID); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(func(c *ui.Context) {
		Page(c, st)
	}, 1000, 900)
	if err := tt.Click("重新应用"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"应用接入配置？", "目标文件", "~/.claude/settings.json", "自动备份"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in apply dialog: %v", want, tt.Texts())
		}
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if st.Access.ApplyTask.IsRunning() {
		t.Fatal("取消不得启动应用任务")
	}
	if err := tt.Click("重新应用"); err != nil {
		t.Fatal(err)
	}
	wait := uitest.AsyncWaiter(t, st)
	if err := tt.Click("确认应用"); err != nil {
		t.Fatal(err)
	}
	wait()
	if st.Access.ApplyTask.IsRunning() || !strings.Contains(st.Access.Msg, "已应用") {
		t.Fatalf("确认后应完成应用任务：msg=%q", st.Access.Msg)
	}
}
