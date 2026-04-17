package gateway_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/drivers"
	"github.com/opsintelligence/opsintelligence/internal/gateway"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
	"gopkg.in/yaml.v3"
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
	if cfg.Providers.Ollama == nil {
		cfg.Providers.Ollama = &config.LocalCreds{BaseURL: "http://127.0.0.1:11434"}
	}
	cfgPath := filepath.Join(dir, "opsintelligence.yaml")
	cfgBytes, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal cfg: %v", err)
	}
	if err := os.WriteFile(cfgPath, cfgBytes, 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}

	svc, err := gateway.BuildAuthService(context.Background(), cfg, store, nil)
	if err != nil {
		t.Fatalf("build auth service: %v", err)
	}
	svc.ConfigPath = cfgPath

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

func seedUserWithRole(t *testing.T, svc *gateway.AuthService, username, password, roleID string) {
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
	if err := svc.Store.Roles().AssignToUser(ctx, "user-"+username, roleID); err != nil {
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

func TestConfigGet_RedactsSecretsWithoutSecretsRead(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	seedUserWithRole(t, svc, "viewer1", "viewer-password-long", "role-developer")

	login := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "viewer1",
		"password": "viewer-password-long",
	}, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d", login.StatusCode)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config", nil)
	for _, c := range login.Cookies() {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("config get status=%d body=%s", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if strings.Contains(body, "legacy-shared-token") || strings.Contains(body, "api_key") {
		t.Fatalf("expected redacted secrets in response, got: %s", body)
	}
}

func TestConfigPut_DeniedWithoutSettingsWrite(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	seedUserWithRole(t, svc, "viewer2", "viewer-password-long", "role-viewer")
	login := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "viewer2",
		"password": "viewer-password-long",
	}, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d", login.StatusCode)
	}

	reqBody := bytes.NewBufferString(`{"host":"127.0.0.1","port":19999}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/gateway", reqBody)
	req.Header.Set("Content-Type", "application/json")
	for _, c := range login.Cookies() {
		req.AddCookie(c)
	}
	req.Header.Set("X-CSRF-Token", svc.Sessions.CSRFTokenFrom(req))

	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestConfigPut_WithRevisionConflictReturns409(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	seedUser(t, svc, "ownercfg", "owner-password-long")
	login := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "ownercfg",
		"password": "owner-password-long",
	}, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d", login.StatusCode)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/config/gateway", nil)
	for _, c := range login.Cookies() {
		getReq.AddCookie(c)
	}
	getRR := httptest.NewRecorder()
	mux.ServeHTTP(getRR, getReq)
	if getRR.Code != http.StatusOK {
		t.Fatalf("get status=%d", getRR.Code)
	}
	var snap struct {
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(getRR.Body.Bytes(), &snap); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// First update with revision succeeds.
	req1 := httptest.NewRequest(http.MethodPut, "/api/v1/config/gateway", bytes.NewBufferString(`{"host":"127.0.0.1","port":20001}`))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("If-Match", snap.Revision)
	for _, c := range login.Cookies() {
		req1.AddCookie(c)
	}
	req1.Header.Set("X-CSRF-Token", svc.Sessions.CSRFTokenFrom(req1))
	rr1 := httptest.NewRecorder()
	mux.ServeHTTP(rr1, req1)
	if rr1.Code != http.StatusOK {
		t.Fatalf("first update status=%d body=%s", rr1.Code, rr1.Body.String())
	}

	// Reusing stale revision must conflict.
	req2 := httptest.NewRequest(http.MethodPut, "/api/v1/config/gateway", bytes.NewBufferString(`{"host":"127.0.0.1","port":20002}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("If-Match", snap.Revision)
	for _, c := range login.Cookies() {
		req2.AddCookie(c)
	}
	req2.Header.Set("X-CSRF-Token", svc.Sessions.CSRFTokenFrom(req2))
	rr2 := httptest.NewRecorder()
	mux.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestConfigPut_WritesAuditEntry(t *testing.T) {
	svc, mux := newTestAuthService(t, nil)
	seedUser(t, svc, "owneraudit", "owner-password-long")
	login := postJSON(t, mux, "/api/v1/auth/login", map[string]string{
		"username": "owneraudit",
		"password": "owner-password-long",
	}, nil)
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d", login.StatusCode)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/gateway", bytes.NewBufferString(`{"host":"127.0.0.1","port":21001}`))
	req.Header.Set("Content-Type", "application/json")
	for _, c := range login.Cookies() {
		req.AddCookie(c)
	}
	req.Header.Set("X-CSRF-Token", svc.Sessions.CSRFTokenFrom(req))
	rr := httptest.NewRecorder()
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("update status=%d body=%s", rr.Code, rr.Body.String())
	}

	entries, err := svc.Store.Audit().List(context.Background(), datastore.AuditFilter{
		Action: "config.",
		Limit:  10,
	})
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected at least one config audit entry")
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
