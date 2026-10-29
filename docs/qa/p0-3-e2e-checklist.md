# P0-3 真实 Agent 与平台验证清单（2026-10-29）

> 本清单用于发布前人工验收。**真实 Agent smoke 会调用上游模型并消耗额度**，默认不自动执行；需用户明确同意后在目标机器上运行。
> **2026-10-29 用户选择**：稍后自行执行真实 smoke，不在本轮自动消耗额度；P0-3 保持「部分完成」。
> 自动化部分（构建、离线测试、交叉编译）已在 2026-10-29 执行，结果见文末。

## 1. 自动化门禁（已执行）

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./internal/storage ./internal/app ./internal/bridge ./internal/desktop/...
GOOS=windows GOARCH=amd64 go build -o /tmp/agentswitch-windows.exe ./cmd/agentswitch
GOOS=darwin  GOARCH=arm64 go build -o /tmp/agentswitch-darwin-arm64 ./cmd/agentswitch
```

- Windows/macOS 交叉编译只证明「能产出二进制」，**不等于**启动、托盘、接入写入、安装包实机验证。
- 平台实机结论不能由交叉编译替代。

## 2. Claude Code smoke（需要用户同意，消耗额度）

前置：
1. 启动 AgentSwitch，网关运行中。
2. 接入页为 Claude Code 创建配置，绑定一个已测试可用的逻辑模型，点「保存并应用」。
3. 确认 `~/.claude/settings.json` 的受管 env 指向 `http://127.0.0.1:<port>`，模型名正确。
4. 新开终端（让 Claude Code 读取最新 settings）。

运行：

```bash
claude -p "Reply with exactly: OK" --model <逻辑模型名>
```

验收：
- 输出包含 `OK`，退出码 0。
- AgentSwitch 设置页「最近请求」出现该请求，状态 2xx，尝试链至少 1 条。
- 若失败：在「测试模型」或最近请求尝试链中定位是 Key、模型名、协议、冷却还是接入文件问题。

## 3. Codex smoke（需要用户同意，消耗额度）

前置：
1. 接入页 Codex 配置保存并应用；确认 `~/.codex/config.toml` 的 `model`/`model_provider=agentswitch`/`base_url` 正确。
2. **完全退出 Codex**（桌面版关窗口不算），重新打开；旧对话的 provider 不迁移，新建对话。

运行：

```bash
codex exec "Reply with exactly: OK"
```

验收：
- 输出包含 `OK`，退出码 0。
- 设置页出现 `/v1/responses` 请求，状态 2xx。
- 流式输出中断/空闲超时场景：Agent 收到 error 事件，不出现半截工具调用或虚假 `message_stop`（用 Fake Provider 已覆盖，实机只做最终确认）。

## 4. Windows x64 实机（待用户环境）

1. 运行安装包/便携版 `AgentSwitch.exe`，确认窗口、托盘、最小尺寸。
2. 启动网关，渠道/逻辑模型/接入页可操作。
3. Claude Code 写入 `%USERPROFILE%\.claude\settings.json`；Codex 写入 `%USERPROFILE%\.codex\config.toml`。
4. 非默认接入配置生成 `.cmd` 启动器；「复制启动命令」/「打开终端」可用。
5. Claude Desktop 路径按 `%LOCALAPPDATA%\Claude` / `Claude-3p` 正确选择。
6. 关窗后托盘驻留、二次启动唤回、退出网关优雅收尾。
7. 记录 Windows 版本、是否 SmartScreen 提示、是否有杀软拦截。

## 5. macOS Apple Silicon（待用户环境）

1. `go tool mygo build` 产出 `.app`/dmg；启动无 Gatekeeper 阻断（或记录未签名提示）。
2. 窗口/托盘/关窗驻留/二次启动唤回。
3. Claude Code/Codex/Claude Desktop 配置文件路径与 `~/Library/Application Support/AgentSwitch` 数据目录。
4. 亮色/深色主题文字颜色正常（Linux 字体规避不影响 macOS）。
5. 记录 macOS 版本、芯片、是否 Rosetta。

## 6. 文档可信度检查（已执行）

- [x] 修正 `docs/audit/qa-report.md` 的 `TestAppMenuShortcuts` 不实声明。
- [x] 修正 `docs/audit/refactoring-plan.md` 「应用菜单已完成」不实记录。
- [x] 修正 `docs/audit/current-state.md` / `ui-review.md` 的「最近 8 条」为 20 条并注明持久化历史。
- [x] README 补充「仅保存/应用」「请求历史持久化」「逻辑模型测试」。

## 7. 2026-10-29 自动化结果

| 项 | 结果 |
|---|---|
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test ./...` | 全部通过 |
| `go test -race`（storage/app/bridge/desktop） | 全部通过 |
| `GOOS=windows GOARCH=amd64 go build` | 产出 31 MB 二进制（未实机运行） |
| `GOOS=darwin GOARCH=arm64 go build` | 产出 29 MB 二进制（未实机运行） |
| 本机 Claude Code CLI | 已安装（`/home/zhangcao/.nvm/.../bin/claude`）；未运行真实请求 |
| 本机 Codex CLI | 已安装（`/home/zhangcao/.nvm/.../bin/codex`）；未运行真实请求 |

**结论**：离线与交叉编译门禁通过；真实 Agent smoke 与 Windows/macOS ARM 实机仍待用户环境执行，未宣称已验证。
