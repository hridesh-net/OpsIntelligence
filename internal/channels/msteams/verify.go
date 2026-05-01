package msteams

// verify.go implements Bot Framework JWT authentication as specified by Microsoft:
// https://learn.microsoft.com/en-us/azure/bot-service/rest-api/bot-framework-rest-connector-authentication
//
// Flow:
//  1. Fetch OpenID metadata from botFrameworkOpenIDMetaURL to discover jwks_uri.
//  2. Fetch and cache RSA public keys from jwks_uri (TTL: 24 h, stale-on-error).
//  3. Verify inbound Bearer JWT: RS256 signature + iss/aud/exp/nbf claims.

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	botFrameworkOpenIDMetaURL = "https://login.botframework.com/v1/.well-known/openidconfiguration"
	botFrameworkIssuer        = "https://api.botframework.com"
	jwksCacheDuration         = 24 * time.Hour
	clockLeeway               = 5 * time.Minute
)

// jwksCache fetches and caches RSA public keys from the Bot Framework JWKS endpoint.
// It is safe for concurrent use and serves stale keys on transient fetch errors.
type jwksCache struct {
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey // kid → key
	fetchedAt time.Time
	metaURL   string // overridable for testing
	client    *http.Client
}

// defaultJWKSCache is the shared cache used by all Channel instances
// that don't inject their own (production default).
var defaultJWKSCache = &jwksCache{
	metaURL: botFrameworkOpenIDMetaURL,
	client:  &http.Client{Timeout: 10 * time.Second},
}

type openIDConfig struct {
	JWKSURI string `json:"jwks_uri"`
}

type jwkSet struct {
	Keys []jwk `json:"keys"`
}

type jwk struct {
	Kty string   `json:"kty"`
	Kid string   `json:"kid"`
	N   string   `json:"n"`
	E   string   `json:"e"`
	X5c []string `json:"x5c"` // optional DER-encoded cert chain (base64 not base64url)
}

