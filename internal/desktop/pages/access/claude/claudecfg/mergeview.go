package claudecfg

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// mergedView 渲染跨层合并预览：有效值、来源层、合并方式、贡献值与权限
// 求值卡。只读，不写回任何文件；敏感值默认遮蔽、逐行可显示。
func mergedView(c *ui.Context, t *ui.Theme, st *state.State) {
	cf := &st.ClaudeCfg
	if cf.MergeEntries == nil {
		cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: cf.Home}
		if _, entries, err := cfgs.MergeClaudeConfigView(cf.Project); err == nil {
			cf.MergeEntries = entries
		}
	}
	ensureMergeState(cf)

	ui.Text(c, "合并预览").Bold()
	ui.Text(c, "优先级：用户级 < 项目级 < 项目本地 < 企业托管；--settings 与草稿不参与合并；托管层的 MDM/远程策略可能不可见（以 Claude Code 实际运行为准）。").
		FontSize(t.FontSize - 2).TextColor(t.TextMuted)
	ui.Text(c, "只读：展示跨层合并后的最终值与来源，不会写回任何文件。").
		FontSize(t.FontSize - 2).TextColor(t.TextMuted)
	ui.Text(c, "图例：覆盖=最高层胜出；并集=数组去重合并；逐变量=env 逐键；累加=hooks；逐键=对象逐键；整值替换；任一层 true=OR").
		FontSize(t.FontSize - 2).TextColor(t.TextMuted)

	// ui.SearchField（mygo v0.7+）：放大镜/一键清除/Esc 清空/读屏 search 角色。
	ui.SearchField(c, &cf.MergeQuery).Placeholder("筛选键路径")
	permissionCard(c, t, cf)
	mergeKeys(c, t, cf)
	mergeSkills(c, t, cf)
}

// ensureMergeState 初始化合并预览的展开/显示映射。
func ensureMergeState(cf *state.ClaudeCfgState) {
	if cf.MergeOpen == nil {
		cf.MergeOpen = map[string]bool{}
	}
	if cf.MergeReveal == nil {
		cf.MergeReveal = map[string]bool{}
	}
}

// mergeKindLabel 把合并策略译成界面徽章文案。
func mergeKindLabel(kind corecfg.MergeKind) string {
	switch kind {
	case corecfg.MergeOverride:
		return "覆盖"
	case corecfg.MergeUnion:
		return "并集"
	case corecfg.MergeEnv:
		return "逐变量"
	case corecfg.MergeHooks:
		return "累加"
	case corecfg.MergePlugins, corecfg.MergeSkill, corecfg.MergeLeaf:
		return "逐键"
	case corecfg.MergeReplace:
		return "整值替换"
	case corecfg.MergeOr:
		return "任一层 true"
	}
	return string(kind)
}

func mergeKeys(c *ui.Context, t *ui.Theme, cf *state.ClaudeCfgState) {
	paths := make([]string, 0, len(cf.MergeEntries))
	for path := range cf.MergeEntries {
		// 权限键在权限求值卡中单独展示，键行里不重复。
		if strings.HasPrefix(path, "permissions.") {
			continue
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	query := strings.TrimSpace(cf.MergeQuery)
	if query != "" {
		filtered := paths[:0]
		for _, path := range paths {
			if strings.Contains(path, query) {
				filtered = append(filtered, path)
			}
		}
		paths = filtered
	}
	if len(paths) > 120 {
		paths = paths[:120]
		ui.Text(c, "仅显示前 120 个叶子路径；完整结果以 Claude Code 实际运行为准").
			FontSize(t.FontSize - 2).TextColor(t.TextMuted)
	}
	for _, path := range paths {
		entry := cf.MergeEntries[path]
		if entry.Path == "" {
			entry.Path = path
		}
		mergeKeyRow(c, t, cf, entry)
	}
	if len(paths) == 0 {
		ui.Text(c, "没有可预览的键；请先在任一层保存设置").TextColor(t.TextMuted)
	}
}

func mergeKeyRow(c *ui.Context, t *ui.Theme, cf *state.ClaudeCfgState, entry corecfg.MergedEntry) {
	s := uikit.Softs(t)
	path := entry.Path
	reveal := cf.MergeReveal[path]
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, path).Font(uikit.MonoFont).Grow(1)
			ui.Text(c, mergeValueText(path, entry.Value, reveal)).Font(uikit.MonoFont).MaxWidth(360)
			uikit.SoftBadge(c, t, s, "gray", mergeKindLabel(entry.Kind))
			if entry.Source != "" {
				ui.Text(c, "来源："+sourceLabel(entry.Source)).
					FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			}
			if corecfg.IsSensitivePath(path) {
				label := "显示"
				if reveal {
					label = "隐藏"
				}
				if uikit.SoftButton(c, t, s, "gray", label).Clicked() {
					cf.MergeReveal[path] = !reveal
				}
			}
			open := cf.MergeOpen[path]
			label := "展开"
			if open {
				label = "收起"
			}
			if uikit.SoftButton(c, t, s, "gray", label).Clicked() {
				cf.MergeOpen[path] = !open
			}
		})
		if cf.MergeOpen[path] && len(entry.Contributions) > 0 {
			mergeContributions(c, t, cf, entry)
		}
	})
}

