// Package sqlstore is a driver-agnostic implementation of
// datastore.Store backed by database/sql. Thin driver packages
// (internal/datastore/driver/sqlite, .../driver/postgres) open a
// *sql.DB, pick a Dialect, and hand both to New — this file handles
// the rest of the repo contract.
package sqlstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// Dialect abstracts the SQL flavour differences we care about. It is
// intentionally tiny — the goal is portability, not cross-database
// query builders.
type Dialect interface {
	// Name returns the driver identifier ("sqlite" | "postgres").
	// It is the key used to find migration files and to make
	// driver-specific decisions.
	Name() string
	// Rebind rewrites `?` placeholders into the driver's native form.
	// SQLite leaves them as `?`; Postgres rewrites to `$1, $2, …`.
	Rebind(q string) string
	// BoolExpr returns the literal true/false constant for the
	// dialect. SQLite uses INTEGER 0/1; Postgres uses TRUE/FALSE.
	BoolExpr(b bool) string
}

// Store is the driver-agnostic datastore.Store implementation. It is
// safe for concurrent use; each repo method opens its own query on the
// shared *sql.DB.
type Store struct {
	db      *sql.DB
	dialect Dialect
}

// New constructs a Store. The caller owns db and must not close it
// independently — call Store.Close.
func New(db *sql.DB, d Dialect) *Store {
	return &Store{db: db, dialect: d}
}

// DB exposes the underlying *sql.DB for tests / advanced callers.
func (s *Store) DB() *sql.DB { return s.db }

// ─── datastore.Store lifecycle ──────────────────────────────────────

// Driver returns the dialect name.
func (s *Store) Driver() string { return s.dialect.Name() }

// Ping forwards to sql.DB.PingContext.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Close releases the pool.
func (s *Store) Close() error { return s.db.Close() }

// Migrate brings the schema up to latest.
func (s *Store) Migrate(ctx context.Context) error {
	return datastore.RunMigrations(ctx, s.db, s.dialect.Name())
}

// MigrationStatus reports applied and latest versions.
func (s *Store) MigrationStatus(ctx context.Context) (applied, latest int, err error) {
	if _, err := s.db.ExecContext(ctx, schemaMigrationsCreate(s.dialect.Name())); err != nil {
		return 0, 0, fmt.Errorf("ensure schema_migrations: %w", err)
	}
	applied, err = datastore.AppliedVersion(ctx, s.db)
	if err != nil {
		return 0, 0, err
	}
	latest, err = datastore.LatestVersion(s.dialect.Name())
	return applied, latest, err
}

// ─── repo accessors ─────────────────────────────────────────────────

func (s *Store) Users() datastore.UserRepo             { return &userRepo{s: s} }
func (s *Store) Roles() datastore.RoleRepo             { return &roleRepo{s: s} }
func (s *Store) APIKeys() datastore.APIKeyRepo         { return &apiKeyRepo{s: s} }
func (s *Store) Sessions() datastore.SessionRepo       { return &sessionRepo{s: s} }
func (s *Store) Audit() datastore.AuditRepo            { return &auditRepo{s: s} }
func (s *Store) TaskHistory() datastore.TaskHistoryRepo { return &taskHistoryRepo{s: s} }
func (s *Store) OIDCState() datastore.OIDCStateRepo    { return &oidcStateRepo{s: s} }

// ─── shared helpers ─────────────────────────────────────────────────

// rebind rewrites `?` placeholders for the current dialect.
func (s *Store) rebind(q string) string { return s.dialect.Rebind(q) }

// scanErr maps driver-level errors into datastore.Err* sentinels so
// callers can use errors.Is without caring about the driver.
func (s *Store) scanErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return datastore.ErrNotFound
	}
	// lib/pq uniqueness violations expose SQLState 23505; mattn/go-sqlite3
	// surfaces the string "UNIQUE constraint failed". Both collapse to
	// datastore.ErrConflict.
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "duplicate key") ||
		strings.Contains(msg, "23505") {
		return fmt.Errorf("%w: %v", datastore.ErrConflict, err)
	}
	return err
}

// schemaMigrationsCreate is duplicated with migrate.go on purpose —
// MigrationStatus may be called before Migrate() has ever run.
func schemaMigrationsCreate(driver string) string {
	if driver == datastore.DriverPostgres {
		return `CREATE TABLE IF NOT EXISTS schema_migrations (
            version     INTEGER     PRIMARY KEY,
            name        TEXT        NOT NULL,
            applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`
	}
	return `CREATE TABLE IF NOT EXISTS schema_migrations (
            version     INTEGER  PRIMARY KEY,
            name        TEXT     NOT NULL,
            applied_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
        )`
}
