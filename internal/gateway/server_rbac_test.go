package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	authpkg "github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// No principal attached (bare deployment, legacy wrapper) — allowed.
func TestEnforceCtxPerm_NoPrincipal_Allows(t *testing.T) {
	s := NewServer(0, 0)
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	rec := httptest.NewRecorder()
	if !s.enforceCtxPerm(rec, req, rbac.PermChatUse) {
		t.Fatal("bare request (no principal attached) must remain allowed")
	}
}

// Authenticated user without chat.use — 403.
func TestEnforceCtxPerm_UserWithoutPerm_Denies(t *testing.T) {
	s := NewServer(0, 0)
	p := &authpkg.Principal{Type: authpkg.PrincipalUser, UserID: "u1", Username: "viewer",
		Permissions: []string{"dashboard.view"}}
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req = req.WithContext(authpkg.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	if s.enforceCtxPerm(rec, req, rbac.PermChatUse) {
		t.Fatal("user without chat.use must be denied")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

// Authenticated user with chat.use — allowed.
func TestEnforceCtxPerm_UserWithPerm_Allows(t *testing.T) {
	s := NewServer(0, 0)
	p := &authpkg.Principal{Type: authpkg.PrincipalUser, UserID: "u2", Username: "dev",
		Permissions: []string{"chat.use"}}
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req = req.WithContext(authpkg.WithPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	if !s.enforceCtxPerm(rec, req, rbac.PermChatUse) {
		t.Fatal("user with chat.use must be allowed")
	}
}

// System principal (legacy gateway token) bypasses.
func TestEnforceCtxPerm_System_Allows(t *testing.T) {
	s := NewServer(0, 0)
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req = req.WithContext(authpkg.WithPrincipal(req.Context(), authpkg.SystemPrincipal("gateway-cli")))
	rec := httptest.NewRecorder()
	if !s.enforceCtxPerm(rec, req, rbac.PermChatUse) {
		t.Fatal("system principal must bypass RBAC")
	}
}

// Anonymous principal explicitly attached (auth middleware ran but the
// caller never authenticated) — denied. This is the pre-v1.0.89 /ws hole.
func TestEnforceCtxPerm_Anonymous_Denies(t *testing.T) {
	s := NewServer(0, 0)
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req = req.WithContext(authpkg.WithPrincipal(req.Context(), authpkg.AnonymousPrincipal))
	rec := httptest.NewRecorder()
	if s.enforceCtxPerm(rec, req, rbac.PermChatUse) {
		t.Fatal("anonymous principal must be denied")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}
