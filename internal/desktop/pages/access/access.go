// Package access 是接入页宿主：Agent 页签行、全局配置卡、实际配置列表与
// 编辑器分发；Claude/Codex/Claude Desktop/指南各自在子包，本包不反向被
// 子包依赖。
//
// access.go 是接入页（spec 2026-10-18-agent-access-config-design）：每 Agent
// （Claude Code / Codex，后者覆盖 CLI / 桌面版 / IDE 插件）多实际配置，
// Codex 另有全局配置（合并进所有实际配置；Claude 全局配置已于 2026-11-07
// 用户决策下线，env 由 Claude Code 设置中心按层管理），
// 「应用」即受管键投影写入 agent 真实配置文件；未应用的配置经持久文件 +
// 启动命令 / 「打开终端」在启动 Agent 时手动指定。取代旧「片段复制」页
// （旧 bridge.Access 已删除）。编辑器默认渲染真实网关令牌（2026-10-21 用户
// 裁定）；保存时锁定键仍由 bridge 剔除、令牌永不落库。审计与错误经 bridge
// 方法返回的中文文案内联显示。
package access

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/desktop/accesskit"
	"AgentSwitch/internal/desktop/pages/access/claude"
	"AgentSwitch/internal/desktop/pages/access/claudedesktop"
	"AgentSwitch/internal/desktop/pages/access/codex"
	"AgentSwitch/internal/desktop/pages/access/guide"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// gatewayGate 返回网关是否可用（应用/启动类操作的前提）与原因。
func gatewayGate(st *state.State) (bool, string) {
	return accesskit.GatewayGate(st.Svc.Gateway)
}

// configs 返回本页的接入配置桥接（每帧构造，与既有页面惯例一致）。
func configs(st *state.State) bridge.AccessConfigs {
	return bridge.AccessConfigs{A: st.Svc.Gateway.A}
}

