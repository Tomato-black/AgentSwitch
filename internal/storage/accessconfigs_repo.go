package storage

import (
	"errors"
)

func scanAccessConfig(sc func(...any) error) (AccessConfig, error) {
	var c AccessConfig
	var cur, pending int
	if err := sc(&c.ID, &c.AppType, &c.Name, &c.LogicalModelID, &c.Settings,
		&cur, &pending, &c.SortOrder, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return AccessConfig{}, err
	}
	c.IsCurrent = cur == 1
	c.NeedsApply = pending == 1
	if c.Settings == "" { // 写入侧保证合法 JSON；空串回读为 "{}"
		c.Settings = "{}"
	}
	return c, nil
}
func (r *accessConfigRepo) ListByApp(appType string) ([]AccessConfig, error) {
	rows, err := r.db.Query(`SELECT `+accessConfigCols+` FROM access_configs WHERE app_type=? ORDER BY sort_order ASC, name ASC`, appType)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AccessConfig
	for rows.Next() {
		c, err := scanAccessConfig(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
func (r *accessConfigRepo) Get(id string) (AccessConfig, error) {
	c, err := scanAccessConfig(r.db.QueryRow(`SELECT `+accessConfigCols+` FROM access_configs WHERE id=?`, id).Scan)
	if err != nil {
		return AccessConfig{}, mapNotFound(err)
	}
	return c, nil
}
func (r *accessConfigRepo) Save(c AccessConfig) (AccessConfig, error) {
	if c.ID == "" {
		c.ID = NewID()
	}
	// 与 Channel.Save 同口径：更新保留原 created_at，返回值与库内一致。
	existing, err := r.Get(c.ID)
	if err == nil {
		c.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return AccessConfig{}, err
	}
	if c.CreatedAt == 0 {
		c.CreatedAt = nowMS()
	}
	c.UpdatedAt = nowMS()
	if c.Settings == "" {
		c.Settings = "{}"
	}
	// UNIQUE(app_type, name) 冲突由驱动冒泡为非 nil error（bridge 负责友好化）。
	_, err = r.db.Exec(`INSERT INTO access_configs(id,app_type,name,logical_model_id,settings,is_current,needs_apply,sort_order,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET app_type=excluded.app_type, name=excluded.name,
			logical_model_id=excluded.logical_model_id, settings=excluded.settings,
			is_current=excluded.is_current, needs_apply=excluded.needs_apply,
			sort_order=excluded.sort_order, updated_at=excluded.updated_at`,
		c.ID, c.AppType, c.Name, c.LogicalModelID, c.Settings,
		boolInt(c.IsCurrent), boolInt(c.NeedsApply), c.SortOrder, c.CreatedAt, c.UpdatedAt)
	if err != nil {
		return AccessConfig{}, err
	}
	return c, nil
}
func (r *accessConfigRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM access_configs WHERE id=?`, id)
	return err
}
func (r *accessConfigRepo) SetCurrent(appType, id string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE access_configs SET is_current=0 WHERE app_type=?`, appType); err != nil {
		tx.Rollback()
		return err
	}
	if id != "" {
		var n int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM access_configs WHERE id=?`, id).Scan(&n); err != nil {
			tx.Rollback()
			return err
		}
		if n == 0 {
			tx.Rollback()
			return ErrNotFound
		}
		if _, err := tx.Exec(`UPDATE access_configs SET is_current=1, needs_apply=0 WHERE id=?`, id); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}
func (r *accessConfigRepo) Current(appType string) (AccessConfig, error) {
	c, err := scanAccessConfig(r.db.QueryRow(`SELECT `+accessConfigCols+` FROM access_configs WHERE app_type=? AND is_current=1`, appType).Scan)
	if err != nil {
		return AccessConfig{}, mapNotFound(err)
	}
	return c, nil
}
