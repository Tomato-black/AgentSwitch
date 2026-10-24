# 贡献指南

感谢关注 AgentSwitch！

## 开发环境

- Go 1.27+（`GOTOOLCHAIN=auto` 自动拉取）；Linux 上需要 GTK3（无需 WebKitGTK）
- 克隆后：`go build ./... && go test ./...` 应全绿

## 工作流

1. Fork → 特性分支（`feat-xxx` / `fix-xxx`）→ PR 到 `master`
2. 提交前本地验证：

```bash
go build ./... && go vet ./... && go test -race ./...
```

## 硬性约定

- **测试全部离线**：用 httptest / Fake Provider，绝不打真实上游；涉及并发/冷却的测试必须 `-race` 通过
- **密钥卫生**：密钥、令牌、Authorization、完整 Prompt 不得出现在代码、测试、日志、错误信息中；测试用占位字符串
- **UI 全中文**（当前阶段）；文案变更不需要截图测试（渲染薄壳不测字符串细节）
- **路由不变量**（`internal/router`、`internal/gateway`）：首字节前可切换目标、首字节后绝不重放；400 不重试；客户端取消不转移；冷却按 ChannelID 原子记账
- **依赖克制**：新依赖需在 PR 中说明理由

## 提交信息

格式 `type(scope): 摘要`（中文正文可），type 取 feat / fix / test / docs / refactor / chore。

## 设计文档

功能以 `docs/superpowers/specs/` 下对应 spec 为准；代码与 spec 冲突时以 spec 为准并在 PR 中指出。新增功能请先开 issue 讨论。
