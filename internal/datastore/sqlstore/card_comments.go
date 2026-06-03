package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type cardCommentRepo struct{ s *Store }

const cardCommentColumns = `id, board_id, card_id, author_id, author_kind, body, mentions, created_at, edited_at, reply_to, deleted_at`

func (r *cardCommentRepo) Create(ctx context.Context, c *datastore.CardComment) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO card_comments (`+cardCommentColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)`),
		c.ID, c.BoardID, c.CardID, c.AuthorID, c.AuthorKind, c.Body,
		nullable(c.Mentions), c.CreatedAt, nullableTime(c.EditedAt),
		nullable(c.ReplyTo), nullableTime(c.DeletedAt),
	)
	return r.s.scanErr(err)
}

func (r *cardCommentRepo) Get(ctx context.Context, id string) (*datastore.CardComment, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+cardCommentColumns+` FROM card_comments WHERE id = ?`), id)
	return scanCardComment(row.Scan, r.s.scanErr)
}

func (r *cardCommentRepo) Update(ctx context.Context, c *datastore.CardComment) error {
	now := time.Now().UTC()
	c.EditedAt = &now
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE card_comments SET body = ?, mentions = ?, edited_at = ? WHERE id = ?`),
		c.Body, nullable(c.Mentions), now, c.ID,
	)
	return r.s.scanErr(err)
}

func (r *cardCommentRepo) SoftDelete(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE card_comments SET deleted_at = ? WHERE id = ?`),
		now, id,
	)
	return r.s.scanErr(err)
}

func (r *cardCommentRepo) List(ctx context.Context, f datastore.CardCommentFilter) ([]datastore.CardComment, error) {
	q := `SELECT ` + cardCommentColumns + ` FROM card_comments WHERE card_id = ?`
	args := []any{f.CardID}
	if !f.IncludeDeleted {
		q += ` AND deleted_at IS NULL`
	}
	q += ` ORDER BY created_at ASC`
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
	var out []datastore.CardComment
	for rows.Next() {
		c, err := scanCardComment(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

func scanCardComment(scan scanFn, mapErr func(error) error) (*datastore.CardComment, error) {
	var (
		c         datastore.CardComment
		mentions  sql.NullString
		editedAt  sql.NullTime
		replyTo   sql.NullString
		deletedAt sql.NullTime
	)
	if err := scan(
		&c.ID, &c.BoardID, &c.CardID, &c.AuthorID, &c.AuthorKind, &c.Body,
		&mentions, &c.CreatedAt, &editedAt, &replyTo, &deletedAt,
	); err != nil {
		return nil, mapErr(err)
	}
	c.Mentions = mentions.String
	c.ReplyTo = replyTo.String
	if editedAt.Valid {
		t := editedAt.Time
		c.EditedAt = &t
	}
	if deletedAt.Valid {
		t := deletedAt.Time
		c.DeletedAt = &t
	}
	return &c, nil
}
