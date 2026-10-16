package gateway

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"AgentSwitch/internal/account"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

// Limits 是网关数据面的三条硬限制（本包自定义，不复用 config.Limits）。
type Limits struct {
	MaxBodyBytes   int64
	MaxConcurrent  int
	RequestTimeout time.Duration
}

// Deps 是 Server 的全部外部依赖，由 Task 10 的 App 组装注入。
type Deps struct {
	Token          func() string
	Limits         func() Limits
	Accounts       func() ([]account.Account, error)
	Alias          func(public string) (upstream string, ok bool, err error)
	ListAliases    func() []string
	Cred           credential.Store
	Router         router.Router
	Adapter        provider.Adapter
	ProviderConfig func() (baseURL string, allowPrivate bool, err error)
	Log            *slog.Logger
	Ring           *telemetry.Ring
	OnRecord       func(telemetry.RequestRecord)
}

type Server struct {
	deps Deps
	mux  *http.ServeMux
	srv  *http.Server
	mu   sync.Mutex
	addr string
	sem  chan struct{}
}

func New(d Deps) *Server {
	s := &Server{deps: d, mux: http.NewServeMux()}
	// 并发上限信号量在构造时初始化：测试路径（Handler() 直用、不经 Start）也要有限额。
	if lim := d.Limits(); lim.MaxConcurrent > 0 {
		s.sem = make(chan struct{}, lim.MaxConcurrent)
	}
	s.mux.HandleFunc("POST /v1/messages", s.handleMessages)
	// 简报内部不一致：Produces 路由表写 GET /v1/models、其测试却请求 GET /models——两者都注册。
	s.mux.HandleFunc("GET /v1/models", s.handleModels)
	s.mux.HandleFunc("GET /models", s.handleModels)
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		w.Write([]byte("ok"))
	})
	s.mux.HandleFunc("GET /readyz", s.handleReadyz)
	return s
}

func (s *Server) Handler() http.Handler { return s }

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) Start(host string, port int) (string, error) {
	addr := fmt.Sprintf("%s:%d", host, port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	s.addr = ln.Addr().String()
	s.srv = &http.Server{Handler: s.mux}
	lim := s.deps.Limits()
	if lim.MaxConcurrent > 0 {
		s.sem = make(chan struct{}, lim.MaxConcurrent)
	}
	s.mu.Unlock()
	go s.srv.Serve(ln)
	return s.addr, nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

func (s *Server) Addr() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.addr
}

// authorized 校验 Authorization: Bearer <token>，比较用常量时间避免逐字节短路。
func (s *Server) authorized(r *http.Request) bool {
	tok := s.deps.Token()
	got := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if tok == "" || !strings.HasPrefix(got, prefix) {
		return false
	}
	return subtleConstantTimeCompare(got[len(prefix):], tok)
}
