package claudecfg

// KeyKind 是键的 JSON 类型。
type KeyKind string

const (
	KindString      KeyKind = "string"
	KindBool        KeyKind = "bool"
	KindInt         KeyKind = "int"
	KindStringArray KeyKind = "stringArray"
	KindObject      KeyKind = "object"
	KindObjectBool  KeyKind = "objectBool"
	KindAny         KeyKind = "any"
)

// ScopeMask 是允许出现的 settings 层掩码。
type ScopeMask uint8

const (
	AllowUser ScopeMask = 1 << iota
	AllowProject
	AllowLocal
	AllowManaged
)

const AllScopes = AllowUser | AllowProject | AllowLocal | AllowManaged

// Has 报告掩码是否包含指定层。
func (m ScopeMask) Has(kind ScopeKind) bool {
	switch kind {
	case ScopeUser:
		return m&AllowUser != 0
	case ScopeProject:
		return m&AllowProject != 0
	case ScopeLocal:
		return m&AllowLocal != 0
	case ScopeManaged:
		return m&AllowManaged != 0
	}
	return false
}

// MergeKind 是跨层合并策略。
type MergeKind string

const (
	MergeOverride MergeKind = "override"
	MergeUnion    MergeKind = "union"
	MergeEnv      MergeKind = "env"
	MergeHooks    MergeKind = "hooks"
	MergePlugins  MergeKind = "plugins"
	MergeSkill    MergeKind = "skill"
	MergeReplace  MergeKind = "replace"
	MergeOr       MergeKind = "or"
	MergeLeaf     MergeKind = "leaf"
)

// ApplyKind 是保存后的生效方式。
type ApplyKind string

const (
	ApplyHot       ApplyKind = "hot"
	ApplyRestart   ApplyKind = "restart"
	ApplyNextStart ApplyKind = "next-start"
	ApplyUnknown   ApplyKind = "unknown"
)

// FormGroup 是表单分组。
type FormGroup string

const (
	GroupModel       FormGroup = "model"
	GroupEnv         FormGroup = "env"
	GroupPermissions FormGroup = "permissions"
	GroupStatusLine  FormGroup = "statusline"
	GroupMCP         FormGroup = "mcp"
	GroupSkills      FormGroup = "skills"
	GroupMemory      FormGroup = "memory"
	GroupNone        FormGroup = "none"
)

// KeySpec 是键注册表的一条记录。
type KeySpec struct {
	Path      string
	Kind      KeyKind
	Scopes    ScopeMask
	Merge     MergeKind
	Apply     ApplyKind
	Group     FormGroup
	Sensitive bool
	Enum      []string
	Default   any
}

// AccessManagedEnv 是接入「应用」写入 Claude live settings 的 13 个 env 键。
// 顺序与桌面接入编辑器一致；设置区按此集合强只读。
var AccessManagedEnv = []string{
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_MODEL",
	"ANTHROPIC_SMALL_FAST_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL",
	"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME",
	"ANTHROPIC_DEFAULT_OPUS_MODEL",
	"ANTHROPIC_DEFAULT_OPUS_MODEL_NAME",
	"ANTHROPIC_DEFAULT_FABLE_MODEL",
	"ANTHROPIC_DEFAULT_FABLE_MODEL_NAME",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL",
	"ANTHROPIC_DEFAULT_HAIKU_MODEL_NAME",
	"CLAUDE_CODE_SUBAGENT_MODEL",
}

// NoProjectShared 是不能写入项目共享（.claude/settings.json）的键。
var NoProjectShared = []string{
	"skipDangerousModePermissionPrompt",
	"syncClaudeAiPlugins",
	"syncClaudeAiSkills",
	"useAutoModeDuringPlan",
}

// UserOnly 是仅用户级/托管/命令行可用的键（P0 已知子集；未知键不阻断）。
var UserOnly = []string{
	"autoMode",
	"modelPicker",
	"worktree.location",
	"sandbox.allowAppleEvents",
}

