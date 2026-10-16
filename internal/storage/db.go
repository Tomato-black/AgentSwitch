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

// migration002 追加 spec §12.4 的"密钥尾4"列（列默认空串：历史行留空）直到
// 下次轮换——迁移绝不回读钥匙环（迁移不得触发凭据访问）。
const migration002 = `ALTER TABLE accounts ADD COLUMN key_tail TEXT NOT NULL DEFAULT ''`

// migration003 是渠道化重构（spec 2026-10-17 §3.1）的三张新表。过渡期与旧
// providers/accounts/models 并存；Task 10 合并为单一 v1 并删旧表（spec 决策 5：
// 发布前无存量用户，删库重建）。
const migration003 = `
CREATE TABLE channels (
  id TEXT PRIMARY KEY, name TEXT NOT NULL, type TEXT NOT NULL,
  protocol TEXT NOT NULL, base_url TEXT NOT NULL,
  priority INTEGER NOT NULL DEFAULT 10, enabled INTEGER NOT NULL DEFAULT 1,
  allow_private_upstream INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE TABLE channel_keys (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  name TEXT NOT NULL, secret_key TEXT NOT NULL, secret_backend TEXT NOT NULL,
  key_tail TEXT NOT NULL DEFAULT '', priority INTEGER NOT NULL DEFAULT 10,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL
);
CREATE INDEX idx_channel_keys_channel ON channel_keys(channel_id);
CREATE TABLE channel_models (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  public_model TEXT NOT NULL, upstream_model TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at TEXT NOT NULL, updated_at TEXT NOT NULL,
  UNIQUE(channel_id, public_model)
);
CREATE INDEX idx_channel_models_channel ON channel_models(channel_id);
CREATE INDEX idx_channel_models_public ON channel_models(public_model);
`

// migrations 是按版本序执行的迁移表；migrate 只跑 schema_migrations 里
// 未记录的版本（幂等）。追加新版本 = 追加一行，勿改已发布语句。
var migrations = []struct {
	version int64
	sql     string
}{
	{1, migration001},
	{2, migration002},
	{3, migration003},
}

type dbStore struct{ db *sql.DB }

func Open(path string) (Store, error) {
	// PRAGMA 经 DSN 下发，modernc 驱动对每条新建连接都会执行
	// （db.Exec("PRAGMA ...") 只作用于池当时借出的一条连接，并发下级联会静默失效）。
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// Ping 立即建连并跑 DSN pragma，尽早暴露坏 DSN/不可写路径。
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
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

// migrate 按 migrations 版本序补齐未执行的版本：每版本独立事务（DDL 与版本
// 记录同成同败），已记录的版本跳过——重开与并发进程重放都幂等。
func migrate(db *sql.DB) error {
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)`); err != nil {
		return err
	}
	for _, m := range migrations {
		var n int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, m.version).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(m.sql); err != nil {
			tx.Rollback()
			return err
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version) VALUES (?)`, m.version); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// NewID 返回 crypto/rand 16 字节的 hex 串。
func NewID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (s *dbStore) Providers() ProviderRepo         { return &providerRepo{s.db} }
func (s *dbStore) Accounts() AccountRepo           { return &accountRepo{s.db} }
func (s *dbStore) Models() ModelRepo               { return &modelRepo{s.db} }
func (s *dbStore) Channels() ChannelRepo           { return &channelRepo{s.db} }
func (s *dbStore) ChannelKeys() ChannelKeyRepo     { return &channelKeyRepo{s.db} }
func (s *dbStore) ChannelModels() ChannelModelRepo { return &channelModelRepo{s.db} }
func (s *dbStore) Settings() SettingsRepo          { return &settingsRepo{s.db} }
func (s *dbStore) Close() error                    { return s.db.Close() }
