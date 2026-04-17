// Package auth defines the identity primitives that flow through the
// ops-plane request path: a Principal describes *who* is making a
// request, regardless of how they authenticated (browser session,
// bearer API key, OIDC token, or the legacy shared-token bootstrap).
//
// This file is deliberately tiny and dependency-free so every layer
// (HTTP middleware, gateway handlers, security guardrail, RBAC engine,
// audit log) can import auth.Principal without pulling in password
// hashing, OIDC, or datastore wiring. Those concerns live in sibling
// files in this package (phase 2b).
package auth

import (
	"context"
	"time"
)

// PrincipalType enumerates how a caller authenticated. The concrete
// value is authoritative — the RBAC engine and audit log switch on it
// rather than duck-typing from presence of fields.
type PrincipalType string

const (
	// PrincipalUser is a human user (local account or OIDC-backed).
	// Backed by a cookie session or dashboard request.
	PrincipalUser PrincipalType = "user"

	// PrincipalAPIKey is a long-lived machine credential attached to
	// a user. Requests present it as Authorization: Bearer opi_<id>_<secret>.
	PrincipalAPIKey PrincipalType = "apikey"

	// PrincipalSystem is an internal actor (cron jobs, webhook handlers,
	// the master agent when it launches sub-agents). It bypasses RBAC
	// but is still audited. Never produced from a network request.
	PrincipalSystem PrincipalType = "system"

	// PrincipalAnonymous is the default when authentication is disabled
	// or the request is unauthenticated but allowed (e.g. /api/v1/bootstrap
	// during first-run). No permissions.
	PrincipalAnonymous PrincipalType = "anonymous"
)

// Principal is the identity attached to every request and every agent
// tool call. It is safe to copy — fields are immutable after
// construction by the Authenticator middleware.
type Principal struct {
	// Type is the authentication scheme that produced this principal.
	Type PrincipalType

	// UserID is the datastore User.ID for User and APIKey principals;
	// empty for System and Anonymous. Use it as the canonical stable
	// identifier across audit / RBAC / ownership checks.
	UserID string

	// Username is a display-oriented alias; for User principals it is
	// the datastore User.Username; for APIKey it is "<user>/<keyID>";
	// for System it is a short name like "cron" / "webhook:github".
	Username string

	// DisplayName, when set, is preferred by the dashboard over
	// Username.
	DisplayName string

	// Email is populated for User principals when available. It is
	// NEVER used for auth decisions — only for display and audit.
	Email string

	// Roles are the roles currently granted to this principal. Empty
	// for Anonymous; ["*"] is a convention reserved for System and
	// means "skip RBAC, audit only". Do not mutate.
	Roles []string

	// Permissions is the flattened, deduplicated set of permission
	// keys resolved from Roles + any direct grants. Order is stable
	// (sorted ascending) so logs diff cleanly.
	Permissions []string

	// APIKeyID identifies the key used when Type == PrincipalAPIKey
	// (public key_id, not the secret). Empty otherwise.
	APIKeyID string

	// IssuedAt / ExpiresAt bracket the credential's validity. Zero
	// values mean "no explicit expiry" (e.g. cookie sessions rely on
	// the session row's expires_at instead of these fields).
	IssuedAt  time.Time
	ExpiresAt time.Time

	// RemoteAddr and UserAgent are captured at middleware time so
	// audit entries downstream don't have to re-parse the request.
	RemoteAddr string
	UserAgent  string
}

// IsAuthenticated reports whether the principal has any proven
// identity. Equivalent to Type != Anonymous.
func (p *Principal) IsAuthenticated() bool {
	if p == nil {
		return false
	}
	return p.Type != "" && p.Type != PrincipalAnonymous
}

// IsSystem reports whether this is an internal actor that bypasses
// RBAC. The gateway NEVER constructs a system principal from a network
// request — system principals are only minted by cron / webhook /
// subagent code paths that have already passed their own gate.
func (p *Principal) IsSystem() bool {
	return p != nil && p.Type == PrincipalSystem
}

// HasRole reports whether the principal has the given role name.
// "*" (system wildcard) matches everything.
func (p *Principal) HasRole(name string) bool {
	if p == nil {
		return false
	}
	for _, r := range p.Roles {
		if r == "*" || r == name {
			return true
		}
	}
	return false
}

// SystemPrincipal returns the well-known "*" system principal used by
// cron, webhook handlers, and master→subagent invocations. It is safe
// to share because every field is immutable.
//
// name annotates the audit log: "cron:memory.sweep",
// "webhook:github", "master:supervise".
func SystemPrincipal(name string) *Principal {
	if name == "" {
		name = "system"
	}
	return &Principal{
		Type:     PrincipalSystem,
		Username: name,
		Roles:    []string{"*"},
	}
}

// AnonymousPrincipal is the singleton used for unauthenticated requests
// that are still allowed to reach a handler (mostly /api/v1/bootstrap
// and the login page). Every field is empty on purpose.
var AnonymousPrincipal = &Principal{Type: PrincipalAnonymous}

// ─────────────────────────────────────────────────────────────────────
// context plumbing
// ─────────────────────────────────────────────────────────────────────

// principalCtxKey is unexported so callers must go through WithPrincipal
// / PrincipalFrom. Using an empty struct type avoids collisions with
// any string-keyed contexts in the codebase.
type principalCtxKey struct{}

// WithPrincipal returns a derived context that carries p. A nil p
// resolves to AnonymousPrincipal on retrieval, which is almost always
// the safer default than panicking.
func WithPrincipal(parent context.Context, p *Principal) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	return context.WithValue(parent, principalCtxKey{}, p)
}

// PrincipalFrom returns the Principal attached to ctx, or
// AnonymousPrincipal if none is set. Never returns nil.
func PrincipalFrom(ctx context.Context) *Principal {
	if ctx == nil {
		return AnonymousPrincipal
	}
	if p, ok := ctx.Value(principalCtxKey{}).(*Principal); ok && p != nil {
		return p
	}
	return AnonymousPrincipal
}

// MustPrincipal is the strict variant used inside handlers that MUST
// have already passed an Authenticator middleware. It panics if the
// context was never populated — calling it in the wrong handler is a
// bug worth crashing in tests. Explicit AnonymousPrincipal stored by
// middleware is fine.
func MustPrincipal(ctx context.Context) *Principal {
	if ctx == nil {
		panic("auth: nil context passed to MustPrincipal")
	}
	p, ok := ctx.Value(principalCtxKey{}).(*Principal)
	if !ok || p == nil {
		panic("auth: no principal in context (was the Authenticator middleware applied?)")
	}
	return p
}
