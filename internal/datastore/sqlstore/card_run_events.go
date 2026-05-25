package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type cardRunEventRepo struct{ s *Store }

const cardRunEventColumns = `id, run_id, kind, phase, message, metadata_json, created_at`

func (r *cardRunEventRepo) Append(ctx context.Context, e *datastore.CardRunEvent) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now().UTC()
	}
	var meta sql.NullString
	if len(e.Metadata) > 0 {
		b, err := json.Marshal(e.Metadata)
		if err != nil {
			return err
		}
		meta.String = string(b)
		meta.Valid = true
	}
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO card_run_events (run_id, kind, phase, message, metadata_json, created_at) VALUES (?,?,?,?,?,?)`),
		e.RunID, e.Kind, nullable(e.Phase), e.Message, meta, e.CreatedAt,
	)
	return r.s.scanErr(err)
}

func (r *cardRunEventRepo) List(ctx context.Context, f datastore.CardRunEventFilter) ([]datastore.CardRunEvent, error) {
	var args []any
	q := `SELECT ` + cardRunEventColumns + ` FROM card_run_events WHERE run_id = ?`
	args = append(args, f.RunID)
	if f.SinceID > 0 {
		q += ` AND id > ?`
		args = append(args, f.SinceID)
	}
	q += ` ORDER BY id ASC`
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	q += ` LIMIT ?`
	args = append(args, limit)

	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(q), args...)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.CardRunEvent
	for rows.Next() {
		var (
			e     datastore.CardRunEvent
			phase sql.NullString
			meta  sql.NullString
		)
		if err := rows.Scan(
			&e.ID, &e.RunID, &e.Kind, &phase, &e.Message, &meta, &e.CreatedAt,
		); err != nil {
			return nil, r.s.scanErr(err)
		}
		e.Phase = phase.String
		if meta.Valid && meta.String != "" {
			_ = json.Unmarshal([]byte(meta.String), &e.Metadata)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