// getKey returns the RSA public key for kid, refreshing the cache when stale or key missing.
func (c *jwksCache) getKey(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	key, ok := c.keys[kid]
	fresh := time.Since(c.fetchedAt) <= jwksCacheDuration
	c.mu.RUnlock()

	if ok && fresh {
		return key, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	// Re-check after acquiring write lock.
	if key, ok := c.keys[kid]; ok && time.Since(c.fetchedAt) <= jwksCacheDuration {
		return key, nil
	}

	keys, err := c.fetchKeys()
	if err != nil {
		// Serve stale on error rather than hard-failing.
		if c.keys != nil {
			if key, ok := c.keys[kid]; ok {
				return key, nil
			}
		}
		return nil, fmt.Errorf("fetch JWKS: %w", err)
	}

	c.keys = keys
	c.fetchedAt = time.Now()

	key, ok = keys[kid]
	if !ok {
		return nil, fmt.Errorf("kid %q not found in Bot Framework JWKS", kid)
	}
	return key, nil
}

func (c *jwksCache) fetchKeys() (map[string]*rsa.PublicKey, error) {
	// Step 1: fetch OpenID metadata to get jwks_uri.
	resp, err := c.client.Get(c.metaURL)
	if err != nil {
		return nil, fmt.Errorf("fetch OpenID metadata: %w", err)
	}
	defer resp.Body.Close()
	metaBody, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))

	var meta openIDConfig
	if err := json.Unmarshal(metaBody, &meta); err != nil {
		return nil, fmt.Errorf("parse OpenID metadata: %w", err)
	}
	if meta.JWKSURI == "" {
		return nil, fmt.Errorf("empty jwks_uri in OpenID metadata")
	}

	// Step 2: fetch the JWKS.
	resp2, err := c.client.Get(meta.JWKSURI)
	if err != nil {
		return nil, fmt.Errorf("fetch JWKS from %s: %w", meta.JWKSURI, err)
	}
	defer resp2.Body.Close()
	jwksBody, _ := io.ReadAll(io.LimitReader(resp2.Body, 256*1024))

	var set jwkSet
	if err := json.Unmarshal(jwksBody, &set); err != nil {
		return nil, fmt.Errorf("parse JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if !strings.EqualFold(k.Kty, "RSA") || k.Kid == "" {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k)
		if err != nil {
			continue // skip malformed keys, don't fail the entire set
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid RSA keys found in JWKS")
	}
	return keys, nil
}

// rsaPublicKeyFromJWK reconstructs an RSA public key from a JWK.
// x5c (certificate chain) is preferred over n/e reconstruction.
func rsaPublicKeyFromJWK(k jwk) (*rsa.PublicKey, error) {
	if len(k.X5c) > 0 {
		der, err := base64.StdEncoding.DecodeString(k.X5c[0])
		if err != nil {
			return nil, fmt.Errorf("decode x5c: %w", err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("parse x5c certificate: %w", err)
		}
		pub, ok := cert.PublicKey.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("x5c key is not RSA")
		}
		return pub, nil
	}

	if k.N == "" || k.E == "" {
		return nil, fmt.Errorf("JWK missing n or e")
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode JWK n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode JWK e: %w", err)
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(new(big.Int).SetBytes(eBytes).Int64()),
	}, nil
}

// verifyInboundJWT validates the Bot Framework bearer token on an inbound request.
// Returns nil on success. Returns an error if the token is missing, malformed,
// expired, or carries an invalid signature.
//
// The channel must have a non-nil jwtCache; when jwtCache is nil the call is a no-op
// (used for local Bot Framework Emulator testing where JWT auth is disabled).
func (c *Channel) verifyInboundJWT(r *http.Request) error {
	if c.jwtCache == nil {
		return nil
	}

	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return fmt.Errorf("msteams: missing or malformed Authorization header")
	}
	rawToken := strings.TrimPrefix(auth, "Bearer ")

	parts := strings.SplitN(rawToken, ".", 3)
	if len(parts) != 3 {
		return fmt.Errorf("msteams: JWT must have 3 parts, got %d", len(parts))
	}

	// --- Header ---
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return fmt.Errorf("msteams: decode JWT header: %w", err)
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return fmt.Errorf("msteams: parse JWT header: %w", err)
	}
	if !strings.EqualFold(header.Alg, "RS256") {
		return fmt.Errorf("msteams: unsupported JWT algorithm %q (expected RS256)", header.Alg)
	}
	if header.Kid == "" {
		return fmt.Errorf("msteams: JWT header missing kid")
	}

	// --- Claims ---
	claimsJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return fmt.Errorf("msteams: decode JWT claims: %w", err)
	}
	var claims struct {
		Iss string          `json:"iss"`
		Aud json.RawMessage `json:"aud"` // string or []string
		Exp int64           `json:"exp"`
		Nbf int64           `json:"nbf"`
	}
	if err := json.Unmarshal(claimsJSON, &claims); err != nil {
		return fmt.Errorf("msteams: parse JWT claims: %w", err)
	}

	if claims.Iss != botFrameworkIssuer {
		return fmt.Errorf("msteams: unexpected JWT issuer %q", claims.Iss)
	}

	if err := c.checkAudClaim(claims.Aud); err != nil {
		return err
	}

	now := time.Now()
	if claims.Exp > 0 && now.Unix() > claims.Exp+int64(clockLeeway.Seconds()) {
		return fmt.Errorf("msteams: JWT expired")
	}
	if claims.Nbf > 0 && now.Unix() < claims.Nbf-int64(clockLeeway.Seconds()) {
		return fmt.Errorf("msteams: JWT not yet valid")
	}

	// --- Signature ---
	pubKey, err := c.jwtCache.getKey(header.Kid)
	if err != nil {
		return fmt.Errorf("msteams: get signing key: %w", err)
	}

	signingInput := parts[0] + "." + parts[1]
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return fmt.Errorf("msteams: decode JWT signature: %w", err)
	}

	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest[:], sigBytes); err != nil {
		return fmt.Errorf("msteams: JWT signature invalid: %w", err)
	}

	return nil
}

// checkAudClaim validates the aud claim (string or []string) against the bot's appID.
func (c *Channel) checkAudClaim(raw json.RawMessage) error {
	if len(raw) == 0 {
		return fmt.Errorf("msteams: JWT missing aud claim")
	}

	// Try string first.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == c.appID {
			return nil
		}
		return fmt.Errorf("msteams: JWT audience %q does not match app_id %q", s, c.appID)
	}

	// Try []string.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return fmt.Errorf("msteams: JWT aud claim is neither string nor array")
	}
	for _, a := range arr {
		if a == c.appID {
			return nil
		}
	}
	return fmt.Errorf("msteams: JWT audience array does not contain app_id %q", c.appID)
}
