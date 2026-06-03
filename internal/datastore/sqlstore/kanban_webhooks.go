package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type kanbanWebhookRepo struct{ s *Store }

// nullableIntVal mirrors nullableInt for non-pointer ints — a zero
// value is stored as SQL NULL.
func nullableIntVal(v int) any {
	if v == 0 {
		return nil
	}
	return v
}

const kanbanWebhookColumns = `id, board_id, url, secret, events, active, created_at, last_status, last_error, last_delivery`

func (r *kanbanWebhookRepo) Create(ctx context.Context, w *datastore.KanbanWebhook) error {
	if w.ID == "" {
		w.ID = uuid.NewString()
	}
	if w.CreatedAt.IsZero() {
		w.CreatedAt = time.Now().UTC()
	}
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO kanban_webhooks (`+kanbanWebhookColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?)`),
		w.ID, nullable(w.BoardID), w.URL, w.Secret, w.Events, w.Active, w.CreatedAt,
		nullableIntVal(w.LastStatus), nullable(w.LastError), nullableTime(w.LastDelivery),
	)
	return r.s.scanErr(err)
}

func (r *kanbanWebhookRepo) Get(ctx context.Context, id string) (*datastore.KanbanWebhook, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+kanbanWebhookColumns+` FROM kanban_webhooks WHERE id = ?`), id)
	return scanKanbanWebhook(row.Scan, r.s.scanErr)
}

func (r *kanbanWebhookRepo) Update(ctx context.Context, w *datastore.KanbanWebhook) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE kanban_webhooks SET board_id = ?, url = ?, secret = ?, events = ?, active = ? WHERE id = ?`),
		nullable(w.BoardID), w.URL, w.Secret, w.Events, w.Active, w.ID,
	)
	return r.s.scanErr(err)
}

func (r *kanbanWebhookRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(`DELETE FROM kanban_webhooks WHERE id = ?`), id)
	return r.s.scanErr(err)
}

func (r *kanbanWebhookRepo) List(ctx context.Context) ([]datastore.KanbanWebhook, error) {
	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(
		`SELECT `+kanbanWebhookColumns+` FROM kanban_webhooks ORDER BY created_at DESC`))
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.KanbanWebhook
	for rows.Next() {
		w, err := scanKanbanWebhook(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *w)
	}
	return out, rows.Err()
}

func (r *kanbanWebhookRepo) UpdateDeliveryStatus(ctx context.Context, id string, status int, errMsg string) error {
	now := time.Now().UTC()
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE kanban_webhooks SET last_status = ?, last_error = ?, last_delivery = ? WHERE id = ?`),
		status, nullable(errMsg), now, id,
	)
	return r.s.scanErr(err)
}

func scanKanbanWebhook(scan scanFn, mapErr func(error) error) (*datastore.KanbanWebhook, error) {
	var (
		w           datastore.KanbanWebhook
		boardID     sql.NullString
		active      sql.NullBool
		lastStatus  sql.NullInt64
		lastError   sql.NullString
		lastDeliv   sql.NullTime
	)
	if err := scan(
		&w.ID, &boardID, &w.URL, &w.Secret, &w.Events, &active, &w.CreatedAt,
		&lastStatus, &lastError, &lastDeliv,
	); err != nil {
		return nil, mapErr(err)
	}
	w.BoardID = boardID.String
	w.Active = active.Bool
	w.LastStatus = int(lastStatus.Int64)
	w.LastError = lastError.String
	if lastDeliv.Valid {
		t := lastDeliv.Time
		w.LastDelivery = &t
	}
	return &w, nil
}
