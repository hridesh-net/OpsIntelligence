package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/drivers" // register sqlite+postgres
	"github.com/opsintelligence/opsintelligence/internal/gateway"
)

// attachAuthToGateway opens the ops-plane datastore, auto-applies
// migrations, seeds the built-in RBAC roles, and mounts the resulting
// AuthService on srv.
//
// Returns a closer the caller MUST invoke on shutdown so the connection
// pool is released cleanly. When the datastore is disabled by config
// (driver == "none") attachAuthToGateway is a no-op and returns a nil
// closer, leaving the gateway in its pre-2c shared-Bearer-token mode.
//
// Errors are fatal for gateway boot — if auth was requested, failing
// to wire it loudly is safer than silently serving without RBAC.
func attachAuthToGateway(ctx context.Context, cfg *config.Config, log *zap.Logger, srv *gateway.Server) (func() error, error) {
	if cfg == nil || srv == nil {
		return nil, nil
	}
	driver := strings.ToLower(strings.TrimSpace(cfg.Datastore.Driver))
	if driver == "" || driver == "none" || driver == "disabled" {
		log.Info("gateway auth disabled (datastore.driver=none); continuing with legacy Bearer token")
		return nil, nil
	}

	store, err := openDatastoreForGateway(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("gateway auth: open datastore: %w", err)
	}

	// Apply any pending migrations up-front so the very first
	// /api/v1/auth/status request does not trip over a missing users
	// table. Safe to call even if the datastore is already current.
	if err := store.Migrate(ctx); err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("gateway auth: migrate datastore: %w", err)
	}

	svc, err := gateway.BuildAuthService(ctx, cfg, store, log)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("gateway auth: build auth service: %w", err)
	}
	srv.AuthService = svc

	log.Info("gateway auth wired",
		zap.String("datastore_driver", store.Driver()),
		zap.Bool("local_enabled", svc.LocalEnabled),
		zap.Bool("api_keys_enabled", svc.APIKeysEnabled),
		zap.Bool("oidc_enabled", svc.OIDCEnabled),
		zap.Bool("csrf_enabled", svc.CSRFEnabled),
		zap.Bool("legacy_token_present", svc.LegacyTokenConfigured),
	)

	return store.Close, nil
}

// openDatastoreForGateway mirrors openDatastoreFromConfig but defaults
// Migrations to "auto" since the gateway wants to come up on a fresh
// machine without a prior `opsintelligence datastore migrate` invocation.
func openDatastoreForGateway(ctx context.Context, cfg *config.Config) (datastore.Store, error) {
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
		Migrations:      "auto",
	})
}
