# Agent 接入与 Claude Code 配置中心设计（2026-11-07，合并版）

> 状态：已批准（2026-11-07 用户确认合并版）
> 关联需求：`docs/requirements/2026-11-07-claude-code-config-page.md`
> 关联原型：`docs/prototypes/claude-code-config-page.html`
> 关联设计系统：`docs/audit/design-system.md`
> 基线：Claude Code 2.1.x（2026-11-07 调研核对）

## 1. 意图与范围

在 AgentSwitch 桌面端**「Agent 接入」页的 Claude Code 页签**内，把网关接入与 Claude Code 自身的分层 settings 放在同一上下文：上方接入摘要卡 + 右侧接入抽屉，下方 Claude Code 设置（用户级 / 项目共享 / 项目本地，企业托管只读）。核心价值是「看清最终生效值 + 安全写入」，而不是做全量设置百科。

### 1.1 已确认决策

| 决策 | 结论 |
|---|---|
| 页面形态 | 唯一入口「Agent 接入」；Claude Code 页签内「接入摘要卡 + 接入抽屉 + Claude Code 设置」；不新增独立「Claude 配置」导航 |
| 层级 | 用户级 `~/.claude/settings.json`；项目共享 `.claude/settings.json`；项目本地 `.claude/settings.local.json`；Managed 只读 |
| 覆盖范围 | 7 组常用结构化表单（含 Skills）+ 任意键 JSON 编辑器兜底 |
| 项目选择 | `~/.claude.json` 的最近项目列表 + 系统目录选择器 |
| 合并预览 | 展示最终值、来源文件、合并方式；数组并集、权限 deny→ask→allow、Skills 逐键、危险模式确认 OR |
| 写入 | 备份 → 冲突检测 → 原子写 → 只 patch 目标键 → 保留未知键 |
| 受管键 | 接入实际投影的键在设置区只读：当前 13 个 env 键强受管；接入文档顶层键与全局 env 动态纳入弱/强警告 |
| 危险模式确认 | `skipDangerousModePermissionPrompt` 默认 `true`；项目共享不可写；任一层 true 即 true |
| Skills | 新增 Skills 卡片，编辑 `skillOverrides` 四态；插件来源锁定为「由插件管理」 |

### 1.2 非目标

不做 Managed 写入、`--settings` 临时层、`~/.claude.json` Global config 编辑、`.mcp.json` 服务定义编辑、插件/Skill 安装卸载、编辑 `SKILL.md`、跨机器同步。

### 1.3 反目标

不静默合并写回、不隐藏 scope 违规、不把密钥写进项目共享、不假装热生效、不自动改 `.gitignore`、不覆盖接入投影键、不写出插件/作者锁定的 Skill override。

## 2. 模块划分

新增三层，依赖方向单向：`claudecfg` ← `bridge` ← `desktop`。

```
internal/claudecfg/          纯逻辑，不依赖 UI/bridge
├─ paths.go                  层路径解析（用户级/项目共享/项目本地/Managed）
├─ keys.go                   键注册表、scope 清单、Skills 枚举
├─ doc.go                    JSON 文档模型、patch、diff、严格解析
├─ merge.go                  跨层合并引擎
├─ scope.go                  scope 校验（新增违规 vs 历史遗留）
├─ sensitive.go              敏感键检测与遮蔽
└─ skills.go                 Skill 清单模型、有效状态与锁定计算（纯函数）

internal/bridge/claude_config.go   给 UI 的桥接 API（读写、合并、备份、Skills）
internal/desktop/pages/access/claude/claudecfg/  Claude 设置区子视图
    （page/form/json/merged/projects/skills），由 claude 页签组合根挂载

internal/desktop/state/state.go    新增 `ClaudeCfg` 状态（接入状态仍为 `Access`）
internal/desktop/pages/access/claude/claude.go  在接入摘要卡之后挂载设置区
```

- 不新增侧栏导航项，不改 `shell.go` 的 pageView case；设置区是 Claude 页签的下半部分。
- `claudecfg` 不 import `internal/bridge`、`internal/desktop`、MyGo。`bridge` 通过参数接收受管键集合与 Skill 清单，不反向依赖接入实现的内部结构。
- `pages/access/claude/claudecfg` 只能 import `uikit`、`state`、`accesskit`、`bridge` 等叶子/桥接包，不得 import `pages/access` 宿主或其它 Agent 子包。

