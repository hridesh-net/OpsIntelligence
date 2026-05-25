package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type boardColumnRepo struct{ s *Store }

const boardColumnColumns = `id, board_id, name, position, color, wip_limit, created_at`

func (r *boardColumnRepo) Create(ctx context.Context, c *datastore.BoardColumn) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	c.CreatedAt = now
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO board_columns (`+boardColumnColumns+`) VALUES (?,?,?,?,?,?,?)`),
		c.ID, c.BoardID, c.Name, c.Position, nullable(c.Color), nullableInt(c.WIPLimit), c.CreatedAt,
	)
	return r.s.scanErr(err)
}

func (r *boardColumnRepo) Get(ctx context.Context, id string) (*datastore.BoardColumn, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+boardColumnColumns+` FROM board_columns WHERE id = ?`), id)
	return scanBoardColumn(row.Scan, r.s.scanErr)
}

func (r *boardColumnRepo) Update(ctx context.Context, c *datastore.BoardColumn) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE board_columns SET name = ?, position = ?, color = ?, wip_limit = ? WHERE id = ?`),
		c.Name, c.Position, nullable(c.Color), nullableInt(c.WIPLimit), c.ID,
	)
	return r.s.scanErr(err)
}

func (r *boardColumnRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(`DELETE FROM board_columns WHERE id = ?`), id)
	return r.s.scanErr(err)
}

func (r *boardColumnRepo) ListByBoard(ctx context.Context, boardID string) ([]datastore.BoardColumn, error) {
	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(
		`SELECT `+boardColumnColumns+` FROM board_columns WHERE board_id = ? ORDER BY position ASC`),
		boardID)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.BoardColumn
	for rows.Next() {
		c, err := scanBoardColumn(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func scanBoardColumn(scan scanFn, mapErr func(error) error) (*datastore.BoardColumn, error) {
	var (
		c        datastore.BoardColumn
		color    sql.NullString
		wipLimit sql.NullInt64
	)
	if err := scan(
		&c.ID, &c.BoardID, &c.Name, &c.Position, &color, &wipLimit, &c.CreatedAt,
	); err != nil {
		return nil, mapErr(err)
	}
	c.Color = color.String
	if wipLimit.Valid {
		v := int(wipLimit.Int64)
		c.WIPLimit = &v
	}
	return &c, nil
}