// accessPage 是接入页（shell.go pageView 分发；签名即接线契约）。
// 结构：Agent tab 行 → 全局配置卡 → 实际配置列表卡 → 编辑器卡（表单 open 时）。
func Page(c *ui.Context, st *state.State) {
	t := c.Theme()
	s := uikit.Softs(t)
	acc := configs(st)
	if st.Access.App == "" {
		st.Access.App = "claude"
	}
	claudeTitle, codexTitle, desktopTitle, guideTitle := accesskit.TabTitles()
	gateOK, gateReason := gatewayGate(st)

	// 编辑器是独立子页（2026-10-29 决策 D2）：打开时只渲染编辑器 + 返回，
	// 不再把长表单追加在列表末尾；返回/取消统一走 CloseEditor 的脏检查。
	// 例外（2026-11-07 原型对齐）：Claude 的编辑接入改为右侧抽屉，主页面
	// 照常渲染（抽屉在 Page 末尾挂载）。
	if st.Access.App != "guide" && st.Access.App != "claude" && (st.Access.Form.Open || st.Access.DesktopForm.Open) {
		editorPage(c, st, acc, gateOK)
		return
	}
	if st.Access.BackupOpen {
		backupPage(c, st, acc)
		return
	}

	// 页头固定、内容滚动：编辑器（角色表 + 320 高 TextArea）会让页面超出
	// 视口，没有滚动容器时底部内容不可达。
	ui.Column(c).Gap(12).Grow(1).Children(func() {
		// 页头（原型 2026-11-07 §4.1）：标题「Agent 接入」，claude 页签副标题
		// 点明同页管理设置。刷新/保存设置是 Claude 设置区专属动作，留在设置区
		// 区块头（2026-11-07 用户裁定），页头不挂页签专属按钮。
		subtitle := "为每个 Agent 管理网关接入与实际配置"
		if st.Access.App == "claude" {
			subtitle = "把 Claude Code 接到 AgentSwitch，并在同一页管理它的设置"
		}
		uikit.PageHeader(c, t, uikit.PageHeaderOpts{
			Title:    "Agent 接入",
			Subtitle: subtitle,
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				// Agent 页签用框架 Tabs：键盘方向键/Home/End 可选，选中态
				// 用中性样式，不再以品牌橙软底冒充「主操作」。
				selectedTab := accesskit.TabIndex(st.Access.App)
				if ui.Tabs(c, &selectedTab, claudeTitle, codexTitle, desktopTitle, guideTitle).Changed() {
					requestTab(st, acc, accesskit.TabApp(selectedTab))
				}
				// 未保存编辑的切换确认（挂在页签行后，guide 早退前渲染）。
				switchConfirm(c, st, acc)
				// 连接指南页签：只渲染通用连接信息卡（第三方系统如 Kun 照此
				// 在目标系统里手工填写），跳过 Agent 专属的全局配置/实际配置/
				// 编辑器（提前退出本闭包，下方卡片不再渲染）。
				if st.Access.App == "guide" {
					guide.Card(c, t, s, st)
					return
				}
				if st.Access.App == "codex" && st.Access.Glob == nil {
					reloadGlobal(st, acc)
				}

				if st.Access.App == "claude" {
					claudeSummaryCard(c, st, acc, gateOK)
				}

				// 全局配置卡（2026-11-07 用户决策：Claude 全局配置下线，仅 Codex
				// 保留 env 合成层；Desktop 无 env 合成层）
				if st.Access.App == "codex" {
					uikit.Card(c, t, func() {
						ui.Column(c).Gap(10).Children(func() {
							ui.Text(c, "全局配置").Bold()
							ui.Text(c, "以下环境变量会应用到本 Agent 的所有实际配置（实际配置的同名覆盖项优先）").TextColor(t.TextMuted)
							// 列头：键/值语义不再靠 placeholder 猜。
							ui.Row(c).Gap(8).Children(func() {
								ui.Text(c, "键").Width(220).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
								ui.Text(c, "值").Grow(1).FontSize(t.FontSize - 2).TextColor(t.TextMuted)
								ui.Box(c).Width(56)
							})
							// 行内删除只记下标，循环结束后统一应用——构建中改切片会
							// 越界（logicalmodels.go 同款注释与模式）。
							globDel := -1
							for i := range st.Access.Glob {
								row := &st.Access.Glob[i]
								ui.Row(c).Gap(8).Children(func() {
									ui.TextInput(c, &row[0]).Width(220).Placeholder("键")
									ui.TextInput(c, &row[1]).Grow(1).Placeholder("值")
									if uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
										globDel = i
									}
								})
							}
							if globDel >= 0 {
								st.Access.Glob = append(st.Access.Glob[:globDel], st.Access.Glob[globDel+1:]...)
							}
							ui.Row(c).Gap(8).Children(func() {
								if uikit.SoftButton(c, t, s, "gray", "＋ 添加一项").Clicked() {
									st.Access.Glob = append(st.Access.Glob, [2]string{})
								}
								if uikit.SoftButton(c, t, s, "act", "保存全局配置").Clicked() {
									// 行绑定随输入即时更新，此处 st.Access.Glob 已是待保存
									// 的新值；重定位旧派生值需要保存前的持久基线。
									oldGlobal, _ := acc.GlobalConfig(st.Access.App)
									if err := accesskit.ValidateGlobalRows(st.Access.Glob); err != nil {
										st.Access.GlobMsg = err.Error()
									} else if err := acc.SaveGlobalConfig(st.Access.App, accesskit.OverridesFromRows(st.Access.Glob)); err != nil {
										st.Access.GlobMsg = err.Error()
									} else {
										st.Access.GlobMsg = "已保存"
										c.Toast("全局配置已保存")
										// 编辑器打开时全局基线变了：删除旧基线派生值并
										// 按新基线与当前表单模型重预览（Important#1）。
										// Codex 另有只读 config.toml 预览，同样需要按新
										// 文档刷新，否则停留在保存前的结果。
										if st.Access.Form.Open {
											if rebased, err := bridge.RebaseAccessDoc(st.Access.App, st.Access.Form.DocJSON, oldGlobal); err == nil {
												st.Access.Form.DocJSON = rebased
											}
											accesskit.RepreviewForm(st, acc)
											if st.Access.App == "codex" {
												codex.RefreshPreview(st, acc)
											}
										}
									}
								}
							})
							uikit.Feedback(c, t, uikit.InferFeedback(st.Access.GlobMsg), st.Access.GlobMsg)
						})
					})
				}

				// 实际配置列表卡（claude 页签不渲染：配置管理在接入抽屉内，
				// 2026-11-07 原型对齐）
				if st.Access.App != "claude" {
					uikit.Card(c, t, func() {
						ui.Column(c).Gap(10).Children(func() {
							ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
								ui.Text(c, "实际配置").Bold()
								ui.Box(c).Grow(1)
								if uikit.SoftButton(c, t, s, "gray", "备份与恢复").Clicked() {
									st.Access.BackupOpen = true
									st.Access.BackupMsg = ""
									st.Access.Backups, _ = acc.ListBackups(st.Access.App)
								}
								if uikit.SoftButton(c, t, s, "gray", "还原最近备份").Clicked() {
									st.Access.RestoreArm = true
								}
							})
							if st.Access.App == "codex" {
								ui.Text(c, "Codex CLI / 桌面版 / IDE 插件共用 ~/.codex/config.toml；应用后需完全退出 Codex 再重新打开").TextColor(t.TextMuted)
								ui.Text(c, "桌面版模型选择器只显示 AgentSwitch 逻辑模型；已有对话的供应商不会迁移，请新建对话").TextColor(t.TextMuted)
							}
							if !gateOK {
								ui.Text(c, gateReason).TextColor(t.TextMuted)
							}
							list := acc.List(st.Access.App)
							if len(list) == 0 {
								logicalCount := 0
								if st.Svc.Logical != nil {
									logicalCount = len(st.Svc.Logical.List())
								}
								if logicalCount == 0 {
									uikit.EmptyStateBare(c, t, uikit.EmptyStateOpts{
										Title: "还没有配置", Hint: "先在「逻辑模型」页创建一个模型",
										ActionLabel: "去逻辑模型", OnAction: func() { st.Go("logical", nil) },
									})
								} else {
									uikit.EmptyStateBare(c, t, uikit.EmptyStateOpts{
										Title: "还没有配置", Hint: "点击下方「新建配置」创建",
									})
								}
							}
							if len(list) > 8 {
								// ui.SearchField（mygo v0.7+）：放大镜/一键清除/Esc 清空/读屏 search 态。
								ui.SearchField(c, &st.Access.Filter).Grow(1).
									Placeholder("搜索配置：名称 / 模型")
							}
							filter := strings.ToLower(strings.TrimSpace(st.Access.Filter))
							visible := make([]bridge.AccessConfigView, 0, len(list))
							for _, v := range list {
								hay := v.Name + " " + v.LogicalModelName + " " + accesskit.ModelSummary(st.Access.App, v.Settings, v.LogicalModelName)
								if filter == "" || strings.Contains(strings.ToLower(hay), filter) {
									visible = append(visible, v)
								}
							}
							if filter != "" && len(visible) == 0 {
								ui.Text(c, "没有匹配的配置").TextColor(t.TextMuted)
							}
							hasCurrent := false
							for i, v := range visible {
								if v.IsCurrent {
									hasCurrent = true
								}
								if i > 0 {
									ui.Box(c).Height(1).Background(t.Border)
								}
								if st.Access.App == "claude_desktop" {
									claudedesktop.ConfigRow(c, t, s, st, acc, v, gateOK)
								} else {
									configRow(c, t, s, st, acc, v, gateOK)
								}
							}
							if len(visible) > 0 && !hasCurrent {
								ui.Text(c, "当前没有配置被应用，Agent 仍使用其原配置文件").TextColor(t.TextMuted)
							}
							uikit.Feedback(c, t, uikit.InferFeedback(st.Access.Msg), st.Access.Msg)
							ui.Row(c).Children(func() {
								ui.Box(c).Grow(1)
								if uikit.PillButton(c, t, "＋ 新建配置").Clicked() {
									if st.Access.App == "claude_desktop" {
										st.Access.DesktopForm = state.DesktopForm{Open: true}
										st.Access.DesktopForm.Baseline = st.Access.DesktopForm.Signature()
										claudedesktop.Prepare(st, acc)
									} else {
										st.Access.Form = state.AccessForm{Open: true}
										preloadForm(st, acc)
									}
								}
							})
						})
					})
				}

				// 删除二次确认（行内「删除」只武装，弹窗确认才执行）
				deleteConfirm(c, st, acc)
				// 还原备份确认（覆盖 live 文件前先备份当前状态，可再次恢复）
				restoreConfirm(c, st, acc)
				// 抽屉配置切换/新建确认（草稿保护，抽屉开/关两态都可达）
				drawerSwitchConfirm(c, st, acc)
			})
		})
	})
	// Claude 接入抽屉（2026-11-07 原型对齐）：Form.Open 即抽屉打开；Esc/
	// 遮罩/头钮只翻 open 位，写回时若因此关闭则按内容差异置 Draft（关闭
	// 保留草稿，摘要卡显示「有未保存的接入更改」）。
	if st.Access.App == "claude" {
		open := st.Access.Form.Open
		uikit.Drawer(c, t, &open, "接入配置", "写入受管键到 ~/.claude/settings.json（写前备份）", func() {
			claude.DrawerCard(c, t, s, st, acc, gateOK, claudeDrawerHooks(st, acc))
		})
		if st.Access.Form.Open != open {
			st.Access.Form.Open = open
			if !open {
				st.Access.Form.Draft = st.Access.Form.Signature("claude") != st.Access.Form.Baseline
			}
		}
	}
}