## 3. 键注册表

静态 Go 表，不用外部 JSON（避免运行时依赖与版本漂移）。每条：

```go
type KeySpec struct {
    Path       string      // 点分路径，如 "permissions.allow"
    Kind       KeyKind     // string/bool/int/stringArray/object/objectBool/any
    Scopes     ScopeMask   // 允许出现的层
    Merge      MergeKind   // override/union/env/hooks/plugins/replace/skill/leaf
    Apply      ApplyKind   // hot/restart/next-start/unknown
    Group      FormGroup   // model/env/permissions/statusline/mcp/skills/memory/none
    Sensitive  bool
    Enum       []string    // 枚举值，空表示不限
}
```

P0 注册表以需求附录 A 为起点，并补入 scope 清单：

- `userOnly`: 30 个「用户级/命令行/托管专属」键（`autoMode`、`modelPicker`、`sandbox.allowAppleEvents` 等）。
- `noProjectShared`: 4 个键（`skipDangerousModePermissionPrompt`、`syncClaudeAiPlugins`、`syncClaudeAiSkills`、`useAutoModeDuringPlan`）。
- `worktree.location`: 仅用户级。
- `managedOnly`: 需求附录 A 中的托管键。
- `skillOverrides`: 允许所有 settings 层；按 Skill 名逐键合并；插件/作者/托管可按 Skill 锁定，锁定发生在 `claudecfg.SkillState` 而不是 scope 校验。
- 未登记键：允许写，标 `unknown`，scope 不阻断，UI 提示「Claude Code 可能忽略」。

键表必须可回答四个问题：路径、类型/枚举、允许层级、合并与生效方式。UI 的 scope 提示、表单校验、合并预览都从这张表读取，不允许各处硬编码。

### 3.1 受管键集合是动态的

接入页的 `writeClaudeLive` 会投影**当前接入文档的全部顶层键**与**合成 env 的全部键**（基础键 + `access.global.claude` + 文档 env）到 `~/.claude/settings.json`。因此配置页不能只锁 13 个 env 键：接入文档顶层键也会被下一次「应用」覆盖，全局 env（如 `HTTPS_PROXY`）同样会被覆盖。

```go
type ManagedSet struct {
    StrongEnv      []string          // 13 个接入投影 env 键：只读 + 不可写
    StrongTopLevel []string          // 任一 Claude 接入文档出现的顶层键：只读
    WeakEnv        []string          // 其余合成 env 键：可编辑但警告
    Source         map[string]string // 键路径 → 来源说明（接入配置名/全局配置）
}

func ComputeManagedSet(configs []map[string]any, composedEnv map[string]string) ManagedSet
```

- `StrongEnv` 固定 13 个：核心三键 + `ANTHROPIC_SMALL_FAST_MODEL` + 4 组 `ANTHROPIC_DEFAULT_*_MODEL(_NAME)` + `CLAUDE_CODE_SUBAGENT_MODEL`。
- `StrongTopLevel` = 全部 Claude 接入配置文档的顶层键（排除 `env`）的并集。接入文档的 `permissions`、`statusLine` 等若存在，设置区对应键只读，行内显示「接入投影」+「打开接入」。
- `WeakEnv` = 合成 env 中不属于 `StrongEnv` 的键（例如全局配置的 `HTTPS_PROXY`）。可编辑，但保存确认框列出「接入应用会覆盖」。
- 接入配置读取失败时，`StrongEnv` 仍固定 13 键，`StrongTopLevel`/`WeakEnv` 退化为只按 live 文件无法区分，置空。
- bridge 负责计算并传给 `claudecfg` 的 scope 校验与合并层；`claudecfg` 不反向依赖接入实现。
- UI：强受管只读 + 锁图标；弱受管可编辑 + 黄色徽章「接入应用可能覆盖」，保存确认框中再次列出。

## 4. 路径解析

```go
type ScopeKind string // user / project / local / managed

type Resolved struct {
    Kind      ScopeKind
    File      string // 真实写入路径（符号链接解析后）
    Display   string // 展示路径（保留 ~）
    Project   string
    Exists    bool
    Note      string // 路径例外说明
}

func Resolve(kind ScopeKind, project string, env Env) (Resolved, error)
```

