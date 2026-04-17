package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/configsvc"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// AuthService is the HTTP surface that exposes the phase-2 auth layer
// to the gateway: a login/logout/whoami endpoint set, the shared
// Authenticator middleware, and the bootstrap handler used on a
// fresh install to mint the first owner account.
//
// It is constructed once at boot (BuildAuthService) and attached to
// Server.AuthService. When that field is nil the gateway runs in its
// legacy shared-bearer-token mode and phase-2 endpoints are absent.
type AuthService struct {
	Store         datastore.Store
	Resolver      *rbac.Resolver
	Authenticator *auth.Authenticator
	Sessions      *auth.SessionManager

	// LocalEnabled / APIKeysEnabled / OIDCEnabled / CSRFEnabled mirror
	// the config flags so the dashboard can render the right login
	// form without a second round-trip.
	LocalEnabled   bool
	APIKeysEnabled bool
	OIDCEnabled    bool
	CSRFEnabled    bool

	MinPasswordLength int

	// LegacySharedToken is surfaced here so handlers can advertise
	// "Bearer"-style clients remain supported, without leaking the
	// token itself.
	LegacyTokenConfigured bool

	// AllowAnonymousBootstrap flips to false automatically once the
	// users table has any row — the /api/v1/auth/bootstrap handler
	// refuses further anonymous writes after the first owner exists.
	AllowAnonymousBootstrap bool

	// ConfigPath points to opsintelligence.yaml and is used by the
	// phase-3 configsvc-backed settings API.
	ConfigPath string

	Log *zap.Logger
}

// BuildAuthService wires every piece phase 2b produced into a single
// gateway-mountable service. Safe to call even when the operator has
// not enabled RBAC yet (AllowAnonymous=true; every endpoint still
// refuses credentials it was not configured to accept).
//
// Returns (nil, nil) when the datastore is not configured — the
// gateway falls back to its pre-2c Bearer-token model in that case.
func BuildAuthService(ctx context.Context, cfg *config.Config, store datastore.Store, log *zap.Logger) (*AuthService, error) {
	if store == nil {
		return nil, nil
	}
	if log == nil {
		log = zap.NewNop()
	}

	// Seed built-in roles so the Resolver always finds role-owner etc.
	// Cheap + idempotent; safer to run on every boot than to rely on
	// an operator having invoked `opsintelligence admin init`.
	if _, _, err := rbac.SeedBuiltInRoles(ctx, store); err != nil {
		return nil, fmt.Errorf("auth: seed built-in roles: %w", err)
	}

	ac := &cfg.Auth
	localEnabled := ac.Local.Enabled == nil || *ac.Local.Enabled
	apiKeysEnabled := ac.APIKeys.Enabled == nil || *ac.APIKeys.Enabled
	csrfEnabled := ac.CSRF.Enabled == nil || *ac.CSRF.Enabled
	allowBootstrap := ac.AllowAnonymousBootstrap == nil || *ac.AllowAnonymousBootstrap

	sessionOpts := auth.SessionOptions{
		CookieName:     strings.TrimSpace(ac.Sessions.CookieName),
		CSRFCookieName: strings.TrimSpace(ac.Sessions.CSRFCookieName),
		Path:           strings.TrimSpace(ac.Sessions.Path),
		Domain:         strings.TrimSpace(ac.Sessions.Domain),
		SameSite:       parseSameSite(ac.Sessions.SameSite),
		TTL:            parseDurationOr(ac.Sessions.TTL, 7*24*time.Hour),
	}
	if ac.Sessions.Secure != nil {
		sessionOpts.Secure = *ac.Sessions.Secure
	}
	sessions := auth.NewSessionManager(store, sessionOpts)

	resolver := rbac.NewResolver(store)

	authn, err := auth.NewAuthenticator(auth.AuthenticatorConfig{
		Store:             store,
		Resolver:          resolver,
		Sessions:          sessions,
		AcceptAPIKeys:     apiKeysEnabled,
		LegacyBearerToken: strings.TrimSpace(ac.LegacySharedToken),
		AllowAnonymous:    true, // middleware per-route decides 401
		ErrorHandler:      jsonAuthError(log),
	})
	if err != nil {
		return nil, fmt.Errorf("auth: build authenticator: %w", err)
	}

	svc := &AuthService{
		Store:                   store,
		Resolver:                resolver,
		Authenticator:           authn,
		Sessions:                sessions,
		LocalEnabled:            localEnabled,
		APIKeysEnabled:          apiKeysEnabled,
		OIDCEnabled:             ac.OIDC.Enabled,
		CSRFEnabled:             csrfEnabled,
		MinPasswordLength:       ac.Local.MinPasswordLength,
		LegacyTokenConfigured:   strings.TrimSpace(ac.LegacySharedToken) != "",
		AllowAnonymousBootstrap: allowBootstrap,
		Log:                     log,
	}
	return svc, nil
}

