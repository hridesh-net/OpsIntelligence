package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type userRepo struct{ s *Store }

// userColumns is the full projection; keep the order in sync with
// scanUser so every query stays self-consistent.
const userColumns = `id, username, email, display_name, password_hash, totp_secret,
    status, oidc_issuer, oidc_subject, created_at, updated_at, last_login_at`

func (r *userRepo) Create(ctx context.Context, u *datastore.User) error {
	now := time.Now().UTC()
	if u.CreatedAt.IsZero() {
		u.CreatedAt = now
	}
	u.UpdatedAt = now
	if u.Status == "" {
		u.Status = datastore.UserActive
	}
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`INSERT INTO users(
            id, username, email, display_name, password_hash, totp_secret,
            status, oidc_issuer, oidc_subject, created_at, updated_at, last_login_at
        ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`),
		u.ID, u.Username, nullable(u.Email), nullable(u.DisplayName),
		nullable(u.PasswordHash), nullable(u.TOTPSecret),
		string(u.Status), nullable(u.OIDCIssuer), nullable(u.OIDCSubject),
		u.CreatedAt, u.UpdatedAt, nullableTime(u.LastLoginAt),
	)
	return r.s.scanErr(err)
}

func (r *userRepo) Get(ctx context.Context, id string) (*datastore.User, error) {
	row := r.s.db.QueryRowContext(ctx,
		r.s.rebind(`SELECT `+userColumns+` FROM users WHERE id = ?`), id)
	return scanUser(row.Scan, r.s.scanErr)
}

func (r *userRepo) GetByUsername(ctx context.Context, username string) (*datastore.User, error) {
	row := r.s.db.QueryRowContext(ctx,
		r.s.rebind(`SELECT `+userColumns+` FROM users WHERE username = ?`), username)
	return scanUser(row.Scan, r.s.scanErr)
}

func (r *userRepo) GetByOIDCSubject(ctx context.Context, issuer, subject string) (*datastore.User, error) {
	row := r.s.db.QueryRowContext(ctx,
		r.s.rebind(`SELECT `+userColumns+` FROM users WHERE oidc_issuer = ? AND oidc_subject = ?`),
		issuer, subject)
	return scanUser(row.Scan, r.s.scanErr)
}

func (r *userRepo) List(ctx context.Context, limit, offset int) ([]datastore.User, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.s.db.QueryContext(ctx,
		r.s.rebind(`SELECT `+userColumns+` FROM users ORDER BY created_at DESC LIMIT ? OFFSET ?`),
		limit, offset)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.User
	for rows.Next() {
		u, err := scanUser(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (r *userRepo) Update(ctx context.Context, u *datastore.User) error {
	u.UpdatedAt = time.Now().UTC()
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`UPDATE users SET
            username = ?, email = ?, display_name = ?, status = ?,
            oidc_issuer = ?, oidc_subject = ?, updated_at = ?
            WHERE id = ?`),
		u.Username, nullable(u.Email), nullable(u.DisplayName), string(u.Status),
		nullable(u.OIDCIssuer), nullable(u.OIDCSubject), u.UpdatedAt, u.ID,
	)
	return r.s.scanErr(err)
}

func (r *userRepo) SetPassword(ctx context.Context, id, hash string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`UPDATE users SET password_hash = ?, updated_at = ? WHERE id = ?`),
		hash, time.Now().UTC(), id)
	return r.s.scanErr(err)
}

func (r *userRepo) SetTOTPSecret(ctx context.Context, id, secret string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`UPDATE users SET totp_secret = ?, updated_at = ? WHERE id = ?`),
		nullable(secret), time.Now().UTC(), id)
	return r.s.scanErr(err)
}

func (r *userRepo) SetStatus(ctx context.Context, id string, status datastore.UserStatus) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`UPDATE users SET status = ?, updated_at = ? WHERE id = ?`),
		string(status), time.Now().UTC(), id)
	return r.s.scanErr(err)
}

func (r *userRepo) RecordLogin(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`),
		now, now, id)
	return r.s.scanErr(err)
}

func (r *userRepo) Delete(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`DELETE FROM users WHERE id = ?`), id)
	return r.s.scanErr(err)
}

func (r *userRepo) Count(ctx context.Context) (int, error) {
	var n int
	row := r.s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`)
	err := row.Scan(&n)
	return n, r.s.scanErr(err)
}

// ─── helpers ────────────────────────────────────────────────────────

// scanFn abstracts over *sql.Row.Scan and *sql.Rows.Scan.
type scanFn func(dest ...any) error

func scanUser(scan scanFn, mapErr func(error) error) (*datastore.User, error) {
	var (
		u          datastore.User
		email      sql.NullString
		display    sql.NullString
		password   sql.NullString
		totp       sql.NullString
		status     string
		issuer     sql.NullString
		subject    sql.NullString
		lastLogin  sql.NullTime
	)
	if err := scan(
		&u.ID, &u.Username, &email, &display, &password, &totp,
		&status, &issuer, &subject, &u.CreatedAt, &u.UpdatedAt, &lastLogin,
	); err != nil {
		return nil, mapErr(err)
	}
	u.Email = email.String
	u.DisplayName = display.String
	u.PasswordHash = password.String
	u.TOTPSecret = totp.String
	u.Status = datastore.UserStatus(status)
	u.OIDCIssuer = issuer.String
	u.OIDCSubject = subject.String
	if lastLogin.Valid {
		t := lastLogin.Time
		u.LastLoginAt = &t
	}
	return &u, nil
}

// nullable converts "" into NULL so unique-constraint columns
// (oidc_subject, …) don't collide on every inserted row.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableTime mirrors nullable for *time.Time.
func nullableTime(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return *t
}
