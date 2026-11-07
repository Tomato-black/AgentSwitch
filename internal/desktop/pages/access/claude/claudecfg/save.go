package claudecfg

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
)

// saveDialog 是保存确认框：目标文件、变更键 chip（最多 8 个）、备份目录
// 与生效范围。受管键/scope/JSON 问题在打开前阻断并落到 Msg。
func saveDialog(c *ui.Context, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	if !cf.SaveArm {
		return
	}
	if reason := saveBlockReason(cf); reason != "" {
		cf.SaveArm = false
		cf.Msg = reason
		return
	}
	t := c.Theme()
	changed := corecfg.Diff(cf.Baseline, cf.Doc)
	title := "保存到用户级配置？"
	if cf.Layer == "project" {
		if cf.Sub == "local" {
			title = "保存到项目本地配置？"
		} else {
			title = "保存到项目共享配置？"
		}
	}
	uikit.RichDialog(c, t, uikit.RichDialogOpts{
		Open:    &cf.SaveArm,
		Title:   title,
		Width:   560,
		Body:    func() { saveDialogBody(c, t, cf, changed) },
		Actions: saveActions(st, cfgs),
	})
}

func saveActions(st *state.State, cfgs bridge.ClaudeConfigs) []uikit.RichDialogAction {
	return []uikit.RichDialogAction{
		{Label: "取消", Default: true},
		{Label: "保存", Primary: true, OnClick: func() { performSave(st, cfgs) }},
	}
}

func saveDialogBody(c *ui.Context, t *ui.Theme, cf *state.ClaudeCfgState, changed []string) {
	s := uikit.Softs(t)
	ui.Column(c).Gap(t.Space(2)).Children(func() {
		dialogKV(c, t, "目标文件", cf.File)
		ui.Row(c).Gap(t.Space(2)).AlignItems(ui.Start).Children(func() {
			ui.Text(c, "变更键").TextColor(t.TextMuted)
			ui.Column(c).Gap(2).Children(func() {
				show := changed
				rest := 0
				if len(show) > 8 {
					show = show[:8]
					rest = len(changed) - 8
				}
				if len(show) == 0 {
					ui.Text(c, "无").TextColor(t.TextMuted)
				}
				for _, path := range show {
					ui.Text(c, path).Font(uikit.MonoFont).FontSize(t.FontSize - 1)
				}
				if rest > 0 {
					ui.Text(c, fmt.Sprintf("等 %d 个", rest)).TextColor(t.TextMuted)
				}
			})
		})
		dialogKV(c, t, "备份目录", backupRootDisplay(cf))
		if weak := weakManagedChangedKeys(cf, changed); len(weak) > 0 {
			ui.Text(c, strings.Join(weak, ", ")+"：接入应用可能覆盖此值").TextColor(t.Warning)
		}
		ui.Row(c).Gap(6).Children(func() {
			ui.Text(c, "生效范围").TextColor(t.TextMuted)
			for _, label := range effectLabels(changed) {
				uikit.SoftBadge(c, t, s, "ok", label)
			}
		})
		if cf.Layer == "project" && cf.Sub == "shared" {
			if corecfg.ContainsSecretLike(cf.Doc) {
				ui.Text(c, "检测到疑似密钥：项目共享会提交给所有协作者，建议改存项目本地。").TextColor(t.Warning)
			} else {
				ui.Text(c, "项目共享文件通常会被提交到 Git，对所有协作者生效。").TextColor(t.TextMuted)
			}
		}
		ui.Text(c, "写入前会备份当前文件；只改以上键，其余设置原样保留。").TextColor(t.TextMuted)
	})
}

func dialogKV(c *ui.Context, t *ui.Theme, label, value string) {
	ui.Row(c).Gap(6).Children(func() {
		ui.Text(c, label).TextColor(t.TextMuted)
		ui.Text(c, value).Font(uikit.MonoFont).SingleLine()
	})
}

// weakManagedChangedKeys 返回变更列表中属于弱受管 env 的键（I4）：保存不
// 阻断，但要在确认框警告「接入应用可能覆盖」。
func weakManagedChangedKeys(cf *state.ClaudeCfgState, changed []string) []string {
	if len(cf.Managed.WeakEnv) == 0 || len(changed) == 0 {
		return nil
	}
	weak := make(map[string]bool, len(cf.Managed.WeakEnv))
	for _, k := range cf.Managed.WeakEnv {
		weak[k] = true
	}
	var out []string
	for _, path := range changed {
		if weak[path] {
			out = append(out, path)
		}
	}
	return out
}

