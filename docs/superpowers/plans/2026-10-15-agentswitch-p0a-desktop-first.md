# AgentSwitch P0-A′ 桌面优先 MVP Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 交付单二进制 AgentSwitch 桌面应用：MyGo 原生 UI 壳 + SQLite 配置（供应商/账号/模型别名）+ Anthropic 协议本地模型网关，agent 指向 127.0.0.1 即可无感切换配置。

**Architecture:** 单进程：`App.Run()` 事件循环占主线程，网关 HTTP listener 为进程内 goroutine；管理面是 `mygo.Bind` 桥接服务（无管理端口）；配置存 SQLite 且网关每请求实时读取（改配置即时生效）；凭据主存 OS 密钥存储（`mygo.Secrets`），无密钥环环境显式降级到 0600 文件。关主窗口经 `OnWindowAllClosed` 拦截驻留托盘；显式退出经 `OnBeforeQuit` 钩子优雅停止网关。

**Tech Stack:** Go 1.27.2 · mygo v0.5.1（原生 UI）· modernc.org/sqlite（纯 Go 驱动，无 cgo）· log/slog

**Spec:** `docs/superpowers/specs/2026-10-15-agentswitch-p0a-desktop-first-design.md`（执行者须同时阅读；本计划的每个论据来自该 spec）

## Global Constraints

- Go 工具链：**1.27.2**（`GOTOOLCHAIN=auto` 已验证可经 goproxy.cn 自动下载）
- 依赖仅允许：`github.com/egoist/mygo v0.5.1`、`modernc.org/sqlite`（及其传递依赖）；不引入其他第三方库
- 网关只监听 **127.0.0.1**，host 不可配置（代码写死）
- 密钥本体永不写入 SQLite；日志/错误/UI 禁止出现明文密钥、Authorization、完整请求体
- 全部测试**离线**可跑（httptest Fake Provider），禁止触真实上游
- 非回环 `base_url` 强制 https；HTTP client 禁止跟随重定向；私网/回环上游需 `allow_private_upstream=true`
- module 名保持 `AgentSwitch`；入口包 `./cmd/agentswitch`（`mygo.json` 的 `"main"` 字段）
- 本计划对 spec 的一处落地修订：spec §9 的 `GatewayStateChanged`/`RequestCompleted` "事件" 在原生 UI 中以 `win.Update` 回调落地（mygo 事件仅送达 web 页面，原生视图用 `win.Update` 在主线程改状态，见 docs/ui/views.md）
- Linux 托盘依赖 `libayatana-appindicator3`，缺失时 `NewTray` 报错——必须容忍失败（记日志继续运行），不得让应用起不来

---

### Task 1: 迁移为 MyGo 原生应用骨架

**Files:**
- Modify: `go.mod`（重写）
- Delete: `main.go`（GoLand 模板）
- Create: `cmd/agentswitch/main.go`
- Create: `cmd/agentswitch/main_test.go`
- Create: `mygo.json`

**Interfaces:**
- Produces: 可运行的最小桌面壳（窗口显示 "AgentSwitch"）；后续所有任务在其上叠加

- [ ] **Step 1: 重写 go.mod**

```go
module AgentSwitch

go 1.27.2

tool github.com/egoist/mygo/cmd/mygo

require github.com/egoist/mygo v0.5.1
```

- [ ] **Step 2: 删除模板 main.go，创建入口与测试**

删除根目录 `main.go`。创建 `cmd/agentswitch/main.go`：

```go
package main

import (
	"log"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:  "AgentSwitch",
			Width:  1080,
			Height: 720,
			Content: ui.View(func(c *ui.Context) {
				ui.Column(c).Gap(12).Children(func() {
					ui.Text(c, "AgentSwitch")
				})
			}),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
```

创建 `cmd/agentswitch/main_test.go`：

```go
package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSplash(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Gap(12).Children(func() {
			ui.Text(c, "AgentSwitch")
		})
	}, 400, 300)
	if !tt.HasText("AgentSwitch") {
		t.Fatalf("texts %q missing title", tt.Texts())
	}
}
```

- [ ] **Step 3: 创建 mygo.json**

```json
{
  "name": "AgentSwitch",
  "identifier": "com.agentswitch.app",
  "version": "0.1.0",
  "main": "./cmd/agentswitch",
  "out": "build"
}
```

- [ ] **Step 4: 拉取依赖并构建**

Run: `go mod tidy && go build ./...`
Expected: 无错误（首次会经工具链缓存解析 mygo 依赖）

- [ ] **Step 5: 运行视图测试**

Run: `go test ./cmd/... -v`
Expected: `TestSplash` PASS

- [ ] **Step 6: Commit**

```bash
git add -A
git commit -m "feat: MyGo 原生应用骨架（cmd 入口 + mygo.json + 最小视图测试）"
```

---

### Task 2: telemetry — 结构化日志、脱敏、请求环形缓冲

**Files:**
- Create: `internal/telemetry/log.go`
- Create: `internal/telemetry/redact.go`
- Create: `internal/telemetry/ring.go`
- Test: `internal/telemetry/redact_test.go`
- Test: `internal/telemetry/ring_test.go`

**Interfaces:**
- Produces:
  - `func NewLogger(path string) (*slog.Logger, io.Closer, error)`
  - `func MaskToken(v string) string`
  - `func RedactHeaders(h http.Header) http.Header`
  - `type RequestRecord struct { ID, Model, UpstreamModel, Account, ErrorClass string; StartedAt time.Time; Status int; DurationMs int64; FailoverCount int; Streaming bool }`
  - `func NewRing(capacity int) *Ring`；`(*Ring).Add(RequestRecord)`；`(*Ring).List() []RequestRecord`（新→旧）

- [ ] **Step 1: 写脱敏测试（失败先行）**

`internal/telemetry/redact_test.go`：

```go
package telemetry

import (
	"net/http"
	"strings"
	"testing"
)

func TestMaskToken(t *testing.T) {
	got := MaskToken("Bearer sk-ant-abcdefghijklmnop")
	if strings.Contains(got, "efghijklmnop") {
		t.Fatalf("secret leaked: %q", got)
	}
	if !strings.HasPrefix(got, "Bearer sk-a") || !strings.HasSuffix(got, "***") {
		t.Fatalf("unexpected mask: %q", got)
	}
	if MaskToken("") != "" {
		t.Fatal("empty stays empty")
	}
}

func TestRedactHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret-token-value")
	h.Set("X-Api-Key", "sk-ant-topsecret")
	h.Set("Content-Type", "application/json")
	out := RedactHeaders(h)
	if strings.Contains(out.Get("Authorization"), "secret-token-value") ||
		strings.Contains(out.Get("X-Api-Key"), "topsecret") {
		t.Fatalf("leak: %v", out)
	}
	if out.Get("Content-Type") != "application/json" {
		t.Fatal("non-sensitive header must pass through")
	}
	if h.Get("Authorization") != "Bearer secret-token-value" {
		t.Fatal("must not mutate the input")
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/telemetry/ -run 'TestMask|TestRedact' -v`
Expected: FAIL（undefined: MaskToken）

- [ ] **Step 3: 实现 redact.go**

`internal/telemetry/redact.go`：

```go
package telemetry

import (
	"net/http"
	"strings"
)

var sensitiveHeaders = []string{"Authorization", "X-Api-Key", "Cookie", "Set-Cookie", "Proxy-Authorization"}

// MaskToken 保留非空前缀的前 4 个字符并追加 "***"，丢弃其余内容。
func MaskToken(v string) string {
	if v == "" {
		return ""
	}
	if len(v) <= 4 {
		return "***"
	}
	head := v
	if i := strings.IndexByte(v, ' '); i > 0 && i+1 < len(v) {
		head = v[:i+1] + v[i+1:min(i+5, len(v))]
	} else {
		head = v[:4]
	}
	return head + "***"
}

// RedactHeaders 返回拷贝，敏感头的值被掩码；输入不被修改。
func RedactHeaders(h http.Header) http.Header {
	out := h.Clone()
	for _, k := range sensitiveHeaders {
		if v := out.Get(k); v != "" {
			out.Set(k, MaskToken(v))
		}
	}
	return out
}
```

- [ ] **Step 4: 写 ring 测试**

`internal/telemetry/ring_test.go`：

```go
package telemetry

import "testing"

func TestRingWrapsAndNewestFirst(t *testing.T) {
	r := NewRing(3)
	for i := 1; i <= 5; i++ {
		r.Add(RequestRecord{ID: string(rune('a' + i)), Status: i})
	}
	got := r.List()
	if len(got) != 3 {
		t.Fatalf("len=%d want 3", len(got))
	}
	if got[0].ID != "f" || got[1].ID != "e" || got[2].ID != "d" {
		t.Fatalf("order wrong: %v %v %v", got[0].ID, got[1].ID, got[2].ID)
	}
}
```

- [ ] **Step 5: 运行确认失败后实现 ring.go 与 log.go**

`internal/telemetry/ring.go`：

```go
package telemetry

import "sync"

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
```

`internal/telemetry/log.go`：

```go
package telemetry

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

// NewLogger 返回写入 path 的 JSON slog.Logger 与关闭句柄。path 为空时写 stderr。
func NewLogger(path string) (*slog.Logger, io.Closer, error) {
	if path == "" {
		return slog.New(slog.NewJSONHandler(os.Stderr, nil)), io.NopCloser(nil), nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	return slog.New(slog.NewJSONHandler(f, nil)), f, nil
}
```

- [ ] **Step 6: 全部通过**

Run: `go test ./internal/telemetry/ -v`
Expected: 全部 PASS

- [ ] **Step 7: Commit**

```bash
git add internal/telemetry/
git commit -m "feat: telemetry（slog JSON、敏感头脱敏、请求环形缓冲）"
```

---

### Task 3: config — 默认值、settings 键与校验

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces:
  - `type Settings struct { Port int; RequestTimeoutSeconds int; ShutdownGraceSeconds int; MaxBodyBytes int64; MaxConcurrent int; GatewayToken string; CredentialFallback bool }`
  - `func Default() Settings`
  - `func Load(get func(key string) (string, error)) (Settings, error)`（缺失键用默认值；`storage.ErrNotFound` 由调用方适配——本包用 `get` 返回 `("", ErrMissing)` 约定：返回空串+nil 视为缺失）
  - `func (s Settings) Save(set func(key, value string) error) error`
  - 常量：`KeyPort = "server.port"`、`KeyTimeout = "server.request_timeout_seconds"`、`KeyGrace = "server.shutdown_grace_seconds"`、`KeyMaxBody = "server.max_body_bytes"`、`KeyMaxConc = "server.max_concurrent"`、`KeyToken = "auth.gateway_token"`、`KeyCredFallback = "credential.fallback_enabled"`

- [ ] **Step 1: 失败先行测试**

`internal/config/config_test.go`：

```go
package config

import (
	"strings"
	"testing"
)

func TestDefaultIsValid(t *testing.T) {
	s := Default()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Port != 17890 || s.RequestTimeoutSeconds != 120 || s.ShutdownGraceSeconds != 15 {
		t.Fatalf("defaults drifted: %+v", s)
	}
	if s.MaxBodyBytes != 10<<20 || s.MaxConcurrent != 32 {
		t.Fatalf("limits drifted: %+v", s)
	}
}

func TestValidateRejectsBadPort(t *testing.T) {
	s := Default()
	s.Port = 0
	if err := s.Validate(); err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("want port error, got %v", err)
	}
}

func TestLoadUsesDefaultsAndOverrides(t *testing.T) {
	vals := map[string]string{KeyPort: "18000", KeyCredFallback: "true"}
	get := func(k string) (string, error) { return vals[k], nil }
	s, err := Load(get)
	if err != nil {
		t.Fatal(err)
	}
	if s.Port != 18000 || !s.CredentialFallback || s.RequestTimeoutSeconds != 120 {
		t.Fatalf("load wrong: %+v", s)
	}
}
```

- [ ] **Step 2: 确认失败**

Run: `go test ./internal/config/ -v`
Expected: FAIL（undefined: Default）

- [ ] **Step 3: 实现**

`internal/config/config.go`：

```go
package config

import (
	"fmt"
	"strconv"
)

const (
	KeyPort         = "server.port"
	KeyTimeout      = "server.request_timeout_seconds"
	KeyGrace        = "server.shutdown_grace_seconds"
	KeyMaxBody      = "server.max_body_bytes"
	KeyMaxConc      = "server.max_concurrent"
	KeyToken        = "auth.gateway_token"
	KeyCredFallback = "credential.fallback_enabled"
)

type Settings struct {
	Port                 int
	RequestTimeoutSeconds int
	ShutdownGraceSeconds int
	MaxBodyBytes         int64
	MaxConcurrent        int
	GatewayToken         string
	CredentialFallback   bool
}

func Default() Settings {
	return Settings{
		Port: 17890, RequestTimeoutSeconds: 120, ShutdownGraceSeconds: 15,
		MaxBodyBytes: 10 << 20, MaxConcurrent: 32,
	}
}

func (s Settings) Validate() error {
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("server.port out of range: %d", s.Port)
	}
	if s.RequestTimeoutSeconds <= 0 {
		return fmt.Errorf("server.request_timeout_seconds must be > 0")
	}
	if s.ShutdownGraceSeconds < 0 {
		return fmt.Errorf("server.shutdown_grace_seconds must be >= 0")
	}
	if s.MaxBodyBytes <= 0 || s.MaxConcurrent <= 0 {
		return fmt.Errorf("limits must be > 0")
	}
	return nil
}

// Load 从 get 读键；空串视为缺失用默认值。
func Load(get func(key string) (string, error)) (Settings, error) {
	s := Default()
	int64s := []struct {
		key string
		dst *int64
	}{{KeyPort, newInt64(&s.Port)}, {KeyTimeout, newInt64(&s.RequestTimeoutSeconds)},
		{KeyGrace, newInt64(&s.ShutdownGraceSeconds)}, {KeyMaxBody, &s.MaxBodyBytes},
		{KeyMaxConc, newInt64(&s.MaxConcurrent)}}
	for _, e := range int64s {
		v, err := get(e.key)
		if err != nil {
			return s, fmt.Errorf("read %s: %w", e.key, err)
		}
		if v == "" {
			continue
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return s, fmt.Errorf("parse %s: %w", e.key, err)
		}
		*e.dst = n
	}
	if v, err := get(KeyToken); err != nil {
		return s, err
	} else {
		s.GatewayToken = v
	}
	if v, err := get(KeyCredFallback); err != nil {
		return s, err
	} else {
		s.CredentialFallback = v == "true"
	}
	return s, s.Validate()
}

func (s Settings) Save(set func(key, value string) error) error {
	if err := s.Validate(); err != nil {
		return err
	}
	pairs := map[string]string{
		KeyPort: strconv.Itoa(s.Port), KeyTimeout: strconv.Itoa(s.RequestTimeoutSeconds),
		KeyGrace: strconv.Itoa(s.ShutdownGraceSeconds), KeyMaxBody: strconv.FormatInt(s.MaxBodyBytes, 10),
		KeyMaxConc: strconv.Itoa(s.MaxConcurrent), KeyToken: s.GatewayToken,
		KeyCredFallback: strconv.FormatBool(s.CredentialFallback),
	}
	for k, v := range pairs {
		if err := set(k, v); err != nil {
			return fmt.Errorf("write %s: %w", k, err)
		}
	}
	return nil
}
```

辅助函数（放同文件）：

```go
// newInt64 让 int 字段也能复用 int64 解析循环。
func newInt64(dst *int) *int64 {
	var buf int64
	_ = dst
	_ = buf
	return nil // 见 Step 3.1 的修正说明
}
```

**Step 3.1 修正**：上面的 `newInt64` 技巧不成立，删除它与 `int64s` 循环，改为显式逐键解析（保持其余不变）：

```go
func Load(get func(key string) (string, error)) (Settings, error) {
	s := Default()
	readInt := func(key string, dst *int) error {
		v, err := get(key)
		if err != nil || v == "" {
			return err
		}
		n, perr := strconv.Atoi(v)
		if perr != nil {
			return fmt.Errorf("parse %s: %w", key, perr)
		}
		*dst = n
		return nil
	}
	if err := readInt(KeyPort, &s.Port); err != nil {
		return s, err
	}
	if err := readInt(KeyTimeout, &s.RequestTimeoutSeconds); err != nil {
		return s, err
	}
	if err := readInt(KeyGrace, &s.ShutdownGraceSeconds); err != nil {
		return s, err
	}
	if v, err := get(KeyMaxBody); err != nil {
		return s, err
	} else if v != "" {
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return s, fmt.Errorf("parse %s: %w", KeyMaxBody, perr)
		}
		s.MaxBodyBytes = n
	}
	if err := readInt(KeyMaxConc, &s.MaxConcurrent); err != nil {
		return s, err
	}
	if v, err := get(KeyToken); err != nil {
		return s, err
	} else {
		s.GatewayToken = v
	}
	if v, err := get(KeyCredFallback); err != nil {
		return s, err
	} else {
		s.CredentialFallback = v == "true"
	}
	return s, s.Validate()
}
```

- [ ] **Step 4: 通过**

Run: `go test ./internal/config/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat: config 默认值、settings 键与校验"
```

---

### Task 4: storage — SQLite、migrations、Repositories

