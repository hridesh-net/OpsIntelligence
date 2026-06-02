package dashboard

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TestHandler_ServesAppShell asserts /dashboard/app returns the React
// SPA shell (Vite-emitted app.html) — the post-login frame mounts at
// #root and loads its hashed bundle from /dashboard/assets/.
func TestHandler_ServesAppShell(t *testing.T) {
	body := getDashboard(t, "/app")
	for _, want := range []string{
		`id="root"`,
		`/dashboard/assets/app-`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard app.html missing %q", want)
		}
	}
}

// TestHandler_ServesLogin keeps the login surface working — the React
// login bundle is loaded and mounts at #root.
func TestHandler_ServesLogin(t *testing.T) {
	body := getDashboard(t, "/login")
	for _, want := range []string{
		`id="root"`,
		`/dashboard/assets/login-`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("dashboard login.html missing %q", want)
		}
	}
}

// TestHandler_ServesBundle checks that the hashed JS bundle referenced
// by app.html is actually embedded and served.
func TestHandler_ServesBundle(t *testing.T) {
	shell := getDashboard(t, "/app")
	re := regexp.MustCompile(`/dashboard/assets/(app-[A-Za-z0-9_-]+\.js)`)
	m := re.FindStringSubmatch(shell)
	if m == nil {
		t.Fatal("app.html does not reference a hashed app-*.js bundle")
	}
	body := getDashboard(t, "/assets/"+m[1])
	if len(body) == 0 {
		t.Fatal("bundle response is empty")
	}
}

// TestHandler_RootRedirects keeps the /dashboard/ landing redirect
// stable.
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

// TestAssets_EmbedsHashedBundle asserts that the Vite-emitted hashed
// assets directory is present in the embedded FS — guards against a
// future change to //go:embed that drops the nested directory.
func TestAssets_EmbedsHashedBundle(t *testing.T) {
	root := Assets()
	entries, err := fs.ReadDir(root, "assets")
	if err != nil {
		t.Fatalf("read embedded assets/: %v", err)
	}
	var sawAppJS bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "app-") && strings.HasSuffix(e.Name(), ".js") {
			sawAppJS = true
		}
	}
	if !sawAppJS {
		t.Fatal("embedded assets/ missing hashed app-*.js bundle")
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
