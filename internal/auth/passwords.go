package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

// ErrInvalidCredentials is the caller-safe sentinel for "password did
// not verify". Handlers translate it into a 401 without leaking which
// of {wrong user, wrong password, disabled account} was the cause.
var ErrInvalidCredentials = errors.New("auth: invalid credentials")

// ErrMalformedHash is returned by VerifyPassword when the stored hash
// cannot be parsed (corrupt row, truncated value, wrong algorithm).
// Distinct from ErrInvalidCredentials because it indicates a data
// problem, not an auth attempt; the caller should log loudly.
var ErrMalformedHash = errors.New("auth: malformed password hash")

// ─────────────────────────────────────────────────────────────────────
// Default argon2id parameters.
//
// Targets a ~50 ms hash on a 2024-era laptop and ~64 MiB RAM. Cloud
// deployments may tune these down via AuthConfig without changing the
// on-disk format — each hash records its own parameters.
// ─────────────────────────────────────────────────────────────────────

// Argon2Params tunes an argon2id hash. Zero values fall back to the
// defaults in DefaultArgon2Params.
type Argon2Params struct {
	TimeCost    uint32 // iterations; argon2 minimum 1
	MemoryKiB   uint32 // RAM per hash
	Parallelism uint8  // lanes; bounded by runtime.NumCPU
	SaltLen     uint32 // bytes
	KeyLen      uint32 // output bytes
}

// DefaultArgon2Params returns the conservative defaults used by
// HashPassword when no explicit params are passed in. Keeping them in
// a constructor (not a package-level var) ensures later tuning does
// not accidentally mutate older hashes.
func DefaultArgon2Params() Argon2Params {
	p := uint8(2)
	if cpu := runtime.NumCPU(); cpu > 0 && uint8(cpu) < p {
		p = uint8(cpu)
	}
	return Argon2Params{
		TimeCost:    3,
		MemoryKiB:   64 * 1024, // 64 MiB
		Parallelism: p,
		SaltLen:     16,
		KeyLen:      32,
	}
}

// HashPassword hashes plaintext with argon2id and returns a PHC-style
// string safe to persist in User.PasswordHash. The returned value
// records its own parameters so a future parameter bump does not break
// existing rows — VerifyPassword reads them back out.
//
// Passing nil params means "use defaults". params.SaltLen and
// params.KeyLen fall back to defaults when zero so callers only need
// to override the cost knobs.
func HashPassword(plaintext string, params *Argon2Params) (string, error) {
	if plaintext == "" {
		return "", errors.New("auth: cannot hash empty password")
	}
	p := DefaultArgon2Params()
	if params != nil {
		if params.TimeCost > 0 {
			p.TimeCost = params.TimeCost
		}
		if params.MemoryKiB > 0 {
			p.MemoryKiB = params.MemoryKiB
		}
		if params.Parallelism > 0 {
			p.Parallelism = params.Parallelism
		}
		if params.SaltLen > 0 {
			p.SaltLen = params.SaltLen
		}
		if params.KeyLen > 0 {
			p.KeyLen = params.KeyLen
		}
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read salt: %w", err)
	}
	key := argon2.IDKey([]byte(plaintext), salt, p.TimeCost, p.MemoryKiB, p.Parallelism, p.KeyLen)

	// PHC-compatible envelope, identical shape to github.com/alexedwards/argon2id
	// so future migrations to an external library are painless.
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		p.MemoryKiB, p.TimeCost, p.Parallelism,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword returns nil on match, ErrInvalidCredentials on
// mismatch, ErrMalformedHash on parse error. Runs in constant time
// against the actual hash bytes to avoid timing oracles.
//
// Supports both argon2id (our default, "$argon2id$...") and bcrypt
// ("$2a$.../$2b$.../$2y$...") so operators can import legacy systems
// without breaking existing users — NewHashPasswordForLogin should be
// called after a successful legacy verify to upgrade the row.
func VerifyPassword(hash, plaintext string) error {
	switch {
	case strings.HasPrefix(hash, "$argon2id$"):
		return verifyArgon2id(hash, plaintext)
	case strings.HasPrefix(hash, "$2a$"), strings.HasPrefix(hash, "$2b$"), strings.HasPrefix(hash, "$2y$"):
		if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(plaintext)); err != nil {
			if errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
				return ErrInvalidCredentials
			}
			return fmt.Errorf("%w: %v", ErrMalformedHash, err)
		}
		return nil
	default:
		return ErrMalformedHash
	}
}

