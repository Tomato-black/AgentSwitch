// logicalmodels.go 是逻辑模型列表页与新建/编辑页（spec §6.2 / Task 12–13）：
// 列表＝卡片流（名称、enabled 徽章、目标链摘要）+ 行内操作（编辑/删除/启用
// 停用）+ 空态引导；删除走 AlertDialog 二次确认。编辑页＝名称/描述 + 目标链
// 行编辑（渠道下拉、模型名输入或建议选择、上移/下移/删除、添加目标）+ 保存
// （Create/Update）。纯助手 moveTarget / lmForm.validate 供测试直断言；渲染
// 薄壳不测字符串细节。
package desktop

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

// lmForm 是逻辑模型新建/编辑表单状态（Task 13）：targets 即目标链行
// （ChannelID/ModelName 随行编辑，Priority 落库前按下标重排）；msg 是
// 表单信息位（校验/保存错误，channels 页同款模式）。
type lmForm struct {
	name        string
	description string
	targets     []bridge.TargetSpec
	msg         string
}

// moveTarget 把 targets[i] 与相邻行换位（delta=-1 上移 / +1 下移）；首行
// 上移、末行下移与越界下标都是 no-op。只换序不写 Priority——落库前按下标
// 重排（saveLogicalModel）。
func moveTarget(targets []bridge.TargetSpec, i, delta int) {
	j := i + delta
	if i < 0 || i >= len(targets) || j < 0 || j >= len(targets) {
		return
	}
	targets[i], targets[j] = targets[j], targets[i]
}

// validate 是保存前校验：名称必填、至少一个目标、每行渠道与模型名必填。
// 错误信息中文、进表单信息位。
func (f lmForm) validate() error {
	if strings.TrimSpace(f.name) == "" {
		return fmt.Errorf("需要填写模型名称")
	}
	if len(f.targets) == 0 {
		return fmt.Errorf("至少需要一个目标")
	}
	for i, tg := range f.targets {
		if tg.ChannelID == "" {
			return fmt.Errorf("目标 %d：需要选择渠道", i+1)
		}
		if strings.TrimSpace(tg.ModelName) == "" {
			return fmt.Errorf("目标 %d：需要填写模型名", i+1)
		}
	}
	return nil
}

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
			u.openLogicalNew()
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
					u.openLogicalEdit(m.ID)
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

// openLogicalNew 进新建页并复位表单（editModelID 空=新建）。
func (u *uiState) openLogicalNew() {
	u.editModelID = ""
	u.lmEdit = lmForm{}
	u.page = "logical-edit"
}

// openLogicalEdit 进编辑页并回显表单：名称/描述/目标链取自 Logical.Get，
// 目标行 Priority 保序（保存前按下标重排）。模型已删则信息位提示。
func (u *uiState) openLogicalEdit(id string) {
	u.editModelID = id
	u.lmEdit = lmForm{}
	if id == "" {
		u.page = "logical-edit"
		return
	}
	if u.svc.Logical != nil {
		if m, err := u.svc.Logical.Get(id); err != nil {
			u.lmEdit.msg = "逻辑模型不存在或已删除"
		} else {
			u.lmEdit.name = m.Name
			u.lmEdit.description = m.Description
			for _, tg := range m.Targets {
				u.lmEdit.targets = append(u.lmEdit.targets, bridge.TargetSpec{
					ChannelID: tg.ChannelID, ModelName: tg.ModelName, Priority: tg.Priority,
				})
			}
		}
	}
	u.page = "logical-edit"
}

// saveLogicalModel 落「保存」：校验 → Priority 按下标重排 → 新建走 Create、
// 编辑走 Update（enabled 保持原值——表单不含启用开关）；成功回列表页，
// 失败错误进表单信息位（channels 页同款模式）。
func (u *uiState) saveLogicalModel() {
	if err := u.lmEdit.validate(); err != nil {
		u.lmEdit.msg = err.Error()
		return
	}
	if u.svc.Logical == nil {
		u.lmEdit.msg = "逻辑模型服务未就绪"
		return
	}
	targets := make([]bridge.TargetSpec, len(u.lmEdit.targets))
	for i, tg := range u.lmEdit.targets {
		targets[i] = bridge.TargetSpec{ChannelID: tg.ChannelID, ModelName: tg.ModelName, Priority: i}
	}
	name := strings.TrimSpace(u.lmEdit.name)
	if u.editModelID == "" {
		if _, err := u.svc.Logical.Create(name, u.lmEdit.description, bridge.ModelMeta{}, targets); err != nil {
			u.lmEdit.msg = err.Error()
			return
		}
	} else {
		enabled := true
		if m, err := u.svc.Logical.Get(u.editModelID); err == nil {
			enabled = m.Enabled
		}
		if err := u.svc.Logical.Update(u.editModelID, name, u.lmEdit.description, bridge.ModelMeta{}, enabled, targets); err != nil {
			u.lmEdit.msg = err.Error()
			return
		}
	}
	u.page = "logical"
}

