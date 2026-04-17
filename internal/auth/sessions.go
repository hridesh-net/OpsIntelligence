package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
)

// Default cookie names. Overridable via SessionOptions so an operator
// running multiple OpsIntelligence instances on the same domain can
// avoid clobbering each other.
const (
	DefaultSessionCookie = "opi_session"
	DefaultCSRFCookie    = "opi_csrf"
	// DefaultSessionTTL is the idle timeout for browser sessions. The
	// session row is refreshed on every authenticated request via
	// SessionRepo.Touch, so active users don't notice the expiry.
	DefaultSessionTTL = 7 * 24 * time.Hour
	// SessionIDBytes is the entropy of a fresh session token. 32 bytes
	// → 256 bits of keyspace, ~43 characters after base64.
	SessionIDBytes = 32
	// CSRFTokenBytes is the entropy of a double-submit CSRF token.
	CSRFTokenBytes = 32
)

// ErrSessionNotFound is the caller-safe "cookie does not map to a row"
// sentinel. Wraps datastore.ErrNotFound transparently.
var ErrSessionNotFound = errors.New("auth: session not found")

// ErrSessionRevoked is returned when a session row exists but has
// been explicitly revoked or its expires_at has passed.
var ErrSessionRevoked = errors.New("auth: session revoked or expired")

// SessionOptions bundles operator-tunable cookie + TTL settings so the
// HTTP layer never has to hard-code them. Zero values fall back to the
// DefaultXxx constants above.
type SessionOptions struct {
	// CookieName defaults to DefaultSessionCookie.
	CookieName string
	// CSRFCookieName defaults to DefaultCSRFCookie.
	CSRFCookieName string
	// Path scope for the cookies. Defaults to "/".
	Path string
	// Domain is the cookie Domain attribute. Empty means "exact host",
	// which is the right default for the dashboard; only set when
	// running on a shared parent domain.
	Domain string
	// Secure sets the Secure flag. TRUE in prod (TLS); FALSE only for
	// local http://127.0.0.1. The gateway flips this based on
	// AuthConfig.Cookie.Secure / deployment profile.
	Secure bool
	// SameSite defaults to Lax. Strict is recommended for cloud
	// deployments that don't embed the dashboard anywhere.
	SameSite http.SameSite
	// TTL defaults to DefaultSessionTTL.
	TTL time.Duration
}

func (o SessionOptions) normalized() SessionOptions {
	if o.CookieName == "" {
		o.CookieName = DefaultSessionCookie
	}
	if o.CSRFCookieName == "" {
		o.CSRFCookieName = DefaultCSRFCookie
	}
	if o.Path == "" {
		o.Path = "/"
	}
	if o.SameSite == 0 {
		o.SameSite = http.SameSiteLaxMode
	}
	if o.TTL <= 0 {
		o.TTL = DefaultSessionTTL
	}
	return o
}

// SessionManager owns the lifecycle of browser sessions. It is a thin
// wrapper around datastore.SessionRepo that produces and consumes
// cookies; it does not know about roles, passwords, or providers.
//
// Safe for concurrent use.
type SessionManager struct {
	Store datastore.Store
	Opts  SessionOptions
}

// NewSessionManager returns a manager bound to store using normalised
// options.
func NewSessionManager(store datastore.Store, opts SessionOptions) *SessionManager {
	return &SessionManager{Store: store, Opts: opts.normalized()}
}

// Create mints a new session row for userID, writes it to the
// datastore, and sets the session cookie (and a fresh CSRF cookie) on
// w. Returns the persisted row on success.
//
// remoteAddr and userAgent are captured for audit; they are safe to
// leave empty in tests.
func (m *SessionManager) Create(ctx context.Context, w http.ResponseWriter, userID, remoteAddr, userAgent string) (*datastore.Session, error) {
	if m == nil || m.Store == nil {
		return nil, errors.New("auth: SessionManager not initialised")
	}
	if strings.TrimSpace(userID) == "" {
		return nil, errors.New("auth: Create requires userID")
	}
	id, err := RandomToken(SessionIDBytes)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	sess := &datastore.Session{
		ID:         id,
		UserID:     userID,
		CreatedAt:  now,
		ExpiresAt:  now.Add(m.Opts.TTL),
		LastSeenAt: now,
		UserAgent:  userAgent,
		RemoteAddr: remoteAddr,
	}
	if err := m.Store.Sessions().Create(ctx, sess); err != nil {
		return nil, fmt.Errorf("auth: persist session: %w", err)
	}
	http.SetCookie(w, m.sessionCookie(sess.ID, sess.ExpiresAt))
	csrf, err := RandomToken(CSRFTokenBytes)
	if err != nil {
		return nil, err
	}
	http.SetCookie(w, m.csrfCookie(csrf, sess.ExpiresAt))
	return sess, nil
}

