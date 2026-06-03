package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type cardRelationshipRepo struct{ s *Store }

const cardRelationshipColumns = `id, board_id, src_card_id, dst_card_id, kind, created_at, created_by`

func (r *cardRelationshipRepo) Create(ctx context.Context, rel *datastore.CardRelationship) error {
	if rel.ID == "" {
		rel.ID = uuid.NewString()
	}
	if rel.CreatedAt.IsZero() {
		rel.CreatedAt = time.Now().UTC()
	}
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO card_relationships (`+cardRelationshipColumns+`) VALUES (?,?,?,?,?,?,?)`),
		rel.ID, rel.BoardID, rel.SrcCardID, rel.DstCardID, rel.Kind, rel.CreatedAt, nullable(rel.CreatedBy),
	)
	return r.s.scanErr(err)
}

func (r *cardRelationshipRepo) Get(ctx context.Context, id string) (*datastore.CardRelationship, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+cardRelationshipColumns+` FROM card_relationships WHERE id = ?`), id)
	return scanCardRelationship(row.Scan, r.s.scanErr)
}

func (r *cardRelationshipRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(
		`DELETE FROM card_relationships WHERE id = ?`), id)
	return r.s.scanErr(err)
}

// ListForCard returns every edge where the card is either source or
// destination. The caller can split them into "outgoing" and "incoming"
// in-process — keeps the SQL simple and stays correct under arbitrary
// kinds.
func (r *cardRelationshipRepo) ListForCard(ctx context.Context, cardID string) ([]datastore.CardRelationship, error) {
	rows, err := r.s.db.QueryContext(ctx, r.s.rebind(
		`SELECT `+cardRelationshipColumns+` FROM card_relationships
		 WHERE src_card_id = ? OR dst_card_id = ?
		 ORDER BY created_at ASC`),
		cardID, cardID,
	)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.CardRelationship
	for rows.Next() {
		rel, err := scanCardRelationship(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *rel)
	}
	return out, rows.Err()
}

// ListAncestors walks the `parent` chain upward from cardID, returning
// every reachable ancestor id. Used by the create handler to refuse
// parent-edges that would create a cycle.
func (r *cardRelationshipRepo) ListAncestors(ctx context.Context, cardID string) ([]string, error) {
	seen := map[string]struct{}{}
	queue := []string{cardID}
	for len(queue) > 0 {
		head := queue[0]
		queue = queue[1:]
		rows, err := r.s.db.QueryContext(ctx, r.s.rebind(
			`SELECT dst_card_id FROM card_relationships WHERE src_card_id = ? AND kind = 'parent'`),
			head,
		)
		if err != nil {
			return nil, r.s.scanErr(err)
		}
		for rows.Next() {
			var parent string
			if err := rows.Scan(&parent); err != nil {
				rows.Close()
				return nil, r.s.scanErr(err)
			}
			if _, dup := seen[parent]; dup {
				continue
			}
			seen[parent] = struct{}{}
			queue = append(queue, parent)
		}
		rows.Close()
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out, nil
}

func scanCardRelationship(scan scanFn, mapErr func(error) error) (*datastore.CardRelationship, error) {
	var (
		rel       datastore.CardRelationship
		createdBy sql.NullString
	)
	if err := scan(
		&rel.ID, &rel.BoardID, &rel.SrcCardID, &rel.DstCardID, &rel.Kind, &rel.CreatedAt, &createdBy,
	); err != nil {
		return nil, mapErr(err)
	}
	rel.CreatedBy = createdBy.String
	return &rel, nil
}
