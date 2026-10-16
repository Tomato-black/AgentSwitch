// Package router 是路由决策层：纯函数，不持共享可变状态（冷却状态由 gateway
// 持有并以快照形式随候选传入，docx §6.3 可解释决策在这里产出）。
package router

import (
	"sort"
	"time"
)

// Candidate 是一个可尝试的「渠道×密钥×模型」三元组（gateway/app 从 storage
// join 装配；CoolingUntil/Reason 来自冷却快照）。
type Candidate struct {
	ChannelID, ChannelName string
	KeyID, KeyName         string
	SecretKey              string // 凭据引用（本体在 SecretStore）
	Protocol, BaseURL      string
	AllowPrivate           bool
	UpstreamModel          string
	ChannelPriority        int
	KeyPriority            int
	CoolingUntil           time.Time // 零值=未冷却
	CoolingReason          string
}

// FilteredCandidate 是被过滤掉的候选与原因（可解释记录，docx §6.3）。
type FilteredCandidate struct {
	Candidate Candidate
	Reason    string
}

// Decision 是一次路由决策：Ordered 为尝试顺序，Filtered 附带剔除原因。
type Decision struct {
	Model    string
	Ordered  []Candidate
	Filtered []FilteredCandidate
}

// Router 依公开模型从候选集产出有序尝试计划。
type Router interface {
	Resolve(model string, cands []Candidate) Decision
}

type PriorityRouter struct{}

// Resolve 剔除冷却中候选（记原因），其余按 渠道优先级→密钥优先级→KeyID 稳定全序。
func (PriorityRouter) Resolve(model string, cands []Candidate) Decision {
	d := Decision{Model: model}
	now := time.Now()
	for _, c := range cands {
		if !c.CoolingUntil.IsZero() && c.CoolingUntil.After(now) {
			d.Filtered = append(d.Filtered, FilteredCandidate{Candidate: c, Reason: "cooling:" + c.CoolingReason})
			continue
		}
		d.Ordered = append(d.Ordered, c)
	}
	sort.Slice(d.Ordered, func(i, j int) bool {
		a, b := d.Ordered[i], d.Ordered[j]
		if a.ChannelPriority != b.ChannelPriority {
			return a.ChannelPriority < b.ChannelPriority
		}
		if a.KeyPriority != b.KeyPriority {
			return a.KeyPriority < b.KeyPriority
		}
		return a.KeyID < b.KeyID
	})
	return d
}
