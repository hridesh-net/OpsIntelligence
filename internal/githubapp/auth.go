package githubapp

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// cachedToken is an installation access token with its expiry.
type cachedToken struct {
	token     string
	expiresAt time.Time
}

// AppClient authenticates as the GitHub App and fetches per-installation
// access tokens. Tokens are cached for their lifetime minus a 5-minute
// safety margin to avoid using expired tokens.
type AppClient struct {
	appID      int64
	privateKey *rsa.PrivateKey
	apiURL     string

	mu    sync.Mutex
	cache map[int64]*cachedToken
}

// LoadPrivateKey reads the RSA private key from a PEM file path or inline
// PEM string. path takes precedence over inline.
func LoadPrivateKey(path, inline string) (*rsa.PrivateKey, error) {
	var pemData []byte
	if path != "" {
		var err error
		pemData, err = os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("githubapp: read private key %q: %w", path, err)
		}
	} else if inline != "" {
		pemData = []byte(inline)
	} else {
		return nil, errors.New("githubapp: no private key configured (set private_key_path or private_key_pem)")
	}

	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, errors.New("githubapp: failed to decode PEM block from private key")
	}

	// GitHub generates PKCS#1 RSA keys; try PKCS#8 as a fallback.
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	iface, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("githubapp: parse private key: %w", err)
	}
	key, ok := iface.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("githubapp: private key is not RSA")
	}
	return key, nil
}

// NewAppClient constructs an AppClient. apiURL defaults to https://api.github.com.
func NewAppClient(appID int64, key *rsa.PrivateKey, apiURL string) *AppClient {
	if apiURL == "" {
		apiURL = "https://api.github.com"
	}
	return &AppClient{
		appID:      appID,
		privateKey: key,
		apiURL:     apiURL,
		cache:      make(map[int64]*cachedToken),
	}
}

// newJWT mints a short-lived App-level JWT used to authenticate App API calls.
// GitHub requires iat ≤ now and exp ≤ now+10m. We backdate iat by 60s to
// account for small clock skews between this host and GitHub.
func (c *AppClient) newJWT() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"iat": now.Add(-60 * time.Second).Unix(),
		"exp": now.Add(9 * time.Minute).Unix(),
		"iss": fmt.Sprintf("%d", c.appID),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return tok.SignedString(c.privateKey)
}

// InstallationToken returns a valid installation access token for the given
// installation_id. The token is cached and refreshed automatically.
func (c *AppClient) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	c.mu.Lock()
	cached := c.cache[installationID]
	c.mu.Unlock()

	if cached != nil && time.Until(cached.expiresAt) > 5*time.Minute {
		return cached.token, nil
	}

	tok, err := c.fetchInstallationToken(ctx, installationID)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.cache[installationID] = tok
	c.mu.Unlock()
	return tok.token, nil
}

// VerifyInstallation calls the GitHub API to confirm the given installation_id
// belongs to this App and returns the account login. Used by the setup handler
// to ensure the caller actually installed the App (not just guessing IDs).
func (c *AppClient) VerifyInstallation(ctx context.Context, installationID int64) (login, accountType string, err error) {
	appJWT, err := c.newJWT()
	if err != nil {
		return "", "", fmt.Errorf("githubapp: sign JWT: %w", err)
	}
	url := fmt.Sprintf("%s/app/installations/%d", c.apiURL, installationID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("githubapp: verify installation: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return "", "", errors.New("githubapp: installation not found")
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("githubapp: verify installation status %d: %s", resp.StatusCode, body)
	}
	var result struct {
		Account struct {
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", "", fmt.Errorf("githubapp: decode installation: %w", err)
	}
	return result.Account.Login, result.Account.Type, nil
}

func (c *AppClient) fetchInstallationToken(ctx context.Context, id int64) (*cachedToken, error) {
	appJWT, err := c.newJWT()
	if err != nil {
		return nil, fmt.Errorf("githubapp: sign JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.apiURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("githubapp: installation token request: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("githubapp: installation token status %d: %s", resp.StatusCode, body)
	}

	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("githubapp: decode token response: %w", err)
	}
	return &cachedToken{token: result.Token, expiresAt: result.ExpiresAt}, nil
}
