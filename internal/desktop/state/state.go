// Package state 是桌面 UI 的共享状态：组合根（internal/desktop）与各页
// 子包（pages/*）共同消费。叶子数据包——只依赖 bridge/telemetry，
// 不 import uikit 或任何页面，避免环依赖。
package state

import (
	"context"
	"errors"
	"strings"
	"sync"

	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/claudecfg"
	"AgentSwitch/internal/telemetry"
)

// Services 是桌面壳持有的桥接服务（mygo.Bind 与各页共用同一实例；
// 由组合根填充）。
type Services struct {
	Gateway  bridge.Gateway
	Channels bridge.Channels
	Logical  *bridge.LogicalModels
}

// State 是桌面 UI 的全局状态：壳层字段 + 各页状态（随页面搬迁逐步填充）。
type State struct {
	Svc    Services
	Recent []telemetry.RequestRecord // 最近请求记录（渠道页健康统计；壳层 OnRecord 头插）

	Page            string       // 当前页；空库默认渠道，其余默认逻辑模型（经 normalizedPage 归一）
	AlertMsg        string       // 最近一次故障/冷却告警（侧栏显示；系统通知另行发送）
	Dispatch        func(func()) // 在 UI 主线程执行异步任务回调（组合根注入 Window.Update；测试可同步执行）
	CloseNotice     bool         // 首关驻留模态 open 位
	StopArm         bool         // 停止网关确认弹窗 open 位
	NavArm          bool         // 未保存编辑的切页确认 open 位
	NavTo           string       // 待切换的页面 id（NavAction 为空时仅改 Page）
	NavAction       func()       // 待执行的页面动作（如返回列表清空子视图）
	CloseConfirmed  bool         // 用户已确认驻留；之后的关窗直接隐藏
	Quitting        bool         // 退出流程中：放行窗口关闭（OnBeforeQuit 置位）
	TrayUnavailable bool         // 托盘创建失败：关窗提示改为「再次启动唤回」
	HideWindow      func()       // 隐藏当前主窗（openWindow 注入；测试可为 nil）
	Schedule        func(func()) // 把窗口操作推迟出 GTK 事件回调（默认 afterEvent）

	Channels  ChannelState
	Logical   LogicalState
	Access    AccessState
	ClaudeCfg ClaudeCfgState
	Settings  SettingsState
}

// AccessState 是接入页状态：当前 Agent 页签、全局配置编辑行、列表/编辑器
// 提示位与两类表单（通用接入文档表单 / Claude Desktop 角色映射表单）。
type AccessState struct {
	App         string // "claude" | "codex" | "claude_desktop" | "guide"
	Filter      string
	Glob        [][2]string
	GlobMsg     string
	Msg         string
	GuideMsg    string
	Form        AccessForm
	DesktopForm DesktopForm
	DeleteArm   bool   // 删除接入配置 AlertDialog open 位
	ApplyID     string // 正在应用的配置 id（空=无应用任务）
	ApplyTask   Async  // 应用任务状态（异步执行，防主线程冻结）
	DeleteID    string // 删除武装对象——确认仅对匹配 id 生效
	SwitchArm   bool   // 页签切换确认 AlertDialog open 位（有未保存编辑时）
	SwitchTo    string // 待切换的 Agent 页签
	RestoreArm  bool   // 还原最近备份确认 AlertDialog open 位

	// 备份与恢复子页（P1-1a）：列出历史备份并选择恢复。
	BackupOpen bool
	Backups    []bridge.BackupInfo
	BackupMsg  string
	RestoreTS  int64
	BackupArm  bool // 所选备份的还原确认
}