func backupRootDisplay(cf *state.ClaudeCfgState) string {
	if cf.Home == "" {
		return "~/.agentswitch/backups/claude-settings"
	}
	return filepath.Join(cf.Home, ".agentswitch", "backups", "claude-settings")
}

// effectLabels 按键注册表把变更键映射为生效范围标签；未知键保守按
// 「下次启动」处理。
func effectLabels(changed []string) []string {
	order := []string{"已热生效", "需重启", "下次启动"}
	found := map[string]bool{}
	for _, path := range changed {
		kind := corecfg.ApplyUnknown
		if spec, ok := corecfg.Lookup(path); ok {
			kind = spec.Apply
		}
		found[applyLabel(kind)] = true
	}
	var out []string
	for _, label := range order {
		if found[label] {
			out = append(out, label)
		}
	}
	return out
}

func applyLabel(kind corecfg.ApplyKind) string {
	switch kind {
	case corecfg.ApplyHot:
		return "已热生效"
	case corecfg.ApplyRestart:
		return "需重启"
	default:
		return "下次启动"
	}
}

// performSave 执行保存：阻断校验 → 组装请求 → 冲突时打开三动作对话框。
func performSave(st *state.State, cfgs bridge.ClaudeConfigs) {
	saveWithHash(st, cfgs, "")
}

func saveWithHash(st *state.State, cfgs bridge.ClaudeConfigs, expectedHash string) {
	cf := &st.ClaudeCfg
	if reason := saveBlockReason(cf); reason != "" {
		cf.Msg = reason
		return
	}
	// 不在此注入 skipDangerousModePermissionPrompt：三态控件的「继承」就是键
	// 缺失，静默补 true 会违背确认框「只改以上键」的承诺（C2 回归）。
	hash := cf.Hash
	if expectedHash != "" {
		hash = expectedHash
	}
	res, err := cfgs.SaveClaudeConfigLayer(bridge.SaveClaudeConfigRequest{
		Kind:         scopeOf(cf.Layer, cf.Sub),
		Project:      cf.Project,
		Sub:          cf.Sub,
		Document:     cf.Doc,
		ExpectedHash: hash,
		Create:       !cf.Exists,
	})
	var ce *bridge.ConflictError
	if errors.As(err, &ce) {
		cf.ConflictArm = true
		cf.ConflictPath = ce.Path
		cf.ConflictDiskHash = ce.DiskHash
		cf.Msg = err.Error()
		return
	}
	if err != nil {
		cf.Msg = err.Error()
		return
	}
	applySaveResult(st, cfgs, res, "已写入")
}

// applySaveResult 展示保存/恢复结果条并重载当前层。先 load 再写结果，
// 避免 load 清掉 Msg 与结果条。
func applySaveResult(st *state.State, cfgs bridge.ClaudeConfigs, res bridge.SaveResult, headline string) {
	cf := &st.ClaudeCfg
	load(st, cfgs)
	cf.Result = &res
	cf.ResultVisible = true
	cf.ResultHeadline = headline + " " + res.File
	cf.Msg = ""
}

// conflictDialog 是外部修改冲突的三动作对话框：取消（默认）/ 重新加载 /
// 覆盖写入（危险，末位）。
func conflictDialog(c *ui.Context, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	if !cf.ConflictArm {
		return
	}
	t := c.Theme()
	diskHash := cf.ConflictDiskHash
	uikit.RichDialog(c, t, uikit.RichDialogOpts{
		Open:     &cf.ConflictArm,
		Title:    "文件已被其他程序修改",
		Subtitle: cf.ConflictPath,
		Width:    520,
		Body: func() {
			ui.Column(c).Gap(t.Space(2)).Children(func() {
				ui.Text(c, "磁盘上的内容与打开时不同。重新加载会丢失当前修改；覆盖写入会用当前内容覆盖外部修改（外部内容已自动备份）。").TextColor(t.TextMuted)
			})
		},
		Actions: []uikit.RichDialogAction{
			{Label: "取消", Default: true},
			{Label: "重新加载", OnClick: func() { load(st, cfgs) }},
			{Label: "覆盖写入", Danger: true, OnClick: func() { saveWithHash(st, cfgs, diskHash) }},
		},
	})
}

