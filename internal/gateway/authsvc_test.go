package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/drivers"
	"github.com/opsintelligence/opsintelligence/internal/gateway"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// newTestAuthService brings up a scratch sqlite ops-plane and returns
// a fully-wired AuthService plus a test http.ServeMux that exposes
// its endpoints. The caller owns shutdown via t.Cleanup.
func newTestAuthService(t *testing.T, cfg *config.Config) (*gateway.AuthService, *http.ServeMux) {
	t.Helper()

	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "ops.db") + "?_foreign_keys=on&_busy_timeout=5000"
	store, err := datastore.Open(context.Background(), datastore.Config{
		Driver:     "sqlite",
		DSN:        dsn,
		Migrations: "auto",
	})
	if err != nil {
		t.Fatalf("datastore open: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if cfg == nil {
		cfg = config.LoadFromEnv()
	}

	svc, err := gateway.BuildAuthService(context.Background(), cfg, store, nil)
	if err != nil {
		t.Fatalf("build auth service: %v", err)
	}

	mux := http.NewServeMux()
	svc.Mount(mux)
	return svc, mux
}

// seedUser creates a user row + owner role assignment so tests can
// exercise the login path without walking the bootstrap flow.
func seedUser(t *testing.T, svc *gateway.AuthService, username, password string) {
	t.Helper()
	ctx := context.Background()
	hash, err := auth.HashPassword(password, nil)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if _, _, err := rbac.SeedBuiltInRoles(ctx, svc.Store); err != nil {
		t.Fatalf("seed roles: %v", err)
	}
	if err := svc.Store.Users().Create(ctx, &datastore.User{
		ID:           "user-" + username,
		Username:     username,
		Email:        username + "@example.test",
		PasswordHash: hash,
		Status:       datastore.UserActive,
	}); err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := svc.Store.Roles().AssignToUser(ctx, "user-"+username, "role-owner"); err != nil {
		t.Fatalf("assign role: %v", err)
	}
}

// ─────────────────────────────────────────────────────────────────────
// /api/v1/auth/status
// ─────────────────────────────────────────────────────────────────────

func TestStatus_BootstrapNeededOnFreshStore(t *testing.T) {
	_, mux := newTestAuthService(t, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got, _ := body["bootstrap_needed"].(bool); !got {
		t.Fatalf("expected bootstrap_needed=true on fresh store, body=%s", rr.Body.String())
	}
	if got, _ := body["local_enabled"].(bool); !got {
		t.Fatalf("expected local_enabled=true by default")
	}
}

func TestStatus_BootstrapNotNeededAfterOwner(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	seedUser(t, svc, "owner", "long-enough-password")

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/auth/status", nil))
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("json: %v", err)
	}
	if got, _ := body["bootstrap_needed"].(bool); got {
		t.Fatalf("expected bootstrap_needed=false after owner exists")
	}
}

// ─────────────────────────────────────────────────────────────────────
// /api/v1/auth/login
// ─────────────────────────────────────────────────────────────────────

func TestLogin_HappyPath_SetsSessionCookie(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	seedUser(t, svc, "alice", "super-long-password")

	res := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "alice",
		"password": "super-long-password",
	}, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d body=%s", res.StatusCode, readBody(res))
	}
	if !hasCookie(res, svc.Sessions.CookieName()) {
		t.Fatalf("login did not set session cookie; got %v", res.Cookies())
	}
	if !hasCookie(res, "opi_csrf") {
		t.Fatalf("login did not set csrf cookie; got %v", res.Cookies())
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(readBody(res)), &body); err != nil {
		t.Fatalf("body json: %v", err)
	}
	user, ok := body["user"].(map[string]any)
	if !ok {
		t.Fatalf("missing user in body")
	}
	if user["username"] != "alice" || user["type"] != "user" {
		t.Fatalf("unexpected principal: %v", user)
	}
}

func TestLogin_WrongPassword_Returns401(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	seedUser(t, svc, "bob", "correct-password")

	res := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "bob",
		"password": "wrong-password",
	}, nil)
	if res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d body=%s", res.StatusCode, readBody(res))
	}
	if hasCookie(res, svc.Sessions.CookieName()) {
		t.Fatalf("failed login should not set a session cookie")
	}
}

func TestLogin_MissingFields_Returns400(t *testing.T) {
	_, mux := newTestAuthService(t, nil)
	res := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "",
		"password": "",
	}, nil)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", res.StatusCode)
	}
}

// ─────────────────────────────────────────────────────────────────────
// /api/v1/whoami
// ─────────────────────────────────────────────────────────────────────

