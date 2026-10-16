package router

import (
	"errors"
	"sort"

	"AgentSwitch/internal/account"
)

var ErrNoCandidate = errors.New("no candidate account")

type Candidate struct {
	Account account.Account
	Skipped bool
	Reason  string
}

type Decision struct {
	Chosen     account.Account
	Candidates []Candidate
	Reason     string
}

type Router interface {
	Resolve(accounts []account.Account) (Decision, error)
}

type Single struct{}

// Resolve 过滤禁用账号，按 priority 升序、id 升序选出唯一候选（P0-B 换实现不换接口）。
// Candidates 先列排序后的启用账号，再附被跳过的禁用账号。
func (Single) Resolve(accounts []account.Account) (Decision, error) {
	var d Decision
	var ok []account.Account
	var skipped []Candidate
	for _, a := range accounts {
		if !a.Enabled {
			skipped = append(skipped, Candidate{Account: a, Skipped: true, Reason: "disabled"})
			continue
		}
		ok = append(ok, a)
	}
	sort.Slice(ok, func(i, j int) bool {
		if ok[i].Priority != ok[j].Priority {
			return ok[i].Priority < ok[j].Priority
		}
		return ok[i].ID < ok[j].ID
	})
	for _, a := range ok {
		d.Candidates = append(d.Candidates, Candidate{Account: a})
	}
	d.Candidates = append(d.Candidates, skipped...)
	if len(ok) == 0 {
		return d, ErrNoCandidate
	}
	d.Chosen = ok[0]
	d.Reason = "single-candidate: first enabled by priority"
	return d, nil
}
