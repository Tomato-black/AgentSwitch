package claudedesktop

import (
	"testing"

	"AgentSwitch/internal/desktop/state"
)

// TestDesktopFormDocRoundTrip：表单行 ↔ 角色映射文档双向转换（UI 层唯一
// 业务逻辑；空角色不落键、oneM 只在启用时生效）。
func TestDesktopFormDocRoundTrip(t *testing.T) {
	f := state.DesktopForm{Models: [4]string{"my-code", "", "mimo", ""}, OneM: [4]bool{true, false, false, false}}
	raw, err := desktopDocFromForm(&f)
	if err != nil {
		t.Fatal(err)
	}
	st := &state.State{}
	desktopFormFromDoc(st, raw)
	if st.Access.DesktopForm.Models[0] != "my-code" || !st.Access.DesktopForm.OneM[0] {
		t.Fatalf("sonnet 回填: %+v", st.Access.DesktopForm)
	}
	if st.Access.DesktopForm.Models[1] != "" || st.Access.DesktopForm.Models[3] != "" {
		t.Fatalf("未启用角色不得回填: %+v", st.Access.DesktopForm)
	}
	if st.Access.DesktopForm.Models[2] != "mimo" || st.Access.DesktopForm.OneM[2] {
		t.Fatalf("haiku 回填: %+v", st.Access.DesktopForm)
	}
	// 全空必须拒绝保存语义（docFromForm 报错）。
	empty := state.DesktopForm{}
	if _, err := desktopDocFromForm(&empty); err == nil {
		t.Fatal("全空角色应报错")
	}
}