**Files:**
- Create: `internal/storage/storage.go`（类型与接口）
- Create: `internal/storage/db.go`（Open、migration）
- Create: `internal/storage/repos.go`（各 Repo 实现）
- Test: `internal/storage/storage_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `var ErrNotFound = errors.New("not found")`
  - `type Provider struct { ID, Name, Protocol, BaseURL string; Enabled, AllowPrivateUpstream bool; CreatedAt, UpdatedAt time.Time }`
  - `type Account struct { ID, ProviderID, Name, SecretKey, SecretBackend string; Priority int; Enabled bool; CreatedAt, UpdatedAt time.Time }`
  - `type ModelMapping struct { ID, PublicModel, UpstreamModel string; Enabled bool; CreatedAt, UpdatedAt time.Time }`
  - `type Store interface { Providers() ProviderRepo; Accounts() AccountRepo; Models() ModelRepo; Settings() SettingsRepo; Close() error }`
  - `type ProviderRepo interface { List() ([]Provider, error); Get(id string) (Provider, error); Save(p Provider) error; Delete(id string) error }`
  - `type AccountRepo interface { List() ([]Account, error); ListByProvider(pid string) ([]Account, error); Get(id string) (Account, error); Save(a Account) error; Delete(id string) error; SetEnabled(id string, on bool) error }`
  - `type ModelRepo interface { List() ([]ModelMapping, error); GetByPublic(public string) (ModelMapping, error); Save(m ModelMapping) error; Delete(id string) error }`
  - `type SettingsRepo interface { Get(key string) (string, error); Set(key, value string) error }`
  - `func Open(path string) (Store, error)`（WAL、`foreign_keys=ON`、文件 0600、migration 幂等）
  - `func NewID() string`（crypto/rand 16 字节 hex）

- [ ] **Step 1: 失败先行测试**

`internal/storage/storage_test.go`：

```go
package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func tempStore(t *testing.T) Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrationIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "m.db")
	s1, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s1.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s2.Close()
}

func TestProviderCRUD(t *testing.T) {
	s := tempStore(t)
	p := Provider{ID: NewID(), Name: "p1", Protocol: "anthropic", BaseURL: "https://api.example.com", Enabled: true}
	if err := s.Providers().Save(p); err != nil {
		t.Fatal(err)
	}
	got, err := s.Providers().Get(p.ID)
	if err != nil || got.BaseURL != p.BaseURL {
		t.Fatalf("get: %+v %v", got, err)
	}
	got.BaseURL = "https://api2.example.com"
	if err := s.Providers().Save(got); err != nil {
		t.Fatal(err)
	}
	if g2, _ := s.Providers().Get(p.ID); g2.BaseURL != "https://api2.example.com" {
		t.Fatal("update not persisted")
	}
	if err := s.Providers().Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Providers().Get(p.ID); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestAccountCascadeAndSettings(t *testing.T) {
	s := tempStore(t)
	p := Provider{ID: NewID(), Name: "p", Protocol: "anthropic", BaseURL: "https://x.example.com", Enabled: true}
	s.Providers().Save(p)
	a := Account{ID: NewID(), ProviderID: p.ID, Name: "a1", SecretKey: "account:k1", SecretBackend: "os-keychain", Priority: 10, Enabled: true, CreatedAt: time.Now()}
	if err := s.Accounts().Save(a); err != nil {
		t.Fatal(err)
	}
	if err := s.Accounts().SetEnabled(a.ID, false); err != nil {
		t.Fatal(err)
	}
	if g, _ := s.Accounts().Get(a.ID); g.Enabled {
		t.Fatal("SetEnabled failed")
	}
	if err := s.Providers().Delete(p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accounts().Get(a.ID); err != ErrNotFound {
		t.Fatalf("cascade delete missing: %v", err)
	}
	if err := s.Settings().Set("k", "v"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.Settings().Get("k"); err != nil || v != "v" {
		t.Fatalf("settings: %q %v", v, err)
	}
}

func TestModelUniquePublic(t *testing.T) {
	s := tempStore(t)
	m := ModelMapping{ID: NewID(), PublicModel: "claude-opus", UpstreamModel: "up-opus", Enabled: true}
	if err := s.Models().Save(m); err != nil {
		t.Fatal(err)
	}
	m2 := ModelMapping{ID: NewID(), PublicModel: "claude-opus", UpstreamModel: "other", Enabled: true}
	if err := s.Models().Save(m2); err == nil {
		t.Fatal("duplicate public_model must fail")
	}
	if g, err := s.Models().GetByPublic("claude-opus"); err != nil || g.UpstreamModel != "up-opus" {
		t.Fatalf("getByPublic: %+v %v", g, err)
	}
	if _, err := s.Models().GetByPublic("nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: 确认失败**

Run: `go test ./internal/storage/ -v`
Expected: FAIL（包不存在）

- [ ] **Step 3: 实现**

`internal/storage/storage.go`：按 Interfaces 块逐一声明类型、接口与 `ErrNotFound`（纯声明，无逻辑）。

`internal/storage/db.go`：

```go
package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"

	_ "modernc.org/sqlite"
)

const migration001 = `
CREATE TABLE providers (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, protocol TEXT NOT NULL,
  base_url TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  allow_private_upstream INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE accounts (
  id TEXT PRIMARY KEY,
  provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  name TEXT NOT NULL, secret_key TEXT NOT NULL, secret_backend TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 10, enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX idx_accounts_provider ON accounts(provider_id);
CREATE TABLE models (
  id TEXT PRIMARY KEY, public_model TEXT NOT NULL UNIQUE,
  upstream_model TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

type dbStore struct{ db *sql.DB }

func Open(path string) (Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &dbStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=1`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(migration001); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (1)`); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func NewID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *dbStore) Providers() ProviderRepo { return &providerRepo{s.db} }
func (s *dbStore) Accounts() AccountRepo   { return &accountRepo{s.db} }
func (s *dbStore) Models() ModelRepo       { return &modelRepo{s.db} }
func (s *dbStore) Settings() SettingsRepo  { return &settingsRepo{s.db} }
func (s *dbStore) Close() error            { return s.db.Close() }
```

`internal/storage/repos.go`：实现四个 Repo。要点（完整实现按此编写）：

```go
package storage

import (
	"database/sql"
	"errors"
	"time"
)

func mapNotFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

type providerRepo struct{ db *sql.DB }

func (r *providerRepo) List() ([]Provider, error) {
	rows, err := r.db.Query(`SELECT id,name,protocol,base_url,enabled,allow_private_upstream,created_at,updated_at FROM providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		var en, ap int
		var ca, ua string
		if err := rows.Scan(&p.ID, &p.Name, &p.Protocol, &p.BaseURL, &en, &ap, &ca, &ua); err != nil {
			return nil, err
		}
		p.Enabled, p.AllowPrivateUpstream = en == 1, ap == 1
		p.CreatedAt, _ = time.Parse(time.RFC3339, ca)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *providerRepo) Get(id string) (Provider, error) {
	var p Provider
	var en, ap int
	var ca, ua string
	err := r.db.QueryRow(`SELECT id,name,protocol,base_url,enabled,allow_private_upstream,created_at,updated_at FROM providers WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.Protocol, &p.BaseURL, &en, &ap, &ca, &ua)
	if err != nil {
		return Provider{}, mapNotFound(err)
	}
	p.Enabled, p.AllowPrivateUpstream = en == 1, ap == 1
	p.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return p, nil
}

func (r *providerRepo) Save(p Provider) error {
	now := time.Now().Format(time.RFC3339)
	if p.ID == "" {
		p.ID = NewID()
	}
	existing, err := r.Get(p.ID)
	if err == nil {
		p.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	p.UpdatedAt = time.Now()
	_, err = r.db.Exec(`INSERT INTO providers(id,name,protocol,base_url,enabled,allow_private_upstream,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, base_url=excluded.base_url,
			enabled=excluded.enabled, allow_private_upstream=excluded.allow_private_upstream, updated_at=excluded.updated_at`,
		p.ID, p.Name, p.Protocol, p.BaseURL, boolInt(p.Enabled), boolInt(p.AllowPrivateUpstream),
		p.CreatedAt.Format(time.RFC3339), p.UpdatedAt.Format(time.RFC3339))
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (r *providerRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM providers WHERE id=?`, id)
	return err
}

// accountRepo / modelRepo / settingsRepo 按同样的模式实现：
// accountRepo: List/ListByProvider/Get/Save/Delete/SetEnabled（SET enabled=?）；
//   Save 同样的 upsert + created_at 保留逻辑；删除依赖 FK ON DELETE CASCADE。
// modelRepo: List（ORDER BY public_model）/GetByPublic/Save/Delete；
//   Save 的 upsert 冲突（public_model UNIQUE）把驱动错误冒泡为非 nil error 即可。
// settingsRepo:
func (r *settingsRepo) Get(key string) (string, error) {
	var v string
	err := r.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil // 缺失 = 空串，config.Load 视为默认值
		}
		return "", err
	}
	return v, nil
}

func (r *settingsRepo) Set(key, value string) error {
	_, err := r.db.Exec(`INSERT INTO settings(key,value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}
```

accountRepo/modelRepo 与 providerRepo 同构，逐字段照写（列名见 migration001；`accountRepo.Save` 保留 CreatedAt 逻辑相同；`ModelRepo.Save` 无 enabled 之外的更新差异）。

- [ ] **Step 4: 通过**

Run: `go mod tidy && go test ./internal/storage/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/storage/ go.mod go.sum
git commit -m "feat: storage（SQLite WAL、migration 001、四表 Repository）"
```

---

### Task 5: credential — Store 接口、OS 密钥存储、文件降级

**Files:**
- Create: `internal/credential/credential.go`
- Create: `internal/credential/fallback.go`
- Create: `internal/credential/oskeychain.go`
- Test: `internal/credential/credential_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - `var ErrNotFound = errors.New("secret not found")`；`var ErrUnavailable = errors.New("credential store unavailable")`
  - `type Store interface { Set(key string, secret []byte) error; Get(key string) ([]byte, error); Delete(key string) error }`
  - `func AccountKey(accountID string) string`（返回 `"account:"+accountID`）
  - `func NewFallbackFile(path string) *FallbackFile`（JSON map，0600）
  - `type OSKeychain struct{}`（包装 `mygo.Secrets`；`mygo.ErrSecretNotFound` → `ErrNotFound`；`errors.ErrUnsupported` → `ErrUnavailable`）
  - `func OSKeychainAvailable() bool`
  - `type Manager struct { Primary, Fallback Store; AllowFallback bool }`（实现 Store；Primary 返回 `ErrUnavailable` 且 AllowFallback 时走 Fallback）

- [ ] **Step 1: 失败先行测试**

`internal/credential/credential_test.go`：

```go
package credential

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAccountKey(t *testing.T) {
	if AccountKey("abc") != "account:abc" {
		t.Fatal(AccountKey("abc"))
	}
}

func TestFallbackRoundTripAndMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secrets.json")
	f := NewFallbackFile(path)
	if err := f.Set("account:1", []byte("sk-secret")); err != nil {
		t.Fatal(err)
	}
	got, err := f.Get("account:1")
	if err != nil || string(got) != "sk-secret" {
		t.Fatalf("get: %q %v", got, err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v want 0600", st.Mode().Perm())
	}
	if err := f.Delete("account:1"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Get("account:1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

type fakeStore struct{ m map[string][]byte; unavailable bool }

func (f *fakeStore) Set(k string, v []byte) error {
	if f.unavailable {
		return ErrUnavailable
	}
	if f.m == nil {
		f.m = map[string][]byte{}
	}
	f.m[k] = v
	return nil
}
func (f *fakeStore) Get(k string) ([]byte, error) {
	if f.unavailable {
		return nil, ErrUnavailable
	}
	v, ok := f.m[k]
	if !ok {
		return nil, ErrNotFound
	}
	return v, nil
}
func (f *fakeStore) Delete(k string) error {
	delete(f.m, k)
	return nil
}

func TestManagerFallsBackWhenAllowed(t *testing.T) {
	primary := &fakeStore{unavailable: true}
	fb := NewFallbackFile(filepath.Join(t.TempDir(), "fb.json"))
	m := Manager{Primary: primary, Fallback: fb, AllowFallback: true}
	if err := m.Set("k", []byte("v")); err != nil {
		t.Fatal(err)
	}
	if got, err := m.Get("k"); err != nil || string(got) != "v" {
		t.Fatalf("fallback get: %q %v", got, err)
	}
}

func TestManagerRefusesFallbackWhenNotAllowed(t *testing.T) {
	primary := &fakeStore{unavailable: true}
	fb := NewFallbackFile(filepath.Join(t.TempDir(), "fb.json"))
	m := Manager{Primary: primary, Fallback: fb, AllowFallback: false }
	if err := m.Set("k", []byte("v")); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}
```

- [ ] **Step 2: 确认失败** → Run: `go test ./internal/credential/ -v` → FAIL

- [ ] **Step 3: 实现**

`internal/credential/credential.go`：`ErrNotFound`、`ErrUnavailable`、`Store` 接口、`AccountKey`、`Manager`（Set/Get：Primary 不可用且 AllowFallback 时用 Fallback；Delete：两边都删，容忍 ErrNotFound）。

`internal/credential/fallback.go`：

```go
package credential

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type FallbackFile struct {
	path string
	mu   sync.Mutex
}

func NewFallbackFile(path string) *FallbackFile { return &FallbackFile{path: path} }

func (f *FallbackFile) load() (map[string]string, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (f *FallbackFile) flush(m map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(f.path, b, 0o600)
}

func (f *FallbackFile) Set(key string, secret []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	m[key] = string(secret)
	return f.flush(m)
}

func (f *FallbackFile) Get(key string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return nil, err
	}
	v, ok := m[key]
	if !ok {
		return nil, ErrNotFound
	}
	return []byte(v), nil
}

func (f *FallbackFile) Delete(key string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, err := f.load()
	if err != nil {
		return err
	}
	if _, ok := m[key]; !ok {
		return ErrNotFound
	}
	delete(m, key)
	return f.flush(m)
}
```

`internal/credential/oskeychain.go`：

```go
package credential

import (
	"errors"

	"github.com/egoist/mygo"
)

type OSKeychain struct{}

func (OSKeychain) Set(key string, secret []byte) error {
	if err := mygo.Secrets.Set(key, secret); err != nil {
		return wrapOSErr(err)
	}
	return nil
}

func (OSKeychain) Get(key string) ([]byte, error) {
	v, err := mygo.Secrets.Get(key)
	if err != nil {
		return nil, wrapOSErr(err)
	}
	return v, nil
}

func (OSKeychain) Delete(key string) error {
	if err := mygo.Secrets.Delete(key); err != nil {
		return wrapOSErr(err)
	}
	return nil
}

func wrapOSErr(err error) error {
	if errors.Is(err, mygo.ErrSecretNotFound) {
		return ErrNotFound
	}
	if errors.Is(err, errors.ErrUnsupported) {
		return ErrUnavailable
	}
	return err
}

// OSKeychainAvailable 探测当前环境是否有可用的系统密钥存储。
func OSKeychainAvailable() bool {
	err := OSKeychain{}.Set("agentswitch.probe", []byte("1"))
	if err == nil {
		OSKeychain{}.Delete("agentswitch.probe")
		return true
	}
	return !errors.Is(err, ErrUnavailable)
}
```

- [ ] **Step 4: 通过** → Run: `go mod tidy && go test ./internal/credential/ -v` → 全部 PASS（本机无密钥环时 OSKeychain 相关行为不影响以上四个测试）

- [ ] **Step 5: Commit**

```bash
git add internal/credential/
git commit -m "feat: credential（Store 接口、OSKeychain、0600 文件降级、Manager）"
```

---

### Task 6: provider — Adapter 接口、Anthropic 实现、错误分类、上游安全

**Files:**
- Create: `internal/account/account.go`
- Create: `internal/provider/provider.go`
- Create: `internal/provider/anthropic.go`
- Create: `internal/provider/classify.go`
- Test: `internal/provider/provider_test.go`

**Interfaces:**
- Produces:
  - `account.Account struct { ID, ProviderID, Name, SecretKey, SecretBackend string; Priority int; Enabled bool }`
  - `provider.DoRequest struct { BaseURL string; AllowPrivate bool; APIKey, UpstreamModel string; RawBody []byte; Stream bool }`
  - `provider.ChatResult struct { StatusCode int; Header http.Header; Body io.ReadCloser; RetryAfterSec int }`
  - `provider.Adapter interface { Do(ctx context.Context, r DoRequest) (ChatResult, error) }`
  - `func NewAnthropicAdapter() *AnthropicAdapter`
  - `type ErrorClass string`：`ClassQuota/ClassRateLimit/ClassAuth/ClassServer/ClassParam/ClassConnect/ClassUnknown`
  - `func Classify(status, retryAfterSec int, body []byte) ErrorClass`
  - `func ValidateBaseURL(raw string, allowPrivate bool) error`
  - `type ProbeResult struct { Stage, Error string; OK bool }`；`func Probe(ctx context.Context, baseURL, apiKey string, allowPrivate bool) ProbeResult`

- [ ] **Step 1: 失败先行测试**

`internal/provider/provider_test.go`：

```go
package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestValidateBaseURL(t *testing.T) {
	cases := []struct {
		url    string
		private bool
		wantErr bool
	}{
		{"https://api.anthropic.com", false, false},
		{"http://api.anthropic.com", false, true},
		{"http://127.0.0.1:9999", false, false}, // 回环显式放行（测试 Fake Provider 需要）
		{"http://192.168.1.5:8080", false, true},
		{"http://192.168.1.5:8080", true, false},
		{"ftp://x.example.com", false, true},
		{"https://user:pass@x.example.com", false, true},
	}
	for _, c := range cases {
		err := ValidateBaseURL(c.url, c.private)
		if (err != nil) != c.wantErr {
			t.Errorf("%s private=%v err=%v wantErr=%v", c.url, c.private, err, c.wantErr)
		}
	}
}

func TestClassifyConservative(t *testing.T) {
	if Classify(429, 7, nil) != ClassRateLimit {
		t.Fatal("429")
	}
	if Classify(429, 0, []byte(`{"error":{"type":"quota_exceeded"}}`)) != ClassQuota {
		t.Fatal("429+quota marker")
	}
	if Classify(403, 0, []byte(`insufficient credit`)) != ClassQuota {
		t.Fatal("403+quota marker")
	}
	if Classify(401, 0, nil) != ClassAuth {
		t.Fatal("401")
	}
	if Classify(500, 0, nil) != ClassServer {
		t.Fatal("500")
	}
	if Classify(400, 0, nil) != ClassParam {
		t.Fatal("400")
	}
	if Classify(404, 0, nil) != ClassParam {
		t.Fatal("404→param（保守，不转移）")
	}
}

func TestAnthropicAdapterHeadersAndNoLocalTokenLeak(t *testing.T) {
	var gotKey, gotVer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		gotVer = r.Header.Get("Anthropic-Version")
		if r.URL.Path != "/v1/messages" {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1"}`))
	}))
	defer srv.Close()
	a := NewAnthropicAdapter()
	res, err := a.Do(context.Background(), DoRequest{
		BaseURL: srv.URL, AllowPrivate: true, APIKey: "sk-upstream",
		UpstreamModel: "up-opus", RawBody: []byte(`{"model":"public-alias","messages":[]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	if string(b) != `{"id":"msg_1"}` {
		t.Fatalf("body %s", b)
	}
	if gotKey != "sk-upstream" || gotVer == "" {
		t.Fatalf("auth headers wrong: key=%q ver=%q", gotKey, gotVer)
	}
}

func TestAdapterDeniesRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://elsewhere.example.com/", http.StatusFound)
	}))
	defer srv.Close()
	a := NewAnthropicAdapter()
	_, err := a.Do(context.Background(), DoRequest{BaseURL: srv.URL, AllowPrivate: true, APIKey: "k", RawBody: []byte(`{}`)})
	if err == nil {
		t.Fatal("redirect must be denied")
	}
}

func TestProbeStages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"type":"authentication_error"}}`))
	}))
	defer srv.Close()
	res := Probe(context.Background(), srv.URL, "bad-key", true)
	if res.OK || res.Stage != "auth" {
		t.Fatalf("probe: %+v", res)
	}
	if strings.Contains(res.Error, "bad-key") {
		t.Fatalf("probe leaked key: %q", res.Error)
	}
}
```

- [ ] **Step 2: 确认失败** → Run: `go test ./internal/provider/ -v` → FAIL（包不存在）

- [ ] **Step 3: 实现**

`internal/account/account.go`：

```go
package account

type Account struct {
	ID, ProviderID, Name   string
	SecretKey, SecretBackend string
	Priority               int
	Enabled                bool
}
```

`internal/provider/provider.go`：`DoRequest`、`ChatResult`、`Adapter` 接口声明（同 Interfaces 块）。

`internal/provider/classify.go`：

```go
package provider

import "strings"

type ErrorClass string

const (
	ClassQuota      ErrorClass = "quota_exhausted"
	ClassRateLimit  ErrorClass = "rate_limited"
	ClassAuth       ErrorClass = "auth"
	ClassServer     ErrorClass = "server"
	ClassParam      ErrorClass = "param"
	ClassUnknown    ErrorClass = "unknown"
)

// Classify 保守分类：只有状态码与响应体标记同时支持时才判额度耗尽。
func Classify(status, retryAfterSec int, body []byte) ErrorClass {
	lower := strings.ToLower(string(body))
	quotaMarked := strings.Contains(lower, "quota") || strings.Contains(lower, "insufficient credit")
	switch {
	case status == 429:
		if quotaMarked {
			return ClassQuota
		}
		return ClassRateLimit
	case status == 401 || status == 403:
		if quotaMarked {
			return ClassQuota
		}
		return ClassAuth
	case status >= 500:
		return ClassServer
	case status >= 400:
		return ClassParam
	default:
		return ClassUnknown
	}
}
```

（`retryAfterSec` 参数保留供调用方读取 Retry-After 后使用，分类本身不依赖；未使用则以 `_ = retryAfterSec` 避免告警。）

`internal/provider/anthropic.go`：

```go
package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func ValidateBaseURL(raw string, allowPrivate bool) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid base_url: %w", err)
	}
	if u.User != nil {
		return errors.New("base_url must not contain userinfo")
	}
	loopback := isLoopbackHost(u.Hostname())
	switch u.Scheme {
	case "https":
	case "http":
		if !loopback && !allowPrivate {
			return errors.New("http base_url requires loopback or allow_private_upstream")
		}
	default:
		return fmt.Errorf("base_url scheme %q not allowed", u.Scheme)
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type AnthropicAdapter struct{ Client *http.Client }

func NewAnthropicAdapter() *AnthropicAdapter {
	return &AnthropicAdapter{Client: &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse // 不跟随：3xx 原样交给分类
		},
	}}
}

