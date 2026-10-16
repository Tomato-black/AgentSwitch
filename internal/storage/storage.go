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

// Channel 是渠道聚合根（spec 2026-10-17 §3.1）：上游入口定义（类型/协议/BaseURL）
// 与路由优先级。
type Channel struct {
	ID                   string
	Name                 string
	Type                 string
	Protocol             string
	BaseURL              string
	Priority             int
	Enabled              bool
	AllowPrivateUpstream bool
	CreatedAt            time.Time
	UpdatedAt            time.Time
}

// ChannelKey 是渠道下的凭据引用（SecretKey 指向密钥存储本体；KeyTail 为非秘密尾 4）。
type ChannelKey struct {
	ID            string
	ChannelID     string
	Name          string
	SecretKey     string
	SecretBackend string
	KeyTail       string
	Priority      int
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// ChannelModel 是渠道级模型映射；同一公开名可存在于多个渠道（UNIQUE(channel_id, public_model)）。
type ChannelModel struct {
	ID            string
	ChannelID     string
	PublicModel   string
	UpstreamModel string
	Enabled       bool
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// Store 是持久层入口（过渡期新旧并存，Task 10 删旧）。
type Store interface {
	Providers() ProviderRepo
	Accounts() AccountRepo
	Models() ModelRepo
	Channels() ChannelRepo
	ChannelKeys() ChannelKeyRepo
	ChannelModels() ChannelModelRepo
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

// ChannelRepo 读写 channels 表；List 已按路由序（priority,id）。
type ChannelRepo interface {
	List() ([]Channel, error)
	Get(id string) (Channel, error)
	Save(c Channel) error
	Delete(id string) error
	SetEnabled(id string, on bool) error
	// HasUsableKey 报告是否存在 enabled 渠道×enabled 密钥（readyz/Degraded 口径）。
	HasUsableKey() (bool, error)
}

// ChannelKeyRepo 读写 channel_keys（secret_key 是凭据引用，本体在密钥存储）。
type ChannelKeyRepo interface {
	List() ([]ChannelKey, error)
	ListByChannel(channelID string) ([]ChannelKey, error)
	Get(id string) (ChannelKey, error)
	Save(k ChannelKey) error
	Delete(id string) error
	SetEnabled(id string, on bool) error
	CountByChannel(channelID string) (int, error)
}

// ChannelModelRepo 读写渠道级模型清单；UNIQUE(channel_id, public_model)。
type ChannelModelRepo interface {
	List() ([]ChannelModel, error)
	ListByChannel(channelID string) ([]ChannelModel, error)
	ListByPublic(public string) ([]ChannelModel, error)
	Get(id string) (ChannelModel, error)
	Save(m ChannelModel) error
	Delete(id string) error
	SetEnabled(id string, on bool) error
	// PublicModels 去重输出 enabled 公开模型名（/v1/models 并集）。
	PublicModels() ([]string, error)
	CountByChannel(channelID string) (int, error)
}
