package storage

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"

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

// migrationV2 为逻辑模型追加元数据列（spec 2026-10-27 codex-access-models-
// metadata）：非破坏性就地升级，v1 库无需删库。reasoning_levels 存 JSON
// 数组文本（空串 = 未配置）。
const migrationV2 = `
ALTER TABLE logical_models ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE logical_models ADD COLUMN context_window INTEGER NOT NULL DEFAULT 0;
ALTER TABLE logical_models ADD COLUMN reasoning_levels TEXT NOT NULL DEFAULT '';
ALTER TABLE logical_models ADD COLUMN default_reasoning_level TEXT NOT NULL DEFAULT '';
`

// migrationV4 为 access_configs 增加 needs_apply：区分「仅保存」与「已应用」。
// v3 是合并单一 schema 前的旧版本号（rejectLegacySchema 拒绝），故从 4 起。
const migrationV4 = `
ALTER TABLE access_configs ADD COLUMN needs_apply INTEGER NOT NULL DEFAULT 0;
`

// migrationV5 新增 request_history：请求/路由决策的脱敏元数据，供重启后
// 排障与诊断导出；不存 prompt/response 正文、Authorization 或密钥。
const migrationV5 = `
CREATE TABLE request_history (
  id TEXT PRIMARY KEY,
  started_at INTEGER NOT NULL,
  model TEXT NOT NULL DEFAULT '',
  upstream_model TEXT NOT NULL DEFAULT '',
  channel TEXT NOT NULL DEFAULT '',
  account TEXT NOT NULL DEFAULT '',
  status INTEGER NOT NULL DEFAULT 0,
  duration_ms INTEGER NOT NULL DEFAULT 0,
  failover_count INTEGER NOT NULL DEFAULT 0,
  streaming INTEGER NOT NULL DEFAULT 0,
  error_class TEXT NOT NULL DEFAULT '',
  attempts_json TEXT NOT NULL DEFAULT '[]'
);
CREATE INDEX idx_request_history_started_at ON request_history(started_at DESC);
`

// migrationV6 为请求历史增加非流式 token usage（P1-2a）；流式暂不统计。
const migrationV6 = `
ALTER TABLE request_history ADD COLUMN input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_history ADD COLUMN output_tokens INTEGER NOT NULL DEFAULT 0;
`

// migrations 是按版本序执行的迁移表；migrate 只跑 schema_migrations 里
// 未记录的版本（幂等）。v1 之后只允许追加就地迁移（ALTER TABLE），勿就地
// 改已发布语句；需删库重建时先过用户决策。
var migrations = []struct {
	version int64
	sql     string
}{
	{1, migrationV1},
	{2, migrationV2},
	{4, migrationV4},
	{5, migrationV5},
	{6, migrationV6},
}

type dbStore struct {
	db  *sql.DB
	key *credential.MasterKey
	// routeVer 见 Store.RouteVersion；四个路由相关 repo 共享同一计数。
	routeVer atomic.Uint64
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
	// 删库重建（2026-10-19）：v3 记录是合并单一 v1 前的旧版本号，一律拒绝。
	// v2 自 2026-10-27 起是合法的就地迁移版本，改由列形状校验（见下）。
	ok, err := hasRecordedVersion(db, 3)
	if err != nil {
		return err
	}
	if ok {
		return errLegacyDB
	}
	// 已记 v2 的库必须真的含元数据列：migrate 见版本已记录会跳过，列缺失
	// 的残库放行后首次读写逻辑模型即裸 SQL 报错。
	v2, err := hasRecordedVersion(db, 2)
	if err != nil {
		return err
	}
	if v2 {
		hasLogicalModels, err := tableExists(db, "logical_models")
		if err != nil {
			return err
		}
		if !hasLogicalModels {
			return errLegacyDB
		}
		hasDisplayName, err := columnExists(db, "logical_models", "display_name")
		if err != nil {
			return err
		}
		if !hasDisplayName {
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
	// v4 记录必须真的含 needs_apply：migrate 见版本已记录会跳过，列缺失的
	// 残库放行后首次读写接入配置即裸 SQL 报错。
	v4, err := hasRecordedVersion(db, 4)
	if err != nil {
		return err
	}
	if v4 {
		hasNeedsApply, err := columnExists(db, "access_configs", "needs_apply")
		if err != nil {
			return err
		}
		if !hasNeedsApply {
			return errLegacyDB
		}
	}
	v5, err := hasRecordedVersion(db, 5)
	if err != nil {
		return err
	}
	if v5 {
		hasHistory, err := tableExists(db, "request_history")
		if err != nil {
			return err
		}
		if !hasHistory {
			return errLegacyDB
		}
	}
	v6, err := hasRecordedVersion(db, 6)
	if err != nil {
		return err
	}
	if v6 {
		for _, column := range []string{"input_tokens", "output_tokens"} {
			ok, err := columnExists(db, "request_history", column)
			if err != nil {
				return err
			}
			if !ok {
				return errLegacyDB
			}
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

func (s *dbStore) Channels() ChannelRepo { return &channelRepo{db: s.db, ver: &s.routeVer} }
func (s *dbStore) ChannelModels() ChannelModelRepo {
	return &channelModelRepo{db: s.db, ver: &s.routeVer}
}
func (s *dbStore) LogicalModels() LogicalModelRepo {
	return &logicalModelRepo{db: s.db, ver: &s.routeVer}
}
func (s *dbStore) LogicalModelTargets() LogicalModelTargetRepo {
	return &logicalModelTargetRepo{db: s.db, ver: &s.routeVer}
}
func (s *dbStore) AccessConfigs() AccessConfigRepo { return &accessConfigRepo{s.db} }
func (s *dbStore) RequestHistory() RequestHistoryRepo {
	return &requestHistoryRepo{s.db}
}
func (s *dbStore) Secrets() SecretRepo    { return &secretStore{db: s.db, key: s.key} }
func (s *dbStore) Settings() SettingsRepo { return &settingsRepo{s.db} }
func (s *dbStore) RouteVersion() uint64   { return s.routeVer.Load() }
func (s *dbStore) Close() error           { return s.db.Close() }