func (a *AnthropicAdapter) Do(ctx context.Context, r DoRequest) (ChatResult, error) {
	if err := ValidateBaseURL(r.BaseURL, r.AllowPrivate); err != nil {
		return ChatResult{}, err
	}
	endpoint := strings.TrimRight(r.BaseURL, "/") + "/v1/messages"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(r.RawBody))
	if err != nil {
		return ChatResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Api-Key", r.APIKey)
	req.Header.Set("Anthropic-Version", "2023-06-01")
	if r.Stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return ChatResult{}, fmt.Errorf("connect: %w", err)
	}
	ra := 0
	if v := resp.Header.Get("Retry-After"); v != "" {
		if n, perr := strconv.Atoi(v); perr == nil {
			ra = n
		}
	}
	if resp.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		resp.Body.Close()
		return ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, RetryAfterSec: ra,
			Body: io.NopCloser(bytes.NewReader(snippet))}, nil
	}
	return ChatResult{StatusCode: resp.StatusCode, Header: resp.Header, Body: resp.Body, RetryAfterSec: ra}, nil
}

type ProbeResult struct {
	Stage string `json:"stage"`
	Error string `json:"error,omitempty"`
	OK    bool   `json:"ok"`
}

// Probe 分级探测：dns → connect → auth（发一条最小请求，只报告分级与脱敏错误）。
func Probe(ctx context.Context, baseURL, apiKey string, allowPrivate bool) ProbeResult {
	if err := ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return ProbeResult{Stage: "validate", Error: err.Error()}
	}
	u, _ := url.Parse(baseURL)
	host := u.Hostname()
	if _, err := net.DefaultResolver.LookupHost(ctx, host); err != nil && !isLoopbackHost(host) {
		return ProbeResult{Stage: "dns", Error: fmt.Sprintf("resolve %s failed", host)}
	}
	a := NewAnthropicAdapter()
	body := []byte(`{"model":"__probe__","max_tokens":1,"messages":[{"role":"user","content":"ping"}]}`)
	res, err := a.Do(ctx, DoRequest{BaseURL: baseURL, AllowPrivate: allowPrivate, APIKey: apiKey, RawBody: body})
	if err != nil {
		return ProbeResult{Stage: "connect", Error: MaskErr(err)}
	}
	defer res.Body.Close()
	io.Copy(io.Discard, io.LimitReader(res.Body, 4<<10))
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return ProbeResult{Stage: "auth", Error: fmt.Sprintf("upstream status %d", res.StatusCode)}
	}
	return ProbeResult{Stage: "auth", OK: true}
}

// MaskErr 去掉错误文本中可能出现的密钥片段。
func MaskErr(err error) string {
	s := err.Error()
	if i := strings.Index(s, "sk-"); i >= 0 {
		return s[:i] + "sk-***"
	}
	return s
}
```

- [ ] **Step 4: 通过** → Run: `go test ./internal/provider/ ./internal/account/ -v` → 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/provider/ internal/account/
git commit -m "feat: provider（AnthropicAdapter、保守错误分类、base_url 安全校验、Probe）"
```

---

### Task 7: protocol — Anthropic 入站解析与错误 envelope

**Files:**
- Create: `internal/protocol/anthropic.go`
- Test: `internal/protocol/anthropic_test.go`

**Interfaces:**
- Produces:
  - `type ChatRequest struct { Model string; Stream bool }`
  - `func ParseChatRequest(body []byte) (ChatRequest, error)`
  - `func RewriteModel(body []byte, upstream string) ([]byte, error)`（保留其他字段）
  - `func ErrorEnvelope(errType, message string) []byte`
  - 常量：`ErrTypeInvalidRequest = "invalid_request_error"`、`ErrTypeNotFound = "not_found_error"`、`ErrTypeAPI = "api_error"`

- [ ] **Step 1: 失败先行测试**

`internal/protocol/anthropic_test.go`：

```go
package protocol

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseChatRequest(t *testing.T) {
	cr, err := ParseChatRequest([]byte(`{"model":"claude-opus","stream":true,"messages":[]}`))
	if err != nil || cr.Model != "claude-opus" || !cr.Stream {
		t.Fatalf("%+v %v", cr, err)
	}
	if _, err := ParseChatRequest([]byte(`{"stream":true}`)); err == nil {
		t.Fatal("missing model must fail")
	}
	if _, err := ParseChatRequest([]byte(`not-json`)); err == nil {
		t.Fatal("bad json must fail")
	}
}

func TestRewriteModelKeepsFields(t *testing.T) {
	out, err := RewriteModel([]byte(`{"model":"public","stream":false,"messages":[{"role":"user"}]}`), "up-opus")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(out, &m); err != nil {
		t.Fatal(err)
	}
	if m["model"] != "up-opus" {
		t.Fatalf("model %v", m["model"])
	}
	if _, ok := m["messages"]; !ok {
		t.Fatal("messages dropped")
	}
}

func TestErrorEnvelopeShape(t *testing.T) {
	b := ErrorEnvelope(ErrTypeNotFound, "unknown model")
	var m map[string]map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if m["type"] != "error" || m["error"]["type"] != ErrTypeNotFound {
		t.Fatalf("envelope: %s", b)
	}
	if !strings.Contains(string(b), "unknown model") {
		t.Fatal("message missing")
	}
}
```

- [ ] **Step 2: 确认失败** → Run: `go test ./internal/protocol/ -v` → FAIL

- [ ] **Step 3: 实现**

`internal/protocol/anthropic.go`：

```go
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
)

const (
	ErrTypeInvalidRequest = "invalid_request_error"
	ErrTypeNotFound       = "not_found_error"
	ErrTypeAPI            = "api_error"
)

type ChatRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

func ParseChatRequest(body []byte) (ChatRequest, error) {
	var cr ChatRequest
	if err := json.Unmarshal(body, &cr); err != nil {
		return ChatRequest{}, fmt.Errorf("%s: %w", ErrTypeInvalidRequest, err)
	}
	if cr.Model == "" {
		return ChatRequest{}, errors.New(ErrTypeInvalidRequest + ": model is required")
	}
	return cr, nil
}

// RewriteModel 只替换顶层 model 字段，保留其余字段（messages/tools/stream 等）。
func RewriteModel(body []byte, upstream string) ([]byte, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(body, &m); err != nil {
		return nil, err
	}
	m["model"] = json.RawMessage(`"` + jsonEscape(upstream) + `"`)
	return json.Marshal(m)
}

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b[1 : len(b)-1])
}

func ErrorEnvelope(errType, message string) []byte {
	b, _ := json.Marshal(map[string]any{
		"type":  "error",
		"error": map[string]string{"type": errType, "message": message},
	})
	return b
}
```

- [ ] **Step 4: 通过** → Run: `go test ./internal/protocol/ -v` → PASS

- [ ] **Step 5: Commit**

```bash
git add internal/protocol/
git commit -m "feat: protocol（解析、model 重写、Anthropic 错误 envelope）"
```

---

### Task 8: router — 接口与 SingleCandidate

**Files:**
- Create: `internal/router/router.go`
- Test: `internal/router/router_test.go`

**Interfaces:**
- Consumes: `account.Account`
- Produces:
  - `type Candidate struct { Account account.Account; Skipped bool; Reason string }`
  - `type Decision struct { Chosen account.Account; Candidates []Candidate; Reason string }`
  - `type Router interface { Resolve(accounts []account.Account) (Decision, error) }`
  - `var ErrNoCandidate = errors.New("no candidate account")`
  - `type Single struct{}`

- [ ] **Step 1: 失败先行测试**

`internal/router/router_test.go`：

```go
package router

import (
	"errors"
	"testing"

	"AgentSwitch/internal/account"
)

func TestSinglePicksLowestPriorityNumber(t *testing.T) {
	accounts := []account.Account{
		{ID: "b", Priority: 20, Enabled: true},
		{ID: "a", Priority: 10, Enabled: true},
		{ID: "c", Priority: 10, Enabled: false},
	}
	d, err := Single{}.Resolve(accounts)
	if err != nil {
		t.Fatal(err)
	}
	if d.Chosen.ID != "a" {
		t.Fatalf("chosen %s", d.Chosen.ID)
	}
	if len(d.Candidates) != 3 {
		t.Fatalf("candidates %d", len(d.Candidates))
	}
	if !d.Candidates[2].Skipped || d.Candidates[2].Reason == "" {
		t.Fatal("disabled account must be recorded skipped with reason")
	}
}

func TestSingleNoCandidate(t *testing.T) {
	if _, err := Single{}.Resolve(nil); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("got %v", err)
	}
	if _, err := Single{}.Resolve([]account.Account{{ID: "x", Enabled: false}}); !errors.Is(err, ErrNoCandidate) {
		t.Fatalf("all disabled: %v", err)
	}
}
```

- [ ] **Step 2: 确认失败** → Run: `go test ./internal/router/ -v` → FAIL

- [ ] **Step 3: 实现**

`internal/router/router.go`：

```go
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
func (Single) Resolve(accounts []account.Account) (Decision, error) {
	var d Decision
	var ok []account.Account
	for _, a := range accounts {
		if !a.Enabled {
			d.Candidates = append(d.Candidates, Candidate{Account: a, Skipped: true, Reason: "disabled"})
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
	if len(ok) == 0 {
		return d, ErrNoCandidate
	}
	d.Chosen = ok[0]
	d.Reason = "single-candidate: first enabled by priority"
	return d, nil
}
```

- [ ] **Step 4: 通过** → Run: `go test ./internal/router/ -v` → PASS

- [ ] **Step 5: Commit**

```bash
git add internal/router/
git commit -m "feat: router（Router 接口 + SingleCandidate 与决策记录）"
```

---

### Task 9: gateway — HTTP 服务、中间件、流式转发与 Fake Provider 集成测试

**Files:**
- Create: `internal/gateway/server.go`
- Create: `internal/gateway/handlers.go`
- Test: `internal/gateway/gateway_test.go`

**Interfaces:**
- Consumes: `config.Limits` 本包自定义；`storage`（本任务测试内直接用 temp db 的接口；正式接线在 Task 10）；`credential.Store`；`router.Router`；`provider.Adapter`；`telemetry.Ring/RequestRecord`
- Produces:
  - `type Limits struct { MaxBodyBytes int64; MaxConcurrent int; RequestTimeout time.Duration }`
  - `type Deps struct { Token func() string; Limits func() Limits; Accounts func() ([]account.Account, error); Alias func(public string) (upstream string, ok bool, err error); Cred credential.Store; Router router.Router; Adapter provider.Adapter; ProviderConfig func() (baseURL string, allowPrivate bool, err error); Log *slog.Logger; Ring *telemetry.Ring; OnRecord func(telemetry.RequestRecord) }`
  - `func New(d Deps) *Server`
  - `func (s *Server) Start(host string, port int) (string, error)`；`func (s *Server) Shutdown(ctx context.Context) error`；`func (s *Server) Addr() string`；`func (s *Server) Handler() http.Handler`（测试用）
  - 路由：`POST /v1/messages`、`GET /v1/models`、`GET /healthz`、`GET /readyz`

- [ ] **Step 1: 写集成测试（失败先行）**

`internal/gateway/gateway_test.go`（用 Fake Provider = httptest）：

