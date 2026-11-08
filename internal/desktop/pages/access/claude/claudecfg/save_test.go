package claudecfg

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
	corecfg "AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/desktop/state"
	"AgentSwitch/internal/desktop/uikit"
	"AgentSwitch/internal/desktop/uitest"
)

// loadedUserState 建立临时 HOME 下的用户级 settings.json，并把共享状态
// 加载到 ready。返回值中的 file 是实际写入路径。
func loadedUserState(t *testing.T) (*state.State, bridge.ClaudeConfigs, string) {
	t.Helper()
	st, _ := uitest.New(t)
	home := t.TempDir()
	st.ClaudeCfg.Home = home
	file := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(`{"model":"sonnet"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfgs := bridge.ClaudeConfigs{A: st.Svc.Gateway.A, Home: home}
	wait := uitest.AsyncWaiter(t, st)
	load(st, cfgs)
	wait()
	if !st.ClaudeCfg.Loaded {
		t.Fatalf("load failed: %s", st.ClaudeCfg.Msg)
	}
	return st, cfgs, file
}

func renderSaveDialog(st *state.State, cfgs bridge.ClaudeConfigs) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		saveDialog(c, st, cfgs)
	}, 900, 800)
}

func TestSaveDialogListsPathChipsBackupAndEffects(t *testing.T) {
	st, cfgs, file := loadedUserState(t)
	cf := &st.ClaudeCfg
	cf.Doc = cloneDoc(cf.Baseline)
	corecfg.Set(cf.Doc, "model", "opus")
	corecfg.Set(cf.Doc, "effortLevel", "high")
	corecfg.Set(cf.Doc, "autoCompactEnabled", false)
	RecomputeDirty(cf)
	cf.SaveArm = true

	tt := renderSaveDialog(st, cfgs)
	for _, want := range []string{
		"保存到用户级配置？", "目标文件", file,
		"model", "effortLevel", "autoCompactEnabled",
		"备份目录", filepath.Join(cf.Home, ".agentswitch", "backups", "claude-settings"),
		"已热生效", "需重启", "取消", "保存",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if !tt.Focused("取消") {
		t.Fatal("cancel must be the default-focused action")
	}
}

func TestSaveDialogCapsChangedChipsAtEight(t *testing.T) {
	st, cfgs, _ := loadedUserState(t)
	cf := &st.ClaudeCfg
	cf.Baseline = map[string]any{}
	cf.Doc = map[string]any{}
	paths := []string{
		"model", "fallbackModel", "effortLevel", "alwaysThinkingEnabled",
		"permissions.defaultMode", "permissions.allow", "permissions.ask", "permissions.deny",
		"autoCompactEnabled", "autoMemoryEnabled",
	}
	for _, path := range paths {
		corecfg.Set(cf.Doc, path, "x")
	}
	RecomputeDirty(cf)
	cf.SaveArm = true

	tt := renderSaveDialog(st, cfgs)
	if !tt.HasText("等 2 个") {
		t.Fatalf("must collapse the remaining chips: %v", tt.Texts())
	}
}

func TestSaveDialogWarnsAboutProjectSharedSecrets(t *testing.T) {
	st, cfgs, _ := loadedUserState(t)
	cf := &st.ClaudeCfg
	cf.Layer, cf.Sub = "project", "shared"
	cf.Project = t.TempDir()
	cf.File = filepath.Join(cf.Project, ".claude", "settings.json")
	cf.Display = cf.File
	cf.Exists = true
	cf.Baseline = map[string]any{}
	cf.Doc = map[string]any{
		"env": map[string]any{"CUSTOM_TOKEN": "sk-abcdefghijklmnopqrstuvwxyz123456"},
	}
	RecomputeDirty(cf)
	cf.SaveArm = true

	tt := renderSaveDialog(st, cfgs)
	for _, want := range []string{"保存到项目共享配置？", "所有协作者", "疑似密钥"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
}

func TestSaveDialogBlockedByManagedKeyEdit(t *testing.T) {
	st, cfgs, _ := loadedUserState(t)
	cf := &st.ClaudeCfg
	cf.Managed = corecfg.ManagedSet{StrongEnv: []string{"ANTHROPIC_AUTH_TOKEN"}}
	cf.Baseline = map[string]any{}
	cf.Doc = map[string]any{"env": map[string]any{"ANTHROPIC_AUTH_TOKEN": "secret"}}
	RecomputeDirty(cf)
	cf.SaveArm = true

	tt := renderSaveDialog(st, cfgs)
	if tt.HasText("保存到用户级配置？") {
		t.Fatal("blocked save must not open the dialog")
	}
	if cf.SaveArm {
		t.Fatal("blocked save must close the arm flag")
	}
	if !strings.Contains(cf.Msg, "受管") {
		t.Fatalf("block reason must be reported: %q", cf.Msg)
	}
}

func TestSaveResultBarShowsThreeEffectsAndActions(t *testing.T) {
	st, cfgs, _ := loadedUserState(t)
	cf := &st.ClaudeCfg
	cf.Result = &bridge.SaveResult{
		File:       "/tmp/settings.json",
		BackupPath: "/tmp/backups/1-settings.json",
		Changed:    []string{"model"},
		Effects:    []string{"已热生效", "需重启", "下次启动"},
	}
	cf.ResultVisible = true

	tt := ui.NewTester(func(c *ui.Context) {
		t := c.Theme()
		saveResultBar(c, t, uikit.Softs(t), st, cfgs)
	}, 900, 300)
	for _, want := range []string{
		"已热生效", "需重启", "下次启动", "/tmp/settings.json",
		"/tmp/backups/1-settings.json", "查看备份", "关闭结果",
	} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in %v", want, tt.Texts())
		}
	}
	if err := tt.Click("查看备份"); err != nil {
		t.Fatal(err)
	}
	if !cf.BackupOpen {
		t.Fatal("查看备份 must open the backup panel")
	}
	if err := tt.Click("关闭结果"); err != nil {
		t.Fatal(err)
	}
	if cf.ResultVisible {
		t.Fatal("关闭结果 must hide the result bar")
	}
}

func TestNextEditClearsSaveResult(t *testing.T) {
	st, _, _ := loadedUserState(t)
	cf := &st.ClaudeCfg
	cf.Result = &bridge.SaveResult{Effects: []string{"已热生效"}}
	cf.ResultVisible = true
	corecfg.Set(cf.Doc, "model", "opus")
	RecomputeDirty(cf)
	if cf.ResultVisible {
		t.Fatal("a new edit must clear the previous save result")
	}
}

func TestSaveSuccessShowsResultAndBackupEntry(t *testing.T) {
	st, _, file := loadedUserState(t)
	cf := &st.ClaudeCfg
	corecfg.Set(cf.Doc, "model", "opus")
	RecomputeDirty(cf)
	cf.SaveArm = true

	tt := ui.NewTester(func(c *ui.Context) {
		Page(c, st)
	}, 1200, 1200)
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	doc, err := corecfg.ParseDoc(mustRead(t, file))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Raw["model"] != "opus" {
		t.Fatalf("saved doc = %v", doc.Raw)
	}
	if !cf.ResultVisible || cf.Result == nil {
		t.Fatal("successful save must show the result bar")
	}
	if cf.Result.BackupPath == "" {
		t.Fatal("saving over an existing file must create a backup")
	}
	if _, err := os.Stat(cf.Result.BackupPath); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if !tt.HasText("已写入") || !tt.HasText("查看备份") {
		t.Fatalf("result bar text missing: %v", tt.Texts())
	}

	if err := tt.Click("查看备份"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("备份与恢复") {
		t.Fatalf("backup panel missing: %v", tt.Texts())
	}
	if len(cf.Backups) == 0 {
		t.Fatal("backup panel must list the current layer backups")
	}

	if err := tt.Click("关闭结果"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("已写入") {
		t.Fatalf("result bar must stay hidden after close: %v", tt.Texts())
	}
}

// TestUserSaveDoesNotInjectDangerousSkip 回归（C2）：用户级保存不得静默注入
// skipDangerousModePermissionPrompt=true——三态控件的「继承」即键缺失。
func TestUserSaveDoesNotInjectDangerousSkip(t *testing.T) {
	st, cfgs, file := loadedUserState(t)
	cf := &st.ClaudeCfg
	if _, ok := cf.Doc["skipDangerousModePermissionPrompt"]; ok {
		t.Fatal("precondition: doc must not contain the key")
	}
	setString(cf.Doc, "model", "opus")
	RecomputeDirty(cf)
	performSave(st, cfgs)
	if !cf.ResultVisible {
		t.Fatalf("save must succeed: %s", cf.Msg)
	}
	doc, err := corecfg.ParseDoc(mustRead(t, file))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Raw["skipDangerousModePermissionPrompt"]; ok {
		t.Fatalf("save must not inject the key: %v", doc.Raw)
	}
	if doc.Raw["model"] != "opus" {
		t.Fatalf("model edit must persist: %v", doc.Raw)
	}
}

// TestSaveDialogWarnsWeakManagedEnvKeys 回归（I4）：弱受管 env 键在变更列表中
// 时，确认框给出「接入应用可能覆盖」警告行。
func TestSaveDialogWarnsWeakManagedEnvKeys(t *testing.T) {
	st, cfgs, _ := loadedUserState(t)
	cf := &st.ClaudeCfg
	cf.Managed = corecfg.ManagedSet{WeakEnv: []string{"env.HTTPS_PROXY"}}
	corecfg.Set(cf.Doc, "env.HTTPS_PROXY", "http://proxy:8080")
	RecomputeDirty(cf)
	cf.SaveArm = true

	tt := renderSaveDialog(st, cfgs)
	if !tt.HasText("接入应用可能覆盖") {
		t.Fatalf("weak-managed warning missing: %v", tt.Texts())
	}
	if !tt.HasText("env.HTTPS_PROXY") {
		t.Fatalf("weak-managed key must be listed: %v", tt.Texts())
	}

	// 无弱受管变更时不出现警告行。
	cf2 := &st.ClaudeCfg
	cf2.Doc = cloneDoc(cf2.Baseline)
	corecfg.Set(cf2.Doc, "model", "opus")
	RecomputeDirty(cf2)
	cf2.SaveArm = true
	tt = renderSaveDialog(st, cfgs)
	if tt.HasText("接入应用可能覆盖") {
		t.Fatalf("warning must not appear without weak changes: %v", tt.Texts())
	}
}

func TestConflictDialogOverwritesWithDiskHash(t *testing.T) {
	st, _, file := loadedUserState(t)
	cf := &st.ClaudeCfg
	corecfg.Set(cf.Doc, "model", "opus")
	RecomputeDirty(cf)
	cf.SaveArm = true

	// 打开页面后，外部程序改写了文件。
	if err := os.WriteFile(file, []byte(`{"model":"haiku"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	tt := ui.NewTester(func(c *ui.Context) {
		Page(c, st)
	}, 1200, 1200)
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if !cf.ConflictArm {
		t.Fatal("a changed disk hash must open the conflict dialog")
	}
	for _, want := range []string{"文件已被其他程序修改", "取消", "重新加载", "覆盖写入"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in conflict dialog: %v", want, tt.Texts())
		}
	}
	if !tt.Focused("取消") {
		t.Fatal("cancel must be the default conflict action")
	}

	if err := tt.Click("覆盖写入"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	doc, err := corecfg.ParseDoc(mustRead(t, file))
	if err != nil {
		t.Fatal(err)
	}
	if doc.Raw["model"] != "opus" {
		t.Fatalf("overwrite must retry with the disk hash: %v", doc.Raw)
	}
	if cf.ConflictArm {
		t.Fatal("overwrite must close the conflict dialog")
	}
	if !cf.ResultVisible {
		t.Fatal("overwrite must produce a save result")
	}
}

func TestRestoreBackupOnlyTouchesCurrentLayer(t *testing.T) {
	st, cfgs, file := loadedUserState(t)
	cf := &st.ClaudeCfg

	// 用户级：把 sonnet 备份下来，再写 opus。
	corecfg.Set(cf.Doc, "model", "opus")
	RecomputeDirty(cf)
	performSave(st, cfgs)
	if !cf.ResultVisible {
		t.Fatal("initial save must succeed")
	}

	// 项目层也生成一份备份，用来验证恢复不会跨层写错文件。
	proj := t.TempDir()
	if _, err := cfgs.SaveClaudeConfigLayer(bridge.SaveClaudeConfigRequest{
		Kind: corecfg.ScopeProject, Project: proj, Sub: "shared",
		Document: map[string]any{"model": "a"}, Create: true,
	}); err != nil {
		t.Fatal(err)
	}
	view, err := cfgs.ReadClaudeConfigLayer(bridge.ReadLayerRequest{
		Kind: corecfg.ScopeProject, Project: proj, Sub: "shared",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfgs.SaveClaudeConfigLayer(bridge.SaveClaudeConfigRequest{
		Kind: corecfg.ScopeProject, Project: proj, Sub: "shared",
		Document: map[string]any{"model": "b"}, ExpectedHash: view.Hash,
	}); err != nil {
		t.Fatal(err)
	}
	projectFile := view.File
	projectBefore := mustRead(t, projectFile)

	// 外部改写用户文件，然后从用户级备份恢复。
	if err := os.WriteFile(file, []byte(`{"model":"haiku"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	wait := uitest.AsyncWaiter(t, st)
	load(st, cfgs)
	wait()
	openBackups(st, cfgs)
	if len(cf.Backups) == 0 {
		t.Fatal("user layer must have a backup")
	}
	backupsBefore := len(cf.Backups)

	target := cf.Backups[0]
	requestRestore(cf, target)
	tt := ui.NewTester(func(c *ui.Context) {
		restoreBackupDialog(c, st, cfgs)
	}, 700, 500)
	for _, want := range []string{"恢复此备份？", file, "取消", "恢复"} {
		if !tt.HasText(want) {
			t.Fatalf("missing %q in restore dialog: %v", want, tt.Texts())
		}
	}
	if err := tt.Click("恢复"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	restored, err := corecfg.ParseDoc(mustRead(t, file))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Raw["model"] != "sonnet" {
		t.Fatalf("restore must write the backup content: %v", restored.Raw)
	}
	if !bytes.Equal(projectBefore, mustRead(t, projectFile)) {
		t.Fatal("restore must not touch another layer's file")
	}
	after, err := cfgs.ListClaudeConfigBackups(bridge.ReadLayerRequest{Kind: corecfg.ScopeUser})
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != backupsBefore+1 {
		t.Fatalf("restore must snapshot the current file first: before=%d after=%d", backupsBefore, len(after))
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
