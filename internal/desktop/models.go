package desktop

import (
	"strconv"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// modelsView 是模型聚合页（spec §8，决策 7）：跨渠道只读视图 + 行内启停；
// 增删改入口收敛在渠道详情，避免双入口不一致。
func (u *uiState) modelsView(c *ui.Context) {
	t := c.Theme()
	rows := bridge.ModelsAgg{A: u.svc.Channels.A}.ListAll()
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "模型").Bold().FontSize(t.FontSize + 2)
			ui.Text(c, strconv.Itoa(len(rows))).TextColor(t.TextMuted)
		})
		ui.Text(c, "公开模型 × 渠道 × 上游模型（编辑请到渠道页对应渠道）").TextColor(t.TextMuted)
		if len(rows) == 0 {
			ui.Text(c, "尚无模型——在渠道页为渠道添加公开模型").TextColor(t.TextMuted)
		}
		cols := []ui.TableColumn{{Title: "公开模型"}, {Title: "渠道"}, {Title: "上游模型"}, {Title: "启用"}}
		ui.Table(c, &u.modelList, cols, len(rows), func(row, col int) {
			m := rows[row]
			switch col {
			case 0:
				ui.Text(c, m.PublicModel).SingleLine()
			case 1:
				ui.Text(c, m.ChannelName).SingleLine()
			case 2:
				ui.Text(c, m.UpstreamModel).SingleLine().Font(monoFont)
			case 3:
				if m.Enabled {
					ui.Badge(c, "启用")
				} else {
					ui.Text(c, "停用").TextColor(t.TextMuted)
				}
			}
		}).Grow(1)
		ui.Row(c).Gap(8).Children(func() {
			if ui.Button(c, "停用/启用").Clicked() {
				if sel := u.modelList.Selected; sel != nil && *sel >= 0 && *sel < len(rows) {
					_ = bridge.ModelsAgg{A: u.svc.Channels.A}.SetEnabled(rows[*sel].ID, !rows[*sel].Enabled)
				}
			}
		})
	})
}
