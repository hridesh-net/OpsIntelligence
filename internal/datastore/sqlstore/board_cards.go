package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type boardCardRepo struct{ s *Store }

const boardCardColumns = `id, board_id, column_id, issue_number, title, description, card_type, priority, effort, status, assignee, assignee_type, branch, worktree_path, cost_usd, token_in, token_out, metadata_json, created_at, updated_at, started_at, completed_at`

func (r *boardCardRepo) Create(ctx context.Context, c *datastore.BoardCard) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	c.CreatedAt = now
	c.UpdatedAt = now
	cfg, err := json.Marshal(c.Metadata)
	if err != nil {
		cfg = []byte("{}")
	}
	_, err = r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO board_cards (`+boardCardColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
		c.ID, c.BoardID, c.ColumnID, nullableInt(c.IssueNumber), c.Title, nullable(c.Description),
		c.CardType, c.Priority, nullable(c.Effort), c.Status, nullable(c.Assignee), nullable(c.AssigneeType),
		nullable(c.Branch), nullable(c.WorktreePath), c.CostUSD, c.TokenIn, c.TokenOut, cfg,
		c.CreatedAt, c.UpdatedAt, nullableTime(c.StartedAt), nullableTime(c.CompletedAt),
	)
	return r.s.scanErr(err)
}

func (r *boardCardRepo) Get(ctx context.Context, id string) (*datastore.BoardCard, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+boardCardColumns+` FROM board_cards WHERE id = ?`), id)
	return scanBoardCard(row.Scan, r.s.scanErr)
}

func (r *boardCardRepo) Update(ctx context.Context, c *datastore.BoardCard) error {
	c.UpdatedAt = time.Now().UTC()
	cfg, err := json.Marshal(c.Metadata)
	if err != nil {
		cfg = []byte("{}")
	}
	_, err = r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE board_cards SET column_id = ?, issue_number = ?, title = ?, description = ?, card_type = ?, priority = ?, effort = ?, status = ?, assignee = ?, assignee_type = ?, branch = ?, worktree_path = ?, cost_usd = ?, token_in = ?, token_out = ?, metadata_json = ?, updated_at = ?, started_at = ?, completed_at = ? WHERE id = ?`),
		c.ColumnID, nullableInt(c.IssueNumber), c.Title, nullable(c.Description),
		c.CardType, c.Priority, nullable(c.Effort), c.Status, nullable(c.Assignee), nullable(c.AssigneeType),
		nullable(c.Branch), nullable(c.WorktreePath), c.CostUSD, c.TokenIn, c.TokenOut, cfg,
		c.UpdatedAt, nullableTime(c.StartedAt), nullableTime(c.CompletedAt), c.ID,
	)
	return r.s.scanErr(err)
}

func (r *boardCardRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(`DELETE FROM board_cards WHERE id = ?`), id)
	return r.s.scanErr(err)
}

func (r *boardCardRepo) Move(ctx context.Context, cardID, columnID string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE board_cards SET column_id = ?, updated_at = ? WHERE id = ?`),
		columnID, time.Now().UTC(), cardID,
	)
	return r.s.scanErr(err)
}

func (r *boardCardRepo) List(ctx context.Context, f datastore.BoardCardFilter) ([]datastore.BoardCard, error) {
	var where []string
	var args []any
	if f.BoardID != "" {
		where = append(where, `board_id = ?`)
		args = append(args, f.BoardID)
	}
	if f.ColumnID != "" {
		where = append(where, `column_id = ?`)
		args = append(args, f.ColumnID)
	}
	if f.Status != "" {
		where = append(where, `status = ?`)
		args = append(args, f.Status)
	}
	if f.Assignee != "" {
		where = append(where, `assignee = ?`)
		args = append(args, f.Assignee)
	}
	q := `SELECT ` + boardCardColumns + ` FROM board_cards`
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
	var out []datastore.BoardCard
	for rows.Next() {
		c, err := scanBoardCard(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func scanBoardCard(scan scanFn, mapErr func(error) error) (*datastore.BoardCard, error) {
	var (
		c            datastore.BoardCard
		issueNumber  sql.NullInt64
		description  sql.NullString
		effort       sql.NullString
		assignee     sql.NullString
		assigneeType sql.NullString
		branch       sql.NullString
		worktreePath sql.NullString
		cfgJSON      []byte
		startedAt    sql.NullTime
		completedAt  sql.NullTime
	)
	if err := scan(
		&c.ID, &c.BoardID, &c.ColumnID, &issueNumber, &c.Title, &description,
		&c.CardType, &c.Priority, &effort, &c.Status, &assignee, &assigneeType,
		&branch, &worktreePath, &c.CostUSD, &c.TokenIn, &c.TokenOut, &cfgJSON,
		&c.CreatedAt, &c.UpdatedAt, &startedAt, &completedAt,
	); err != nil {
		return nil, mapErr(err)
	}
	if issueNumber.Valid {
		v := int(issueNumber.Int64)
		c.IssueNumber = &v
	}
	c.Description = description.String
	c.Effort = effort.String
	c.Assignee = assignee.String
	c.AssigneeType = assigneeType.String
	c.Branch = branch.String
	c.WorktreePath = worktreePath.String
	if len(cfgJSON) > 0 {
		_ = json.Unmarshal(cfgJSON, &c.Metadata)
	}
	if startedAt.Valid {
		v := startedAt.Time
		c.StartedAt = &v
	}
	if completedAt.Valid {
		v := completedAt.Time
		c.CompletedAt = &v
	}
	return &c, nil
}
