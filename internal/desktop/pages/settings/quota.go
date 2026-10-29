package settings

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// quotaSection 渲染本地渠道配额：已用/剩余 + JSON 编辑。这是本地估算，
// 不是上游账户余额；上游余额在渠道详情「查余额」。
func quotaSection(c *ui.Context, t *ui.Theme, st *state.State) {
	ui.Text(c, "本地配额（按渠道）").Bold()
	ui.Text(c, "按本地月份与 reset_day 计算，是本地估算，不是上游账户余额").TextColor(t.TextMuted)
	if len(st.Settings.QuotaReport) == 0 {
		ui.Text(c, "未设置本地配额").TextColor(t.TextMuted)
	}
	for _, q := range st.Settings.QuotaReport {
		parts := make([]string, 0, 2)
		if q.TokenQuota > 0 {
			parts = append(parts, fmt.Sprintf("Token %s / 已用 %s / 剩余 %s",
				shortTokens(q.TokenQuota), shortTokens(q.TokenUsed), shortTokens(q.TokenRemaining)))
		}
		if q.USDQuota > 0 {
			parts = append(parts, fmt.Sprintf("$%.2f / 已用 $%.2f / 剩余 $%.2f",
				q.USDQuota, q.USDUsed, q.USDRemaining))
		}
		line := q.Channel + " · " + strings.Join(parts, " · ") +
			fmt.Sprintf(" · 每月 %d 日重置", q.ResetDay)
		color := t.TextMuted
		if q.TokenRemaining < 0 || q.USDRemaining < 0 {
			color = t.Danger
		} else if (q.TokenQuota > 0 && q.TokenRemaining*5 < q.TokenQuota) ||
			(q.USDQuota > 0 && q.USDRemaining < q.USDQuota*0.2) {
			color = t.Warning
		}
		ui.Text(c, line).TextColor(color).SingleLine()
	}
	ui.Text(c, "配额表（JSON，按渠道名）").Bold()
	ui.TextArea(c, &st.Settings.QuotasJSON).Height(100).Font(uikit.MonoFont)
	ui.Row(c).Gap(8).Children(func() {
		if uikit.SoftButton(c, t, uikit.Softs(t), "act", "保存配额").Clicked() {
			if err := st.Svc.Gateway.SaveQuotasJSON(st.Settings.QuotasJSON); err != nil {
				st.Settings.QuotaMsg = err.Error()
			} else {
				st.Settings.QuotaMsg = "配额已保存"
				loadUsage(st)
			}
		}
	})
	uikit.Feedback(c, t, uikit.InferFeedback(st.Settings.QuotaMsg), st.Settings.QuotaMsg)
}