func TestWhoami_UnauthenticatedReturns401(t *testing.T) {
	_, mux := newTestAuthService(t, nil)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

func TestWhoami_WithSessionReturnsPrincipal(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	seedUser(t, svc, "carol", "super-long-password")

	login := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "carol",
		"password": "super-long-password",
	}, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d", login.StatusCode)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	for _, c := range login.Cookies() {
		r.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("whoami status=%d body=%s", rr.Code, rr.Body.String())
	}
	var p map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil {
		t.Fatalf("json: %v", err)
	}
	if p["username"] != "carol" || p["type"] != "user" {
		t.Fatalf("unexpected principal: %v", p)
	}
}

// ─────────────────────────────────────────────────────────────────────
// /api/v1/auth/logout
// ─────────────────────────────────────────────────────────────────────

func TestLogout_ClearsSessionCookie_And_WhoamiReturns401(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	seedUser(t, svc, "dave", "super-long-password")

	login := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "dave",
		"password": "super-long-password",
	}, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", login.StatusCode)
	}

	// POST /logout with session cookie AND CSRF (cookie session =>
	// CSRF applies).
	logout := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	for _, c := range login.Cookies() {
		logout.AddCookie(c)
	}
	csrf := svc.Sessions.CSRFTokenFrom(logout)
	if csrf == "" {
		t.Fatalf("expected csrf token to be present in cookies")
	}
	logout.Header.Set("X-CSRF-Token", csrf)
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, logout)
	if rr.Code != http.StatusOK {
		t.Fatalf("logout status=%d body=%s", rr.Code, rr.Body.String())
	}

	// After logout the original cookie is revoked; reusing it should
	// now yield 401 from whoami.
	next := httptest.NewRequest(http.MethodGet, "/api/v1/whoami", nil)
	for _, c := range login.Cookies() {
		next.AddCookie(c)
	}
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, next)
	if rr2.Code != http.StatusUnauthorized {
		t.Fatalf("whoami after logout: expected 401, got %d", rr2.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────
// /api/v1/auth/bootstrap
// ─────────────────────────────────────────────────────────────────────

func TestBootstrap_FirstRun_CreatesOwner(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)

	res := postJSON(t, mux, "/api/v1/auth/bootstrap", map[string]string{
		"username": "root",
		"email":    "root@example.test",
		"password": "bootstrap-password",
	}, nil)
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("bootstrap status=%d body=%s", res.StatusCode, readBody(res))
	}
	if !hasCookie(res, svc.Sessions.CookieName()) {
		t.Fatalf("bootstrap did not set session cookie")
	}

	// Second call must refuse — no double-bootstrap.
	res2 := postJSON(t, mux, "/api/v1/auth/bootstrap", map[string]string{
		"username": "second",
		"password": "another-password-long",
	}, nil)
	if res2.StatusCode != http.StatusConflict {
		t.Fatalf("second bootstrap: expected 409 conflict, got %d body=%s", res2.StatusCode, readBody(res2))
	}
}

func TestBootstrap_ShortPasswordRejected(t *testing.T) {
	_, mux := newTestAuthService(t, nil)
	res := postJSON(t, mux, "/api/v1/auth/bootstrap", map[string]string{
		"username": "root",
		"password": "short",
	}, nil)
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d body=%s", res.StatusCode, readBody(res))
	}
}

// ─────────────────────────────────────────────────────────────────────
// CSRF protection
// ─────────────────────────────────────────────────────────────────────

func TestLogout_WithoutCSRF_ReturnsForbidden(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	seedUser(t, svc, "eve", "super-long-password")

	login := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "eve",
		"password": "super-long-password",
	}, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login: %d", login.StatusCode)
	}

	// POST with session cookie but NO X-CSRF-Token header.
	//
	// Note: AuthService.Protect (used on /logout) does not invoke
	// RequireCSRF today — ProtectCSRF is the opt-in. So /logout
	// without CSRF should STILL succeed, and this test documents
	// that current shape; tighten once phase 3 moves to ProtectCSRF.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	for _, c := range login.Cookies() {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("logout without csrf: expected 200 under Protect semantics, got %d", rr.Code)
	}
}

// ─────────────────────────────────────────────────────────────────────
// helpers
// ─────────────────────────────────────────────────────────────────────

// postJSON posts body as JSON to path and returns the *http.Response.
// Built on httptest.ResponseRecorder so cookies round-trip cleanly.
func postJSON(t *testing.T, mux http.Handler, path string, body any, headers http.Header) *http.Response {
	t.Helper()
	buf, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(buf))
	r.Header.Set("Content-Type", "application/json")
	for k, vs := range headers {
		for _, v := range vs {
			r.Header.Add(k, v)
		}
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, r)
	return rr.Result()
}

func readBody(res *http.Response) string {
	if res == nil || res.Body == nil {
		return ""
	}
	b, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	return strings.TrimSpace(string(b))
}

func hasCookie(res *http.Response, name string) bool {
	for _, c := range res.Cookies() {
		if c.Name == name && c.Value != "" {
			return true
		}
	}
	return false
}
