// Package channels 是渠道页：列表（单 Key 语义 + 近况统计）、预设网格新建
// 页与详情页（协议可编辑、Key 更新留空不改、建议模型、连通测试、删除引用
// 警告）。实现按视图分文件：channels_list/new/detail；表单与状态流转在
// channels_form.go。
package channels

import (
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/state"
)

func Page(c *ui.Context, st *state.State) {
	switch st.Channels.View {
	case "new":
		newView(c, st)
	case "detail":
		detailView(c, st)
	default:
		listView(c, st)
	}
}