```go
package gateway

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"AgentSwitch/internal/account"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

type memCred map[string][]byte

func (m memCred) Set(k string, v []byte) error { m[k] = v; return nil }
func (m memCred) Get(k string) ([]byte, error) {
	v, ok := m[k]
	if !ok {
		return nil, credential.ErrNotFound
	}
	return v, nil
}
func (m memCred) Delete(k string) error { delete(m, k); return nil }

func testServer(t *testing.T, upstream http.HandlerFunc, tok string, accounts []account.Account, alias map[string]string) *Server {
	t.Helper()
	var upBase string
	srv := httptest.NewServer(upstream)
	t.Cleanup(srv.Close)
	upBase = srv.URL
	ring := telemetry.NewRing(10)
	d := Deps{
		Token:  func() string { return tok },
		Limits: func() Limits { return Limits{MaxBodyBytes: 1 << 20, MaxConcurrent: 8, RequestTimeout: 5 * time.Second} },
		Accounts: func() ([]account.Account, error) { return accounts, nil },
		Alias: func(public string) (string, bool, error) {
			u, ok := alias[public]
			return u, ok, nil
		},
		Cred: memCred{"account:a1": []byte("sk-upstream")},
		Router: router.Single{},
		Adapter: provider.NewAnthropicAdapter(),
		ProviderConfig: func() (string, bool, error) { return upBase, true, nil },
		Log:  slog.New(slog.NewTextHandler(io.Discard, nil)),
		Ring: ring,
	}
	return New(d)
}

func authReq(ts *Server, method, path, tok, body string) *http.Request {
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	if tok != "" {
		r.Header.Set("Authorization", "Bearer "+tok)
	}
	return r
}

func TestNonStreamPassThroughAndModelRewrite(t *testing.T) {
	var sawModel, sawKey string
	up := func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var m map[string]any
		json.Unmarshal(b, &m)
		sawModel, _ = m["model"].(string)
		sawKey = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"msg_1","content":[]}`))
	}
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"claude-opus": "up-opus"})
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok",
		`{"model":"claude-opus","messages":[{"role":"user","content":"hi"}]}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "msg_1") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	if sawModel != "up-opus" {
		t.Fatalf("upstream model %q", sawModel)
	}
	if sawKey != "sk-upstream" {
		t.Fatalf("upstream key %q", sawKey)
	}
}

func TestStreamPassThrough(t *testing.T) {
	up := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message_start\ndata: {}\n\n"))
		w.Write([]byte("event: message_stop\ndata: {}\n\n"))
	}
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"m": "u"})
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok",
		`{"model":"m","stream":true,"messages":[]}`))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "message_stop") {
		t.Fatalf("stream code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestRejectsBadTokenWithoutUpstreamCall(t *testing.T) {
	var calls atomic.Int32
	up := func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"m": "u"})
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "wrong", `{"model":"m","messages":[]}`))
	if rec.Code != 401 || calls.Load() != 0 {
		t.Fatalf("code=%d calls=%d", rec.Code, calls.Load())
	}
}

func TestErrorClassesPropagateAsEnvelope(t *testing.T) {
	for _, c := range []struct {
		status int
		want   string
	}{{401, "auth"}, {429, "rate_limited"}, {500, "server"}} {
		up := func(w http.ResponseWriter, r *http.Request) {
			if c.status == 429 {
				w.Header().Set("Retry-After", "7")
			}
			w.WriteHeader(c.status)
			w.Write([]byte(`{"error":{"message":"boom"}}`))
		}
		accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
		ts := testServer(t, up, "local-tok", accts, map[string]string{"m": "u"})
		rec := httptest.NewRecorder()
		ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok", `{"model":"m","messages":[]}`))
		if rec.Code != c.status {
			t.Fatalf("%d: code=%d", c.status, rec.Code)
		}
		var env map[string]map[string]string
		json.Unmarshal(rec.Body.Bytes(), &env)
		if env["type"] != "error" {
			t.Fatalf("%d: not envelope: %s", c.status, rec.Body.String())
		}
		// Retry-After 透传
		if c.status == 429 && rec.Header().Get("Retry-After") != "7" {
			t.Fatal("Retry-After not forwarded")
		}
	}
}

func TestUnknownModelListsAliases(t *testing.T) {
	up := func(w http.ResponseWriter, r *http.Request) { t.Fatal("must not reach upstream") }
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"known": "u"})
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "POST", "/v1/messages", "local-tok", `{"model":"nope","messages":[]}`))
	if rec.Code != 404 || !strings.Contains(rec.Body.String(), "known") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHealthAndReadyz(t *testing.T) {
	up := func(w http.ResponseWriter, r *http.Request) {}
	ts := testServer(t, up, "local-tok", nil, nil)
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("healthz %d", rec.Code)
	}
	rec = httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 {
		t.Fatalf("readyz without accounts=%d want 503", rec.Code)
	}
	ts2 := testServer(t, up, "local-tok",
		[]account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}, nil)
	rec = httptest.NewRecorder()
	ts2.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 200 {
		t.Fatalf("readyz with accounts=%d", rec.Code)
	}
}

func TestModelsEndpointRequiresToken(t *testing.T) {
	up := func(w http.ResponseWriter, r *http.Request) {}
	ts := testServer(t, up, "local-tok", nil, map[string]string{"alias-a": "u"})
	rec := httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "GET", "/models", "local-tok", ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "alias-a") {
		t.Fatalf("code=%d body=%s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	ts.Handler().ServeHTTP(rec, authReq(ts, "GET", "/models", "", ""))
	if rec.Code != 401 {
		t.Fatalf("unauth models=%d", rec.Code)
	}
}

func TestClientCancelPropagatesUpstream(t *testing.T) {
	started := make(chan struct{}, 1)
	up := func(w http.ResponseWriter, r *http.Request) {
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	}
	accts := []account.Account{{ID: "a1", Priority: 10, Enabled: true, SecretKey: "account:a1"}}
	ts := testServer(t, up, "local-tok", accts, map[string]string{"m": "u"})
	ctx, cancel := context.WithCancel(context.Background())
	req := authReq(ts, "POST", "/v1/messages", "local-tok", `{"model":"m","messages":[]}`).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		ts.Handler().ServeHTTP(httptest.NewRecorder(), req)
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not return after client cancel")
	}
}
```

- [ ] **Step 2: 确认失败**

Run: `go test ./internal/gateway/ -v`
Expected: FAIL（包不存在）

- [ ] **Step 3: 实现 server.go 与 handlers.go**

`internal/gateway/server.go`：

```go
package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"AgentSwitch/internal/account"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

type Limits struct {
	MaxBodyBytes    int64
	MaxConcurrent   int
	RequestTimeout  time.Duration
}

type Deps struct {
	Token         func() string
	Limits        func() Limits
	Accounts      func() ([]account.Account, error)
	Alias         func(public string) (upstream string, ok bool, err error)
	Cred          credential.Store
	Router        router.Router
	Adapter       provider.Adapter
	ProviderConfig func() (baseURL string, allowPrivate bool, err error)
	Log           *slog.Logger
	Ring          *telemetry.Ring
	OnRecord      func(telemetry.RequestRecord)
}

type Server struct {
	deps   Deps
	mux    *http.ServeMux
	srv    *http.Server
	mu     sync.Mutex
	addr   string
	sem    chan struct{}
}

func New(d Deps) *Server {
	s := &Server{deps: d, mux: http.NewServeMux()}
	s.mux.HandleFunc("POST /v1/messages", s.handleMessages)
	s.mux.HandleFunc("GET /v1/models", s.handleModels)
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

func (s *Server) authorized(r *http.Request) bool {
	tok := s.deps.Token()
	got := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if tok == "" || !strings_HasPrefix(got, prefix) {
		return false
	}
	return subtleConstantTimeCompare(got[len(prefix):], tok)
}
```

（`strings_HasPrefix` 用 `strings.HasPrefix` 替换即可；比较用 `crypto/subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1`——在实现时直接写这两个标准库调用，不要自造函数名。）

`internal/gateway/handlers.go`：

```go
package gateway

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/protocol"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/telemetry"
)

// responseHeaderWhitelist：回传给客户端的上游响应头。
var responseHeaderWhitelist = []string{"Content-Type", "Retry-After", "Anthropic-Request-Id"}

func writeErr(w http.ResponseWriter, status int, errType, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	w.Write(protocol.ErrorEnvelope(errType, msg))
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	accts, err := s.deps.Accounts()
	if err != nil || len(accts) == 0 || s.deps.Token() == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(503)
		json.NewEncoder(w).Encode(map[string]any{"status": "degraded",
			"reason": "no usable account or token"})
		return
	}
	w.WriteHeader(200)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeErr(w, 401, protocol.ErrTypeAuth(), "invalid gateway token")
		return
	}
	accts, err := s.deps.Accounts()
	if err != nil {
		writeErr(w, 500, protocol.ErrTypeAPI, "internal error")
		return
	}
	// 别名来自 Alias 闭包无法枚举——Deps 增加 ListAliases func() []string。
	aliases := s.deps.ListAliases()
	type model struct {
		ID          string `json:"id"`
		Type        string `json:"type"`
		DisplayName string `json:"display_name"`
	}
	data := []model{}
	for _, a := range aliases {
		data = append(data, model{ID: a, Type: "model", DisplayName: a})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"data": data, "has_more": false})
}

func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	started := time.Now()
	rec := telemetry.RequestRecord{ID: telemetryID(), StartedAt: started}
	defer func() {
		rec.DurationMs = time.Since(started).Milliseconds()
		if s.deps.Ring != nil {
			s.deps.Ring.Add(rec)
		}
		if s.deps.OnRecord != nil {
			s.deps.OnRecord(rec)
		}
	}()

	if !s.authorized(r) {
		rec.Status, rec.ErrorClass = 401, "auth"
		writeErr(w, 401, protocol.ErrTypeAuth(), "invalid gateway token")
		return
	}
	lim := s.deps.Limits()
	if lim.MaxConcurrent > 0 {
		select {
		case s.sem <- struct{}{}:
			defer func() { <-s.sem }()
		default:
			rec.Status, rec.ErrorClass = 503, "busy"
			writeErr(w, 503, protocol.ErrTypeAPI, "gateway busy, retry later")
			return
		}
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, lim.MaxBodyBytes+1))
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErr(w, 400, protocol.ErrTypeInvalidRequest, "read body failed")
		return
	}
	if int64(len(body)) > lim.MaxBodyBytes {
		rec.Status, rec.ErrorClass = 413, protocol.ErrTypeInvalidRequest
		writeErr(w, 413, protocol.ErrTypeInvalidRequest, "request body too large")
		return
	}
	cr, err := protocol.ParseChatRequest(body)
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErr(w, 400, protocol.ErrTypeInvalidRequest, "invalid body: model and valid JSON required")
		return
	}
	rec.Model, rec.Streaming = cr.Model, cr.Stream

	upstream, ok, err := s.deps.Alias(cr.Model)
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErr(w, 500, protocol.ErrTypeAPI, "alias lookup failed")
		return
	}
	if !ok {
		rec.Status, rec.ErrorClass = 404, protocol.ErrTypeNotFound
		writeErr(w, 404, protocol.ErrTypeNotFound, "unknown model "+cr.Model+"; aliases: "+strings.Join(s.deps.ListAliases(), ", "))
		return
	}
	rec.UpstreamModel = upstream
	rewritten, err := protocol.RewriteModel(body, upstream)
	if err != nil {
		rec.Status, rec.ErrorClass = 400, protocol.ErrTypeInvalidRequest
		writeErr(w, 400, protocol.ErrTypeInvalidRequest, "invalid body")
		return
	}

	accts, err := s.deps.Accounts()
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErr(w, 500, protocol.ErrTypeAPI, "accounts unavailable")
		return
	}
	decision, err := s.deps.Router.Resolve(accts)
	if err != nil {
		rec.Status, rec.ErrorClass = 503, "no_candidate"
		writeErr(w, 503, protocol.ErrTypeAPI, "no usable account: "+err.Error())
		return
	}
	rec.Account = decision.Chosen.ID
	apiKey, err := s.deps.Cred.Get(decision.Chosen.SecretKey)
	if err != nil {
		rec.Status, rec.ErrorClass = 500, "credential"
		writeErr(w, 500, protocol.ErrTypeAPI, "credential unavailable for account "+decision.Chosen.Name)
		return
	}
	baseURL, allowPrivate, err := s.deps.ProviderConfig()
	if err != nil {
		rec.Status, rec.ErrorClass = 500, protocol.ErrTypeAPI
		writeErr(w, 500, protocol.ErrTypeAPI, "provider config error")
		return
	}

	ctx := r.Context()
	if lim.RequestTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, lim.RequestTimeout)
		defer cancel()
	}
	res, err := s.deps.Adapter.Do(ctx, provider.DoRequest{
		BaseURL: baseURL, AllowPrivate: allowPrivate, APIKey: string(apiKey),
		UpstreamModel: upstream, RawBody: rewritten, Stream: cr.Stream,
	})
	if err != nil {
		rec.Status, rec.ErrorClass = 502, string(provider.ClassConnect)
		writeErr(w, 502, protocol.ErrTypeAPI, "upstream connect failed")
		return
	}
	defer res.Body.Close()
	rec.Status = res.StatusCode
	if res.StatusCode >= 400 {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 64<<10))
		rec.ErrorClass = string(provider.Classify(res.StatusCode, res.RetryAfterSec, snippet))
		for _, h := range responseHeaderWhitelist {
			if v := res.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(res.StatusCode)
		w.Write(snippet) // 上游错误体已为 JSON envelope 形状，直接透传
		return
	}
	for _, h := range responseHeaderWhitelist {
		if v := res.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.WriteHeader(res.StatusCode)
	if cr.Stream {
		copyStream(w, res.Body)
	} else {
		io.Copy(w, res.Body)
	}
}

// copyStream 逐块转发并 flush；客户端断开由上游 ctx 取消收尾。
func copyStream(w http.ResponseWriter, src io.Reader) {
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			w.Write(buf[:n])
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

func subtleConstantTimeCompare(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func telemetryID() string {
	b := make([]byte, 8)
	rand_Read(b)
	return hex_EncodeToString(b)
}
```

实现时的两处修正（照做）：

1. `Deps` 增加字段 `ListAliases func() []string`（`handleModels` 与未知模型错误信息用它枚举别名）。
2. `telemetryID` 直接写标准库：`func telemetryID() string { b := make([]byte, 8); rand.Read(b); return hex.EncodeToString(b) }`（import `crypto/rand`、`encoding/hex`）；删除 `strings_HasPrefix` 等自造名，`authorized` 内直接用 `strings.HasPrefix` 与 `subtleConstantTimeCompare`（保留此小助手）。

- [ ] **Step 4: 通过**

Run: `go test ./internal/gateway/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/gateway/
git commit -m "feat: gateway（令牌/限额中间件、流式透传、错误 envelope、readyz 降级）"
```

---

### Task 10: app — 组装、状态机、生命周期

**Files:**
- Create: `internal/app/app.go`
- Test: `internal/app/app_test.go`

**Interfaces:**
- Consumes: Task 2-9 全部产出
- Produces:
  - `type State string`（`"stopped"|"starting"|"running"|"degraded"|"stopping"|"failed"`）
  - `type StateInfo struct { State State; Addr string; DegradedReason string; Since time.Time; Version string }`
  - `type Options struct { DBPath, LogPath, Version, Host string }`
  - `func Open(opts Options) (*App, error)`（开库、迁移、确保令牌存在——空则 crypto/rand 生成并持久化）
  - `func (a *App) Store() storage.Store`；`func (a *App) Ring() *telemetry.Ring`；`func (a *App) Log() *slog.Logger`
  - `func (a *App) OnState(fn func(StateInfo))`；`func (a *App) OnRecord(fn func(telemetry.RequestRecord))`
  - `func (a *App) State() StateInfo`；`func (a *App) StartGateway() error`；`func (a *App) StopGateway(ctx context.Context) error`
  - `func (a *App) RegenerateToken() (string, error)`；`func (a *App) SaveSettings(port, timeoutSeconds int) (restartRequired bool, err error)`
  - `func (a *App) Recent() []telemetry.RequestRecord`；`func (a *App) Close() error`

- [ ] **Step 1: 失败先行测试**

`internal/app/app_test.go`：

```go
package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"AgentSwitch/internal/account"
	"AgentSwitch/internal/config"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/storage"
)

func newTestApp(t *testing.T, upstreamURL string, withAccount bool) *App {
	t.Helper()
	a, err := Open(Options{
		DBPath:  filepath.Join(t.TempDir(), "app.db"),
		LogPath: filepath.Join(t.TempDir(), "app.log"),
		Version: "test",
		Host:    "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	if err := a.Store().Providers().Save(storage.Provider{
		ID: "p1", Name: "up", Protocol: "anthropic", BaseURL: upstreamURL,
		Enabled: true, AllowPrivateUpstream: true,
	}); err != nil {
		t.Fatal(err)
	}
	if withAccount {
		if err := a.Store().Accounts().Save(storage.Account{
			ID: "a1", ProviderID: "p1", Name: "acc", SecretKey: "account:a1",
			SecretBackend: "fallback-file", Priority: 10, Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		if err := a.creds.Set("account:a1", []byte("sk-test")); err != nil {
			t.Fatal(err)
		}
	}
	return a
}

func TestLifecycleDegradedAndRunning(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	a := newTestApp(t, up.URL, false)
	if a.State().State != StateStopped {
		t.Fatalf("initial %s", a.State().State)
	}
	if err := a.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if a.State().State != StateDegraded {
		t.Fatalf("no accounts → degraded, got %s", a.State().State)
	}
	if err := a.StopGateway(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.State().State != StateStopped {
		t.Fatalf("after stop %s", a.State().State)
	}

	a2 := newTestApp(t, up.URL, true)
	if err := a2.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if a2.State().State != StateRunning {
		t.Fatalf("want running, got %s (%s)", a2.State().State, a2.State().DegradedReason)
	}
}

func TestOpenGeneratesTokenAndRegeneratePersists(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "t.db")
	a, err := Open(Options{DBPath: db, LogPath: filepath.Join(dir, "l.log"), Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	tok := a.State().token()
	if tok == "" {
		t.Fatal("token must be generated")
	}
	newTok, err := a.RegenerateToken()
	if err != nil || newTok == "" || newTok == tok {
		t.Fatalf("regen: %q %v", newTok, err)
	}
	a.Close()
	a2, err := Open(Options{DBPath: db, LogPath: filepath.Join(dir, "l2.log"), Host: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	defer a2.Close()
	if a2.State().token() != newTok {
		t.Fatal("regenerated token not persisted")
	}
}

func TestStateCallbacksFire(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer up.Close()
	a := newTestApp(t, up.URL, true)
	var got []State
	a.OnState(func(si StateInfo) { got = append(got, si.State) })
	if err := a.StartGateway(); err != nil {
		t.Fatal(err)
	}
	if len(got) < 2 || got[0] != StateStarting || got[len(got)-1] != StateRunning {
		t.Fatalf("states %v", got)
	}
	_ = provider.ClassUnknown // keep import if unused elsewhere
	_ = account.Account{}
	_ = config.Default
	_ = time.Now
}
```

