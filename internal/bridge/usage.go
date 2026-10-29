package bridge

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

const (
	usagePricesKey = "usage.prices"
	usageQuotasKey = "usage.quotas"
)

// ChannelQuota 是本地渠道配额（按渠道名），单位：token 数 / 美元。
// ResetDay 是每月配额重置日（1–28）；未设置按 1 日。
type ChannelQuota struct {
	Tokens   int64   `json:"tokens,omitempty"`
	USD      float64 `json:"usd,omitempty"`
	ResetDay int     `json:"reset_day,omitempty"`
}

// ModelPrice 是每 1M token 的价格（美元）；用于成本估算，不代表账单。
type ModelPrice struct {
	Input  float64 `json:"input"`
	Output float64 `json:"output"`
}

// UsageRow 是按渠道·模型聚合的用量。
type UsageRow struct {
	Channel, Model            string
	Requests                  int64
	InputTokens, OutputTokens int64
	CostUSD                   float64
	Priced                    bool
}

// UsageReport 是一段时间的用量/成本估算。
type UsageReport struct {
	Days         int
	Rows         []UsageRow
	TotalInput   int64
	TotalOutput  int64
	TotalCostUSD float64
	Priced       bool // 至少一行匹配到价格
	Error        string
}

// DurationPoint 是一条响应耗时点（按渠道绘折线图）。
type DurationPoint struct {
	Channel    string
	StartedAt  time.Time
	DurationMs int64
	Status     int
	Streaming  bool
}