// Mount attaches the phase-2 endpoints to mux:
//
//	GET  /api/v1/auth/status    — public; tells the dashboard what to render
//	POST /api/v1/auth/bootstrap — anonymous UNTIL first owner exists
//	POST /api/v1/auth/login     — public
//	POST /api/v1/auth/logout    — authenticated
//	GET  /api/v1/whoami         — authenticated
//
// Other /api/v1/ endpoints will land in phase 3b and run through
// Protect / ProtectCSRF which live on this service.
func (s *AuthService) Mount(mux *http.ServeMux) {
	if s == nil || mux == nil {
		return
	}
	mux.HandleFunc("/api/v1/auth/status", s.handleStatus)
	mux.HandleFunc("/api/v1/auth/bootstrap", s.handleBootstrap)
	mux.HandleFunc("/api/v1/auth/login", s.handleLogin)
	mux.Handle("/api/v1/auth/logout", s.Protect(http.HandlerFunc(s.handleLogout)))
	mux.Handle("/api/v1/whoami", s.Protect(http.HandlerFunc(s.handleWhoami)))

	// Phase 3b: configsvc-backed settings API.
	mux.Handle("/api/v1/config", s.Protect(http.HandlerFunc(s.handleConfigRoot)))
	mux.Handle("/api/v1/config/", s.ProtectCSRF(http.HandlerFunc(s.handleConfigSections)))

	// Phase 3d: users, roles, and API keys management. Reads use
	// Protect (no CSRF); mutating verbs run through ProtectCSRF so
	// cookie sessions need the paired X-CSRF-Token header. API-key
	// callers are exempt from CSRF by virtue of Authenticator's
	// per-scheme logic.
	mux.Handle("/api/v1/users", s.usersRouter())
	mux.Handle("/api/v1/users/", s.userSubtreeRouter())
	mux.Handle("/api/v1/roles", s.Protect(http.HandlerFunc(s.handleRoles)))
	mux.Handle("/api/v1/roles/", s.Protect(http.HandlerFunc(s.handleRoleGet)))
	mux.Handle("/api/v1/apikeys", s.apikeysRouter())
	mux.Handle("/api/v1/apikeys/", s.apikeyItemRouter())
}

// usersRouter dispatches between Protect (for GET) and ProtectCSRF
// (for POST) on the same path so mutating verbs always pay the CSRF
// cost but read verbs stay cookie-friendly.
func (s *AuthService) usersRouter() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.Protect(http.HandlerFunc(s.handleUsers)).ServeHTTP(w, r)
			return
		}
		s.ProtectCSRF(http.HandlerFunc(s.handleUsers)).ServeHTTP(w, r)
	})
}

func (s *AuthService) userSubtreeRouter() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.Protect(http.HandlerFunc(s.handleUserSubtree)).ServeHTTP(w, r)
			return
		}
		s.ProtectCSRF(http.HandlerFunc(s.handleUserSubtree)).ServeHTTP(w, r)
	})
}

func (s *AuthService) apikeysRouter() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			s.Protect(http.HandlerFunc(s.handleAPIKeys)).ServeHTTP(w, r)
			return
		}
		s.ProtectCSRF(http.HandlerFunc(s.handleAPIKeys)).ServeHTTP(w, r)
	})
}

func (s *AuthService) apikeyItemRouter() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.ProtectCSRF(http.HandlerFunc(s.handleAPIKeyItem)).ServeHTTP(w, r)
	})
}

