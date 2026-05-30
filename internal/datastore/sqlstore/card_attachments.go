package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type cardAttachmentRepo struct{ s *Store }

const cardAttachmentColumns = `id, card_id, filename, mime_type, size_bytes, path, created_by, created_at`

func (r *cardAttachmentRepo) Create(ctx context.Context, a *datastore.CardAttachment) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.MimeType == "" {
		a.MimeType = "application/octet-stream"
	}
	a.CreatedAt = time.Now().UTC()
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO card_attachments (`+cardAttachmentColumns+`) VALUES (?,?,?,?,?,?,?,?)`),
		a.ID, a.CardID, a.Filename, a.MimeType, a.SizeBytes, a.Path, nullable(a.CreatedBy), a.CreatedAt,
	)
	return r.s.scanErr(err)
}

func (r *cardAttachmentRepo) Get(ctx context.Context, id string) (*datastore.CardAttachment, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+cardAttachmentColumns+` FROM card_attachments WHERE id = ?`), id)
	return scanCardAttachment(row.Scan, r.s.scanErr)
}

func (r *cardAttachmentRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(`DELETE FROM card_attachments WHERE id = ?`), id)
	return r.s.scanErr(err)
}

func (r *cardAttachmentRepo) ListByCard(ctx context.Context, cardID string) ([]datastore.CardAttachment, error) {
	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(
		`SELECT `+cardAttachmentColumns+` FROM card_attachments WHERE card_id = ? ORDER BY created_at DESC`), cardID)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	out := []datastore.CardAttachment{}
	for rows.Next() {
		a, err := scanCardAttachment(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *a)
	}
	return out, rows.Err()
}

func scanCardAttachment(scan func(...any) error, wrap func(error) error) (*datastore.CardAttachment, error) {
	var a datastore.CardAttachment
	var icon, createdBy sql.NullString
	err := scan(&a.ID, &a.CardID, &a.Filename, &a.MimeType, &a.SizeBytes, &a.Path, &createdBy, &a.CreatedAt)
	if err != nil {
		return nil, wrap(err)
	}
	a.CreatedBy = createdBy.String
	_ = icon // future-proofing
	return &a, nil
}