- `Env` 注入 `HOME`、`CLAUDE_CONFIG_DIR`、`GOOS`，便于测试与 Windows 映射。
- **`CLAUDE_CONFIG_DIR` P0 不支持**：用户级固定 `~/.claude/settings.json`，与接入页 `writeClaudeLive` 保持一致。检测到该环境变量时 UI 显示黄色提示「本版本不支持自定义 Claude 配置目录，页面仍读写 ~/.claude」。项目列表、会话记录、插件清单同样固定 `~/.claude*`，不随该变量迁移。完整支持留到后续版本，届时接入页、项目列表、插件路径必须同步改，不能只改配置页。
- 项目共享：`<project>/.claude/settings.json`。
- 项目本地：按 Claude Code 2.1.211+ 基线，默认落在 Git 仓库根的 `.claude/settings.local.json`。解析器同时返回 `Candidates`：
  - `Primary`：向上查找 `.git`（目录或文件，支持 worktree）得到仓库根；找不到、Windows、仓库根等于家目录、owner 不匹配时回退到项目目录。
  - `Candidates`：`Primary` 与 `<project>/.claude/settings.local.json`（若不同）。
  - UI 必须显示 `Primary` 的实际路径；当候选路径不同且文件存在时，额外提示「若 Claude Code 从子目录启动或使用旧版本，可能读取另一份」。
  - 写入默认 `Primary`；用户可在候选间显式切换（切换后该次编辑绑定所选路径）。
- Managed：macOS `/Library/Application Support/ClaudeCode/managed-settings.json`；Linux `/etc/claude-code/managed-settings.json`；Windows `C:\Program Files\ClaudeCode\managed-settings.json`。另读 `managed-settings.d/*.json`（按文件名排序），读不到记为「不可见」。
- 符号链接：`RealPath` 用于写入与展示；不替换链接。
- 不自动创建目录；保存时由 bridge 在用户确认后创建 `.claude`。

## 5. 文档模型与写入

### 5.1 文档模型

```go
type Doc struct {
    Raw      map[string]any // json.Number 保精度
    Original map[string]any // 打开时基线，用于 diff 与冲突检测
    Hash     string         // 打开时内容 sha256
    MTime    time.Time
}
```

- 严格 JSON：拒绝注释、尾逗号；错误带行、列、原因。
- 未知键原样保留。
- 超过 1 MiB 拒绝页内编辑。
- 表单与 JSON 共用同一个 `Doc`：表单修改 `Raw`，JSON 视图序列化；JSON 合法解析后更新 `Raw` 与 dirty。

### 5.2 patch 与序列化

- 表单保存：计算 `ChangedLeaves(Original, Raw)`，只把这些叶子路径写入磁盘文档；删除键 = 删除该路径。
- JSON 保存：写入完整 `Raw`。
- 序列化：2 空格缩进 + 末尾换行（与现有 access 写入一致）。
- 受管键保护：保存前把 `StrongEnv`（13 个）与 `StrongTopLevel` 强制恢复为磁盘原值；若用户改过，返回 `ErrManagedKey`，不静默覆盖。`WeakEnv` 不强制恢复，但变化会进入保存确认警告。

### 5.3 备份与原子写

- 备份按「源文件路径」分桶，避免不同项目的同名 `settings.json` 互相当成对方的备份：

```
~/.agentswitch/backups/claude-settings/<sha256(realpath)[:16]>/
├─ <timestamp>-<filename>        # 备份文件本体，0600
└─ meta.json                     # {targetPath, kind, project, sub, createdAt}
```

- 每个 `targetPath` 桶保留最近 10 份；`meta.json` 用于恢复时确定唯一目标路径，**不靠文件名猜目标**。
- `RestoreClaudeConfigBackup` 的 id 为 `<pathHash>/<backupFileName>`；恢复前必须：
  1. 持 `claudeSettingsMu`；
  2. 重新读取当前目标文件并备份（除非当前文件不存在）；
  3. 原子写回备份内容。
- 备份失败 → 取消写入。
- 原子写：同目录临时文件 → chmod（已有文件保留原 mode，新文件 0600）→ fsync → rename。
- 复用 `internal/bridge/access_live.go` 的 `atomicWriteFile0600`；新增 `backupFileTo(bucketDir, path, meta)` 供配置页使用，不改变接入页现有目录结构（接入页仍写 `backups/<appType>/`）。
- 写入前若发现 group/world 可读的老文件，不强制改权限，只在 UI 提示。

### 5.4 并发与冲突

