package githubapp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"
)

// ConnectToken is a one-time credential that the org's OpsIntelligence uses
// to authenticate its outbound WebSocket connection to the relay hub.
// It is generated on the setup page and stored in the DB; once the
// WebSocket session is established the token remains valid for the
// lifetime of that session (it can be rotated by visiting setup again).
type ConnectToken struct {
	// Token is the secret string the client presents in the ws URL.
	Token string

	// InstallationID links the token to one GitHub App installation.
	InstallationID int64

	// CreatedAt is when the token was issued.
	CreatedAt time.Time

	// ExpiresAt — unused in the current implementation (tokens do not expire
	// on their own; they are replaced when the setup page is re-submitted).
	ExpiresAt time.Time
}

// ConnectTokenRepo persists per-installation connect tokens.
// Implementations are in internal/datastore/sqlstore/github_app.go.
type ConnectTokenRepo interface {
	// Upsert creates or replaces the connect token for an installation.
	Upsert(ctx context.Context, t *ConnectToken) error

	// Get returns the token for the given installation_id.
	Get(ctx context.Context, installationID int64) (*ConnectToken, error)

	// GetByToken looks up an installation_id by its token string.
	GetByToken(ctx context.Context, token string) (*ConnectToken, error)

	// Delete removes the token for an installation.
	Delete(ctx context.Context, installationID int64) error
}

// GenerateToken returns a cryptographically random 32-byte hex string.
func GenerateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