// Protect is the handler-wrapping shorthand that phase-3b handlers
// will use to require a valid Principal. Chains the Authenticator
// middleware in a require-auth mode that rejects anonymous callers.
func (s *AuthService) Protect(next http.Handler) http.Handler {
	if s == nil || s.Authenticator == nil {
		return next
	}
	// Authenticator is constructed with AllowAnonymous=true so the
	// middleware always attaches SOME principal; we then reject the
	// anonymous case here to keep per-route behaviour flexible.
	return s.Authenticator.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.PrincipalFrom(r.Context())
		if !p.IsAuthenticated() {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// ProtectCSRF composes Protect + RequireCSRF. Use on mutating
// endpoints that are accessible over a cookie session.
func (s *AuthService) ProtectCSRF(next http.Handler) http.Handler {
	if s == nil || s.Authenticator == nil {
		return next
	}
	if !s.CSRFEnabled {
		return s.Protect(next)
	}
	return s.Authenticator.Middleware(s.Authenticator.RequireCSRF(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.PrincipalFrom(r.Context())
		if !p.IsAuthenticated() {
			writeJSONError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})))
}

// ─────────────────────────────────────────────────────────────────────
// handlers
// ─────────────────────────────────────────────────────────────────────

type authStatusResponse struct {
	BootstrapNeeded    bool   `json:"bootstrap_needed"`
	LocalEnabled       bool   `json:"local_enabled"`
	APIKeysEnabled     bool   `json:"api_keys_enabled"`
	OIDCEnabled        bool   `json:"oidc_enabled"`
	CSRFEnabled        bool   `json:"csrf_enabled"`
	MinPasswordLength  int    `json:"min_password_length"`
	Version            string `json:"version,omitempty"`
	LegacyTokenPresent bool   `json:"legacy_token_present"`
}

func (s *AuthService) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	need, err := s.bootstrapNeeded(r.Context())
	if err != nil {
		s.Log.Error("auth status", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "auth status failed")
		return
	}
	writeJSON(w, http.StatusOK, authStatusResponse{
		BootstrapNeeded:    need,
		LocalEnabled:       s.LocalEnabled,
		APIKeysEnabled:     s.APIKeysEnabled,
		OIDCEnabled:        s.OIDCEnabled,
		CSRFEnabled:        s.CSRFEnabled,
		MinPasswordLength:  s.MinPasswordLength,
		LegacyTokenPresent: s.LegacyTokenConfigured,
	})
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	User      principalDTO `json:"user"`
	CSRFToken string       `json:"csrf_token,omitempty"`
	ExpiresAt time.Time    `json:"expires_at"`
}

func (s *AuthService) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !s.LocalEnabled {
		writeJSONError(w, http.StatusForbidden, "local password login disabled")
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "username and password are required")
		return
	}

	ctx := r.Context()
	user, err := s.Store.Users().GetByUsername(ctx, req.Username)
	if err != nil {
		// Constant-time password comparison against a fake hash would
		// be nicer, but the username enumeration surface is already
		// audited; return a generic 401.
		writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if user.Status != datastore.UserActive {
		writeJSONError(w, http.StatusUnauthorized, "account disabled")
		return
	}
	if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		s.Log.Error("login verify", zap.String("user", user.Username), zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "login failed")
		return
	}
	if auth.NeedsRehash(user.PasswordHash) {
		// Opportunistic upgrade: best effort.
		if newHash, herr := auth.HashPassword(req.Password, nil); herr == nil {
			_ = s.Store.Users().SetPassword(ctx, user.ID, newHash)
		}
	}

	principal, err := s.Resolver.ForUser(ctx, user)
	if err != nil {
		s.Log.Error("login resolve", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "login failed")
		return
	}
	sess, err := s.Sessions.Create(ctx, w, user.ID, r.RemoteAddr, r.UserAgent())
	if err != nil {
		s.Log.Error("login session", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "login failed")
		return
	}
	_ = s.Store.Users().RecordLogin(ctx, user.ID)

	writeJSON(w, http.StatusOK, loginResponse{
		User:      newPrincipalDTO(principal),
		CSRFToken: s.Sessions.CSRFTokenFrom(r),
		ExpiresAt: sess.ExpiresAt,
	})
}

func (s *AuthService) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Best-effort: identify the session cookie and revoke the row.
	if c, err := r.Cookie(s.Sessions.CookieName()); err == nil && c.Value != "" {
		_ = s.Sessions.Revoke(r.Context(), w, c.Value)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *AuthService) handleWhoami(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	p := auth.PrincipalFrom(r.Context())
	writeJSON(w, http.StatusOK, newPrincipalDTO(p))
}

