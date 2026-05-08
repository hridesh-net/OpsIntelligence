package githubapp

import (
	"context"
	"time"
)

// Installation is one row in the github_app_installations table.
// It represents a single GitHub App installation in an org or user account.
type Installation struct {
	// ID is the numeric installation_id assigned by GitHub.
	ID int64

	// AccountLogin is the org or user slug (e.g. "acme-corp").
	AccountLogin string

	// AccountType is "Organization" or "User".
	AccountType string

	// OpsEndpoint is the on-premise OpsIntelligence base URL to relay events
	// to (e.g. "https://opi.acme.internal"). Empty means process locally.
	OpsEndpoint string

	// OpsWebhookSecret is the webhook secret configured in the org's local
	// OpsIntelligence under webhooks.adapters.github.secret. The relay uses
	// this to re-sign forwarded payloads so the org's instance can verify them.
	OpsWebhookSecret string

	// Active is false when the installation has been suspended or uninstalled.
	Active bool

	CreatedAt time.Time
	UpdatedAt time.Time
}

// InstallationRepo persists GitHub App installations. Implementations are
// in internal/datastore/sqlstore/github_app.go.
type InstallationRepo interface {
	// Upsert creates or updates an installation row. On conflict the
	// AccountLogin and AccountType fields are refreshed; endpoint config
	// is preserved unless explicitly set via SetEndpoint.
	Upsert(ctx context.Context, i *Installation) error

	// Get returns the installation with the given GitHub installation_id.
	// Returns datastore.ErrNotFound when absent.
	Get(ctx context.Context, id int64) (*Installation, error)

	// GetByLogin returns the active installation for the given org/user login.
	// Returns datastore.ErrNotFound when absent.
	GetByLogin(ctx context.Context, login string) (*Installation, error)

	// List returns all installations, most recently updated first.
	List(ctx context.Context) ([]Installation, error)

	// SetActive marks an installation active or inactive (suspend / uninstall).
	SetActive(ctx context.Context, id int64, active bool) error

	// SetEndpoint stores the org's on-premise OpsIntelligence endpoint and
	// the webhook secret used to re-sign relayed payloads.
	SetEndpoint(ctx context.Context, id int64, endpoint, webhookSecret string) error

	// Delete hard-deletes an installation row (used during uninstall cleanup).
	Delete(ctx context.Context, id int64) error
}
