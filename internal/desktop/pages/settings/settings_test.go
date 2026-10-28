package settings

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/config"
	"AgentSwitch/internal/desktop/uitest"
)

// TestSettingsFormMapsPort 覆盖表单↔Settings 编解码纯函数：端口/超时/透传
// 往返一致（端口是重点锚点），非整数输入报「端口/超时必须是整数」。
func TestSettingsFormMapsPort(t *testing.T) {
	portIn, timeoutIn, pas := settingsToForm(config.Settings{
		Port: 8080, RequestTimeoutSeconds: 90, PassthroughUnregistered: true,
	})
	if portIn != "8080" || timeoutIn != "90" || !pas {
		t.Fatalf("encode = %q %q %v, want 8080 90 true", portIn, timeoutIn, pas)
	}
	s, err := formToSettings(portIn, timeoutIn, pas)
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 8080 || s.RequestTimeoutSeconds != 90 || !s.PassthroughUnregistered {
		t.Fatalf("decode = %+v", s)
	}
	if _, err := formToSettings("abc", "90", false); err == nil {
		t.Fatal("non-integer port must error")
	}
	if _, err := formToSettings("8080", "x", false); err == nil {
		t.Fatal("non-integer timeout must error")
	}
	if _, err := formToSettings("8080", "90", false); err != nil {
		t.Fatal(err)
	}
}

// TestSettingsPageRenders 覆盖设置页替换占位：端口/超时/透传预留/版本/
// 保存按钮都在场；不再出现 TODO 占位。
func TestSettingsPageRenders(t *testing.T) {
	st, _ := uitest.New(t)
	st.Page = "settings"
	tt := ui.NewTester(func(c *ui.Context) { Page(c, st) }, 900, 600)
	for _, want := range []string{"端口", "请求超时", "透传", "预留", "版本", "保存设置"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if tt.HasText("TODO: 设置") {
		t.Fatalf("placeholder leaked: %v", tt.Texts())
	}
}
