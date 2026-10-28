package bridge

import (
	"fmt"
	"regexp"
	"strings"

	"AgentSwitch/internal/app"
	"AgentSwitch/internal/storage"
)

// LogicalModels 是逻辑模型管理面（logical-models spec §4）：对外模型名 +
// 有序目标链（渠道·模型）。目标链即故障转移顺序（priority 越小越优先）；
// 只做配置 CRUD，不承载模型请求正文（两平面分离）。
type LogicalModels struct{ A *app.App }

// TargetSpec 是目标链编辑入参；Create/Update 全量提交整条链。
type TargetSpec struct {
	ChannelID string
	ModelName string
	Priority  int
}

// ModelMeta 是逻辑模型元数据（codex-access-models-metadata spec §4）：供
// 模型目录生成使用；上下文窗口/思考等级由用户声明，网关不校验上游能力。
type ModelMeta struct {
	DisplayName           string
	ContextWindow         int64
	ReasoningLevels       []string
	DefaultReasoningLevel string
}

// reasoningLevelWhitelist 是 Codex 认可的思考等级（spec §4）。
var reasoningLevelWhitelist = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true,
	"high": true, "xhigh": true, "max": true,
}

// validateModelMeta 校验元数据：思考等级须在白名单内、默认等级须属于列表、
// 上下文窗口不得为负（0 表示沿用模板）。
func validateModelMeta(meta ModelMeta) error {
	if meta.ContextWindow < 0 {
		return fmt.Errorf("上下文窗口不能为负数")
	}
	known := make(map[string]bool, len(meta.ReasoningLevels))
	for _, lv := range meta.ReasoningLevels {
		if !reasoningLevelWhitelist[lv] {
			return fmt.Errorf("未知思考等级：%s", lv)
		}
		known[lv] = true
	}
	if meta.DefaultReasoningLevel != "" && !known[meta.DefaultReasoningLevel] {
		return fmt.Errorf("默认思考等级必须属于思考等级列表")
	}
	return nil
}

// ModelView 是逻辑模型视图；Targets 按 priority 升序（故障转移链顺序）。
type ModelView struct {
	ID, Name, Description string
	DisplayName           string
	ContextWindow         int64
	ReasoningLevels       []string
	DefaultReasoningLevel string
	Enabled               bool
	Targets               []TargetView
}

// TargetView 是目标链一环。Healthy=渠道可用（存在、enabled 且有 Key）——
// 配置口径；运行时冷却不计入（冷却是易变状态，走 Cooldowns 快照另行指示）。
type TargetView struct {
	ID, ChannelID, ChannelName, ModelName string
	Priority                              int
	Healthy                               bool
}

// logicalNameRe 是网关对外模型名约束：^[a-zA-Z0-9._-]+$（须与协议 model 字段兼容）。
var logicalNameRe = regexp.MustCompile(`^[a-zA-Z0-9._-]+$`)

func validateLogicalName(name string) error {
	if !logicalNameRe.MatchString(name) {
		return fmt.Errorf("逻辑模型名只能包含字母、数字、点、下划线与连字符")
	}
	return nil
}

// friendlyModelError 把 UNIQUE 冲突译成中文友好错误（repo 约定由 bridge 友好化）：
// logical_models 表冲突=模型重名；否则为目标链重复。
func friendlyModelError(err error) error {
	if !isUniqueViolation(err) {
		return err
	}
	if strings.Contains(err.Error(), "logical_models") {
		return fmt.Errorf("逻辑模型名已存在")
	}
	return fmt.Errorf("目标重复")
}

func (s LogicalModels) targetsView(modelID string) []TargetView {
	ts, err := s.A.Store().LogicalModelTargets().ListByModel(modelID)
	if err != nil {
		return nil
	}
	chans := map[string]storage.Channel{}
	if cs, err := s.A.Store().Channels().List(); err == nil {
		for _, c := range cs {
			chans[c.ID] = c
		}
	}
	out := make([]TargetView, 0, len(ts))
	for _, t := range ts {
		v := TargetView{ID: t.ID, ChannelID: t.ChannelID, ModelName: t.ModelName, Priority: t.Priority}
		if c, ok := chans[t.ChannelID]; ok {
			v.ChannelName = c.Name
			v.Healthy = c.Enabled && c.CredentialRef != ""
		}
		out = append(out, v)
	}
	return out
}