// OpenAccessDrawer 从其他页跳回接入页并打开当前配置的接入抽屉（设置页受管
// 键行「打开接入」的跨页入口；dirty 草稿保护经 openAccessEditor 生效）。
func OpenAccessDrawer(st *state.State) {
	st.Go("access", func() {
		acc := bridge.AccessConfigs{A: st.Svc.Gateway.A}
		id := ""
		for _, v := range acc.List("claude") {
			if v.IsCurrent {
				id = v.ID
				break
			}
		}
		openAccessEditor(st, acc, id)
	})
}

// openAccessEditor 打开 Claude 接入抽屉的编辑目标（终审 Important#3）：
// 同目标的保留草稿直接恢复不清表单；不同目标（含新建草稿）有未保存修改时
// 武装切换确认（确认框在 Page 层渲染，抽屉关闭态也可达），确认后才切换。
func openAccessEditor(st *state.State, acc bridge.AccessConfigs, id string) {
	if st.Access.Form.Dirty("claude") {
		if st.Access.Form.EditID == id {
			st.Access.Form.Open = true
			return
		}
		st.Access.DrawerSwitchArm = true
		st.Access.DrawerSwitchID = id
		return
	}
	st.Access.Form = state.AccessForm{Open: true, EditID: id}
	preloadForm(st, acc)
}

