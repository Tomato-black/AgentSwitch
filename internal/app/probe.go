package app

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
)

// ModelProbeTarget 是逻辑模型测试中单个目标的结果。
type ModelProbeTarget struct {
	ChannelID, ChannelName, Model string
	OK                            bool
	Status                        int
	DurationMs                    int64
	ErrorClass                    string
	NextStep                      string
	Cooling                       bool
}

// ModelProbeResult 是一次逻辑模型端到端测试的结果。
type ModelProbeResult struct {
	Model   string
	OK      bool
	Hit     string
	Summary string
	Targets []ModelProbeTarget
}

// ProbeLogicalModel 按目标链依次发送最小请求，返回每个目标的结果。
// 不修改冷却/统计，也不写 request_history。
func (a *App) ProbeLogicalModel(ctx context.Context, logicalName string) (ModelProbeResult, error) {
	cands, err := a.candidates(logicalName)
	if err != nil {
		return ModelProbeResult{}, err
	}
	res := ModelProbeResult{Model: logicalName}
	if len(cands) == 0 {
		res.Summary = "没有可用目标：请确认逻辑模型已启用、渠道已启用且配置了 Key"
		return res, nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].Priority != cands[j].Priority {
			return cands[i].Priority < cands[j].Priority
		}
		return cands[i].ChannelID < cands[j].ChannelID
	})
	for _, c := range cands {
		t, err := a.probeTarget(ctx, c)
		if err != nil {
			return res, err
		}
		res.Targets = append(res.Targets, t)
		if t.OK && !res.OK {
			res.OK = true
			res.Hit = c.ChannelName + "·" + c.UpstreamModel
		}
	}
	if res.OK {
		res.Summary = "至少一个目标可用；首次命中 " + res.Hit
	} else {
		res.Summary = "所有目标均失败；请按每条提示处理"
	}
	return res, nil
}

func (a *App) probeTarget(ctx context.Context, c router.Candidate) (ModelProbeTarget, error) {
	t := ModelProbeTarget{
		ChannelID: c.ChannelID, ChannelName: c.ChannelName, Model: c.UpstreamModel,
		Cooling: !c.CoolingUntil.IsZero() && c.CoolingUntil.After(time.Now()),
	}
	if err := ctx.Err(); err != nil {
		return t, err
	}
	key, err := a.creds.Get(c.SecretKey)
	if err != nil {
		t.ErrorClass = "credential"
		t.NextStep = "渠道密钥不可读；请到渠道页重新保存 Key"
		return t, nil
	}
	ad, ok := a.adapters[c.Protocol]
	if !ok {
		t.ErrorClass = "protocol"
		t.NextStep = "渠道协议不受支持；请改为 anthropic 或 openai"
		return t, nil
	}
	body := []byte(fmt.Sprintf(`{"model":%q,"max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`, c.UpstreamModel))
	reqCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	start := time.Now()
	r, err := ad.Do(reqCtx, provider.DoRequest{
		BaseURL: c.BaseURL, AllowPrivate: c.AllowPrivate, APIKey: string(key),
		UpstreamModel: c.UpstreamModel, RawBody: body,
	})
	t.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		if ctx.Err() != nil {
			return t, ctx.Err()
		}
		t.ErrorClass = string(provider.ClassConnect)
		t.NextStep = probeNextStep(t.ErrorClass)
		return t, nil
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(r.Body, 8<<10))
	t.Status = r.StatusCode
	if r.StatusCode >= 200 && r.StatusCode < 300 {
		t.OK = true
		return t, nil
	}
	t.ErrorClass = string(provider.Classify(r.StatusCode, r.RetryAfterSec, b))
	t.NextStep = probeNextStep(t.ErrorClass)
	return t, nil
}

// probeNextStep 把错误分类翻译成用户可执行的下一步；文案不含密钥/上游正文。
func probeNextStep(class string) string {
	switch class {
	case string(provider.ClassAuth):
		return "检查渠道 API Key 是否正确、是否有该模型权限"
	case string(provider.ClassEndpoint):
		return "检查渠道 Base URL/协议，或到渠道页「拉取模型」确认模型名"
	case string(provider.ClassRateLimit):
		return "上游限流：等待后重试，或在逻辑模型目标链加入备用渠道"
	case string(provider.ClassQuota):
		return "额度不足：更换 Key/渠道，或等待额度恢复"
	case string(provider.ClassServer):
		return "上游 5xx：稍后重试，或把备用渠道前移"
	case string(provider.ClassParam):
		return "请求被上游拒绝：检查渠道模型名是否受支持"
	case string(provider.ClassConnect):
		return "连接失败：检查 Base URL、网络/代理与「允许私网」设置"
	default:
		return "查看渠道详情与设置页最近请求"
	}
}
