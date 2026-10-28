// Package state 是桌面 UI 的共享状态：组合根（internal/desktop）与各页
// 子包（pages/*）共同消费。叶子数据包——只依赖 bridge/telemetry，
// 不 import uikit 或任何页面，避免环依赖。
package state

import (
	"strings"

	"AgentSwitch/internal/bridge"
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

	Page           string       // 当前页；"logical" 默认（经 normalizedPage 归一）
	CloseNotice    bool         // 首关驻留模态 open 位
	StopArm        bool         // 停止网关确认弹窗 open 位
	CloseConfirmed bool         // 用户已确认驻留；之后的关窗直接隐藏
	Quitting       bool         // 退出流程中：放行窗口关闭（OnBeforeQuit 置位）
	HideWindow     func()       // 隐藏当前主窗（openWindow 注入；测试可为 nil）
	Schedule       func(func()) // 把窗口操作推迟出 GTK 事件回调（默认 afterEvent）

	Channels ChannelState
	Logical  LogicalState
	Access   AccessState
	Settings SettingsState
}

// AccessState 是接入页状态：当前 Agent 页签、全局配置编辑行、列表/编辑器
// 提示位与两类表单（通用接入文档表单 / Claude Desktop 角色映射表单）。
type AccessState struct {
	App         string // "claude" | "codex" | "claude_desktop" | "guide"
	Glob        [][2]string
	GlobMsg     string
	Msg         string
	GuideMsg    string
	Form        AccessForm
	DesktopForm DesktopForm
	DeleteArm   bool   // 删除接入配置 AlertDialog open 位
	DeleteID    string // 删除武装对象——确认仅对匹配 id 生效
	SwitchArm   bool   // 页签切换确认 AlertDialog open 位（有未保存编辑时）
	SwitchTo    string // 待切换的 Agent 页签
	RestoreArm  bool   // 还原最近备份确认 AlertDialog open 位
}

// AccessForm 是接入配置新建/编辑表单状态（Open 非空即编辑）。DocJSON 是唯一
// 共享存储：编辑器显示 PreviewDoc 合成结果（含真实网关令牌），保存原文送
// bridge 归一化。角色行/Codex 模型列表均由 DocJSON 双向同步。
type AccessForm struct {
	Open       bool
	EditID     string // 非空 = 编辑该配置
	Name       string
	ModelLabel string // 选中的逻辑模型名（保存时映射回 ID）
	DocJSON    string
	Msg        string

	// 角色映射行状态（Claude 5 角色同序；由 DocJSON 双向同步）
	RoleModels []string
	RoleNames  []string
	RoleOneM   []bool
	MainOneM   bool // 主模型 1M 勾选（ANTHROPIC_MODEL 半锁定后缀）

	// Codex 模型列表与高级设置（由 DocJSON models/codex 双向同步，首项即默认模型）
	CodexModels     []string
	SubagentModel   string
	SubagentEffort  string
	AutoReviewModel string
	CodexPreview    string
	ClaudePreview   string // Claude Code：预览将写入的 settings.json（live 合并结果）

	// Baseline 是打开编辑器时的内容签名，用于页签切换脏检查。
	Baseline string
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
	View      string
	DetailID  string
	Form      ChannelForm
	KeyInput  string // 详情 Key 更新框（留空=不改）
	SuggText  string // 详情建议模型多行文本（逗号/换行分隔）
	TestMsg   string // 连通测试/操作结果信息位
	DeleteArm bool   // 删除渠道 AlertDialog open 位
	DeleteID  string // 删除武装对象——弹窗仅对匹配 id 触发

	// 拉取模型的「追加/替换」确认：已有建议文本时先询问，空文本直接填入。
	FetchArm    bool
	FetchIDs    []string
	FetchTarget string // "detail" | "new"
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
	Edit        LMForm   // 新建/编辑表单状态（名称/描述/目标链/信息位）
	DeleteArm   bool     // 删除 AlertDialog open 位
	DeleteID    string   // 删除武装对象——确认仅对匹配 id 生效
	DeleteRefs  []string // 引用该模型的接入配置摘要（武装时计算，弹窗展示）
}

// LMForm 是逻辑模型新建/编辑表单状态：Targets 即目标链行（Priority 落库
// 前按下标重排）；元数据以原始字符串保存（在 meta 校验时解析）；Msg 是
// 表单信息位（校验/保存错误）。
type LMForm struct {
	Name                  string
	Description           string
	DisplayName           string
	ContextWindow         string
	ReasoningLevels       string
	DefaultReasoningLevel string
	Targets               []bridge.TargetSpec
	Msg                   string
}

// SettingsState 是设置页状态：输入框为空时按当前生效值回显（不与输入中的
// 用户打架），保存成功后清空、下一帧回显新生效值。
type SettingsState struct {
	PortInput     string // 端口输入（为空时回显 Status.Port）
	TimeoutInput  string // 超时输入（为空时回显 Status.TimeoutSeconds）
	Msg           string // 保存结果提示
	PassthroughOn bool   // 透传未登记模型开关（预留：SaveSettings 不消费）
}

// New 构造初始状态：默认落在逻辑模型页，渠道新建表单预置 openai/启用，
// 并带上网关已有的最近请求记录。
func New(svc Services) *State {
	st := &State{Svc: svc, Recent: svc.Gateway.Recent(), Page: "logical"}
	st.Channels.Form = ChannelForm{Protocol: "openai", Enabled: true}
	return st
}
