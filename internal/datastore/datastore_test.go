package datastore_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/datastore"
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/drivers"
)

// openTestStore opens a fresh SQLite store under a temp dir. Postgres
// is covered by the TestPostgres_* block below, gated on an env var
// so CI that doesn't ship Postgres still passes cleanly.
func openTestStore(t *testing.T) datastore.Store {
	t.Helper()
	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "ops.db") + "?_foreign_keys=on"
	store, err := datastore.Open(context.Background(), datastore.Config{
		Driver:     datastore.DriverSQLite,
		DSN:        dsn,
		Migrations: "auto",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestOpen_UnknownDriver(t *testing.T) {
	_, err := datastore.Open(context.Background(), datastore.Config{Driver: "mysql", DSN: "x"})
	if !errors.Is(err, datastore.ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
}

func TestOpen_EmptyDSN(t *testing.T) {
	_, err := datastore.Open(context.Background(), datastore.Config{Driver: datastore.DriverSQLite, DSN: ""})
	if !errors.Is(err, datastore.ErrInvalidConfig) {
		t.Fatalf("want ErrInvalidConfig, got %v", err)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	applied, latest, err := store.MigrationStatus(ctx)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if applied != latest || applied == 0 {
		t.Fatalf("want applied==latest>0, got applied=%d latest=%d", applied, latest)
	}
}

func TestUsers_CRUD(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	u := &datastore.User{
		ID:           "u-1",
		Username:     "alice",
		Email:        "alice@example.com",
		PasswordHash: "$argon2id$...",
	}
	if err := store.Users().Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.Users().GetByUsername(ctx, "alice")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "u-1" || got.Email != "alice@example.com" {
		t.Fatalf("unexpected user: %+v", got)
	}
	if err := store.Users().RecordLogin(ctx, "u-1"); err != nil {
		t.Fatalf("record login: %v", err)
	}
	got, _ = store.Users().Get(ctx, "u-1")
	if got.LastLoginAt == nil {
		t.Fatal("expected last_login_at to be set")
	}

	if err := store.Users().Create(ctx, u); !errors.Is(err, datastore.ErrConflict) {
		t.Fatalf("want ErrConflict on duplicate username, got %v", err)
	}
}

func TestUsers_GetNotFound(t *testing.T) {
	store := openTestStore(t)
	_, err := store.Users().Get(context.Background(), "ghost")
	if !errors.Is(err, datastore.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestRoles_WithPermissions(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()

	user := &datastore.User{ID: "u-1", Username: "alice", Status: datastore.UserActive}
	if err := store.Users().Create(ctx, user); err != nil {
		t.Fatalf("create user: %v", err)
	}
	admin := &datastore.Role{ID: "r-admin", Name: "admin", IsBuiltIn: true}
	if err := store.Roles().Create(ctx, admin); err != nil {
		t.Fatalf("create role: %v", err)
	}
	perms := []datastore.Permission{"tasks.read", "tasks.cancel", "users.manage"}
	if err := store.Roles().SetPermissions(ctx, "r-admin", perms); err != nil {
		t.Fatalf("set perms: %v", err)
	}
	// Re-set idempotency.
	if err := store.Roles().SetPermissions(ctx, "r-admin", perms); err != nil {
		t.Fatalf("set perms again: %v", err)
	}
	if err := store.Roles().AssignToUser(ctx, "u-1", "r-admin"); err != nil {
		t.Fatalf("assign: %v", err)
	}
	userPerms, err := store.Roles().ListPermissionsForUser(ctx, "u-1")
	if err != nil {
		t.Fatalf("list perms for user: %v", err)
	}
	if len(userPerms) != 3 {
		t.Fatalf("want 3 perms for user, got %d (%v)", len(userPerms), userPerms)
	}
	roles, err := store.Roles().ListRolesForUser(ctx, "u-1")
	if err != nil {
		t.Fatalf("list roles for user: %v", err)
	}
	if len(roles) != 1 || roles[0].Name != "admin" {
		t.Fatalf("unexpected roles: %+v", roles)
	}
}

func TestAPIKeys_LifecycleAndList(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.Users().Create(ctx, &datastore.User{ID: "u-1", Username: "alice"}); err != nil {
		t.Fatalf("user: %v", err)
	}
	k := &datastore.APIKey{
		ID:     "k-1",
		KeyID:  "abcd1234",
		Hash:   "$argon2id$...",
		UserID: "u-1",
		Name:   "ci-token",
		Scopes: []string{"tasks.read", "webhooks.read"},
	}
	if err := store.APIKeys().Create(ctx, k); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.APIKeys().GetByKeyID(ctx, "abcd1234")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Scopes) != 2 {
		t.Fatalf("scopes lost: %+v", got.Scopes)
	}
	if err := store.APIKeys().TouchUsage(ctx, "k-1"); err != nil {
		t.Fatalf("touch: %v", err)
	}
	if err := store.APIKeys().Revoke(ctx, "k-1"); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got, _ = store.APIKeys().GetByKeyID(ctx, "abcd1234")
	if got.RevokedAt == nil {
		t.Fatal("expected revoked_at to be set")
	}
	list, err := store.APIKeys().ListForUser(ctx, "u-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 key, got %d", len(list))
	}
}

func TestSessions_AndExpiry(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	if err := store.Users().Create(ctx, &datastore.User{ID: "u-1", Username: "alice"}); err != nil {
		t.Fatalf("user: %v", err)
	}
	live := &datastore.Session{
		ID:        "s-live",
		UserID:    "u-1",
		ExpiresAt: time.Now().Add(time.Hour),
	}
	stale := &datastore.Session{
		ID:        "s-stale",
		UserID:    "u-1",
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	for _, s := range []*datastore.Session{live, stale} {
		if err := store.Sessions().Create(ctx, s); err != nil {
			t.Fatalf("create %s: %v", s.ID, err)
		}
	}
	n, err := store.Sessions().DeleteExpired(ctx)
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 expired session removed, got %d", n)
	}
	if _, err := store.Sessions().Get(ctx, "s-stale"); !errors.Is(err, datastore.ErrNotFound) {
		t.Fatalf("want ErrNotFound for stale, got %v", err)
	}
	if err := store.Sessions().Touch(ctx, "s-live"); err != nil {
		t.Fatalf("touch: %v", err)
	}
}

func TestAudit_AppendAndFilter(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Second)
	base := datastore.AuditEntry{
		ActorType: datastore.ActorUser,
		ActorID:   "u-1",
		Action:    "tasks.cancel",
		Success:   true,
	}
	for i := 0; i < 3; i++ {
		e := base
		e.Timestamp = time.Now().UTC()
		if err := store.Audit().Append(ctx, &e); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	// one non-matching row
	other := datastore.AuditEntry{ActorType: datastore.ActorSystem, Action: "skills.install", Success: true}
	if err := store.Audit().Append(ctx, &other); err != nil {
		t.Fatalf("append other: %v", err)
	}
	yes := true
	list, err := store.Audit().List(ctx, datastore.AuditFilter{
		Since:   &start,
		Action:  "tasks.",
		Success: &yes,
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("want 3 rows, got %d", len(list))
	}
	n, err := store.Audit().Count(ctx, datastore.AuditFilter{ActorType: datastore.ActorSystem})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Fatalf("want 1 system row, got %d", n)
	}
}

func TestTaskHistory_UpsertAndEvents(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	th := &datastore.TaskHistory{
		ID:        "th-1",
		TaskID:    "task-42",
		Goal:      "run pr review",
		Status:    "running",
		StartedAt: &now,
		ActorID:   "u-1",
	}
	if err := store.TaskHistory().Upsert(ctx, th); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Update status.
	th.Status = "completed"
	done := time.Now().UTC()
	th.CompletedAt = &done
	if err := store.TaskHistory().Upsert(ctx, th); err != nil {
		t.Fatalf("upsert 2: %v", err)
	}
	got, err := store.TaskHistory().Get(ctx, "task-42")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != "completed" || got.CompletedAt == nil {
		t.Fatalf("status not updated: %+v", got)
	}
	// Events.
	if err := store.TaskHistory().AppendEvent(ctx, &datastore.TaskHistoryEvent{
		TaskID: "task-42", Index: 0, Kind: "progress", Message: "starting",
	}); err != nil {
		t.Fatalf("append event: %v", err)
	}
	if err := store.TaskHistory().AppendEvent(ctx, &datastore.TaskHistoryEvent{
		TaskID: "task-42", Index: 1, Kind: "progress", Message: "halfway",
		Metadata: map[string]any{"pct": 50},
	}); err != nil {
		t.Fatalf("append event 2: %v", err)
	}
	evs, err := store.TaskHistory().ListEvents(ctx, "task-42", -1, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("want 2 events, got %d", len(evs))
	}
	if evs[1].Metadata["pct"] != float64(50) {
		t.Fatalf("metadata round-trip lost: %+v", evs[1].Metadata)
	}
}

func TestOIDCState_TakeOnceAndExpiry(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	st := &datastore.OIDCState{
		State:     "abc",
		Nonce:     "n",
		ExpiresAt: time.Now().Add(time.Minute),
	}
	if err := store.OIDCState().Put(ctx, st); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := store.OIDCState().Take(ctx, "abc")
	if err != nil {
		t.Fatalf("take: %v", err)
	}
	if got.State != "abc" {
		t.Fatalf("state round-trip: %+v", got)
	}
	if _, err := store.OIDCState().Take(ctx, "abc"); !errors.Is(err, datastore.ErrNotFound) {
		t.Fatalf("want ErrNotFound on second take, got %v", err)
	}

	expired := &datastore.OIDCState{
		State:     "old",
		Nonce:     "n",
		ExpiresAt: time.Now().Add(-time.Minute),
	}
	_ = store.OIDCState().Put(ctx, expired)
	_, err = store.OIDCState().Take(ctx, "old")
	if !errors.Is(err, datastore.ErrExpired) {
		t.Fatalf("want ErrExpired, got %v", err)
	}
}

// TestPostgres_Smoke is gated by OPSINTELLIGENCE_TEST_POSTGRES_DSN so CI
// without a Postgres instance stays green. Run locally with
//   OPSINTELLIGENCE_TEST_POSTGRES_DSN='postgres://localhost/opsi_test?sslmode=disable' \
//     go test ./internal/datastore/...
func TestPostgres_Smoke(t *testing.T) {
	dsn := os.Getenv("OPSINTELLIGENCE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("set OPSINTELLIGENCE_TEST_POSTGRES_DSN to run this test")
	}
	store, err := datastore.Open(context.Background(), datastore.Config{
		Driver:     datastore.DriverPostgres,
		DSN:        dsn,
		Migrations: "auto",
	})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	if err := store.Users().Create(ctx, &datastore.User{ID: "u-pg-1", Username: "pg-alice"}); err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = store.Users().Delete(ctx, "u-pg-1") }()

	got, err := store.Users().GetByUsername(ctx, "pg-alice")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Username != "pg-alice" {
		t.Fatalf("round-trip lost: %+v", got)
	}
}
