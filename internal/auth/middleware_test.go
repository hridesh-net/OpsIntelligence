package auth_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// finalHandler echoes the principal's username + type so tests can
// assert the middleware attached the right identity.
var finalHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	p := auth.PrincipalFrom(r.Context())
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, string(p.Type)+":"+p.Username)
})

func buildAuth(t *testing.T, withSessions bool) (*auth.Authenticator, *auth.SessionManager, datastore.Store) {
	t.Helper()
	store := openStore(t)
	if _, _, err := rbac.SeedBuiltInRoles(context.Background(), store); err != nil {
		t.Fatal(err)
	}
	var sm *auth.SessionManager
	cfg := auth.AuthenticatorConfig{
		Store:         store,
		Resolver:      rbac.NewResolver(store),
		AcceptAPIKeys: true,
	}
	if withSessions {
		sm = auth.NewSessionManager(store, auth.SessionOptions{
			Secure: false,
		})
		cfg.Sessions = sm
	}
	a, err := auth.NewAuthenticator(cfg)
	if err != nil {
		t.Fatalf("NewAuthenticator: %v", err)
	}
	return a, sm, store
}

func TestAuthenticator_NoCreds_Rejects(t *testing.T) {
	a, _, _ := buildAuth(t, false)
	srv := httptest.NewServer(a.Middleware(finalHandler))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.Contains(got, "Bearer") {
		t.Errorf("missing WWW-Authenticate Bearer challenge: %q", got)
	}
}

func TestAuthenticator_AllowAnonymous(t *testing.T) {
	store := openStore(t)
	a, err := auth.NewAuthenticator(auth.AuthenticatorConfig{
		Store:          store,
		Resolver:       rbac.NewResolver(store),
		AllowAnonymous: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Middleware(finalHandler))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d body=%s, want 200", resp.StatusCode, body)
	}
	if string(body) != "anonymous:" {
		t.Errorf("body = %q, want anonymous:", body)
	}
}

func TestAuthenticator_APIKey(t *testing.T) {
	a, _, store := buildAuth(t, false)
	ctx := context.Background()
	u := &datastore.User{ID: "u1", Username: "alice", PasswordHash: "h", Status: datastore.UserActive}
	if err := store.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := store.Roles().AssignToUser(ctx, "u1", "role-admin"); err != nil {
		t.Fatal(err)
	}
	pt, err := auth.GenerateAPIKey("u1", "ci", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.APIKeys().Create(ctx, pt.Record); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(a.Middleware(finalHandler))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer "+pt.PlainToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	// Username format for api key principals: "<user>/<keyID>".
	if !strings.HasPrefix(string(body), "apikey:alice/") {
		t.Errorf("body = %q, want prefix apikey:alice/", body)
	}
}

func TestAuthenticator_InvalidBearer(t *testing.T) {
	a, _, _ := buildAuth(t, false)
	srv := httptest.NewServer(a.Middleware(finalHandler))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer opi_aaaaaaaa_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuthenticator_CookieSession(t *testing.T) {
	a, sm, store := buildAuth(t, true)
	ctx := context.Background()
	u := &datastore.User{ID: "u1", Username: "alice", PasswordHash: "h", Status: datastore.UserActive}
	if err := store.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := store.Roles().AssignToUser(ctx, "u1", "role-admin"); err != nil {
		t.Fatal(err)
	}

	// Manually create a session to simulate a logged-in browser.
	w := httptest.NewRecorder()
	if _, err := sm.Create(ctx, w, "u1", "", ""); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(a.Middleware(finalHandler))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	for _, c := range w.Result().Cookies() {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if string(body) != "user:alice" {
		t.Errorf("body = %q, want user:alice", body)
	}
}

func TestAuthenticator_RevokedSession(t *testing.T) {
	a, sm, store := buildAuth(t, true)
	ctx := context.Background()
	u := &datastore.User{ID: "u1", Username: "alice", PasswordHash: "h", Status: datastore.UserActive}
	if err := store.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	sess, err := sm.Create(ctx, w, "u1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Sessions().Revoke(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(a.Middleware(finalHandler))
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	for _, c := range w.Result().Cookies() {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("revoked session status = %d, want 401", resp.StatusCode)
	}
}

func TestAuthenticator_LegacyToken(t *testing.T) {
	store := openStore(t)
	a, err := auth.NewAuthenticator(auth.AuthenticatorConfig{
		Store:             store,
		Resolver:          rbac.NewResolver(store),
		LegacyBearerToken: "shh-its-a-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.Middleware(finalHandler))
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.Header.Set("Authorization", "Bearer shh-its-a-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if string(body) != "system:legacy-shared-token" {
		t.Errorf("body = %q, want system:legacy-shared-token", body)
	}
}

func TestRequireCSRF(t *testing.T) {
	a, sm, store := buildAuth(t, true)
	ctx := context.Background()
	u := &datastore.User{ID: "u1", Username: "alice", PasswordHash: "h", Status: datastore.UserActive}
	if err := store.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	_, err := sm.Create(ctx, w, "u1", "", "")
	if err != nil {
		t.Fatal(err)
	}
	var csrfCookie *http.Cookie
	cookies := w.Result().Cookies()
	for _, c := range cookies {
		if c.Name == auth.DefaultCSRFCookie {
			csrfCookie = c
		}
	}
	if csrfCookie == nil {
		t.Fatal("csrf cookie missing")
	}

	handler := a.Middleware(a.RequireCSRF(finalHandler))
	srv := httptest.NewServer(handler)
	defer srv.Close()

	// GET is always allowed, even without the header.
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET status=%d, want 200", resp.StatusCode)
	}

	// POST without the header is rejected.
	req, _ = http.NewRequest("POST", srv.URL+"/", strings.NewReader(""))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST without header status=%d, want 403", resp.StatusCode)
	}

	// POST with matching header is accepted.
	req, _ = http.NewRequest("POST", srv.URL+"/", strings.NewReader(""))
	for _, c := range cookies {
		req.AddCookie(c)
	}
	req.Header.Set("X-CSRF-Token", csrfCookie.Value)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("POST with header status=%d, want 200", resp.StatusCode)
	}
}
