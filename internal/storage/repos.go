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

type providerRepo struct{ db *sql.DB }

func (r *providerRepo) List() ([]Provider, error) {
	rows, err := r.db.Query(`SELECT id,name,protocol,base_url,enabled,allow_private_upstream,created_at,updated_at FROM providers ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Provider
	for rows.Next() {
		var p Provider
		var en, ap int
		var ca, ua string
		if err := rows.Scan(&p.ID, &p.Name, &p.Protocol, &p.BaseURL, &en, &ap, &ca, &ua); err != nil {
			return nil, err
		}
		p.Enabled, p.AllowPrivateUpstream = en == 1, ap == 1
		p.CreatedAt, _ = time.Parse(time.RFC3339, ca)
		p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (r *providerRepo) Get(id string) (Provider, error) {
	var p Provider
	var en, ap int
	var ca, ua string
	err := r.db.QueryRow(`SELECT id,name,protocol,base_url,enabled,allow_private_upstream,created_at,updated_at FROM providers WHERE id=?`, id).
		Scan(&p.ID, &p.Name, &p.Protocol, &p.BaseURL, &en, &ap, &ca, &ua)
	if err != nil {
		return Provider{}, mapNotFound(err)
	}
	p.Enabled, p.AllowPrivateUpstream = en == 1, ap == 1
	p.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	p.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return p, nil
}

func (r *providerRepo) Save(p Provider) error {
	if p.ID == "" {
		p.ID = NewID()
	}
	existing, err := r.Get(p.ID)
	if err == nil {
		p.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	p.UpdatedAt = time.Now()
	_, err = r.db.Exec(`INSERT INTO providers(id,name,protocol,base_url,enabled,allow_private_upstream,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, protocol=excluded.protocol, base_url=excluded.base_url,
			enabled=excluded.enabled, allow_private_upstream=excluded.allow_private_upstream, updated_at=excluded.updated_at`,
		p.ID, p.Name, p.Protocol, p.BaseURL, boolInt(p.Enabled), boolInt(p.AllowPrivateUpstream),
		p.CreatedAt.Format(time.RFC3339), p.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *providerRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM providers WHERE id=?`, id)
	return err
}

type accountRepo struct{ db *sql.DB }

func (r *accountRepo) List() ([]Account, error) {
	rows, err := r.db.Query(`SELECT id,provider_id,name,secret_key,secret_backend,key_tail,priority,enabled,created_at,updated_at FROM accounts ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAccounts(rows)
}

func (r *accountRepo) ListByProvider(pid string) ([]Account, error) {
	rows, err := r.db.Query(`SELECT id,provider_id,name,secret_key,secret_backend,key_tail,priority,enabled,created_at,updated_at FROM accounts WHERE provider_id=? ORDER BY name`, pid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAccounts(rows)
}

func scanAccounts(rows *sql.Rows) ([]Account, error) {
	var out []Account
	for rows.Next() {
		var a Account
		var en int
		var ca, ua string
		if err := rows.Scan(&a.ID, &a.ProviderID, &a.Name, &a.SecretKey, &a.SecretBackend, &a.KeyTail, &a.Priority, &en, &ca, &ua); err != nil {
			return nil, err
		}
		a.Enabled = en == 1
		a.CreatedAt, _ = time.Parse(time.RFC3339, ca)
		a.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *accountRepo) Get(id string) (Account, error) {
	var a Account
	var en int
	var ca, ua string
	err := r.db.QueryRow(`SELECT id,provider_id,name,secret_key,secret_backend,key_tail,priority,enabled,created_at,updated_at FROM accounts WHERE id=?`, id).
		Scan(&a.ID, &a.ProviderID, &a.Name, &a.SecretKey, &a.SecretBackend, &a.KeyTail, &a.Priority, &en, &ca, &ua)
	if err != nil {
		return Account{}, mapNotFound(err)
	}
	a.Enabled = en == 1
	a.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	a.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return a, nil
}

func (r *accountRepo) Save(a Account) error {
	if a.ID == "" {
		a.ID = NewID()
	}
	existing, err := r.Get(a.ID)
	if err == nil {
		a.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now()
	}
	a.UpdatedAt = time.Now()
	_, err = r.db.Exec(`INSERT INTO accounts(id,provider_id,name,secret_key,secret_backend,key_tail,priority,enabled,created_at,updated_at)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET provider_id=excluded.provider_id, name=excluded.name,
			secret_key=excluded.secret_key, secret_backend=excluded.secret_backend, key_tail=excluded.key_tail,
			priority=excluded.priority, enabled=excluded.enabled, updated_at=excluded.updated_at`,
		a.ID, a.ProviderID, a.Name, a.SecretKey, a.SecretBackend, a.KeyTail, a.Priority, boolInt(a.Enabled),
		a.CreatedAt.Format(time.RFC3339), a.UpdatedAt.Format(time.RFC3339))
	return err
}

func (r *accountRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM accounts WHERE id=?`, id)
	return err
}

func (r *accountRepo) SetEnabled(id string, on bool) error {
	_, err := r.db.Exec(`UPDATE accounts SET enabled=? WHERE id=?`, boolInt(on), id)
	return err
}

type modelRepo struct{ db *sql.DB }

func (r *modelRepo) List() ([]ModelMapping, error) {
	rows, err := r.db.Query(`SELECT id,public_model,upstream_model,enabled,created_at,updated_at FROM models ORDER BY public_model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ModelMapping
	for rows.Next() {
		var m ModelMapping
		var en int
		var ca, ua string
		if err := rows.Scan(&m.ID, &m.PublicModel, &m.UpstreamModel, &en, &ca, &ua); err != nil {
			return nil, err
		}
		m.Enabled = en == 1
		m.CreatedAt, _ = time.Parse(time.RFC3339, ca)
		m.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *modelRepo) GetByPublic(public string) (ModelMapping, error) {
	var m ModelMapping
	var en int
	var ca, ua string
	err := r.db.QueryRow(`SELECT id,public_model,upstream_model,enabled,created_at,updated_at FROM models WHERE public_model=?`, public).
		Scan(&m.ID, &m.PublicModel, &m.UpstreamModel, &en, &ca, &ua)
	if err != nil {
		return ModelMapping{}, mapNotFound(err)
	}
	m.Enabled = en == 1
	m.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	m.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return m, nil
}

func (r *modelRepo) Save(m ModelMapping) error {
	if m.ID == "" {
		m.ID = NewID()
	}
	existing, err := r.getByID(m.ID)
	if err == nil {
		m.CreatedAt = existing.CreatedAt
	} else if !errors.Is(err, ErrNotFound) {
		return err
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
	m.UpdatedAt = time.Now()
	// public_model 的 UNIQUE 冲突由驱动冒泡为非 nil error。
	_, err = r.db.Exec(`INSERT INTO models(id,public_model,upstream_model,enabled,created_at,updated_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET public_model=excluded.public_model, upstream_model=excluded.upstream_model,
			enabled=excluded.enabled, updated_at=excluded.updated_at`,
		m.ID, m.PublicModel, m.UpstreamModel, boolInt(m.Enabled),
		m.CreatedAt.Format(time.RFC3339), m.UpdatedAt.Format(time.RFC3339))
	return err
}

// getByID 按 id 查行（ModelRepo 接口不暴露 Get，仅供 Save 保留 CreatedAt）。
func (r *modelRepo) getByID(id string) (ModelMapping, error) {
	var m ModelMapping
	var en int
	var ca, ua string
	err := r.db.QueryRow(`SELECT id,public_model,upstream_model,enabled,created_at,updated_at FROM models WHERE id=?`, id).
		Scan(&m.ID, &m.PublicModel, &m.UpstreamModel, &en, &ca, &ua)
	if err != nil {
		return ModelMapping{}, mapNotFound(err)
	}
	m.Enabled = en == 1
	m.CreatedAt, _ = time.Parse(time.RFC3339, ca)
	m.UpdatedAt, _ = time.Parse(time.RFC3339, ua)
	return m, nil
}

func (r *modelRepo) Delete(id string) error {
	_, err := r.db.Exec(`DELETE FROM models WHERE id=?`, id)
	return err
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
