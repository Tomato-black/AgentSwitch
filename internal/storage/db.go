package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"

	_ "modernc.org/sqlite"
)

// migrationV1 是逻辑模型重构后的首发 schema（spec 2026-10-17-logical-models §5；
// 用户决策：删库重建——勿改已发布语句的纪律自本 v1 起生效）。ID 均为 TEXT
// NewID()，时间戳为 INTEGER 毫秒。
const migrationV1 = `
CREATE TABLE channels (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  protocol TEXT NOT NULL,
  base_url TEXT NOT NULL,
  credential_ref TEXT NOT NULL,
  preset_id TEXT NOT NULL DEFAULT '',
  allow_private_upstream INTEGER NOT NULL DEFAULT 0,
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE channel_models (
  id TEXT PRIMARY KEY,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  model_name TEXT NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE(channel_id, model_name)
);
CREATE TABLE logical_models (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL UNIQUE,
  description TEXT NOT NULL DEFAULT '',
  enabled INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE TABLE logical_model_targets (
  id TEXT PRIMARY KEY,
  logical_model_id TEXT NOT NULL REFERENCES logical_models(id) ON DELETE CASCADE,
  channel_id TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  model_name TEXT NOT NULL,
  priority INTEGER NOT NULL,
  UNIQUE(logical_model_id, channel_id, model_name)
);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// migrations 是按版本序执行的迁移表；migrate 只跑 schema_migrations 里
// 未记录的版本（幂等）。追加新版本 = 追加一行，勿改已发布语句。
var migrations = []struct {
	version int64
	sql     string
}{
	{1, migrationV1},
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
	if err := rejectLegacySchema(db); err != nil {
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

// rejectLegacySchema 是删库重建的旧库守卫：库中存在 channel_keys（多 Key 旧
// schema）时明确拒绝并提示删除文件，不静默清库也不就地迁移——旧库的
// schema_migrations 可能已记 v1，直接走 migrate 会把旧表原样留下。
func rejectLegacySchema(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='channel_keys'`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return errors.New("检测到旧版多 Key 数据库（存在 channel_keys 表）：本次为删库重建，不提供就地迁移；请删除数据库文件（agentswitch.db）后重启，配置将重建")
	}
	return nil
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

func (s *dbStore) Channels() ChannelRepo           { return &channelRepo{s.db} }
func (s *dbStore) ChannelModels() ChannelModelRepo { return &channelModelRepo{s.db} }
func (s *dbStore) LogicalModels() LogicalModelRepo { return &logicalModelRepo{s.db} }
func (s *dbStore) LogicalModelTargets() LogicalModelTargetRepo {
	return &logicalModelTargetRepo{s.db}
}
func (s *dbStore) Settings() SettingsRepo { return &settingsRepo{s.db} }
func (s *dbStore) Close() error           { return s.db.Close() }
