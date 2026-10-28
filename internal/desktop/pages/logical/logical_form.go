// logical_form.go 是逻辑模型页的表单与状态流转：思考等级/元数据解析、
// 保存校验、目标链排序摘要与新建/编辑/保存动作（纯助手供测试直断言）。
package logical

import (
	"fmt"
	"strings"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
)

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
func validate(f state.LMForm) error {
	if strings.TrimSpace(f.Name) == "" {
		return fmt.Errorf("需要填写模型名称")
	}
	if len(f.Targets) == 0 {
		return fmt.Errorf("至少需要一个目标")
	}
	for i, tg := range f.Targets {
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

// openLogicalNew 进新建页并复位表单（editModelID 空=新建）。
func openNew(st *state.State) {
	st.Logical.EditModelID = ""
	st.Logical.Edit = state.LMForm{}
	st.Page = "logical-edit"
}

// openLogicalEdit 进编辑页并回显表单：名称/描述/目标链取自 Logical.Get，
// 目标行 Priority 保序（保存前按下标重排）。模型已删则信息位提示。
func openEdit(st *state.State, id string) {
	st.Logical.EditModelID = id
	st.Logical.Edit = state.LMForm{}
	if id == "" {
		st.Page = "logical-edit"
		return
	}
	if st.Svc.Logical != nil {
		if m, err := st.Svc.Logical.Get(id); err != nil {
			st.Logical.Edit.Msg = "逻辑模型不存在或已删除"
		} else {
			st.Logical.Edit.Name = m.Name
			st.Logical.Edit.Description = m.Description
			for _, tg := range m.Targets {
				st.Logical.Edit.Targets = append(st.Logical.Edit.Targets, bridge.TargetSpec{
					ChannelID: tg.ChannelID, ModelName: tg.ModelName, Priority: tg.Priority,
				})
			}
		}
	}
	st.Page = "logical-edit"
}

// saveLogicalModel 落「保存」：校验 → Priority 按下标重排 → 新建走 Create、
// 编辑走 Update（enabled 保持原值——表单不含启用开关）；成功回列表页，
// 失败错误进表单信息位（channels 页同款模式）。
func save(st *state.State) {
	if err := validate(st.Logical.Edit); err != nil {
		st.Logical.Edit.Msg = err.Error()
		return
	}
	if st.Svc.Logical == nil {
		st.Logical.Edit.Msg = "逻辑模型服务未就绪"
		return
	}
	// Codex 元数据（显示名/上下文窗口/思考等级）已移至接入页维护：
	// 保存时保留既有值，新建模型不带元数据。
	var modelMeta bridge.ModelMeta
	if st.Logical.EditModelID != "" {
		if m, err := st.Svc.Logical.Get(st.Logical.EditModelID); err == nil {
			modelMeta = bridge.ModelMeta{
				DisplayName: m.DisplayName, ContextWindow: m.ContextWindow,
				ReasoningLevels: m.ReasoningLevels, DefaultReasoningLevel: m.DefaultReasoningLevel,
			}
		}
	}
	targets := make([]bridge.TargetSpec, len(st.Logical.Edit.Targets))
	for i, tg := range st.Logical.Edit.Targets {
		targets[i] = bridge.TargetSpec{ChannelID: tg.ChannelID, ModelName: tg.ModelName, Priority: i}
	}
	name := strings.TrimSpace(st.Logical.Edit.Name)
	if st.Logical.EditModelID == "" {
		if _, err := st.Svc.Logical.Create(name, st.Logical.Edit.Description, modelMeta, targets); err != nil {
			st.Logical.Edit.Msg = err.Error()
			return
		}
	} else {
		enabled := true
		if m, err := st.Svc.Logical.Get(st.Logical.EditModelID); err == nil {
			enabled = m.Enabled
		}
		if err := st.Svc.Logical.Update(st.Logical.EditModelID, name, st.Logical.Edit.Description, modelMeta, enabled, targets); err != nil {
			st.Logical.Edit.Msg = err.Error()
			return
		}
	}
	st.Page = "logical"
}

// hasDanglingTarget 报告目标链是否含已失效目标（渠道被删后 ChannelName
// 解析为空）：列表据此打「目标失效」警示，避免运行期静默跳过。
func hasDanglingTarget(m bridge.ModelView) bool {
	for _, tg := range m.Targets {
		if tg.ChannelID != "" && tg.ChannelName == "" {
			return true
		}
	}
	return false
}