// DurationSeries 返回最近 days 天的请求耗时点（按时间升序），最多 limit 条。
// 数据来自持久化请求历史；失败请求也包含，便于观察异常尖峰。
func (g Gateway) DurationSeries(days, limit int) []DurationPoint {
	if days <= 0 {
		days = 1
	}
	if limit <= 0 {
		limit = 500
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	recs, err := g.A.Store().RequestHistory().ListRecent(limit)
	if err != nil {
		return nil
	}
	out := make([]DurationPoint, 0, len(recs))
	for i := len(recs) - 1; i >= 0; i-- {
		r := recs[i]
		if r.StartedAt < since {
			continue
		}
		out = append(out, DurationPoint{
			Channel: r.Channel, StartedAt: time.UnixMilli(r.StartedAt),
			DurationMs: r.DurationMs, Status: r.Status, Streaming: r.Streaming,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out
}

// DailyUsageRow 是按本地日期聚合的用量（缺失日期补 0）。
type DailyUsageRow struct {
	Day                       string
	Requests                  int64
	InputTokens, OutputTokens int64
}

// DailyUsage 返回最近 days 个自然日（含今天）的 token 用量，按日期升序。
func (g Gateway) DailyUsage(days int) []DailyUsageRow {
	if days <= 0 {
		days = 7
	}
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, -(days - 1))
	rows, err := g.A.Store().RequestHistory().UsageByDay(start.UnixMilli())
	if err != nil {
		return nil
	}
	byDay := make(map[string]DailyUsageRow, len(rows))
	for _, r := range rows {
		byDay[r.Day] = DailyUsageRow{
			Day: r.Day, Requests: r.Requests,
			InputTokens: r.InputTokens, OutputTokens: r.OutputTokens,
		}
	}
	out := make([]DailyUsageRow, 0, days)
	for d := start; !d.After(now); d = d.AddDate(0, 0, 1) {
		day := d.Format("2006-01-02")
		row := byDay[day]
		row.Day = day
		out = append(out, row)
	}
	return out
}

// UsageReport 聚合最近 days 天的 token 用量；流式与非流式均统计（上游返回 usage 时）。
func (g Gateway) UsageReport(days int) UsageReport {
	if days <= 0 {
		days = 30
	}
	since := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	aggs, err := g.A.Store().RequestHistory().UsageByChannelModel(since)
	rep := UsageReport{Days: days}
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	prices := g.priceMap()
	for _, a := range aggs {
		row := UsageRow{
			Channel: a.Channel, Model: a.Model, Requests: a.Requests,
			InputTokens: a.InputTokens, OutputTokens: a.OutputTokens,
		}
		if p, ok := prices[a.Model]; ok {
			row.Priced = true
			row.CostUSD = float64(a.InputTokens)/1e6*p.Input + float64(a.OutputTokens)/1e6*p.Output
			rep.Priced = true
			rep.TotalCostUSD += row.CostUSD
		}
		rep.TotalInput += a.InputTokens
		rep.TotalOutput += a.OutputTokens
		rep.Rows = append(rep.Rows, row)
	}
	return rep
}

// PricesJSON 返回价格表原始 JSON（未配置返回 "{}"）。
func (g Gateway) PricesJSON() string {
	raw, err := g.A.Store().Settings().Get(usagePricesKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return "{}"
	}
	return raw
}

// SavePricesJSON 校验并保存价格表；空串归一为 "{}"。
func (g Gateway) SavePricesJSON(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	var prices map[string]ModelPrice
	if err := json.Unmarshal([]byte(raw), &prices); err != nil {
		return fmt.Errorf("价格表 JSON 无效：%w", err)
	}
	for model, p := range prices {
		if p.Input < 0 || p.Output < 0 {
			return fmt.Errorf("价格不能为负数：%s", model)
		}
	}
	b, err := json.Marshal(prices)
	if err != nil {
		return err
	}
	return g.A.Store().Settings().Set(usagePricesKey, string(b))
}

// QuotasJSON 返回本地配额表原文（未配置返回 "{}"）。
func (g Gateway) QuotasJSON() string {
	raw, err := g.A.Store().Settings().Get(usageQuotasKey)
	if err != nil || strings.TrimSpace(raw) == "" {
		return "{}"
	}
	return raw
}

// SaveQuotasJSON 校验并保存本地配额表；空串归一为 "{}"。
func (g Gateway) SaveQuotasJSON(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = "{}"
	}
	var quotas map[string]ChannelQuota
	if err := json.Unmarshal([]byte(raw), &quotas); err != nil {
		return fmt.Errorf("配额表 JSON 无效：%w", err)
	}
	for channel, q := range quotas {
		if q.Tokens < 0 || q.USD < 0 {
			return fmt.Errorf("配额不能为负数：%s", channel)
		}
		if q.ResetDay < 0 || q.ResetDay > 28 {
			return fmt.Errorf("重置日需在 1–28 之间：%s", channel)
		}
		if q.ResetDay == 0 {
			q.ResetDay = 1
			quotas[channel] = q
		}
	}
	b, err := json.Marshal(quotas)
	if err != nil {
		return err
	}
	return g.A.Store().Settings().Set(usageQuotasKey, string(b))
}

// QuotaStatus 是单渠道本地配额状态；本地估算，不代表上游账户余额。
type QuotaStatus struct {
	Channel        string
	TokenQuota     int64
	TokenUsed      int64
	TokenRemaining int64
	USDQuota       float64
	USDUsed        float64
	USDRemaining   float64
	ResetDay       int
}

// QuotaReport 计算每个已配置渠道在当前配额周期内的已用/剩余量。
// 周期起点 = 本月 reset_day 零点；若今天早于 reset_day，则取上月 reset_day。
func (g Gateway) QuotaReport() []QuotaStatus {
	var quotas map[string]ChannelQuota
	if err := json.Unmarshal([]byte(g.QuotasJSON()), &quotas); err != nil {
		return nil
	}
	now := time.Now()
	prices := g.priceMap()
	byChannel := map[string]struct {
		tokens int64
		usd    float64
	}{}
	// 每个渠道可能有不同 reset_day，按各自周期查询；先按 reset_day 分组。
	groups := map[int][]string{}
	for channel, q := range quotas {
		rd := q.ResetDay
		if rd < 1 || rd > 28 {
			rd = 1
		}
		groups[rd] = append(groups[rd], channel)
	}
	for rd, channels := range groups {
		start := quotaPeriodStart(now, rd)
		aggs, err := g.A.Store().RequestHistory().UsageByChannelModel(start.UnixMilli())
		if err != nil {
			continue
		}
		want := map[string]bool{}
		for _, ch := range channels {
			want[ch] = true
		}
		for _, a := range aggs {
			if !want[a.Channel] {
				continue
			}
			row := byChannel[a.Channel]
			row.tokens += a.InputTokens + a.OutputTokens
			if p, ok := prices[a.Model]; ok {
				row.usd += float64(a.InputTokens)/1e6*p.Input + float64(a.OutputTokens)/1e6*p.Output
			}
			byChannel[a.Channel] = row
		}
	}
	out := make([]QuotaStatus, 0, len(quotas))
	for channel, q := range quotas {
		rd := q.ResetDay
		if rd < 1 || rd > 28 {
			rd = 1
		}
		used := byChannel[channel]
		st := QuotaStatus{
			Channel: channel, ResetDay: rd,
			TokenQuota: q.Tokens, TokenUsed: used.tokens,
			USDQuota: q.USD, USDUsed: used.usd,
		}
		st.TokenRemaining = st.TokenQuota - st.TokenUsed
		st.USDRemaining = st.USDQuota - st.USDUsed
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Channel < out[j].Channel })
	return out
}

func quotaPeriodStart(now time.Time, resetDay int) time.Time {
	if resetDay < 1 || resetDay > 28 {
		resetDay = 1
	}
	start := time.Date(now.Year(), now.Month(), resetDay, 0, 0, 0, 0, now.Location())
	if now.Before(start) {
		start = start.AddDate(0, -1, 0)
	}
	return start
}

func (g Gateway) priceMap() map[string]ModelPrice {
	var prices map[string]ModelPrice
	if err := json.Unmarshal([]byte(g.PricesJSON()), &prices); err != nil {
		return nil
	}
	return prices
}