// saveResultBar 是保存/恢复后的结果条：标题、生效范围、备份路径与
// 「查看备份 / 关闭结果」。
func saveResultBar(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	if !cf.ResultVisible || cf.Result == nil {
		return
	}
	res := cf.Result
	headline := cf.ResultHeadline
	if headline == "" {
		headline = "已写入 " + res.File
	}
	uikit.Card(c, t, func() {
		ui.Column(c).Gap(t.Space(2)).Children(func() {
			ui.Text(c, headline).Bold()
			if len(res.Effects) > 0 {
				ui.Row(c).Gap(4).Children(func() {
					ui.Text(c, "生效范围").TextColor(t.TextMuted)
					for _, label := range res.Effects {
						uikit.SoftBadge(c, t, s, "ok", label)
					}
				})
			}
			if res.BackupPath != "" {
				dialogKV(c, t, "备份", res.BackupPath)
			}
			ui.Row(c).Gap(t.Space(2)).Children(func() {
				if uikit.SoftButton(c, t, s, "act", "查看备份").Clicked() {
					openBackups(st, cfgs)
				}
				if uikit.SoftButton(c, t, s, "gray", "关闭结果").Clicked() {
					cf.ResultVisible = false
				}
			})
		})
	})
}

// backupPanel 是「备份与恢复」面板：当前层的备份列表与逐条恢复入口。
func backupPanel(c *ui.Context, t *ui.Theme, s uikit.SoftPalette, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	if !cf.BackupOpen {
		return
	}
	uikit.Card(c, t, func() {
		ui.Column(c).Gap(t.Space(2)).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.Text(c, "备份与恢复").Bold()
				ui.Box(c).Grow(1)
				if uikit.SoftButton(c, t, s, "gray", "收起").Clicked() {
					cf.BackupOpen = false
				}
			})
			if cf.BackupMsg != "" {
				ui.Text(c, cf.BackupMsg).TextColor(t.Warning)
			}
			if len(cf.Backups) == 0 {
				ui.Text(c, "当前层还没有备份。").TextColor(t.TextMuted)
				return
			}
			for i := range cf.Backups {
				b := cf.Backups[i]
				ui.Row(c).Gap(8).Children(func() {
					ui.Text(c, backupTime(b.CreatedAt)).TextColor(t.TextMuted)
					ui.Text(c, fmt.Sprintf("%d B", b.Size)).Font(uikit.MonoFont)
					ui.Box(c).Grow(1)
					if uikit.SoftButton(c, t, s, "gray", "恢复").Clicked() {
						requestRestore(cf, b)
					}
				})
			}
			ui.Text(c, "恢复会把所选备份写回目标文件；当前文件会先自动快照一份。").TextColor(t.TextMuted)
		})
	})
}

func backupTime(unixMilli int64) string {
	if unixMilli <= 0 {
		return "未知时间"
	}
	return time.UnixMilli(unixMilli).Format("2006-01-02 15:04")
}

// openBackups 加载当前层备份并打开面板。
func openBackups(st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	list, err := cfgs.ListClaudeConfigBackups(bridge.ReadLayerRequest{
		Kind: scopeOf(cf.Layer, cf.Sub), Project: cf.Project, Sub: cf.Sub,
	})
	if err != nil {
		cf.BackupMsg = err.Error()
	} else {
		cf.BackupMsg = ""
	}
	cf.Backups = list
	cf.BackupOpen = true
}

// requestRestore 武装恢复确认框。
func requestRestore(cf *state.ClaudeCfgState, target bridge.ClaudeBackupInfo) {
	cf.RestoreID = target.ID
	cf.RestoreLabel = target.Path
	cf.RestoreArm = true
}

// restoreBackupDialog 是恢复确认框：目标路径 + 危险提醒；恢复本身先快照
// 当前文件，可再次撤销。
func restoreBackupDialog(c *ui.Context, st *state.State, cfgs bridge.ClaudeConfigs) {
	cf := &st.ClaudeCfg
	if !cf.RestoreArm {
		return
	}
	t := c.Theme()
	uikit.RichDialog(c, t, uikit.RichDialogOpts{
		Open:     &cf.RestoreArm,
		Title:    "恢复此备份？",
		Subtitle: cf.File,
		Width:    520,
		Body: func() {
			ui.Column(c).Gap(t.Space(2)).Children(func() {
				dialogKV(c, t, "备份文件", cf.RestoreLabel)
				ui.Text(c, "恢复会覆盖目标文件的当前内容（当前内容会先自动备份一份）。").TextColor(t.TextMuted)
			})
		},
		Actions: []uikit.RichDialogAction{
			{Label: "取消", Default: true},
			{Label: "恢复", Primary: true, OnClick: func() {
				res, err := cfgs.RestoreClaudeConfigBackup(cf.RestoreID)
				if err != nil {
					cf.Msg = err.Error()
					return
				}
				applySaveResult(st, cfgs, res, "已恢复")
				openBackups(st, cfgs)
			}},
		},
	})
}