// ManagedOnly 是仅托管策略可写入的键。
var ManagedOnly = []string{
	"managedMcpServers",
	"isolation",
	"deniedModels",
	"availableModelsMatch",
}

var keySpecs = map[string]KeySpec{
	"model": {
		Path: "model", Kind: KindString, Scopes: AllScopes, Merge: MergeOverride,
		Apply: ApplyRestart, Group: GroupModel,
	},
	"fallbackModel": {
		Path: "fallbackModel", Kind: KindStringArray, Scopes: AllScopes, Merge: MergeReplace,
		Apply: ApplyRestart, Group: GroupModel,
	},
	"effortLevel": {
		Path: "effortLevel", Kind: KindString, Scopes: AllScopes, Merge: MergeOverride,
		Apply: ApplyRestart, Group: GroupModel, Enum: []string{"low", "medium", "high", "xhigh"},
	},
	"alwaysThinkingEnabled": {
		Path: "alwaysThinkingEnabled", Kind: KindBool, Scopes: AllScopes, Merge: MergeOverride,
		Apply: ApplyRestart, Group: GroupModel,
	},
	"env": {
		Path: "env", Kind: KindObject, Scopes: AllScopes, Merge: MergeEnv,
		Apply: ApplyHot, Group: GroupEnv,
	},
	"permissions.defaultMode": {
		Path: "permissions.defaultMode", Kind: KindString, Scopes: AllScopes, Merge: MergeOverride,
		Apply: ApplyHot, Group: GroupPermissions,
		Enum: []string{"default", "acceptEdits", "plan", "auto", "bypassPermissions"},
	},
	"permissions.allow": {
		Path: "permissions.allow", Kind: KindStringArray, Scopes: AllScopes, Merge: MergeUnion,
		Apply: ApplyHot, Group: GroupPermissions,
	},
	"permissions.ask": {
		Path: "permissions.ask", Kind: KindStringArray, Scopes: AllScopes, Merge: MergeUnion,
		Apply: ApplyHot, Group: GroupPermissions,
	},
	"permissions.deny": {
		Path: "permissions.deny", Kind: KindStringArray, Scopes: AllScopes, Merge: MergeUnion,
		Apply: ApplyHot, Group: GroupPermissions,
	},
	"permissions.additionalDirectories": {
		Path: "permissions.additionalDirectories", Kind: KindStringArray, Scopes: AllScopes,
		Merge: MergeUnion, Apply: ApplyHot, Group: GroupPermissions,
	},
	"statusLine": {
		Path: "statusLine", Kind: KindObject, Scopes: AllScopes, Merge: MergeOverride,
		Apply: ApplyRestart, Group: GroupStatusLine,
	},
	"enabledPlugins": {
		Path: "enabledPlugins", Kind: KindObjectBool, Scopes: AllScopes, Merge: MergePlugins,
		Apply: ApplyHot, Group: GroupMCP,
	},
	"enableAllProjectMcpServers": {
		Path: "enableAllProjectMcpServers", Kind: KindBool, Scopes: AllScopes, Merge: MergeOverride,
		Apply: ApplyHot, Group: GroupMCP,
	},
	"enabledMcpjsonServers": {
		Path: "enabledMcpjsonServers", Kind: KindStringArray, Scopes: AllScopes, Merge: MergeUnion,
		Apply: ApplyHot, Group: GroupMCP,
	},
	"disabledMcpjsonServers": {
		Path: "disabledMcpjsonServers", Kind: KindStringArray, Scopes: AllScopes, Merge: MergeUnion,
		Apply: ApplyHot, Group: GroupMCP,
	},
	"syncClaudeAiPlugins": {
		Path: "syncClaudeAiPlugins", Kind: KindBool, Scopes: AllowUser | AllowLocal | AllowManaged,
		Merge: MergeOverride, Apply: ApplyHot, Group: GroupMCP,
	},
	"syncClaudeAiSkills": {
		Path: "syncClaudeAiSkills", Kind: KindBool, Scopes: AllowUser | AllowLocal | AllowManaged,
		Merge: MergeOverride, Apply: ApplyHot, Group: GroupMCP,
	},
	"autoCompactEnabled": {
		Path: "autoCompactEnabled", Kind: KindBool, Scopes: AllScopes, Merge: MergeOverride,
		Apply: ApplyHot, Group: GroupMemory,
	},
	"autoMemoryEnabled": {
		Path: "autoMemoryEnabled", Kind: KindBool, Scopes: AllScopes, Merge: MergeOverride,
		Apply: ApplyHot, Group: GroupMemory,
	},
	"cleanupPeriodDays": {
		Path: "cleanupPeriodDays", Kind: KindInt, Scopes: AllowUser | AllowManaged,
		Merge: MergeOverride, Apply: ApplyNextStart, Group: GroupMemory, Default: 30,
	},
	"autoMode": {
		Path: "autoMode", Kind: KindObject, Scopes: AllowUser | AllowManaged,
		Merge: MergeOverride, Apply: ApplyNextStart, Group: GroupNone,
	},
	"modelPicker": {
		Path: "modelPicker", Kind: KindObject, Scopes: AllowUser | AllowManaged,
		Merge: MergeReplace, Apply: ApplyNextStart, Group: GroupNone,
	},
	"availableModels": {
		Path: "availableModels", Kind: KindStringArray, Scopes: AllScopes,
		Merge: MergeReplace, Apply: ApplyNextStart, Group: GroupNone,
	},
	"modelSettings": {
		Path: "modelSettings", Kind: KindObject, Scopes: AllScopes,
		Merge: MergeReplace, Apply: ApplyNextStart, Group: GroupNone,
	},
	"maxEffortLevel": {
		Path: "maxEffortLevel", Kind: KindString, Scopes: AllScopes,
		Merge: MergeOverride, Apply: ApplyNextStart, Group: GroupNone,
	},
	"autoCompactWindow": {
		Path: "autoCompactWindow", Kind: KindAny, Scopes: AllScopes,
		Merge: MergeOverride, Apply: ApplyHot, Group: GroupNone,
	},
	"autoMemoryDirectory": {
		Path: "autoMemoryDirectory", Kind: KindString, Scopes: AllScopes,
		Merge: MergeOverride, Apply: ApplyHot, Group: GroupNone,
	},
	"hooks": {
		Path: "hooks", Kind: KindObject, Scopes: AllScopes, Merge: MergeHooks,
		Apply: ApplyHot, Group: GroupNone,
	},
	"sandbox": {
		Path: "sandbox", Kind: KindObject, Scopes: AllScopes, Merge: MergeOverride,
		Apply: ApplyNextStart, Group: GroupNone,
	},
	"worktree.location": {
		Path: "worktree.location", Kind: KindString, Scopes: AllowUser | AllowManaged,
		Merge: MergeOverride, Apply: ApplyNextStart, Group: GroupNone,
	},
	"skipDangerousModePermissionPrompt": {
		Path: "skipDangerousModePermissionPrompt", Kind: KindBool,
		Scopes: AllowUser | AllowLocal | AllowManaged, Merge: MergeOr,
		Apply: ApplyNextStart, Group: GroupPermissions, Default: true,
	},
	"useAutoModeDuringPlan": {
		Path: "useAutoModeDuringPlan", Kind: KindBool,
		Scopes: AllowUser | AllowLocal | AllowManaged, Merge: MergeOverride,
		Apply: ApplyHot, Group: GroupNone,
	},
	"skillOverrides": {
		Path: "skillOverrides", Kind: KindObject, Scopes: AllScopes, Merge: MergeSkill,
		Apply: ApplyNextStart, Group: GroupSkills,
	},
	"disableBundledSkills": {
		Path: "disableBundledSkills", Kind: KindBool, Scopes: AllScopes,
		Merge: MergeOverride, Apply: ApplyNextStart, Group: GroupSkills,
	},
}

// Lookup 返回键注册表记录。
func Lookup(path string) (KeySpec, bool) {
	spec, ok := keySpecs[path]
	return spec, ok
}