（`StateInfo.token()` 非导出访问器不必要——测试改用 `a.currentToken()`：实现中提供非导出方法 `func (a *App) currentToken() string` 读 settings；测试同包可直接调用。实现与测试按此对齐。）

- [ ] **Step 2: 确认失败** → Run: `go test ./internal/app/ -v` → FAIL

- [ ] **Step 3: 实现 app.go**

`internal/app/app.go`（核心骨架，字段与逻辑照此完整实现）：

```go
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"AgentSwitch/internal/account"
	"AgentSwitch/internal/config"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/gateway"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/router"
	"AgentSwitch/internal/storage"
	"AgentSwitch/internal/telemetry"
)

type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateDegraded State = "degraded"
	StateStopping State = "stopping"
	StateFailed   State = "failed"
)

type StateInfo struct {
	State          State     `json:"state"`
	Addr           string    `json:"addr,omitempty"`
	DegradedReason string    `json:"degradedReason,omitempty"`
	Since          time.Time `json:"since"`
	Version        string    `json:"version,omitempty"`
}

type Options struct {
	DBPath, LogPath, Version, Host string
}

type App struct {
	mu       sync.Mutex
	opts     Options
	store    storage.Store
	logClose io.Closer
	log      *slog.Logger
	ring     *telemetry.Ring
	creds    credential.Manager
	gw       *gateway.Server
	state    StateInfo
	onState  []func(StateInfo)
	onRecord []func(telemetry.RequestRecord)
}

func Open(opts Options) (*App, error) {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	if opts.DBPath == "" {
		return nil, fmt.Errorf("DBPath required")
	}
	st, err := storage.Open(opts.DBPath)
	if err != nil {
		return nil, err
	}
	logger, closer, err := telemetry.NewLogger(opts.LogPath)
	if err != nil {
		st.Close()
		return nil, err
	}
	fb := credential.NewFallbackFile(filepath.Join(filepath.Dir(opts.DBPath), "secrets-fallback.json"))
	a := &App{
		opts:     opts,
		store:    st,
		log:      logger,
		logClose: closer,
		ring:     telemetry.NewRing(100),
		creds:    credential.Manager{Primary: credential.OSKeychain{}, Fallback: fb},
	}
	// 确保令牌存在
	s, err := a.settings()
	if err != nil {
		a.Close()
		return nil, err
	}
	if s.GatewayToken == "" {
		if _, err := a.RegenerateToken(); err != nil {
			a.Close()
			return nil, err
		}
	}
	a.setState(StateInfo{State: StateStopped, Since: time.Now(), Version: opts.Version})
	return a, nil
}

func (a *App) Store() storage.Store       { return a.store }
func (a *App) Ring() *telemetry.Ring      { return a.ring }
func (a *App) Log() *slog.Logger          { return a.log }

func (a *App) OnState(fn func(StateInfo)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onState = append(a.onState, fn)
}

func (a *App) OnRecord(fn func(telemetry.RequestRecord)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onRecord = append(a.onRecord, fn)
}

func (a *App) State() StateInfo {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state
}

func (a *App) setState(si StateInfo) {
	a.mu.Lock()
	a.state = si
	cbs := append([]func(StateInfo){}, a.onState...)
	a.mu.Unlock()
	for _, fn := range cbs {
		fn(si)
	}
}

func (a *App) settings() (config.Settings, error) {
	return config.Load(a.store.Settings().Get)
}

func (a *App) currentToken() string {
	s, err := a.settings()
	if err != nil {
		return ""
	}
	return s.GatewayToken
}

func (a *App) usableAccounts() []account.Account {
	rows, err := a.store.Accounts().List()
	if err != nil {
		return nil
	}
	out := make([]account.Account, 0, len(rows))
	for _, r := range rows {
		out = append(out, account.Account{
			ID: r.ID, ProviderID: r.ProviderID, Name: r.Name,
			SecretKey: r.SecretKey, SecretBackend: r.SecretBackend,
			Priority: r.Priority, Enabled: r.Enabled,
		})
	}
	return out
}

func (a *App) StartGateway() error {
	a.setState(StateInfo{State: StateStarting, Since: time.Now(), Version: a.State().Version})
	s, err := a.settings()
	if err != nil {
		a.setState(StateInfo{State: StateFailed, DegradedReason: err.Error(), Since: time.Now(), Version: a.State().Version})
		return err
	}
	d := gateway.Deps{
		Token:  a.currentToken,
		Limits: func() gateway.Limits {
			s, _ := a.settings()
			return gateway.Limits{
				MaxBodyBytes: s.MaxBodyBytes, MaxConcurrent: s.MaxConcurrent,
				RequestTimeout: time.Duration(s.RequestTimeoutSeconds) * time.Second,
			}
		},
		Accounts: func() ([]account.Account, error) { return a.usableAccounts(), nil },
		Alias: func(public string) (string, bool, error) {
			m, err := a.store.Models().GetByPublic(public)
			if err == storage.ErrNotFound {
				return "", false, nil
			}
			if err != nil {
				return "", false, err
			}
			return m.UpstreamModel, m.Enabled, nil
		},
		ListAliases: func() []string {
			ms, _ := a.store.Models().List()
			var out []string
			for _, m := range ms {
				if m.Enabled {
					out = append(out, m.PublicModel)
				}
			}
			return out
		},
		Cred:    a.creds,
		Router:  router.Single{},
		Adapter: provider.NewAnthropicAdapter(),
		ProviderConfig: func() (string, bool, error) {
			ps, err := a.store.Providers().List()
			if err != nil {
				return "", false, err
			}
			for _, p := range ps {
				if p.Enabled && p.Protocol == "anthropic" {
					return p.BaseURL, p.AllowPrivateUpstream, nil
				}
			}
			return "", false, fmt.Errorf("no enabled anthropic provider")
		},
		Log:  a.log,
		Ring: a.ring,
		OnRecord: func(rec telemetry.RequestRecord) {
			a.mu.Lock()
			cbs := append([]func(telemetry.RequestRecord){}, a.onRecord...)
			a.mu.Unlock()
			for _, fn := range cbs {
				fn(rec)
			}
		},
	}
	gw := gateway.New(d)
	addr, err := gw.Start(a.opts.Host, s.Port)
	if err != nil {
		a.setState(StateInfo{State: StateFailed, DegradedReason: err.Error(), Since: time.Now(), Version: a.State().Version})
		return err
	}
	a.mu.Lock()
	a.gw = gw
	a.mu.Unlock()
	si := StateInfo{State: StateRunning, Addr: addr, Since: time.Now(), Version: a.State().Version}
	if len(a.usableAccounts()) == 0 {
		si.State = StateDegraded
		si.DegradedReason = "no enabled account"
	}
	a.log.Info("gateway started", "addr", addr, "state", si.State)
	a.setState(si)
	return nil
}

func (a *App) StopGateway(ctx context.Context) error {
	a.mu.Lock()
	gw := a.gw
	a.mu.Unlock()
	if gw == nil {
		return nil
	}
	a.setState(StateInfo{State: StateStopping, Addr: gw.Addr(), Since: time.Now(), Version: a.State().Version})
	err := gw.Shutdown(ctx)
	a.mu.Lock()
	a.gw = nil
	a.mu.Unlock()
	a.setState(StateInfo{State: StateStopped, Since: time.Now(), Version: a.State().Version})
	a.log.Info("gateway stopped")
	return err
}

func (a *App) RegenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	s, err := a.settings()
	if err != nil {
		return "", err
	}
	s.GatewayToken = tok
	if err := s.Save(a.store.Settings().Set); err != nil {
		return "", err
	}
	return tok, nil
}

// SaveSettings 保存端口与请求超时；网关运行中且端口变化时要求重启。
func (a *App) SaveSettings(port, timeoutSeconds int) (bool, error) {
	s, err := a.settings()
	if err != nil {
		return false, err
	}
	restart := port != s.Port && a.State().State != StateStopped
	s.Port = port
	s.RequestTimeoutSeconds = timeoutSeconds
	if err := s.Save(a.store.Settings().Set); err != nil {
		return false, err
	}
	return restart, nil
}

func (a *App) Recent() []telemetry.RequestRecord { return a.ring.List() }

func (a *App) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = a.StopGateway(ctx)
	a.mu.Lock()
	closer := a.logClose
	st := a.store
	a.mu.Unlock()
	if closer != nil {
		closer.Close()
	}
	if st != nil {
		return st.Close()
	}
	return nil
}
```

- [ ] **Step 4: 通过** → Run: `go test ./internal/app/ -v` → 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/
git commit -m "feat: app（组装、六态状态机、生命周期、令牌与设置管理）"
```

---

### Task 11: bridge — 桥接服务（管理面）

**Files:**
- Create: `internal/bridge/bridge.go`
- Test: `internal/bridge/bridge_test.go`

**Interfaces:**
- Consumes: `app.App`、`storage`、`credential`、`provider.Probe`
- Produces（全部经 `mygo.Bind` 暴露，方法名即前端可见面）：
  - `type Gateway struct{ A *app.App }`；`type Status struct { State, Addr, DegradedReason, Token, Version string; Since time.Time; Recent []telemetry.RequestRecord }`
  - `func (Gateway) Status() Status`；`Start() error`；`Stop() error`；`RegenerateToken() (string, error)`；`SaveSettings(port, timeoutSeconds int) (bool, error)`；`Recent() []telemetry.RequestRecord`
  - `type ProviderView struct { ID, Name, Protocol, BaseURL string; Enabled, AllowPrivateUpstream bool }`
  - `func (Providers) List() []ProviderView`；`Create(name, protocol, baseURL string, enabled, allowPrivate bool) (ProviderView, error)`；`Update(id, name, baseURL string, enabled, allowPrivate bool) error`；`Remove(id string) error`（级联删账号+密钥）；`Test(id string) provider.ProbeResult`
  - `type AccountView struct { ID, ProviderID, Name, KeyTail, SecretBackend string; Priority int; Enabled bool }`
  - `func (Accounts) List() []AccountView`；`Create(name, providerID, apiKey string, priority int, enabled, allowFallback bool) (AccountView, error)`；`Update(id, name string, priority int) error`；`SetEnabled(id string, on bool) error`；`RotateSecret(id, apiKey string, allowFallback bool) error`；`Remove(id string) error`
  - `type ModelView struct { ID, PublicModel, UpstreamModel string; Enabled bool }`
  - `func (Models) List() []ModelView`；`Create(public, upstream string) (ModelView, error)`；`Update(id, public, upstream string) error`；`Remove(id string) error`

- [ ] **Step 1: 失败先行测试**

`internal/bridge/bridge_test.go`：

```go
package bridge

import (
	"path/filepath"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/storage"
)

