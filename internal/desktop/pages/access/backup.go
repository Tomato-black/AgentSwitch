package access

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// backupPage 是「备份与恢复」独立子页：列出该 Agent 的历史备份集，
// 选择任意一份还原；还原前 bridge 会先快照当前 live。
func backupPage(c *ui.Context, st *state.State, acc bridge.AccessConfigs) {
	t := c.Theme()
	s := uikit.Softs(t)
	ui.Column(c).Gap(12).Grow(1).Children(func() {
		uikit.PageHeader(c, t, uikit.PageHeaderOpts{
			Title: "备份与恢复", Subtitle: "选择一份历史备份覆盖当前 Agent 配置",
			BackLabel: "← 返回接入",
			OnBack: func() {
				st.Access.BackupOpen = false
				st.Access.BackupArm = false
				st.Access.BackupMsg = ""
			},
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(10).Children(func() {
				uikit.Feedback(c, t, uikit.InferFeedback(st.Access.BackupMsg), st.Access.BackupMsg)
				if len(st.Access.Backups) == 0 {
					uikit.EmptyStateBare(c, t, uikit.EmptyStateOpts{
						Title: "还没有备份", Hint: "应用配置时会自动备份当前文件",
					})
				}
				for _, b := range st.Access.Backups {
					info := b
					uikit.Card(c, t, func() {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, time.UnixMilli(info.Timestamp).Format("2006-01-02 15:04:05")).Bold()
							ui.Text(c, fmt.Sprintf("%d 个文件 · %d B", len(info.Files), info.SizeBytes)).
								TextColor(t.TextMuted).Grow(1)
							if uikit.SoftButton(c, t, s, "danger", "还原").Clicked() {
								st.Access.RestoreTS = info.Timestamp
								st.Access.BackupArm = true
							}
						})
						ui.Text(c, strings.Join(info.Files, "、")).TextColor(t.TextMuted).SingleLine()
					})
				}
			})
		})
	})
	if st.Access.BackupArm {
		uikit.ConfirmDialog(c, t, uikit.ConfirmOpts{
			Open: &st.Access.BackupArm, Title: "还原备份",
			Message:     "将用所选备份覆盖当前 Agent 配置文件（覆盖前会先备份当前状态，可再次还原）。",
			CancelLabel: "取消", ConfirmLabel: "还原", Danger: true,
			OnConfirm: func() {
				if err := acc.RestoreBackup(st.Access.App, st.Access.RestoreTS); err != nil {
					st.Access.BackupMsg = "还原失败：" + err.Error()
					return
				}
				st.Access.BackupMsg = "已还原所选备份"
				st.Access.Backups, _ = acc.ListBackups(st.Access.App)
			},
		})
	}
}