// ClaudeCfgState 是 Claude Code 设置区状态。Doc 是当前层工作文档，
// Baseline 是打开/上次保存时的文档；Dirty 由 diff 计算。
type ClaudeCfgState struct {
	Loaded       bool
	Loading      bool
	Home         string // 测试注入；空=os.UserHomeDir()
	Layer        string // user/project/managed
	Sub          string // shared/local
	Project      string
	ProjectInput string
	// 项目本地层候选路径（Git 根 ≠ 项目目录时 >1）；LocalPath 是用户显式
	// 选定的候选（空=默认首候选），随 ReadLayerRequest/保存请求透传 bridge。
	Candidates []string
	LocalPath  string
	View         string // form/json/merged
	File         string
	Display      string
	Exists       bool
	Hash         string
	Doc          map[string]any
	Baseline     map[string]any
	Dirty        bool
	Msg          string
	Warning      string
	ParseErr     string
	JSONText     string
	MergeText    string

	// JSON 视图与合并预览的交互状态（仅 UI 用，不参与保存）。
	JSONPending  bool  // 300ms 防抖解析在等待
	JSONDueNano  int64 // 防抖截止（c.Now().UnixNano()）
	JSONResetArm bool  // 「重置」确认框 open 位
	JSONHint     string
	MergeQuery   string
	MergeOpen    map[string]bool // 按路径展开贡献值
	MergeReveal  map[string]bool // 按路径显示敏感值

	ModelInput    string
	FallbackInput string
	DangerInput   bool
	SkillStates   map[string]string

	// 配置页表单的只读派生数据：受管集合与跨层合并来源。由 load 一次性
	// 计算，渲染帧只读，避免逐帧查库。
	Managed      claudecfg.ManagedSet
	MergeEntries map[string]claudecfg.MergedEntry
	Violations   []claudecfg.Violation

	// Reveal 是敏感值「显示」开关（按路径）。
	Reveal map[string]bool

	// 环境变量与插件的新增行输入（跨帧保存）；ChipInputs 是列表 chip 的
	// 添加行输入（按键路径）。
	EnvExpanded bool
	EnvNewKey   string
	EnvNewValue string
	PluginNew   string
	ChipInputs  map[string]string
	FormMsg     string
	SaveArm     bool

	// 保存结果条（成功保存/恢复后显示，直到下一次编辑或手动关闭）。
	Result         *bridge.SaveResult
	ResultVisible  bool
	ResultRestore  bool
	ResultHeadline string

	// 保存冲突对话框：保存时磁盘 Hash 与打开时不一致，允许重新加载或
	// 以 ConflictDiskHash 为 ExpectedHash 覆盖写入。
	ConflictArm      bool
	ConflictPath     string
	ConflictDiskHash string

	// 当前层备份列表与恢复确认（ID 即 RestoreClaudeConfigBackup 的入参）。
	BackupOpen   bool
	Backups      []bridge.ClaudeBackupInfo
	BackupMsg    string
	RestoreArm   bool
	RestoreID    string
	RestoreLabel string

	// ReloadArm 是「放弃未保存修改」确认框 open 位：刷新/切层/切项目在有
	// 未保存修改时先武装，PendingReload 存确认后要执行的动作（I1）。
	ReloadArm     bool
	PendingReload func()

	// OpenAccess 是受管键行「打开接入」回调（接入页宿主每帧注入；打开当前
	// 配置的接入抽屉）。nil 时该入口不渲染（如独立渲染设置区时）。
	OpenAccess func()

	// BrokenFile：磁盘文件不是合法 JSON 时的只读修复态（I5）——表单停用、
	// JSON 视图只读显示原文；RebuildArm 是「用空配置重建」危险确认 open 位。
	BrokenFile bool
	RebuildArm bool

	Projects []bridge.ClaudeProject
	Skills   []bridge.SkillView
	SkillErr string
}

