package storage

import (
	"database/sql"
	"encoding/json"
)

// requestHistoryRepo 读写 request_history；只存脱敏元数据。
type requestHistoryRepo struct{ db *sql.DB }

const requestHistoryCols = `id,started_at,model,upstream_model,channel,account,status,duration_ms,failover_count,streaming,error_class,attempts_json,input_tokens,output_tokens`

func (r *requestHistoryRepo) Add(rec RequestHistoryRecord) error {
	attempts, err := json.Marshal(rec.Attempts)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`INSERT INTO request_history(`+requestHistoryCols+`)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO NOTHING`,
		rec.ID, rec.StartedAt, rec.Model, rec.UpstreamModel, rec.Channel, rec.Account,
		rec.Status, rec.DurationMs, rec.FailoverCount, boolInt(rec.Streaming),
		rec.ErrorClass, string(attempts), rec.InputTokens, rec.OutputTokens)
	return err
}

func (r *requestHistoryRepo) ListRecent(limit int, sinceMs int64) ([]RequestHistoryRecord, error) {
	if limit <= 0 {
		limit = 100
	}
	where := ""
	args := []any{limit}
	if sinceMs > 0 {
		where = ` WHERE started_at >= ?`
		args = append([]any{sinceMs}, limit)
	}
	rows, err := r.db.Query(`SELECT `+requestHistoryCols+`
		FROM request_history`+where+` ORDER BY started_at DESC, rowid DESC LIMIT ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RequestHistoryRecord
	for rows.Next() {
		var rec RequestHistoryRecord
		var streaming int
		var attempts string
		if err := rows.Scan(&rec.ID, &rec.StartedAt, &rec.Model, &rec.UpstreamModel,
			&rec.Channel, &rec.Account, &rec.Status, &rec.DurationMs,
			&rec.FailoverCount, &streaming, &rec.ErrorClass, &attempts,
			&rec.InputTokens, &rec.OutputTokens); err != nil {
			return nil, err
		}
		rec.Streaming = streaming == 1
		if attempts != "" {
			_ = json.Unmarshal([]byte(attempts), &rec.Attempts)
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (r *requestHistoryRepo) DeleteOlderThan(cutoffMs int64) (int64, error) {
	res, err := r.db.Exec(`DELETE FROM request_history WHERE started_at < ?`, cutoffMs)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (r *requestHistoryRepo) UsageByChannelModel(sinceMs int64) ([]UsageAggregate, error) {
	rows, err := r.db.Query(`SELECT channel, model, COUNT(*),
		COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		FROM request_history WHERE started_at >= ?
		GROUP BY channel, model
		ORDER BY SUM(input_tokens+output_tokens) DESC, channel ASC, model ASC`, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []UsageAggregate
	for rows.Next() {
		var a UsageAggregate
		if err := rows.Scan(&a.Channel, &a.Model, &a.Requests, &a.InputTokens, &a.OutputTokens); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *requestHistoryRepo) UsageByDay(sinceMs int64) ([]DailyUsage, error) {
	rows, err := r.db.Query(`SELECT strftime('%Y-%m-%d', started_at/1000, 'unixepoch', 'localtime') AS day,
		COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		FROM request_history WHERE started_at >= ?
		GROUP BY day ORDER BY day`, sinceMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DailyUsage
	for rows.Next() {
		var d DailyUsage
		if err := rows.Scan(&d.Day, &d.Requests, &d.InputTokens, &d.OutputTokens); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
