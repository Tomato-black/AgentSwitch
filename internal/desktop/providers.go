package desktop

import (
	"github.com/egoist/mygo/ui"
)

// providersView 是供应商页：供应商表、新建/编辑表单（名称/协议/Base URL/
// 启用+允许私网）、保存（Create/Update）、载入所选、删除二次确认与连通测试。
func (u *uiState) providersView(c *ui.Context) {
	provs := u.svc.Providers.List()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "供应商")
		cols := []ui.TableColumn{{Title: "名称"}, {Title: "协议"}, {Title: "Base URL"}, {Title: "启用"}}
		ui.Table(c, &u.provList, cols, len(provs), func(row, col int) {
			p := provs[row]
			switch col {
			case 0:
				ui.Text(c, p.Name).SingleLine()
			case 1:
				ui.Text(c, p.Protocol).SingleLine()
			case 2:
				ui.Text(c, p.BaseURL).SingleLine()
			case 3:
				if p.Enabled {
					ui.Text(c, "是")
				} else {
					ui.Text(c, "否")
				}
			}
		}).Grow(1)

		// 表单四要素：名称 / 协议 / Base URL / 启用+允许私网上游
		ui.Text(c, "名称:")
		ui.TextInput(c, &u.provForm.name)
		ui.Text(c, "协议:")
		ui.Select(c, &u.provForm.protocol, []string{"anthropic"})
		ui.Text(c, "Base URL:")
		ui.TextInput(c, &u.provForm.baseURL)
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "启用")
			ui.Switch(c, &u.provForm.enabled)
			ui.Text(c, "允许私网上游")
			ui.Switch(c, &u.provForm.allowPrivate)
		})
		if u.provForm.msg != "" {
			ui.Text(c, u.provForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			// 保存（editID 空 = Create）/ 更新（editID 非空 = Update）
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
			// 载入所选：provList.Selected 是 *int（nil=未选，-1=无），
			// 判空后按 *sel 索引（简报修正条款 2）。
			if ui.Button(c, "载入所选").Clicked() {
				if sel := u.provList.Selected; sel != nil && *sel >= 0 && *sel < len(provs) {
					p := provs[*sel]
					u.provForm = provFormData{name: p.Name, protocol: p.Protocol, baseURL: p.BaseURL,
						enabled: p.Enabled, allowPrivate: p.AllowPrivateUpstream, editID: p.ID}
					u.provTestMsg = ""
				}
			}
			// 删除：editID 非空才进入二次确认（与测试连通同门槛）
			if ui.Button(c, "删除").Clicked() && u.provForm.editID != "" {
				u.provConfirmDelete = true
			}
			if u.provConfirmDelete {
				if ui.Button(c, "确认删除").Clicked() {
					if err := u.svc.Providers.Remove(u.provForm.editID); err != nil {
						// 失败：保留 editID 供重试，错误进 msg
						u.provForm.msg = err.Error()
					} else {
						u.resetProvForm()
					}
					u.provConfirmDelete = false
				}
			}
			// 测试连通：editID 非空才执行；同步调用——Probe 最多 15s 阻塞
			// UI 线程，对 P0-A 可接受（简报修正条款 1）。
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
