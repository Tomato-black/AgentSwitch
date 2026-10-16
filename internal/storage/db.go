package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"

	_ "modernc.org/sqlite"
)

const migration001 = `
CREATE TABLE providers (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, protocol TEXT NOT NULL,
  base_url TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  allow_private_upstream INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE accounts (
  id TEXT PRIMARY KEY,
  provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  name TEXT NOT NULL, secret_key TEXT NOT NULL, secret_backend TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 10, enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX idx_accounts_provider ON accounts(provider_id);
CREATE TABLE models (
  id TEXT PRIMARY KEY, public_model TEXT NOT NULL UNIQUE,
  upstream_model TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

type dbStore struct{ db *sql.DB }

func Open(path string) (Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	for _, pragma := range []string{"PRAGMA journal_mode=WAL", "PRAGMA foreign_keys=ON", "PRAGMA busy_timeout=5000"} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	if err := os.Chmod(path, 0o600); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &dbStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=1`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(migration001); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (1)`); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

// NewID 返回 crypto/rand 16 字节的 hex 串。
func NewID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *dbStore) Providers() ProviderRepo { return &providerRepo{s.db} }
func (s *dbStore) Accounts() AccountRepo   { return &accountRepo{s.db} }
func (s *dbStore) Models() ModelRepo       { return &modelRepo{s.db} }
func (s *dbStore) Settings() SettingsRepo  { return &settingsRepo{s.db} }
func (s *dbStore) Close() error            { return s.db.Close() }
