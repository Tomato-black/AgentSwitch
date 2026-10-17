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

const channelCols = `id,name,type,protocol,base_url,priority,enabled,allow_private_upstream,created_at,updated_at`

func scanChannel(sc func(...any) error) (Channel, error) {
	var c Channel
	var pr, en, ap int
	var ca, ua string
	if err := sc(&c.ID, &c.Name, &c.Type, &c.Protocol, &c.BaseURL, &pr, &en, &ap, &ca, &ua); err != nil {
		return Channel{}, err
	}
	c.Priority, c.Enabled, c.AllowPrivateUpstream = pr, en == 1, ap == 1
	c.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	c.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return c, nil
}

func (r *channelRepo) List() ([]Channel, error) {
	rows, err := r.db.Query(`SELECT ` + channelCols + ` FROM channels ORDER BY priority ASC, id ASC`)
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

func (r *channelRepo) Save(c Channel) error {
	if c.ID == "" {
		c.ID = NewID()
	}
	existing, err := r.Get(c.ID)
	if err == nil {
		c.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now()
	}
	c.UpdatedAt = time.Now()
	_, err = r.db.Exec(`INSERT INTO channels(id,name,type,protocol,base_url,priority,enabled,allow_private_upstream,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, type=excluded.type, protocol=excluded.protocol,
			base_url=excluded.base_url, priority=excluded.priority, enabled=excluded.enabled,
			allow_private_upstream=excluded.allow_private_upstream, updated_at=excluded.updated_at`,
		c.ID, c.Name, c.Type, c.Protocol, c.BaseURL, c.Priority, boolInt(c.Enabled), boolInt(c.AllowPrivateUpstream),
		c.CreatedAt.Format(time.RFC3339), c.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *channelRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM channels WHERE id=?`, id)
	return err
}

func (r *channelRepo) SetEnabled(id string, on bool) error {
	_, err := r.db.Exec(`UPDATE channels SET enabled=? WHERE id=?`, boolInt(on), id)
	return err
}

// HasUsableKey 报告是否存在 enabled 渠道×enabled 密钥（readyz/Degraded 口径）。
func (r *channelRepo) HasUsableKey() (bool, error) {
	var n int
	err := r.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM channels c
		JOIN channel_keys k ON k.channel_id = c.id
		WHERE c.enabled=1 AND k.enabled=1)`).Scan(&n)
	return n == 1, err
}

type channelKeyRepo struct{ db *sql.DB }

const keyCols = `id,channel_id,name,secret_key,secret_backend,key_tail,priority,enabled,created_at,updated_at`

// scanKey 与 scanChannel 同构（列指针经 sc 注入，bool/RFC3339 就地转换）。
func scanKey(sc func(...any) error) (ChannelKey, error) {
	var k ChannelKey
	var pr, en int
	var ca, ua string
	if err := sc(&k.ID, &k.ChannelID, &k.Name, &k.SecretKey, &k.SecretBackend, &k.KeyTail, &k.Priority, &en, &ca, &ua); err != nil {
		return ChannelKey{}, err
	}
	k.Priority, k.Enabled = pr, en == 1
	k.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	k.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return k, nil
}

func scanKeys(rows *sql.Rows) ([]ChannelKey, error) {
	var out []ChannelKey
	for rows.Next() {
		k, err := scanKey(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

func (r *channelKeyRepo) List() ([]ChannelKey, error) {
	rows, err := r.db.Query(`SELECT ` + keyCols + ` FROM channel_keys ORDER BY channel_id ASC, priority ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanKeys(rows)
}

func (r *channelKeyRepo) ListByChannel(channelID string) ([]ChannelKey, error) {
	rows, err := r.db.Query(`SELECT `+keyCols+` FROM channel_keys WHERE channel_id=? ORDER BY priority ASC, id ASC`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanKeys(rows)
}

func (r *channelKeyRepo) Get(id string) (ChannelKey, error) {
	k, err := scanKey(r.db.QueryRow(`SELECT `+keyCols+` FROM channel_keys WHERE id=?`, id).Scan)
	if err != nil {
		return ChannelKey{}, mapNotFound(err)
	}
	return k, nil
}

func (r *channelKeyRepo) Save(k ChannelKey) error {
	if k.ID == "" {
		k.ID = NewID()
	}
	existing, err := r.Get(k.ID)
	if err == nil {
		k.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now()
	}
	k.UpdatedAt = time.Now()
	_, err = r.db.Exec(`INSERT INTO channel_keys(id,channel_id,name,secret_key,secret_backend,key_tail,priority,enabled,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET channel_id=excluded.channel_id, name=excluded.name,
			secret_key=excluded.secret_key, secret_backend=excluded.secret_backend, key_tail=excluded.key_tail,
			priority=excluded.priority, enabled=excluded.enabled, updated_at=excluded.updated_at`,
		k.ID, k.ChannelID, k.Name, k.SecretKey, k.SecretBackend, k.KeyTail, k.Priority, boolInt(k.Enabled),
		k.CreatedAt.Format(time.RFC3339), k.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *channelKeyRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM channel_keys WHERE id=?`, id)
	return err
}

func (r *channelKeyRepo) SetEnabled(id string, on bool) error {
	_, err := r.db.Exec(`UPDATE channel_keys SET enabled=? WHERE id=?`, boolInt(on), id)
	return err
}

func (r *channelKeyRepo) CountByChannel(channelID string) (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM channel_keys WHERE channel_id=?`, channelID).Scan(&n)
	return n, err
}

type channelModelRepo struct{ db *sql.DB }

const modelCols = `id,channel_id,public_model,upstream_model,enabled,created_at,updated_at`

// scanModel 与 scanChannel 同构（列指针经 sc 注入，bool/RFC3339 就地转换）。
func scanModel(sc func(...any) error) (ChannelModel, error) {
	var m ChannelModel
	var en int
	var ca, ua string
	if err := sc(&m.ID, &m.ChannelID, &m.PublicModel, &m.UpstreamModel, &en, &ca, &ua); err != nil {
		return ChannelModel{}, err
	}
	m.Enabled = en == 1
	m.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	m.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
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

// List 按 (channel_id, public_model) 序——渠道内 public_model 已 UNIQUE，天然确定序。
func (r *channelModelRepo) List() ([]ChannelModel, error) {
	rows, err := r.db.Query(`SELECT ` + modelCols + ` FROM channel_models ORDER BY channel_id ASC, public_model ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModels(rows)
}

func (r *channelModelRepo) ListByChannel(channelID string) ([]ChannelModel, error) {
	rows, err := r.db.Query(`SELECT `+modelCols+` FROM channel_models WHERE channel_id=? ORDER BY public_model ASC`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModels(rows)
}

func (r *channelModelRepo) ListByPublic(public string) ([]ChannelModel, error) {
	rows, err := r.db.Query(`SELECT `+modelCols+` FROM channel_models WHERE public_model=? ORDER BY channel_id ASC`, public)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanModels(rows)
}

func (r *channelModelRepo) Get(id string) (ChannelModel, error) {
	m, err := scanModel(r.db.QueryRow(`SELECT `+modelCols+` FROM channel_models WHERE id=?`, id).Scan)
	if err != nil {
		return ChannelModel{}, mapNotFound(err)
	}
	return m, nil
}

func (r *channelModelRepo) Save(m ChannelModel) error {
	if m.ID == "" {
		m.ID = NewID()
	}
	existing, err := r.Get(m.ID)
	if err == nil {
		m.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	m.UpdatedAt = time.Now()
	// UNIQUE(channel_id,public_model) 冲突由驱动冒泡为非 nil error（与旧
	// models.public_model UNIQUE 同口径，bridge 负责友好化）。
	_, err = r.db.Exec(`INSERT INTO channel_models(id,channel_id,public_model,upstream_model,enabled,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET channel_id=excluded.channel_id, public_model=excluded.public_model,
			upstream_model=excluded.upstream_model, enabled=excluded.enabled, updated_at=excluded.updated_at`,
		m.ID, m.ChannelID, m.PublicModel, m.UpstreamModel, boolInt(m.Enabled),
		m.CreatedAt.Format(time.RFC3339), m.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *channelModelRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM channel_models WHERE id=?`, id)
	return err
}

func (r *channelModelRepo) SetEnabled(id string, on bool) error {
	_, err := r.db.Exec(`UPDATE channel_models SET enabled=? WHERE id=?`, boolInt(on), id)
	return err
}

// PublicModels 去重输出 enabled 公开模型名（/v1/models 并集）。
func (r *channelModelRepo) PublicModels() ([]string, error) {
	rows, err := r.db.Query(`SELECT DISTINCT public_model FROM channel_models WHERE enabled=1 ORDER BY public_model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *channelModelRepo) CountByChannel(channelID string) (int, error) {
	var n int
	err := r.db.QueryRow(`SELECT COUNT(*) FROM channel_models WHERE channel_id=?`, channelID).Scan(&n)
	return n, err
}
