package desktop

import (
	"github.com/egoist/mygo/ui"
)

// modelFormData 是模型页的新建/编辑表单；editID 非空 = 编辑该 id。
type modelFormData struct {
	public, upstream, msg, editID string
}

// modelsView 是模型别名页（Agent 请求名 → 上游模型名）：两列表格、表单与
// 操作行（保存、载入所选、删除两段式确认）。
func (u *uiState) modelsView(c *ui.Context) {
	models := u.svc.Models.List()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "模型别名（Agent 请求名 → 上游模型名）")
		cols := []ui.TableColumn{{Title: "公开模型名"}, {Title: "上游模型名"}}
		ui.Table(c, &u.modelList, cols, len(models), func(row, col int) {
			m := models[row]
			if col == 0 {
				ui.Text(c, m.PublicModel).SingleLine()
			} else {
				ui.Text(c, m.UpstreamModel).SingleLine()
			}
		}).Grow(1)
		ui.Text(c, "公开模型名:")
		ui.TextInput(c, &u.modelForm.public)
		ui.Text(c, "上游模型名:")
		ui.TextInput(c, &u.modelForm.upstream)
		if u.modelForm.msg != "" {
			ui.Text(c, u.modelForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			// 保存（editID 空 = Create）/ 更新（editID 非空 = Update）；别名
			// 唯一等存储错误一律进 msg，不静默丢弃。
			if ui.PrimaryButton(c, "保存").Clicked() {
				if u.modelForm.editID == "" {
					if _, err := u.svc.Models.Create(u.modelForm.public, u.modelForm.upstream); err != nil {
						u.modelForm.msg = err.Error()
					} else {
						u.modelForm = modelFormData{}
					}
				} else {
					if err := u.svc.Models.Update(u.modelForm.editID, u.modelForm.public, u.modelForm.upstream); err != nil {
						u.modelForm.msg = err.Error()
					} else {
						u.modelForm = modelFormData{}
					}
				}
			}
			// 载入所选：modelList.Selected 是 *int（nil=未选，-1=无），判空并
			// 夹紧界后索引（与供应商/账号页同模式）；整表回填顺带清掉过期 msg。
			if ui.Button(c, "载入所选").Clicked() {
				if sel := u.modelList.Selected; sel != nil && *sel >= 0 && *sel < len(models) {
					m := models[*sel]
					u.modelForm = modelFormData{public: m.PublicModel, upstream: m.UpstreamModel, editID: m.ID}
				}
			}
			// 删除两段式（裁定 3，spec 要求）：首击只置确认位，须已载入 editID。
			if ui.Button(c, "删除").Clicked() && u.modelForm.editID != "" {
				u.modelConfirmDelete = true
			}
			if u.modelConfirmDelete {
				if ui.Button(c, "确认删除").Clicked() {
					if err := u.svc.Models.Remove(u.modelForm.editID); err != nil {
						// 失败：保留 editID 供重试，错误进 msg
						u.modelForm.msg = err.Error()
					} else {
						u.modelForm = modelFormData{}
					}
					u.modelConfirmDelete = false
				}
			}
		})
	})
}
