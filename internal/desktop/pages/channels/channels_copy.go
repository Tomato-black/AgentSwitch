// 复制渠道的确认框（C3）：列表行与详情页共用；默认不复制 Key，勾选后
// bridge Copy(id, true) 连同凭据一起复制。
package channels

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// armChannelCopy 打开复制渠道确认框；after 为复制成功后的落位动作（详情
// 页传 openDetail，列表页传 nil）。
func armChannelCopy(st *state.State, id string, after func(*state.State, string)) {
	st.Channels.CopyID = id
	st.Channels.CopyKey = false
	st.Channels.CopyAfter = after
	st.Channels.CopyArm = true
}

// performChannelCopy 执行复制：目标链联动与提示文案统一（原两处内联逻辑
// 归一），不含 Key 时提示补填。
func performChannelCopy(st *state.State) {
	st.Channels.CopyArm = false
	v, err := st.Svc.Channels.Copy(st.Channels.CopyID, st.Channels.CopyKey)
	if err != nil {
		st.Channels.TestMsg = "复制失败：" + err.Error()
		return
	}
	keyNote := "含 Key"
	if !st.Channels.CopyKey {
		keyNote = "不含 Key"
	}
	msg := "已复制渠道「" + v.Name + "」（" + keyNote + "）"
	if st.Svc.Logical != nil {
		if models, err := st.Svc.Logical.AddCopyToReferencing(st.Channels.CopyID, v.ID); err == nil && len(models) > 0 {
			msg += "；已加入目标链：" + strings.Join(models, "、")
		}
	}
	if after := st.Channels.CopyAfter; after != nil {
		after(st, v.ID)
	}
	if !st.Channels.CopyKey {
		msg += "；请填写 Key"
	}
	st.Channels.TestMsg = msg
}

// copyConfirm 渲染复制确认框：与 AlertDialog 同帧结算问题同 fetchConfirm，
// 在 Page 顶部处理。
func copyConfirm(c *ui.Context, st *state.State) {
	if !st.Channels.CopyArm {
		return
	}
	t := c.Theme()
	uikit.RichDialog(c, t, uikit.RichDialogOpts{
		Open:     &st.Channels.CopyArm,
		Title:    "复制渠道",
		Subtitle: "副本自动加入原渠道被引用的逻辑模型目标链",
		Body: func() {
			ui.Column(c).Gap(t.Space(2)).Children(func() {
				ui.Checkbox(c, &st.Channels.CopyKey, "同时复制 API Key")
				ui.Text(c, "默认不复制：副本需重新填写 Key，避免密钥随渠道扩散。").
					TextColor(t.TextMuted)
			})
		},
		Actions: []uikit.RichDialogAction{
			{Label: "取消", Default: true, OnClick: func() { st.Channels.CopyArm = false }},
			{Label: "确认复制", Primary: true, OnClick: func() { performChannelCopy(st) }},
		},
	})
}