func tempApp(t *testing.T) *app.App {
	t.Helper()
	a, err := app.Open(app.Options{
		DBPath: filepath.Join(t.TempDir(), "b.db"), LogPath: filepath.Join(t.TempDir(), "b.log"), Host: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	return a
}

func TestProviderCRUDAndValidation(t *testing.T) {
	a := tempApp(t)
	ps := Providers{A: a}
	p, err := ps.Create("main", "anthropic", "https://api.example.com", true, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.Create("bad", "openai", "https://x.example.com", true, false); err == nil {
		t.Fatal("non-anthropic protocol must be rejected in P0-A")
	}
	if _, err := ps.Create("badurl", "anthropic", "http://plain.example.com", true, false); err == nil {
		t.Fatal("non-loopback http must be rejected")
	}
	if err := ps.Update(p.ID, "main2", "https://api2.example.com", false, false); err != nil {
		t.Fatal(err)
	}
	if got := ps.List(); len(got) != 1 || got[0].Name != "main2" || got[0].Enabled {
		t.Fatalf("list %+v", got)
	}
	if err := ps.Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(ps.List()) != 0 {
		t.Fatal("remove failed")
	}
}

func TestAccountSecretLifecycle(t *testing.T) {
	a := tempApp(t)
	ps := Providers{A: a}
	p, _ := ps.Create("main", "anthropic", "https://api.example.com", true, false)
	creds := credential.Manager{
		Primary:   credential.NewFallbackFile(filepath.Join(t.TempDir(), "s.json")),
		AllowFallback: true,
	}
	as := Accounts{A: a, Creds: creds}
	av, err := as.Create("acc1", p.ID, "sk-ant-xyz7890", 10, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if av.KeyTail != "7890" || av.SecretBackend != "fallback-file" {
		t.Fatalf("view %+v", av)
	}
	got, err := creds.Get(credential.AccountKey(av.ID))
	if err != nil || string(got) != "sk-ant-xyz7890" {
		t.Fatalf("secret: %q %v", got, err)
	}
	if err := as.SetEnabled(av.ID, false); err != nil {
		t.Fatal(err)
	}
	if l := as.List(); len(l) != 1 || l[0].Enabled {
		t.Fatalf("setEnabled %+v", l)
	}
	if err := as.RotateSecret(av.ID, "sk-ant-newkey99", true); err != nil {
		t.Fatal(err)
	}
	if v, _ := creds.Get(credential.AccountKey(av.ID)); string(v) != "sk-ant-newkey99" {
		t.Fatal("rotate failed")
	}
	// 删除 provider 级联删除账号与密钥
	if err := ps.Remove(p.ID); err != nil {
		t.Fatal(err)
	}
	if len(as.List()) != 0 {
		t.Fatal("cascade failed")
	}
	if _, err := creds.Get(credential.AccountKey(av.ID)); err == nil {
		t.Fatal("cascade must delete secret")
	}
}

func TestModelsCRUDAndUnique(t *testing.T) {
	a := tempApp(t)
	ms := Models{A: a}
	m, err := ms.Create("claude-opus", "up-opus")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ms.Create("claude-opus", "other"); err == nil {
		t.Fatal("duplicate alias must fail")
	}
	if err := ms.Update(m.ID, "claude-opus-2", "up-opus"); err != nil {
		t.Fatal(err)
	}
	if got := ms.List(); len(got) != 1 || got[0].PublicModel != "claude-opus-2" {
		t.Fatalf("%+v", got)
	}
	if err := ms.Remove(m.ID); err != nil {
		t.Fatal(err)
	}
}

func TestGatewayStatusShape(t *testing.T) {
	a := tempApp(t)
	g := Gateway{A: a}
	st := g.Status()
	if st.State != "stopped" || st.Token == "" {
		t.Fatalf("%+v", st)
	}
	tok, err := g.RegenerateToken()
	if err != nil || tok == st.Token {
		t.Fatalf("%q %v", tok, err)
	}
	_ = storage.NewID
}
```

- [ ] **Step 2: 确认失败** → Run: `go test ./internal/bridge/ -v` → FAIL

- [ ] **Step 3: 实现 bridge.go**

```go
package bridge

import (
	"fmt"
	"strconv"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/config"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/provider"
	"AgentSwitch/internal/storage"
	"AgentSwitch/internal/telemetry"
)

type Gateway struct{ A *app.App }

type Status struct {
	State          string                   `json:"state"`
	Addr           string                   `json:"addr"`
	DegradedReason string                   `json:"degradedReason,omitempty"`
	Since          int64                    `json:"sinceMs"`
	Token          string                   `json:"token"`
	Version        string                   `json:"version"`
	Recent         []telemetry.RequestRecord `json:"recent"`
}

func (g Gateway) Status() Status {
	si := g.A.State()
	s, _ := config.Load(g.A.Store().Settings().Get)
	return Status{
		State: string(si.State), Addr: si.Addr, DegradedReason: si.DegradedReason,
		Since: si.Since.UnixMilli(), Token: s.GatewayToken, Version: si.Version,
		Recent: g.A.Recent(),
	}
}

func (g Gateway) Start() error                        { return g.A.StartGateway() }
func (g Gateway) Stop() error                         { return g.A.StopGateway(context15()) }
func (g Gateway) RegenerateToken() (string, error)    { return g.A.RegenerateToken() }
func (g Gateway) SaveSettings(port, timeout int) (bool, error) { return g.A.SaveSettings(port, timeout) }
func (g Gateway) Recent() []telemetry.RequestRecord   { return g.A.Recent() }

type ProviderView struct {
	ID, Name, Protocol, BaseURL string
	Enabled, AllowPrivateUpstream bool
}

type Providers struct{ A *app.App }

func providerView(p storage.Provider) ProviderView {
	return ProviderView{p.ID, p.Name, p.Protocol, p.BaseURL, p.Enabled, p.AllowPrivateUpstream}
}

func (s Providers) List() []ProviderView {
	ps, _ := s.A.Store().Providers().List()
	out := make([]ProviderView, 0, len(ps))
	for _, p := range ps {
		out = append(out, providerView(p))
	}
	return out
}

func (s Providers) Create(name, protocol, baseURL string, enabled, allowPrivate bool) (ProviderView, error) {
	if protocol != "anthropic" {
		return ProviderView{}, fmt.Errorf("P0-A 仅支持协议 anthropic，收到 %q", protocol)
	}
	if err := provider.ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return ProviderView{}, err
	}
	p := storage.Provider{ID: storage.NewID(), Name: name, Protocol: protocol,
		BaseURL: baseURL, Enabled: enabled, AllowPrivateUpstream: allowPrivate}
	if err := s.A.Store().Providers().Save(p); err != nil {
		return ProviderView{}, err
	}
	s.A.Log().Info("provider created", "id", p.ID, "name", name)
	return providerView(p), nil
}

func (s Providers) Update(id, name, baseURL string, enabled, allowPrivate bool) error {
	p, err := s.A.Store().Providers().Get(id)
	if err != nil {
		return err
	}
	if err := provider.ValidateBaseURL(baseURL, allowPrivate); err != nil {
		return err
	}
	p.Name, p.BaseURL, p.Enabled, p.AllowPrivateUpstream = name, baseURL, enabled, allowPrivate
	if err := s.A.Store().Providers().Save(p); err != nil {
		return err
	}
	s.A.Log().Info("provider updated", "id", id)
	return nil
}

// Remove 删除供应商并级联清理账号密钥。
func (s Providers) Remove(id string) error {
	accounts, _ := s.A.Store().Accounts().ListByProvider(id)
	for _, a := range accounts {
		s.A.Creds().Delete(a.SecretKey)
	}
	if err := s.A.Store().Providers().Delete(id); err != nil {
		return err
	}
	s.A.Log().Info("provider removed", "id", id)
	return nil
}

func (s Providers) Test(id string) provider.ProbeResult {
	p, err := s.A.Store().Providers().Get(id)
	if err != nil {
		return provider.ProbeResult{Stage: "lookup", Error: err.Error()}
	}
	accounts, _ := s.A.Store().Accounts().ListByProvider(id)
	apiKey := ""
	for _, a := range accounts {
		if a.Enabled {
			if v, err := s.A.Creds().Get(a.SecretKey); err == nil {
				apiKey = string(v)
			}
			break
		}
	}
	return provider.Probe(context15ctx(), p.BaseURL, apiKey, p.AllowPrivateUpstream)
}

type AccountView struct {
	ID, ProviderID, Name, KeyTail, SecretBackend string
	Priority                                     int
	Enabled                                      bool
}

type Accounts struct {
	A     *app.App
	Creds credential.Store
}

func accountView(a storage.Account) AccountView {
	tail := ""
	if len(a.SecretKey) > 0 {
		// KeyTail 由密钥本体尾 4 位构成——Create/Rotate 时写入 secret_key 后无法回读本体，
		// 因此视图尾 4 位从密钥本体取：见 Create 中存入 tail 的约定。
	}
	_ = tail
	return AccountView{a.ID, a.ProviderID, a.Name, keyTailOf(a), a.SecretBackend, a.Priority, a.Enabled}
}

// keyTailOf 约定：storage.Account.SecretKey 存 Secrets 键名；尾 4 位在 Create 时
// 以 "account:<id>" 键写入密钥库，尾 4 位无法从键名恢复——因此额外约定：
// Create 把尾 4 位写进 Account.Name 之外的字段不可行（schema 固定）。
// P0-A 落地：KeyTail 由调用方 Rotate/Create 后即时返回，List 时显示 "***"。
// 简化（本任务采纳）：Create/Rotate 返回的视图带真实尾 4 位；List 返回 ""。
func keyTailOf(a storage.Account) string { return "" }

func (s Accounts) List() []AccountView {
	as, _ := s.A.Store().Accounts().List()
	out := make([]AccountView, 0, len(as))
	for _, a := range as {
		out = append(out, accountView(a))
	}
	return out
}

func (s Accounts) Create(name, providerID, apiKey string, priority int, enabled, allowFallback bool) (AccountView, error) {
	if apiKey == "" {
		return AccountView{}, fmt.Errorf("apiKey required")
	}
	if _, err := s.A.Store().Providers().Get(providerID); err != nil {
		return AccountView{}, fmt.Errorf("provider: %w", err)
	}
	backend, err := s.pickBackend(allowFallback)
	if err != nil {
		return AccountView{}, err
	}
	id := storage.NewID()
	key := credential.AccountKey(id)
	if err := s.Creds.Set(key, []byte(apiKey)); err != nil {
		return AccountView{}, err
	}
	row := storage.Account{ID: id, ProviderID: providerID, Name: name, SecretKey: key,
		SecretBackend: backend, Priority: priority, Enabled: enabled}
	if err := s.A.Store().Accounts().Save(row); err != nil {
		s.Creds.Delete(key)
		return AccountView{}, err
	}
	s.A.Log().Info("account created", "id", id, "provider", providerID, "backend", backend)
	view := accountView(row)
	view.KeyTail = tail4(apiKey)
	return view, nil
}

func (s Accounts) pickBackend(allowFallback bool) (string, error) {
	if credential.OSKeychainAvailable() {
		return "os-keychain", nil
	}
	if allowFallback {
		return "fallback-file", nil
	}
	return "", fmt.Errorf("系统密钥存储不可用；需显式允许文件降级（allowFallback）")
}

func (s Accounts) Update(id, name string, priority int) error {
	a, err := s.A.Store().Accounts().Get(id)
	if err != nil {
		return err
	}
	a.Name, a.Priority = name, priority
	return s.A.Store().Accounts().Save(a)
}

func (s Accounts) SetEnabled(id string, on bool) error { return s.A.Store().Accounts().SetEnabled(id, on) }

func (s Accounts) RotateSecret(id, apiKey string, allowFallback bool) error {
	a, err := s.A.Store().Accounts().Get(id)
	if err != nil {
		return err
	}
	if apiKey == "" {
		return fmt.Errorf("apiKey required")
	}
	backend, err := s.pickBackend(allowFallback)
	if err != nil {
		return err
	}
	if err := s.Creds.Set(a.SecretKey, []byte(apiKey)); err != nil {
		return err
	}
	a.SecretBackend = backend
	s.A.Log().Info("account secret rotated", "id", id)
	return s.A.Store().Accounts().Save(a)
}

func (s Accounts) Remove(id string) error {
	a, err := s.A.Store().Accounts().Get(id)
	if err != nil {
		return err
	}
	s.Creds.Delete(a.SecretKey)
	s.A.Log().Info("account removed", "id", id)
	return s.A.Store().Accounts().Delete(id)
}

func tail4(secret string) string {
	if len(secret) <= 4 {
		return "****"
	}
	return secret[len(secret)-4:]
}

type ModelView struct {
	ID, PublicModel, UpstreamModel string
	Enabled                        bool
}

type Models struct{ A *app.App }

func (s Models) List() []ModelView {
	ms, _ := s.A.Store().Models().List()
	out := make([]ModelView, 0, len(ms))
	for _, m := range ms {
		out = append(out, ModelView{m.ID, m.PublicModel, m.UpstreamModel, m.Enabled})
	}
	return out
}

func (s Models) Create(public, upstream string) (ModelView, error) {
	if public == "" || upstream == "" {
		return ModelView{}, fmt.Errorf("public 与 upstream 均不能为空")
	}
	m := storage.ModelMapping{ID: storage.NewID(), PublicModel: public, UpstreamModel: upstream, Enabled: true}
	if err := s.A.Store().Models().Save(m); err != nil {
		return ModelView{}, err
	}
	s.A.Log().Info("model created", "public", public, "upstream", upstream)
	return ModelView{m.ID, m.PublicModel, m.UpstreamModel, m.Enabled}, nil
}

func (s Models) Update(id, public, upstream string) error {
	m, err := s.A.Store().Models().GetByPublic(public)
	if err != nil && err != storage.ErrNotFound {
		return err
	}
	if err == nil && m.ID != id {
		return fmt.Errorf("公开模型名已存在: %s", public)
	}
	// 按 id 重新取（GetByPublic 只是查重）
	list, _ := s.A.Store().Models().List()
	for _, x := range list {
		if x.ID == id {
			m = x
			break
		}
	}
	if m.ID != id {
		return storage.ErrNotFound
	}
	m.PublicModel, m.UpstreamModel = public, upstream
	return s.A.Store().Models().Save(m)
}

func (s Models) Remove(id string) error { return s.A.Store().Models().Delete(id) }
```

两处实现约定（照做）：
1. `context15()` 返回 `context.WithTimeout(context.Background(), 15*time.Second)` 的组合（cancel 在 goroutine 结束后调用或直接用 `context.Background()` + 超时包装——实现为：`func context15() context.Context { ctx, _ := context.WithTimeout(context.Background(), 15*time.Second); return ctx }`；`context15ctx()` 同义，二选一统一即可）。
2. `Accounts` 依赖的 `s.A.Creds()`：`app.App` 需要暴露 `func (a *App) Creds() credential.Store`（Task 10 已实现 App；此处给 App 增加一个 5 行 getter，并在 bridge 测试中使用 `a.Creds().Set(...)` 替代直接字段访问 `a.creds`）。相应地把 Task 10 测试里的 `a.creds.Set` 改为 `a.Creds().Set`。

- [ ] **Step 4: 通过** → Run: `go test ./internal/bridge/ ./internal/app/ -v` → 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/bridge/ internal/app/
git commit -m "feat: bridge（管理面服务：网关控制、供应商/账号/模型 CRUD 与测试）"
```

---

### Task 12: desktop — 窗口、托盘、关窗拦截、退出钩子与状态桥接

**Files:**
- Create: `internal/desktop/desktop.go`
- Create: `internal/desktop/tray.png`（Step 1 生成）
- Modify: `cmd/agentswitch/main.go`（替换为调用 desktop.Run）
- Test: `internal/desktop/desktop_test.go`（状态视图部分）

**Interfaces:**
- Consumes: `app.App`、`bridge.*`、mygo 全套（Bind/Window/Tray/OnWindowAllClosed/OnBeforeQuit/Clipboard——已验证 API）
- Produces:
  - `type Services struct { Gateway bridge.Gateway; Providers bridge.Providers; Accounts bridge.Accounts; Models bridge.Models }`
  - `func Run(a *app.App) error`（Bind → WhenReady 建窗口/托盘 → 状态桥接 → App.Run）
  - `type uiState struct{...}` 及最小视图（状态文本 + Start/Stop）；Task 13-15 扩展
  - `var closeEducated bool` 语义：首次关窗 → `e.PreventDefault()` + 视图内显示驻留说明；用户点"继续关闭"后放行

- [ ] **Step 1: 生成托盘图标**

Run:

```bash
python3 - <<'EOF'
import zlib, struct
def chunk(t, d):
    c = struct.pack(">I", len(d)) + t + d
    return c + struct.pack(">I", zlib.crc32(t + d) & 0xffffffff)
w = h = 16
raw = b""
for y in range(h):
    raw += b"\x00" + bytes([40, 44, 52, 255] * w)
png = (b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0))
       + chunk(b"IDAT", zlib.compress(raw)) + chunk(b"IEND", b""))
open("internal/desktop/tray.png", "wb").write(png)
print("ok")
EOF
```

Expected: 输出 `ok`，文件存在。

- [ ] **Step 2: 实现 desktop.go 与替换入口**

`internal/desktop/desktop.go`：

```go
package desktop

import (
	"context"
	_ "embed"
	"log"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/telemetry"
)

//go:embed tray.png
var trayIcon []byte

type Services struct {
	Gateway   bridge.Gateway
	Providers bridge.Providers
	Accounts  bridge.Accounts
	Models    bridge.Models
}

type uiState struct {
	svc            Services
	status         app.StateInfo
	recent         []telemetry.RequestRecord
	token          string
	closeNotice    bool // 首关教育提示
	closeConfirmed bool // 用户已知晓，允许关闭
}

func newUIState(svc Services, a *app.App) *uiState {
	si := a.State()
	st, _ := a.CurrentSettings()
	return &uiState{svc: svc, status: si, recent: a.Recent(), token: st.GatewayToken}
}

func (u *uiState) view(c *ui.Context) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Textf(c, "网关状态: %s", u.status.State)
		if u.status.Addr != "" {
			ui.Textf(c, "监听: %s", u.status.Addr)
		}
		if u.closeNotice && !u.closeConfirmed {
			ui.Text(c, "窗口关闭后应用将驻留托盘，网关继续运行。")
			if ui.PrimaryButton(c, "继续关闭窗口").Clicked() {
				u.closeConfirmed = true
				if w := mygo.FocusedWindow(); w != nil {
					w.Close()
				}
			}
			if ui.Button(c, "取消").Clicked() {
				u.closeNotice = false
			}
		}
		if ui.PrimaryButton(c, "启动网关").Clicked() && u.status.State == "stopped" {
			go func() { _ = u.svc.Gateway.Start() }()
		}
		if ui.Button(c, "停止网关").Clicked() && (u.status.State == "running" || u.status.State == "degraded") {
			go func() { _ = u.svc.Gateway.Stop() }()
		}
	})
}

func Run(a *app.App) error {
	svc := Services{
		Gateway:   bridge.Gateway{A: a},
		Providers: bridge.Providers{A: a},
		Accounts:  bridge.Accounts{A: a, Creds: a.Creds()},
		Models:    bridge.Models{A: a},
	}
	mygo.Bind(svc.Gateway)
	mygo.Bind(svc.Providers)
	mygo.Bind(svc.Accounts)
	mygo.Bind(svc.Models)

	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}
	u := newUIState(svc, a)
	var win *mygo.Window

	mygo.App.WhenReady(func() {
		if tray, err := mygo.NewTray(mygo.TrayOptions{
			Icon:    trayIcon,
			ToolTip: "AgentSwitch",
			Menu: mygo.NewMenu([]*mygo.MenuItem{
				{Label: "打开主窗口", Click: func(*mygo.MenuItem, *mygo.Window) {
					if win != nil {
						win.Restore()
						win.Focus()
					} else {
						openWindow(u, &win)
					}
				}},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}),
		}); err != nil {
			a.Log().Info("tray unavailable (继续运行)", "err", err.Error())
		} else {
			_ = tray
		}
		openWindow(u, &win)
		win.OnClose(func(e *mygo.CloseEvent) {
			if !u.closeConfirmed {
				e.PreventDefault()
				u.closeNotice = true
				win.Update(func() {}) // 触发重绘显示提示
			}
		})
		a.OnState(func(si app.StateInfo) {
			if win != nil {
				win.Update(func() { u.status = si })
			}
		})
		a.OnRecord(func(rec telemetry.RequestRecord) {
			if win != nil {
				win.Update(func() {
					u.recent = append([]telemetry.RequestRecord{rec}, u.recent...)
					if len(u.recent) > 100 {
						u.recent = u.recent[:100]
					}
				})
			}
		})
	})

	// 关窗不退出：最后一个窗口关闭时保持运行（托盘驻留）
	mygo.App.OnWindowAllClosed(func() { /* 故意为空 */ })

	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		ctx, cancel := context.WithTimeout(context.Background(),
			time.Duration(graceSeconds(a))*time.Second)
		defer cancel()
		if err := a.StopGateway(ctx); err != nil {
			log.Println("stop gateway:", err)
		}
		if err := a.Close(); err != nil {
			log.Println("close app:", err)
		}
	})
	return mygo.App.Run()
}

func openWindow(u *uiState, win **mygo.Window) {
	*win = mygo.NewWindow(mygo.WindowOptions{
		Title: "AgentSwitch", Width: 1080, Height: 720, StateKey: "main",
		Content: ui.View(u.view),
	})
}

func graceSeconds(a *app.App) int {
	st, err := a.CurrentSettings()
	if err != nil || st.ShutdownGraceSeconds <= 0 {
		return 15
	}
	return st.ShutdownGraceSeconds
}
```

`app` 包配套（Task 10 的 App 增加两个小方法，放回 `internal/app/app.go`）：

```go
func (a *App) CurrentSettings() (config.Settings, error) { return a.settings() }
func (a *App) Creds() credential.Store                   { return a.creds }
```

（`Creds()` 在 Task 11 已约定；`CurrentSettings` 供 desktop 使用。若 Task 11 已加 `Creds()` 则此处只加 `CurrentSettings`。）

`cmd/agentswitch/main.go` 整体替换为：

```go
package main

import (
	"log"
	"os"
	"path/filepath"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/desktop"
)

func main() {
	base, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	dir := filepath.Join(base, "AgentSwitch")
	a, err := app.Open(app.Options{
		DBPath:  filepath.Join(dir, "agentswitch.db"),
		LogPath: filepath.Join(dir, "logs", "app.log"),
		Version: "0.1.0",
		Host:    "127.0.0.1",
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := desktop.Run(a); err != nil {
		log.Fatal(err)
	}
}
```

- [ ] **Step 3: 状态驱动视图测试**

`internal/desktop/desktop_test.go`：

```go
package desktop

import (
	"path/filepath"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
	"github.com/egoist/mygo/ui"
)

func testUI(t *testing.T) (*uiState, *app.App) {
	t.Helper()
	a, err := app.Open(app.Options{
		DBPath: filepath.Join(t.TempDir(), "d.db"), LogPath: filepath.Join(t.TempDir(), "d.log"), Host: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	svc := Services{
		Gateway: bridge.Gateway{A: a}, Providers: bridge.Providers{A: a},
		Accounts: bridge.Accounts{A: a, Creds: a.Creds()}, Models: bridge.Models{A: a},
	}
	return newUIState(svc, a), a
}

func TestOverviewShowsState(t *testing.T) {
	u, _ := testUI(t)
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("stopped") {
		t.Fatalf("texts %v", tt.Texts())
	}
}

func TestCloseNoticeBlocksFirstClose(t *testing.T) {
	u, _ := testUI(t)
	u.closeNotice = true
	tt := ui.NewTester(u.view, 800, 600)
	if !tt.HasText("驻留托盘") {
		t.Fatalf("notice missing: %v", tt.Texts())
	}
	if err := tt.Click("继续关闭窗口"); err != nil {
		t.Fatal(err)
	}
	if !u.closeConfirmed {
		t.Fatal("confirm not recorded")
	}
}
```

- [ ] **Step 4: 构建与测试通过**

Run: `go build ./... && go test ./internal/desktop/ ./internal/app/ -v`
Expected: 全部 PASS

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/ cmd/agentswitch/ internal/app/
git commit -m "feat: desktop（窗口/托盘/关窗拦截/退出钩子/状态桥接，入口接线）"
```

---

### Task 13: UI 概览页（令牌、配置片段、设置、最近请求）

**Files:**
- Modify: `internal/desktop/desktop.go`（扩展 uiState 与 view：页面容器 + 概览）
- Create: `internal/desktop/overview.go`
- Create: `internal/desktop/overview_test.go`

**Interfaces:**
- Consumes: Task 12 的 `uiState`；`mygo.Clipboard.Write(transfer.TextData(s))`（已验证）；`ui.Table/ListState`（已验证）
- Produces（扩展 uiState）：
  - `type uiState struct { svc Services; status app.StateInfo; recent []telemetry.RequestRecord; token string; showToken bool; page string; portInput, timeoutInput string; settingsMsg string; table ui.ListState; closeNotice, closeConfirmed bool }`
  - `func (u *uiState) overviewView(c *ui.Context)`（desktop.go 的 view 改为按 u.page 分发：`"overview"` 默认）

- [ ] **Step 1: 视图测试（失败先行）**

`internal/desktop/overview_test.go`：

```go
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestOverviewTokenAndSnippet(t *testing.T) {
	u, a := testUI(t)
	st, _ := a.CurrentSettings()
	u.token = st.GatewayToken
	tt := ui.NewTester(u.overviewView, 900, 700)
	if !tt.HasText("访问令牌") || !tt.HasText("ANTHROPIC_BASE_URL") {
		t.Fatalf("texts %v", tt.Texts())
	}
	if err := tt.Click("显示"); err != nil {
		t.Fatal(err)
	}
	if !u.showToken {
		t.Fatal("showToken toggle failed")
	}
	if err := tt.Click("重新生成令牌"); err != nil {
		t.Fatal(err)
	}
	if !u.confirmRegen {
		t.Fatal("regen must require confirm click")
	}
	if err := tt.Click("确认重新生成"); err != nil {
		t.Fatal(err)
	}
	if u.confirmRegen {
		t.Fatal("confirm cleared")
	}
}

func TestOverviewRecentTableRenders(t *testing.T) {
	u, _ := testUI(t)
	u.recent = recent3()
	tt := ui.NewTester(u.overviewView, 1000, 700)
	if !tt.HasText("模型") || !tt.HasText("耗时") {
		t.Fatalf("table headers missing: %v", tt.Texts())
	}
}
```

`recent3()` 辅助（放同测试文件）：

```go
func recent3() []telemetry.RequestRecord {
	return []telemetry.RequestRecord{
		{Model: "claude-opus", Account: "a1", Status: 200, DurationMs: 1200},
		{Model: "claude-sonnet", Account: "a1", Status: 429, DurationMs: 90, ErrorClass: "rate_limited"},
		{Model: "claude-opus", Account: "a1", Status: 200, DurationMs: 800},
	}
}
```

- [ ] **Step 2: 确认失败** → Run: `go test ./internal/desktop/ -run Overview -v` → FAIL

- [ ] **Step 3: 扩展 uiState 并实现 overview.go**

在 `desktop.go` 的 `uiState` 增加字段：`showToken bool`、`page string`（初始 `"overview"`）、`portInput, timeoutInput string`、`settingsMsg string`、`table ui.ListState`、`confirmRegen bool`、`settingsOpen bool`。`view` 改为：

```go
func (u *uiState) view(c *ui.Context) {
	switch u.page {
	default:
		u.overviewView(c)
	}
}
```

`internal/desktop/overview.go`：

```go
package desktop

import (
	"fmt"
	"strconv"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
)

func (u *uiState) overviewView(c *ui.Context) {
	ui.Column(c).Gap(8).Children(func() {
		ui.Textf(c, "网关状态: %s", u.status.State)
		if u.status.Addr != "" {
			ui.Textf(c, "监听地址: %s", u.status.Addr)
		}
		if u.status.DegradedReason != "" {
			ui.Textf(c, "降级原因: %s", u.status.DegradedReason)
		}

		// 操作行
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, "启动网关").Clicked() && u.status.State == "stopped" {
				go func() { _ = u.svc.Gateway.Start() }()
			}
			if ui.Button(c, "停止网关").Clicked() &&
				(u.status.State == "running" || u.status.State == "degraded") {
				go func() { _ = u.svc.Gateway.Stop() }()
			}
			if ui.Button(c, "设置").Clicked() {
				u.settingsOpen = !u.settingsOpen
				st, _ := u.svc.Gateway.Status()
				u.portInput, u.timeoutInput = "17890", "120"
				_ = st
			}
		})

		// 设置面板
		if u.settingsOpen {
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "端口:")
				ui.TextInput(c, &u.portInput)
				ui.Text(c, "请求超时（秒）:")
				ui.TextInput(c, &u.timeoutInput)
				if ui.PrimaryButton(c, "保存设置").Clicked() {
					port, err1 := strconv.Atoi(u.portInput)
					to, err2 := strconv.Atoi(u.timeoutInput)
					if err1 != nil || err2 != nil {
						u.settingsMsg = "端口/超时必须是整数"
					} else {
						restart, err := u.svc.Gateway.SaveSettings(port, to)
						if err != nil {
							u.settingsMsg = err.Error()
						} else if restart {
							u.settingsMsg = "已保存；重启网关后生效"
						} else {
							u.settingsMsg = "已保存"
						}
					}
				}
				if u.settingsMsg != "" {
					ui.Text(c, u.settingsMsg)
				}
			})
		}

		// 令牌
		ui.Text(c, "访问令牌:")
		masked := "***"
		if u.showToken {
			masked = u.token
		}
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, masked)
			label := "显示"
			if u.showToken {
				label = "隐藏"
			}
			if ui.Button(c, label).Clicked() {
				u.showToken = !u.showToken
			}
			if ui.Button(c, "复制令牌").Clicked() {
				mygo.Clipboard.Write(transfer.TextData(u.token))
			}
			if ui.Button(c, "重新生成令牌").Clicked() {
				u.confirmRegen = !u.confirmRegen
			}
			if u.confirmRegen {
				if ui.Button(c, "确认重新生成").Clicked() {
					go func() {
						tok, err := u.svc.Gateway.RegenerateToken()
						if err == nil {
							u2 := u
							// 令牌刷新在主线程做最稳妥；RegenerateToken 很快，直接同步也行
							_ = u2
							_ = tok
						}
					}()
					u.confirmRegen = false
				}
			}
		})

		// Claude Code 配置片段
		ui.Text(c, "Claude Code 配置片段:")
		snippet := fmt.Sprintf("export ANTHROPIC_BASE_URL=http://127.0.0.1:17890\nexport ANTHROPIC_AUTH_TOKEN=%s", u.token)
		ui.TextArea(c, &snippet).ReadOnly(true)
		if ui.Button(c, "复制配置片段").Clicked() {
			mygo.Clipboard.Write(transfer.TextData(snippet))
		}

		// 首关教育
		if u.closeNotice && !u.closeConfirmed {
			ui.Text(c, "窗口关闭后应用将驻留托盘，网关继续运行。")
			if ui.PrimaryButton(c, "继续关闭窗口").Clicked() {
				u.closeConfirmed = true
				if w := mygo.FocusedWindow(); w != nil {
					w.Close()
				}
			}
			if ui.Button(c, "取消").Clicked() {
				u.closeNotice = false
			}
		}

		// 最近请求
		ui.Text(c, "最近请求:")
		cols := []ui.TableColumn{
			{Title: "时间"}, {Title: "模型"}, {Title: "账号"}, {Title: "状态"}, {Title: "耗时(ms)"}, {Title: "错误"},
		}
		ui.Table(c, &u.table, cols, len(u.recent), func(row, col int) {
			rec := u.recent[row]
			switch col {
			case 0:
				ui.Text(c, rec.StartedAt.Format("15:04:05")).SingleLine()
			case 1:
				ui.Text(c, rec.Model).SingleLine()
			case 2:
				ui.Text(c, rec.Account).SingleLine()
			case 3:
				ui.Text(c, strconv.Itoa(rec.Status)).SingleLine()
			case 4:
				ui.Text(c, strconv.FormatInt(rec.DurationMs, 10)).SingleLine()
			case 5:
				ui.Text(c, rec.ErrorClass).SingleLine()
			}
		}).Grow(1)
	})
}
```

实现时修正两处：
1. `u.svc.Gateway.Status()` 返回 `Status` 非 (Status, error)——设置面板打开时读当前值：`st := u.svc.Gateway.Status(); u.portInput = strconv.Itoa(portOf(st))` 有难度（Status 不含 port）。改为：设置面板打开时从 `u.svc` 无直接端口来源——**约定**：`Status` 增加字段 `Port int` 与 `TimeoutSeconds int`（bridge.Status 补这两个字段，`Gateway.Status()` 从 settings 填充）。测试与实现按此对齐。
2. 重新生成令牌后的刷新：把 goroutine 去掉，点击"确认重新生成"时同步调用 `tok, err := u.svc.Gateway.RegenerateToken(); if err == nil { u.token = tok; u.showToken = false }`。
3. `ui.TextArea(c, &snippet).ReadOnly(true)`——若 `TextArea` 无 ReadOnly 链式方法（未验证），降级为 `ui.Text(c, snippet)` 多行文本展示（Text 支持换行渲染；若单行则逐行 `ui.Text`）。实现时若编译报错，用逐行 `ui.Text` 方案。

- [ ] **Step 4: 通过** → Run: `go build ./... && go test ./internal/desktop/ -v` → PASS

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/ internal/bridge/
git commit -m "feat: UI 概览页（状态/操作/令牌/配置片段/设置/最近请求表）"
```

