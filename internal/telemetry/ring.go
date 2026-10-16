package telemetry

import (
	"sync"
	"time"
)

// RequestRecord 是一条已完成请求的记录，存入环形缓冲。
type RequestRecord struct {
	ID            string
	Model         string
	UpstreamModel string
	Account       string
	Channel       string // 渠道名（渠道化后 Account 语义 = 密钥名）
	ErrorClass    string
	StartedAt     time.Time
	Status        int
	DurationMs    int64
	FailoverCount int
	Streaming     bool
}

type Ring struct {
	mu    sync.Mutex
	items []RequestRecord
	cap   int
}

func NewRing(capacity int) *Ring {
	if capacity <= 0 {
		capacity = 100
	}
	return &Ring{cap: capacity}
}

func (r *Ring) Add(rec RequestRecord) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.items = append(r.items, rec)
	if len(r.items) > r.cap {
		r.items = r.items[len(r.items)-r.cap:]
	}
}

// List 返回新→旧的记录拷贝。
func (r *Ring) List() []RequestRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RequestRecord, len(r.items))
	for i, rec := range r.items {
		out[len(r.items)-1-i] = rec
	}
	return out
}
