package storage

import (
	"database/sql"
	"errors"
)

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
