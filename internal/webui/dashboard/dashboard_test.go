package dashboard

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandler_ServesAppShell asserts /dashboard/app returns the
// post-login frame with the phase-3c settings sub-nav wired up. If
// this fails the SPA bundle was not embedded (typical break: a new
// asset was added but not committed under assets/).
func TestHandler_ServesAppShell(t *testing.T) {
	body := getDashboard(t, "/app")
	wantSubstrings := []string{
		`id="settings-nav"`,
		`data-section="gateway"`,
		`data-section="providers"`,
		`data-section="mcp"`,
		`data-section="webhooks"`,
		`id="settings-body"`,
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(body, s) {
			t.Fatalf("dashboard app.html missing %q", s)
		}
	}
}

// TestHandler_ServesLogin keeps phase-2c login surface working.
func TestHandler_ServesLogin(t *testing.T) {
	body := getDashboard(t, "/login")
	if !strings.Contains(body, `id="login-form"`) {
		t.Fatal("dashboard login.html missing login form")
	}
}

// TestHandler_ServesSPABundle checks the JS bundle is present and
// contains the schema-driven settings renderer the new UI relies on.
func TestHandler_ServesSPABundle(t *testing.T) {
	body := getDashboard(t, "/app.js")
	wantSubstrings := []string{
		"CONFIG_SCHEMA",
		"loadSettingsSection",
		"saveSettingsForm",
		"If-Match",
		"renderProvidersSection",
		"renderMCPSection",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(body, s) {
			t.Fatalf("dashboard app.js missing %q", s)
		}
	}
}

// TestHandler_ServesStyles makes sure new selectors used by the
// settings UI shipped with the bundle.
func TestHandler_ServesStyles(t *testing.T) {
	body := getDashboard(t, "/style.css")
	wantSubstrings := []string{
		".settings-shell",
		".settings-nav-item",
		".section-form",
		".toast",
	}
	for _, s := range wantSubstrings {
		if !strings.Contains(body, s) {
			t.Fatalf("dashboard style.css missing %q", s)
		}
	}
}

// TestHandler_RootRedirects keeps the /dashboard/ landing redirect
// behaviour stable (regression test for phase 2c bug where the
// redirect leaked the upstream Host).
func TestHandler_RootRedirects(t *testing.T) {
	srv := httptest.NewServer(http.StripPrefix("/dashboard", Handler()))
	defer srv.Close()
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get(srv.URL + "/dashboard/")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status: got %d, want 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/dashboard/app" {
		t.Fatalf("location: got %q, want /dashboard/app", loc)
	}
}

func getDashboard(t *testing.T, path string) string {
	t.Helper()
	srv := httptest.NewServer(http.StripPrefix("/dashboard", Handler()))
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/dashboard" + path)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %s: got %d, want 200", path, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}
