package auth_test

import (
	"context"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/auth"
)

func TestPrincipalFromEmptyContextIsAnonymous(t *testing.T) {
	p := auth.PrincipalFrom(context.Background())
	if p == nil {
		t.Fatal("PrincipalFrom returned nil")
	}
	if p != auth.AnonymousPrincipal {
		t.Errorf("empty ctx should yield AnonymousPrincipal, got %+v", p)
	}
	if p.IsAuthenticated() {
		t.Errorf("anonymous should not be authenticated")
	}
	if p.IsSystem() {
		t.Errorf("anonymous should not be system")
	}
}

func TestPrincipalFromNilContext(t *testing.T) {
	// PrincipalFrom must not panic on a nil context.
	p := auth.PrincipalFrom(nil) //nolint:staticcheck // intentional nil check
	if p != auth.AnonymousPrincipal {
		t.Errorf("nil ctx should yield AnonymousPrincipal")
	}
}

func TestWithPrincipalRoundTrip(t *testing.T) {
	p := &auth.Principal{
		Type:     auth.PrincipalUser,
		UserID:   "u1",
		Username: "alice",
		Roles:    []string{"admin"},
	}
	ctx := auth.WithPrincipal(context.Background(), p)
	got := auth.PrincipalFrom(ctx)
	if got != p {
		t.Errorf("round-trip failed: got %+v, want %+v", got, p)
	}
	if !got.IsAuthenticated() {
		t.Errorf("user principal should be authenticated")
	}
	if got.IsSystem() {
		t.Errorf("user principal should not be system")
	}
	if !got.HasRole("admin") {
		t.Errorf("HasRole(admin) should be true")
	}
	if got.HasRole("operator") {
		t.Errorf("HasRole(operator) should be false")
	}
}

func TestSystemPrincipal(t *testing.T) {
	sys := auth.SystemPrincipal("cron:memory.sweep")
	if !sys.IsSystem() {
		t.Errorf("SystemPrincipal should IsSystem()")
	}
	if !sys.IsAuthenticated() {
		t.Errorf("SystemPrincipal should count as authenticated")
	}
	if !sys.HasRole("anything") {
		t.Errorf("SystemPrincipal should match any role via *")
	}
	if sys.Username != "cron:memory.sweep" {
		t.Errorf("Username = %q, want cron:memory.sweep", sys.Username)
	}
}

func TestWithPrincipalNilParent(t *testing.T) {
	// WithPrincipal should not panic on a nil parent context; it
	// silently upgrades to context.Background so middleware can
	// construct contexts without a pre-existing one.
	p := auth.SystemPrincipal("test")
	ctx := auth.WithPrincipal(nil, p) //nolint:staticcheck // intentional nil check
	if auth.PrincipalFrom(ctx) != p {
		t.Errorf("round-trip from nil-parent failed")
	}
}

func TestMustPrincipalPanicsOnMissing(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Errorf("MustPrincipal should panic when no principal in ctx")
		}
	}()
	_ = auth.MustPrincipal(context.Background())
}

func TestMustPrincipalReturnsAttached(t *testing.T) {
	p := &auth.Principal{Type: auth.PrincipalUser, UserID: "u1", Username: "a"}
	ctx := auth.WithPrincipal(context.Background(), p)
	got := auth.MustPrincipal(ctx)
	if got != p {
		t.Errorf("MustPrincipal returned wrong principal")
	}
}
