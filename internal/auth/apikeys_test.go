package auth_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"github.com/opsintelligence/opsintelligence/internal/datastore"
	_ "github.com/opsintelligence/opsintelligence/internal/datastore/drivers"
)

func openStore(t *testing.T) datastore.Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "auth.db") + "?_foreign_keys=on&_busy_timeout=5000"
	store, err := datastore.Open(context.Background(), datastore.Config{
		Driver:     "sqlite",
		DSN:        dsn,
		Migrations: "auto",
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

func seedUser(t *testing.T, store datastore.Store, id string) *datastore.User {
	t.Helper()
	u := &datastore.User{
		ID:           id,
		Username:     id,
		Email:        id + "@example.com",
		PasswordHash: "hash",
		Status:       datastore.UserActive,
	}
	if err := store.Users().Create(context.Background(), u); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return u
}

func TestGenerateAPIKeyShape(t *testing.T) {
	pt, err := auth.GenerateAPIKey("user-1", "ci", []string{"tasks.read"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if !strings.HasPrefix(pt.PlainToken, auth.APIKeyPrefix) {
		t.Errorf("token missing prefix: %q", pt.PlainToken)
	}
	keyID, secret, err := auth.ParseAPIKey(pt.PlainToken)
	if err != nil {
		t.Fatalf("ParseAPIKey: %v", err)
	}
	if keyID != pt.Record.KeyID {
		t.Errorf("keyID mismatch: %q vs %q", keyID, pt.Record.KeyID)
	}
	if len(secret) < 30 {
		t.Errorf("secret looks too short: %q", secret)
	}
	if pt.Record.Hash == "" || !strings.HasPrefix(pt.Record.Hash, "$argon2id$") {
		t.Errorf("hash not argon2id: %q", pt.Record.Hash)
	}
	if pt.Record.ID == "" {
		t.Errorf("Record.ID should be populated")
	}
}

func TestGenerateAPIKeyValidation(t *testing.T) {
	if _, err := auth.GenerateAPIKey("", "n", nil); err == nil {
		t.Errorf("empty userID must fail")
	}
	if _, err := auth.GenerateAPIKey("u", "", nil); err == nil {
		t.Errorf("empty name must fail")
	}
}

func TestParseAPIKeyRejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"opi_",
		"opi_abcd",
		"opi_abcd_",
		"opi_abcd_tooshort",
		"not-opi_abcdefgh_ssssssssssssssssssssssssssssss",
	}
	for _, c := range cases {
		if _, _, err := auth.ParseAPIKey(c); !errors.Is(err, auth.ErrInvalidAPIKey) {
			t.Errorf("ParseAPIKey(%q) = %v, want ErrInvalidAPIKey", c, err)
		}
	}
}

func TestVerifyAPIKeyRoundTrip(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	seedUser(t, store, "user-1")

	pt, err := auth.GenerateAPIKey("user-1", "ci", []string{"tasks.read"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.APIKeys().Create(ctx, pt.Record); err != nil {
		t.Fatal(err)
	}

	key, err := auth.VerifyAPIKey(ctx, store, pt.PlainToken)
	if err != nil {
		t.Fatalf("VerifyAPIKey: %v", err)
	}
	if key.ID != pt.Record.ID {
		t.Errorf("verified key.ID %q, want %q", key.ID, pt.Record.ID)
	}

	// Wrong secret → ErrInvalidAPIKey
	keyID, _, _ := auth.ParseAPIKey(pt.PlainToken)
	tampered := auth.APIKeyPrefix + keyID + "_" + strings.Repeat("a", 32)
	if _, err := auth.VerifyAPIKey(ctx, store, tampered); !errors.Is(err, auth.ErrInvalidAPIKey) {
		t.Errorf("tampered verify = %v, want ErrInvalidAPIKey", err)
	}

	// Revoke and re-verify
	if err := store.APIKeys().Revoke(ctx, pt.Record.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.VerifyAPIKey(ctx, store, pt.PlainToken); !errors.Is(err, auth.ErrInvalidAPIKey) {
		t.Errorf("revoked verify = %v, want ErrInvalidAPIKey", err)
	}
}

func TestVerifyAPIKeyExpired(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	seedUser(t, store, "u")
	pt, err := auth.GenerateAPIKey("u", "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-time.Hour).UTC()
	pt.Record.ExpiresAt = &past
	if err := store.APIKeys().Create(ctx, pt.Record); err != nil {
		t.Fatal(err)
	}
	_, err = auth.VerifyAPIKey(ctx, store, pt.PlainToken)
	if !errors.Is(err, auth.ErrInvalidAPIKey) {
		t.Errorf("expired key = %v, want ErrInvalidAPIKey", err)
	}
}

func TestMaskAPIKey(t *testing.T) {
	pt, err := auth.GenerateAPIKey("u", "k", nil)
	if err != nil {
		t.Fatal(err)
	}
	masked := auth.MaskAPIKey(pt.PlainToken)
	if !strings.HasSuffix(masked, "_***") {
		t.Errorf("masked token should end with _***: %q", masked)
	}
	if strings.Contains(masked, strings.TrimPrefix(pt.PlainToken, auth.APIKeyPrefix+pt.Record.KeyID+"_")) {
		t.Errorf("masked token leaked secret: %q", masked)
	}
	if auth.MaskAPIKey("garbage") != "***" {
		t.Errorf("garbage input should mask to ***")
	}
}
