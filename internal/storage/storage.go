package storage

import (
	"errors"
	"time"
)

// ErrNotFound 是所有 Repo 查询未命中时的哨兵错误（经 mapNotFound 映射）。
var ErrNotFound = errors.New("not found")

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

// Store 是持久层入口。
type Store interface {
	Channels() ChannelRepo
	ChannelKeys() ChannelKeyRepo
	ChannelModels() ChannelModelRepo
	Settings() SettingsRepo
	Close() error
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