// drawerSwitchConfirm 是抽屉配置切换/新建的确认框（抽屉开/关两态都渲染；
// 取消默认聚焦）。confirmDrawerSwitch 的目标由 DrawerSwitchID 挂起。
func drawerSwitchConfirm(c *ui.Context, st *state.State, acc bridge.AccessConfigs) {
	if !st.Access.DrawerSwitchArm {
		return
	}
	uikit.ConfirmDialog(c, c.Theme(), uikit.ConfirmOpts{
		Open:        &st.Access.DrawerSwitchArm,
		Title:       "放弃未保存的接入修改？",
		Message:     "名称/模型/角色映射修改尚未保存，切换配置将丢弃这些修改。",
		CancelLabel: "取消", ConfirmLabel: "放弃并切换", Danger: true,
		OnConfirm: func() { claude.ConfirmDrawerSwitch(st, claudeDrawerHooks(st, acc)) },
	})
}

// claudeDrawerHooks 返回抽屉的配置切换/新建回调：重置表单为编辑（或新建）
// 状态并预载文档（宿主职责——claude 子包不反向依赖本包）。
func claudeDrawerHooks(st *state.State, acc bridge.AccessConfigs) claude.DrawerHooks {
	return claude.DrawerHooks{
		SwitchTo: func(id string) {
			st.Access.Form = state.AccessForm{Open: true, EditID: id}
			preloadForm(st, acc)
		},
		NewDraft: func() {
			st.Access.Form = state.AccessForm{Open: true}
			preloadForm(st, acc)
		},
	}
}

