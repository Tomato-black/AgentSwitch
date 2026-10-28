package state

import "testing"

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
