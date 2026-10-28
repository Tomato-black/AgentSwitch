// Package logical 是逻辑模型页：卡片列表（启用徽章/目标链摘要/行内操作 +
// 删除二次确认）与新建/编辑页（名称/描述/元数据/目标链编辑）。实现按
// 表单流转（logical_form.go）与渲染（logical_view.go）分文件。
package logical

import (
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/state"
)

// Page 是逻辑模型页入口（组合根 pageView 分发）：编辑态渲染新建/编辑页，
// 其余（含零值与未知 id 的兜底）渲染列表页。
func Page(c *ui.Context, st *state.State) {
	if st.Page == "logical-edit" {
		editView(c, st)
		return
	}
	listView(c, st)
}
