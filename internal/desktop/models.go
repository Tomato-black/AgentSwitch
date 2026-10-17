package desktop

import (
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// sortModelRows 按 PublicModel 升序、同组内 ChannelName 升序原地排序——
// 同名对外模型相邻排列（spec §5.4 视觉归组）；两级比较键即归组依据。
func sortModelRows(rows []bridge.ModelAggregateView) {
	slices.SortFunc(rows, func(a, b bridge.ModelAggregateView) int {
		if c := strings.Compare(a.PublicModel, b.PublicModel); c != 0 {
			return c
		}
		return strings.Compare(a.ChannelName, b.ChannelName)
	})
}

// filterModelRows 过滤行集：q 忽略大小写包含于 PublicModel/UpstreamModel/
// ChannelName 任一即命中；空 q 原样返回。渲染前先 sortModelRows 再过滤，
// 命中行保持归组顺序。
func filterModelRows(rows []bridge.ModelAggregateView, q string) []bridge.ModelAggregateView {
	if q == "" {
		return rows
	}
	needle := strings.ToLower(q)
	out := make([]bridge.ModelAggregateView, 0, len(rows))
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.PublicModel), needle) ||
			strings.Contains(strings.ToLower(r.UpstreamModel), needle) ||
			strings.Contains(strings.ToLower(r.ChannelName), needle) {
			out = append(out, r)
		}
	}
	return out
}

// toggleModelRow 落单条渠道模型记录的启停——聚合页唯一写操作。只切本行
// 同名对外模型的其他渠道行不受波及（行 = 渠道模型记录，非对外模型合并
// 行，评审裁定 4）。
func (u *uiState) toggleModelRow(id string, on bool) {
	_ = bridge.ModelsAgg{A: u.svc.Channels.A}.SetEnabled(id, on)
}

// modelsView 是模型聚合页（spec §5.4）：跨渠道只读展平 + 每行启停 Switch
// + 页头搜索。增删改入口收敛在渠道详情——点来源渠道 chip 直达。行序 =
// filterModelRows(sortModelRows(ListAll())…)：同名对外模型相邻归组，组内
// 渠道名升序。
func (u *uiState) modelsView(c *ui.Context) {
	t := c.Theme()
	s := softs(t)
	rows := bridge.ModelsAgg{A: u.svc.Channels.A}.ListAll()
	sortModelRows(rows)
	rows = filterModelRows(rows, u.modelQuery)

	// 页头统计（spec §5.4）：N 条渠道模型 · 来自 M 渠道 · 停用 X——
	// 三个数都取过滤后行集；M 按渠道 id 去重（同名渠道仍算两个）。
	off := 0
	chans := map[string]bool{}
	for _, r := range rows {
		if !r.Enabled {
			off++
		}
		chans[r.ChannelID] = true
	}
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "模型").Bold().FontSize(t.FontSize + 2)
			ui.Textf(c, "%d 条渠道模型 · 来自 %d 渠道 · 停用 %d", len(rows), len(chans), off).
				TextColor(t.TextMuted)
			ui.Box(c).Grow(1)
			ui.SearchField(c, &u.modelQuery).Width(240)
		})
		if len(rows) == 0 {
			// 空态：无数据引导去渠道页；搜索落空则提示改关键词（非空白）。
			if u.modelQuery != "" {
				ui.Text(c, "无匹配模型——试试其它关键词").TextColor(t.TextMuted)
			} else {
				ui.Text(c, "尚无模型——在渠道页为渠道添加公开模型").TextColor(t.TextMuted)
			}
			return
		}
		cols := []ui.TableColumn{
			{Title: "启用", Width: 64},
			{Title: "对外模型"},
			{Title: "来源渠道", Width: 150},
			{Title: "上游映射"},
		}
		ui.Table(c, &u.modelList, cols, len(rows), func(row, col int) {
			m := rows[row]
			switch col {
			case 0:
				// 行内启停：只切本行记录。Switch 是比表行更内层的交互件，
				// 命中即截获、不冒泡成行选中（input.go hitChain 取最内）。
				on := m.Enabled
				if ui.Switch(c, &on).Label("启用 " + m.ChannelName + " 的 " + m.PublicModel).Changed() {
					u.toggleModelRow(m.ID, on)
				}
			case 1:
				ui.Text(c, m.PublicModel).Bold().SingleLine()
			case 2:
				// 来源渠道 chip = 增删改入口（决策 7）：点进该渠道详情。
				b := ui.ButtonBase(c).Children(func() {
					softBadge(c, t, s, "gray", m.ChannelName)
				})
				if b.Clicked() {
					u.openChannelByName(m.ChannelName)
				}
			case 3:
				ui.Text(c, m.UpstreamModel).SingleLine().Font(monoFont).TextColor(t.TextMuted)
			}
		}).Grow(1)
	})
}

// openChannelByName 按渠道名跳详情：聚合行只带 ChannelName，回查列表得 id；
// 查不到（行是孤儿或重名取首个）不跳。
func (u *uiState) openChannelByName(name string) {
	for _, ch := range u.svc.Channels.List() {
		if ch.Name == name {
			u.openChannelDetail(ch.ID)
			return
		}
	}
}
