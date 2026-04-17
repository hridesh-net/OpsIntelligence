package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/drivers" // register sqlite+postgres
)

func datastoreCmd(gf *globalFlags) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "datastore",
		Short: "Manage the ops-plane datastore (users, RBAC, audit, tasks)",
		Long: `The ops-plane datastore is strictly separate from agent memory.
It persists users, roles, permissions, API keys, sessions, audit log,
task history and OIDC state. Local installs default to SQLite under
<state_dir>/ops.db; cloud installs point datastore.driver: postgres and
datastore.dsn: postgres://... in opsintelligence.yaml or via the
OPSINTELLIGENCE_DATASTORE_DSN environment variable.`,
	}
	cmd.AddCommand(datastoreMigrateCmd(gf))
	cmd.AddCommand(datastoreStatusCmd(gf))
	cmd.AddCommand(datastorePingCmd(gf))
	cmd.AddCommand(datastoreDownCmd(gf))
	return cmd
}

// openDatastoreFromConfig is the CLI-side helper that takes an already
// loaded *config.Config and returns a live datastore.Store. It is the
// single authoritative place the CLI converts config.DatastoreConfig
// into datastore.Config; the gateway will grow its own copy later.
func openDatastoreFromConfig(ctx context.Context, cfg *config.Config) (datastore.Store, error) {
	dc := cfg.Datastore
	lifetime := time.Duration(0)
	if strings.TrimSpace(dc.ConnMaxLifetime) != "" {
		d, err := time.ParseDuration(dc.ConnMaxLifetime)
		if err != nil {
			return nil, fmt.Errorf("datastore.conn_max_lifetime: %w", err)
		}
		lifetime = d
	}
	return datastore.Open(ctx, datastore.Config{
		Driver:          dc.Driver,
		DSN:             dc.DSN,
		MaxOpenConns:    dc.MaxOpenConns,
		MaxIdleConns:    dc.MaxIdleConns,
		ConnMaxLifetime: lifetime,
		Migrations:      "manual", // the CLI subcommands drive migrations explicitly
	})
}

func datastoreMigrateCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "migrate",
		Short: "Apply all pending schema migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return fmt.Errorf("open datastore: %w", err)
			}
			defer store.Close()

			before, latest, err := store.MigrationStatus(ctx)
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}
			fmt.Printf("driver=%s\n", store.Driver())
			fmt.Printf("applied=%d latest=%d\n", before, latest)
			if before == latest {
				fmt.Println("schema already up to date, nothing to do")
				return nil
			}
			if err := store.Migrate(ctx); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
			after, _, err := store.MigrationStatus(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("migrated to version %d (%+d)\n", after, after-before)
			return nil
		},
	}
}

func datastoreStatusCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show applied and latest schema versions",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return fmt.Errorf("open datastore: %w", err)
			}
			defer store.Close()

			applied, latest, err := store.MigrationStatus(ctx)
			if err != nil {
				return fmt.Errorf("status: %w", err)
			}
			mig, err := datastore.LoadMigrations(store.Driver())
			if err != nil {
				return err
			}
			fmt.Printf("driver       = %s\n", store.Driver())
			fmt.Printf("dsn          = %s\n", redactDSN(cfg.Datastore.DSN))
			fmt.Printf("applied      = %d\n", applied)
			fmt.Printf("latest       = %d\n", latest)
			fmt.Printf("bundled      = %d migrations\n", len(mig))
			if applied < latest {
				fmt.Printf("status       = pending — run `opsintelligence datastore migrate`\n")
			} else {
				fmt.Printf("status       = up-to-date\n")
			}
			return nil
		},
	}
}

func datastorePingCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "ping",
		Short: "Verify the datastore connection is healthy",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := buildLogger(gf.logLevel)
			cfg, err := loadConfig(gf.configPath, log)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			store, err := openDatastoreFromConfig(ctx, cfg)
			if err != nil {
				return fmt.Errorf("open datastore: %w", err)
			}
			defer store.Close()
			if err := store.Ping(ctx); err != nil {
				return fmt.Errorf("ping: %w", err)
			}
			fmt.Printf("ok  driver=%s\n", store.Driver())
			return nil
		},
	}
}

// datastoreDownCmd is intentionally a stub. Down-migrations have
// historically been the source of quiet data loss; we prefer "forward
// only + manual SQL" for destructive schema changes. The command stays
// registered so operators discover the guidance without hunting.
func datastoreDownCmd(gf *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "(not implemented) roll a single migration back",
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf(`datastore down-migrations are not supported.

If you truly need a destructive schema change:
  1. Snapshot the database (sqlite3 .backup / pg_dump)
  2. Apply the reverse SQL manually
  3. DELETE FROM schema_migrations WHERE version = <N>

Consider writing a forward-fix migration instead.`)
		},
	}
}

// redactDSN strips passwords from Postgres DSNs for safe logging. It
// is a best-effort helper, not a security boundary.
func redactDSN(dsn string) string {
	if dsn == "" {
		return "(default)"
	}
	if !strings.HasPrefix(dsn, "postgres://") && !strings.HasPrefix(dsn, "postgresql://") {
		return dsn
	}
	at := strings.Index(dsn, "@")
	if at < 0 {
		return dsn
	}
	scheme := strings.Index(dsn, "://")
	if scheme < 0 {
		return dsn
	}
	userinfo := dsn[scheme+3 : at]
	if colon := strings.Index(userinfo, ":"); colon >= 0 {
		userinfo = userinfo[:colon+1] + "***"
	}
	return dsn[:scheme+3] + userinfo + dsn[at:]
}