// editorPage 是接入编辑器的独立子页（claude 已改为抽屉，仅剩 Codex /
// Claude Desktop）：页头返回 + 当前 Agent 标题，正文只渲染对应编辑器。
func editorPage(c *ui.Context, st *state.State, acc bridge.AccessConfigs, gateOK bool) {
	t := c.Theme()
	s := uikit.Softs(t)
	title := "新建配置"
	if st.Access.Form.EditID != "" || st.Access.DesktopForm.EditID != "" {
		title = "编辑配置"
	}
	ui.Column(c).Gap(12).Grow(1).Children(func() {
		uikit.PageHeader(c, t, uikit.PageHeaderOpts{
			Title: title, Subtitle: accesskit.TabTitle(st.Access.App),
			BackLabel: "← 返回接入",
			OnBack:    func() { accesskit.CloseEditor(st) },
		})
		ui.Scroll(c).Grow(1).Children(func() {
			ui.Column(c).Gap(12).Children(func() {
				if st.Access.App == "claude_desktop" {
					claudedesktop.EditorCard(c, t, s, st, acc, gateOK)
				} else {
					codex.EditorCard(c, t, s, st, acc, gateOK)
				}
			})
		})
	})
}

// accessReloadGlobal 把全局配置读入编辑行（tab 切换/首帧；不覆盖正在
// 编辑的行以外的时机由调用点控制）。
func reloadGlobal(st *state.State, acc bridge.AccessConfigs) {
	env, err := acc.GlobalConfig(st.Access.App)
	if err != nil {
		st.Access.GlobMsg = err.Error()
		return
	}
	st.Access.Glob = accesskit.GlobalRows(env)
	st.Access.GlobMsg = ""
}

// preloadForm 打开编辑器：预填名称/模型，加载 PreviewDoc 合成预览
// （新建以 "{}" 为底；预览失败回退原文并内联报错）。
func preloadForm(st *state.State, acc bridge.AccessConfigs) {
	f := &st.Access.Form
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	if f.ModelLabel == "" {
		if labels := accesskit.ModelLabels(models); len(labels) > 0 {
			f.ModelLabel = labels[0]
		}
	}
	raw := "{}"
	if f.EditID != "" {
		for _, v := range acc.List(st.Access.App) {
			if v.ID == f.EditID {
				f.Name = v.Name
				f.ModelLabel = v.LogicalModelName
				raw = v.Settings
			}
		}
	}
	f.OriginalDoc = raw
	modelID := accesskit.ModelIDByLabel(models, f.ModelLabel)
	preview, err := acc.PreviewDoc(st.Access.App, modelID, raw)
	if err != nil {
		f.DocJSON = raw
		f.Msg = "预览失败：" + err.Error()
		return
	}
	f.DocJSON = preview
	if st.Access.App == "codex" {
		// 编辑器打开即回填模型列表并生成只读 config.toml 预览。
		codex.Prepare(st, acc)
	}
	if st.Access.App == "claude" {
		// 编辑器打开即生成只读 settings.json 预览（live 合并结果）。
		claude.Prepare(st, acc)
	}
	f.Baseline = f.Signature(st.Access.App)
}

// accessRepreviewForm 用当前表单模型重新合成预览（基线变化后调用）；失败内联报错。
func repreviewForm(st *state.State, acc bridge.AccessConfigs) {
	var models []bridge.ModelView
	if st.Svc.Logical != nil {
		models = st.Svc.Logical.List()
	}
	modelID := accesskit.ModelIDByLabel(models, st.Access.Form.ModelLabel)
	preview, err := acc.PreviewDoc(st.Access.App, modelID, st.Access.Form.DocJSON)
	if err != nil {
		st.Access.Form.Msg = "预览失败：" + err.Error()
		return
	}
	st.Access.Form.DocJSON = preview
	st.Access.Form.Msg = ""
}

