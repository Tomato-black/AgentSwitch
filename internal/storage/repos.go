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

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nowMS() int64 { return time.Now().UnixMilli() }

type settingsRepo struct{ db *sql.DB }

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

type channelRepo struct{ db *sql.DB }

const channelCols = `id,name,protocol,base_url,credential_ref,preset_id,enabled,allow_private_upstream,created_at,updated_at`

func scanChannel(sc func(...any) error) (Channel, error) {
	var c Channel
	var en, ap int
	if err := sc(&c.ID, &c.Name, &c.Protocol, &c.BaseURL, &c.CredentialRef, &c.PresetID, &en, &ap, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return Channel{}, err
	}
	c.Enabled, c.AllowPrivateUpstream = en == 1, ap == 1
	return c, nil
}

func (r *channelRepo) List() ([]Channel, error) {
	rows, err := r.db.Query(`SELECT ` + channelCols + ` FROM channels ORDER BY name ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Channel
	for rows.Next() {
		c, err := scanChannel(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *channelRepo) Get(id string) (Channel, error) {
	c, err := scanChannel(r.db.QueryRow(`SELECT `+channelCols+` FROM channels WHERE id=?`, id).Scan)
	if err != nil {
		return Channel{}, mapNotFound(err)
	}
	return c, nil
}

func (r *channelRepo) Save(c Channel) (Channel, error) {
	if c.ID == "" {
		c.ID = NewID()
	}
	existing, err := r.Get(c.ID)
	if err == nil {
		c.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return Channel{}, err
	}
	if c.CreatedAt == 0 {
		c.CreatedAt = nowMS()
	}
	c.UpdatedAt = nowMS()
	// name UNIQUE 冲突由驱动冒泡为非 nil error（bridge 负责友好化）。
	_, err = r.db.Exec(`INSERT INTO channels(id,name,protocol,base_url,credential_ref,preset_id,enabled,allow_private_upstream,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, protocol=excluded.protocol,
			base_url=excluded.base_url, credential_ref=excluded.credential_ref, preset_id=excluded.preset_id,
			enabled=excluded.enabled, allow_private_upstream=excluded.allow_private_upstream, updated_at=excluded.updated_at`,
		c.ID, c.Name, c.Protocol, c.BaseURL, c.CredentialRef, c.PresetID,
		boolInt(c.Enabled), boolInt(c.AllowPrivateUpstream), c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return Channel{}, err
	}
	return c, nil
}

func (r *channelRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM channels WHERE id=?`, id)
	return err
}

func (r *channelRepo) SetEnabled(id string, on bool) error {
	_, err := r.db.Exec(`UPDATE channels SET enabled=? WHERE id=?`, boolInt(on), id)
	return err
}

// HasUsableKey 报告是否存在 enabled 且 credential_ref 非空的渠道（单 Key 口径）。
func (r *channelRepo) HasUsableKey() (bool, error) {
	var n int
	err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM channels WHERE enabled=1 AND credential_ref != '')`).Scan(&n)
	return n == 1, err
}

type channelModelRepo struct{ db *sql.DB }

const modelCols = `id,channel_id,model_name,created_at`

func scanModel(sc func(...any) error) (ChannelModel, error) {
	var m ChannelModel
	if err := sc(&m.ID, &m.ChannelID, &m.ModelName, &m.CreatedAt); err != nil {
		return ChannelModel{}, err
	}
	return m, nil
}

func scanModels(rows *sql.Rows) ([]ChannelModel, error) {
	var out []ChannelModel
	for rows.Next() {
		m, err := scanModel(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *channelModelRepo) ListByChannel(channelID string) ([]ChannelModel, error) {
	rows, err := r.db.Query(`SELECT `+modelCols+` FROM channel_models WHERE channel_id=? ORDER BY model_name ASC, id ASC`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModels(rows)
}

func (r *channelModelRepo) Save(m ChannelModel) (ChannelModel, error) {
	if m.ID == "" {
		m.ID = NewID()
	}
	// 与 Channel.Save 同口径：更新时保留原 created_at，返回值与库内一致。
	var existing int64
	err := r.db.QueryRow(`SELECT created_at FROM channel_models WHERE id=?`, m.ID).Scan(&existing)
	if err == nil {
		m.CreatedAt = existing
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ChannelModel{}, err
	}
	if m.CreatedAt == 0 {
		m.CreatedAt = nowMS()
	}
	// UNIQUE(channel_id,model_name) 冲突由驱动冒泡为非 nil error。
	_, err = r.db.Exec(`INSERT INTO channel_models(id,channel_id,model_name,created_at)
		VALUES(?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET channel_id=excluded.channel_id, model_name=excluded.model_name`,
		m.ID, m.ChannelID, m.ModelName, m.CreatedAt)
	if err != nil {
		return ChannelModel{}, err
	}
	return m, nil
}

func (r *channelModelRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM channel_models WHERE id=?`, id)
	return err
}

func (r *channelModelRepo) DeleteByChannel(channelID string) error {
	_, err := r.db.Exec(`DELETE FROM channel_models WHERE channel_id=?`, channelID)
	return err
}

// Suggestions 返回渠道的模型名列表（按名排序，目标编辑下拉数据源）。
func (r *channelModelRepo) Suggestions(channelID string) ([]string, error) {
	rows, err := r.db.Query(`SELECT model_name FROM channel_models WHERE channel_id=? ORDER BY model_name ASC`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

// ---- logical_models / logical_model_targets 骨架（Task 3 补全行为）----

type logicalModelRepo struct{ db *sql.DB }

func (r *logicalModelRepo) List() ([]LogicalModel, error) {
	return nil, nil
}

func (r *logicalModelRepo) Get(id string) (LogicalModel, error) {
	return LogicalModel{}, ErrNotFound
}

func (r *logicalModelRepo) GetByName(name string) (LogicalModel, error) {
	return LogicalModel{}, ErrNotFound
}

func (r *logicalModelRepo) Save(m LogicalModel) (LogicalModel, error) {
	return LogicalModel{}, ErrNotFound
}

func (r *logicalModelRepo) Delete(id string) error { return nil }

func (r *logicalModelRepo) SetEnabled(id string, on bool) error { return nil }

type logicalModelTargetRepo struct{ db *sql.DB }

func (r *logicalModelTargetRepo) ListByModel(modelID string) ([]LogicalModelTarget, error) {
	return nil, nil
}

func (r *logicalModelTargetRepo) Save(t LogicalModelTarget) (LogicalModelTarget, error) {
	return LogicalModelTarget{}, ErrNotFound
}

func (r *logicalModelTargetRepo) Delete(id string) error { return nil }

func (r *logicalModelTargetRepo) DeleteByModel(modelID string) error { return nil }
