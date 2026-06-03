package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/agent"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/drivers" // register sqlite+postgres
	"github.com/opsintelligence/opsintelligence/internal/devops/github"
	"github.com/opsintelligence/opsintelligence/internal/gateway"
	"github.com/opsintelligence/opsintelligence/internal/githubapp"
	"github.com/opsintelligence/opsintelligence/internal/kanban"
	"github.com/opsintelligence/opsintelligence/internal/kanban/cost"
	"github.com/opsintelligence/opsintelligence/internal/kanban/dispatcher"
	"github.com/opsintelligence/opsintelligence/internal/kanban/events"
	"github.com/opsintelligence/opsintelligence/internal/kanban/githubmode"
	"github.com/opsintelligence/opsintelligence/internal/kanban/preview"
	"github.com/opsintelligence/opsintelligence/internal/kanban/sentry"
	"github.com/opsintelligence/opsintelligence/internal/kanban/worktree"
	"github.com/opsintelligence/opsintelligence/internal/memory"
	"github.com/opsintelligence/opsintelligence/internal/provider"
	"github.com/opsintelligence/opsintelligence/internal/redis"
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
func attachAuthToGateway(ctx context.Context, cfg *config.Config, configPath string, log *zap.Logger, srv *gateway.Server, redisCache *redis.Cache) (func() error, error) {
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

	// Optionally wrap sessions with Redis cache for fast lookups.
	var sessionRepo datastore.SessionRepo
	if redisCache != nil && redisCache.Enabled() {
		ttl := 5 * time.Minute
		if d, err := time.ParseDuration(cfg.Redis.CacheTTL); err == nil && d > 0 {
			ttl = d
		}
		sessionRepo = redis.NewSessionCache(store.Sessions(), redisCache, ttl, log)
		log.Info("redis session cache enabled", zap.Duration("ttl", ttl))
	}

	svc, err := gateway.BuildAuthService(ctx, cfg, store, sessionRepo, log)
	if err != nil {
		_ = store.Close()
		return nil, fmt.Errorf("gateway auth: build auth service: %w", err)
	}
	svc.ConfigPath = configPath
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

// attachGitHubAppToGateway wires the multi-tenant GitHub App handler onto the
// gateway server. It loads the App's private key, opens the installation store
// (reusing the datastore already opened for auth when available), and mounts
// the handler. Safe to call only when cfg.GitHubApp.Enabled is true.
func attachGitHubAppToGateway(cfg *config.Config, srv *gateway.Server, runner *agent.Runner, log *zap.Logger) error {
	appCfg := githubapp.Config{
		Enabled:        cfg.GitHubApp.Enabled,
		AppID:          cfg.GitHubApp.AppID,
		PrivateKeyPath: cfg.GitHubApp.PrivateKeyPath,
		PrivateKeyPEM:  cfg.GitHubApp.PrivateKeyPEM,
		WebhookSecret:  cfg.GitHubApp.WebhookSecret,
		PublicURL:      cfg.GitHubApp.PublicURL,
		GitHubAPIURL:   cfg.GitHubApp.GitHubAPIURL,
	}

	// Open a dedicated datastore for the GitHub App installations table.
	// This is kept separate from the auth store closer so the auth path
	// is not affected if the GitHub App store fails.
	store, err := openDatastoreForGateway(context.Background(), cfg)
	if err != nil {
		return fmt.Errorf("github-app: open datastore: %w", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		_ = store.Close()
		return fmt.Errorf("github-app: migrate datastore: %w", err)
	}

	// Build the AppClient (JWT signer + installation token cache) when a
	// private key is configured. Used by the setup page to verify installations.
	var appClient *githubapp.AppClient
	if cfg.GitHubApp.AppID > 0 && (cfg.GitHubApp.PrivateKeyPath != "" || cfg.GitHubApp.PrivateKeyPEM != "") {
		key, err := githubapp.LoadPrivateKey(cfg.GitHubApp.PrivateKeyPath, cfg.GitHubApp.PrivateKeyPEM)
		if err != nil {
			_ = store.Close()
			return fmt.Errorf("github-app: load private key: %w", err)
		}
		appClient = githubapp.NewAppClient(cfg.GitHubApp.AppID, key, cfg.GitHubApp.GitHubAPIURL)
	}

	// localRunner bridges the GitHub App handler to the existing agent runner.
	// Called for installations without a configured on-premise endpoint.
	var localRunner func(context.Context, string, string, []byte) error
	if runner != nil {
		localRunner = func(ctx context.Context, event, deliveryID string, payload []byte) error {
			prompt := fmt.Sprintf(
				"GitHub App event: %s (delivery %s)\nProcess this event appropriately for DevOps automation.",
				event, deliveryID,
			)
			msg := memory.Message{
				Role:    memory.RoleUser,
				Content: prompt,
			}
			_, err := runner.Run(ctx, msg)
			return err
		}
	}

	h := githubapp.New(appCfg,
		store.GitHubAppInstallations(),
		store.GitHubAppConnectTokens(),
		appClient,
		localRunner,
		log,
	)
	srv.GitHubApp = h

	log.Info("github-app: relay handler initialized",
		zap.Int64("app_id", cfg.GitHubApp.AppID),
		zap.String("public_url", cfg.GitHubApp.PublicURL),
	)
	return nil
}

// StartGitHubAppConnector starts the outbound WebSocket connector on this
// instance so it receives events pushed from a remote relay. Call this when
// cfg.GitHubAppConnector.Enabled is true. The connector runs until ctx is
// cancelled (typically on process shutdown).
func StartGitHubAppConnector(ctx context.Context, cfg *config.Config, runner *agent.Runner, log *zap.Logger) {
	cc := cfg.GitHubAppConnector
	connCfg := githubapp.ConnectorConfig{
		Enabled:           cc.Enabled,
		RelayURL:          cc.RelayURL,
		InstallationID:    cc.InstallationID,
		ConnectToken:      cc.ConnectToken,
		ReconnectInterval: cc.ReconnectInterval,
	}

	handler := func(ctx context.Context, env *githubapp.EventEnvelope) error {
		if runner == nil {
			return nil
		}
		prompt := fmt.Sprintf(
			"GitHub App event: %s (delivery %s, repo %s)\nProcess this event appropriately.",
			env.Event, env.DeliveryID, env.Repository,
		)
		msg := memory.Message{Role: memory.RoleUser, Content: prompt}
		_, err := runner.Run(ctx, msg)
		return err
	}

	conn := githubapp.NewConnector(connCfg, handler, log)
	log.Info("github-app connector: starting outbound WebSocket",
		zap.String("relay", cc.RelayURL),
		zap.Int64("installation_id", cc.InstallationID),
	)
	go conn.Run(ctx)
}

// attachKanbanToGateway wires the kanban dispatch service when the datastore
// and provider registry are available.
func attachKanbanToGateway(cfg *config.Config, reg *provider.Registry, srv *gateway.Server, log *zap.Logger) {
	if srv == nil || srv.AuthService == nil || srv.AuthService.Store == nil {
		return
	}
	store := srv.AuthService.Store

	wtBase := filepath.Join(cfg.StateDir, "workspace", "kanban")
	if err := os.MkdirAll(wtBase, 0o755); err != nil {
		log.Warn("kanban: failed to create worktree base", zap.Error(err))
	}

	wtMgr := &worktree.Manager{BaseDir: wtBase}

	drivers := make(map[string]dispatcher.AgentDriver)

	// Register the Go driver if the gateway has a Runner.
	if srv.Runner != nil {
		drivers["go"] = dispatcher.NewGoDriver(srv.Runner)
		log.Info("kanban: registered Go driver")
	}

	// Register CLI adapters (best-effort; each one works if the binary is
	// on PATH and authenticated. Operators see "no driver for type X" when
	// they dispatch to an agent_type whose binary isn't installed; we
	// don't probe at startup to keep cold-start cheap.)
	drivers["claude-code"] = dispatcher.NewClaudeCodeDriver() // stream-json
	drivers["codex"] = dispatcher.NewCodexDriver()
	drivers["gemini"] = dispatcher.NewGeminiDriver()
	drivers["cursor-agent"] = dispatcher.NewCursorDriver()
	drivers["gh-copilot"] = dispatcher.NewCopilotDriver()
	drivers["opencode"] = dispatcher.NewOpenCodeDriver()
	drivers["amp"] = dispatcher.NewAmpDriver()
	drivers["qwen"] = dispatcher.NewQwenDriver()
	drivers["droid"] = dispatcher.NewDroidDriver()
	drivers["ccr"] = dispatcher.NewCCRDriver()
	// Generic ACP driver — board agents with agent_type "acp" let the
	// operator point at any ACP-compliant CLI by setting `binary` in the
	// agent's config_json (wired up in dispatchAgentResolver, separate PR).
	drivers["acp"] = dispatcher.NewACPDriver("acp-agent")

	driverNames := make([]string, 0, len(drivers))
	for n := range drivers {
		driverNames = append(driverNames, n)
	}
	log.Info("kanban: registered CLI drivers", zap.Strings("drivers", driverNames))

	calc := cost.NewCalculator()
	bus := events.NewBus()
	svc := kanban.NewDispatchService(store, wtMgr, drivers, calc)
	svc.Events = bus
	srv.AuthService.Kanban = svc
	srv.AuthService.KanbanEvents = bus
	// Card attachments live next to the worktrees so they share the same
	// state-dir-scoped lifecycle (cleaned up when the board is deleted /
	// state-dir purged).
	srv.AuthService.AttachmentRoot = filepath.Join(cfg.StateDir, "workspace", "kanban", "attachments")

	// Autopilot — feature-dev / qa loop runner. Bound to the same dispatch
	// service so child runs flow through the standard pipeline.
	srv.AuthService.KanbanAutopilot = kanban.NewAutopilot(svc)

	// GitHub workspace mode — bridges Board.Mode="github" cards to
	// real GH issues. Wired only when a GitHub token is available;
	// boards without mode=github simply never call into this.
	ghToken := strings.TrimSpace(cfg.DevOps.GitHub.Token)
	if ghToken == "" {
		ghToken = os.Getenv("OPSINTELLIGENCE_GITHUB_TOKEN")
	}
	if ghToken != "" {
		ghBase := cfg.DevOps.GitHub.BaseURL
		if ghBase == "" {
			ghBase = "https://api.github.com"
		}
		ghClient := github.New(github.Config{Token: ghToken, BaseURL: ghBase}, nil)
		srv.AuthService.KanbanGitHub = githubmode.New(store, ghClient)
		log.Info("kanban: github workspace mode wired", zap.String("base_url", ghBase))
	} else {
		log.Info("kanban: github workspace mode not configured (no token); skipping")
	}

	// Sentry importer — pulls Sentry issues into board cards on demand.
	sentryToken := strings.TrimSpace(cfg.DevOps.Sentry.Token)
	if sentryToken == "" {
		sentryToken = os.Getenv("OPSINTELLIGENCE_SENTRY_TOKEN")
	}
	if sentryToken != "" {
		sentryBase := cfg.DevOps.Sentry.BaseURL
		sentryClient := sentry.NewClient(sentryBase, sentryToken)
		srv.AuthService.KanbanSentry = sentry.New(store, sentryClient)
		log.Info("kanban: sentry importer wired")
	}

	// Branch preview manager — uses Tailscale Funnel for a public URL
	// when the daemon is configured for it (gateway.tailscale.mode=funnel).
	funnelEnabled := strings.EqualFold(strings.TrimSpace(cfg.Gateway.Tailscale.Mode), "funnel")
	srv.AuthService.KanbanPreview = preview.New(funnelEnabled)
	log.Info("kanban: branch preview manager wired", zap.Bool("funnel_enabled", funnelEnabled))

	log.Info("kanban dispatch service wired",
		zap.String("worktree_base", wtBase),
		zap.Int("drivers", len(drivers)),
	)
}