func mergeContributions(c *ui.Context, t *ui.Theme, cf *state.ClaudeCfgState, entry corecfg.MergedEntry) {
	s := uikit.Softs(t)
	reveal := cf.MergeReveal[entry.Path]
	for _, con := range entry.Contributions {
		ui.Row(c).Gap(8).AlignItems(ui.Center).PaddingStart(16).Children(func() {
			ui.Text(c, sourceLabel(con.Layer)).Width(70).TextColor(t.TextMuted)
			ui.Text(c, con.File).Font(uikit.MonoFont).FontSize(t.FontSize - 2).
				TextColor(t.TextMuted).Grow(1)
			ui.Text(c, mergeValueText(entry.Path, con.Value, reveal)).Font(uikit.MonoFont)
			if con.Effective {
				uikit.SoftBadge(c, t, s, "ok", "生效")
			} else if entry.Kind == corecfg.MergeOr {
				uikit.SoftBadge(c, t, s, "gray", "未生效")
			} else {
				uikit.SoftBadge(c, t, s, "gray", "被覆盖")
			}
		})
	}
}

// mergeValueText 按路径遮蔽敏感值；reveal=true 时显示明文。
func mergeValueText(path string, v any, reveal bool) string {
	if corecfg.IsSensitivePath(path) {
		if reveal {
			return stringValue(v)
		}
		return corecfg.MaskSecret(stringValue(v))
	}
	return displayValue(path, v)
}

