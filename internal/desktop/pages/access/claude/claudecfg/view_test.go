package claudecfg

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/uitest"
)

func TestSectionRendersClaudeSettings(t *testing.T) {
	st, _ := uitest.New(t)
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st.Access.App = "claude"
	st.ClaudeCfg.Home = home
	tt := ui.NewTester(func(c *ui.Context) {
		Section(c, st)
	}, 1000, 1600)
	for _, want := range []string{
		"Claude Code 设置", "受管 13 键", "用户级", "项目级", "企业托管",
		"表单", "JSON", "合并预览", "模型与响应", "权限", "Skills",
		"危险模式确认", "保存设置",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}
