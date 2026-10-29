// Package access 是接入页宿主：Agent 页签行、全局配置卡、实际配置列表与
// 编辑器分发；Claude/Codex/Claude Desktop/指南各自在子包，本包不反向被
// 子包依赖。
//
// access.go 是接入页（spec 2026-10-18-agent-access-config-design）：每 Agent
// （Claude Code / Codex，后者覆盖 CLI / 桌面版 / IDE 插件）多实际配置 +
// 全局配置（合并进所有实际配置），
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
	if st.Access.App != "guide" && (st.Access.Form.Open || st.Access.DesktopForm.Open) {
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
		uikit.PageHeader(c, t, uikit.PageHeaderOpts{
			Title: "接入", Subtitle: "为每个 Agent 管理网关接入、全局配置与实际配置",
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
				if st.Access.Glob == nil {
					reloadGlobal(st, acc)
				}

				// 全局配置卡（Desktop 无 env 合成层，不显示）
				if st.Access.App != "claude_desktop" {
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

				// 实际配置列表卡
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
							ui.TextInput(c, &st.Access.Filter).Grow(1).
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

				// 删除二次确认（行内「删除」只武装，弹窗确认才执行）
				deleteConfirm(c, st, acc)
				// 还原备份确认（覆盖 live 文件前先备份当前状态，可再次恢复）
				restoreConfirm(c, st, acc)
			})
		})
	})
}

// editorPage 是接入编辑器的独立子页：页头返回 + 当前 Agent 标题，正文只
// 渲染对应编辑器（Claude/Codex/Claude Desktop）。
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
				} else if st.Access.App == "codex" {
					codex.EditorCard(c, t, s, st, acc, gateOK)
				} else {
					claude.EditorCard(c, t, s, st, acc, gateOK)
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
			moreLabel := "更多"
			if st.Access.RowMore == v.ID {
				moreLabel = "收起"
			}
			if uikit.SoftButton(c, t, s, "gray", moreLabel).Clicked() {
				if st.Access.RowMore == v.ID {
					st.Access.RowMore = ""
				} else {
					st.Access.RowMore = v.ID
				}
			}
		})
		// 次级操作收纳在「更多」展开行：主次分明，窄窗可换行。
		if st.Access.RowMore == v.ID {
			ui.Row(c).Gap(8).Wrap().Children(func() {
				if gateOK {
					if uikit.SoftButton(c, t, s, "gray", "复制启动命令").Clicked() {
						cmd, err := acc.LaunchCommand(v.ID)
						if err != nil {
							st.Access.Msg = "生成启动命令失败：" + err.Error()
						} else {
							mygo.Clipboard.Write(transfer.TextData(cmd))
							st.Access.Msg = "启动命令已复制"
						}
					}
					if uikit.SoftButton(c, t, s, "gray", "打开终端").Clicked() {
						if err := acc.OpenTerminal(v.ID); err != nil {
							st.Access.Msg = "打开终端失败：" + err.Error()
						} else {
							st.Access.Msg = ""
						}
					}
				}
				if uikit.SoftButton(c, t, s, "danger", "删除").Clicked() {
					st.Access.DeleteArm = true
					st.Access.DeleteID = v.ID
				}
			})
		}
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
			if st.Access.RowMore == st.Access.DeleteID {
				st.Access.RowMore = ""
			}
			st.Access.DeleteID = ""
		},
	})
}

// tabDirty 报告接入页是否存在未保存的编辑器内容（两套表单任一）。
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
// （claude/codex 有 env 合成层，需要重载全局配置行）。
func switchTab(st *state.State, acc bridge.AccessConfigs, app string) {
	st.Access.App = app
	st.Access.Msg = ""
	st.Access.Form = state.AccessForm{}
	st.Access.DesktopForm = state.DesktopForm{}
	st.Access.RowMore = ""
	if app == "claude" || app == "codex" {
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