新增包级 `var claudeSettingsMu sync.Mutex`，接入页 `writeClaudeLive` 与配置页保存共用：

```go
claudeSettingsMu.Lock()
defer claudeSettingsMu.Unlock()
// 重新读取磁盘 → 比对 Hash/MTime → 备份 → patch → 原子写
```

- 磁盘 Hash 与打开时不同 → 返回 `ConflictError{Path, DiskHash}`，UI 弹冲突对话框。
- 默认安全动作是「取消」；「覆盖写入」为危险软底。
- 写入期间 UI 显示 Spinner，保存按钮禁用。

## 6. 合并引擎

### 6.1 输入输出

```go
type LayerDoc struct {
    Kind ScopeKind
    File string
    Doc  map[string]any
}

func Merge(layers []LayerDoc, specs map[string]KeySpec) MergedView

type MergedEntry struct {
    Path          string
    Value         any
    Kind          MergeKind
    Source        ScopeKind
    Effective     bool
    Contributions []Contribution // 低层 → 高层
}

type Contribution struct {
    Layer    ScopeKind
    File     string
    Value    any
    Shadowed bool
}
```

层顺序固定低 → 高：user → project → local → managed。`--settings` 无法可靠检测，不在输入中，预览顶部标注「不参与」。

### 6.1.1 Managed 层内部合并

`managed-settings.json` 与 `managed-settings.d/*.json` 不是简单取一个文件。P0 按 Claude Code 规则先在 managed 层内合并成一个 `LayerDoc`，再做跨层合并：

1. 先 `managed-settings.json`，再按文件名字母序读 drop-in。
2. 标量后写覆盖；数组并集去重；对象逐键合并。
3. 每个键保留「来自哪个 managed 文件」的来源，供只读视图展开。
4. `fallbackModel`、`modelPicker`、`availableModels`、同名 `extraKnownMarketplaces`/`managedMcpServers` 整值替换。
5. `managedSourcesBehavior`、MDM、远程策略 P0 不参与计算，UI 标注「不可见/未完整支持」。

### 6.2 合并策略

| MergeKind | 行为 |
|---|---|
| `override` | 最高层标量/对象整值胜出 |
| `union` | 字符串数组跨层并集、去重、保留每项首次来源 |
| `env` | `env` 对象逐变量最高层胜出 |
| `hooks` | 跨层累加，按事件分组展示 |
| `plugins` | `enabledPlugins` 逐插件最高层胜出 |
| `skill` | `skillOverrides` 按 Skill 名逐键：policy/flag 锁定优先，否则 local > project > user > `on`；插件/作者锁由 `SkillState` 单独计算 |
| `replace` | `fallbackModel`/`modelPicker`/`availableModels`/`modelSettings` 取最高层整值，不并集 |
| `leaf` | 未登记对象逐叶子键最高层胜出（保守默认） |

特殊有效值：

- `skipDangerousModePermissionPrompt`：**任一层 true 即 true（OR）**，不使用最高层覆盖；`false` 不能覆盖其他层 `true`。
- `skillOverrides`：插件来源 Skill 的覆盖值不生效；作者 `disable-model-invocation` 时最高只能到 `user-invocable-only`；`__proto__` 不可写入。
- 项目层的 `permissions.defaultMode` 为 `auto` / `bypassPermissions` → `Effective=false`，值回落至更低层。
- 项目共享的 `autoMemoryDirectory` → `Effective=false`，提示被忽略。
- Managed 的「更严格值例外」P0 不完整实现，只在托管只读视图展示原始策略并标注「部分限制可能无法在预览中体现」。

### 6.3 权限视图

单独输出：

```go
type PermissionView struct {
    Deny, Ask, Allow []RuleContribution // 规则 + 来源 + 是否被更早匹配遮蔽（P1 才做匹配模拟）
    Order            []string           // deny, ask, allow
    DefaultMode      MergedEntry
    DefaultModeNote  string             // 项目级 auto/bypass 不生效说明
}
```

P0 只展示三组规则、来源与求值顺序，不做工具调用匹配模拟。

## 7. Scope 校验

```go
type Violation struct {
    Path    string
    Reason  string // managed-only / user-only / not-in-project-shared / ineffective-at-project
    Present bool   // 文件中已存在
    Changed bool   // 本次修改引入
}
```

