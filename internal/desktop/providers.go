package desktop

import (
	"strconv"

	"github.com/egoist/mygo/ui"
)

// providersView 是供应商页（spec §5.2）：页头记数、供应商表（启用列
// Badge）、丝印式表单（固定宽弱色标签 + 字段同行）与操作行。
func (u *uiState) providersView(c *ui.Context) {
	t := c.Theme()
	provs := u.svc.Providers.List()
	ui.Column(c).Gap(12).Children(func() {
		// 页头：标题 + TextMuted 记数（不用间隔点连接）
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "供应商").Bold().FontSize(t.FontSize + 2)
			ui.Text(c, strconv.Itoa(len(provs))).TextColor(t.TextMuted)
		})
		if len(provs) == 0 {
			ui.Text(c, "尚无供应商——在下方表单创建第一个").TextColor(t.TextMuted)
		}
		cols := []ui.TableColumn{{Title: "名称"}, {Title: "协议"}, {Title: "Base URL"}, {Title: "启用"}}
		ui.Table(c, &u.provList, cols, len(provs), func(row, col int) {
			p := provs[row]
			switch col {
			case 0:
				ui.Text(c, p.Name).SingleLine()
			case 1:
				ui.Text(c, p.Protocol).SingleLine()
			case 2:
				ui.Text(c, p.BaseURL).SingleLine().Font(monoFont)
			case 3:
				if p.Enabled {
					ui.Badge(c, "启用")
				} else {
					ui.Text(c, "停用").TextColor(t.TextMuted)
				}
			}
		}).Grow(1)
		ui.Divider(c)

		// 表单：丝印式两栏（spec §5.2；已知偏离：标签左对齐固定宽，
		// 文字右对齐 API 未验证不假定）
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.provForm.name).Grow(1)
			ui.Text(c, "协议:").Width(96).TextColor(t.TextMuted)
			ui.Select(c, &u.provForm.protocol, []string{"anthropic"})
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "Base URL:").Width(96).TextColor(t.TextMuted)
			ui.TextInput(c, &u.provForm.baseURL).Grow(1)
		})
		ui.Row(c).Gap(16).Children(func() {
			ui.Text(c, "启用").TextColor(t.TextMuted)
			ui.Switch(c, &u.provForm.enabled)
			ui.Text(c, "允许私网上游").TextColor(t.TextMuted)
			ui.Switch(c, &u.provForm.allowPrivate)
		})
		if u.provForm.msg != "" {
			ui.Text(c, u.provForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			saveLabel := "保存"
			if u.provForm.editID != "" {
				saveLabel = "更新"
			}
			if ui.PrimaryButton(c, saveLabel).Clicked() {
				if u.provForm.editID == "" {
					if _, err := u.svc.Providers.Create(u.provForm.name, u.provForm.protocol,
						u.provForm.baseURL, u.provForm.enabled, u.provForm.allowPrivate); err != nil {
						u.provForm.msg = err.Error()
					} else {
						u.resetProvForm()
					}
				} else {
					if err := u.svc.Providers.Update(u.provForm.editID, u.provForm.name,
						u.provForm.baseURL, u.provForm.enabled, u.provForm.allowPrivate); err != nil {
						u.provForm.msg = err.Error()
					} else {
						u.resetProvForm()
					}
				}
			}
			if ui.Button(c, "载入所选").Clicked() {
				if sel := u.provList.Selected; sel != nil && *sel >= 0 && *sel < len(provs) {
					p := provs[*sel]
					u.provForm = provFormData{name: p.Name, protocol: p.Protocol, baseURL: p.BaseURL,
						enabled: p.Enabled, allowPrivate: p.AllowPrivateUpstream, editID: p.ID}
					u.provTestMsg = ""
					u.provConfirmDelete = false
				}
			}
			if ui.Button(c, "删除").Clicked() && u.provForm.editID != "" {
				u.provConfirmDelete = true
			}
			if u.provConfirmDelete {
				if ui.Button(c, "确认删除").Clicked() {
					if err := u.svc.Providers.Remove(u.provForm.editID); err != nil {
						u.provForm.msg = err.Error()
					} else {
						u.resetProvForm()
					}
					u.provConfirmDelete = false
				}
			}
			// 测试连通：同步调用——Probe 最多 15s 阻塞 UI 线程，对 P0-A 可接受（简报修正条款 1）。
			if ui.Button(c, "测试连通").Clicked() && u.provForm.editID != "" {
				res := u.svc.Providers.Test(u.provForm.editID)
				if res.OK {
					u.provTestMsg = "连通正常 (" + res.Stage + ")"
				} else {
					u.provTestMsg = res.Stage + " 失败: " + res.Error
				}
			}
		})
		if u.provTestMsg != "" {
			ui.Text(c, u.provTestMsg)
		}
	})
}

// resetProvForm 复位表单到新建默认态（协议 anthropic、启用开）并清掉已
// 过期的连通结果——新建/更新/删除成功后调用；失败路径保留原表单与 msg。
func (u *uiState) resetProvForm() {
	u.provForm = provFormData{protocol: "anthropic", enabled: true}
	u.provTestMsg = ""
}