// claudeSummaryCard 是合并页的接入摘要卡：当前应用配置、网关状态、模型与
// 主操作。实际配置列表仍在下方，供多配置管理（全局配置已下线）。
func claudeSummaryCard(c *ui.Context, st *state.State, acc bridge.AccessConfigs, gateOK bool) {
	t := c.Theme()
	s := uikit.Softs(t)
	list := acc.List("claude")
	var cur *bridge.AccessConfigView
	for i := range list {
		if list[i].IsCurrent {
			cur = &list[i]
			break
		}
	}
	stt := st.Svc.Gateway.Status()
	uikit.Card(c, t, func() {
		ui.Column(c).Gap(8).Children(func() {
			// 第一行：标题 + 状态徽章（未保存接入更改 > 待应用 > 已应用/
			// 尚未应用；网关停止是叠加态，不遮蔽接入状态）+ 右侧操作。
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Claude Code 接入").Bold()
				switch {
				case st.Access.Form.Dirty("claude"):
					uikit.SoftBadge(c, t, s, "warn", "有未保存的接入更改")
				case cur == nil:
					uikit.SoftBadge(c, t, s, "gray", "尚未应用")
				case cur.NeedsApply:
					uikit.SoftBadge(c, t, s, "warn", "待应用")
				default:
					uikit.SoftBadge(c, t, s, "ok", "已应用")
				}
				if !gateOK {
					uikit.SoftBadge(c, t, s, "danger", "网关已停止")
				}
				ui.Box(c).Grow(1)
				if cur != nil {
					if uikit.SoftButton(c, t, s, "gray", "编辑接入").Clicked() {
						openAccessEditor(st, acc, cur.ID)
					}
					if gateOK {
						label := "应用接入"
						if !cur.NeedsApply {
							label = "重新应用"
						}
						switch {
						case st.Access.ApplyTask.Running && st.Access.ApplyID == cur.ID:
							uikit.BusyRow(c, t, st.Access.ApplyTask.Label, nil)
						case st.Access.ApplyTask.Running:
							uikit.SoftButton(c, t, s, "gray", label).Disabled(true)
						default:
							if uikit.SoftButton(c, t, s, "act", label).Clicked() {
								accesskit.ApplyAsync(st, acc, *cur)
							}
						}
					}
				}
			})
			// 第二行 meta（原型 2026-11-07 §6.2.1）：LED + 状态词 + 等宽地址 +
			// 配置名 + 模型 + 1M 徽章。
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				uikit.LedDot(c, t, stt.State)
				ui.Text(c, gatewayStateWord(stt.State))
				if (stt.State == "running" || stt.State == "degraded") && stt.Addr != "" {
					ui.Text(c, stt.Addr).Font(uikit.MonoFont)
				}
				if cur == nil {
					ui.Text(c, "尚未选择接入配置").TextColor(t.TextMuted)
				} else {
					ui.Text(c, "配置 "+cur.Name)
					ui.Text(c, "模型 "+accesskit.ModelSummary("claude", cur.Settings, cur.LogicalModelName))
					if accesskit.MainOneMDeclared(cur.Settings) {
						uikit.SoftBadge(c, t, s, "act", "1M 上下文")
					}
				}
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "接入只配置模型与网关；权限、状态栏、Skills 在侧栏「Claude Code 设置」页编辑").
					TextColor(t.TextMuted).Grow(1)
				if uikit.SoftButton(c, t, s, "gray", "打开设置页").Clicked() {
					st.Go("claude-settings", nil)
				}
			})
			// 无逻辑模型的空态引导（列表卡移除后由摘要卡承接，2026-11-07）。
			if st.Svc.Logical != nil && len(st.Svc.Logical.List()) == 0 {
				ui.Text(c, "还没有逻辑模型——先在「逻辑模型」页创建一个，再回来配置接入").TextColor(t.TextMuted)
				if uikit.SoftButton(c, t, s, "act", "去逻辑模型").Clicked() {
					st.Go("logical", nil)
				}
			}
			if !gateOK {
				ui.Text(c, "网关不可用：先启动网关再应用接入").TextColor(t.TextMuted)
			}
			// 接入操作反馈（应用失败/阻断提示/删除与还原结果）唯一渲染位：
			// 列表卡移除后由摘要卡承接，否则主操作失败毫无解释（终审 Critical#1）。
			uikit.Feedback(c, t, uikit.InferFeedback(st.Access.Msg), st.Access.Msg)
		})
	})
}

