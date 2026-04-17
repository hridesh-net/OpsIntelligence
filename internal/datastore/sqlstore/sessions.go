package sqlstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type sessionRepo struct{ s *Store }

const sessionColumns = `id, user_id, created_at, expires_at, last_seen_at,
    user_agent, remote_addr, revoked_at`

func (r *sessionRepo) Create(ctx context.Context, sess *datastore.Session) error {
	// Normalise all times to UTC so SQLite's ISO-8601 comparison
	// matches regardless of the host's time.Local. Every read goes
	// back through Scan() which returns UTC already.
	sess.CreatedAt = nonZeroUTC(sess.CreatedAt)
	sess.ExpiresAt = sess.ExpiresAt.UTC()
	if sess.LastSeenAt.IsZero() {
		sess.LastSeenAt = sess.CreatedAt
	} else {
		sess.LastSeenAt = sess.LastSeenAt.UTC()
	}
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`INSERT INTO sessions(
            id, user_id, created_at, expires_at, last_seen_at,
            user_agent, remote_addr, revoked_at
        ) VALUES (?,?,?,?,?,?,?,?)`),
		sess.ID, sess.UserID, sess.CreatedAt, sess.ExpiresAt, sess.LastSeenAt,
		nullable(sess.UserAgent), nullable(sess.RemoteAddr), nullableTime(sess.RevokedAt),
	)
	return r.s.scanErr(err)
}

// nonZeroUTC returns t.UTC() or time.Now().UTC() if t is the zero value.
func nonZeroUTC(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now().UTC()
	}
	return t.UTC()
}

func (r *sessionRepo) Get(ctx context.Context, id string) (*datastore.Session, error) {
	row := r.s.db.QueryRowContext(ctx,
		r.s.rebind(`SELECT `+sessionColumns+` FROM sessions WHERE id = ?`), id)
	return scanSession(row.Scan, r.s.scanErr)
}

func (r *sessionRepo) Touch(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`UPDATE sessions SET last_seen_at = ? WHERE id = ? AND revoked_at IS NULL`),
		time.Now().UTC(), id)
	return r.s.scanErr(err)
}

func (r *sessionRepo) Revoke(ctx context.Context, id string) error {
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL`),
		time.Now().UTC(), id)
	return r.s.scanErr(err)
}

func (r *sessionRepo) DeleteExpired(ctx context.Context) (int, error) {
	res, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`DELETE FROM sessions WHERE expires_at < ?`), time.Now().UTC())
	if err != nil {
		return 0, r.s.scanErr(err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *sessionRepo) ListForUser(ctx context.Context, userID string) ([]datastore.Session, error) {
	rows, err := r.s.db.QueryContext(ctx,
		r.s.rebind(`SELECT `+sessionColumns+` FROM sessions WHERE user_id = ? ORDER BY created_at DESC`),
		userID)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer rows.Close()
	var out []datastore.Session
	for rows.Next() {
		sess, err := scanSession(rows.Scan, r.s.scanErr)
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, rows.Err()
}

func scanSession(scan scanFn, mapErr func(error) error) (*datastore.Session, error) {
	var (
		sess    datastore.Session
		ua      sql.NullString
		addr    sql.NullString
		revoked sql.NullTime
	)
	if err := scan(
		&sess.ID, &sess.UserID, &sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt,
		&ua, &addr, &revoked,
	); err != nil {
		return nil, mapErr(err)
	}
	sess.UserAgent = ua.String
	sess.RemoteAddr = addr.String
	if revoked.Valid {
		t := revoked.Time
		sess.RevokedAt = &t
	}
	return &sess, nil
}