- `Changed=true` 的违规阻断保存。
- 仅 `Present=true, Changed=false` 的历史违规只警告并原样保留。
- 受管键变化单独返回 `ErrManagedKey`，优先于 scope 违规。
- 未登记键不违规，标 unknown。
- `skillOverrides` 的 scope 合法（所有层可写）；非法枚举值、`__proto__`、空 Skill 名视为错误。插件/作者/托管锁定不是 scope 违规，由 `SkillState` 返回锁定原因，UI 禁用控件。
- 项目共享写入 `skillOverrides` 允许，但保存确认框增加「会影响所有协作者并可能被提交」。

## 8. 敏感键与密钥安全

- 键名匹配 `TOKEN|KEY|SECRET|PASSWORD|AUTH`（大小写不敏感）或注册表 `Sensitive=true` → 表单/预览默认遮蔽。
- JSON 视图显示明文，顶部强警告；日志与遥测永不记录值。
- 项目共享文件保存前扫描值：疑似密钥（长度 ≥20 且高熵/匹配常见前缀）→ 警告并要求确认，建议改存项目本地。
- 复制敏感值走剪贴板 + Toast，不写日志。

## 9. Bridge API

```go
// internal/bridge/claude_config.go
type ClaudeConfigs struct{ A *app.App }

func (s ClaudeConfigs) ClaudeConfigOverview() (ClaudeConfigOverview, error)
func (s ClaudeConfigs) ReadClaudeConfigLayer(req ReadLayerRequest) (LayerView, error)
func (s ClaudeConfigs) SaveClaudeConfigLayer(req SaveClaudeConfigRequest) (SaveResult, error)
func (s ClaudeConfigs) MergeClaudeConfig(project string) (MergedView, error)
func (s ClaudeConfigs) ListClaudeProjects() ([]ClaudeProject, error)
func (s ClaudeConfigs) ListClaudeConfigBackups(req ReadLayerRequest) ([]BackupInfo, error)
func (s ClaudeConfigs) RestoreClaudeConfigBackup(id string) (SaveResult, error)
func (s ClaudeConfigs) ListClaudeSkills(project string) ([]SkillView, error)
```

`ReadLayerRequest`：`Kind`、`Project`、`Sub`、可选 `LocalPath`（在候选路径中显式选择）。
`LayerView` 包含：路径、候选路径、存在性、文档、原始 JSON、解析错误、scope 违规、`ManagedSet`、生效范围。
`SaveClaudeConfigRequest`：`Kind`、`Project`、`Sub`、`LocalPath`、`Document` 或 `Patch`、`ExpectedHash`、`Create`。
`ClaudeProject`：路径、名称、共享/本地文件存在性与路径、最近使用依据、目录是否存在。
`SkillView`：`Name`、`Source`（built-in/user/project/plugin/unknown）、`Description`、`PluginID`、`Effective`、`LockedBy`（author/plugin/policy/flag/reserved-name）、`TokenEstimate`、`CurrentOverride`。Skill 清单由 bridge 扫描用户/项目 skills 目录、插件清单与内置清单；`claudecfg` 只做有效状态与锁定计算。读取失败返回错误，UI 保留已有 `skillOverrides` 并提示，不返回空列表冒充无 Skill。

`ListClaudeProjects` 读 `~/.claude.json` 的 `projects`：
- 真实结构与需求描述不同：`projects` 是**以项目绝对路径为键的对象 map**，不是数组。每个 value 可能含 `lastSessionId`、`mcpServers`、trust 状态等。
- P0 以 map 为契约；同时兼容 value 为数组的旧形态（若遇到）。
- 排序依据：`~/.claude/projects/<encoded>/<lastSessionId>.jsonl` mtime → 项目目录 mtime → 名称。读不到就回退到名称，不报错。
- `<encoded>` 编码规则无法完全确定时跳过会话 mtime，不影响项目列表。

目录选择在 desktop 层用 MyGo 的文件对话框，选择结果作为 `project` 传入 bridge。若检测到 `CLAUDE_CONFIG_DIR`，`ClaudeConfigOverview` 返回警告字段，UI 展示「本版本不支持自定义配置目录」。

## 10. 桌面页面

### 10.1 页面结构

- 不新增导航项；侧栏唯一入口「Agent 接入」，Claude Code 页签内：
  1. 接入摘要卡（常驻）；
  2. Claude Code 设置区（本设计的主体）；
  3. 右侧接入抽屉由摘要卡打开。
