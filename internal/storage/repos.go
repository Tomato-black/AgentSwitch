package storage

import (
	"database/sql"
	"errors"
	"sync/atomic"
	"time"
)

// bumpRoute 推进路由配置版本号（见 Store.RouteVersion）。ver 为 nil 时是
// 无操作——测试可直接构造只读 repo，不必关心计数。
func bumpRoute(ver *atomic.Uint64) {
	if ver != nil {
		ver.Add(1)
	}
}

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

type channelRepo struct {
	db  *sql.DB
	ver *atomic.Uint64
}

const channelCols = `id,name,protocol,base_url,credential_ref,preset_id,enabled,allow_private_upstream,created_at,updated_at`

// HasUsableKey 报告是否存在 enabled 且 credential_ref 非空的渠道（单 Key 口径）。

type channelModelRepo struct {
	db  *sql.DB
	ver *atomic.Uint64
}

const modelCols = `id,channel_id,model_name,created_at`

// Suggestions 返回渠道的模型名列表（按名排序，目标编辑下拉数据源）。

// ---- logical_models / logical_model_targets ----

type logicalModelRepo struct {
	db  *sql.DB
	ver *atomic.Uint64
}

const logicalModelCols = `id,name,description,enabled,display_name,context_window,reasoning_levels,default_reasoning_level,created_at,updated_at`

type logicalModelTargetRepo struct {
	db  *sql.DB
	ver *atomic.Uint64
}

const targetCols = `id,logical_model_id,channel_id,model_name,priority`

// ---- access_configs ----

type accessConfigRepo struct{ db *sql.DB }

const accessConfigCols = `id,app_type,name,logical_model_id,settings,is_current,sort_order,created_at,updated_at`

// SetCurrent 在事务内原子切换该 app_type 的 current：先清同 app 全部
// is_current，再置目标 id（id 必须已存在，否则回滚报错）；id=="" 仅清空。
