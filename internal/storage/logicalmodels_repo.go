package storage

import (
	"encoding/json"
	"errors"
)

func scanLogicalModel(sc func(...any) error) (LogicalModel, error) {
	var m LogicalModel
	var en int
	var levels string
	if err := sc(&m.ID, &m.Name, &m.Description, &en,
		&m.DisplayName, &m.ContextWindow, &levels, &m.DefaultReasoningLevel,
		&m.CreatedAt, &m.UpdatedAt); err != nil {
		return LogicalModel{}, err
	}
	m.Enabled = en == 1
	if levels != "" {
		if err := json.Unmarshal([]byte(levels), &m.ReasoningLevels); err != nil {
			return LogicalModel{}, err
		}
	}
	return m, nil
}
func (r *logicalModelRepo) List() ([]LogicalModel, error) {
	rows, err := r.db.Query(`SELECT ` + logicalModelCols + ` FROM logical_models ORDER BY name ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogicalModel
	for rows.Next() {
		m, err := scanLogicalModel(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
func (r *logicalModelRepo) Get(id string) (LogicalModel, error) {
	m, err := scanLogicalModel(r.db.QueryRow(`SELECT `+logicalModelCols+` FROM logical_models WHERE id=?`, id).Scan)
	if err != nil {
		return LogicalModel{}, mapNotFound(err)
	}
	return m, nil
}
func (r *logicalModelRepo) GetByName(name string) (LogicalModel, error) {
	m, err := scanLogicalModel(r.db.QueryRow(`SELECT `+logicalModelCols+` FROM logical_models WHERE name=?`, name).Scan)
	if err != nil {
		return LogicalModel{}, mapNotFound(err)
	}
	return m, nil
}
func (r *logicalModelRepo) Save(m LogicalModel) (LogicalModel, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	// 与 Channel.Save 同口径：更新保留原 created_at，返回值与库内一致。
	existing, err := r.Get(m.ID)
	if err == nil {
		m.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return LogicalModel{}, err
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = nowMS()
	}
	m.UpdatedAt = nowMS()
	// reasoning_levels 以 JSON 数组文本落库；未配置存空串（回读为空切片）。
	levels := ""
	if len(m.ReasoningLevels) > 0 {
		b, err := json.Marshal(m.ReasoningLevels)
		if err != nil {
			return LogicalModel{}, err
		}
		levels = string(b)
	}
	// name UNIQUE 冲突由驱动冒泡为非 nil error（bridge 负责友好化）。
	_, err = r.db.Exec(`INSERT INTO logical_models(id,name,description,enabled,display_name,context_window,reasoning_levels,default_reasoning_level,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description,
			enabled=excluded.enabled, display_name=excluded.display_name,
			context_window=excluded.context_window, reasoning_levels=excluded.reasoning_levels,
			default_reasoning_level=excluded.default_reasoning_level, updated_at=excluded.updated_at`,
		m.ID, m.Name, m.Description, boolInt(m.Enabled),
		m.DisplayName, m.ContextWindow, levels, m.DefaultReasoningLevel,
		m.CreatedAt, m.UpdatedAt)
	if err != nil {
		return LogicalModel{}, err
	}
	bumpRoute(r.ver)
	return m, nil
}
func (r *logicalModelRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM logical_models WHERE id=?`, id)
	if err == nil {
		bumpRoute(r.ver)
	}
	return err
}
func (r *logicalModelRepo) SetEnabled(id string, on bool) error {
	_, err := r.db.Exec(`UPDATE logical_models SET enabled=? WHERE id=?`, boolInt(on), id)
	if err == nil {
		bumpRoute(r.ver)
	}
	return err
}
func scanTarget(sc func(...any) error) (LogicalModelTarget, error) {
	var t LogicalModelTarget
	if err := sc(&t.ID, &t.LogicalModelID, &t.ChannelID, &t.ModelName, &t.Priority); err != nil {
		return LogicalModelTarget{}, err
	}
	return t, nil
}
func (r *logicalModelTargetRepo) ListByModel(modelID string) ([]LogicalModelTarget, error) {
	// priority 越小越优先——升序即故障转移链顺序。
	rows, err := r.db.Query(`SELECT `+targetCols+` FROM logical_model_targets WHERE logical_model_id=? ORDER BY priority ASC, id ASC`, modelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LogicalModelTarget
	for rows.Next() {
		t, err := scanTarget(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
func (r *logicalModelTargetRepo) Save(t LogicalModelTarget) (LogicalModelTarget, error) {
	if t.ID == "" {
		t.ID = NewID()
	}
	// UNIQUE(logical_model_id, channel_id, model_name) 冲突由驱动冒泡为非 nil error。
	_, err := r.db.Exec(`INSERT INTO logical_model_targets(id,logical_model_id,channel_id,model_name,priority)
		VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET logical_model_id=excluded.logical_model_id, channel_id=excluded.channel_id,
			model_name=excluded.model_name, priority=excluded.priority`,
		t.ID, t.LogicalModelID, t.ChannelID, t.ModelName, t.Priority)
	if err != nil {
		return LogicalModelTarget{}, err
	}
	bumpRoute(r.ver)
	return t, nil
}
func (r *logicalModelTargetRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM logical_model_targets WHERE id=?`, id)
	if err == nil {
		bumpRoute(r.ver)
	}
	return err
}
func (r *logicalModelTargetRepo) DeleteByModel(modelID string) error {
	_, err := r.db.Exec(`DELETE FROM logical_model_targets WHERE logical_model_id=?`, modelID)
	if err == nil {
		bumpRoute(r.ver)
	}
	return err
}
