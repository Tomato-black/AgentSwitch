package claudecfg

import (
	"strings"

	"github.com/egoist/mygo/ui"

	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

var skillOptions = []string{"继承（未设置）", "开启", "仅名称", "仅手动调用", "关闭"}
var skillValues = []string{"", "on", "name-only", "user-invocable-only", "off"}
var skillLabels = map[string]string{
	"on": "开启", "name-only": "仅名称", "user-invocable-only": "仅手动调用", "off": "关闭",
}

// SkillsCard 渲染 Skills 卡片：来源、锁定原因、当前有效状态与
// skillOverrides 四态开关（继承 / 开启 / 仅名称 / 仅手动调用 / 关闭）。
// 插件、作者与托管锁定的 Skill 只显示状态，不提供可写控件。
func SkillsCard(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	cf := &st.ClaudeCfg
	if cf.SkillStates == nil {
		cf.SkillStates = map[string]string{}
	}

	formText(c, t, "Skills", "skillOverrides 四态：开启 / 仅名称 / 仅手动调用 / 关闭；插件与作者锁定的 Skill 只读")
	if cf.SkillErr != "" {
		uikit.Feedback(c, t, uikit.FeedbackWarning, cf.SkillErr)
	}
	if len(cf.Skills) == 0 {
		ui.Text(c, "未读取到 Skills").TextColor(t.TextMuted)
		return
	}

	for i := range cf.Skills {
		skill := &cf.Skills[i]
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Gap(1).Grow(1).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, skill.Name).Font(uikit.MonoFont)
					uikit.SoftBadge(c, t, s, "gray", skillSourceLabel(skill.Source))
					if skill.LockedBy != "" {
						uikit.SoftBadge(c, t, s, "warn", lockLabel(skill.LockedBy))
					}
				})
				if desc := strings.TrimSpace(skill.Description); desc != "" {
					ui.Text(c, desc).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
				}
			})
			if skill.LockedBy != "" {
				ui.Text(c, "有效："+skillLabel(skill.Effective)).Width(100).TextColor(t.TextMuted)
				return
			}
			cur := skillChoice(cf.SkillStates[skill.Name])
			sel := ui.Select(c, &cur, skillOptions).Width(150)
			if sel.Changed() {
				setSkillState(cf, skill.Name, skillChoiceValue(cur))
			}
		})
	}
}

func skillSourceLabel(src corecfg.SkillSource) string {
	switch src {
	case corecfg.SkillSourceBuiltIn:
		return "内置"
	case corecfg.SkillSourceUser:
		return "用户"
	case corecfg.SkillSourceProject:
		return "项目"
	case corecfg.SkillSourcePlugin:
		return "插件"
	default:
		return "未知来源"
	}
}

func skillChoice(state string) string {
	if label, ok := skillLabels[state]; ok {
		return label
	}
	return skillOptions[0]
}

func skillChoiceValue(label string) string {
	for i, opt := range skillOptions {
		if opt == label {
			return skillValues[i]
		}
	}
	return ""
}

// setSkillState 写入当前层的 skillOverrides；state 为空表示继承（删除）。
// 保留其它 Skill 的 override（含已不存在的未知 Skill）。
func setSkillState(cf *state.ClaudeCfgState, name, state string) {
	if cf.SkillStates == nil {
		cf.SkillStates = map[string]string{}
	}
	m, _ := cf.Doc["skillOverrides"].(map[string]any)
	if m == nil {
		m = map[string]any{}
	}
	if state == "" {
		delete(m, name)
		delete(cf.SkillStates, name)
	} else {
		m[name] = state
		cf.SkillStates[name] = state
	}
	if len(m) == 0 {
		delete(cf.Doc, "skillOverrides")
	} else {
		cf.Doc["skillOverrides"] = m
	}
	RecomputeDirty(cf)
}
