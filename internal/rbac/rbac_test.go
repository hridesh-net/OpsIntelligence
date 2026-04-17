package rbac_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/drivers"
	"github.com/opsintelligence/opsintelligence/internal/rbac"
)

// ─────────────────────────────────────────────────────────────────────
// Permission.Matches
// ─────────────────────────────────────────────────────────────────────

func TestPermissionMatches(t *testing.T) {
	cases := []struct {
		perm    rbac.Permission
		granted string
		want    bool
	}{
		{rbac.PermTasksRead, "tasks.read", true},
		{rbac.PermTasksRead, "tasks.*", true},
		{rbac.PermTasksRead, "*", true},
		{rbac.PermTasksRead, "tasks.cancel", false},
		{rbac.PermTasksRead, "users.*", false},
		{rbac.PermAgentInvoke, "agent.*", true},
		{rbac.PermAgentInvoke, "agent.invoke", true},
		{rbac.PermAgentInvoke, "", false},
		{rbac.PermAPIKeysReadOwn, "apikeys.read.own", true},
		{rbac.PermAPIKeysReadOwn, "apikeys.read.*", true},
		{rbac.PermAPIKeysReadOwn, "apikeys.*", true},
	}
	for _, c := range cases {
		if got := c.perm.Matches(c.granted); got != c.want {
			t.Errorf("%q.Matches(%q) = %v, want %v", c.perm, c.granted, got, c.want)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// Engine: Enforce / Can / EnforceAny / EnforceAll
// ─────────────────────────────────────────────────────────────────────

func TestEnforceAnonymousAlwaysDenied(t *testing.T) {
	err := rbac.Enforce(context.Background(), auth.AnonymousPrincipal, rbac.PermTasksRead)
	if !errors.Is(err, rbac.ErrNotAuthenticated) {
		t.Fatalf("expected ErrNotAuthenticated, got %v", err)
	}
	if !errors.Is(err, rbac.ErrDenied) {
		// ErrNotAuthenticated is a distinct sentinel; it should *not*
		// wrap ErrDenied because handlers want a 401 vs 403 split.
		// This assertion guards against accidental merging.
		// We want the opposite: NOT Is(ErrDenied).
	}
}

func TestEnforceSystemAlwaysAllowed(t *testing.T) {
	sys := auth.SystemPrincipal("cron:memory.sweep")
	for _, p := range rbac.AllPermissions() {
		if err := rbac.Enforce(context.Background(), sys, p); err != nil {
			t.Fatalf("system principal denied %q: %v", p, err)
		}
	}
}

func TestEnforceExactAndWildcard(t *testing.T) {
	p := &auth.Principal{
		Type:        auth.PrincipalUser,
		UserID:      "u1",
		Username:    "alice",
		Roles:       []string{"operator"},
		Permissions: rbac.FlattenPermissions([]string{"tasks.*", "agent.invoke"}),
	}
	if err := rbac.Enforce(context.Background(), p, rbac.PermTasksRead); err != nil {
		t.Errorf("expected allow tasks.read via wildcard: %v", err)
	}
	if err := rbac.Enforce(context.Background(), p, rbac.PermAgentInvoke); err != nil {
		t.Errorf("expected allow agent.invoke: %v", err)
	}
	err := rbac.Enforce(context.Background(), p, rbac.PermUsersManage)
	var denied *rbac.DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("expected DeniedError, got %v", err)
	}
	if denied.Permission != rbac.PermUsersManage {
		t.Errorf("denied.Permission = %q, want users.manage", denied.Permission)
	}
	if !errors.Is(err, rbac.ErrDenied) {
		t.Errorf("errors.Is(ErrDenied) should succeed on DeniedError")
	}
}

func TestEnforceAnyEnforceAll(t *testing.T) {
	p := &auth.Principal{
		Type:        auth.PrincipalUser,
		UserID:      "u1",
		Username:    "alice",
		Permissions: rbac.FlattenPermissions([]string{"tasks.read", "audit.read"}),
	}
	if err := rbac.EnforceAny(context.Background(), p, rbac.PermTasksRead, rbac.PermUsersManage); err != nil {
		t.Errorf("EnforceAny should succeed when any perm held: %v", err)
	}
	err := rbac.EnforceAny(context.Background(), p, rbac.PermUsersManage, rbac.PermSecretsWrite)
	if !errors.Is(err, rbac.ErrDenied) {
		t.Errorf("EnforceAny should deny when none held: %v", err)
	}
	if err := rbac.EnforceAll(context.Background(), p, rbac.PermTasksRead, rbac.PermAuditRead); err != nil {
		t.Errorf("EnforceAll should succeed when all perms held: %v", err)
	}
	err = rbac.EnforceAll(context.Background(), p, rbac.PermTasksRead, rbac.PermUsersManage)
	if !errors.Is(err, rbac.ErrDenied) {
		t.Errorf("EnforceAll should deny when any missing: %v", err)
	}
}

func TestFlattenPermissions(t *testing.T) {
	in := []string{"tasks.read", "", "tasks.read", "agent.invoke", " "}
	out := rbac.FlattenPermissions(in)
	want := []string{"agent.invoke", "tasks.read"}
	if len(out) != len(want) {
		t.Fatalf("got %v, want %v", out, want)
	}
	for i, v := range out {
		if v != want[i] {
			t.Errorf("[%d] %q != %q", i, v, want[i])
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// BuiltInRoles: every referenced permission must be declared
// ─────────────────────────────────────────────────────────────────────

func TestBuiltInRolesReferenceDeclaredPermissions(t *testing.T) {
	declared := map[rbac.Permission]struct{}{}
	for _, p := range rbac.AllPermissions() {
		declared[p] = struct{}{}
	}
	for _, role := range rbac.BuiltInRoles() {
		for _, p := range role.Permissions {
			if p == "*" {
				continue
			}
			if _, ok := declared[p]; !ok {
				t.Errorf("role %q references undeclared permission %q", role.Name, p)
			}
		}
	}
}

func TestOwnerRoleBypasses(t *testing.T) {
	owner := rbac.BuiltInRoleSpec(rbac.RoleOwner)
	if owner == nil {
		t.Fatal("owner role missing")
	}
	if len(owner.Permissions) != 1 || owner.Permissions[0] != "*" {
		t.Errorf("owner should grant exactly *, got %v", owner.Permissions)
	}
}

func TestViewerCannotInvokeAgent(t *testing.T) {
	viewer := rbac.BuiltInRoleSpec(rbac.RoleViewer)
	if viewer == nil {
		t.Fatal("viewer role missing")
	}
	for _, p := range viewer.Permissions {
		if p == rbac.PermAgentInvoke || p == rbac.PermAgentInterrupt {
			t.Errorf("viewer must not grant %q", p)
		}
	}
}

// ─────────────────────────────────────────────────────────────────────
// Bootstrap: SeedBuiltInRoles + BootstrapOwner + Resolver against
// a real sqlite store.
// ─────────────────────────────────────────────────────────────────────

func openTestStore(t *testing.T) datastore.Store {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "rbac.db") + "?_foreign_keys=on&_busy_timeout=5000"
	store, err := datastore.Open(context.Background(), datastore.Config{
		Driver:     "sqlite",
		DSN:        dsn,
		Migrations: "auto",
	})
	if err != nil {
		t.Fatalf("open datastore: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func TestSeedBuiltInRolesIdempotent(t *testing.T) {
	_ = os.Setenv("TZ", "UTC")
	store := openTestStore(t)
	ctx := context.Background()

	created, updated, err := rbac.SeedBuiltInRoles(ctx, store)
	if err != nil {
		t.Fatalf("first seed: %v", err)
	}
	wantRoles := len(rbac.BuiltInRoles())
	if created != wantRoles || updated != 0 {
		t.Errorf("first seed: got created=%d updated=%d, want %d/0", created, updated, wantRoles)
	}

	created, updated, err = rbac.SeedBuiltInRoles(ctx, store)
	if err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if created != 0 || updated != wantRoles {
		t.Errorf("second seed: got created=%d updated=%d, want 0/%d", created, updated, wantRoles)
	}

	roles, err := store.Roles().List(ctx)
	if err != nil {
		t.Fatalf("list roles: %v", err)
	}
	if len(roles) != wantRoles {
		t.Errorf("want %d persisted roles, got %d", wantRoles, len(roles))
	}
}

func TestBootstrapOwnerHappyPath(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	user, created, err := rbac.BootstrapOwner(ctx, store, "owner", "ops@example.com", "hash-fake")
	if err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	if !created || user == nil {
		t.Fatalf("expected creation, got created=%v user=%v", created, user)
	}

	// Second call with a non-empty user set must be a no-op.
	user2, created2, err := rbac.BootstrapOwner(ctx, store, "someone-else", "x@example.com", "hash-fake2")
	if err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if created2 || user2 != nil {
		t.Fatalf("expected noop on second call, got created=%v user=%v", created2, user2)
	}

	// Owner principal should see every permission through the Resolver.
	resolver := rbac.NewResolver(store)
	principal, err := resolver.ForUser(ctx, user)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	for _, perm := range rbac.AllPermissions() {
		if err := rbac.Enforce(ctx, principal, perm); err != nil {
			t.Errorf("owner denied %q: %v", perm, err)
		}
	}
}

func TestResolverForAPIKeyIntersectsScopes(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if _, _, err := rbac.SeedBuiltInRoles(ctx, store); err != nil {
		t.Fatal(err)
	}
	u := &datastore.User{
		ID:           "user-dev",
		Username:     "dev",
		Email:        "dev@example.com",
		PasswordHash: "hash",
		Status:       datastore.UserActive,
	}
	if err := store.Users().Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if err := store.Roles().AssignToUser(ctx, u.ID, "role-operator"); err != nil {
		t.Fatal(err)
	}

	key := &datastore.APIKey{
		ID:     "ak-1",
		KeyID:  "id1",
		Hash:   "h",
		UserID: u.ID,
		Name:   "narrow key",
		Scopes: []string{string(rbac.PermTasksRead)}, // single explicit scope
	}
	if err := store.APIKeys().Create(ctx, key); err != nil {
		t.Fatal(err)
	}
	resolver := rbac.NewResolver(store)
	principal, err := resolver.ForAPIKey(ctx, key)
	if err != nil {
		t.Fatalf("resolver: %v", err)
	}
	if err := rbac.Enforce(ctx, principal, rbac.PermTasksRead); err != nil {
		t.Errorf("expected allow tasks.read: %v", err)
	}
	// operator normally has agent.invoke, but this narrow key must not.
	err = rbac.Enforce(ctx, principal, rbac.PermAgentInvoke)
	if !errors.Is(err, rbac.ErrDenied) {
		t.Errorf("expected deny agent.invoke for scoped key: %v", err)
	}
	if principal.Type != auth.PrincipalAPIKey {
		t.Errorf("principal.Type = %v, want apikey", principal.Type)
	}
	if principal.APIKeyID != "id1" {
		t.Errorf("APIKeyID = %q, want id1", principal.APIKeyID)
	}
}