// gatewayStateWord 把网关状态译成摘要卡 meta 行的状态词。
func gatewayStateWord(state string) string {
	switch state {
	case "running":
		return "运行中"
	case "degraded":
		return "降级"
	case "starting":
		return "启动中"
	}
	return "已停止"
}

// configRow 渲染一行实际配置：第一行名称/使用中/模型摘要，第二行操作，
// 「更多」的次级操作放第三行并允许换行——长名称与窄窗不再把按钮挤出。
func configRow(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, acc bridge.AccessConfigs, v bridge.AccessConfigView, gateOK bool) {
	ui.Column(c).Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, v.Name).Bold().Width(180).SingleLine()
			if badge := accesskit.RowBadge(v.IsCurrent); badge != "" {
				uikit.SoftBadge(c, t, s, "ok", badge)
			}
			if v.NeedsApply {
				uikit.SoftBadge(c, t, s, "warn", "待应用")
			}
			ui.Text(c, "→ "+accesskit.ModelSummary(st.Access.App, v.Settings, v.LogicalModelName)).
				TextColor(t.TextMuted).Grow(1).SingleLine()
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			// 应用/复制命令/打开终端依赖网关（终审#3：编辑/删除是纯 DB 操作，
			// 不受门禁影响——spec 只点名门禁前三个操作）。
			if gateOK {
				label := "应用"
				if v.IsCurrent && !v.NeedsApply {
					label = "重新应用"
				}
				switch {
				case st.Access.ApplyTask.Running && st.Access.ApplyID == v.ID:
					uikit.BusyRow(c, t, st.Access.ApplyTask.Label, nil)
				case st.Access.ApplyTask.Running:
					uikit.SoftButton(c, t, s, "gray", label).Disabled(true)
				default:
					if uikit.SoftButton(c, t, s, "act", label).Clicked() {
						accesskit.ApplyAsync(st, acc, v)
					}
				}
			}
			if uikit.SoftButton(c, t, s, "gray", "编辑").Clicked() {
				st.Access.Form = state.AccessForm{Open: true, EditID: v.ID}
				preloadForm(st, acc)
			}
			// 次级操作走主题化菜单（mygo v0.7.2+ PopoverMenuButton：键盘 +
			// 读屏支持；网关停止时菜单项置灰而非隐藏；删除为 Danger 红项）。
			ui.PopoverMenuButton(c, "更多", func(m *ui.Menu) {
				if m.Item("复制启动命令").Disabled(!gateOK).Chosen() {
					cmd, err := acc.LaunchCommand(v.ID)
					if err != nil {
						st.Access.Msg = "生成启动命令失败：" + err.Error()
					} else {
						mygo.Clipboard.Write(transfer.TextData(cmd))
						st.Access.Msg = "启动命令已复制"
					}
				}
				if m.Item("打开终端").Disabled(!gateOK).Chosen() {
					if err := acc.OpenTerminal(v.ID); err != nil {
						st.Access.Msg = "打开终端失败：" + err.Error()
					} else {
						st.Access.Msg = ""
					}
				}
				if m.Item("删除").Danger().Chosen() {
					st.Access.DeleteArm = true
					st.Access.DeleteID = v.ID
				}
			})
		})
	})
}

// deleteConfirm 是接入配置的删除二次确认：与渠道/逻辑模型页一致的两段式
// 交互（行内「删除」武装，弹窗确认才执行）；删除不动 Agent live 文件。
func deleteConfirm(c *ui.Context, st *state.State, acc bridge.AccessConfigs) {
	if !st.Access.DeleteArm {
		st.Access.DeleteID = ""
		return
	}
	name := ""
	for _, v := range acc.List(st.Access.App) {
		if v.ID == st.Access.DeleteID {
			name = v.Name
			break
		}
	}
	if name == "" { // 对象已不存在：静默收起
		st.Access.DeleteArm = false
		st.Access.DeleteID = ""
		return
	}
	msg := "确定删除接入配置「" + name + "」？Agent 仍在使用上次写入的网关配置。"
	uikit.ConfirmDialog(c, c.Theme(), uikit.ConfirmOpts{
		Open: &st.Access.DeleteArm, Title: "删除接入配置", Message: msg,
		CancelLabel: "取消", ConfirmLabel: "确认删除", Danger: true,
		OnConfirm: func() {
			if err := acc.Remove(st.Access.DeleteID); err != nil {
				st.Access.Msg = "删除失败：" + err.Error()
			} else {
				st.Access.Msg = "已删除「" + name + "」——Agent 配置文件保留最后状态"
			}
			st.Access.DeleteID = ""
		},
	})
}