// AccessForm 是接入配置新建/编辑表单状态（Open 非空即编辑）。DocJSON 是唯一
// 共享存储：编辑器显示 PreviewDoc 合成结果（含真实网关令牌），保存原文送
// bridge 归一化。角色行/Codex 模型列表均由 DocJSON 双向同步。
type AccessForm struct {
	Open        bool
	EditID      string // 非空 = 编辑该配置
	Name        string
	ModelLabel  string // 选中的逻辑模型名（保存时映射回 ID）
	DocJSON     string
	OriginalDoc string // 打开编辑器时的原文档（变更对比基线）
	Msg         string

	// 角色映射行状态（Claude 5 角色同序；由 DocJSON 双向同步）
	RoleModels []string
	RoleNames  []string
	RoleOneM   []bool
	MainOneM   bool // 主模型 1M 勾选（ANTHROPIC_MODEL 半锁定后缀）

	// Claude 模型选择行（2026-11-07 归接入）：ModelKey 是顶层 model 键
	//（空＝跟随网关），FallbackModels 是顶层 fallbackModel 有序链。
	ModelKey       string
	FallbackModels []string

	// Codex 模型列表与高级设置（由 DocJSON models/codex 双向同步，首项即默认模型）
	CodexModels     []string
	SubagentModel   string
	SubagentEffort  string
	AutoReviewModel string
	CodexPreview    string
	ClaudePreview   string // Claude Code：预览将写入的 settings.json（live 合并结果）
	// CodexMeta 是 Codex 接入页的模型元数据编辑行（按模型名索引；2026-10-28
	// 从逻辑模型编辑页迁入——显示名/上下文窗口/思考等级只影响 Codex 目录）。
	CodexMeta map[string]*MetaEditState

	// Baseline 是打开编辑器时的内容签名，用于页签切换脏检查。
	Baseline string
}

// MetaEditState 是 Codex 模型元数据的一行编辑状态（原始字符串，保存时解析）。
type MetaEditState struct {
	DisplayName           string
	ContextWindow         string
	ReasoningLevels       string
	DefaultReasoningLevel string
	Msg                   string
}

// DesktopForm 是 Claude Desktop 接入的新建/编辑表单状态；Models/OneM 与
// bridge 角色固定序（sonnet/opus/haiku/fable）同序，Models[i] 为空 = 未启用。
type DesktopForm struct {
	Open    bool
	EditID  string // 非空 = 编辑该配置
	Name    string
	Models  [4]string
	OneM    [4]bool
	Msg     string
	Preview string // 预览将写入的网关 profile JSON

	// Baseline 是打开编辑器时的内容签名，用于页签切换脏检查。
	Baseline string
}

// Signature 返回表单内容签名（页签切换脏检查用）：名称/模型/配置 JSON。
// 角色行与 Codex 模型列表的变化都会写回 DocJSON，故无需单独计入。
func (f *AccessForm) Signature(app string) string {
	return app + "\x00" + f.Name + "\x00" + f.ModelLabel + "\x00" + f.DocJSON
}

// Dirty 报告编辑器是否有未保存修改（未打开视为干净）。
func (f *AccessForm) Dirty(app string) bool {
	return f.Open && f.Signature(app) != f.Baseline
}

// Signature 返回 Desktop 角色表单的内容签名（名称/四角色/1M 勾选）。
func (f *DesktopForm) Signature() string {
	oneM := ""
	for _, b := range f.OneM {
		if b {
			oneM += "1"
		} else {
			oneM += "0"
		}
	}
	return f.Name + "\x00" + strings.Join(f.Models[:], "\x00") + "\x00" + oneM
}

// Dirty 报告 Desktop 编辑器是否有未保存修改（未打开视为干净）。
func (f *DesktopForm) Dirty() bool {
	return f.Open && f.Signature() != f.Baseline
}

