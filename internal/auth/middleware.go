package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// PrincipalResolver builds a full Principal from a datastore User or
// APIKey row. internal/rbac.Resolver satisfies this interface; we
// keep it narrow here so the middleware never needs to import rbac
// and vice versa.
type PrincipalResolver interface {
	ForUser(ctx context.Context, user *datastore.User) (*Principal, error)
	ForAPIKey(ctx context.Context, key *datastore.APIKey) (*Principal, error)
}

// AuthenticatorConfig parameterises the Authenticator middleware.
// Zero-value fields are off — the operator must opt in to each
// credential source. This keeps test setups explicit.
type AuthenticatorConfig struct {
	// Store is required. Used for user/apikey lookup and session
	// loads.
	Store datastore.Store

	// Resolver is required. Produces Principals from datastore rows.
	Resolver PrincipalResolver

	// Sessions is optional. When set, the middleware accepts the
	// session cookie. Required for the dashboard path.
	Sessions *SessionManager

	// AcceptAPIKeys enables Authorization: Bearer opi_<id>_<secret>.
	// Orthogonal to Sessions — a gateway can accept API keys without
	// a dashboard.
	AcceptAPIKeys bool

	// LegacyBearerToken, when non-empty, is accepted as a fixed
	// Authorization: Bearer <token> and grants a synthetic "system"
	// principal named "legacy-shared-token". This is the bootstrap
	// path for existing automation that predates the users table;
	// leave empty in new deployments.
	LegacyBearerToken string

	// AllowAnonymous, when true, does NOT reject unauthenticated
	// requests; it merely attaches AnonymousPrincipal so downstream
	// handlers can decide (e.g. /api/v1/bootstrap during first-run).
	// When false (default), missing credentials yield 401.
	AllowAnonymous bool

	// ErrorHandler, when set, overrides the default 401/500 renderer.
	// Use it to emit JSON errors from the gateway's main API surface.
	ErrorHandler func(w http.ResponseWriter, r *http.Request, status int, err error)
}

func (c AuthenticatorConfig) validate() error {
	if c.Store == nil {
		return errors.New("auth: AuthenticatorConfig.Store is required")
	}
	if c.Resolver == nil {
		return errors.New("auth: AuthenticatorConfig.Resolver is required")
	}
	return nil
}

// Authenticator is the credential-extraction middleware. It inspects
// each inbound request in this order:
//
//  1. Cookie session        (if SessionManager configured)
//  2. Authorization: Bearer <api key>   (if AcceptAPIKeys)
//  3. Authorization: Bearer <legacy>    (if LegacyBearerToken set)
//
// On success it attaches the resolved Principal to ctx and touches
// the session row (if source was a cookie). On failure it either
// rejects with 401 or — when AllowAnonymous — attaches the anonymous
// principal and lets downstream handlers decide.
//
// Never panics on misconfiguration; returns a 500 handler from New()
// so the error surfaces on the first request instead of at boot.
type Authenticator struct {
	cfg AuthenticatorConfig
}

// NewAuthenticator validates the config and returns an Authenticator.
// Returns an error so the caller can fail fast during wiring.
func NewAuthenticator(cfg AuthenticatorConfig) (*Authenticator, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Authenticator{cfg: cfg}, nil
}

