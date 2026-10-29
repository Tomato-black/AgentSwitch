package settings

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/uitest"
)

func TestQuotaSectionRenders(t *testing.T) {
	st, _ := uitest.New(t)
	st.Settings.QuotaReport = []bridge.QuotaStatus{{
		Channel: "ds", TokenQuota: 1_000_000, TokenUsed: 216_000,
		TokenRemaining: 784_000, ResetDay: 1,
	}}
	st.Settings.QuotasJSON = `{"ds":{"tokens":1000000,"reset_day":1}}`
	tt := ui.NewTester(func(c *ui.Context) { quotaSection(c, c.Theme(), st) }, 900, 500)
	for _, want := range []string{"本地配额", "ds", "784.0k", "保存配额"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q: %v", want, tt.Texts())
		}
	}
}
