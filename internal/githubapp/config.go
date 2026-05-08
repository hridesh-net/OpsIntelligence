// Package githubapp implements a multi-tenant GitHub App that any GitHub
// organisation can install. Each installation can optionally point to its
// own on-premise OpsIntelligence endpoint; when set, incoming GitHub events
// are relayed there instead of being processed locally.
package githubapp

// Config is the operator-level configuration block read from
// opsintelligence.yaml under the `github_app:` key.
type Config struct {
	// Enabled activates the GitHub App subsystem. Default false.
	Enabled bool `yaml:"enabled"`

	// AppID is the numeric GitHub App ID shown on the App's settings page.
	AppID int64 `yaml:"app_id"`

	// PrivateKeyPath is the path to the RSA private key PEM file downloaded
	// from the GitHub App settings. Prefer this over PrivateKeyPEM for secrets.
	PrivateKeyPath string `yaml:"private_key_path"`

	// PrivateKeyPEM is the inline RSA private key PEM. Used when the operator
	// cannot mount a key file (e.g. cloud env-var injection). PrivateKeyPath
	// takes precedence when both are set.
	PrivateKeyPEM string `yaml:"private_key_pem"`

	// WebhookSecret is the shared HMAC secret configured on the GitHub App's
	// webhook settings. All incoming deliveries are verified against this.
	WebhookSecret string `yaml:"webhook_secret"`

	// WebhookPath is the path segment appended to /api/ for the incoming
	// webhook. Default: "github-app/webhook" → POST /api/github-app/webhook.
	WebhookPath string `yaml:"webhook_path"`

	// SetupPath is the path for the post-install setup page. Default:
	// "github-app/setup" → GET/POST /api/github-app/setup.
	SetupPath string `yaml:"setup_path"`

	// PublicURL is the externally-reachable base URL of this OpsIntelligence
	// instance (e.g. "https://opi.example.com"). It is embedded in the
	// GitHub App registration as the "Setup URL" so GitHub redirects org
	// admins here after they install the app.
	PublicURL string `yaml:"public_url"`

	// GitHubAPIURL allows pointing at a GitHub Enterprise Server instance.
	// Default: "https://api.github.com".
	GitHubAPIURL string `yaml:"github_api_url"`
}

func (c *Config) webhookPath() string {
	if c.WebhookPath != "" {
		return c.WebhookPath
	}
	return "github-app/webhook"
}

func (c *Config) setupPath() string {
	if c.SetupPath != "" {
		return c.SetupPath
	}
	return "github-app/setup"
}

func (c *Config) apiURL() string {
	if c.GitHubAPIURL != "" {
		return c.GitHubAPIURL
	}
	return "https://api.github.com"
}
