// Package dashboard embeds the OpsIntelligence ops-plane dashboard:
// login + first-run owner bootstrap, an app shell with hash-based
// routing, and full Settings pages wired to the configsvc HTTP API
// (/api/v1/config/*).
//
// Phase 3c surface:
//
//   - GET  /dashboard/login          → password / OIDC sign-in form
//   - GET  /dashboard/app#/overview  → signed-in dashboard
//   - GET  /dashboard/app#/settings/<section>
//     → live editor backed by
//     /api/v1/config/<section>
//
// The dashboard talks to the gateway only over the public auth/config
// JSON APIs — no privileged direct access. Session cookies, CSRF and
// optimistic concurrency (If-Match) are all enforced server-side, so
// the JS stays small and unprivileged.
//
// Assets are embedded via //go:embed so the binary stays single-file.
package dashboard

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed assets/*
var embedded embed.FS

// Assets returns the embedded filesystem rooted at the assets/
// directory — handy for tests and callers that want to mount the
// raw static files under a custom prefix.
func Assets() fs.FS {
	sub, err := fs.Sub(embedded, "assets")
	if err != nil {
		panic("dashboard: sub assets: " + err.Error())
	}
	return sub
}

// Handler returns an http.Handler that serves the dashboard shell.
//
// Mount it under a prefix (typically "/dashboard/") using
// http.StripPrefix. The handler serves:
//
//   - /              → redirect to /app (the frame)
//   - /login         → login.html
//   - /app           → app.html (post-login frame)
//   - /app.js, /style.css, /favicon.svg → embedded static assets
//
// The handler does NOT perform authentication itself; the SPA uses
// fetch("/api/v1/whoami") to decide whether to show the login form or
// the frame. Mounting under TLS + the auth cookie makes the whole
// surface safe for public exposure.
func Handler() http.Handler {
	assets := Assets()
	static := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		htmlEntry := false
		switch {
		case path == "" || path == "/":
			// Absolute path so the redirect survives http.StripPrefix,
			// which rewrites r.URL.Path to "/" before reaching us.
			http.Redirect(w, r, "/dashboard/app", http.StatusFound)
			return
		case path == "app":
			r.URL.Path = "/app.html"
			htmlEntry = true
		case path == "login":
			r.URL.Path = "/login.html"
			htmlEntry = true
		case path == "kanban" || path == "boards" || path == "scrun" || path == "scrun/":
			// Boards tab → the React Scrun bundle under assets/scrun/.
			// Serve the entry HTML's bytes directly: rewriting to
			// "/scrun/index.html" and handing it to http.FileServer
			// trips its built-in canonicalisation, which 301-redirects any
			// "/index.html" path to "./" — that bounced the Boards tab back
			// to /dashboard/ → /dashboard/app instead of showing Scrun.
			setDashboardHeaders(w, true)
			serveEntry(w, r, assets, "scrun/index.html")
			return
		}
		setDashboardHeaders(w, htmlEntry)
		static.ServeHTTP(w, r)
	})
}

// serveEntry writes a single embedded HTML entry file directly, bypassing
// http.FileServer so its "/index.html" → "./" redirect never fires.
func serveEntry(w http.ResponseWriter, r *http.Request, assets fs.FS, name string) {
	data, err := fs.ReadFile(assets, name)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(data)
}

// setDashboardHeaders stamps the small set of security headers the
// dashboard depends on. Kept in a helper so it's easy to tighten CSP
// later when we inline less HTML.
//
// htmlEntry should be true for the *.html shell responses (app, login,
// kanban/scrun) so browsers always re-fetch the entry HTML and pick up
// fresh hashed-asset URLs after a redeploy. Hashed JS/CSS bundles
// under /assets/ stay cacheable — their content is keyed by hash.
func setDashboardHeaders(w http.ResponseWriter, htmlEntry bool) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "same-origin")
	if htmlEntry {
		w.Header().Set("Cache-Control", "no-store, max-age=0, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
}
