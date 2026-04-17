package sqlstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type apiKeyRepo struct{ s *Store }

const apiKeyColumns = `id, key_id, hash, user_id, name, scopes,
    created_at, last_used_at, expires_at, revoked_at`

func (r *apiKeyRepo) Create(ctx context.Context, k *datastore.APIKey) error {
	if k.CreatedAt.IsZero() {
		k.CreatedAt = time.Now().UTC()
	}
	scopes, _ := json.Marshal(k.Scopes)
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`INSERT INTO api_keys(
            id, key_id, hash, user_id, name, scopes,
            created_at, last_used_at, expires_at, revoked_at
        ) VALUES (?,?,?,?,?,?,?,?,?,?)`),
		k.ID, k.KeyID, k.Hash, k.UserID, k.Name, string(scopes),
		k.CreatedAt, nullableTime(k.LastUsedAt), nullableTime(k.ExpiresAt), nullableTime(k.RevokedAt),
	)
	return r.s.scanErr(err)
}

func (r *apiKeyRepo) GetByKeyID(ctx context.Context, keyID string) (*datastore.APIKey, error) {
	row := r.s.db.QueryRowContext(ctx,
		r.s.rebind(`SELECT `+apiKeyColumns+` FROM api_keys WHERE key_id = ?`), keyID)
	return scanAPIKey(row.Scan, r.s.scanErr)
}

func (r *apiKeyRepo) ListForUser(ctx context.Context, userID string) ([]datastore.APIKey, error) {
	rows, err := r.s.db.QueryContext(ctx,
		r.s.rebind(`SELECT `+apiKeyColumns+` FROM api_keys WHERE user_id = ? ORDER BY created_at DESC`),
		userID)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	return scanAPIKeys(rows, r.s.scanErr)
}

func (r *apiKeyRepo) ListAll(ctx context.Context, limit, offset int) ([]datastore.APIKey, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.s.db.QueryContext(ctx,
		r.s.rebind(`SELECT `+apiKeyColumns+` FROM api_keys ORDER BY created_at DESC LIMIT ? OFFSET ?`),
		limit, offset)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	return scanAPIKeys(rows, r.s.scanErr)
}

func (r *apiKeyRepo) TouchUsage(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`UPDATE api_keys SET last_used_at = ? WHERE id = ?`),
		time.Now().UTC(), id)
	return r.s.scanErr(err)
}

func (r *apiKeyRepo) Revoke(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`UPDATE api_keys SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`),
		time.Now().UTC(), id)
	return r.s.scanErr(err)
}

func (r *apiKeyRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`DELETE FROM api_keys WHERE id = ?`), id)
	return r.s.scanErr(err)
}

// ─── helpers ────────────────────────────────────────────────────────

func scanAPIKeys(rows *sql.Rows, mapErr func(error) error) ([]datastore.APIKey, error) {
	var out []datastore.APIKey
	for rows.Next() {
		k, err := scanAPIKey(rows.Scan, mapErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

func scanAPIKey(scan scanFn, mapErr func(error) error) (*datastore.APIKey, error) {
	var (
		k         datastore.APIKey
		scopesRaw string
		lastUsed  sql.NullTime
		expires   sql.NullTime
		revoked   sql.NullTime
	)
	if err := scan(
		&k.ID, &k.KeyID, &k.Hash, &k.UserID, &k.Name, &scopesRaw,
		&k.CreatedAt, &lastUsed, &expires, &revoked,
	); err != nil {
		return nil, mapErr(err)
	}
	if scopesRaw != "" {
		_ = json.Unmarshal([]byte(scopesRaw), &k.Scopes)
	}
	if lastUsed.Valid {
		t := lastUsed.Time
		k.LastUsedAt = &t
	}
	if expires.Valid {
		t := expires.Time
		k.ExpiresAt = &t
	}
	if revoked.Valid {
		t := revoked.Time
		k.RevokedAt = &t
	}
	return &k, nil
}
