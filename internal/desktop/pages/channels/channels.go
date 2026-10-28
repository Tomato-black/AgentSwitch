// Package channels 是渠道页：列表（单 Key 语义 + 近况统计）、预设网格新建
// 页与详情页（协议可编辑、Key 更新留空不改、建议模型、连通测试、删除引用
// 警告）。实现按视图分文件：channels_list/new/detail；表单与状态流转在
// channels_form.go。
package channels

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/state"
)

func Page(c *ui.Context, st *state.State) {
	// 先处理拉取模型的确认弹窗：按钮点击发生在视图渲染中，若同帧再调用
	// AlertDialog 会被框架当作「已打开」立即结算，选择结果丢失。
	fetchConfirm(c, st)
	switch st.Channels.View {
	case "new":
		newView(c, st)
	case "detail":
		detailView(c, st)
	default:
		listView(c, st)
	}
}

// fetchConfirm 是「拉取模型」在已有建议文本时的追加/替换确认：空文本直接
// 填入，不弹窗；确认后才写入目标文本框。
func fetchConfirm(c *ui.Context, st *state.State) {
	if !st.Channels.FetchArm {
		return
	}
	ids := st.Channels.FetchIDs
	target := st.Channels.FetchTarget
	msg := fmt.Sprintf("上游返回 %d 个模型。追加到现有建议文本末尾，还是整体替换？", len(ids))
	choice := ui.AlertDialog(c, &st.Channels.FetchArm, "拉取模型", msg, "取消", "追加", "替换")
	if choice == -1 { // 仍在等待选择：保留暂存结果
		return
	}
	switch choice {
	case 1:
		if target == "detail" {
			st.Channels.SuggText = appendModelIDs(st.Channels.SuggText, ids)
			st.Channels.TestMsg = fmt.Sprintf("已追加 %d 个模型（保存后生效）", len(ids))
		} else {
			f := &st.Channels.Form
			f.Suggestions = appendModelIDs(f.Suggestions, ids)
			f.Msg = fmt.Sprintf("已追加 %d 个模型（创建后生效）", len(ids))
		}
	case 2:
		if target == "detail" {
			st.Channels.SuggText = strings.Join(ids, "\n")
			st.Channels.TestMsg = fmt.Sprintf("已替换为上游 %d 个模型（保存后生效）", len(ids))
		} else {
			f := &st.Channels.Form
			f.Suggestions = strings.Join(ids, "\n")
			f.Msg = fmt.Sprintf("已替换为上游 %d 个模型（创建后生效）", len(ids))
		}
	case 0, ui.AlertDismissed:
	}
	st.Channels.FetchIDs = nil
	st.Channels.FetchTarget = ""
}
