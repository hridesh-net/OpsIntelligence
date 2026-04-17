package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

type oidcStateRepo struct{ s *Store }

func (r *oidcStateRepo) Put(ctx context.Context, st *datastore.OIDCState) error {
	if st.CreatedAt.IsZero() {
		st.CreatedAt = time.Now().UTC()
	}
	_, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`INSERT INTO oidc_state(state, nonce, pkce_verifier, redirect_after, created_at, expires_at)
            VALUES (?,?,?,?,?,?)`),
		st.State, st.Nonce, nullable(st.PKCEVerifier), nullable(st.RedirectAfter),
		st.CreatedAt, st.ExpiresAt,
	)
	return r.s.scanErr(err)
}

// Take reads-and-deletes a state row. If the row is expired, the
// caller still sees ErrExpired (not ErrNotFound) so they can log the
// attempt distinctly from a missing state.
func (r *oidcStateRepo) Take(ctx context.Context, state string) (*datastore.OIDCState, error) {
	tx, err := r.s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, r.s.scanErr(err)
	}
	defer tx.Rollback() //nolint:errcheck

	row := tx.QueryRowContext(ctx,
		r.s.rebind(`SELECT state, nonce, pkce_verifier, redirect_after, created_at, expires_at
            FROM oidc_state WHERE state = ?`),
		state)
	var (
		st       datastore.OIDCState
		verifier sql.NullString
		redir    sql.NullString
	)
	if err := row.Scan(&st.State, &st.Nonce, &verifier, &redir, &st.CreatedAt, &st.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, datastore.ErrNotFound
		}
		return nil, r.s.scanErr(err)
	}
	st.PKCEVerifier = verifier.String
	st.RedirectAfter = redir.String

	if _, err := tx.ExecContext(ctx,
		r.s.rebind(`DELETE FROM oidc_state WHERE state = ?`), state); err != nil {
		return nil, r.s.scanErr(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, r.s.scanErr(err)
	}
	if time.Now().UTC().After(st.ExpiresAt) {
		return &st, datastore.ErrExpired
	}
	return &st, nil
}

func (r *oidcStateRepo) DeleteExpired(ctx context.Context) (int, error) {
	res, err := r.s.db.ExecContext(ctx,
		r.s.rebind(`DELETE FROM oidc_state WHERE expires_at < ?`), time.Now().UTC())
	if err != nil {
		return 0, r.s.scanErr(err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