- 设置区顶部：区块标题「Claude Code 设置」+「受管 13 键」徽章 + 保存按钮（主）。
- 当前文件条：路径、状态徽章、保存时间。
- 层级切换：用户级 / 项目级 / 企业托管。
- 项目级：项目选择器 + 共享/本地子页签 + 实际路径 + 路径说明。
- 视图切换：表单 / JSON / 合并预览（托管层不显示）。
- 内容卡片流。
- 保存结果条。
- 接入抽屉打开时设置区仍可见；设置区 dirty 时点「应用接入」先弹「先处理未保存的 Claude Code 设置」。

### 10.2 状态

`state.State` 新增：

```go
ClaudeCfg struct {
    Layer       string // user/project/managed
    Sub         string // shared/local
    Project     string
    View        string // form/json/merged
    Doc         map[string]any
    Baseline    string
    Hash        string
    Dirty       bool
    ParseError  string
    Violations  []Violation
    Result      *SaveResult
    Skills      []SkillView
    SkillsError string
    AccessDrawerOpen bool
}

`DirtyCurrent`、`discardDrafts` 纳入该状态；`DirtyCurrent` 同时考虑 `ClaudeCfg.Dirty` 与 `Access.Form` 的接入草稿。`discardDrafts` 不丢接入抽屉的独立草稿，除非用户确认。

### 10.3 表单分组

1. 模型与响应：`model`、`fallbackModel`、`effortLevel`、`alwaysThinkingEnabled`。
2. 环境变量：表格 + 受管键只读 + 敏感遮蔽；核心三键常显，其余 10 个接入投影键折叠。
3. 权限：`defaultMode`、`allow`、`ask`、`deny`、`additionalDirectories`；`skipDangerousModePermissionPrompt` 三态，用户级默认 true。
4. 状态栏：`statusLine` 子字段。
5. MCP 与插件：`enabledPlugins`、`enableAllProjectMcpServers`、`enabledMcpjsonServers`、`disabledMcpjsonServers`、`syncClaudeAiPlugins`、`syncClaudeAiSkills`。
6. Skills：`skillOverrides` 四态 + 继承；来源/锁定/有效状态；插件 Skills 只读。
7. 记忆与上下文：`autoCompactEnabled`、`autoMemoryEnabled`、`cleanupPeriodDays`。

当前层缺失的键：显示「继承自下层」+ 来源 + 「覆盖」按钮；不把继承值写入当前文件。

### 10.4 JSON 视图

- 等宽 `ui.TextArea`、300ms 防抖解析、错误显示「第 X 行第 Y 列 + 原因」。
- **P0 不做行号 gutter**：MyGo v0.6.2 的 `ui.TextArea` 不暴露滚动偏移，无法可靠同步行号；自绘编辑器成本高。解析错误中给出行列，并在错误条提供「定位」按钮把光标移到出错偏移。
- 格式化、重置（确认）、校验。
- 受管键被改 → 保存阻断。
- scope 违规分「本次引入」与「历史遗留」。

### 10.5 合并预览

- 顶部优先级说明、`--settings` 不参与、托管不可见提示、草稿不含提示。
- 搜索框、图例。
- 权限求值卡。
- 键行：路径、有效值、来源徽章、合并方式徽章、展开贡献值。
- 敏感值遮蔽，逐行可显示。

### 10.6 保存与生效

- 保存确认框：路径、变更键 chip、备份路径、生效范围。
- 生效判断来自注册表 `Apply`；项目级默认 `next-start`；未知键保守为 `next-start`。
- 保存后结果条：已热生效 / 需重启 / 下次启动 + 触发键 + 备份路径 + 「查看备份」。
- P0 提供从最近备份恢复（复用 `RestoreClaudeConfigBackup`）。

### 10.7 错误与空态

按需求 §6.9 实现。错误文案必须包含「发生了什么 + 下一步」。

对话框：

- 现有 `uikit.ConfirmDialog` 只支持标题 + 一段文本 + 两个按钮，无法承载保存确认（路径、变更键 chip、备份路径、生效徽章）与冲突对话框（取消/重新加载/覆盖写入三动作）。
- P0 在 `uikit` 新增 `RichDialog`（标题、可选副标题、任意 body 视图、N 个动作按钮），内部复用现有对话框壳、遮罩与焦点管理；`ConfirmDialog` 保持不动，现有调用方零改动。
- 危险动作统一 `SoftButton("danger")` 且放末位；安全动作（取消/继续编辑）默认聚焦。
- 保存确认、冲突、脏检查、备份恢复全部使用 `RichDialog`。

## 11. 与接入页的边界

- 受管键集合由 bridge 的 `ComputeManagedSet` 动态计算：
  - `StrongEnv`：13 个接入投影 env 键；
  - `StrongTopLevel`：任一 Claude 接入文档顶层键（排除 `env`）；
  - `WeakEnv`：合成 env 中不属于 `StrongEnv` 的键（全局 env、文档自定义 env）。
- `ManagedSet` 传给 `claudecfg` 的 scope 校验与合并层；`claudecfg` 不反向依赖接入实现。
- 接入页写 `~/.claude/settings.json` 时持有 `claudeSettingsMu`；配置页保存同样持锁。
- 配置页保存时强制恢复 `StrongEnv` 与 `StrongTopLevel` 原值；用户改动返回错误，不静默覆盖。`WeakEnv` 允许改，但保存确认框列出「接入应用会覆盖」。
- 接入页 Claude 编辑器底部增加「更多 Claude Code 设置」按钮：展开/滚动到同页设置区用户级，不跳转页面。
- 配置页受管行提供「打开接入」，打开同页右侧抽屉，不跳转。
- 两边写入前都重新读取并比对；冲突走对话框。
- 接入抽屉「保存并应用」与设置保存共享 `claudeSettingsMu`；设置区 dirty 时应用接入阻断。
- `CLAUDE_CONFIG_DIR` 两页都固定 `~/.claude`；配置页检测到该变量时警告，接入页暂不处理。

## 12. PM 待确认项的裁决

| 项 | 裁决 | 理由 |
|---|---|---|
| 受管键是否扩大 | `StrongEnv` 固定 13 个接入投影 env 键（只读）；`StrongTopLevel` 动态覆盖接入文档顶层键；其余合成 env 为 `WeakEnv`（可编辑但警告） | 接入 Apply 会投影整份文档与合成 env；只锁 13 个 env 键仍会覆盖顶层 `permissions`/全局 env |
| `model` 与 `ANTHROPIC_MODEL` 冲突 | 可编辑 + 风险提示 + 「跟随网关」快捷值，不锁定 | `model` 是 Claude Code 原生键，锁定会让用户无法用原生别名；提示已足够 |
| 导航命名 | 唯一入口「Agent 接入」；Claude 页签内合并接入与设置 | 用户反馈两页切换成本高；未来 Codex 设置留在 Codex 页签 |
| 托管来源展示 | 只展示可读的本地文件与 drop-in；MDM/远程标注「不可见」 | 不假装完整；读取注册表/远程策略超出桌面工具职责 |
| JSON 密钥显示 | 表单/预览遮蔽，JSON 明文 + 强警告 | 与现有接入页「配置 JSON 显示真实令牌」的已确认决策一致；高级用户需要可编辑 |
| `.gitignore` | 只提示，不自动改 | 安全默认；自动改仓库文件属于越权 |
| 撤销本次保存 | P0 提供「从最近备份恢复」，不做完整撤销栈 | 现有备份能力可直接复用，成本低 |
| 键注册表维护 | UI 显示基线「Claude Code 2.1.x」；升级时人工复核 | 无官方稳定 schema 可依赖 |
| 受管键集合 | **动态计算**：13 个强 env + 接入文档顶层键只读；其余合成 env 弱受管 | `writeClaudeLive` 会投影整份接入文档与 env，只锁三键无法阻止跨页覆盖 |
| 危险模式确认 | `skipDangerousModePermissionPrompt` 默认 true，可关；项目共享不可写；OR 合并 | 2026-11-07 用户指示默认 true；保留关闭入口 |
| Skills 开关 | `skillOverrides` 四态 + 继承；插件/作者/托管锁定；未知 override 保留 | 2026-11-07 用户要求 Skills 支持开关；沿用 Claude Code 模型 |
| 项目本地路径 | 按 2.1.211+ 规则默认 Git 根，同时返回 cwd 候选并在路径不同时提示 | 公开 issue #74023（2.1.198）报告按字面 cwd 解析；两版行为不同，必须显示实际路径并让用户可见 |
| `CLAUDE_CONFIG_DIR` | P0 不支持，固定 `~/.claude`，检测到时警告 | 接入页硬编码 `~/.claude`；只改配置页会让两页指向不同文件 |
| 备份 id | `<sha256(realpath)[:16]>/<timestamp>-<filename>` + `meta.json` | 不同项目的 `settings.json` 同名，必须靠 meta 绑定目标路径 |
| Managed drop-in | P0 在 managed 层内按标量覆盖/数组并集/对象合并后参与跨层合并 | 只读 base 文件会显示错误的托管有效值 |
| JSON 行号 | P0 不做 gutter，只给行列错误与定位 | MyGo TextArea 无滚动偏移，无法同步行号 |
| 对话框 | 新增 `uikit.RichDialog`，不改 `ConfirmDialog` | 保存确认与冲突对话框需要富内容和三动作 |

## 13. 测试策略

- `internal/claudecfg`：表驱动单元测试覆盖路径解析（含 Windows/非 Git/家目录仓库/候选路径/符号链接）、合并策略（含四个整值替换例外、数组并集、env 逐变量、hooks 累加、项目级 auto/bypass 失效）、managed drop-in 内部合并、scope 校验、敏感检测、patch diff、`skipDangerousModePermissionPrompt` OR、`skillOverrides` 逐键与锁定。
- `internal/bridge`：临时 HOME 下的读写测试、`ManagedSet` 动态计算（13 强 env + 接入文档顶层键 + 弱 env、接入未应用时退化）、备份分桶与 `meta.json`、保留 10 份、备份失败不写、恢复只作用于正确目标路径、冲突检测、受管键保护、并发写串行化（`-race`）。
- `internal/bridge` 项目列表：`projects` 为 map 的 fixture、兼容数组形态、缺失/损坏 `~/.claude.json` 不阻塞手动选目录。
- `internal/bridge` Skills：用户/项目/插件/内置 fixture、插件锁定、作者锁定、托管锁定、未知 override 保留、非法枚举。
- `internal/desktop`：`uitest` 覆盖层级切换、脏检查、表单编辑、危险模式确认默认 true、Skills 开关、JSON 错误定位、合并预览来源、保存确认、`RichDialog` 焦点顺序。
- 不依赖真实 Claude Code 或真实用户文件；全部使用临时目录与 fake 数据。

## 14. 分期

| 阶段 | 内容 | 验收 |
|---|---|---|
| P0-1 | `claudecfg` 路径 + 键注册表 + 文档解析/patch/diff | 单元测试通过 |
| P0-2 | 合并引擎 + scope 校验 + 敏感检测 | 单元测试覆盖所有策略 |
| P0-3 | bridge API + 动态 `ManagedSet` + 备份分桶/meta/恢复 + 原子写/冲突/并发锁 | `-race` 测试通过 |
| P0-4 | 项目列表（map 契约）+ 项目本地路径候选 + `CLAUDE_CONFIG_DIR` 警告 | 临时 HOME 测试 |
| P0-5 | `uikit.RichDialog` + Claude 页签设置区骨架 + 层级/项目/视图切换 + 脏状态 | uitest 通过 |
| P0-6 | 七组表单（含危险模式确认与 Skills）+ 受管键只读 | uitest 通过 |
| P0-7 | JSON 视图 + 校验 + 错误提示 | uitest 通过 |
| P0-8 | 合并预览 + 权限卡 | uitest 通过 |
| P0-9 | 保存确认 + 生效提示 + 备份恢复 + 托管只读 | uitest 通过 |
| P0-10 | 设计系统对齐、空态/错误态、最终验收 | 全量 `go build/vet/test` |

## 15. 风险

| 风险 | 缓解 |
|---|---|
| Claude Code 版本升级改变键/scope | 注册表集中维护；UI 显示基线版本；未知键走 JSON |
| 合并预览与真实行为不一致 | 标注 `--settings`/托管不可见；特殊规则单列；文案「以 Claude Code 实际运行为准」 |
| 项目本地路径例外 | 始终展示实际解析路径；解析失败回退并提示 |
| 与接入页并发写 | 进程内互斥 + 保存前重读 + 冲突对话框 |
| JSON 明文密钥泄露 | 表单/预览遮蔽；JSON 强警告；日志脱敏 |
| 大文件卡顿 | 1 MiB 上限；防抖解析 |
| Skills 清单读取不全/版本差异 | 显示来源与错误；未知 override 保留；以 `/skills` 为最终参照 |
| 插件 Skill 被误认为可单独关闭 | 插件来源行锁定并指向插件开关；不写无效 override |
