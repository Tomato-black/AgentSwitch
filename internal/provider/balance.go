package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// BalanceResult 是上游账户余额查询结果；Supported=false 表示该供应商没有
// 公开余额 API，调用方应展示 Message，而不是伪造数字。
type BalanceResult struct {
	Supported bool    `json:"supported"`
	Provider  string  `json:"provider,omitempty"`
	Currency  string  `json:"currency,omitempty"`
	Total     float64 `json:"total,omitempty"`
	Granted   float64 `json:"granted,omitempty"`
	ToppedUp  float64 `json:"topped_up,omitempty"`
	Available float64 `json:"available,omitempty"`
	Message   string  `json:"message,omitempty"`
}

// ProbeBalance 按 preset/host 识别供应商并查询余额。只支持有公开只读余额
// API 的供应商；其余返回 Supported=false。查询失败不返回密钥或响应正文。
func ProbeBalance(ctx context.Context, presetID, baseURL, apiKey string, allowPrivate bool) BalanceResult {
	if err := ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return BalanceResult{Message: "Base URL 无效：" + err.Error()}
	}
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return BalanceResult{Message: "Base URL 无效"}
	}
	provider := strings.ToLower(strings.TrimSpace(presetID))
	host := strings.ToLower(u.Hostname())
	if provider == "" {
		switch {
		case host == "api.deepseek.com":
			provider = "deepseek"
		case host == "api.moonshot.cn" || host == "api.moonshot.ai":
			provider = "kimi"
		case host == "openrouter.ai":
			provider = "openrouter"
		}
	}
	origin := u.Scheme + "://" + u.Host
	var endpoint string
	switch provider {
	case "deepseek":
		endpoint = origin + "/user/balance"
	case "kimi":
		endpoint = origin + "/v1/users/me/balance"
	case "openrouter":
		endpoint = origin + "/api/v1/credits"
	default:
		return BalanceResult{
			Supported: false,
			Message:   "该供应商未提供公开余额查询；可参考本地用量/配额估算",
		}
	}

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return BalanceResult{Supported: true, Provider: provider, Message: "构造请求失败"}
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := client.Do(req)
	if err != nil {
		return BalanceResult{Supported: true, Provider: provider, Message: "连接失败；请检查网络/代理"}
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("查询失败：HTTP %d", resp.StatusCode)
		if provider == "openrouter" && (resp.StatusCode == 401 || resp.StatusCode == 403) {
			msg = "OpenRouter 需要 Management Key；普通推理 Key 不能查询余额"
		}
		return BalanceResult{Supported: true, Provider: provider, Message: msg}
	}

	switch provider {
	case "deepseek":
		var r struct {
			IsAvailable  bool `json:"is_available"`
			BalanceInfos []struct {
				Currency        string `json:"currency"`
				TotalBalance    string `json:"total_balance"`
				GrantedBalance  string `json:"granted_balance"`
				ToppedUpBalance string `json:"topped_up_balance"`
			} `json:"balance_infos"`
		}
		if json.Unmarshal(body, &r) != nil || len(r.BalanceInfos) == 0 {
			return BalanceResult{Supported: true, Provider: provider, Message: "响应格式无法识别"}
		}
		info := r.BalanceInfos[0]
		total := parseAmount(info.TotalBalance)
		return BalanceResult{
			Supported: true, Provider: provider, Currency: info.Currency,
			Total: total, Granted: parseAmount(info.GrantedBalance),
			ToppedUp: parseAmount(info.ToppedUpBalance), Available: total,
		}
	case "kimi":
		var r struct {
			Code int `json:"code"`
			Data struct {
				AvailableBalance float64 `json:"available_balance"`
				VoucherBalance   float64 `json:"voucher_balance"`
				CashBalance      float64 `json:"cash_balance"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &r) != nil {
			return BalanceResult{Supported: true, Provider: provider, Message: "响应格式无法识别"}
		}
		if r.Code != 0 {
			return BalanceResult{Supported: true, Provider: provider, Message: "查询失败：上游返回非 0 code"}
		}
		return BalanceResult{
			Supported: true, Provider: provider, Currency: "CNY",
			Total: r.Data.AvailableBalance, Available: r.Data.AvailableBalance,
			Granted: r.Data.VoucherBalance, ToppedUp: r.Data.CashBalance,
		}
	case "openrouter":
		var r struct {
			Data struct {
				TotalCredits float64 `json:"total_credits"`
				TotalUsage   float64 `json:"total_usage"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &r) != nil {
			return BalanceResult{Supported: true, Provider: provider, Message: "响应格式无法识别"}
		}
		return BalanceResult{
			Supported: true, Provider: provider, Currency: "USD",
			Total: r.Data.TotalCredits, Available: r.Data.TotalCredits - r.Data.TotalUsage,
		}
	}
	return BalanceResult{Supported: false, Message: "该供应商未提供公开余额查询"}
}

func parseAmount(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}
