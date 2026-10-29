// Package storage 是 SQLite 持久化层：内嵌 schema 迁移、Repository 接口及
// 实现（settings/channels/logical_models/access_configs/secrets/desktop
// alias）；渠道密钥密文走 AES-256-GCM（主密钥由 credential 维护）。
package storage

import (
	"errors"
)

// ErrNotFound 是所有 Repo 查询未命中时的哨兵错误（经 mapNotFound 映射）。
var ErrNotFound = errors.New("not found")

// Channel 是渠道聚合根（spec 2026-10-17-logical-models §5）：上游入口定义，
// 每渠道恰好一个凭据（CredentialRef 为 identity；密文在 channel_secrets，
// 主密钥 master.key 分离存放）。时间戳为 Unix 毫秒。
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
// 各渠道的具体模型。元数据列（migration v2，spec 2026-10-27）：DisplayName
// 与 ContextWindow 供 Agent 接入文档生成，ReasoningLevels 为可选推理档位
// （JSON 文本列），DefaultReasoningLevel 缺省档位。
type LogicalModel struct {
	ID                    string
	Name                  string
	Description           string
	DisplayName           string
	ContextWindow         int64
	ReasoningLevels       []string
	DefaultReasoningLevel string
	Enabled               bool
	CreatedAt             int64
	UpdatedAt             int64
}

// LogicalModelTarget 是逻辑模型目标链的一环；priority 越小越优先。
type LogicalModelTarget struct {
	ID             string
	LogicalModelID string
	ChannelID      string
	ModelName      string
	Priority       int
}

// AccessConfig 是一份 Agent 接入配置（spec 2026-10-18 agent-access-config，
// 2026-10-19 文档化升级）：按 app 分组，指向一个逻辑模型；同 app 名唯一、
// 至多一个 is_current。Settings 为整份 JSON 文本（默认 "{}"）。
type AccessConfig struct {
	ID             string
	AppType        string
	Name           string
	LogicalModelID string
	Settings       string // 整份 JSON 文档（spec 2026-10-19）；空读写归一化为 "{}"
	IsCurrent      bool
	NeedsApply     bool // current 配置有未投影到 Agent live 文件的修改
	SortOrder      int
	CreatedAt      int64
	UpdatedAt      int64
}

// RequestAttempt 是请求历史中的一次故障转移尝试/跳过记录（脱敏元数据）。
type RequestAttempt struct {
	Channel, Model, Outcome, Reason string
	DurationMs                      int64
}

// RequestHistoryRecord 是持久化的请求记录：只含脱敏元数据，不含
// prompt/response 正文、Authorization 或密钥。
type RequestHistoryRecord struct {
	ID            string
	StartedAt     int64 // Unix 毫秒
	Model         string
	UpstreamModel string
	Channel       string
	Account       string
	Status        int
	DurationMs    int64
	FailoverCount int
	Streaming     bool
	InputTokens   int
	OutputTokens  int
	ErrorClass    string
	Attempts      []RequestAttempt
}

// UsageAggregate 是按渠道·模型聚合的 token 用量。
type UsageAggregate struct {
	Channel, Model            string
	Requests                  int64
	InputTokens, OutputTokens int64
}

// DailyUsage 是按本地日期聚合的 token 用量。
type DailyUsage struct {
	Day                       string // YYYY-MM-DD（本地时区）
	Requests                  int64
	InputTokens, OutputTokens int64
}

// RequestHistoryRepo 读写 request_history 表；ListRecent 按时间新→旧。
type RequestHistoryRepo interface {
	Add(rec RequestHistoryRecord) error
	ListRecent(limit int) ([]RequestHistoryRecord, error)
	DeleteOlderThan(cutoffMs int64) (int64, error)
	UsageByChannelModel(sinceMs int64) ([]UsageAggregate, error)
	UsageByDay(sinceMs int64) ([]DailyUsage, error)
}

// Store 是持久层入口。
type Store interface {
	Channels() ChannelRepo
	ChannelModels() ChannelModelRepo
	LogicalModels() LogicalModelRepo
	LogicalModelTargets() LogicalModelTargetRepo
	AccessConfigs() AccessConfigRepo
	RequestHistory() RequestHistoryRepo
	Secrets() SecretRepo
	Settings() SettingsRepo

	// RouteVersion 是路由配置的单调版本号：channels / channel_models /
	// logical_models / logical_model_targets 任一写操作成功后递增，只读不变。
	// app 层路由缓存以它作失效键——失效由写入方（存储层）负责，任何写入口
	// （桥接、测试夹具、未来 CLI）都不需要记得手动清缓存。
	RouteVersion() uint64

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

// AccessConfigRepo 读写 access_configs；UNIQUE(app_type, name)、
// UNIQUE(app_type) WHERE is_current=1。
type AccessConfigRepo interface {
	ListByApp(appType string) ([]AccessConfig, error)
	Get(id string) (AccessConfig, error)
	Save(c AccessConfig) (AccessConfig, error)
	Delete(id string) error
	// SetCurrent：id="" 仅清空该 app 的 current；否则事务内清 app 全部再置
	// id（id 必须存在）。
	SetCurrent(appType, id string) error
	// Current 返回该 app 当前生效的配置；无 → ErrNotFound。
	Current(appType string) (AccessConfig, error)
}