// tabDirty 报告接入页是否存在未保存的编辑器内容（两套表单任一）。Claude
// Code 设置已拆独立页，其脏态由侧栏切页守卫承担（2026-11-07）。
func tabDirty(st *state.State) bool {
	return st.Access.Form.Dirty(st.Access.App) || st.Access.DesktopForm.Dirty()
}

// requestTab 处理页签点击：干净则立即切换；有未保存编辑则武装确认弹窗。
func requestTab(st *state.State, acc bridge.AccessConfigs, app string) {
	if app == st.Access.App {
		return
	}
	if tabDirty(st) {
		st.Access.SwitchArm = true
		st.Access.SwitchTo = app
		return
	}
	switchTab(st, acc, app)
}

// switchTab 执行页签切换：丢弃两套编辑器草稿，按目标页签刷新全局配置
// （codex 仍有 env 合成层；claude 全局配置已下线）。
func switchTab(st *state.State, acc bridge.AccessConfigs, app string) {
	st.Access.App = app
	st.Access.Msg = ""
	st.Access.Form = state.AccessForm{}
	st.Access.DesktopForm = state.DesktopForm{}
	// Claude 设置区草稿随页签丢弃（保留注入 Home；下次进入重新加载）。
	st.ClaudeCfg = state.ClaudeCfgState{Home: st.ClaudeCfg.Home}
	if app == "codex" {
		reloadGlobal(st, acc)
	}
}

// switchConfirm 是未保存编辑的切换确认弹窗：确认才执行切换。
func switchConfirm(c *ui.Context, st *state.State, acc bridge.AccessConfigs) {
	if !st.Access.SwitchArm {
		st.Access.SwitchTo = ""
		return
	}
	target := st.Access.SwitchTo
	msg := "当前编辑器有未保存的修改，切换到「" + accesskit.TabTitle(target) + "」将丢弃这些修改。"
	uikit.ConfirmDialog(c, c.Theme(), uikit.ConfirmOpts{
		Open: &st.Access.SwitchArm, Title: "未保存的编辑", Message: msg,
		CancelLabel: "继续编辑", ConfirmLabel: "放弃并切换", Danger: true,
		OnConfirm: func() {
			st.Access.SwitchTo = ""
			switchTab(st, acc, target)
		},
	})
}

// restoreConfirm 是「还原最近备份」的二次确认：把该 Agent 最近一份备份集
// 写回 live 文件（撤销「应用」）。恢复前 bridge 会先快照当前 live，
// 因此恢复本身也可再次恢复。
func restoreConfirm(c *ui.Context, st *state.State, acc bridge.AccessConfigs) {
	if !st.Access.RestoreArm {
		return
	}
	msg := "将用「" + accesskit.TabTitle(st.Access.App) + "」最近一次备份覆盖当前 Agent 配置文件（覆盖前会先备份当前状态，可再次还原）。「使用中」标记保持不变，如需重新接管请再点「应用」。"
	uikit.ConfirmDialog(c, c.Theme(), uikit.ConfirmOpts{
		Open: &st.Access.RestoreArm, Title: "还原最近备份", Message: msg,
		CancelLabel: "取消", ConfirmLabel: "还原", Danger: true,
		OnConfirm: func() {
			if err := acc.RestoreLatestBackup(st.Access.App); err != nil {
				st.Access.Msg = "还原失败：" + err.Error()
			} else {
				st.Access.Msg = "已还原最近备份——Agent 配置文件已回滚；如需重新接管请再点「应用」"
			}
		},
	})
}