// ChannelState 是渠道页状态：View 为子视图（""=列表，"new"=新建，
// "detail"=详情），Form 是新建/详情表单（EditID 非空 = 编辑该渠道）。
type ChannelState struct {
	View         string
	Filter       string
	PresetFilter string
	DetailID     string
	Baseline     string // 打开新建/详情时的表单签名（脏检查）
	Form         ChannelForm
	KeyInput     string // 详情 Key 更新框（留空=不改）
	SuggText     string // 详情建议模型多行文本（逗号/换行分隔）
	TestMsg      string // 连通测试/操作结果信息位
	BalanceMsg   string // 上游余额查询结果
	DeleteArm    bool   // 删除渠道 AlertDialog open 位
	TestTask     Async  // 「测试连通」异步任务
	FetchTask    Async  // 「拉取模型」异步任务
	BalanceTask  Async  // 「查余额」异步任务
	DeleteID     string // 删除武装对象——弹窗仅对匹配 id 触发

	// 拉取模型的「追加/替换」确认：已有建议文本时先询问，空文本直接填入。
	FetchArm    bool
	FetchIDs    []string
	FetchTarget string // "detail" | "new"

	// 复制渠道确认框（C3）：CopyKey 勾选=同时复制 API Key（默认不复制），
	// CopyAfter 是复制成功后的落位动作（详情页打开新渠道，列表页无）。
	CopyArm   bool
	CopyID    string
	CopyKey   bool
	CopyAfter func(st *State, newID string)
}

// ChannelForm 是渠道新建/详情编辑的表单状态：APIKey 留空 = 不改密钥
// （详情态）；PresetID 由预设网格点选写入（自定义为空）。
type ChannelForm struct {
	PresetID     string
	Protocol     string
	Name         string
	BaseURL      string
	APIKey       string // 新建必填；详情留空=保持原 Key
	Suggestions  string // 逗号/换行分隔；预设回填默认模型
	Enabled      bool
	AllowPrivate bool
	Msg          string
	EditID       string // 非空 = 编辑该渠道
}

// LogicalState 是逻辑模型页状态。
type LogicalState struct {
	EditModelID string   // 编辑页锚定逻辑模型 id（空=新建）
	Filter      string   // 列表搜索
	Baseline    string   // 打开编辑页时的表单签名（脏检查）
	Edit        LMForm   // 新建/编辑表单状态（名称/描述/目标链/信息位）
	DeleteArm   bool     // 删除 AlertDialog open 位
	DeleteID    string   // 删除武装对象——确认仅对匹配 id 生效
	DeleteRefs  []string // 引用该模型的接入配置摘要（武装时计算，弹窗展示）

	// 端到端测试（P0-2）：按目标链发最小请求，结果按模型 ID 缓存。
	TestTask    Async
	TestModelID string
	TestResults map[string]bridge.ModelProbeResult
	TestMsg     string
}

// LMForm 是逻辑模型新建/编辑表单状态：Targets 即目标链行（Priority 落库
// 前按下标重排）；元数据以原始字符串保存（在 meta 校验时解析）；Msg 是
// 表单信息位（校验/保存错误）。
type LMForm struct {
	Name        string
	Description string
	Targets     []bridge.TargetSpec
	Msg         string
}

// SettingsState 是设置页状态：输入框为空时按当前生效值回显（不与输入中的
// 用户打架），保存成功后清空、下一帧回显新生效值。
type SettingsState struct {
	PortInput      string // 端口输入（为空时回显 Status.Port）
	TimeoutInput   string // 超时输入（为空时回显 Status.TimeoutSeconds）
	Msg            string // 保存结果提示
	HistoryFilter  string // 最近请求筛选（模型/渠道/状态子串）
	HistoryMsg     string // 历史区提示（导出诊断结果）
	DataMsg        string // 配置导出/导入结果提示
	ImportPreview  *bridge.ImportPreview
	ImportPath     string
	ImportArm      bool
	// 导出口令（可选加密）：文件对话框确定路径后弹口令输入，空=明文 v1。
	ExportArm bool
	ExportPath string
	ExportPwd string
	// 导入口令：包为 v2 加密信封时弹输入；ImportPassphrase 陪同预览→确认导入。
	ImportPassArm  bool
	ImportPwd      string
	ImportPassphrase string
	UsageLoaded    bool
	UsageReport    bridge.UsageReport
	DailyUsage     []bridge.DailyUsageRow
	DurationSeries []bridge.DurationPoint
	QuotaReport    []bridge.QuotaStatus
	QuotasJSON     string
	QuotaMsg       string
	UsagePrices    string
	UsageMsg       string
	AlertsOn       bool
	AlertsLoaded   bool
	Recent         []telemetry.RequestRecord // 最近请求（持久化历史，含重启前记录；区别于内存环 Recent）
	RecentWindow   int                       // 时间范围：0=全部 1=今天 2=24小时 3=7天
	RecentLoaded   bool
	RecentOpen     string // 展开尝试链的请求 ID（空=全部收起）
	PassthroughOn  bool   // 透传未登记模型开关（预留：SaveSettings 不消费）
}