// logicalModelEditView 是新建/编辑页（spec §6.2 / Task 13）：名称/描述 +
// 目标链行编辑（渠道下拉=Channel list、模型名输入或建议选择、上移/下移/
// 删除）+ 添加目标 + 保存。editModelID 空=新建；校验/保存错误进表单信息位，
// 保存成功回列表页（saveLogicalModel）。
func (u *uiState) logicalModelEditView(c *ui.Context) {
	t := c.Theme()
	s := softs(t)
	chans := u.svc.Channels.List()
	title := "新建逻辑模型"
	if u.editModelID != "" {
		title = "编辑逻辑模型"
	}
	ui.Scroll(c).Grow(1).Children(func() {
		ui.Column(c).Gap(12).Children(func() {
			ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
				if ui.Button(c, "返回列表").Clicked() {
					u.page = "logical"
				}
				ui.Text(c, title).Bold().FontSize(t.FontSize + 2)
			})
			card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Text(c, "基本信息").Bold()
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "名称:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &u.lmEdit.name).Grow(1).Placeholder("网关对外模型名，如 my-code")
					})
					ui.Row(c).Gap(12).Children(func() {
						ui.Text(c, "描述:").Width(96).TextColor(t.TextMuted)
						ui.TextInput(c, &u.lmEdit.description).Grow(1)
					})
				})
			})
			card(c, t, func() {
				ui.Column(c).Gap(12).Children(func() {
					ui.Text(c, "目标链").Bold()
					ui.Text(c, "按序故障转移：靠前的目标优先").TextColor(t.TextMuted)
					if len(chans) == 0 {
						ui.Text(c, "还没有渠道——先在「渠道」页添加").TextColor(t.TextMuted)
					}
					// 行内按钮只记动作，循环结束后统一应用：构建中改切片会
					// 让 range 跳行/越界。
					act, actI := "", -1
					for i := range u.lmEdit.targets {
						if a := u.lmTargetRow(c, t, s, i, chans); a != "" {
							act, actI = a, i
						}
					}
					switch act {
					case "up":
						moveTarget(u.lmEdit.targets, actI, -1)
					case "down":
						moveTarget(u.lmEdit.targets, actI, +1)
					case "del":
						u.lmEdit.targets = append(u.lmEdit.targets[:actI], u.lmEdit.targets[actI+1:]...)
					}
					if softButton(c, t, s, "act", "＋ 添加目标").Clicked() {
						u.lmEdit.targets = append(u.lmEdit.targets, bridge.TargetSpec{})
					}
				})
			})
			ui.Row(c).Gap(8).Children(func() {
				if pillButton(c, t, "保存").Clicked() {
					u.saveLogicalModel()
				}
				ui.Box(c).Grow(1)
			})
			if u.lmEdit.msg != "" {
				ui.Text(c, u.lmEdit.msg).TextColor(t.Danger)
			}
		})
	})
}

// lmTargetRow 渲染一行目标编辑控件并返回行操作（""|"up"|"down"|"del"），
// 由调用方在循环后统一应用。渠道用 SelectBase 绑 ChannelID、显示渠道名；
// 模型名有建议时走建议下拉、否则文本输入。
func (u *uiState) lmTargetRow(c *ui.Context, t *ui.Theme, s softPalette, i int, chans []bridge.ChannelView) string {
	tg := &u.lmEdit.targets[i]
	act := ""
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, fmt.Sprintf("%d.", i+1)).Width(20).TextColor(t.TextMuted)
		lmChannelSelect(c, t, &tg.ChannelID, chans)
		sugg := u.svc.Channels.Suggestions(tg.ChannelID)
		if len(sugg) > 0 {
			opts := sugg
			if tg.ModelName != "" && !slices.Contains(opts, tg.ModelName) {
				opts = append([]string{tg.ModelName}, sugg...)
			}
			ui.Select(c, &tg.ModelName, opts).Grow(1)
		} else {
			ui.TextInput(c, &tg.ModelName).Grow(1).Placeholder("模型名")
		}
		if softButton(c, t, s, "gray", "上移").Clicked() {
			act = "up"
		}
		if softButton(c, t, s, "gray", "下移").Clicked() {
			act = "down"
		}
		if softButton(c, t, s, "danger", "删除").Clicked() {
			act = "del"
		}
	})
	return act
}

// lmChannelSelect 是目标行的渠道下拉（spec §6.2「渠道下拉=Channel list」）：
// SelectBase 值绑 ChannelID，触发器与选项显示渠道名。
func lmChannelSelect(c *ui.Context, t *ui.Theme, id *string, chans []bridge.ChannelView) {
	label := "选择渠道"
	for _, ch := range chans {
		if ch.ID == *id {
			label = ch.Name
			break
		}
	}
	sel := ui.SelectBase(c, id)
	sel.Trigger.Padding(6, 10).Border(1, t.Border).Radius(t.Radius).
		Background(t.Surface).MinWidth(140).Children(func() {
		ui.Text(c, label).SingleLine()
	})
	sel.Popup(func(panel ui.Element) {
		panel.Padding(4).Background(t.Surface).Border(1, t.Border).Radius(t.Radius)
		for _, ch := range chans {
			item := sel.Item(ch.ID).Padding(6, 10).Radius(t.Radius)
			switch {
			case item.Highlighted():
				item.Background(t.Accent).TextColor(t.AccentText)
			case ch.ID == *id:
				item.Background(t.SurfaceHover)
			}
			item.Children(func() { ui.Text(c, ch.Name).SingleLine() })
		}
	})
}