// NeedsRehash reports whether the stored hash was produced with older
// / weaker params than the current defaults, so the caller can silently
// upgrade the row after a successful verify. Always true for bcrypt
// (we standardise on argon2id for new hashes).
func NeedsRehash(hash string) bool {
	if !strings.HasPrefix(hash, "$argon2id$") {
		return true
	}
	params, _, _, err := parseArgon2id(hash)
	if err != nil {
		return true
	}
	def := DefaultArgon2Params()
	return params.TimeCost < def.TimeCost ||
		params.MemoryKiB < def.MemoryKiB ||
		params.Parallelism < def.Parallelism
}

func verifyArgon2id(hash, plaintext string) error {
	params, salt, digest, err := parseArgon2id(hash)
	if err != nil {
		return err
	}
	got := argon2.IDKey([]byte(plaintext), salt, params.TimeCost, params.MemoryKiB, params.Parallelism, uint32(len(digest)))
	if subtle.ConstantTimeCompare(got, digest) == 1 {
		return nil
	}
	return ErrInvalidCredentials
}

func parseArgon2id(hash string) (Argon2Params, []byte, []byte, error) {
	// $argon2id$v=19$m=65536,t=3,p=2$<salt>$<digest>
	parts := strings.Split(hash, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return Argon2Params{}, nil, nil, ErrMalformedHash
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return Argon2Params{}, nil, nil, fmt.Errorf("%w: version: %v", ErrMalformedHash, err)
	}
	if version != argon2.Version {
		return Argon2Params{}, nil, nil, fmt.Errorf("%w: unsupported argon2 version %d", ErrMalformedHash, version)
	}
	var memory, timeCost uint64
	var parallelism uint64
	for _, kv := range strings.Split(parts[3], ",") {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			return Argon2Params{}, nil, nil, fmt.Errorf("%w: param %q", ErrMalformedHash, kv)
		}
		key, val := kv[:eq], kv[eq+1:]
		n, err := strconv.ParseUint(val, 10, 32)
		if err != nil {
			return Argon2Params{}, nil, nil, fmt.Errorf("%w: param %q value: %v", ErrMalformedHash, key, err)
		}
		switch key {
		case "m":
			memory = n
		case "t":
			timeCost = n
		case "p":
			parallelism = n
		}
	}
	if memory == 0 || timeCost == 0 || parallelism == 0 {
		return Argon2Params{}, nil, nil, fmt.Errorf("%w: missing cost params", ErrMalformedHash)
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return Argon2Params{}, nil, nil, fmt.Errorf("%w: salt: %v", ErrMalformedHash, err)
	}
	digest, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return Argon2Params{}, nil, nil, fmt.Errorf("%w: digest: %v", ErrMalformedHash, err)
	}
	return Argon2Params{
		TimeCost:    uint32(timeCost),
		MemoryKiB:   uint32(memory),
		Parallelism: uint8(parallelism),
		SaltLen:     uint32(len(salt)),
		KeyLen:      uint32(len(digest)),
	}, salt, digest, nil
}

// ─────────────────────────────────────────────────────────────────────
// Random token helpers
// ─────────────────────────────────────────────────────────────────────

// RandomToken returns a cryptographically-random URL-safe base64
// string of the given byte length. Panics only if crypto/rand itself
// fails — that is a fatal system condition, not a user error.
func RandomToken(nBytes int) (string, error) {
	if nBytes <= 0 {
		return "", errors.New("auth: RandomToken length must be > 0")
	}
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: rand read: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// ConstantTimeEqual is a convenience around subtle.ConstantTimeCompare
// for string comparisons (session/CSRF tokens).
func ConstantTimeEqual(a, b string) bool {
	if len(a) != len(b) {
		// constant time in length is fine: callers should have normalised
		// token widths before calling this (we always do).
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