---

### Task 14: UI 供应商页与页面导航

**Files:**
- Create: `internal/desktop/providers.go`
- Modify: `internal/desktop/desktop.go`（uiState 增加 providers 页状态与导航行；view 分发）
- Create: `internal/desktop/providers_test.go`

**Interfaces:**
- Consumes: `uiState`；`bridge.Providers`（List/Create/Update/Remove/Test，签名见 Task 11）；`ui.Select(&s, []string).Label(...)`、`ui.Switch(&bool)`、`ui.TextInput(&string)`（已验证）
- Produces:
  - `uiState` 增加：`provSelected int`、`provList ui.ListState`、`provForm struct{ name, protocol, baseURL string; enabled, allowPrivate bool; msg string; editID string }`、`provConfirmDelete bool`、`provTestMsg string`
  - `func (u *uiState) providersView(c *ui.Context)`
  - 导航：view 顶部渲染页面按钮行（"概览"、"供应商"），点击切换 `u.page`

- [ ] **Step 1: 视图测试（失败先行）**

`internal/desktop/providers_test.go`：

```go
package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestProvidersFormAndCreate(t *testing.T) {
	u, _ := testUI(t)
	u.page = "providers"
	tt := ui.NewTester(u.providersView, 1000, 700)
	if !tt.HasText("供应商") {
		t.Fatalf("texts %v", tt.Texts())
	}
	u.provForm.name = "main"
	u.provForm.protocol = "anthropic"
	u.provForm.baseURL = "https://api.example.com"
	u.provForm.enabled = true
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	// Create 会走网络校验？不会：ValidateBaseURL 是纯解析。List 应出现
	if got := u.svc.Providers.List(); len(got) != 1 || got[0].Name != "main" {
		t.Fatalf("providers %+v", got)
	}
}

func TestProvidersRejectBadProtocol(t *testing.T) {
	u, _ := testUI(t)
	u.page = "providers"
	tt := ui.NewTester(u.providersView, 1000, 700)
	u.provForm.name = "x"
	u.provForm.protocol = "openai"
	u.provForm.baseURL = "https://x.example.com"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	if u.provForm.msg == "" {
		t.Fatal("protocol error must surface in form msg")
	}
}
```

- [ ] **Step 2: 确认失败** → Run: `go test ./internal/desktop/ -run Providers -v` → FAIL

- [ ] **Step 3: 扩展 uiState 与实现 providers.go**

`desktop.go` 中 `view` 顶部加导航并分发：

```go
func (u *uiState) view(c *ui.Context) {
	ui.Row(c).Gap(8).Children(func() {
		if ui.Button(c, "概览").Clicked() {
			u.page = "overview"
		}
		if ui.Button(c, "供应商").Clicked() {
			u.page = "providers"
		}
	})
	switch u.page {
	case "providers":
		u.providersView(c)
	default:
		u.overviewView(c)
	}
}
```

`uiState` 增加（Task 13 的字段之外）：`provSelected int`、`provList ui.ListState`、`provForm provFormData`、`provConfirmDelete bool`、`provTestMsg string`，其中：

```go
type provFormData struct {
	name, protocol, baseURL string
	enabled, allowPrivate   bool
	msg                     string
	editID                  string // 非空 = 编辑该 id
}
```

`internal/desktop/providers.go`：

```go
package desktop

import (
	"github.com/egoist/mygo/ui"

	"AgentSwitch/internal/bridge"
)

func (u *uiState) providersView(c *ui.Context) {
	provs := u.svc.Providers.List()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "供应商")
		cols := []ui.TableColumn{{Title: "名称"}, {Title: "协议"}, {Title: "Base URL"}, {Title: "启用"}}
		ui.Table(c, &u.provList, cols, len(provs), func(row, col int) {
			p := provs[row]
			switch col {
			case 0:
				ui.Text(c, p.Name).SingleLine()
			case 1:
				ui.Text(c, p.Protocol).SingleLine()
			case 2:
				ui.Text(c, p.BaseURL).SingleLine()
			case 3:
				if p.Enabled {
					ui.Text(c, "是")
				} else {
					ui.Text(c, "否")
				}
			}
		}).Grow(1)

		// 表单
		ui.Text(c, "名称:")
		ui.TextInput(c, &u.provForm.name)
		ui.Text(c, "协议:")
		ui.Select(c, &u.provForm.protocol, []string{"anthropic"})
		ui.Text(c, "Base URL:")
		ui.TextInput(c, &u.provForm.baseURL)
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "启用")
			ui.Switch(c, &u.provForm.enabled)
			ui.Text(c, "允许私网上游")
			ui.Switch(c, &u.provForm.allowPrivate)
		})
		if u.provForm.msg != "" {
			ui.Text(c, u.provForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			saveLabel := "保存"
			if u.provForm.editID != "" {
				saveLabel = "更新"
			}
			if ui.PrimaryButton(c, saveLabel).Clicked() {
				if u.provForm.editID == "" {
					p, err := u.svc.Providers.Create(u.provForm.name, u.provForm.protocol,
						u.provForm.baseURL, u.provForm.enabled, u.provForm.allowPrivate)
					if err != nil {
						u.provForm.msg = err.Error()
					} else {
						u.provForm = provFormData{protocol: "anthropic", enabled: true}
						_ = p
					}
				} else {
					err := u.svc.Providers.Update(u.provForm.editID, u.provForm.name,
						u.provForm.baseURL, u.provForm.enabled, u.provForm.allowPrivate)
					if err != nil {
						u.provForm.msg = err.Error()
					} else {
						u.provForm = provFormData{protocol: "anthropic", enabled: true}
					}
				}
			}
			if ui.Button(c, "载入所选").Clicked() && u.provSelected < len(provs) {
				p := provs[u.provSelected]
				u.provForm = provFormData{name: p.Name, protocol: p.Protocol, baseURL: p.BaseURL,
					enabled: p.Enabled, allowPrivate: p.AllowPrivateUpstream, editID: p.ID}
			}
			if ui.Button(c, "删除").Clicked() && u.provForm.editID != "" {
				u.provConfirmDelete = true
			}
			if u.provConfirmDelete {
				if ui.Button(c, "确认删除").Clicked() {
					if err := u.svc.Providers.Remove(u.provForm.editID); err != nil {
						u.provForm.msg = err.Error()
					}
					u.provConfirmDelete = false
					u.provForm = provFormData{protocol: "anthropic", enabled: true}
				}
			}
			if ui.Button(c, "测试连通").Clicked() && u.provForm.editID != "" {
				id := u.provForm.editID
				go func() {
					res := u.svc.Providers.Test(id)
					_ = res // 结果行展示见下
				}()
			}
		})
		if u.provTestMsg != "" {
			ui.Text(c, u.provTestMsg)
		}
		_ = bridge.ProviderView{}
	})
}
```

实现时修正：`测试连通` 的结果展示——改为同步调用（Probe 最多 15s，可接受）：`res := u.svc.Providers.Test(u.provForm.editID); if res.OK { u.provTestMsg = "连通正常 ("+res.Stage+")" } else { u.provTestMsg = res.Stage+" 失败: "+res.Error }`。行选择：`u.provList.Selected` 是 `*int`（table.md 验证）——`载入所选` 使用 `sel := u.provList.Selected; if sel != nil { p := provs[*sel]; ... }`。

- [ ] **Step 4: 通过** → Run: `go build ./... && go test ./internal/desktop/ -v` → PASS

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/
git commit -m "feat: UI 供应商页（CRUD、私网开关、连通测试、删除确认）与导航"
```

---

### Task 15: UI 账号页与模型页

**Files:**
- Create: `internal/desktop/accounts.go`
- Create: `internal/desktop/models.go`
- Modify: `internal/desktop/desktop.go`（uiState 扩展、导航加两项）
- Create: `internal/desktop/accounts_test.go`
- Create: `internal/desktop/models_test.go`

**Interfaces:**
- Consumes: `bridge.Accounts`（List/Create/Update/SetEnabled/RotateSecret/Remove）、`bridge.Models`、`bridge.Providers.List`（账号表单的供应商下拉）；Task 14 的页面模式
- Produces:
  - `uiState` 增加：`accForm struct{ name, providerID, apiKey string; priority int; enabled, allowFallback bool; msg, editID string }`、`accList ui.ListState`、`accConfirmDelete bool`
  - `uiState` 增加：`modelForm struct{ public, upstream, msg, editID string }`、`modelList ui.ListState`
  - `func (u *uiState) accountsView(c *ui.Context)`、`func (u *uiState) modelsView(c *ui.Context)`
  - 导航行加 "账号"、"模型" 按钮

- [ ] **Step 1: 视图测试（失败先行）**

`internal/desktop/accounts_test.go`：

```go
package desktop

