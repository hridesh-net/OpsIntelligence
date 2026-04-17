// Package dashboard embeds the OpsIntelligence ops-plane dashboard
// shell — a minimal SPA that lets an operator sign in, see who they
// are, and render a placeholder Settings frame.
//
// The real settings / users / tasks / logs UIs land in phase 3c once
// /api/v1/configsvc is in place. This package only ships enough
// markup and JS to verify the phase-2c auth pipeline end-to-end in a
// browser:
//
//   - unauthenticated hit on / redirects to the login page
//   - login POSTs to /api/v1/auth/login, receives a session cookie
//   - the dashboard frame renders with the user's name + roles
//   - logout POSTs to /api/v1/auth/logout and clears the session
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
		switch path {
		case "", "/":
			// Absolute path so the redirect survives http.StripPrefix,
			// which rewrites r.URL.Path to "/" before reaching us.
			http.Redirect(w, r, "/dashboard/app", http.StatusFound)
			return
		case "app":
			r.URL.Path = "/app.html"
		case "login":
			r.URL.Path = "/login.html"
		}
		setDashboardHeaders(w)
		static.ServeHTTP(w, r)
	})
}

// setDashboardHeaders stamps the small set of security headers the
// dashboard depends on. Kept in a helper so it's easy to tighten CSP
// later when we inline less HTML.
func setDashboardHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Referrer-Policy", "same-origin")
	w.Header().Set("Cache-Control", "no-cache")
}
