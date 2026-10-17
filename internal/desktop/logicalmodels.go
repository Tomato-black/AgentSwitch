// logicalmodels.go 是逻辑模型列表页（spec §6.2 / Task 12）：卡片列表（名称、
// enabled 徽章、目标链摘要）+ 行内操作（编辑/删除/启用停用）+ 空态引导与
// 新建入口；删除走 AlertDialog 二次确认。新建/编辑表单由 Task 13 填进
// logicalModelEditView 占位。
package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// formatTargetChain 把目标链摘要成一行 `渠道A·model → 渠道B·model`（spec
// §6.2）；无目标返回 `（无目标）`。渠道名缺失时退回渠道 id。
func formatTargetChain(m bridge.ModelView) string {
	if len(m.Targets) == 0 {
		return "（无目标）"
	}
	parts := make([]string, 0, len(m.Targets))
	for _, tg := range m.Targets {
		ch := tg.ChannelName
		if ch == "" {
			ch = tg.ChannelID
		}
		parts = append(parts, ch+"·"+tg.ModelName)
	}
	return strings.Join(parts, " → ")
}

// logicalModelsView 是逻辑模型列表页主体：页头新建钮 + 卡片行列表（名称/
// 启用徽章/目标链摘要 + 编辑/删除/启用停用），空态引导「先建渠道，再把渠道
// 挂进逻辑模型」。数据每帧自 u.svc.Logical.List() 取（进程内直调）。
func logicalModelsView(c *ui.Context, u *uiState) {
	t := c.Theme()
	s := softs(t)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "逻辑模型").Bold().FontSize(16).Grow(1)
		if pillButton(c, t, "＋ 新建逻辑模型").Clicked() {
			u.editModelID = ""
			u.page = "logical-edit"
		}
	})
	var models []bridge.ModelView
	if u.svc.Logical != nil {
		models = u.svc.Logical.List()
	}
	if len(models) == 0 {
		card(c, t, func() {
			ui.Text(c, "先建渠道，再把渠道挂进逻辑模型").TextColor(t.TextMuted)
		})
	}
	for _, m := range models {
		card(c, t, func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.Name).Bold().Grow(1).SingleLine()
				if m.Enabled {
					softBadge(c, t, s, "ok", "启用")
				} else {
					softBadge(c, t, s, "gray", "停用")
				}
				if softButton(c, t, s, "gray", "编辑").Clicked() {
					u.editModelID = m.ID
					u.page = "logical-edit"
				}
				if softButton(c, t, s, "danger", "删除").Clicked() {
					u.lmDeleteArm = true
					u.lmDeleteID = m.ID
				}
				toggleLabel := "停用"
				if !m.Enabled {
					toggleLabel = "启用"
				}
				if softButton(c, t, s, "act", toggleLabel).Clicked() && u.svc.Logical != nil {
					u.svc.Logical.SetEnabled(m.ID, !m.Enabled)
				}
			})
			ui.Text(c, formatTargetChain(m)).TextColor(t.TextMuted)
		})
	}
	// 删除二次确认（复用渠道页两段式删除确认交互）：armed 位跨帧保留，
	// 弹窗标题带模型名、确认（下标 1）才 Remove。
	armedName := ""
	for _, m := range models {
		if m.ID == u.lmDeleteID {
			armedName = m.Name
		}
	}
	logicalDeleteConfirm(c, u, armedName)
}

// logicalDeleteConfirm 是删除逻辑模型的 AlertDialog 二次确认：open 位
// lmDeleteArm、对象 lmDeleteID；框架在选定后自关 open 位，Escape 走
// AlertDismissed 同样收场。按钮序「取消, 删除」——末位是主钮。
func logicalDeleteConfirm(c *ui.Context, u *uiState, name string) {
	if !u.lmDeleteArm {
		return
	}
	msg := "确定删除该逻辑模型？"
	if name != "" {
		msg = fmt.Sprintf("确定删除逻辑模型「%s」？", name)
	}
	switch ui.AlertDialog(c, &u.lmDeleteArm, "删除逻辑模型", msg, "取消", "删除") {
	case 1:
		if u.svc.Logical != nil {
			u.svc.Logical.Remove(u.lmDeleteID)
		}
		u.lmDeleteID = ""
	case 0, ui.AlertDismissed:
		u.lmDeleteID = ""
	}
}

// logicalModelEditView 是新建/编辑页占位（Task 13 填名称/描述/目标链表单）；
// editModelID 空=新建。本 Task 只接线 pageView 分发。
func (u *uiState) logicalModelEditView(c *ui.Context) {
	ui.Text(c, "TODO: 逻辑模型编辑")
}
