package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type pendingDecisionRepo struct{ s *Store }

const pendingDecisionColumns = `id, run_id, card_id, question, options_json, status, answer, answered_at, created_at`

func (r *pendingDecisionRepo) Create(ctx context.Context, d *datastore.PendingDecision) error {
	if d.ID == "" {
		d.ID = uuid.NewString()
	}
	d.CreatedAt = time.Now().UTC()
	if d.Status == "" {
		d.Status = "pending"
	}
	opts, err := json.Marshal(d.Options)
	if err != nil {
		return err
	}
	_, err = r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO pending_decisions (`+pendingDecisionColumns+`) VALUES (?,?,?,?,?,?,?,?,?)`),
		d.ID, d.RunID, d.CardID, d.Question, opts, d.Status, nullable(d.Answer),
		nullableTime(d.AnsweredAt), d.CreatedAt,
	)
	return r.s.scanErr(err)
}

func (r *pendingDecisionRepo) Get(ctx context.Context, id string) (*datastore.PendingDecision, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+pendingDecisionColumns+` FROM pending_decisions WHERE id = ?`), id)
	return scanPendingDecision(row.Scan, r.s.scanErr)
}

func (r *pendingDecisionRepo) Answer(ctx context.Context, id, answer string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE pending_decisions SET status = ?, answer = ?, answered_at = ? WHERE id = ?`),
		"answered", answer, time.Now().UTC(), id,
	)
	return r.s.scanErr(err)
}

func (r *pendingDecisionRepo) Dismiss(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE pending_decisions SET status = ?, answered_at = ? WHERE id = ?`),
		"dismissed", time.Now().UTC(), id,
	)
	return r.s.scanErr(err)
}

func (r *pendingDecisionRepo) ListByRun(ctx context.Context, runID string) ([]datastore.PendingDecision, error) {
	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(
		`SELECT `+pendingDecisionColumns+` FROM pending_decisions WHERE run_id = ? ORDER BY created_at DESC`),
		runID)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.PendingDecision
	for rows.Next() {
		d, err := scanPendingDecision(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (r *pendingDecisionRepo) ListByCard(ctx context.Context, cardID string) ([]datastore.PendingDecision, error) {
	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(
		`SELECT `+pendingDecisionColumns+` FROM pending_decisions WHERE card_id = ? ORDER BY created_at DESC`),
		cardID)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.PendingDecision
	for rows.Next() {
		d, err := scanPendingDecision(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func scanPendingDecision(scan scanFn, mapErr func(error) error) (*datastore.PendingDecision, error) {
	var (
		d           datastore.PendingDecision
		answer      sql.NullString
		answeredAt  sql.NullTime
		optionsJSON []byte
	)
	if err := scan(
		&d.ID, &d.RunID, &d.CardID, &d.Question, &optionsJSON, &d.Status, &answer, &answeredAt, &d.CreatedAt,
	); err != nil {
		return nil, mapErr(err)
	}
	if len(optionsJSON) > 0 {
		_ = json.Unmarshal(optionsJSON, &d.Options)
	}
	d.Answer = answer.String
	if answeredAt.Valid {
		v := answeredAt.Time
		d.AnsweredAt = &v
	}
	return &d, nil
}
