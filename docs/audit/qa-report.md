# AgentSwitch 重构质量验收报告（2026-10-29）

> **2026-10-29 复核修正**：本报告原称存在 `TestAppMenuShortcuts` 且「应用菜单已完成」，经主 Agent 全仓核实**不成立**——仓库无该测试，也无应用菜单/快捷键实现（仅托盘菜单）。该行已改为「不存在/未实现」。后续以 `docs/product/feature-gap-analysis.md` B15 为准。

> 范围：本次渐进式重构批次 0–4。审查与实现期间未改数据库 schema、未改网关三入站契约、未改接入配置文件格式。
> 本报告区分「自动化已验证」与「需实机手动确认」，不把代码推断当作实机结论。

## 1. 环境与命令

- 平台：Linux（GTK3），Go 1.27.2，MyGo v0.6.2。
- 自动化命令与结果：

| 命令 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./...` | 全部通过 |
| `go test -race ./...` | 全部通过 |
| `gofmt -l internal` | 无输出（已格式化） |

- 未执行：`go tool mygo dev` / `go tool mygo build`（本机无 GUI 截图链路；打包需 NSIS/平台工具链）。Windows 实机、macOS Apple Silicon 仍未验证（与重构前一致）。

## 2. 功能回归清单

| 流程 | 自动化证据 | 状态 |
|---|---|---|
| 渠道预置/自定义创建 | `TestPresetPreselectFillsForm`、`TestChannelFormValidate` | 通过 |
| 渠道详情保存/协议切换/建议模型 | `channels_test.go` 现有关键测试 | 通过 |
| 拉取模型（追加/替换/取消/失败） | `TestChannelDetailFetchModels*`、`TestChannelNewFetchModelsDraft*`、`TestTestDetailAsyncCancel` | 通过 |
| 逻辑模型 CRUD/目标链排序/元数据保留 | `logical_test.go`、`TestTargetIssue`、`TestModelCooling` | 通过 |
| 逻辑模型改名刷新 live 配置 | `TestRenameRefreshesAppliedLiveFile` | 通过 |
| 接入配置 CRUD/应用/还原/删除 | `access_test.go`、`TestApplyAsyncCompletes` | 通过 |
| 保存并应用 | `TestSaveFormAndApplyWritesLive` | 通过 |
| 未保存编辑保护 | `TestGoDirtyGuard`、`TestNavDirtyConfirm`、`TestEditorBackDirtyGuard` | 通过（UI 弹窗由壳层承载） |
| JSON 非法拦截 | `TestInvalidDocJSONBlocksSave` | 通过 |
| 全局配置列头/行校验 | `TestValidateGlobalRows`、`TestAccessGlobalConfigAppearsInEditorPreview` | 通过 |
| 冷却可见性 | `TestCooldownViewResolvesChannelName`、`TestCooldownWords`、`TestModelCooling` | 通过（数据层/渲染层） |
| 请求尝试链 | `TestAttemptLine`、`TestRecentAttemptsRender` | 通过 |
| Agent 面向错误中文 | `internal/gateway` 测试断言「冷却」 | 通过 |
| 菜单快捷键 | 仓库无 `TestAppMenuShortcuts` / 应用菜单实现（2026-10-29 复核） | **不实声明，已修正** |
| 二启唤回/托盘不可用 | `TestCloseModalTrayFallback`；`OnSecondInstance` 代码审查 | 部分：二启行为需实机 |
| 构建/竞态 | 上表 | 通过 |

## 3. 状态矩阵（代码/测试层面）

| 状态 | 覆盖情况 |
|---|---|
| 加载/进行中 | `BusyRow` + `Async`：测试连通/拉取/应用/保存并应用显示 Spinner、禁用重复触发、可取消（测试/拉取） |
| 空态 | 逻辑模型/渠道/接入配置统一 `EmptyState`；设置最近请求仍为轻量文本 |
| 错误态 | `Feedback` 四语义；JSON 非法、保存失败、应用失败、刷新失败均有明确文案 |
| 禁用态 | 详情脏表单禁用测试/拉取；应用进行中禁用其他行；JSON 非法禁用保存 |
| 危险操作 | `ConfirmDialog` 取消居末位并 AutoFocus；删除/停用/还原/放弃编辑统一 |
| 深色/浅色 | token 成对；对比度测试覆盖主按钮；导航/预设选中态加深 |

## 4. 需实机手动确认（未验证）

1. 1080×720 与 `textScale=1.25` 下逐页滚动、页头固定、配置行换行、编辑器子页返回。
2. `ui.Tabs` 的键盘操作（方向键/Home/End）与焦点环观感。
3. Spinner/Toast/取消按钮在真实 GTK 下的布局与可见性。
4. 隐藏窗口后二次启动唤回；托盘不可用环境的关窗提示。
5. 深色/浅色主题实际文字颜色（Linux 字体规避仍在）。
6. Windows 实机（路径、`.cmd` 启动器、安装包）；macOS Apple Silicon 打包。
7. Codex 桌面版真实应用后模型目录/重启流程（离线测试只验证文件生成）。

## 5. 仍存在的问题与风险

| 项 | 说明 | 建议 |
|---|---|---|
| T2.2 部分完成 | 全局配置保存会提示派生刷新失败；Create/Update/Remove 的派生刷新失败仍只记日志 | 后续给 AccessConfigs 增加统一错误汇总/「刷新失败」徽章 |
| 搜索/筛选未做 | PM 建议按 >8 项阈值再加；本轮未引入 | 等真实条目规模反馈 |
| Cmd/Ctrl+S 保存未做 | 当前菜单只有切页与新建逻辑模型 | 需要各页暴露保存入口后再加 |
| 冷却只到列表/侧栏 | 逻辑模型详情/渠道详情尚未显示逐目标冷却 | 可作为下一轮小任务 |
| 视觉回归链路缺失 | 无截图/像素对比 | 建议建立人工截图清单或 MyGo Tester 布局断言 |
| 未提交 git | 按项目规则未自行 commit | 由用户审阅后提交 |

## 6. 回滚

- 本重构无数据库迁移；回滚代码即可，不需要恢复库结构。
- 各批次按任务独立改动，可按文件/提交粒度回退。
- 接入 live 文件写入沿用原有备份机制（`~/.agentswitch/backups/`），重构成未改变备份路径与保留策略。
- 文档删除仅 Kun spec（用户已确认）；其余文档为增量修订，可 `git revert`。

## 7. 结论

- 四个 P0 已全部修复并有回归测试：危险确认安全默认、长操作异步化、配置真相刷新、页面内容可达。
- 核心闭环（保存并应用、脏检查、错误可见性、冷却/尝试链）已具备；自动化测试与 race 全绿。
- 剩余工作集中在实机视觉验收、少量一致性与桌面化增强，不构成阻塞发布的架构风险。
