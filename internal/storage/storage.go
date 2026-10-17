package storage

import (
	"errors"
)

// ErrNotFound 是所有 Repo 查询未命中时的哨兵错误（经 mapNotFound 映射）。
var ErrNotFound = errors.New("not found")

// Channel 是渠道聚合根（spec 2026-10-17-logical-models §5）：上游入口定义，
// 每渠道恰好一个凭据（CredentialRef 指向 OS 密钥存储）。时间戳为 Unix 毫秒。
type Channel struct {
	ID                   string
	Name                 string
	Protocol             string
	BaseURL              string
	CredentialRef        string
	PresetID             string
	Enabled              bool
	AllowPrivateUpstream bool
	CreatedAt            int64
	UpdatedAt            int64
}

// ChannelModel 是渠道的模型建议列表（添加逻辑模型目标时的下拉数据源），
// 不再是路由主体；UNIQUE(channel_id, model_name)。
type ChannelModel struct {
	ID        string
	ChannelID string
	ModelName string
	CreatedAt int64
}

// LogicalModel 是网关对外的逻辑模型（Name 为对外模型名），经目标链映射到
// 各渠道的具体模型。
type LogicalModel struct {
	ID          string
	Name        string
	Description string
	Enabled     bool
	CreatedAt   int64
	UpdatedAt   int64
}

// LogicalModelTarget 是逻辑模型目标链的一环；priority 越小越优先。
type LogicalModelTarget struct {
	ID             string
	LogicalModelID string
	ChannelID      string
	ModelName      string
	Priority       int
}

// Store 是持久层入口。
type Store interface {
	Channels() ChannelRepo
	ChannelModels() ChannelModelRepo
	LogicalModels() LogicalModelRepo
	LogicalModelTargets() LogicalModelTargetRepo
	Settings() SettingsRepo
	Close() error
}

// SettingsRepo 是 settings 表的 KV 存取；Get 缺失返回 ("", nil)。
type SettingsRepo interface {
	Get(key string) (string, error)
	Set(key, value string) error
}

// ChannelRepo 读写 channels 表（单 Key：credential_ref 非空即有可用凭据）。
type ChannelRepo interface {
	List() ([]Channel, error)
	Get(id string) (Channel, error)
	Save(c Channel) (Channel, error)
	Delete(id string) error
	SetEnabled(id string, on bool) error
	// HasUsableKey 报告是否存在 enabled 且 credential_ref 非空的渠道（readyz 口径）。
	HasUsableKey() (bool, error)
}

// ChannelModelRepo 读写渠道模型建议列表；UNIQUE(channel_id, model_name)。
type ChannelModelRepo interface {
	ListByChannel(channelID string) ([]ChannelModel, error)
	Save(m ChannelModel) (ChannelModel, error)
	Delete(id string) error
	DeleteByChannel(channelID string) error
	// Suggestions 返回渠道的模型名列表（目标编辑下拉数据源，按名排序）。
	Suggestions(channelID string) ([]string, error)
}

// LogicalModelRepo 读写 logical_models；name UNIQUE（网关对外模型名）。
type LogicalModelRepo interface {
	List() ([]LogicalModel, error)
	Get(id string) (LogicalModel, error)
	GetByName(name string) (LogicalModel, error)
	Save(m LogicalModel) (LogicalModel, error)
	Delete(id string) error
	SetEnabled(id string, on bool) error
}

// LogicalModelTargetRepo 读写 logical_model_targets；priority 越小越优先。
type LogicalModelTargetRepo interface {
	ListByModel(modelID string) ([]LogicalModelTarget, error)
	Save(t LogicalModelTarget) (LogicalModelTarget, error)
	Delete(id string) error
	DeleteByModel(modelID string) error
}
