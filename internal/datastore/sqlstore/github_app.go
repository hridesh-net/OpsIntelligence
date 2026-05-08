package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/githubapp"
)

// githubAppRepo implements githubapp.InstallationRepo against the shared sqlstore.
type githubAppRepo struct{ s *Store }

func (r *githubAppRepo) Upsert(ctx context.Context, i *githubapp.Installation) error {
	now := time.Now().UTC()
	q := r.s.rebind(`
INSERT INTO github_app_installations
    (id, account_login, account_type, ops_endpoint, ops_webhook_secret, active, created_at, updated_at)
VALUES
    (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    account_login      = excluded.account_login,
    account_type       = excluded.account_type,
    active             = excluded.active,
    updated_at         = excluded.updated_at`)
	_, err := r.s.db.ExecContext(ctx, q,
		i.ID,
		i.AccountLogin,
		i.AccountType,
		i.OpsEndpoint,
		i.OpsWebhookSecret,
		boolToInt(r.s.dialect, i.Active),
		now,
		now,
	)
	return r.s.scanErr(err)
}

func (r *githubAppRepo) Get(ctx context.Context, id int64) (*githubapp.Installation, error) {
	q := r.s.rebind(`
SELECT id, account_login, account_type, ops_endpoint, ops_webhook_secret, active, created_at, updated_at
FROM github_app_installations WHERE id = ?`)
	row := r.s.db.QueryRowContext(ctx, q, id)
	return scanInstallation(r.s, row)
}

func (r *githubAppRepo) GetByLogin(ctx context.Context, login string) (*githubapp.Installation, error) {
	q := r.s.rebind(`
SELECT id, account_login, account_type, ops_endpoint, ops_webhook_secret, active, created_at, updated_at
FROM github_app_installations WHERE account_login = ? AND active = ` + r.s.dialect.BoolExpr(true) + `
ORDER BY updated_at DESC LIMIT 1`)
	row := r.s.db.QueryRowContext(ctx, q, login)
	return scanInstallation(r.s, row)
}

func (r *githubAppRepo) List(ctx context.Context) ([]githubapp.Installation, error) {
	q := `SELECT id, account_login, account_type, ops_endpoint, ops_webhook_secret, active, created_at, updated_at
FROM github_app_installations ORDER BY updated_at DESC`
	rows, err := r.s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []githubapp.Installation
	for rows.Next() {
		i, err := scanInstallationRow(r.s, rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *i)
	}
	return out, rows.Err()
}

func (r *githubAppRepo) SetActive(ctx context.Context, id int64, active bool) error {
	q := r.s.rebind(`UPDATE github_app_installations SET active = ?, updated_at = ? WHERE id = ?`)
	res, err := r.s.db.ExecContext(ctx, q, boolToInt(r.s.dialect, active), time.Now().UTC(), id)
	if err != nil {
		return r.s.scanErr(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return datastore.ErrNotFound
	}
	return nil
}

func (r *githubAppRepo) SetEndpoint(ctx context.Context, id int64, endpoint, webhookSecret string) error {
	q := r.s.rebind(`UPDATE github_app_installations SET ops_endpoint = ?, ops_webhook_secret = ?, updated_at = ? WHERE id = ?`)
	res, err := r.s.db.ExecContext(ctx, q, endpoint, webhookSecret, time.Now().UTC(), id)
	if err != nil {
		return r.s.scanErr(err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return datastore.ErrNotFound
	}
	return nil
}

func (r *githubAppRepo) Delete(ctx context.Context, id int64) error {
	q := r.s.rebind(`DELETE FROM github_app_installations WHERE id = ?`)
	_, err := r.s.db.ExecContext(ctx, q, id)
	return r.s.scanErr(err)
}

// ─── connect token repo ──────────────────────────────────────────────────────

type connectTokenRepo struct{ s *Store }

func (r *connectTokenRepo) Upsert(ctx context.Context, t *githubapp.ConnectToken) error {
	now := time.Now().UTC()
	q := r.s.rebind(`
INSERT INTO github_app_connect_tokens (installation_id, token, created_at, expires_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(installation_id) DO UPDATE SET
    token      = excluded.token,
    created_at = excluded.created_at,
    expires_at = excluded.expires_at`)
	_, err := r.s.db.ExecContext(ctx, q,
		t.InstallationID, t.Token, now, t.ExpiresAt.UTC())
	return r.s.scanErr(err)
}

func (r *connectTokenRepo) Get(ctx context.Context, installationID int64) (*githubapp.ConnectToken, error) {
	q := r.s.rebind(`SELECT installation_id, token, created_at, expires_at FROM github_app_connect_tokens WHERE installation_id = ?`)
	row := r.s.db.QueryRowContext(ctx, q, installationID)
	return scanConnectToken(r.s, row)
}

func (r *connectTokenRepo) GetByToken(ctx context.Context, token string) (*githubapp.ConnectToken, error) {
	q := r.s.rebind(`SELECT installation_id, token, created_at, expires_at FROM github_app_connect_tokens WHERE token = ?`)
	row := r.s.db.QueryRowContext(ctx, q, token)
	return scanConnectToken(r.s, row)
}

func (r *connectTokenRepo) Delete(ctx context.Context, installationID int64) error {
	q := r.s.rebind(`DELETE FROM github_app_connect_tokens WHERE installation_id = ?`)
	_, err := r.s.db.ExecContext(ctx, q, installationID)
	return r.s.scanErr(err)
}

func scanConnectToken(s *Store, row *sql.Row) (*githubapp.ConnectToken, error) {
	var t githubapp.ConnectToken
	var createdAt, expiresAt string
	if err := row.Scan(&t.InstallationID, &t.Token, &createdAt, &expiresAt); err != nil {
		return nil, s.scanErr(err)
	}
	t.CreatedAt, _ = parseTime(createdAt)
	t.ExpiresAt, _ = parseTime(expiresAt)
	return &t, nil
}

// ─── scan helpers ────────────────────────────────────────────────────────────

func scanInstallation(s *Store, row *sql.Row) (*githubapp.Installation, error) {
	var i githubapp.Installation
	var activeVal interface{}
	var createdAt, updatedAt string
	err := row.Scan(
		&i.ID, &i.AccountLogin, &i.AccountType,
		&i.OpsEndpoint, &i.OpsWebhookSecret,
		&activeVal, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, s.scanErr(err)
	}
	i.Active = intToBool(activeVal)
	i.CreatedAt, _ = parseTime(createdAt)
	i.UpdatedAt, _ = parseTime(updatedAt)
	return &i, nil
}

func scanInstallationRow(s *Store, rows *sql.Rows) (*githubapp.Installation, error) {
	var i githubapp.Installation
	var activeVal interface{}
	var createdAt, updatedAt string
	err := rows.Scan(
		&i.ID, &i.AccountLogin, &i.AccountType,
		&i.OpsEndpoint, &i.OpsWebhookSecret,
		&activeVal, &createdAt, &updatedAt,
	)
	if err != nil {
		return nil, err
	}
	i.Active = intToBool(activeVal)
	i.CreatedAt, _ = parseTime(createdAt)
	i.UpdatedAt, _ = parseTime(updatedAt)
	return &i, nil
}

func boolToInt(d Dialect, b bool) interface{} {
	if d.Name() == "postgres" {
		return b
	}
	if b {
		return 1
	}
	return 0
}

func intToBool(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case int64:
		return val != 0
	case int:
		return val != 0
	case []byte:
		return len(val) > 0 && val[0] != '0'
	}
	return false
}

func parseTime(s string) (time.Time, error) {
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05Z",
	} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("cannot parse time: " + s)
}
