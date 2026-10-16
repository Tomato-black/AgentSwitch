package desktop

import (
	"strconv"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// accFormData 是账号页的新建/编辑表单。providerName 存供应商名称（下拉按名
// 选择，提交时经 provByID 映射回 ID）；providerID 记录已载入的供应商 ID（名
// 称映射不到时兜底）。apiKey 只在输入时存在：保存/轮换成功即清空，载入所选
// 整表回填——已有密钥永不回显。priorityInput 是字符串，提交时 strconv.Atoi。
type accFormData struct {
	name, providerID, providerName, apiKey, priorityInput string
	enabled, allowFallback                                bool
	msg, editID                                           string // editID 非空 = 编辑该 id
}

// accountsView 是账号页（spec §5.3）：页顶降级警示横幅（Warning 文字 +
// 边框，文案不变）、六列表（密钥尾4/存储等宽）、丝印表单与操作行。
func (u *uiState) accountsView(c *ui.Context) {
	t := c.Theme()
	accounts := u.svc.Accounts.List()
	provs := u.svc.Providers.List()
	provNames := make([]string, 0, len(provs))
	provByID := map[string]string{}
	for _, p := range provs {
		provNames = append(provNames, p.Name)
		provByID[p.Name] = p.ID
	}
	ui.Column(c).Gap(12).Children(func() {
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "账号").Bold().FontSize(t.FontSize + 2)
			ui.Text(c, strconv.Itoa(len(accounts))).TextColor(t.TextMuted)
		})
		// 降级警示横幅：任一账号密钥落 fallback-file 即页顶常驻（文案逐字保留）
		if credentialFallbackInUse(accounts) {
			ui.Row(c).Gap(8).Padding(8).Border(1, t.Warning).Children(func() {
				ui.Text(c, "警告：凭据使用文件降级存储（未启用系统密钥环）").TextColor(t.Warning).Grow(1)
			})
		}
		if len(accounts) == 0 {
			ui.Text(c, "尚无账号——在下方表单添加第一个").TextColor(t.TextMuted)
		}
		cols := []ui.TableColumn{{Title: "名称"}, {Title: "供应商"}, {Title: "优先级"},
			{Title: "启用"}, {Title: "密钥尾4"}, {Title: "存储"}}
		ui.Table(c, &u.accList, cols, len(accounts), func(row, col int) {
			a := accounts[row]
			switch col {
			case 0:
				ui.Text(c, a.Name).SingleLine()
			case 1:
				ui.Text(c, provNameOf(provs, a.ProviderID)).SingleLine()
			case 2:
				ui.Text(c, strconv.Itoa(a.Priority)).SingleLine().Font(monoFont)
			case 3:
				if a.Enabled {
					ui.Badge(c, "启用")
				} else {
					ui.Text(c, "停用").TextColor(t.TextMuted)
				}
			// 密钥尾4 列取自 List 的持久化 key_tail（spec §12.4，Create/轮换时写入）；
			// migration 002 之前建的历史行留空直到下次轮换。
			case 4:
				ui.Text(c, a.KeyTail).SingleLine().Font(monoFont)
			case 5:
				ui.Text(c, a.SecretBackend).SingleLine().Font(monoFont)
			}
		}).Grow(1)
		ui.Divider(c)

		// 表单：三行丝印（名称+优先级一行；供应商一行；密钥独占）
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "名称:").Width(120).TextColor(t.TextMuted)
			ui.TextInput(c, &u.accForm.name).Grow(1)
			ui.Text(c, "优先级（数字越小越优先）:").Width(160).TextColor(t.TextMuted)
			ui.TextInput(c, &u.accForm.priorityInput).Grow(1)
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "供应商:").Width(120).TextColor(t.TextMuted)
			ui.Select(c, &u.accForm.providerName, provNames).Grow(1)
		})
		ui.Row(c).Gap(12).Children(func() {
			ui.Text(c, "API Key（保存后不可回显）:").Width(160).TextColor(t.TextMuted)
			ui.TextInput(c, &u.accForm.apiKey).Grow(1)
		})
		ui.Row(c).Gap(16).Children(func() {
			ui.Text(c, "启用").TextColor(t.TextMuted)
			ui.Switch(c, &u.accForm.enabled)
			ui.Text(c, "允许文件降级").TextColor(t.TextMuted)
			ui.Switch(c, &u.accForm.allowFallback)
		})
		if u.accForm.msg != "" {
			ui.Text(c, u.accForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			label := "保存"
			if u.accForm.editID != "" {
				label = "更新"
			}
			if ui.PrimaryButton(c, label).Clicked() {
				u.submitAccount(provByID)
			}
			if ui.Button(c, "载入所选").Clicked() {
				if sel := u.accList.Selected; sel != nil && *sel >= 0 && *sel < len(accounts) {
					a := accounts[*sel]
					u.accForm = accFormData{name: a.Name, providerID: a.ProviderID,
						providerName:  provNameOf(provs, a.ProviderID),
						priorityInput: strconv.Itoa(a.Priority),
						enabled:       a.Enabled, editID: a.ID}
					u.accTestMsg = ""
					u.accConfirmDelete = false
				}
			}
			if ui.Button(c, "停用/启用").Clicked() {
				if sel := u.accList.Selected; sel != nil && *sel >= 0 && *sel < len(accounts) {
					a := accounts[*sel]
					_ = u.svc.Accounts.SetEnabled(a.ID, !a.Enabled)
				}
			}
			if ui.Button(c, "轮换密钥").Clicked() && u.accForm.editID != "" && u.accForm.apiKey != "" {
				if err := u.svc.Accounts.RotateSecret(u.accForm.editID, u.accForm.apiKey, u.accForm.allowFallback); err != nil {
					u.accForm.msg = err.Error()
				} else {
					u.accForm.msg = "密钥已轮换"
					u.accForm.apiKey = ""
				}
			}
			if ui.Button(c, "测试").Clicked() {
				id := u.accForm.editID
				if id == "" {
					if sel := u.accList.Selected; sel != nil && *sel >= 0 && *sel < len(accounts) {
						id = accounts[*sel].ID
					}
				}
				if id != "" {
					res := u.svc.Accounts.Test(id)
					if res.OK {
						u.accTestMsg = "连通正常 (" + res.Stage + ")"
					} else {
						u.accTestMsg = res.Stage + " 失败: " + res.Error
					}
				}
			}
			if ui.Button(c, "删除").Clicked() && u.accForm.editID != "" {
				u.accConfirmDelete = true
			}
			if u.accConfirmDelete {
				if ui.Button(c, "确认删除").Clicked() {
					if err := u.svc.Accounts.Remove(u.accForm.editID); err != nil {
						u.accForm.msg = err.Error()
					} else {
						u.resetAccForm()
					}
					u.accConfirmDelete = false
				}
			}
		})
		if u.accTestMsg != "" {
			ui.Text(c, u.accTestMsg)
		}
	})
}