type bootstrapRequest struct {
	Username string `json:"username"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

type bootstrapResponse struct {
	User principalDTO `json:"user"`
}

func (s *AuthService) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx := r.Context()
	need, err := s.bootstrapNeeded(ctx)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "bootstrap check failed")
		return
	}
	if !need {
		writeJSONError(w, http.StatusConflict, "ops plane already bootstrapped")
		return
	}
	if !s.AllowAnonymousBootstrap {
		writeJSONError(w, http.StatusForbidden, "anonymous bootstrap disabled by config")
		return
	}
	var req bootstrapRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid json")
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	req.Email = strings.TrimSpace(req.Email)
	if req.Username == "" || req.Password == "" {
		writeJSONError(w, http.StatusBadRequest, "username and password are required")
		return
	}
	if s.MinPasswordLength > 0 && len(req.Password) < s.MinPasswordLength {
		writeJSONError(w, http.StatusBadRequest,
			fmt.Sprintf("password must be at least %d characters", s.MinPasswordLength))
		return
	}
	hash, err := auth.HashPassword(req.Password, nil)
	if err != nil {
		s.Log.Error("bootstrap hash", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "bootstrap failed")
		return
	}
	user, created, err := rbac.BootstrapOwner(ctx, s.Store, req.Username, req.Email, hash)
	if err != nil {
		s.Log.Error("bootstrap owner", zap.Error(err))
		writeJSONError(w, http.StatusInternalServerError, "bootstrap failed")
		return
	}
	if !created {
		writeJSONError(w, http.StatusConflict, "ops plane already bootstrapped")
		return
	}
	principal, err := s.Resolver.ForUser(ctx, user)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "bootstrap resolve failed")
		return
	}
	if _, err := s.Sessions.Create(ctx, w, user.ID, r.RemoteAddr, r.UserAgent()); err != nil {
		s.Log.Warn("bootstrap session create", zap.Error(err))
	}
	writeJSON(w, http.StatusCreated, bootstrapResponse{User: newPrincipalDTO(principal)})
}

// bootstrapNeeded is a single-call helper so every surface that
// advertises first-run mode (status, bootstrap) agrees on the answer.
func (s *AuthService) bootstrapNeeded(ctx context.Context) (bool, error) {
	if s == nil || s.Store == nil {
		return false, errors.New("auth service not initialised")
	}
	n, err := s.Store.Users().Count(ctx)
	if err != nil {
		return false, err
	}
	return n == 0, nil
}

// ─────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────

// principalDTO is the over-the-wire shape for /whoami and login
// responses. Keeps Permissions out unless the dashboard explicitly
// asks — wasteful to ship a 30-entry array on every page load.
type principalDTO struct {
	Type        string   `json:"type"`
	UserID      string   `json:"user_id,omitempty"`
	Username    string   `json:"username,omitempty"`
	DisplayName string   `json:"display_name,omitempty"`
	Email       string   `json:"email,omitempty"`
	Roles       []string `json:"roles,omitempty"`
	APIKeyID    string   `json:"api_key_id,omitempty"`
	IsSystem    bool     `json:"is_system,omitempty"`
}

func newPrincipalDTO(p *auth.Principal) principalDTO {
	if p == nil {
		return principalDTO{Type: "anonymous"}
	}
	return principalDTO{
		Type:        string(p.Type),
		UserID:      p.UserID,
		Username:    p.Username,
		DisplayName: p.DisplayName,
		Email:       p.Email,
		Roles:       append([]string(nil), p.Roles...),
		APIKeyID:    p.APIKeyID,
		IsSystem:    p.IsSystem(),
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *AuthService) cfgSvc() *configsvc.Service {
	return configsvc.New(s.ConfigPath)
}

// jsonAuthError is the ErrorHandler passed into auth.Authenticator so
// 401s from the middleware ship JSON bodies that the dashboard
// understands, instead of the default plain-text.
func jsonAuthError(log *zap.Logger) func(http.ResponseWriter, *http.Request, int, error) {
	return func(w http.ResponseWriter, r *http.Request, status int, err error) {
		if status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="opsintelligence"`)
		}
		if log != nil && status >= 500 {
			log.Warn("gateway auth error",
				zap.Int("status", status),
				zap.String("path", r.URL.Path),
				zap.Error(err),
			)
		}
		msg := "unauthorized"
		if err != nil {
			switch status {
			case http.StatusForbidden:
				msg = "forbidden"
			case http.StatusUnauthorized:
				msg = "unauthorized"
			case http.StatusInternalServerError:
				msg = "internal error"
			default:
				msg = err.Error()
			}
		}
		writeJSON(w, status, map[string]string{"error": msg})
	}
}

func parseSameSite(s string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "strict":
		return http.SameSiteStrictMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}
