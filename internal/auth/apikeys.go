package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// API key wire format.
//
//	opi_<key_id>_<secret>
//
// - `opi` is a fixed identifier so accidental leaks (log lines, git
//   history) can be grep'd for and invalidated fleet-wide.
// - `<key_id>` is an 8-char base32-ish public identifier persisted in
//   APIKey.KeyID. It is NOT secret; it is what the user sees in the
//   dashboard ("opi_x7k2fe8p_…") and what we index on for revoke +
//   audit logs.
// - `<secret>` is 32 random URL-safe base64 bytes (~43 chars). Only
//   its argon2id hash is persisted; the plaintext is shown exactly
//   once, at creation time, then discarded.
const (
	APIKeyPrefix    = "opi_"
	apiKeyIDLen     = 8
	apiKeySecretLen = 32 // bytes → 43 base64 chars
)

// ErrInvalidAPIKey is returned by ParseAPIKey / VerifyAPIKey when the
// provided token is malformed, unknown, revoked, or expired. Handlers
// treat every variant as 401 to avoid disclosure.
var ErrInvalidAPIKey = errors.New("auth: invalid api key")

// APIKeyPlaintext bundles the new-key values the caller needs for one
// render: the wire-format string (shown to the user once) plus the
// persisted row. Callers MUST NOT log PlainToken.
type APIKeyPlaintext struct {
	PlainToken string            // "opi_<id>_<secret>" — show once, never store
	Record     *datastore.APIKey // KeyID + Hash populated; ID is caller-supplied
}

// GenerateAPIKey mints a fresh key and hashes its secret. The caller
// supplies the owning user, a display name, and optional scopes. The
// returned APIKey has Hash populated; persist it with
// store.APIKeys().Create. Scopes are stored verbatim; the rbac
// Resolver intersects them with the owner's permissions at request
// time.
//
// The generated ID/KeyID/CreatedAt are set here. ExpiresAt is left
// nil — callers set it explicitly when needed.
func GenerateAPIKey(userID, name string, scopes []string) (*APIKeyPlaintext, error) {
	if strings.TrimSpace(userID) == "" {
		return nil, errors.New("auth: GenerateAPIKey requires userID")
	}
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("auth: GenerateAPIKey requires a display name")
	}
	keyID, err := randomKeyID(apiKeyIDLen)
	if err != nil {
		return nil, err
	}
	secret, err := RandomToken(apiKeySecretLen)
	if err != nil {
		return nil, err
	}
	hash, err := HashPassword(secret, nil) // reuse argon2id hasher for consistency
	if err != nil {
		return nil, fmt.Errorf("auth: hash api key: %w", err)
	}
	now := time.Now().UTC()
	return &APIKeyPlaintext{
		PlainToken: APIKeyPrefix + keyID + "_" + secret,
		Record: &datastore.APIKey{
			ID:        "ak-" + keyID,
			KeyID:     keyID,
			Hash:      hash,
			UserID:    userID,
			Name:      name,
			Scopes:    append([]string(nil), scopes...),
			CreatedAt: now,
		},
	}, nil
}

// ParseAPIKey splits a wire-format token into (keyID, secret). It
// validates the prefix and shape but performs no datastore I/O.
// Returns ErrInvalidAPIKey on malformed input.
func ParseAPIKey(token string) (keyID, secret string, err error) {
	if !strings.HasPrefix(token, APIKeyPrefix) {
		return "", "", ErrInvalidAPIKey
	}
	rest := token[len(APIKeyPrefix):]
	sep := strings.IndexByte(rest, '_')
	if sep <= 0 || sep == len(rest)-1 {
		return "", "", ErrInvalidAPIKey
	}
	keyID = rest[:sep]
	secret = rest[sep+1:]
	if len(keyID) != apiKeyIDLen || len(secret) < 20 {
		return "", "", ErrInvalidAPIKey
	}
	return keyID, secret, nil
}

// VerifyAPIKey looks up the key by ID, verifies the secret, checks
// lifecycle flags (revoked, expired), and returns the persisted row
// on success. On any failure path it returns ErrInvalidAPIKey wrapped
// with context (safe to log).
//
// The caller should follow success with store.APIKeys().TouchUsage
// in a separate goroutine — we do not block the auth path on a write.
func VerifyAPIKey(ctx context.Context, store datastore.Store, token string) (*datastore.APIKey, error) {
	if store == nil {
		return nil, errors.New("auth: VerifyAPIKey requires a non-nil datastore.Store")
	}
	keyID, secret, err := ParseAPIKey(token)
	if err != nil {
		return nil, err
	}
	row, err := store.APIKeys().GetByKeyID(ctx, keyID)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			return nil, ErrInvalidAPIKey
		}
		return nil, fmt.Errorf("auth: load api key: %w", err)
	}
	if row.RevokedAt != nil {
		return nil, fmt.Errorf("%w: revoked", ErrInvalidAPIKey)
	}
	if row.ExpiresAt != nil && !row.ExpiresAt.IsZero() && row.ExpiresAt.Before(time.Now()) {
		return nil, fmt.Errorf("%w: expired", ErrInvalidAPIKey)
	}
	if err := VerifyPassword(row.Hash, secret); err != nil {
		if errors.Is(err, ErrInvalidCredentials) {
			return nil, ErrInvalidAPIKey
		}
		return nil, fmt.Errorf("%w: %v", ErrInvalidAPIKey, err)
	}
	return row, nil
}

// MaskAPIKey returns a dashboard-safe prefix of a wire-format token,
// e.g. "opi_x7k2fe8p_...". Never returns the secret half. Used by the
// admin CLI so operators can identify a row after the one-time reveal.
func MaskAPIKey(token string) string {
	if !strings.HasPrefix(token, APIKeyPrefix) {
		return "***"
	}
	rest := token[len(APIKeyPrefix):]
	sep := strings.IndexByte(rest, '_')
	if sep <= 0 {
		return APIKeyPrefix + "***"
	}
	return APIKeyPrefix + rest[:sep] + "_***"
}

// randomKeyID returns a URL-safe, lowercase, n-character identifier.
// We use RandomToken + slice rather than a custom alphabet so we never
// have to audit encoding logic ourselves.
func randomKeyID(n int) (string, error) {
	// base64 yields ~1.33 chars per byte; ask for more than we need
	// and truncate, so we can reject the "=" padding safely.
	raw, err := RandomToken(n*2 + 2)
	if err != nil {
		return "", err
	}
	// Strip anything non-[a-z0-9] to keep key IDs greppable.
	out := make([]byte, 0, n)
	for _, c := range raw {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			out = append(out, byte(c))
			if len(out) == n {
				break
			}
		} else if c >= 'A' && c <= 'Z' {
			out = append(out, byte(c)+32)
			if len(out) == n {
				break
			}
		}
	}
	if len(out) < n {
		// Retry once with a bigger pool; vanishingly rare.
		raw, err := RandomToken(n * 4)
		if err != nil {
			return "", err
		}
		for _, c := range raw {
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
				out = append(out, byte(c))
			} else if c >= 'A' && c <= 'Z' {
				out = append(out, byte(c)+32)
			}
			if len(out) == n {
				break
			}
		}
		if len(out) < n {
			return "", errors.New("auth: not enough entropy for key id")
		}
	}
	return string(out), nil
}
