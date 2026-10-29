# P1-2c 按日聚合 + 配额/余额实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 设置页用量卡增加「每日 token」聚合图、本地渠道配额剩余、以及支持供应商的上游余额查询；不虚构不支持的余额数据。

**Architecture:** storage 增加 `UsageByDay` 聚合；bridge 增加 `DailyUsage`、`QuotaReport`（settings JSON `usage.quotas`）、`Channels.Balance`；provider 增加 `ProbeBalance`（DeepSeek/Kimi/OpenRouter，按 preset/host 识别）；UI 在用量卡加每日图与配额表，在渠道详情加「查余额」。

**Tech Stack:** Go 1.27、SQLite、MyGo Painter；测试离线（httptest）。

**Spec:** `docs/product/roadmap.md` P1-2；`docs/product/decisions.md` PD-6。

## Global Constraints

- 不自动 commit。
- **不虚构余额**：不支持的供应商明确显示「未提供公开余额查询」。
- 只存 token/成本估算，不存密钥/正文。
- 配额周期按本地月份 + `reset_day`（1–28）；未设置配额不显示剩余。
- 余额查询失败不阻塞 UI，显示 HTTP 状态/下一步。

---

### Task 1: 按日聚合

**Files:**
- Modify: `internal/storage/storage.go`、`internal/storage/request_history_repo.go`
- Modify: `internal/bridge/usage.go`
- Test: `internal/storage/storage_test.go`、`internal/bridge/usage_test.go`

**Interfaces:**
- `DailyUsage{Day string; Requests, InputTokens, OutputTokens int64}`
- `RequestHistoryRepo.UsageByDay(sinceMs int64) ([]DailyUsage, error)`
- `Gateway.DailyUsage(days int) []bridge.DailyUsageRow`（补齐缺失日期为 0）

- [x] **Step 1: 测试**

```go
func TestDailyUsageFillsMissingDays(t *testing.T) {
	a := tempApp(t)
	now := time.Now()
	_ = a.Store().RequestHistory().Add(storage.RequestHistoryRecord{
		ID: "r1", StartedAt: now.UnixMilli(), Channel: "ds", InputTokens: 100, OutputTokens: 20,
	})
	got := (Gateway{A: a}).DailyUsage(3)
	if len(got) != 3 || got[2].InputTokens != 100 || got[2].OutputTokens != 20 {
		t.Fatalf("daily = %+v", got)
	}
}
```

- [x] **Step 2: 实现**

SQL：

```sql
SELECT strftime('%Y-%m-%d', started_at/1000, 'unixepoch', 'localtime') AS day,
       COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
FROM request_history WHERE started_at >= ?
GROUP BY day ORDER BY day
```

bridge 从 `time.Now().AddDate(0,0,-days+1)` 的本地零点开始，按日填充缺失项。

- [x] **Step 3: 运行通过**

---

### Task 2: 上游余额探测

**Files:**
- Create: `internal/provider/balance.go`
- Test: `internal/provider/balance_test.go`
- Modify: `internal/bridge/channels.go`
- Test: `internal/bridge/channels_test.go`
- Modify: `internal/desktop/state/state.go`、`internal/desktop/pages/channels/channels_detail.go`

**Interfaces:**
- `provider.BalanceResult{Supported bool; Provider, Currency, Message string; Total, Granted, ToppedUp, Available float64}`
- `provider.ProbeBalance(ctx, presetID, baseURL, apiKey string, allowPrivate bool) BalanceResult`
- `Channels.BalanceContext(ctx, id) provider.BalanceResult` / `Channels.Balance(id)`

- [x] **Step 1: 测试**

用 httptest 模拟：
- DeepSeek `GET /user/balance` → `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"110.00","granted_balance":"10.00","topped_up_balance":"100.00"}]}`
- Kimi `GET /v1/users/me/balance` → `{"code":0,"data":{"available_balance":88.5,"voucher_balance":8.5,"cash_balance":80}}`
- OpenRouter `GET /api/v1/credits` → `{"data":{"total_credits":100.5,"total_usage":25.75}}`，Available=74.75
- 未知供应商 → `Supported=false`，Message 明确。

- [x] **Step 2: 实现**

按 `presetID` 或 host 识别；HTTP GET + Bearer + 10s 超时 + 不跟随重定向；非 200 返回状态消息；不打印密钥。DeepSeek/Kimi 的 base URL 取 scheme+host 拼固定路径，避免 `/v1/v1`。

- [x] **Step 3: UI**

渠道详情增加「查余额」按钮 + `ChannelState.BalanceTask/BalanceMsg`；异步调用 `Channels.BalanceContext`；成功显示货币与金额，失败显示错误；不支持时显示 Message。

- [x] **Step 4: 运行通过**

---

### Task 3: 本地渠道配额

**Files:**
- Modify: `internal/bridge/usage.go`
- Test: `internal/bridge/usage_test.go`
- Modify: `internal/desktop/state/state.go`、`internal/desktop/pages/settings/settings.go`

**Interfaces:**
- `ChannelQuota{Tokens int64; USD float64; ResetDay int}`
- `Gateway.QuotasJSON()` / `SaveQuotasJSON(raw string)`
- `QuotaStatus{Channel string; TokenQuota, TokenUsed, TokenRemaining int64; USDQuota, USDUsed, USDRemaining float64; ResetDay int}`
- `Gateway.QuotaReport() []QuotaStatus`

- [x] **Step 1: 测试**

设置配额 `{"ds":{"tokens":1000000,"usd":10,"reset_day":1}}`，写入本月 100 token / $1 用量，断言剩余 999900 token / $9；reset_day 越界/负数拒绝。

- [x] **Step 2: 实现**

- 配额 JSON 存 settings `usage.quotas`。
- 周期起点：本月 `reset_day` 零点；若今天在 reset_day 之前，用上月 reset_day。
- `UsageByChannelModel(periodStart)` 汇总 token；成本用价格表。
- 只返回有配额的渠道；未配置不显示。

- [x] **Step 3: UI**

用量卡增加：
- 「每日 Token（近 7 天）」柱状图（单色，日期 MM-DD）
- 配额行：`渠道 · 配额 1.0M / 已用 216k / 剩余 784k · $10 / $3 / $7`
- 「配额表（JSON）」TextArea + 保存按钮；说明按本地月份和 reset_day 计算，非上游余额。

- [x] **Step 4: 运行通过**

---

### Task 4: 全量回归与文档

- [x] `go build ./...`、`go vet ./...`、`go test ./...`
- [x] `go test -race ./internal/provider ./internal/bridge ./internal/desktop/pages/settings ./internal/desktop/pages/channels`
- [x] README/roadmap 同步；有限支持余额的供应商列表写明。

## Self-Review

- 按日聚合、本地配额、上游余额三者分开：本地配额是估算，上游余额是只读探测。
- 不支持的供应商明确提示，不伪造数据。
- 已知限制：本地配额按本地月份；余额仅 DeepSeek/Kimi/OpenRouter（OpenRouter 需 Management Key）。


---

## 执行结果（2026-10-29）

- 按日聚合、每日 token 图、本地渠道配额、DeepSeek/Kimi/OpenRouter 余额查询均已完成。
- `go build ./...`、`go vet ./...`、`go test ./...` 通过；相关包 `-race` 通过。
- 余额查询只覆盖有公开 API 的供应商；其余显示「未提供公开余额查询」，不伪造数字。
- 本地配额按本地月份 + reset_day 计算，是估算，不是上游账户余额。
- 未执行 git commit（等待用户验收）。
