package datastore

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var migrationsFS embed.FS

// migrationFilePattern matches "0001_init.sql" style names. The numeric
// prefix is the monotonically-increasing version.
var migrationFilePattern = regexp.MustCompile(`^(\d+)_[a-z0-9_-]+\.sql$`)

// Migration is a single named up-migration loaded from embed.FS.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// LoadMigrations returns the migrations bundled for the given driver
// ("sqlite" or "postgres") sorted by version.
func LoadMigrations(driver string) ([]Migration, error) {
	dir := "migrations/" + driver
	entries, err := fs.ReadDir(migrationsFS, dir)
	if err != nil {
		return nil, fmt.Errorf("datastore: read migrations %q: %w", dir, err)
	}
	out := make([]Migration, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationFilePattern.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		v, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("datastore: bad migration version %q: %w", e.Name(), err)
		}
		body, err := fs.ReadFile(migrationsFS, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("datastore: read %q: %w", e.Name(), err)
		}
		out = append(out, Migration{
			Version: v,
			Name:    strings.TrimSuffix(e.Name(), ".sql"),
			SQL:     string(body),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	return out, nil
}

// RunMigrations applies every bundled migration whose version is
// greater than the one recorded in schema_migrations. It is the
// helper that driver packages call from Store.Migrate.
//
// The schema_migrations table layout is driver-portable:
//
//	CREATE TABLE schema_migrations (
//	    version     INTEGER PRIMARY KEY,
//	    name        TEXT NOT NULL,
//	    applied_at  TIMESTAMP NOT NULL
//	)
//
// Statements inside each migration file are split on `;` followed by a
// newline (so transactional blocks must use a single statement or the
// driver's multi-statement mode). For now every migration in this
// repo is one logical transaction per file; if that ever stops being
// true, replace the splitter with a proper SQL lexer.
func RunMigrations(ctx context.Context, db *sql.DB, driver string) error {
	if err := ensureMigrationsTable(ctx, db, driver); err != nil {
		return err
	}
	applied, err := appliedVersions(ctx, db)
	if err != nil {
		return err
	}
	mig, err := LoadMigrations(driver)
	if err != nil {
		return err
	}
	for _, m := range mig {
		if applied[m.Version] {
			continue
		}
		if err := applyMigration(ctx, db, driver, m); err != nil {
			return fmt.Errorf("datastore: migration %d (%s) failed: %w", m.Version, m.Name, err)
		}
	}
	return nil
}

// LatestVersion returns the newest embedded version for a driver, or 0
// if there are none. Used by MigrationStatus.
func LatestVersion(driver string) (int, error) {
	mig, err := LoadMigrations(driver)
	if err != nil {
		return 0, err
	}
	if len(mig) == 0 {
		return 0, nil
	}
	return mig[len(mig)-1].Version, nil
}

// AppliedVersion returns the largest version recorded in
// schema_migrations, or 0 if none.
func AppliedVersion(ctx context.Context, db *sql.DB) (int, error) {
	var v sql.NullInt64
	row := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`)
	if err := row.Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

// ─────────────────────────────────────────────────────────────────────
// internals
// ─────────────────────────────────────────────────────────────────────

func ensureMigrationsTable(ctx context.Context, db *sql.DB, driver string) error {
	var stmt string
	switch driver {
	case DriverPostgres:
		stmt = `CREATE TABLE IF NOT EXISTS schema_migrations (
            version     INTEGER     PRIMARY KEY,
            name        TEXT        NOT NULL,
            applied_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
        )`
	default:
		stmt = `CREATE TABLE IF NOT EXISTS schema_migrations (
            version     INTEGER  PRIMARY KEY,
            name        TEXT     NOT NULL,
            applied_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
        )`
	}
	if _, err := db.ExecContext(ctx, stmt); err != nil {
		return fmt.Errorf("datastore: create schema_migrations: %w", err)
	}
	return nil
}

func appliedVersions(ctx context.Context, db *sql.DB) (map[int]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("datastore: read schema_migrations: %w", err)
	}
	defer rows.Close()
	out := make(map[int]bool)
	for rows.Next() {
		var v int
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out[v] = true
	}
	return out, rows.Err()
}

func applyMigration(ctx context.Context, db *sql.DB, driver string, m Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	for _, stmt := range splitStatements(m.SQL) {
		if strings.TrimSpace(stmt) == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("exec: %w\n--- statement ---\n%s", err, stmt)
		}
	}

	switch driver {
	case DriverPostgres:
		_, err = tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, applied_at) VALUES ($1, $2, NOW())`,
			m.Version, m.Name)
	default:
		_, err = tx.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, applied_at) VALUES (?, ?, CURRENT_TIMESTAMP)`,
			m.Version, m.Name)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

// splitStatements is a pragmatic splitter for the simple DDL we ship.
// It splits on ";\n" and preserves ";" inside single-quoted strings.
// Comments ("-- ...") are preserved so any error messages round-trip.
func splitStatements(raw string) []string {
	var (
		out      []string
		b        strings.Builder
		inSingle bool
	)
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '\'' {
			inSingle = !inSingle
		}
		if c == ';' && !inSingle {
			// look-ahead: split only if followed by newline/EOF so
			// single-line "CREATE ...; INSERT ..." blocks inside one
			// migration still work.
			if i+1 < len(raw) && raw[i+1] != '\n' {
				b.WriteByte(c)
				continue
			}
			out = append(out, b.String())
			b.Reset()
			continue
		}
		b.WriteByte(c)
	}
	if rest := strings.TrimSpace(b.String()); rest != "" {
		out = append(out, rest)
	}
	return out
}
