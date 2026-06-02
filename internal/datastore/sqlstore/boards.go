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

type boardRepo struct{ s *Store }

const boardColumns = `id, name, team_id, repo_url, repo_path, mode, config_json, created_at, updated_at`

func (r *boardRepo) Create(ctx context.Context, b *datastore.Board) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	b.CreatedAt = now
	b.UpdatedAt = now
	cfg, err := json.Marshal(b.Config)
	if err != nil {
		cfg = []byte("{}")
	}
	_, err = r.s.db.ExecContext(ctx, r.s.rebind(
		`INSERT INTO boards (`+boardColumns+`) VALUES (?,?,?,?,?,?,?,?,?)`),
		b.ID, b.Name, nullable(b.TeamID), nullable(b.RepoURL), nullable(b.RepoPath),
		b.Mode, cfg, b.CreatedAt, b.UpdatedAt,
	)
	return r.s.scanErr(err)
}

func (r *boardRepo) Get(ctx context.Context, id string) (*datastore.Board, error) {
	row := r.s.db.QueryRowContext(ctx, r.s.rebind(
		`SELECT `+boardColumns+` FROM boards WHERE id = ?`), id)
	return scanBoard(row.Scan, r.s.scanErr)
}

func (r *boardRepo) Update(ctx context.Context, b *datastore.Board) error {
	b.UpdatedAt = time.Now().UTC()
	cfg, err := json.Marshal(b.Config)
	if err != nil {
		cfg = []byte("{}")
	}
	_, err = r.s.db.ExecContext(ctx, r.s.rebind(
		`UPDATE boards SET name = ?, team_id = ?, repo_url = ?, repo_path = ?, mode = ?, config_json = ?, updated_at = ? WHERE id = ?`),
		b.Name, nullable(b.TeamID), nullable(b.RepoURL), nullable(b.RepoPath),
		b.Mode, cfg, b.UpdatedAt, b.ID,
	)
	return r.s.scanErr(err)
}

func (r *boardRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx, r.s.rebind(`DELETE FROM boards WHERE id = ?`), id)
	return r.s.scanErr(err)
}

func (r *boardRepo) List(ctx context.Context, f datastore.BoardFilter) ([]datastore.Board, error) {
	var where []string
	var args []any
	if f.TeamID != "" {
		where = append(where, `team_id = ?`)
		args = append(args, f.TeamID)
	}
	q := `SELECT ` + boardColumns + ` FROM boards`
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
	var out []datastore.Board
	for rows.Next() {
		b, err := scanBoard(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *b)
	}
	return out, rows.Err()
}

func scanBoard(scan func(...any) error, scanErr func(error) error) (*datastore.Board, error) {
	var b datastore.Board
	var cfgJSON []byte
	// team_id / repo_url / repo_path are stored as NULL when unset (see
	// nullable() on the write side). Scanning a NULL into a plain `string`
	// errors out, so funnel them through sql.NullString.
	var teamID, repoURL, repoPath sql.NullString
	err := scan(&b.ID, &b.Name, &teamID, &repoURL, &repoPath, &b.Mode, &cfgJSON, &b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		return nil, scanErr(err)
	}
	b.TeamID = teamID.String
	b.RepoURL = repoURL.String
	b.RepoPath = repoPath.String
	if len(cfgJSON) > 0 {
		_ = json.Unmarshal(cfgJSON, &b.Config)
	}
	return &b, nil
}
