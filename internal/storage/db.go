package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	"AgentSwitch/internal/credential"
	_ "modernc.org/sqlite"
)

// migrationV1 是删库重建后的单一 schema（spec 2026-10-19-access-full-settings）：
// 合并原 v1/v2/v3，access_configs 以 settings 整份文档列取代 overrides。
// 用户决策（2026-10-19）：删库重建——旧库启动报错提示删一次库，不就地迁移。
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
CREATE TABLE channel_secrets (
  credential_ref TEXT PRIMARY KEY,
  ciphertext BLOB NOT NULL
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
CREATE TABLE access_configs (
  id TEXT PRIMARY KEY,
  app_type TEXT NOT NULL,
  name TEXT NOT NULL,
  logical_model_id TEXT NOT NULL,
  settings TEXT NOT NULL DEFAULT '{}',
  is_current INTEGER NOT NULL DEFAULT 0,
  sort_order INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX idx_access_configs_app_name ON access_configs(app_type, name);
CREATE UNIQUE INDEX idx_access_configs_app_current ON access_configs(app_type) WHERE is_current = 1;
`

// migrations 是按版本序执行的迁移表；migrate 只跑 schema_migrations 里
// 未记录的版本（幂等）。删库重建后只剩单一 v1——追加新版本须先过用户决策
// （是否又一次删库重建），勿就地改已发布语句。
var migrations = []struct {
	version int64
	sql     string
}{
	{1, migrationV1},
}

type dbStore struct {
	db  *sql.DB
	key *credential.MasterKey
}

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
	// 主密钥与库同目录（与旧 secrets-fallback.json 同位置约定）；缺失即生成。
	mk, err := credential.LoadMasterKey(filepath.Join(filepath.Dir(path), "master.key"))
	if err != nil {
		db.Close()
		return nil, err
	}
	return &dbStore{db: db, key: mk}, nil
}

// errLegacyDB 是删库重建守卫的统一文案：提示用户删除一次库文件。
var errLegacyDB = errors.New("检测到旧版数据库（schema_migrations 与最终 schema 不一致或含旧表）：本次为删库重建，不提供就地迁移；请删除数据库文件（agentswitch.db）后重启，配置将重建")

// rejectLegacySchema 是删库重建的旧库守卫：凡不能确认已是「最终 schema」的库
// 一律拒绝并提示删除文件，不静默清库也不就地迁移。
//
// 关键失败模式（用户实报「保存渠道没有 channels 表」）：providers 时代旧库
// schema_migrations 已记 v1，但无 channel_keys——旧守卫放行后 migrate 见 v1
// 已记录直接跳过，新表建不出来。因此除旧表名外，还须校验：已记 v1 时
// channels 存在且含 credential_ref。
func rejectLegacySchema(db *sql.DB) error {
	for _, legacyTable := range []string{"channel_keys", "providers", "accounts"} {
		ok, err := tableExists(db, legacyTable)
		if err != nil {
			return err
		}
		if ok {
			return errLegacyDB
		}
	}
	v1, err := hasRecordedVersion(db, 1)
	if err != nil {
		return err
	}
	if !v1 {
		// 空库或无版本记录：交给 migrate 建最终 schema。
		return nil
	}
	hasChannels, err := tableExists(db, "channels")
	if err != nil {
		return err
	}
	if !hasChannels {
		return errLegacyDB
	}
	hasCredRef, err := columnExists(db, "channels", "credential_ref")
	if err != nil {
		return err
	}
	if !hasCredRef {
		return errLegacyDB
	}
	// 已记 v1 即应是最终 schema（spec §2.3 评审补充 2026-10-19）：缺
	// channel_secrets/access_configs 是合并前的旧时代库——放行后首次读
	// 密钥/接入配置会裸 SQL 报错，正是本守卫要避免的故障形态。
	for _, required := range []string{"channel_secrets", "access_configs"} {
		ok, err := tableExists(db, required)
		if err != nil {
			return err
		}
		if !ok {
			return errLegacyDB
		}
	}
	// 删库重建（2026-10-19）：v2/v3 记录的库与旧形态 access_configs 一律拒绝。
	for _, legacyVersion := range []int64{2, 3} {
		ok, err := hasRecordedVersion(db, legacyVersion)
		if err != nil {
			return err
		}
		if ok {
			return errLegacyDB
		}
	}
	hasAccess, err := tableExists(db, "access_configs")
	if err != nil {
		return err
	}
	if hasAccess {
		hasSettings, err := columnExists(db, "access_configs", "settings")
		if err != nil {
			return err
		}
		if !hasSettings {
			return errLegacyDB
		}
	}
	return nil
}

func tableExists(db *sql.DB, name string) (bool, error) {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n)
	return n > 0, err
}

func hasRecordedVersion(db *sql.DB, version int64) (bool, error) {
	ok, err := tableExists(db, "schema_migrations")
	if err != nil || !ok {
		return false, err
	}
	var n int
	err = db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE version=?`, version).Scan(&n)
	return n > 0, err
}

func columnExists(db *sql.DB, table, column string) (bool, error) {
	// PRAGMA 表名不能参数化；表名来自本包白名单/调用方固定字符串。
	rows, err := db.Query(`PRAGMA table_info(` + table + `)`)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name, typ string
			notNull   int
			dflt      sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
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
func (s *dbStore) AccessConfigs() AccessConfigRepo { return &accessConfigRepo{s.db} }
func (s *dbStore) Secrets() SecretRepo             { return &secretStore{db: s.db, key: s.key} }
func (s *dbStore) Settings() SettingsRepo          { return &settingsRepo{s.db} }
func (s *dbStore) Close() error                    { return s.db.Close() }
