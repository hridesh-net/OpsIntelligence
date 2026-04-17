package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type taskHistoryRepo struct{ s *Store }

const taskHistoryColumns = `id, task_id, session_id, subagent_id, goal, prompt, response,
    status, iterations, error, actor_id, created_at, started_at, completed_at, updated_at`

// Upsert writes a task row, creating it on the first call and updating
// the mutable columns on subsequent calls. SQLite uses "INSERT ...
// ON CONFLICT ... DO UPDATE"; Postgres uses the same syntax, so the
// single query string is portable.
func (r *taskHistoryRepo) Upsert(ctx context.Context, t *datastore.TaskHistory) error {
	now := time.Now().UTC()
	if t.CreatedAt.IsZero() {
		t.CreatedAt = now
	}
	t.UpdatedAt = now
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`INSERT INTO task_history(
            id, task_id, session_id, subagent_id, goal, prompt, response,
            status, iterations, error, actor_id,
            created_at, started_at, completed_at, updated_at
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
        ON CONFLICT(task_id) DO UPDATE SET
            status       = excluded.status,
            iterations   = excluded.iterations,
            goal         = excluded.goal,
            prompt       = excluded.prompt,
            response     = excluded.response,
            error        = excluded.error,
            started_at   = excluded.started_at,
            completed_at = excluded.completed_at,
            updated_at   = excluded.updated_at`),
		t.ID, t.TaskID, nullable(t.SessionID), nullable(t.SubAgentID),
		nullable(t.Goal), nullable(t.Prompt), nullable(t.Response),
		t.Status, t.Iterations, nullable(t.Error), nullable(t.ActorID),
		t.CreatedAt, nullableTime(t.StartedAt), nullableTime(t.CompletedAt), t.UpdatedAt,
	)
	return r.s.scanErr(err)
}

func (r *taskHistoryRepo) Get(ctx context.Context, taskID string) (*datastore.TaskHistory, error) {
	row := r.s.db.QueryRowContext(ctx,
		r.s.rebind(`SELECT `+taskHistoryColumns+` FROM task_history WHERE task_id = ?`), taskID)
	return scanTaskHistory(row.Scan, r.s.scanErr)
}

func (r *taskHistoryRepo) List(ctx context.Context, f datastore.TaskFilter) ([]datastore.TaskHistory, error) {
	var (
		where []string
		args  []any
	)
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, f.Status)
	}
	if f.ActorID != "" {
		where = append(where, `actor_id = ?`)
		args = append(args, f.ActorID)
	}
	if f.SessionID != "" {
		where = append(where, `session_id = ?`)
		args = append(args, f.SessionID)
	}
	if f.Since != nil {
		where = append(where, `created_at >= ?`)
		args = append(args, *f.Since)
	}
	if f.Until != nil {
		where = append(where, `created_at < ?`)
		args = append(args, *f.Until)
	}
	q := `SELECT ` + taskHistoryColumns + ` FROM task_history`
	if len(where) > 0 {
		q += ` WHERE ` + strings.Join(where, ` AND `)
	}
	q += ` ORDER BY created_at DESC`
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}
	q += ` LIMIT ? OFFSET ?`
	args = append(args, limit, f.Offset)

	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(q), args...)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.TaskHistory
	for rows.Next() {
		t, err := scanTaskHistory(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func (r *taskHistoryRepo) AppendEvent(ctx context.Context, e *datastore.TaskHistoryEvent) error {
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
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`INSERT INTO task_history_events(
            task_id, event_index, kind, phase, source, message, metadata_json, created_at
        ) VALUES (?,?,?,?,?,?,?,?)`),
		e.TaskID, e.Index, e.Kind, nullable(e.Phase), nullable(e.Source),
		e.Message, meta, e.CreatedAt,
	)
	return r.s.scanErr(err)
}

func (r *taskHistoryRepo) ListEvents(ctx context.Context, taskID string, sinceIndex, limit int) ([]datastore.TaskHistoryEvent, error) {
	if limit <= 0 {
		limit = 200
	}
	rows, err := r.s.db.QueryContext(ctx,
		r.s.rebind(`SELECT task_id, event_index, kind, phase, source, message, metadata_json, created_at
            FROM task_history_events
            WHERE task_id = ? AND event_index > ?
            ORDER BY event_index ASC
            LIMIT ?`),
		taskID, sinceIndex, limit)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.TaskHistoryEvent
	for rows.Next() {
		var (
			e     datastore.TaskHistoryEvent
			phase sql.NullString
			src   sql.NullString
			meta  sql.NullString
		)
		if err := rows.Scan(
			&e.TaskID, &e.Index, &e.Kind, &phase, &src, &e.Message, &meta, &e.CreatedAt,
		); err != nil {
			return nil, r.s.scanErr(err)
		}
		e.Phase = phase.String
		e.Source = src.String
		if meta.Valid && meta.String != "" {
			_ = json.Unmarshal([]byte(meta.String), &e.Metadata)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *taskHistoryRepo) DeleteOlderThan(ctx context.Context, cutoffUnix int64) (int, error) {
	cutoff := time.Unix(cutoffUnix, 0).UTC()
	res, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`DELETE FROM task_history
            WHERE status IN ('completed','failed','cancelled')
              AND (completed_at IS NOT NULL AND completed_at < ?)`),
		cutoff)
	if err != nil {
		return 0, r.s.scanErr(err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanTaskHistory(scan scanFn, mapErr func(error) error) (*datastore.TaskHistory, error) {
	var (
		t         datastore.TaskHistory
		session   sql.NullString
		sub       sql.NullString
		goal      sql.NullString
		prompt    sql.NullString
		response  sql.NullString
		errStr    sql.NullString
		actor     sql.NullString
		started   sql.NullTime
		completed sql.NullTime
	)
	if err := scan(
		&t.ID, &t.TaskID, &session, &sub, &goal, &prompt, &response,
		&t.Status, &t.Iterations, &errStr, &actor,
		&t.CreatedAt, &started, &completed, &t.UpdatedAt,
	); err != nil {
		return nil, mapErr(err)
	}
	t.SessionID = session.String
	t.SubAgentID = sub.String
	t.Goal = goal.String
	t.Prompt = prompt.String
	t.Response = response.String
	t.Error = errStr.String
	t.ActorID = actor.String
	if started.Valid {
		v := started.Time
		t.StartedAt = &v
	}
	if completed.Valid {
		v := completed.Time
		t.CompletedAt = &v
	}
	return &t, nil
}
