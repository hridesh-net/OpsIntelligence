// Package sqlite registers the SQLite driver with the datastore layer.
// Import for side effects:
//
//	import _ "github.com/opsintelligence/opsintelligence/internal/datastore/driver/sqlite"
//
// Side effects: registers datastore.DriverSQLite via datastore.Register.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3" // database/sql driver registration
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/datastore/sqlstore"
)

func init() {
	datastore.Register(datastore.DriverSQLite, open)
}

// Dialect is the SQLite flavour of sqlstore.Dialect. Placeholders stay
// as `?` and booleans are expressed as 0/1 (which the go driver also
// accepts as native bool).
type Dialect struct{}

func (Dialect) Name() string         { return datastore.DriverSQLite }
func (Dialect) Rebind(q string) string { return q }
func (Dialect) BoolExpr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func open(ctx context.Context, cfg datastore.Config) (datastore.Store, error) {
	dsn, err := ensureDSN(cfg.DSN)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("datastore/sqlite: open: %w", err)
	}
	// SQLite is file-bound; parallel writers serialise anyway. Keep the
	// pool small to avoid locking retries.
	db.SetMaxOpenConns(firstNonZero(cfg.MaxOpenConns, 4))
	db.SetMaxIdleConns(firstNonZero(cfg.MaxIdleConns, 2))
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("datastore/sqlite: ping: %w", err)
	}
	// Foreign keys are off by default in every new connection; turn
	// them on now so ON DELETE CASCADE actually fires.
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("datastore/sqlite: enable foreign_keys: %w", err)
	}
	return sqlstore.New(db, Dialect{}), nil
}

// ensureDSN normalises DSNs of the form
//
//	/path/to/db.sqlite
//	file:/path/to/db.sqlite?_foreign_keys=on
//	:memory:
//
// creates the parent directory when the path is on disk, and force-
// appends `_loc=UTC` so datetime round-trips are timezone-safe across
// hosts (without this, mattn/go-sqlite3 serialises time.Now() as local
// time and WHERE expires_at < $now-UTC misfires in non-UTC zones).
func ensureDSN(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("%w: empty DSN", datastore.ErrInvalidConfig)
	}
	if raw == ":memory:" {
		return withUTCLoc(raw), nil
	}

	path := raw
	if strings.HasPrefix(raw, "file:") {
		trimmed := strings.TrimPrefix(raw, "file:")
		if idx := strings.Index(trimmed, "?"); idx >= 0 {
			path = trimmed[:idx]
		} else {
			path = trimmed
		}
	}
	if path == ":memory:" {
		return withUTCLoc(raw), nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("datastore/sqlite: abs path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return "", fmt.Errorf("datastore/sqlite: mkdir: %w", err)
	}
	return withUTCLoc(raw), nil
}

// withUTCLoc appends _loc=UTC to the DSN's query string unless the
// operator already set _loc explicitly.
func withUTCLoc(dsn string) string {
	if strings.Contains(dsn, "_loc=") {
		return dsn
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	return dsn + sep + "_loc=UTC"
}

func firstNonZero(a, fallback int) int {
	if a > 0 {
		return a
	}
	return fallback
}
