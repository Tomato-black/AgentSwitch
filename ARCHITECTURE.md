# 架构概览

## 两平面分离（最重要的边界）

```
┌─────────────────────────── 桌面控制面 ───────────────────────────┐
│  MyGo 原生 UI（internal/desktop）                                  │
│    逻辑模型 · 渠道 · 接入 · 设置                                     │
│  桥接（internal/bridge, mygo.Bind）                                │
│  应用装配（internal/app）· SQLite（internal/storage）               │
│  ── 只做配置/状态/操作/查询，绝不承载模型请求正文流量 ──               │
└──────────────────────────────┬───────────────────────────────────┘
                               │ 进程内（单二进制）
┌──────────────────────────────▼───────────────────────────────────┐
│                       模型数据面（internal/gateway）                │
│  三入站：/v1/messages · /v1/chat/completions · /v1/responses       │
│  协议转换（internal/protocol）→ 路由（internal/router）              │
│  → 供应商适配（internal/provider）→ 上游 API                         │
└───────────────────────────────────────────────────────────────────┘
```

- **网关生命周期独立于 UI**：关窗 ≠ 停网关（托盘驻留）；停止走优雅收尾（拒新请求、宽限在途、刷事件队列、关 SQLite）
- 核心业务包可在无桌面 UI 的测试环境运行（全部测试离线）

## 包依赖规则

```
cmd/agentswitch ──→ desktop ──→ bridge ──→ app ──→ storage / credential / config
                                   │
gateway ──→ protocol / router / provider / telemetry
```

- `router` 不依赖 MyGo、UI 或具体 HTTP handler；`provider` 通过接口暴露调用能力，不反向依赖 `router`
- `gateway` 组合 protocol/router/provider，不自行实现各家供应商的错误判断（分类在 `provider/classify.go`）
- SQLite 与密钥存储通过接口注入；`desktop` 收敛平台差异（如 Linux 字体渲染规避）

## 路由与故障转移

路由主体是用户自定义的**逻辑模型**（`logical_models` + `logical_model_targets` 有序目标链）：

1. Agent 请求逻辑模型名（如 `my-code`），网关按目标 `priority` 升序跨「渠道·模型」尝试
2. **仅在尚未向客户端提交响应头/任何流式事件前**允许跨目标切换；流已开始后绝不静默重放（上游可能已计费）
3. 错误分类不只凭 HTTP 状态码（结合供应商错误体）；400 参数错误不重试；客户端取消不转移
4. 冷却按 ChannelID 记账、原子更新；发请求前**二次校验**渠道冷却状态
5. 未登记模型名返回明确错误（不做静默透传）

## 渠道与协议

- 渠道 = 单 Key 执行单元（`channels` + `channel_models` 建议列表 + `channel_secrets` 密文）
- 渠道协议两种：`anthropic`（出站 `/v1/messages`）、`openai`（出站 `/chat/completions`）；入站三协议与渠道协议不一致时在 `internal/protocol` 互转（含 Responses↔Chat 的工具映射）
- 渠道预置（`internal/presets`）：embed 默认 JSON（20 条双协议，官网核实）+ `~/.agentswitch/presets.json` 用户覆盖

## 存储

- SQLite 单库（非秘密配置 + 状态）；migrations 内嵌 `internal/storage`（v1 + v2 纯增列就地升级；破坏性变更走删库重建、启动显式报错）
- 密钥密文（AES-256-GCM）在库内、主密钥 `master.key` 在库外
- 事件遥测 = 内存环 + 轮转文件日志（5 MiB gzip × 2），不落 SQLite