import (
	"path/filepath"
	"testing"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/bridge"
	"AgentSwitch/internal/credential"
	"AgentSwitch/internal/storage"
	"github.com/egoist/mygo/ui"
)

func testUIWithCreds(t *testing.T) (*uiState, *app.App, credential.Store) {
	t.Helper()
	a, err := app.Open(app.Options{
		DBPath: filepath.Join(t.TempDir(), "a.db"), LogPath: filepath.Join(t.TempDir(), "a.log"), Host: "127.0.0.1",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	creds := credential.Manager{
		Primary:       credential.NewFallbackFile(filepath.Join(t.TempDir(), "s.json")),
		AllowFallback: true,
	}
	svc := Services{
		Gateway: bridge.Gateway{A: a}, Providers: bridge.Providers{A: a},
		Accounts: bridge.Accounts{A: a, Creds: creds}, Models: bridge.Models{A: a},
	}
	return newUIState(svc, a), a, creds
}

func TestAccountsCreateFlow(t *testing.T) {
	u, a, _ := testUIWithCreds(t)
	if err := a.Store().Providers().Save(storage.Provider{
		ID: "p1", Name: "main", Protocol: "anthropic", BaseURL: "https://api.example.com", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	u.page = "accounts"
	tt := ui.NewTester(u.accountsView, 1000, 700)
	if !tt.HasText("账号") {
		t.Fatalf("texts %v", tt.Texts())
	}
	u.accForm.name = "acc1"
	u.accForm.providerID = "p1"
	u.accForm.apiKey = "sk-ant-secret1234"
	u.accForm.priority = 10
	u.accForm.enabled = true
	u.accForm.allowFallback = true
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	got := u.svc.Accounts.List()
	if len(got) != 1 || got[0].Name != "acc1" || got[0].KeyTail != "1234" {
		t.Fatalf("accounts %+v", got)
	}
	if u.accForm.apiKey != "" {
		t.Fatal("api key input must be cleared after save")
	}
}

func TestModelsCreateFlow(t *testing.T) {
	u, _, _ := testUIWithCreds(t)
	u.page = "models"
	tt := ui.NewTester(u.modelsView, 900, 600)
	u.modelForm.public = "claude-opus"
	u.modelForm.upstream = "claude-opus-4-8"
	if err := tt.Click("保存"); err != nil {
		t.Fatal(err)
	}
	got := u.svc.Models.List()
	if len(got) != 1 || got[0].PublicModel != "claude-opus" {
		t.Fatalf("models %+v", got)
	}
}
```

- [ ] **Step 2: 确认失败** → Run: `go test ./internal/desktop/ -run 'Accounts|Models' -v` → FAIL

- [ ] **Step 3: 实现 accounts.go / models.go 并扩展导航**

`desktop.go` 导航行加：

```go
if ui.Button(c, "账号").Clicked() {
	u.page = "accounts"
}
if ui.Button(c, "模型").Clicked() {
	u.page = "models"
}
```

`view` 分发加 `case "accounts": u.accountsView(c)` 与 `case "models": u.modelsView(c)`。`uiState` 增加 Interfaces 块中的字段（`accForm` 内嵌 struct 逐字段声明；`priority` 输入用字符串 `priorityInput string` 更稳，保存时 `strconv.Atoi`——按此实现）。

`internal/desktop/accounts.go`：

```go
package desktop

import (
	"strconv"

	"github.com/egoist/mygo/ui"
)

type accFormData struct {
	name, providerID, apiKey, priorityInput string
	enabled, allowFallback                  bool
	msg, editID                             string
}

func (u *uiState) accountsView(c *ui.Context) {
	accounts := u.svc.Accounts.List()
	provs := u.svc.Providers.List()
	provNames := make([]string, 0, len(provs))
	provByID := map[string]string{}
	for _, p := range provs {
		provNames = append(provNames, p.Name)
		provByID[p.Name] = p.ID
	}
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "账号")
		if len(u.accForm.msg) == 0 && credentialFallbackInUse(accounts) {
			ui.Text(c, "警告：凭据使用文件降级存储（未启用系统密钥环）")
		}
		cols := []ui.TableColumn{{Title: "名称"}, {Title: "供应商"}, {Title: "优先级"}, {Title: "启用"}, {Title: "密钥尾4"}, {Title: "存储"}}
		ui.Table(c, &u.accList, cols, len(accounts), func(row, col int) {
			a := accounts[row]
			pname := a.ProviderID
			for _, p := range provs {
				if p.ID == a.ProviderID {
					pname = p.Name
				}
			}
			switch col {
			case 0:
				ui.Text(c, a.Name).SingleLine()
			case 1:
				ui.Text(c, pname).SingleLine()
			case 2:
				ui.Text(c, strconv.Itoa(a.Priority)).SingleLine()
			case 3:
				if a.Enabled {
					ui.Text(c, "是")
				} else {
					ui.Text(c, "否")
				}
			case 4:
				ui.Text(c, a.KeyTail).SingleLine()
			case 5:
				ui.Text(c, a.SecretBackend).SingleLine()
			}
		}).Grow(1)

		ui.Text(c, "名称:")
		ui.TextInput(c, &u.accForm.name)
		ui.Text(c, "供应商:")
		ui.Select(c, &u.accForm.providerName, provNames)
		ui.Text(c, "API Key（保存后不可回显）:")
		ui.TextInput(c, &u.accForm.apiKey)
		ui.Text(c, "优先级（数字越小越优先）:")
		ui.TextInput(c, &u.accForm.priorityInput)
		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "启用")
			ui.Switch(c, &u.accForm.enabled)
			ui.Text(c, "允许文件降级")
			ui.Switch(c, &u.accForm.allowFallback)
		})
		if u.accForm.msg != "" {
			ui.Text(c, u.accForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			label := "保存"
			if u.accForm.editID != "" {
				label = "更新"
			}
			if ui.PrimaryButton(c, label).Clicked() {
				u.submitAccount(provByID)
			}
			if ui.Button(c, "载入所选").Clicked() && u.accList.Selected != nil {
				a := accounts[*u.accList.Selected]
				u.accForm = accFormData{name: a.Name, priorityInput: strconv.Itoa(a.Priority),
					enabled: a.Enabled, editID: a.ID}
				for _, p := range provs {
					if p.ID == a.ProviderID {
						u.accForm.providerName = p.Name
					}
				}
			}
			if ui.Button(c, "停用/启用").Clicked() && u.accList.Selected != nil {
				a := accounts[*u.accList.Selected]
				_ = u.svc.Accounts.SetEnabled(a.ID, !a.Enabled)
			}
			if ui.Button(c, "轮换密钥").Clicked() && u.accForm.editID != "" && u.accForm.apiKey != "" {
				if err := u.svc.Accounts.RotateSecret(u.accForm.editID, u.accForm.apiKey, u.accForm.allowFallback); err != nil {
					u.accForm.msg = err.Error()
				} else {
					u.accForm.msg = "密钥已轮换"
					u.accForm.apiKey = ""
				}
			}
			if ui.Button(c, "删除").Clicked() && u.accForm.editID != "" {
				u.accConfirmDelete = true
			}
			if u.accConfirmDelete {
				if ui.Button(c, "确认删除").Clicked() {
					if err := u.svc.Accounts.Remove(u.accForm.editID); err != nil {
						u.accForm.msg = err.Error()
					}
					u.accConfirmDelete = false
					u.accForm = accFormData{}
				}
			}
		})
	})
}

func (u *uiState) submitAccount(provByID map[string]string) {
	pid := provByID[u.accForm.providerName]
	if pid == "" {
		u.accForm.msg = "请先在供应商页创建供应商"
		return
	}
	prio, err := strconv.Atoi(u.accForm.priorityInput)
	if err != nil {
		u.accForm.msg = "优先级必须是整数"
		return
	}
	if u.accForm.editID == "" {
		av, err := u.svc.Accounts.Create(u.accForm.name, pid, u.accForm.apiKey, prio,
			u.accForm.enabled, u.accForm.allowFallback)
		if err != nil {
			u.accForm.msg = err.Error()
			return
		}
		u.accForm = accFormData{enabled: true}
		_ = av
	} else {
		if u.accForm.apiKey != "" {
			if err := u.svc.Accounts.RotateSecret(u.accForm.editID, u.accForm.apiKey, u.accForm.allowFallback); err != nil {
				u.accForm.msg = err.Error()
				return
			}
		}
		if err := u.svc.Accounts.Update(u.accForm.editID, u.accForm.name, prio); err != nil {
			u.accForm.msg = err.Error()
			return
		}
		u.accForm = accFormData{enabled: true}
	}
}

func credentialFallbackInUse(views []bridge.AccountView) bool {
	for _, v := range views {
		if v.SecretBackend == "fallback-file" {
			return true
		}
	}
	return false
}
```

`uiState` 里 `accForm` 的类型是 `accFormData`（含 `providerName` 字段——实现时把它加进 `accFormData`：`providerName string`），初始 `enabled: true` 在 `newUIState` 或首次进入页时设置。

`internal/desktop/models.go`：

```go
package desktop

import (
	"github.com/egoist/mygo/ui"
)

type modelFormData struct {
	public, upstream, msg, editID string
}

func (u *uiState) modelsView(c *ui.Context) {
	models := u.svc.Models.List()
	ui.Column(c).Gap(8).Children(func() {
		ui.Text(c, "模型别名（Agent 请求名 → 上游模型名）")
		cols := []ui.TableColumn{{Title: "公开模型名"}, {Title: "上游模型名"}}
		ui.Table(c, &u.modelList, cols, len(models), func(row, col int) {
			m := models[row]
			if col == 0 {
				ui.Text(c, m.PublicModel).SingleLine()
			} else {
				ui.Text(c, m.UpstreamModel).SingleLine()
			}
		}).Grow(1)
		ui.Text(c, "公开模型名:")
		ui.TextInput(c, &u.modelForm.public)
		ui.Text(c, "上游模型名:")
		ui.TextInput(c, &u.modelForm.upstream)
		if u.modelForm.msg != "" {
			ui.Text(c, u.modelForm.msg)
		}
		ui.Row(c).Gap(8).Children(func() {
			if ui.PrimaryButton(c, "保存").Clicked() {
				if u.modelForm.editID == "" {
					if _, err := u.svc.Models.Create(u.modelForm.public, u.modelForm.upstream); err != nil {
						u.modelForm.msg = err.Error()
					} else {
						u.modelForm = modelFormData{}
					}
				} else {
					if err := u.svc.Models.Update(u.modelForm.editID, u.modelForm.public, u.modelForm.upstream); err != nil {
						u.modelForm.msg = err.Error()
					} else {
						u.modelForm = modelFormData{}
					}
				}
			}
			if ui.Button(c, "载入所选").Clicked() && u.modelList.Selected != nil {
				m := models[*u.modelList.Selected]
				u.modelForm = modelFormData{public: m.PublicModel, upstream: m.UpstreamModel, editID: m.ID}
			}
			if ui.Button(c, "删除").Clicked() && u.modelForm.editID != "" {
				if err := u.svc.Models.Remove(u.modelForm.editID); err != nil {
					u.modelForm.msg = err.Error()
				} else {
					u.modelForm = modelFormData{}
				}
			}
		})
	})
}
```

（模型删除按 spec 需二次确认——把"删除"点击改为 `u.modelConfirmDelete = true` + "确认删除"按钮，模式同供应商页。）

- [ ] **Step 4: 通过** → Run: `go build ./... && go test ./internal/desktop/ -v` → PASS

- [ ] **Step 5: Commit**

```bash
git add internal/desktop/
git commit -m "feat: UI 账号页与模型页（CRUD、密钥轮换、降级警示、别名映射）"
```

---

### Task 16: 端到端冒烟与 README

**Files:**
- Create: `README.md`
- Modify: `CLAUDE.md`（更新常用命令）
- Test: `internal/app/smoke_test.go`（端到端：经 app 启动网关 → 真实 HTTP 调用 → 改别名即刻生效）

**Interfaces:**
- Consumes: 全部任务产出
- Produces: 可交接的开发/验收文档；一条"切换即生效"的自动化回归

- [ ] **Step 1: 端到端冒烟测试（失败先行）**

`internal/app/smoke_test.go`：

```go
package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"AgentSwitch/internal/storage"
)

// TestConfigChangeTakesEffectWithoutRestart 覆盖 spec §15 验收项 2：
// 网关运行中修改模型别名，下一次请求即走新配置（agent 无感）。
func TestConfigChangeTakesEffectWithoutRestart(t *testing.T) {
	var lastModel string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		lastModel = strings.Split(string(b), `"model":"`)[1]
		lastModel = strings.Split(lastModel, `"`)[0]
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":"ok"}`))
	}))
	defer up.Close()

	a := newTestApp(t, up.URL, true)
	if err := a.Store().Models().Save(storage.ModelMapping{
		ID: "m1", PublicModel: "alias", UpstreamModel: "model-v1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.StartGateway(); err != nil {
		t.Fatal(err)
	}
	call := func() string {
		req, _ := http.NewRequest("POST", "http://"+a.State().Addr+"/v1/messages",
			strings.NewReader(`{"model":"alias","messages":[]}`))
		req.Header.Set("Authorization", "Bearer "+a.currentToken())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("status %d: %s", resp.StatusCode, b)
		}
		return lastModel
	}
	if got := call(); got != "model-v1" {
		t.Fatalf("first call upstream model %q", got)
	}
	// 运行中改别名 → 下一请求生效（无需重启网关/agent）
	m, err := a.Store().Models().GetByPublic("alias")
	if err != nil {
		t.Fatal(err)
	}
	m.UpstreamModel = "model-v2"
	if err := a.Store().Models().Save(m); err != nil {
		t.Fatal(err)
	}
	if got := call(); got != "model-v2" {
		t.Fatalf("after alias change upstream model %q", got)
	}
	_ = context.Background
}
```

- [ ] **Step 2: 确认失败** → Run: `go test ./internal/app/ -run Smoke -v` → FAIL（函数名不同也行：`-run TestConfigChangeTakesEffectWithoutRestart`）

- [ ] **Step 3: 通过**

Run: `go test ./internal/app/ -run TestConfigChangeTakesEffectWithoutRestart -v`
Expected: PASS

- [ ] **Step 4: 全量回归**

Run: `go vet ./... && go test ./...`
Expected: 全部 PASS

- [ ] **Step 5: 写 README.md**

```markdown
# AgentSwitch

本地 AI Agent 模型网关 + 桌面控制面（MyGo 原生 UI，单二进制）。
Agent 指向 `127.0.0.1:17890`，供应商/账号/模型切换在网关层完成，agent 无需重启。

设计文档：`docs/superpowers/specs/2026-10-15-agentswitch-p0a-desktop-first-design.md`
架构文档（v2.0）：仓库根目录 docx。

## 开发

- 依赖：Go 1.27+（`GOTOOLCHAIN=auto` 会自动拉取）、Linux 需 WebKitGTK 不需要（原生 UI 只需 GTK3）
- 运行（开发模式，热重载）：`go tool mygo dev`
- 打包：`go tool mygo build`（产物在 `build/`，含 .deb 与 install.sh）
- 测试：`go test ./...`（全部离线，Fake Provider）

## 首次使用

1. 启动应用 → 供应商页添加上游（Base URL + 协议 anthropic）
2. 账号页添加 API Key（优先系统密钥环；无密钥环环境勾选"允许文件降级"）
3. 模型页添加别名（公开模型名 → 上游模型名）
4. 概览页启动网关，复制"Claude Code 配置片段"到 shell 环境
5. 用 Claude Code 发起真实请求；在 UI 改别名/账号即时生效，无需重启 agent

## 验收清单（对应 spec §15）

见 spec §15；核心自动化回归：`go test ./internal/app -run TestConfigChangeTakesEffectWithoutRestart`
```

同步修改 `CLAUDE.md` 的"常用命令"节：把 `go build ./...` 等保留，追加 `go tool mygo dev`（开发运行）与 `go tool mygo build`（打包），并注明入口为 `cmd/agentswitch`、UI 在 `internal/desktop`。

- [ ] **Step 6: Commit**

```bash
git add README.md CLAUDE.md internal/app/smoke_test.go
git commit -m "docs: README 与 CLAUDE.md 命令更新；端到端切换回归测试"
```

---

## 计划自审记录

1. **Spec 覆盖**：§2 决策 1-9 → Task 1/4/5/11/6/9；§5 进程模型 → Task 10/12；§6 包结构 → 各任务按目录落位；§7 数据模型 → Task 4；§8 凭据 → Task 5/11；§9 桥接 → Task 11；§10 流水线 → Task 7/8/9；§11 安全 → Task 6/9（令牌/限额/https/重定向/白名单）；§12 UI → Task 12-15；§13 日志 → Task 2/9/10；§14 测试 → 各任务 TDD + Task 16；§15 验收 → Task 16 + 手动清单（README）。无缺口。
2. **占位符**：无 TBD/TODO；Task 3/9/13 中的"实现时修正"是对已写代码的具体修正指令（含完整替代代码），非空缺。
3. **类型一致性**：`bridge.Accounts.Creds credential.Store` 与 Task 5 的 Manager 满足接口一致；`app.Creds()` 在 Task 11 约定、Task 12 使用；`gateway.Deps.ListAliases` 在 Task 9 修正指令中补入、Task 10 使用；`bridge.Status.Port/TimeoutSeconds` 在 Task 13 修正指令中补入；`accFormData.providerName` 在 Task 15 明确加入。