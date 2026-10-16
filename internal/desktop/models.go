package desktop

import (
	"strconv"

	"github.com/egoist/mygo/ui"
)

// modelFormData 是模型页的新建/编辑表单；editID 非空 = 编辑该 id。
type modelFormData struct {
	public, upstream, msg, editID string
}

// modelsView 是模型别名页（spec §5.2）：页头记数、两列表（上游名等宽）、
// 丝印表单与操作行（保存、载入所选、删除两段式确认）。
func (u *uiState) modelsView(c *ui.Context) {
	t := c.Theme()
	models := u.svc.Models.List()
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "模型别名").Bold().FontSize(t.FontSize + 2)
			ui.Text(c, strconv.Itoa(len(models))).TextColor(t.TextMuted)
		})
		ui.Text(c, "Agent 请求名 → 上游模型名").TextColor(t.TextMuted)
		if len(models) == 0 {
			ui.Text(c, "尚无模型别名——在下方表单添加").TextColor(t.TextMuted)
		}
		cols := []ui.TableColumn{{Title: "公开模型名"}, {Title: "上游模型名"}}
		ui.Table(c, &u.modelList, cols, len(models), func(row, col int) {
			m := models[row]
			if col == 0 {
				ui.Text(c, m.PublicModel).SingleLine()
			} else {
				ui.Text(c, m.UpstreamModel).SingleLine().Font(monoFont)
			}
		}).Grow(1)
		ui.Divider(c)

		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "公开模型名:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.modelForm.public).Grow(1)
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "上游模型名:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.modelForm.upstream).Grow(1)
		})
		if u.modelForm.msg != "" {
			ui.Text(c, u.modelForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
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
			if ui.Button(c, "载入所选").Clicked() {
				if sel := u.modelList.Selected; sel != nil && *sel >= 0 && *sel < len(models) {
					m := models[*sel]
					u.modelForm = modelFormData{public: m.PublicModel, upstream: m.UpstreamModel, editID: m.ID}
					u.modelConfirmDelete = false
				}
			}
			if ui.Button(c, "删除").Clicked() && u.modelForm.editID != "" {
				u.modelConfirmDelete = true
			}
			if u.modelConfirmDelete {
				if ui.Button(c, "确认删除").Clicked() {
					if err := u.svc.Models.Remove(u.modelForm.editID); err != nil {
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