// Async 是一次后台任务的状态。字段只在 UI 主线程读写：任务在 goroutine
// 执行，完成经 State.Dispatch 回到主线程更新。
type Async struct {
	mu      sync.Mutex
	running bool
	label   string // 进行中文案（如「正在拉取模型…」）
	errMsg  string // 失败信息；取消不算失败
	cancel  context.CancelFunc
}

// IsRunning 报告任务是否进行中（跨线程安全：完成回调可能在异步 goroutine
// 写状态，UI/测试在主线程读——2026-11-07 ubuntu -race 暴露）。
func (a *Async) IsRunning() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running
}

// BusyLabel 返回进行中文案（BusyRow 显示用）。
func (a *Async) BusyLabel() string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.label
}

// ErrText 返回失败信息（取消不算失败；用于结果条/错误提示）。
func (a *Async) ErrText() string {
	if a == nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.errMsg
}

// State 返回任务的一致快照（进行中标志/文案/失败信息）。
func (a *Async) State() (running bool, label, errMsg string) {
	if a == nil {
		return false, "", ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.running, a.label, a.errMsg
}

// Cancel 请求取消当前任务（按住确认/点击取消时调用）。
func (a *Async) Cancel() {
	if a == nil {
		return
	}
	a.mu.Lock()
	cancel := a.cancel
	a.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// RunAsync 启动一次可取消的后台任务：work 在 goroutine 执行，返回结果或
// 错误；完成后经 Dispatch 回主线程更新 Async 并调用 done。已在运行时不
// 重复启动。泛型 T 让拉取结果（[]string）与测试结果（文案）都能直接带出。
func RunAsync[T any](s *State, a *Async, label string, work func(context.Context) (T, error), done func(T, error)) {
	if s == nil || a == nil || a.IsRunning() {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.mu.Lock()
	a.running, a.label, a.errMsg = true, label, ""
	a.cancel = cancel
	a.mu.Unlock()
	go func() {
		result, err := work(ctx)
		finish := func() {
			a.mu.Lock()
			a.running = false
			a.cancel = nil
			if err != nil && !errors.Is(err, context.Canceled) {
				a.errMsg = err.Error()
			}
			a.mu.Unlock()
			if done != nil {
				done(result, err)
			}
		}
		if s.Dispatch != nil {
			s.Dispatch(finish)
		} else {
			finish()
		}
	}()
}

// Signature 是逻辑模型表单的脏检查签名（名称/描述/目标链）。
func (f LMForm) Signature() string {
	var b strings.Builder
	b.WriteString(f.Name)
	b.WriteByte(0)
	b.WriteString(f.Description)
	for _, t := range f.Targets {
		b.WriteByte(0)
		b.WriteString(t.ChannelID)
		b.WriteByte(1)
		b.WriteString(t.ModelName)
	}
	return b.String()
}

// Signature 是渠道新建/详情表单的脏检查签名（含详情页的 Key 输入与建议
// 文本——它们不在 ChannelForm 里）。
func (c *ChannelState) Signature() string {
	f := c.Form
	bools := func(v bool) string {
		if v {
			return "1"
		}
		return "0"
	}
	return strings.Join([]string{
		f.PresetID, f.Protocol, f.Name, f.BaseURL, f.APIKey, f.Suggestions,
		c.SuggText, c.KeyInput, bools(f.Enabled), bools(f.AllowPrivate),
	}, "\x00")
}

// DirtyCurrent 报告当前页是否有未保存编辑（壳层切页/返回列表前确认）。
func (s *State) DirtyCurrent() bool {
	switch s.Page {
	case "logical-edit":
		return s.Logical.Edit.Signature() != s.Logical.Baseline
	case "channels":
		if s.Channels.View == "new" || s.Channels.View == "detail" {
			return s.Channels.Signature() != s.Channels.Baseline
		}
	case "access":
		return s.Access.Form.Dirty(s.Access.App) || s.Access.DesktopForm.Dirty()
	case "claude-settings":
		return claudeCfgDirty(s)
	}
	return false
}

// claudeCfgDirty 报告 Claude Code 设置页是否有未保存编辑（I1）。2026-11-07
// 拆页后按页判定（此前随接入页 Claude 页签判定）。
func claudeCfgDirty(s *State) bool {
	return s.Page == "claude-settings" && s.ClaudeCfg.Dirty
}

// Go 切换页面/子视图：有未保存编辑时先武装确认（NavArm），确认后 Navigate。
func (s *State) Go(page string, action func()) {
	if page == s.Page && action == nil {
		return
	}
	if s.DirtyCurrent() {
		s.NavArm = true
		s.NavTo = page
		s.NavAction = action
		return
	}
	s.Navigate(page, action)
}

// Navigate 执行切页：先丢弃当前页草稿，再改 Page/执行动作。
func (s *State) Navigate(page string, action func()) {
	pending := s.NavAction
	s.NavArm = false
	s.NavTo = ""
	s.NavAction = nil
	s.discardDrafts()
	if page != "" {
		s.Page = page
	}
	if action != nil {
		action()
	} else if pending != nil {
		pending()
	}
}

// discardDrafts 丢弃当前页未保存的表单草稿（切页/返回时调用）。
func (s *State) discardDrafts() {
	switch s.Page {
	case "logical", "logical-edit":
		s.Logical.Edit = LMForm{}
		s.Logical.Baseline = ""
	case "channels":
		s.Channels.View = ""
		s.Channels.KeyInput = ""
		s.Channels.SuggText = ""
		s.Channels.Form = ChannelForm{Protocol: "openai", Enabled: true}
		s.Channels.Baseline = ""
	case "access":
		s.Access.Form = AccessForm{}
		s.Access.DesktopForm = DesktopForm{}
		s.Access.ApplyID = ""
	case "claude-settings":
		// Claude 设置页草稿丢弃（保留测试注入的 Home；Loaded 归 false，下次
		// 进入重新加载）。
		s.ClaudeCfg = ClaudeCfgState{Home: s.ClaudeCfg.Home}
	}
}

// New 构造初始状态：默认落在逻辑模型页，渠道新建表单预置 openai/启用，
// 并带上网关已有的最近请求记录。Dispatch 默认同步（测试/无窗口场景），
// 组合根会替换为 Window.Update。
func New(svc Services) *State {
	// 空库默认落渠道页（依赖顺序：渠道 → 逻辑模型 → 接入）；已有渠道时
	// 仍默认逻辑模型页。
	page := "logical"
	if len(svc.Channels.List()) == 0 {
		page = "channels"
	}
	st := &State{Svc: svc, Recent: svc.Gateway.Recent(), Page: page}
	st.Dispatch = func(fn func()) { fn() }
	st.Channels.Form = ChannelForm{Protocol: "openai", Enabled: true}
	return st
}
