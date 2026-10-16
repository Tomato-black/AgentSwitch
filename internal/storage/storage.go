package storage

import (
	"errors"
	"time"
)

// ErrNotFound 是所有 Repo 查询未命中时的哨兵错误（经 mapNotFound 映射）。
var ErrNotFound = errors.New("not found")

// Provider 是上游服务商定义。
type Provider struct {
	ID                   string
	Name                 string
	Protocol             string
	BaseURL              string
	Enabled              bool
	AllowPrivateUpstream bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// Account 是某 Provider 下的凭据账户（secret 经 SecretBackend 指向的后端存取）。
type Account struct {
	ID            string
	ProviderID    string
	Name          string
	SecretKey     string
	SecretBackend string
	// KeyTail 是密钥本体尾 4 位（spec §12.4 列"密钥尾4"）：Create/Rotate 时由
	// bridge 写入，非秘密（只存尾 4）。迁移前建的历史行留空直到下次轮换。
	KeyTail   string
	Priority  int
	Enabled   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// ModelMapping 是 public model → upstream model 的映射。
type ModelMapping struct {
	ID            string
	PublicModel   string
	UpstreamModel string
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Store 是四表持久层入口。
type Store interface {
	Providers() ProviderRepo
	Accounts() AccountRepo
	Models() ModelRepo
	Settings() SettingsRepo
	Close() error
}

// ProviderRepo 读写 providers 表。
type ProviderRepo interface {
	List() ([]Provider, error)
	Get(id string) (Provider, error)
	Save(p Provider) error
	Delete(id string) error
}

// AccountRepo 读写 accounts 表（删除依赖 FK ON DELETE CASCADE）。
type AccountRepo interface {
	List() ([]Account, error)
	ListByProvider(pid string) ([]Account, error)
	Get(id string) (Account, error)
	Save(a Account) error
	Delete(id string) error
	SetEnabled(id string, on bool) error
}

// ModelRepo 读写 models 表；public_model 有 UNIQUE 约束。
type ModelRepo interface {
	List() ([]ModelMapping, error)
	GetByPublic(public string) (ModelMapping, error)
	Save(m ModelMapping) error
	Delete(id string) error
}

// SettingsRepo 是 settings 表的 KV 存取；Get 缺失返回 ("", nil)。
type SettingsRepo interface {
	Get(key string) (string, error)
	Set(key, value string) error
}