// Middleware wraps next so every request flowing through it has a
// Principal attached to ctx.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	if a == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		principal, err := a.Authenticate(ctx, r)
		if err != nil {
			if errors.Is(err, errNoCredentials) && a.cfg.AllowAnonymous {
				p := *AnonymousPrincipal // copy so we can tag remote addr / UA
				p.RemoteAddr = r.RemoteAddr
				p.UserAgent = r.UserAgent()
				next.ServeHTTP(w, r.WithContext(WithPrincipal(ctx, &p)))
				return
			}
			a.reject(w, r, statusFor(err), err)
			return
		}
		principal.RemoteAddr = r.RemoteAddr
		principal.UserAgent = r.UserAgent()
		ctx = WithPrincipal(ctx, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Authenticate runs the full credential chain and returns the
// resolved Principal (or errNoCredentials / a more specific error).
// Exposed separately from Middleware so non-HTTP call sites (e.g. a
// future WebSocket upgrade) can reuse it.
func (a *Authenticator) Authenticate(ctx context.Context, r *http.Request) (*Principal, error) {
	if a == nil || a.cfg.Store == nil {
		return nil, errors.New("auth: Authenticator not initialised")
	}

	if a.cfg.Sessions != nil {
		sess, err := a.cfg.Sessions.Load(ctx, r)
		switch {
		case err == nil:
			user, err := a.cfg.Store.Users().Get(ctx, sess.UserID)
			if err != nil {
				return nil, err
			}
			if user.Status != datastore.UserActive {
				return nil, ErrInvalidCredentials
			}
			principal, err := a.cfg.Resolver.ForUser(ctx, user)
			if err != nil {
				return nil, err
			}
			_ = a.cfg.Sessions.Touch(ctx, sess.ID) // best effort
			return principal, nil
		case errors.Is(err, ErrSessionNotFound):
			// fall through to next scheme
		case errors.Is(err, ErrSessionRevoked):
			return nil, err
		default:
			return nil, err
		}
	}

	if auth := authorizationHeader(r); auth != "" {
		token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
		if token == auth {
			// No Bearer prefix — unsupported scheme.
			return nil, errUnsupportedScheme
		}
		// 1. Modern API key.
		if a.cfg.AcceptAPIKeys && strings.HasPrefix(token, APIKeyPrefix) {
			key, err := VerifyAPIKey(ctx, a.cfg.Store, token)
			if err != nil {
				return nil, err
			}
			principal, err := a.cfg.Resolver.ForAPIKey(ctx, key)
			if err != nil {
				return nil, err
			}
			// best-effort usage touch in a detached goroutine so the
			// verify path stays fast. A cancelled request ctx should
			// not abort the bookkeeping write.
			go func(id string) {
				_ = a.cfg.Store.APIKeys().TouchUsage(context.Background(), id)
			}(key.ID)
			return principal, nil
		}
		// 2. Legacy shared token (bootstrap path).
		if a.cfg.LegacyBearerToken != "" && ConstantTimeEqual(token, a.cfg.LegacyBearerToken) {
			return SystemPrincipal("legacy-shared-token"), nil
		}
		return nil, ErrInvalidCredentials
	}

	return nil, errNoCredentials
}

// ─────────────────────────────────────────────────────────────────────
// CSRF helper for unsafe methods (POST/PUT/PATCH/DELETE)
//
// The dashboard's SPA mirrors the CSRF cookie into an X-CSRF-Token
// header; the middleware compares them in constant time. API-key /
// legacy bearer paths bypass CSRF — those clients cannot "be tricked"
// by a browser.
// ─────────────────────────────────────────────────────────────────────

// RequireCSRF returns a middleware that enforces the double-submit
// CSRF check for cookie-authenticated unsafe-method requests. Must be
// applied AFTER Authenticator.Middleware.
func (a *Authenticator) RequireCSRF(next http.Handler) http.Handler {
	if a == nil || a.cfg.Sessions == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		p := PrincipalFrom(r.Context())
		// CSRF only applies to cookie-backed user sessions. API keys
		// and system/legacy bearer tokens are exempt.
		if p.Type != PrincipalUser {
			next.ServeHTTP(w, r)
			return
		}
		cookieTok := a.cfg.Sessions.CSRFTokenFrom(r)
		headerTok := r.Header.Get("X-CSRF-Token")
		if cookieTok == "" || headerTok == "" || !ConstantTimeEqual(cookieTok, headerTok) {
			a.reject(w, r, http.StatusForbidden, errCSRFMismatch)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ─────────────────────────────────────────────────────────────────────
// internals
// ─────────────────────────────────────────────────────────────────────

var (
	errNoCredentials     = errors.New("auth: no credentials presented")
	errUnsupportedScheme = errors.New("auth: unsupported Authorization scheme")
	errCSRFMismatch      = errors.New("auth: CSRF token mismatch")
)

func authorizationHeader(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if v != "" {
		return v
	}
	return r.Header.Get("X-API-Key")
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, ErrInvalidCredentials),
		errors.Is(err, ErrInvalidAPIKey),
		errors.Is(err, ErrSessionNotFound),
		errors.Is(err, ErrSessionRevoked),
		errors.Is(err, errNoCredentials),
		errors.Is(err, errUnsupportedScheme):
		return http.StatusUnauthorized
	case errors.Is(err, errCSRFMismatch):
		return http.StatusForbidden
	default:
		return http.StatusInternalServerError
	}
}

func (a *Authenticator) reject(w http.ResponseWriter, r *http.Request, status int, err error) {
	if a.cfg.ErrorHandler != nil {
		a.cfg.ErrorHandler(w, r, status, err)
		return
	}
	// Sensible default: plain text + WWW-Authenticate on 401 so curl
	// prompts correctly.
	if status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", "Bearer realm=\"opsintelligence\"")
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, http.StatusText(status), status)
}
