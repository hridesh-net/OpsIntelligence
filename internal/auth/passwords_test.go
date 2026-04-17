package auth_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/opsintelligence/opsintelligence/internal/auth"
	"golang.org/x/crypto/bcrypt"
)

func TestHashAndVerifyArgon2id(t *testing.T) {
	pw := "correct horse battery staple"
	hash, err := auth.HashPassword(pw, &auth.Argon2Params{
		// Lower cost for the test so the suite stays fast without
		// losing the argon2id code path coverage.
		TimeCost:    1,
		MemoryKiB:   8 * 1024,
		Parallelism: 1,
	})
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$") {
		t.Errorf("hash missing argon2id prefix: %q", hash)
	}
	if err := auth.VerifyPassword(hash, pw); err != nil {
		t.Errorf("VerifyPassword(correct) = %v, want nil", err)
	}
	err = auth.VerifyPassword(hash, "wrong")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("VerifyPassword(wrong) = %v, want ErrInvalidCredentials", err)
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := auth.HashPassword("", nil); err == nil {
		t.Errorf("HashPassword(\"\") should fail")
	}
}

func TestHashPasswordUniqueSalts(t *testing.T) {
	p := &auth.Argon2Params{TimeCost: 1, MemoryKiB: 8 * 1024, Parallelism: 1}
	a, err := auth.HashPassword("same", p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := auth.HashPassword("same", p)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("two hashes of the same password must differ (salt)")
	}
}

func TestVerifyPasswordBcryptInterop(t *testing.T) {
	h, err := bcrypt.GenerateFromPassword([]byte("legacy-pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	if err := auth.VerifyPassword(string(h), "legacy-pw"); err != nil {
		t.Errorf("bcrypt verify: %v", err)
	}
	err = auth.VerifyPassword(string(h), "nope")
	if !errors.Is(err, auth.ErrInvalidCredentials) {
		t.Errorf("bcrypt wrong pw = %v, want ErrInvalidCredentials", err)
	}
	if !auth.NeedsRehash(string(h)) {
		t.Errorf("bcrypt hash should NeedsRehash == true")
	}
}

func TestVerifyPasswordMalformed(t *testing.T) {
	cases := []string{
		"",
		"plain",
		"$argon2id$",
		"$argon2id$v=99$m=1,t=1,p=1$aa$bb",
	}
	for _, h := range cases {
		err := auth.VerifyPassword(h, "whatever")
		if !errors.Is(err, auth.ErrMalformedHash) {
			t.Errorf("VerifyPassword(%q) = %v, want ErrMalformedHash", h, err)
		}
	}
}

func TestNeedsRehashOnWeakerParams(t *testing.T) {
	weak, err := auth.HashPassword("pw", &auth.Argon2Params{TimeCost: 1, MemoryKiB: 1024, Parallelism: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !auth.NeedsRehash(weak) {
		t.Errorf("weak params should NeedsRehash")
	}
	strong, err := auth.HashPassword("pw", nil) // defaults
	if err != nil {
		t.Fatal(err)
	}
	if auth.NeedsRehash(strong) {
		t.Errorf("default params should NOT NeedsRehash, got hash=%q", strong)
	}
}

func TestRandomToken(t *testing.T) {
	a, err := auth.RandomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	b, err := auth.RandomToken(32)
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("RandomToken collision: %q", a)
	}
	if len(a) < 40 {
		t.Errorf("32-byte token produced a %d-char string, expected ~43", len(a))
	}
	if _, err := auth.RandomToken(0); err == nil {
		t.Errorf("RandomToken(0) must fail")
	}
}

func TestConstantTimeEqual(t *testing.T) {
	if !auth.ConstantTimeEqual("abc", "abc") {
		t.Errorf("equal strings should match")
	}
	if auth.ConstantTimeEqual("abc", "abd") {
		t.Errorf("different strings should not match")
	}
	if auth.ConstantTimeEqual("abc", "ab") {
		t.Errorf("different-length strings should not match")
	}
}