func (s LogicalModels) view(m storage.LogicalModel) ModelView {
	return ModelView{ID: m.ID, Name: m.Name, Description: m.Description,
		DisplayName: m.DisplayName, ContextWindow: m.ContextWindow,
		ReasoningLevels: m.ReasoningLevels, DefaultReasoningLevel: m.DefaultReasoningLevel,
		Enabled: m.Enabled, Targets: s.targetsView(m.ID)}
}

func (s LogicalModels) List() []ModelView {
	ms, err := s.A.Store().LogicalModels().List()
	if err != nil {
		return nil
	}
	out := make([]ModelView, 0, len(ms))
	for _, m := range ms {
		out = append(out, s.view(m))
	}
	return out
}

func (s LogicalModels) Get(id string) (ModelView, error) {
	m, err := s.A.Store().LogicalModels().Get(id)
	if err != nil {
		return ModelView{}, err
	}
	return s.view(m), nil
}

// writeTargets 全量替换目标链（DeleteByModel + 重插），Update 复用。
func (s LogicalModels) writeTargets(modelID string, targets []TargetSpec) error {
	if err := s.A.Store().LogicalModelTargets().DeleteByModel(modelID); err != nil {
		return err
	}
	for _, t := range targets {
		if _, err := s.A.Store().LogicalModelTargets().Save(storage.LogicalModelTarget{
			LogicalModelID: modelID, ChannelID: t.ChannelID,
			ModelName: t.ModelName, Priority: t.Priority,
		}); err != nil {
			return friendlyModelError(err)
		}
	}
	return nil
}

// Create 建逻辑模型：name 匹配 ^[a-zA-Z0-9._-]+$，targets 非空，meta 合法；
// 新建默认 enabled。
func (s LogicalModels) Create(name, description string, meta ModelMeta, targets []TargetSpec) (ModelView, error) {
	if err := validateLogicalName(name); err != nil {
		return ModelView{}, err
	}
	if len(targets) == 0 {
		return ModelView{}, fmt.Errorf("至少需要一个目标")
	}
	if err := validateModelMeta(meta); err != nil {
		return ModelView{}, err
	}
	m, err := s.A.Store().LogicalModels().Save(storage.LogicalModel{
		Name: name, Description: description, Enabled: true,
		DisplayName: meta.DisplayName, ContextWindow: meta.ContextWindow,
		ReasoningLevels: meta.ReasoningLevels, DefaultReasoningLevel: meta.DefaultReasoningLevel,
	})
	if err != nil {
		return ModelView{}, friendlyModelError(err)
	}
	if err := s.writeTargets(m.ID, targets); err != nil {
		s.A.Store().LogicalModels().Delete(m.ID) // 目标写失败回滚模型行
		return ModelView{}, err
	}
	s.A.Log().Info("logical model created", "id", m.ID, "actor", "ui", "result", "ok")
	return s.Get(m.ID)
}

// Update 改逻辑模型字段并全量替换目标链（空 targets=清空链；非空校验只在 Create）。
func (s LogicalModels) Update(id, name, description string, meta ModelMeta, enabled bool, targets []TargetSpec) error {
	m, err := s.A.Store().LogicalModels().Get(id)
	if err != nil {
		return err
	}
	if err := validateLogicalName(name); err != nil {
		return err
	}
	if err := validateModelMeta(meta); err != nil {
		return err
	}
	m.Name, m.Description, m.Enabled = name, description, enabled
	m.DisplayName, m.ContextWindow = meta.DisplayName, meta.ContextWindow
	m.ReasoningLevels, m.DefaultReasoningLevel = meta.ReasoningLevels, meta.DefaultReasoningLevel
	if _, err := s.A.Store().LogicalModels().Save(m); err != nil {
		return friendlyModelError(err)
	}
	if err := s.writeTargets(id, targets); err != nil {
		return err
	}
	s.A.Log().Info("logical model updated", "id", id, "actor", "ui", "result", "ok")
	return nil
}

// Remove 删逻辑模型（logical_model_targets 由 FK CASCADE）。
func (s LogicalModels) Remove(id string) {
	if err := s.A.Store().LogicalModels().Delete(id); err != nil {
		s.A.Log().Error("logical model remove failed", "id", id, "err", err)
		return
	}
	s.A.Log().Info("logical model removed", "id", id, "actor", "ui", "result", "ok")
}

func (s LogicalModels) SetEnabled(id string, on bool) {
	if err := s.A.Store().LogicalModels().SetEnabled(id, on); err != nil {
		s.A.Log().Error("logical model enable failed", "id", id, "on", on, "err", err)
		return
	}
	s.A.Log().Info("logical model enabled", "id", id, "on", on, "actor", "ui", "result", "ok")
}
