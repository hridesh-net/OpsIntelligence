package sqlstore

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type cardRunRepo struct{ s *Store }

const cardRunColumns = `id, card_id, run_number, agent_id, agent_type, model, persona_id, status, cost_usd, token_in, token_out, elapsed_ms, worktree_path, branch, base_branch, result_summary, error, created_by, created_at, completed_at`

func (r *cardRunRepo) Create(ctx context.Context, run *datastore.CardRun) error {
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	run.CreatedAt = now
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO card_runs (`+cardRunColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		run.ID, run.CardID, run.RunNumber, run.AgentID, run.AgentType, nullable(run.Model),
		nullable(run.PersonaID), run.Status, run.CostUSD, run.TokenIn, run.TokenOut, run.ElapsedMs,
		nullable(run.WorktreePath), nullable(run.Branch), nullable(run.BaseBranch),
		nullable(run.ResultSummary), nullable(run.Error), nullable(run.CreatedBy), run.CreatedAt,
		nullableTime(run.CompletedAt),
	)
	return r.s.scanErr(err)
}

func (r *cardRunRepo) Get(ctx context.Context, id string) (*datastore.CardRun, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+cardRunColumns+` FROM card_runs WHERE id = ?`), id)
	return scanCardRun(row.Scan, r.s.scanErr)
}

func (r *cardRunRepo) Update(ctx context.Context, run *datastore.CardRun) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE card_runs SET card_id = ?, run_number = ?, agent_id = ?, agent_type = ?, model = ?, persona_id = ?, status = ?, cost_usd = ?, token_in = ?, token_out = ?, elapsed_ms = ?, worktree_path = ?, branch = ?, base_branch = ?, result_summary = ?, error = ?, created_by = ?, completed_at = ? WHERE id = ?`),
		run.CardID, run.RunNumber, run.AgentID, run.AgentType, nullable(run.Model),
		nullable(run.PersonaID), run.Status, run.CostUSD, run.TokenIn, run.TokenOut, run.ElapsedMs,
		nullable(run.WorktreePath), nullable(run.Branch), nullable(run.BaseBranch),
		nullable(run.ResultSummary), nullable(run.Error), nullable(run.CreatedBy),
		nullableTime(run.CompletedAt), run.ID,
	)
	return r.s.scanErr(err)
}

func (r *cardRunRepo) List(ctx context.Context, f datastore.CardRunFilter) ([]datastore.CardRun, error) {
	var where []string
	var args []any
	if f.CardID != "" {
		where = append(where, `card_id = ?`)
		args = append(args, f.CardID)
	}
	if f.AgentID != "" {
		where = append(where, `agent_id = ?`)
		args = append(args, f.AgentID)
	}
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, f.Status)
	}
	q := `SELECT ` + cardRunColumns + ` FROM card_runs`
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
	var out []datastore.CardRun
	for rows.Next() {
		run, err := scanCardRun(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *run)
	}
	return out, rows.Err()
}

func scanCardRun(scan scanFn, mapErr func(error) error) (*datastore.CardRun, error) {
	var (
		run         datastore.CardRun
		model       sql.NullString
		personaID   sql.NullString
		worktree    sql.NullString
		branch      sql.NullString
		baseBranch  sql.NullString
		result      sql.NullString
		errStr      sql.NullString
		createdBy   sql.NullString
		completedAt sql.NullTime
	)
	if err := scan(
		&run.ID, &run.CardID, &run.RunNumber, &run.AgentID, &run.AgentType, &model,
		&personaID, &run.Status, &run.CostUSD, &run.TokenIn, &run.TokenOut, &run.ElapsedMs,
		&worktree, &branch, &baseBranch, &result, &errStr, &createdBy, &run.CreatedAt, &completedAt,
	); err != nil {
		return nil, mapErr(err)
	}
	run.Model = model.String
	run.PersonaID = personaID.String
	run.WorktreePath = worktree.String
	run.Branch = branch.String
	run.BaseBranch = baseBranch.String
	run.ResultSummary = result.String
	run.Error = errStr.String
	run.CreatedBy = createdBy.String
	if completedAt.Valid {
		v := completedAt.Time
		run.CompletedAt = &v
	}
	return &run, nil
}
