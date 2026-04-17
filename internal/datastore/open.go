package datastore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"
)

// DriverSQLite is the identifier for the bundled embedded SQLite driver.
const DriverSQLite = "sqlite"

// DriverPostgres is the identifier for the lib/pq Postgres driver.
const DriverPostgres = "postgres"

// Config is the driver-agnostic configuration for Open. It intentionally
// mirrors internal/config.DatastoreConfig so the two can be wired with
// a tiny adapter without a circular import.
type Config struct {
	// Driver selects the backend: DriverSQLite or DriverPostgres.
	// Empty defaults to DriverSQLite.
	Driver string

	// DSN is driver-specific. For SQLite this is a file path or URI
	// (e.g. "file:/var/lib/opsintelligence/ops.db?_foreign_keys=on");
	// for Postgres it's a libpq URL
	// ("postgres://user:pass@host:5432/dbname?sslmode=require").
	DSN string

	// MaxOpenConns caps the connection pool. 0 -> database/sql default.
	MaxOpenConns int

	// MaxIdleConns caps idle connections. 0 -> database/sql default.
	MaxIdleConns int

	// ConnMaxLifetime, when positive, rotates connections periodically.
	ConnMaxLifetime time.Duration

	// Migrations controls auto-migration on Open:
	//   "auto"   -> run Migrate() before returning (default)
	//   "manual" -> skip; operator runs `opsintelligence datastore migrate`
	Migrations string

	// Logger is optional; the driver may emit INFO-level events here.
	Logger *zap.Logger
}

// OpenFunc is the driver-side factory. Drivers register themselves
// via Register() so internal/datastore can stay import-free of
// driver packages (avoiding a cycle through `database/sql` drivers
// that some callers won't need).
type OpenFunc func(ctx context.Context, cfg Config) (Store, error)

var drivers = map[string]OpenFunc{}

// Register wires a driver identifier to its OpenFunc. Panics if the
// same driver name is registered twice — duplicates are always a bug.
func Register(name string, fn OpenFunc) {
	if name == "" || fn == nil {
		panic("datastore: Register requires a name and non-nil OpenFunc")
	}
	if _, exists := drivers[name]; exists {
		panic("datastore: driver already registered: " + name)
	}
	drivers[name] = fn
}

// Drivers returns the sorted list of registered driver names. Mostly
// useful for `doctor` output.
func Drivers() []string {
	out := make([]string, 0, len(drivers))
	for k := range drivers {
		out = append(out, k)
	}
	return out
}

// Open resolves the driver and returns a live Store. Callers MUST
// defer Store.Close(). When cfg.Migrations is "" or "auto", Migrate
// runs before returning.
func Open(ctx context.Context, cfg Config) (Store, error) {
	driver := strings.TrimSpace(cfg.Driver)
	if driver == "" {
		driver = DriverSQLite
	}
	fn, ok := drivers[driver]
	if !ok {
		return nil, fmt.Errorf("%w: unknown driver %q (registered: %v)", ErrInvalidConfig, driver, Drivers())
	}
	if strings.TrimSpace(cfg.DSN) == "" {
		return nil, fmt.Errorf("%w: empty DSN for driver %q", ErrInvalidConfig, driver)
	}
	cfg.Driver = driver

	store, err := fn(ctx, cfg)
	if err != nil {
		return nil, err
	}

	mode := strings.ToLower(strings.TrimSpace(cfg.Migrations))
	if mode == "" {
		mode = "auto"
	}
	switch mode {
	case "auto":
		if err := store.Migrate(ctx); err != nil {
			_ = store.Close()
			return nil, fmt.Errorf("datastore: auto-migrate failed: %w", err)
		}
	case "manual":
		// Operator runs `opsintelligence datastore migrate` themselves.
	default:
		_ = store.Close()
		return nil, fmt.Errorf("%w: migrations mode must be auto|manual, got %q", ErrInvalidConfig, cfg.Migrations)
	}
	return store, nil
}
