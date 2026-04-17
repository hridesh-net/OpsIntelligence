// Package postgres registers the Postgres driver with the datastore
// layer, backed by github.com/lib/pq. Import for side effects:
//
//	import _ "github.com/opsintelligence/opsintelligence/internal/datastore/driver/postgres"
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	_ "github.com/lib/pq" // database/sql driver registration
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/datastore/sqlstore"
)

func init() {
	datastore.Register(datastore.DriverPostgres, open)
}

// Dialect is the Postgres flavour of sqlstore.Dialect. `?` placeholders
// written in sqlstore SQL are rewritten to `$N` at query time.
type Dialect struct{}

func (Dialect) Name() string { return datastore.DriverPostgres }

// Rebind replaces `?` with `$1, $2, …`. Single-quoted strings are
// preserved so literal `?` characters inside string literals survive
// (unlikely in our DDL but correct behaviour regardless).
func (Dialect) Rebind(q string) string {
	var (
		b        strings.Builder
		n        int
		inSingle bool
	)
	for i := 0; i < len(q); i++ {
		c := q[i]
		if c == '\'' {
			inSingle = !inSingle
		}
		if c == '?' && !inSingle {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func (Dialect) BoolExpr(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

func open(ctx context.Context, cfg datastore.Config) (datastore.Store, error) {
	dsn := strings.TrimSpace(cfg.DSN)
	if dsn == "" {
		return nil, fmt.Errorf("%w: empty DSN", datastore.ErrInvalidConfig)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("datastore/postgres: open: %w", err)
	}
	if cfg.MaxOpenConns > 0 {
		db.SetMaxOpenConns(cfg.MaxOpenConns)
	}
	if cfg.MaxIdleConns > 0 {
		db.SetMaxIdleConns(cfg.MaxIdleConns)
	}
	if cfg.ConnMaxLifetime > 0 {
		db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("datastore/postgres: ping: %w", err)
	}
	return sqlstore.New(db, Dialect{}), nil
}
