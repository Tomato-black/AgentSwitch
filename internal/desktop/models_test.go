package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestModelsCreateFlow 覆盖模型页主路径：新建保存落库并复位表单；重复公开
// 名（别名唯一）的错误必须进 msg 且不产生第二行；载入所选回填；删除两段式
// ——首击只置确认位，"确认删除"才真正落刀。
func TestModelsCreateFlow(t *testing.T) {
	u, _, _ := testUIWithCreds(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 900, 600)
	if !tt.HasText("模型") {
		t.Fatalf("texts %v", tt.Texts())
	}
	u.modelForm.public = "claude-opus"
	u.modelForm.upstream = "claude-opus-4-8"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	got := u.svc.Models.List()
	if len(got) != 1 || got[0].PublicModel != "claude-opus" {
		t.Fatalf("models %+v", got)
	}
	if u.modelForm.msg != "" || u.modelForm.editID != "" {
		t.Fatalf("form not reset: %+v", u.modelForm)
	}
	// 别名唯一：重复公开名的错误入 msg（不静默丢弃），且不落第二行。
	u.modelForm.public = "claude-opus"
	u.modelForm.upstream = "claude-opus-4-8"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if u.modelForm.msg == "" {
		t.Fatal("duplicate alias error must surface in form msg")
	}
	if len(u.svc.Models.List()) != 1 {
		t.Fatalf("duplicate must not persist: %+v", u.svc.Models.List())
	}
	// 载入所选回填（顺带清掉已过期的 msg）。
	u.modelSel = 0
	if err := tt.Click("载入所选"); err != nil {
		t.Fatal(err)
	}
	if u.modelForm.editID == "" || u.modelForm.public != "claude-opus" ||
		u.modelForm.upstream != "claude-opus-4-8" {
		t.Fatalf("load selected %+v", u.modelForm)
	}
	if u.modelForm.msg != "" {
		t.Fatalf("load must clear stale msg: %+v", u.modelForm)
	}
	// 删除两段式（裁定 3）：首击只置确认位。
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !u.modelConfirmDelete {
		t.Fatal("first delete click must only arm the confirmation")
	}
	if len(u.svc.Models.List()) != 1 {
		t.Fatal("armed confirmation must not delete yet")
	}
	// 载入所选清位（三页 confirm 清位）：武装后再载入，确认位必须落空。
	if err := tt.Click("载入所选"); err != nil {
		t.Fatal(err)
	}
	if u.modelConfirmDelete {
		t.Fatal("loading another row must disarm the delete confirmation")
	}
	// 重新武装后走原两段式第二段。
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if !u.modelConfirmDelete {
		t.Fatal("re-arm after disarm must succeed")
	}
	// 第二段："确认删除"真正执行。
	if err := tt.Click("确认删除"); err != nil {
		t.Fatal(err)
	}
	if u.modelConfirmDelete {
		t.Fatal("confirmation must be disarmed after confirm")
	}
	if got := u.svc.Models.List(); len(got) != 0 {
		t.Fatalf("confirm must remove: %+v", got)
	}
	if u.modelForm.editID != "" {
		t.Fatalf("form must reset after delete: %+v", u.modelForm)
	}
}
