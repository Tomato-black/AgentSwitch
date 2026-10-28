// logical_form.go 是逻辑模型页的表单与状态流转：思考等级/元数据解析、
// 保存校验、目标链排序摘要与新建/编辑/保存动作（纯助手供测试直断言）。
package logical

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/state"
)

// lmReasoningLevels 是 Codex 认可的思考等级（spec §4），与 bridge 的
// reasoningLevelWhitelist 保持一致。桌面层提前校验，错误直接进表单信息位；
// bridge 保存时仍会再校验一次（防御纵深）。
var lmReasoningLevels = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// lmParsedLevels 把思考等级输入框（逗号分隔）解析成选项列表：去空格、
// 去空项、按首次出现去重。渲染默认等级下拉与 meta() 共用同一规则。
func lmParsedLevels(raw string) []string {
	seen := make(map[string]bool)
	var out []string
	for _, lv := range strings.Split(raw, ",") {
		lv = strings.TrimSpace(lv)
		if lv == "" || seen[lv] {
			continue
		}
		seen[lv] = true
		out = append(out, lv)
	}
	return out
}

// meta 把表单字符串解析成桥接层元数据：上下文窗口空串=0、否则须为非负
// 整数；思考等级须在白名单内；默认等级空时取列表首项（spec §4「非空但
// 默认空 → 列表第一项」），非空须属于列表。错误信息中文、可进信息位。
func meta(f state.LMForm) (bridge.ModelMeta, error) {
	meta := bridge.ModelMeta{DisplayName: strings.TrimSpace(f.DisplayName)}
	if raw := strings.TrimSpace(f.ContextWindow); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 0 {
			return meta, fmt.Errorf("上下文窗口需要是非负整数，如 1000000")
		}
		meta.ContextWindow = n
	}
	known := make(map[string]bool, len(lmReasoningLevels))
	for _, lv := range lmReasoningLevels {
		known[lv] = true
	}
	meta.ReasoningLevels = lmParsedLevels(f.ReasoningLevels)
	for _, lv := range meta.ReasoningLevels {
		if !known[lv] {
			return meta, fmt.Errorf("未知思考等级「%s」，可用：%s",
				lv, strings.Join(lmReasoningLevels, "、"))
		}
	}
	def := strings.TrimSpace(f.DefaultReasoningLevel)
	if def == "" && len(meta.ReasoningLevels) > 0 {
		def = meta.ReasoningLevels[0]
	}
	inList := slices.Contains(meta.ReasoningLevels, def)
	if def != "" && !inList {
		return meta, fmt.Errorf("默认思考等级「%s」不在思考等级列表中", def)
	}
	meta.DefaultReasoningLevel = def
	return meta, nil
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
			st.Logical.Edit.DisplayName = m.DisplayName
			if m.ContextWindow > 0 {
				st.Logical.Edit.ContextWindow = strconv.FormatInt(m.ContextWindow, 10)
			}
			st.Logical.Edit.ReasoningLevels = strings.Join(m.ReasoningLevels, ",")
			st.Logical.Edit.DefaultReasoningLevel = m.DefaultReasoningLevel
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
	modelMeta, err := meta(st.Logical.Edit)
	if err != nil {
		st.Logical.Edit.Msg = err.Error()
		return
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