// submitAccount 分支：无供应商可映射 → 报错；优先级非整数 → 报错（密钥保留
// 供重试）；Create 成功 → 复位表单（密钥清空）；Update → 有新密钥先轮换再改
// 名称/优先级。失败一律把错误写进 accForm.msg，不落半截数据。
func (u *uiState) submitAccount(provByID map[string]string) {
	pid := provByID[u.accForm.providerName]
	if pid == "" {
		pid = u.accForm.providerID // 名称未选/供应商已删时，退回已载入的 ID
	}
	if pid == "" {
		u.accForm.msg = "请先在供应商页创建供应商"
		return
	}
	prio, err := strconv.Atoi(u.accForm.priorityInput)
	if err != nil {
		u.accForm.msg = "优先级必须是整数"
		return
	}
	if u.accForm.editID == "" {
		if _, err := u.svc.Accounts.Create(u.accForm.name, pid, u.accForm.apiKey, prio,
			u.accForm.enabled, u.accForm.allowFallback); err != nil {
			u.accForm.msg = err.Error()
			return
		}
		u.resetAccForm()
		return
	}
	if u.accForm.apiKey != "" {
		if err := u.svc.Accounts.RotateSecret(u.accForm.editID, u.accForm.apiKey, u.accForm.allowFallback); err != nil {
			u.accForm.msg = err.Error()
			return
		}
	}
	if err := u.svc.Accounts.Update(u.accForm.editID, u.accForm.name, prio); err != nil {
		u.accForm.msg = err.Error()
		return
	}
	u.resetAccForm()
}

// resetAccForm 复位表单到新建默认态（启用开）并清掉已过期的连通测试结果
// ——新建/更新/删除成功后调用；失败路径保留原表单与 msg（与供应商页同模式）。
func (u *uiState) resetAccForm() {
	u.accForm = accFormData{enabled: true}
	u.accTestMsg = ""
}

// credentialFallbackInUse 报告列表中是否有账号的密钥落在 0600 文件降级存储。
func credentialFallbackInUse(views []bridge.AccountView) bool {
	for _, v := range views {
		if v.SecretBackend == "fallback-file" {
			return true
		}
	}
	return false
}

// provNameOf 把供应商 ID 映射回名称；找不到时返回原 ID（作为显示兜底）。
func provNameOf(provs []bridge.ProviderView, id string) string {
	for _, p := range provs {
		if p.ID == id {
			return p.Name
		}
	}
	return id
}