func permissionCard(c *ui.Context, t *ui.Theme, cf *state.ClaudeCfgState) {
	s := uikit.Softs(t)
	ui.Text(c, "权限求值").Bold()
	// 三段式（原型 §6.8.2）：1 拒绝 → 2 询问 → 3 允许，首个匹配决定。
	type seg struct{ num, path string }
	segs := []seg{
		{"1 拒绝", "permissions.deny"},
		{"2 询问", "permissions.ask"},
		{"3 允许", "permissions.allow"},
	}
	ui.Row(c).Gap(7).AlignItems(ui.Start).Children(func() {
		for i, sg := range segs {
			if i > 0 {
				ui.Text(c, "→").TextColor(t.TextMuted)
			}
			entry, ok := cf.MergeEntries[sg.path]
			count := 0
			if ok {
				if arr, isArr := entry.Value.([]any); isArr {
					count = len(arr)
				}
			}
			ui.Box(c).Border(1, t.Border).Radius(9).Padding(7, 9).Grow(1).Children(func() {
				ui.Column(c).Gap(3).Children(func() {
					ui.Text(c, sg.num).Bold()
					ui.Text(c, fmt.Sprintf("%d 条", count)).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
					if ok {
						ui.Text(c, mergeValueText(sg.path, entry.Value, cf.MergeReveal[sg.path])).
							Font(uikit.MonoFont).FontSize(t.FontSize - 2).MaxWidth(160)
					} else {
						ui.Text(c, "未设置").FontSize(t.FontSize - 2).TextColor(t.TextMuted)
					}
				})
			})
		}
	})
	ui.Text(c, "deny 任意层压过 allow 任意层；首个匹配决定").FontSize(t.FontSize - 2).TextColor(t.TextMuted)

	entry, ok := cf.MergeEntries["permissions.additionalDirectories"]
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "附加目录").Width(90).TextColor(t.TextMuted)
		if !ok {
			ui.Text(c, "未设置").TextColor(t.TextMuted).Grow(1)
			return
		}
		ui.Text(c, mergeValueText("permissions.additionalDirectories", entry.Value, cf.MergeReveal[entry.Path])).
			Font(uikit.MonoFont).Grow(1)
		uikit.SoftBadge(c, t, s, "gray", mergeKindLabel(entry.Kind))
		if entry.Source != "" {
			ui.Text(c, "来源："+sourceLabel(entry.Source)).
				FontSize(t.FontSize - 2).TextColor(t.TextMuted)
		}
	})

	if entry, ok := cf.MergeEntries["permissions.defaultMode"]; ok {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "默认模式").Width(90).TextColor(t.TextMuted)
			ui.Text(c, displayValue("permissions.defaultMode", entry.Value)).Font(uikit.MonoFont).Grow(1)
			if entry.Source != "" {
				ui.Text(c, "来源："+sourceLabel(entry.Source)).
					FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			}
		})
		for _, con := range entry.Contributions {
			mode, _ := con.Value.(string)
			if con.Effective || (mode != "auto" && mode != "bypassPermissions") {
				continue
			}
			if con.Layer != corecfg.ScopeProject && con.Layer != corecfg.ScopeLocal {
				continue
			}
			uikit.Feedback(c, t, uikit.FeedbackWarning,
				"项目级 "+mode+" 不生效；实际使用 "+displayValue("permissions.defaultMode", entry.Value))
		}
	}
	if _, ok := cf.MergeEntries["skipDangerousModePermissionPrompt"]; ok {
		uikit.Feedback(c, t, uikit.FeedbackNeutral, mergeOrNote)
	}
}

func mergeSkills(c *ui.Context, t *ui.Theme, cf *state.ClaudeCfgState) {
	s := uikit.Softs(t)
	ui.Text(c, "Skills").Bold()
	if len(cf.Skills) == 0 {
		ui.Text(c, "未读取到 Skills").TextColor(t.TextMuted)
		return
	}
	ui.Text(c, "插件与作者锁定的 Skill 只读；skillOverrides 按 Skill 名逐键合并。").
		FontSize(t.FontSize - 2).TextColor(t.TextMuted)
	for _, skill := range cf.Skills {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, skill.Name).Font(uikit.MonoFont).Grow(1)
			uikit.SoftBadge(c, t, s, "gray", skillSourceLabel(skill.Source))
			ui.Text(c, "有效："+skillLabel(skill.Effective)).
				FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			if lock := mergeSkillLock(skill); lock != "" {
				uikit.SoftBadge(c, t, s, "warn", lock)
			}
			if entry, ok := cf.MergeEntries["skillOverrides."+skill.Name]; ok {
				ui.Text(c, "覆盖："+mergeValueText("skillOverrides."+skill.Name, entry.Value, false)).
					FontSize(t.FontSize - 2).TextColor(t.TextMuted)
			}
		})
	}
}

// mergeSkillLock 返回 Skill 的锁定徽章文案；空字符串表示可写。
// 插件来源的 Skill 由插件开关统一管理，即使 ResolveSkill 未给 LockedBy
// 也必须标为锁定。
func mergeSkillLock(skill bridge.SkillView) string {
	if skill.Source == corecfg.SkillSourcePlugin {
		return "插件锁定"
	}
	if skill.LockedBy != "" {
		return lockLabel(skill.LockedBy)
	}
	return ""
}

const mergeOrNote = "危险模式确认合并规则：任一层 true 即 true（OR），false 不能覆盖其他层的 true"
