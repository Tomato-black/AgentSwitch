package state

import (
	"path/filepath"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
)

// TestAccessFormDirty 覆盖编辑器脏判定：未修改干净、改名称/文档变脏、
// 未打开的表单始终干净。
func TestAccessFormDirty(t *testing.T) {
	f := &AccessForm{Open: true}
	f.Baseline = f.Signature("claude")
	if f.Dirty("claude") {
		t.Fatal("opened but unchanged must be clean")
	}
	f.Name = "draft"
	if !f.Dirty("claude") {
		t.Fatal("name change must be dirty")
	}
	f.Name = ""
	f.DocJSON = `{"env":{}}`
	if !f.Dirty("claude") {
		t.Fatal("doc change must be dirty")
	}
	closed := &AccessForm{Baseline: "x"}
	if closed.Dirty("claude") {
		t.Fatal("closed form must be clean")
	}
}

// TestDesktopFormDirty 覆盖 Desktop 角色表单脏判定。
func TestDesktopFormDirty(t *testing.T) {
	f := &DesktopForm{Open: true}
	f.Baseline = f.Signature()
	if f.Dirty() {
		t.Fatal("unchanged must be clean")
	}
	f.Models[0] = "my-code"
	if !f.Dirty() {
		t.Fatal("role change must be dirty")
	}
	f.Models[0] = ""
	f.OneM[2] = true
	if !f.Dirty() {
		t.Fatal("1M change must be dirty")
	}
}

// TestGoDirtyGuard 覆盖 P1-2：有未保存编辑时 Go 只武装确认，Navigate 才
// 丢弃草稿并切换；无修改时直接切换。返回列表这种同页子视图动作也要先确认。
func TestGoDirtyGuard(t *testing.T) {
	s := &State{Page: "logical-edit"}
	s.Logical.Baseline = s.Logical.Edit.Signature()
	s.Logical.Edit.Name = "draft"
	s.Go("channels", nil)
	if !s.NavArm || s.NavTo != "channels" {
		t.Fatalf("dirty goto must arm confirm: %+v", s)
	}
	if s.Page != "logical-edit" {
		t.Fatal("page must not change before confirm")
	}
	s.Navigate(s.NavTo, nil)
	if s.Page != "channels" || s.Logical.Edit.Name != "" {
		t.Fatalf("navigate must switch and discard: page=%q edit=%+v", s.Page, s.Logical.Edit)
	}

	s.Page = "channels"
	s.Channels.View = "detail"
	s.Channels.Baseline = s.Channels.Signature()
	s.Channels.Form.Name = "changed"
	back := func() { s.Channels.View = "" }
	s.Go("channels", back)
	if !s.NavArm || s.NavTo != "channels" {
		t.Fatalf("dirty back must arm confirm: %+v", s)
	}
	s.Navigate(s.NavTo, nil)
	if s.Channels.View != "" {
		t.Fatalf("back action must run after confirm: %q", s.Channels.View)
	}
	if s.Channels.Form.Name != "" {
		t.Fatalf("drafts must be discarded: %+v", s.Channels.Form)
	}
}

// TestGoDirtyGuardClaudeCfg 回归（I1）：接入页 Claude 页签的设置区有未保存
// 修改时，侧栏切页必须武装确认；确认后草稿连同表单一起丢弃（Home 保留）。
func TestGoDirtyGuardClaudeCfg(t *testing.T) {
	s := &State{Page: "access"}
	s.Access.App = "claude"
	if s.DirtyCurrent() {
		t.Fatal("clean settings section must not block nav")
	}
	s.ClaudeCfg.Home = "/tmp/home"
	s.ClaudeCfg.Dirty = true
	if !s.DirtyCurrent() {
		t.Fatal("dirty claude config must block sidebar nav")
	}
	s.Go("channels", nil)
	if !s.NavArm || s.NavTo != "channels" {
		t.Fatalf("dirty goto must arm confirm: %+v", s)
	}
	s.Navigate(s.NavTo, nil)
	if s.ClaudeCfg.Dirty || s.ClaudeCfg.Loaded {
		t.Fatalf("navigate must discard claude config draft: %+v", s.ClaudeCfg)
	}
	if s.ClaudeCfg.Home != "/tmp/home" {
		t.Fatalf("injected Home must survive discard: %q", s.ClaudeCfg.Home)
	}

	// 非 Claude 页签不受该区脏状态影响。
	s = &State{Page: "access"}
	s.Access.App = "codex"
	s.ClaudeCfg.Dirty = true
	if s.DirtyCurrent() {
		t.Fatal("codex tab must ignore claude config dirty flag")
	}
}

func TestNewDefaultsToChannelsWhenEmpty(t *testing.T) {
	a, err := app.Open(app.Options{
		DBPath:  filepath.Join(t.TempDir(), "s.db"),
		LogPath: filepath.Join(t.TempDir(), "s.log"),
		Host:    "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	st := New(Services{
		Gateway:  bridge.Gateway{A: a},
		Channels: bridge.Channels{A: a},
		Logical:  &bridge.LogicalModels{A: a},
	})
	if st.Page != "channels" {
		t.Fatalf("empty state default page = %q, want channels", st.Page)
	}
}