// Load reads the session cookie off r, verifies the row in the
// datastore, and returns the persisted session. Returns
// ErrSessionNotFound when no cookie/row is present, ErrSessionRevoked
// when the row is revoked or expired.
//
// Load does NOT touch LastSeenAt; that is the caller's responsibility
// (the Authenticator middleware does it once per request).
func (m *SessionManager) Load(ctx context.Context, r *http.Request) (*datastore.Session, error) {
	if m == nil || m.Store == nil {
		return nil, errors.New("auth: SessionManager not initialised")
	}
	c, err := r.Cookie(m.Opts.CookieName)
	if err != nil || c == nil || c.Value == "" {
		return nil, ErrSessionNotFound
	}
	sess, err := m.Store.Sessions().Get(ctx, c.Value)
	if err != nil {
		if errors.Is(err, datastore.ErrNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("auth: load session: %w", err)
	}
	if sess.RevokedAt != nil {
		return nil, ErrSessionRevoked
	}
	if !sess.ExpiresAt.IsZero() && sess.ExpiresAt.Before(time.Now()) {
		return nil, ErrSessionRevoked
	}
	return sess, nil
}

// Touch updates the session's LastSeenAt timestamp. Called by the
// Authenticator middleware on every authenticated request. Errors are
// non-fatal — return them so the caller can log, but the request
// should continue.
func (m *SessionManager) Touch(ctx context.Context, id string) error {
	return m.Store.Sessions().Touch(ctx, id)
}

// Revoke marks the session as revoked in the datastore and expires
// both cookies on the response. Safe to call even if the session is
// already gone.
func (m *SessionManager) Revoke(ctx context.Context, w http.ResponseWriter, id string) error {
	if m == nil || m.Store == nil {
		return errors.New("auth: SessionManager not initialised")
	}
	_ = m.Store.Sessions().Revoke(ctx, id) // ignore "already gone"
	http.SetCookie(w, m.expireCookie(m.Opts.CookieName))
	http.SetCookie(w, m.expireCookie(m.Opts.CSRFCookieName))
	return nil
}

// CSRFTokenFrom returns the CSRF token from the double-submit cookie
// on r. The Authenticator middleware compares this against the value
// carried in an X-CSRF-Token header for unsafe methods.
func (m *SessionManager) CSRFTokenFrom(r *http.Request) string {
	c, err := r.Cookie(m.Opts.CSRFCookieName)
	if err != nil || c == nil {
		return ""
	}
	return c.Value
}

// IssueCSRF writes a fresh CSRF cookie on w, usually called after
// Create on login or Rotate after a privilege change. Returns the
// plaintext token so the caller can embed it in the login response
// body for the SPA.
func (m *SessionManager) IssueCSRF(w http.ResponseWriter, expires time.Time) (string, error) {
	tok, err := RandomToken(CSRFTokenBytes)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, m.csrfCookie(tok, expires))
	return tok, nil
}

// CookieName returns the session cookie name currently configured.
// Useful for the dashboard's logout handler.
func (m *SessionManager) CookieName() string { return m.Opts.CookieName }

// ─────────────────────────────────────────────────────────────────────
// private cookie builders
// ─────────────────────────────────────────────────────────────────────

func (m *SessionManager) sessionCookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     m.Opts.CookieName,
		Value:    value,
		Path:     m.Opts.Path,
		Domain:   m.Opts.Domain,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: true,
		Secure:   m.Opts.Secure,
		SameSite: m.Opts.SameSite,
	}
}

func (m *SessionManager) csrfCookie(value string, expires time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     m.Opts.CSRFCookieName,
		Value:    value,
		Path:     m.Opts.Path,
		Domain:   m.Opts.Domain,
		Expires:  expires,
		MaxAge:   int(time.Until(expires).Seconds()),
		HttpOnly: false, // SPA reads this to mirror into X-CSRF-Token
		Secure:   m.Opts.Secure,
		SameSite: m.Opts.SameSite,
	}
}

func (m *SessionManager) expireCookie(name string) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     m.Opts.Path,
		Domain:   m.Opts.Domain,
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   m.Opts.Secure,
		SameSite: m.Opts.SameSite,
	}
}
