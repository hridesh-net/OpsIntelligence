// Package config loads and validates OpsIntelligence configuration from YAML.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/mempalace"
	"gopkg.in/yaml.v3"
)

// Config is the root configuration structure for OpsIntelligence.
type Config struct {
	// Version identifies the config schema version for migration.
	Version int `yaml:"version"`

	// StateDir is the root directory for all OpsIntelligence state (~/.opsintelligence by default).
	StateDir string `yaml:"state_dir"`

	// Gateway configures the HTTP/WebSocket gateway.
	Gateway GatewayConfig `yaml:"gateway"`

	// Providers holds LLM provider credentials and settings.
	Providers ProvidersConfig `yaml:"providers"`

	// Embeddings configures embedding model providers.
	Embeddings EmbeddingsConfig `yaml:"embeddings"`

	// Memory configures the three-tier memory system.
	Memory MemoryConfig `yaml:"memory"`

	// Routing defines multi-model task routing rules.
	Routing RoutingConfig `yaml:"routing"`

	// Hardware configures C++ sensing integration.
	Hardware HardwareConfig `yaml:"hardware"`

	// Agent configures the agent runner behavior.
	Agent AgentConfig `yaml:"agent"`

	// Channels configures messaging channel integrations.
	Channels ChannelsConfig `yaml:"channels"`

	// Plano configures the optional smart AI routing proxy.
	Plano PlanoConfig `yaml:"plano"`

	// MCP configures the Model Context Protocol server and external MCP client connections.
	MCP MCPConfig `yaml:"mcp"`

	// Security configures the runtime safety guardrail and audit log.
	Security SecurityConfig `yaml:"security"`

	// Cron configures static scheduled jobs.
	Cron []CronJobConfig `yaml:"cron"`

	// A2A configures the Agent-to-Agent protocol support.
	A2A A2AConfig `yaml:"a2a"`

	// Webhooks configures generic incoming webhook handlers.
	Webhooks WebhookConfig `yaml:"webhooks"`

	// Gmail configures Gmail Pub/Sub watcher settings.
	Gmail GmailConfig `yaml:"gmail"`

	// Voice configures internal STT/TTS and continuous conversation.
	Voice VoiceConfig `yaml:"voice"`

	// Extensions configures optional hooks available in OpsIntelligence (prompt
	// fragments only; there is no Node plugin loader). See `opsintelligence extensions list`.
	Extensions ExtensionsConfig `yaml:"extensions"`

	// Tracing configures optional OpenTelemetry tracing.
	Tracing TracingConfig `yaml:"tracing"`

	// DevOps configures the first-class DevOps platform integrations
	// (GitHub, GitLab, Jenkins, SonarQube) used by devops.* agent tools.
	DevOps DevOpsConfig `yaml:"devops"`

	// Teams selects the active team whose *.md guidelines are loaded into the
	// system prompt, plus the directory they live under.
	Teams TeamsConfig `yaml:"teams"`

	// Datastore configures the ops-plane persistence layer (users, roles,
	// api keys, sessions, audit log, task history, oidc state). Strictly
	// separate from agent memory. Default: SQLite under state_dir/ops.db.
	Datastore DatastoreConfig `yaml:"datastore"`
}

// DatastoreConfig configures the ops-plane persistence backend.
//
// Driver selects between the embedded SQLite driver (default, zero
// config, perfect for local installs) and the Postgres driver (for
// cloud / multi-operator installs). Only RBAC/auth/audit/task-history
// tables live here; agent memory stays in memory.episodic_db_path and
// memory.semantic_db_path.
type DatastoreConfig struct {
	// Driver selects the backend: "sqlite" (default) or "postgres".
	Driver string `yaml:"driver"`
	// DSN is driver-specific. For SQLite: a file path or URI. For
	// Postgres: a libpq URL. When empty, defaults to
	// <state_dir>/ops.db for SQLite.
	//
	// The value is overridden by the OPSINTELLIGENCE_DATASTORE_DSN
	// environment variable (useful for cloud installs / secrets).
	DSN string `yaml:"dsn"`
	// MaxOpenConns caps the connection pool (0 -> driver default).
	MaxOpenConns int `yaml:"max_open_conns"`
	// MaxIdleConns caps idle connections (0 -> driver default).
	MaxIdleConns int `yaml:"max_idle_conns"`
	// ConnMaxLifetime is a Go duration string (e.g. "30m"). Empty ->
	// driver default. Useful behind a pgBouncer or cloud LB that
	// silently drops idle connections.
	ConnMaxLifetime string `yaml:"conn_max_lifetime"`
	// Migrations controls auto-run on startup: "auto" (default) or
	// "manual" (operator runs `opsintelligence datastore migrate`).
	Migrations string `yaml:"migrations"`
}

// DevOpsConfig groups the built-in DevOps platform integrations.
type DevOpsConfig struct {
	GitHub  GitHubConfig  `yaml:"github"`
	GitLab  GitLabConfig  `yaml:"gitlab"`
	Jenkins JenkinsConfig `yaml:"jenkins"`
	Sonar   SonarConfig   `yaml:"sonar"`
}

// GitHubConfig configures GitHub access for PR review and Actions monitoring.
type GitHubConfig struct {
	Enabled    bool   `yaml:"enabled"`
	BaseURL    string `yaml:"base_url"`    // default https://api.github.com
	Token      string `yaml:"token"`       // inline token (prefer TokenEnv for secrets)
	TokenEnv   string `yaml:"token_env"`   // e.g. GITHUB_TOKEN
	DefaultOrg string `yaml:"default_org"` // e.g. acme; used for short-hand repo queries
}

// GitLabConfig configures GitLab access for MR review and pipeline monitoring.
type GitLabConfig struct {
	Enabled  bool   `yaml:"enabled"`
	BaseURL  string `yaml:"base_url"`
	Token    string `yaml:"token"`
	TokenEnv string `yaml:"token_env"`
}

// JenkinsConfig configures Jenkins access for job/build status.
type JenkinsConfig struct {
	Enabled  bool   `yaml:"enabled"`
	BaseURL  string `yaml:"base_url"`
	User     string `yaml:"user"`
	Token    string `yaml:"token"`
	TokenEnv string `yaml:"token_env"`
}

// SonarConfig configures SonarQube access for quality-gate + issues.
type SonarConfig struct {
	Enabled          bool   `yaml:"enabled"`
	BaseURL          string `yaml:"base_url"`
	Token            string `yaml:"token"`
	TokenEnv         string `yaml:"token_env"`
	ProjectKeyPrefix string `yaml:"project_key_prefix"`
}

// TeamsConfig selects the active team and where team directories live.
type TeamsConfig struct {
	Active string `yaml:"active"` // directory name under Dir
	Dir    string `yaml:"dir"`    // default: <state_dir>/teams
}

// ExtensionsConfig holds lightweight extension points: optional markdown merged into the system prompt.
type ExtensionsConfig struct {
	Enabled bool `yaml:"enabled"`
	// PromptFiles are paths to UTF-8 text/markdown files merged into the system prompt when
	// Enabled is true. Relative paths resolve under StateDir (e.g. extensions/extra-prompt.md).
	PromptFiles []string `yaml:"prompt_files"`
}

// TracingConfig controls optional OpenTelemetry tracing.
type TracingConfig struct {
	Enabled      bool    `yaml:"enabled"`
	OTLPEndpoint string  `yaml:"otlp_endpoint"` // e.g. localhost:4317
	ServiceName  string  `yaml:"service_name"`
	SampleRatio  float64 `yaml:"sample_ratio"` // 0..1
}

// A2AConfig holds metadata for the Agent-to-Agent protocol.
type A2AConfig struct {
	Enabled     bool   `yaml:"enabled"`
	Name        string `yaml:"name"`
	Description string `yaml:"description"`
	AgentID     string `yaml:"agent_id"` // Optional UUID or stable identifier
}

type CronJobConfig struct {
	ID       string `yaml:"id"`
	Schedule string `yaml:"schedule"`
	Prompt   string `yaml:"prompt"`
}

// WebhookConfig configures the incoming webhook endpoint.
//
// Two layers live here:
//
//   - Adapters (preferred for platform integrations). Each entry in
//     Adapters is a first-class, typed adapter (GitHub, GitLab, …) with
//     its own verification, event parsing, action allowlist and prompt
//     templates. Implementations live in internal/webhookadapter/<name>/.
//   - Mappings (legacy / lightweight generic receivers). These just
//     pattern-match a URL path and substitute top-level JSON keys into
//     a prompt template. Useful for webhooks that don't have a typed
//     adapter yet (e.g. ad-hoc Zapier / n8n flows).
//
// Both layers can coexist: Adapters are matched first; any request that
// doesn't hit an adapter falls through to the generic Mappings handler.
type WebhookConfig struct {
	Enabled bool `yaml:"enabled"`
	// Token is the shared secret checked for generic Mappings. Adapters
	// do their own verification (HMAC, mTLS, …) and bypass this field.
	Token    string           `yaml:"token"`
	Mappings []WebhookMapping `yaml:"mappings"`

	// Adapters is the typed webhook-adapter registry (GitHub today;
	// GitLab / Bitbucket / Jira / Datadog / PagerDuty as follow-ups).
	// Each adapter mounts at /api/webhook/<adapter.path>.
	Adapters WebhookAdapters `yaml:"adapters"`

	// MaxConcurrent caps how many background agent runs can be in flight
	// across ALL webhook adapters at once. Saturation → 503 + Retry-After
	// so senders (GitHub, GitLab, …) back off with their own retry logic
	// instead of this process fanning unbounded goroutines. Default 10.
	MaxConcurrent int `yaml:"max_concurrent,omitempty"`
	// Timeout bounds each adapter-triggered agent run. Accepts Go
	// durations ("10m", "30s"). Default 10m.
	Timeout string `yaml:"timeout,omitempty"`
}

// WebhookAdapters groups typed webhook-adapter configurations. Adding a
// new adapter is a three-step change: add a package under
// internal/webhookadapter/<name>/, add a typed field here, and wire the
// constructor into cmd/opsintelligence when the adapter is enabled.
type WebhookAdapters struct {
	GitHub GitHubWebhookConfig `yaml:"github"`
	// Future:
	//   GitLab   GitLabWebhookConfig   `yaml:"gitlab"`
	//   Bitbucket BitbucketWebhookConfig `yaml:"bitbucket"`
}

// WebhookMapping defines how an incoming webhook maps to an agent action.
type WebhookMapping struct {
	Path           string `yaml:"path"`            // Match /api/webhook/{path}
	PromptTemplate string `yaml:"prompt_template"` // Template for agent prompt (e.g. "Got webhook from {{.source}}: {{.body}}")
	Deliver        bool   `yaml:"deliver"`         // Whether to deliver results to a channel
	Channel        string `yaml:"channel"`         // Channel title to deliver to (e.g. "telegram")
	To             string `yaml:"to"`              // Destination account
	AllowUnsafe    bool   `yaml:"allow_unsafe"`    // If true, doesn't wrap payload in safety boundaries
}

// GitHubWebhookConfig is the typed configuration for the GitHub
// webhook adapter (internal/webhookadapter/github). It sits under
// webhooks.adapters.github in YAML, and is applied by the adapter at
// Path() = Path (default "github") → /api/webhook/github.
//
// Example:
//
//	webhooks:
//	  enabled: true
//	  adapters:
//	    github:
//	      enabled: true
//	      secret: "${OPSINTEL_GITHUB_WEBHOOK_SECRET}"
//	      path: "github"
//	      events:
//	        pull_request:    [opened, reopened, synchronize, ready_for_review]
//	        workflow_run:    [completed]
//	      prompts:
//	        pull_request: |
//	          PR {{.action}} on {{.repository.full_name}}#{{.pull_request.number}}.
//
// Bounded concurrency (max_concurrent) and the per-delivery agent timeout
// are router-level settings (see webhooks.router).
type GitHubWebhookConfig struct {
	Enabled bool   `yaml:"enabled"`
	Secret  string `yaml:"secret"`               // HMAC secret for X-Hub-Signature-256 (required when Enabled)
	Path    string `yaml:"path,omitempty"`       // URL suffix, default "github" (→ /api/webhook/github)
	Default string `yaml:"default_prompt,omitempty"`
	// Events maps a GitHub event name (from the X-GitHub-Event header, e.g.
	// "pull_request", "workflow_run") to the allowlist of payload actions
	// that should trigger the agent. An empty list means "any action for
	// this event". If the map itself is empty, every event is allowed.
	Events map[string][]string `yaml:"events,omitempty"`
	// Prompts maps a GitHub event name to a Go text/template. Templates have
	// access to the full parsed JSON payload plus these extras:
	//   .event         — the X-GitHub-Event value
	//   .delivery_id   — the X-GitHub-Delivery value
	//   .action        — payload.action (convenience, may be empty)
	// Missing templates fall back to Default, then to a concise auto-summary.
	Prompts map[string]string `yaml:"prompts,omitempty"`
	// MaxConcurrent caps how many background agent runs may spawn from
	// GitHub webhooks at once. Additional deliveries still queue via the
	// sub-agent TaskManager. Defaults to 5.
	MaxConcurrent int `yaml:"max_concurrent,omitempty"`
	// Timeout bounds each agent run triggered by a GitHub webhook. Accepts
	// Go durations ("10m", "30s"). Defaults to 10m.
	Timeout string `yaml:"timeout,omitempty"`
	// AllowUnverified bypasses HMAC checks. NEVER enable this in production:
	// it exists solely for local testing with tools like smee.io.
	AllowUnverified bool `yaml:"allow_unverified,omitempty"`
}

// GmailConfig holds settings for the Gmail Pub/Sub integration.
type GmailConfig struct {
	Enabled      bool   `yaml:"enabled"`
	Account      string `yaml:"account"`
	Topic        string `yaml:"topic"`
	Label        string `yaml:"label"`         // e.g. "INBOX"
	SkipWatcher  bool   `yaml:"skip_watcher"`  // If true, OpsIntelligence won't manage the gogcli daemon
	PushEndpoint string `yaml:"push_endpoint"` // Public URL for Pub/Sub push (if not using Tailscale)
}

// VoiceConfig configures internal voice processing (STT/TTS).
type VoiceConfig struct {
	Enabled       bool   `yaml:"enabled"`
	ServicePort   int    `yaml:"service_port"`    // default: 11000
	STTModel      string `yaml:"stt_model"`       // whisper: tiny, base, small, medium, large
	TTSModel      string `yaml:"tts_model"`       // voxcpm
	VoiceCloneRef string `yaml:"voice_clone_ref"` // Path to 5s voice clip
	VenvPath      string `yaml:"venv_path"`       // Path to py venv
}

// MCPConfig configures the MCP server and external MCP client connections.
type MCPConfig struct {
	Server  MCPServerConfig   `yaml:"server"`
	Clients []MCPClientConfig `yaml:"clients"`
}

// MCPServerConfig configures the built-in MCP server.
type MCPServerConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Transport string `yaml:"transport"`  // "stdio" | "http" — default: stdio
	HTTPPort  int    `yaml:"http_port"`  // default: 5173
	AuthToken string `yaml:"auth_token"` // optional bearer token for HTTP mode
}

// MCPClientConfig configures a connection to an external MCP server.
type MCPClientConfig struct {
	Name      string   `yaml:"name"`
	Transport string   `yaml:"transport"` // "stdio" | "http"
	Command   string   `yaml:"command"`   // e.g. "npx @modelcontextprotocol/server-filesystem /tmp"
	Args      []string `yaml:"args"`
	Dir       string   `yaml:"dir"` // stdio: working directory for the child process
	Env       []string `yaml:"env"` // stdio: extra KEY=value entries for the child environment
	URL       string   `yaml:"url"`
	AuthToken string   `yaml:"auth_token"`
}

// PlanoConfig configures the Plano smart routing proxy.
type PlanoConfig struct {
	Enabled          bool              `yaml:"enabled"`
	Endpoint         string            `yaml:"endpoint"`          // default: http://localhost:12000/v1
	FallbackProvider string            `yaml:"fallback_provider"` // provider name: openai, groq, etc.
	Preferences      []PlanoPreference `yaml:"preferences"`
}

// PlanoPreference maps a plain-English routing description to a preferred model.
type PlanoPreference struct {
	Description string `yaml:"description"`
	PreferModel string `yaml:"prefer_model"`
}

// GatewayConfig controls the HTTP/WebSocket gateway.
type GatewayConfig struct {
	Host  string `yaml:"host"`
	Port  int    `yaml:"port"`
	Token string `yaml:"token"`
	TLS   struct {
		Cert string `yaml:"cert"`
		Key  string `yaml:"key"`
	} `yaml:"tls"`
	Bind      string          `yaml:"bind"` // loopback, lan, tailnet, custom
	Tailscale TailscaleConfig `yaml:"tailscale"`
}

type TailscaleConfig struct {
	Mode        string `yaml:"mode"` // off, serve, funnel
	ResetOnExit bool   `yaml:"reset_on_exit"`
}

// ProvidersConfig holds all LLM provider configurations.
type ProvidersConfig struct {
	OpenAI      *ProviderCreds    `yaml:"openai"`
	AzureOpenAI *AzureCreds       `yaml:"azure_openai"`
	Anthropic   *ProviderCreds    `yaml:"anthropic"`
	Bedrock     *BedrockCreds     `yaml:"bedrock"`
	Vertex      *VertexCreds      `yaml:"vertex"`
	Ollama      *LocalCreds       `yaml:"ollama"`
	VLLM        *LocalCreds       `yaml:"vllm"`
	LMStudio    *LocalCreds       `yaml:"lm_studio"`
	Groq        *ProviderCreds    `yaml:"groq"`
	Mistral     *ProviderCreds    `yaml:"mistral"`
	Together    *ProviderCreds    `yaml:"together"`
	OpenRouter  *OpenRouterCreds  `yaml:"openrouter"`
	NVIDIA      *ProviderCreds    `yaml:"nvidia"`
	Cohere      *ProviderCreds    `yaml:"cohere"`
	DeepSeek    *ProviderCreds    `yaml:"deepseek"`
	Perplexity  *ProviderCreds    `yaml:"perplexity"`
	XAI         *ProviderCreds    `yaml:"xai"`
	Voyage      *ProviderCreds    `yaml:"voyage"`
	HuggingFace *HuggingFaceCreds `yaml:"huggingface"`
}

// ProviderCreds holds API key and optional settings for a cloud provider.
type ProviderCreds struct {
	APIKey       string `yaml:"api_key"`
	BaseURL      string `yaml:"base_url"`
	DefaultModel string `yaml:"default_model"`
}

// AzureCreds adds Azure-specific fields.
type AzureCreds struct {
	ProviderCreds `yaml:",inline"`
	APIVersion    string `yaml:"api_version"`
}

// BedrockCreds holds AWS Bedrock authentication settings.
type BedrockCreds struct {
	Region          string `yaml:"region"`
	Profile         string `yaml:"profile"` // AWS named profile
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
	APIKey          string `yaml:"api_key"`
	DefaultModel    string `yaml:"default_model"`
}

// VertexCreds holds Google Vertex AI settings.
type VertexCreds struct {
	ProjectID    string `yaml:"project_id"`
	Location     string `yaml:"location"`
	Credentials  string `yaml:"credentials"` // path to service account JSON
	DefaultModel string `yaml:"default_model"`
}

// LocalCreds configures a local server (Ollama, vLLM, LM Studio).
type LocalCreds struct {
	BaseURL      string `yaml:"base_url"`
	APIKey       string `yaml:"api_key"` // optional for vLLM
	DefaultModel string `yaml:"default_model"`
}

// OpenRouterCreds adds OpenRouter-specific fields.
type OpenRouterCreds struct {
	ProviderCreds `yaml:",inline"`
	SiteName      string `yaml:"site_name"`
	SiteURL       string `yaml:"site_url"`
}

// HuggingFaceCreds adds HuggingFace-specific fields.
type HuggingFaceCreds struct {
	ProviderCreds `yaml:",inline"`
	Model         string `yaml:"model"` // specific model endpoint
}

// EmbeddingsConfig configures embedding provider priority.
type EmbeddingsConfig struct {
	// Priority lists providers in order of preference.
	// Accepted values: openai, cohere, google, ollama, huggingface
	Priority    []string          `yaml:"priority"`
	OpenAI      *ProviderCreds    `yaml:"openai"`
	AzureOpenAI *AzureCreds       `yaml:"azure_openai"`
	Cohere      *ProviderCreds    `yaml:"cohere"`
	Google      *ProviderCreds    `yaml:"google"`
	OllamaEmbed *LocalCreds       `yaml:"ollama"`
	Bedrock     *BedrockCreds     `yaml:"bedrock"`
	Voyage      *ProviderCreds    `yaml:"voyage"`
	Mistral     *ProviderCreds    `yaml:"mistral"`
	Vertex      *VertexCreds      `yaml:"vertex"`
	HuggingFace *HuggingFaceCreds `yaml:"huggingface"`
}

// MemoryConfig controls the memory system.
type MemoryConfig struct {
	// WorkingTokenBudget is the max token count kept in working memory.
	WorkingTokenBudget int `yaml:"working_token_budget"`
	// EpisodicDBPath is the path to the episodic SQLite database.
	EpisodicDBPath string `yaml:"episodic_db_path"`
	// SemanticDBPath is the path to the semantic sqlite-vec database.
	SemanticDBPath string `yaml:"semantic_db_path"`
	// SemanticBackend selects OpsIntelligence's built-in semantic store (sqlite-vec only today).
	SemanticBackend string `yaml:"semantic_backend"`
	// MemPalace wires the upstream MemPalace project (Python + MCP). See memory.mempalace and mcp.clients.
	MemPalace MemoryMemPalaceConfig `yaml:"mempalace"`
	// Mining configures optional taxonomy mining/backfill runs.
	Mining MemoryMiningConfig `yaml:"mining"`
}

// MemoryMemPalaceConfig integrates the real MemPalace memory system via MCP (python -m mempalace.mcp_server).
// It does not replace OpsIntelligence's episodic DB; it adds MemPalace tools and optional delegation from memory_search.
type MemoryMemPalaceConfig struct {
	Enabled bool `yaml:"enabled"`
	// MCPClientName must match an entry in mcp.clients[].name or the synthetic client when AutoStart (default: mempalace).
	MCPClientName string `yaml:"mcp_client_name"`
	// AutoStart runs MemPalace as a stdio MCP child process (in-process sidecar) when no mcp.clients
	// entry exists for MCPClientName. Recommended for single-binary installs; use explicit mcp.clients
	// or an external supervisor instead when you want a true out-of-process sidecar.
	AutoStart bool `yaml:"auto_start"`
	// ManagedVenv creates state_dir/mempalace/venv, pip-installs mempalace, runs mempalace init once,
	// and pins PythonExecutable to that venv. Requires auto_start: true. See `opsintelligence mempalace setup`.
	ManagedVenv bool `yaml:"managed_venv"`
	// BootstrapPython is the host interpreter used only to create the managed venv (stdlib venv module).
	// Default: python3, or OPSINTELLIGENCE_MEMPALACE_BOOTSTRAP_PYTHON.
	BootstrapPython string `yaml:"bootstrap_python"`
	// PythonExecutable is the interpreter for `python -m mempalace.mcp_server` when AutoStart is true.
	// Override with env OPSINTELLIGENCE_MEMPALACE_PYTHON if unset in YAML.
	PythonExecutable string `yaml:"python_executable"`
	// InjectIntoMemorySearch appends MemPalace MCP mempalace_search results to the built-in memory_search tool output.
	InjectIntoMemorySearch bool `yaml:"inject_into_memory_search"`
	// SearchLimit caps the limit argument passed to mempalace_search (0 = use the memory_search limit).
	SearchLimit int `yaml:"search_limit"`
}

type MemoryMiningConfig struct {
	Enabled        bool     `yaml:"enabled"`
	Mode           string   `yaml:"mode"` // incremental | full
	Include        []string `yaml:"include"`
	Exclude        []string `yaml:"exclude"`
	ChunkSize      int      `yaml:"chunk_size"`
	ChunkOverlap   int      `yaml:"chunk_overlap"`
	MaxFileSizeKB  int      `yaml:"max_file_size_kb"`
	MaxFilesPerRun int      `yaml:"max_files_per_run"`
	StatePath      string   `yaml:"state_path"`
}

// RoutingConfig defines multi-model routing rules.
type RoutingConfig struct {
	// Default model used when no rule matches.
	Default string `yaml:"default"`
	// Fallback model used when the primary provider is unavailable.
	Fallback string `yaml:"fallback"`
	// Rules maps task types to model strings (e.g. "ollama/llama3.2").
	Rules []RoutingRule `yaml:"rules"`
}

// RoutingRule maps a task type to a specific model.
type RoutingRule struct {
	Task  string `yaml:"task"`
	Model string `yaml:"model"`
}

// HardwareConfig controls C++ sensing integration.
type HardwareConfig struct {
	Camera CameraConfig `yaml:"camera"`
	Audio  AudioConfig  `yaml:"audio"`
	GPIO   GPIOConfig   `yaml:"gpio"`
}

// CameraConfig configures the camera sensing process.
type CameraConfig struct {
	Enabled     bool   `yaml:"enabled"`
	BinaryPath  string `yaml:"binary_path"`
	DeviceIndex int    `yaml:"device_index"`
	Width       int    `yaml:"width"`
	Height      int    `yaml:"height"`
	FPS         int    `yaml:"fps"`
}

// AudioConfig configures the audio sensing process.
type AudioConfig struct {
	Enabled    bool   `yaml:"enabled"`
	BinaryPath string `yaml:"binary_path"`
	SampleRate int    `yaml:"sample_rate"`
	Channels   int    `yaml:"channels"`
}

// GPIOConfig configures GPIO control.
type GPIOConfig struct {
	Enabled    bool   `yaml:"enabled"`
	BinaryPath string `yaml:"binary_path"`
}

// AgentConfig controls agent runner behavior.
type AgentConfig struct {
	MaxIterations   int      `yaml:"max_iterations"`
	SystemPromptExt string   `yaml:"system_prompt_ext"`
	ToolsDir        string   `yaml:"tools_dir"`
	SkillsDir       string   `yaml:"skills_dir"`
	EnabledSkills   []string `yaml:"enabled_skills"`
	// Heartbeat schedules periodic synthetic prompts (proactive ticks on a dedicated session).
	Heartbeat HeartbeatConfig `yaml:"heartbeat"`
	// Planning adds an upfront milestone breakdown (extra LLM call). Nil = enabled (default on).
	Planning *bool `yaml:"planning"`
	// Reflection adds a self-critique pass when a turn completes without tools. Nil = disabled (saves tokens).
	Reflection *bool `yaml:"reflection"`
	// Palace is optional OpsIntelligence-local retrieval shaping (not the MemPalace product).
	Palace PalaceConfig `yaml:"palace"`
	// LocalIntel runs Gemma 4 E2B inside the process (when built with opsintelligence_localgemma) and
	// prepends a short advisory into the cloud model's system prompt for that turn.
	LocalIntel LocalIntelConfig `yaml:"local_intel"`
}

// LocalIntelConfig enables optional on-device Gemma before the main (cloud) model sees the request.
type LocalIntelConfig struct {
	Enabled bool `yaml:"enabled"`
	// GGUFPath overrides OPSINTELLIGENCE_LOCAL_GEMMA_GGUF when non-empty.
	GGUFPath  string `yaml:"gguf_path"`
	MaxTokens int    `yaml:"max_tokens"`
	// SystemPrompt overrides the default advisory instructions when non-empty.
	SystemPrompt string `yaml:"system_prompt"`
	// CacheDir is where embedded GGUF bytes are materialized; default <state_dir>/localintel.
	CacheDir string `yaml:"cache_dir"`
}

type PalaceConfig struct {
	Enabled             bool `yaml:"enabled"`
	ShadowOnly          bool `yaml:"shadow_only"`
	PromptRouting       bool `yaml:"prompt_routing"`
	MemorySearchRouting bool `yaml:"memory_search_routing"`
	ToolRouting         bool `yaml:"tool_routing"`
	FailOpen            bool `yaml:"fail_open"`
	LogDecisions        bool `yaml:"log_decisions"`
}

// HeartbeatConfig drives autonomous periodic agent turns on a dedicated session.
type HeartbeatConfig struct {
	Enabled   bool   `yaml:"enabled"`
	Interval  string `yaml:"interval"`   // e.g. 30m, 1h (default 30m when enabled)
	SessionID string `yaml:"session_id"` // dedicated session; default opsintelligence:heartbeat
	Prompt    string `yaml:"prompt"`     // defaults to standard HEARTBEAT.md instruction
}

// SecurityConfig configures the runtime safety guardrail and tamper-evident audit log.
type SecurityConfig struct {
	// Mode controls how findings are acted upon.
	// Values: "monitor" (log only, default), "enforce" (block HIGH), "strict" (block MEDIUM+HIGH)
	Mode string `yaml:"mode"`

	// LogPath is the path to the NDJSON audit log.
	// Defaults to <state_dir>/security/audit.ndjson
	LogPath string `yaml:"log_path"`

	// PIIMask replaces detected PII in audit log entries with [REDACTED:<type>].
	PIIMask bool `yaml:"pii_mask"`

	// BlockPatterns are additional regex patterns to block in input.
	BlockPatterns []string `yaml:"block_patterns"`

	// Profile determines which tools are available. "full" or "coding"
	Profile string `yaml:"profile"`

	// OwnerOnlyPaths lists paths relative to state_dir that the agent must never modify
	// (write_file, edit, apply_patch, env write_file, or bash referencing those paths).
	// YAML key omitted → defaults to POLICIES.md, RULES.md, and the policies/ directory.
	// Set explicitly to an empty list (owner_only_paths: []) to disable.
	OwnerOnlyPaths *[]string `yaml:"owner_only_paths"`
}

// ChannelsConfig configures messaging channels. OpsIntelligence ships with
// enterprise channels only (Slack + REST/WS gateway).
type ChannelsConfig struct {
	Outbound OutboundReliabilityConfig `yaml:"outbound"`
	Slack    *SlackConfig              `yaml:"slack"`
}

// OutboundReliabilityConfig configures shared retry/circuit-breaker/DLQ behavior
// for adapter outbound sends.
type OutboundReliabilityConfig struct {
	MaxAttempts      int     `yaml:"max_attempts"`
	BaseDelayMS      int     `yaml:"base_delay_ms"`
	MaxDelayMS       int     `yaml:"max_delay_ms"`
	JitterPercent    float64 `yaml:"jitter_percent"`
	BreakerThreshold int     `yaml:"breaker_threshold"`
	BreakerCooldownS int     `yaml:"breaker_cooldown_s"`
	DLQPath          string  `yaml:"dlq_path"`
}

type SlackConfig struct {
	BotToken  string   `yaml:"bot_token"`
	AppToken  string   `yaml:"app_token"`
	DMMode    string   `yaml:"dm_mode"`    // open, pairing, allowlist, disabled
	AllowFrom []string `yaml:"allow_from"` // Whitelisted IDs/Usernames
}

// ─────────────────────────────────────────────
// Loading
// ─────────────────────────────────────────────

// Load reads configuration from the given file path, expanding environment
// variables in values. Returns a Config with defaults applied.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	// Expand ${ENV_VAR} patterns in the config file.
	expanded := os.ExpandEnv(string(data))

	var cfg Config
	if err := yaml.Unmarshal([]byte(expanded), &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	applyDefaults(&cfg)
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}
	return &cfg, nil
}

// LoadFromEnv builds a minimal Config from environment variables only.
// Useful for containerized deployments without a config file.
func LoadFromEnv() *Config {
	cfg := &Config{}
	applyDefaults(cfg)
	applyProviderEnv(cfg)
	return cfg
}

// MemPalaceBootstrapConfig returns a config with StateDir set and defaults applied, plus the same
// provider environment wiring as [LoadFromEnv]. Use from installers / `opsintelligence mempalace setup --state-dir`
// when opsintelligence.yaml does not exist yet.
func MemPalaceBootstrapConfig(stateDir string) *Config {
	cfg := &Config{}
	cfg.StateDir = filepath.Clean(stateDir)
	applyDefaults(cfg)
	applyProviderEnv(cfg)
	return cfg
}

func applyProviderEnv(cfg *Config) {
	if key := os.Getenv("OPSINTELLIGENCE_OPENAI_API_KEY"); key != "" {
		cfg.Providers.OpenAI = &ProviderCreds{APIKey: key}
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" && cfg.Providers.OpenAI == nil {
		cfg.Providers.OpenAI = &ProviderCreds{APIKey: key}
	}
	if key := os.Getenv("OPSINTELLIGENCE_ANTHROPIC_API_KEY"); key != "" {
		cfg.Providers.Anthropic = &ProviderCreds{APIKey: key}
	}
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" && cfg.Providers.Anthropic == nil {
		cfg.Providers.Anthropic = &ProviderCreds{APIKey: key}
	}
	if key := os.Getenv("OPSINTELLIGENCE_GROQ_API_KEY"); key != "" {
		cfg.Providers.Groq = &ProviderCreds{APIKey: key}
	}
	if key := os.Getenv("GROQ_API_KEY"); key != "" && cfg.Providers.Groq == nil {
		cfg.Providers.Groq = &ProviderCreds{APIKey: key}
	}

	if key := os.Getenv("OPSINTELLIGENCE_XAI_API_KEY"); key != "" {
		cfg.Providers.XAI = &ProviderCreds{APIKey: key}
	}
	if key := os.Getenv("XAI_API_KEY"); key != "" && cfg.Providers.XAI == nil {
		cfg.Providers.XAI = &ProviderCreds{APIKey: key}
	}
	if key := os.Getenv("OPSINTELLIGENCE_MISTRAL_API_KEY"); key != "" {
		cfg.Providers.Mistral = &ProviderCreds{APIKey: key}
	}
	if key := os.Getenv("MISTRAL_API_KEY"); key != "" && cfg.Providers.Mistral == nil {
		cfg.Providers.Mistral = &ProviderCreds{APIKey: key}
	}
	if key := os.Getenv("OPSINTELLIGENCE_OPENROUTER_API_KEY"); key != "" {
		cfg.Providers.OpenRouter = &OpenRouterCreds{ProviderCreds: ProviderCreds{APIKey: key}}
	}
	if key := os.Getenv("OPENROUTER_API_KEY"); key != "" && cfg.Providers.OpenRouter == nil {
		cfg.Providers.OpenRouter = &OpenRouterCreds{ProviderCreds: ProviderCreds{APIKey: key}}
	}
	if url := os.Getenv("OPSINTELLIGENCE_OLLAMA_BASE_URL"); url != "" {
		cfg.Providers.Ollama = &LocalCreds{BaseURL: url}
	} else if os.Getenv("OPSINTELLIGENCE_OLLAMA_ENABLED") == "1" || os.Getenv("OPSINTELLIGENCE_OLLAMA_ENABLED") == "true" {
		cfg.Providers.Ollama = &LocalCreds{BaseURL: "http://localhost:11434"}
	}

	if os.Getenv("OPSINTELLIGENCE_VOICE_ENABLED") == "1" || os.Getenv("OPSINTELLIGENCE_VOICE_ENABLED") == "true" {
		cfg.Voice.Enabled = true
	}
}

// applyDefaults fills in default values for missing configuration.
func applyDefaults(cfg *Config) {
	if cfg.StateDir == "" {
		if env := os.Getenv("OPSINTELLIGENCE_STATE_DIR"); env != "" {
			cfg.StateDir = env
		} else {
			home, _ := os.UserHomeDir()
			cfg.StateDir = filepath.Join(home, ".opsintelligence")
		}
	}
	if cfg.Gateway.Host == "" {
		cfg.Gateway.Host = "127.0.0.1"
	}
	if cfg.Gateway.Port == 0 {
		cfg.Gateway.Port = 18790
	}
	if cfg.Memory.WorkingTokenBudget == 0 {
		cfg.Memory.WorkingTokenBudget = 100_000
	}
	if cfg.Memory.EpisodicDBPath == "" {
		cfg.Memory.EpisodicDBPath = filepath.Join(cfg.StateDir, "memory", "episodic.db")
	}
	if cfg.Memory.SemanticDBPath == "" {
		cfg.Memory.SemanticDBPath = filepath.Join(cfg.StateDir, "memory", "semantic.db")
	}
	if cfg.Memory.SemanticBackend == "" {
		cfg.Memory.SemanticBackend = "sqlite_vec"
	}
	if cfg.Memory.Mining.Mode == "" {
		cfg.Memory.Mining.Mode = "incremental"
	}
	if len(cfg.Memory.Mining.Include) == 0 {
		cfg.Memory.Mining.Include = []string{"MEMORY.md", "memory/*.md"}
	}
	if cfg.Memory.Mining.ChunkSize == 0 {
		cfg.Memory.Mining.ChunkSize = 512
	}
	if cfg.Memory.Mining.ChunkOverlap == 0 {
		cfg.Memory.Mining.ChunkOverlap = 64
	}
	if cfg.Memory.Mining.MaxFileSizeKB == 0 {
		cfg.Memory.Mining.MaxFileSizeKB = 512
	}
	if cfg.Memory.Mining.MaxFilesPerRun == 0 {
		cfg.Memory.Mining.MaxFilesPerRun = 1000
	}
	if strings.TrimSpace(cfg.Memory.Mining.StatePath) == "" {
		cfg.Memory.Mining.StatePath = filepath.Join(cfg.StateDir, "memory", "mining_state.json")
	}
	if strings.TrimSpace(cfg.Memory.MemPalace.MCPClientName) == "" {
		cfg.Memory.MemPalace.MCPClientName = "mempalace"
	}
	if cfg.Memory.MemPalace.ManagedVenv {
		if strings.TrimSpace(cfg.Memory.MemPalace.BootstrapPython) == "" {
			if env := strings.TrimSpace(os.Getenv("OPSINTELLIGENCE_MEMPALACE_BOOTSTRAP_PYTHON")); env != "" {
				cfg.Memory.MemPalace.BootstrapPython = env
			}
		}
		if strings.TrimSpace(cfg.Memory.MemPalace.BootstrapPython) == "" {
			cfg.Memory.MemPalace.BootstrapPython = "python3"
		}
	}
	if cfg.Memory.MemPalace.ManagedVenv && cfg.Memory.MemPalace.AutoStart {
		cfg.Memory.MemPalace.PythonExecutable = mempalace.VenvInterpreter(mempalace.ManagedVenvRoot(cfg.StateDir))
	} else if cfg.Memory.MemPalace.AutoStart {
		if strings.TrimSpace(cfg.Memory.MemPalace.PythonExecutable) == "" {
			if env := strings.TrimSpace(os.Getenv("OPSINTELLIGENCE_MEMPALACE_PYTHON")); env != "" {
				cfg.Memory.MemPalace.PythonExecutable = env
			}
		}
		if strings.TrimSpace(cfg.Memory.MemPalace.PythonExecutable) == "" {
			cfg.Memory.MemPalace.PythonExecutable = "python3"
		}
	}
	if cfg.Agent.MaxIterations == 0 {
		cfg.Agent.MaxIterations = 64
	}

	// Datastore defaults. Local profile: SQLite under state_dir/ops.db.
	// Cloud operators override via YAML or OPSINTELLIGENCE_DATASTORE_DSN.
	if strings.TrimSpace(cfg.Datastore.Driver) == "" {
		cfg.Datastore.Driver = "sqlite"
	}
	if strings.TrimSpace(cfg.Datastore.DSN) == "" {
		if env := strings.TrimSpace(os.Getenv("OPSINTELLIGENCE_DATASTORE_DSN")); env != "" {
			cfg.Datastore.DSN = env
		} else if cfg.Datastore.Driver == "sqlite" {
			// Expand a literal leading "~" — the onboarding template
			// writes state_dir: "~/.opsintelligence" verbatim and the
			// mattn/go-sqlite3 driver does not expand tildes.
			base := cfg.StateDir
			if strings.HasPrefix(base, "~") {
				if home, err := os.UserHomeDir(); err == nil {
					base = filepath.Join(home, strings.TrimPrefix(base, "~"))
				}
			}
			cfg.Datastore.DSN = "file:" + filepath.Join(base, "ops.db") + "?_foreign_keys=on&_busy_timeout=5000"
		}
	}
	if strings.TrimSpace(cfg.Datastore.Migrations) == "" {
		cfg.Datastore.Migrations = "auto"
	}
	if cfg.Agent.ToolsDir == "" {
		cfg.Agent.ToolsDir = filepath.Join(cfg.StateDir, "tools")
	}
	if cfg.Agent.SkillsDir == "" {
		cfg.Agent.SkillsDir = filepath.Join(cfg.StateDir, "skills")
	}
	if cfg.Teams.Dir == "" {
		cfg.Teams.Dir = filepath.Join(cfg.StateDir, "teams")
	}
	if cfg.DevOps.GitHub.BaseURL == "" {
		cfg.DevOps.GitHub.BaseURL = "https://api.github.com"
	}
	resolveDevOpsTokens(&cfg.DevOps)
	applyTeamPromptFiles(cfg)
	if strings.TrimSpace(cfg.Agent.LocalIntel.CacheDir) == "" {
		cfg.Agent.LocalIntel.CacheDir = filepath.Join(cfg.StateDir, "localintel")
	}
	if len(cfg.Embeddings.Priority) == 0 {
		cfg.Embeddings.Priority = []string{"openai", "ollama", "cohere", "voyage", "mistral", "google", "huggingface"}
	}

	// Voice Defaults
	cfg.Voice.Enabled = true // Enabled by default as an inbuilt feature
	if cfg.Voice.ServicePort == 0 {
		cfg.Voice.ServicePort = 11000
	}
	if cfg.Voice.STTModel == "" {
		cfg.Voice.STTModel = "base"
	}
	if cfg.Voice.TTSModel == "" {
		cfg.Voice.TTSModel = "voxcpm"
	}
	if cfg.Voice.VenvPath == "" {
		cfg.Voice.VenvPath = filepath.Join(cfg.StateDir, "voice_env")
	}

	if cfg.Security.OwnerOnlyPaths == nil {
		p := []string{"POLICIES.md", "RULES.md", "policies"}
		cfg.Security.OwnerOnlyPaths = &p
	}

	// Shared outbound reliability defaults (adapter send path).
	if cfg.Channels.Outbound.MaxAttempts == 0 {
		cfg.Channels.Outbound.MaxAttempts = 5
	}
	if cfg.Channels.Outbound.BaseDelayMS == 0 {
		cfg.Channels.Outbound.BaseDelayMS = 250
	}
	if cfg.Channels.Outbound.MaxDelayMS == 0 {
		cfg.Channels.Outbound.MaxDelayMS = 10_000
	}
	if cfg.Channels.Outbound.JitterPercent == 0 {
		cfg.Channels.Outbound.JitterPercent = 0.2
	}
	if cfg.Channels.Outbound.BreakerThreshold == 0 {
		cfg.Channels.Outbound.BreakerThreshold = 5
	}
	if cfg.Channels.Outbound.BreakerCooldownS == 0 {
		cfg.Channels.Outbound.BreakerCooldownS = 30
	}
	if strings.TrimSpace(cfg.Channels.Outbound.DLQPath) == "" {
		cfg.Channels.Outbound.DLQPath = filepath.Join(cfg.StateDir, "channels", "dlq.ndjson")
	}
	if strings.TrimSpace(cfg.Tracing.OTLPEndpoint) == "" {
		cfg.Tracing.OTLPEndpoint = "localhost:4317"
	}
	if strings.TrimSpace(cfg.Tracing.ServiceName) == "" {
		cfg.Tracing.ServiceName = "opsintelligence"
	}
	if cfg.Tracing.SampleRatio <= 0 {
		cfg.Tracing.SampleRatio = 0.01
	}
	if !cfg.Agent.Palace.Enabled &&
		!cfg.Agent.Palace.ShadowOnly &&
		!cfg.Agent.Palace.PromptRouting &&
		!cfg.Agent.Palace.MemorySearchRouting &&
		!cfg.Agent.Palace.ToolRouting &&
		!cfg.Agent.Palace.LogDecisions &&
		!cfg.Agent.Palace.FailOpen {
		// Safe default for fresh configs while preserving explicit false in active setups.
		cfg.Agent.Palace.FailOpen = true
	}
}

// validate checks that required fields are present.
func validate(cfg *Config) error {
	var issues []string

	if cfg.Routing.Default == "" && cfg.Providers.OpenAI == nil && cfg.Providers.Anthropic == nil &&
		cfg.Providers.Ollama == nil && cfg.Providers.VLLM == nil {
		issues = append(issues, "at least one LLM provider must be configured (providers.openai, providers.anthropic, providers.ollama, etc.)")
	}

	if cfg.Gateway.Port < 1 || cfg.Gateway.Port > 65535 {
		issues = append(issues, "gateway.port must be between 1 and 65535")
	}
	if cfg.Channels.Outbound.MaxAttempts < 1 {
		issues = append(issues, "channels.outbound.max_attempts must be >= 1")
	}
	if cfg.Channels.Outbound.BaseDelayMS < 1 {
		issues = append(issues, "channels.outbound.base_delay_ms must be >= 1")
	}
	if cfg.Channels.Outbound.MaxDelayMS < cfg.Channels.Outbound.BaseDelayMS {
		issues = append(issues, "channels.outbound.max_delay_ms must be >= channels.outbound.base_delay_ms")
	}
	if cfg.Channels.Outbound.JitterPercent < 0 || cfg.Channels.Outbound.JitterPercent > 1 {
		issues = append(issues, "channels.outbound.jitter_percent must be between 0 and 1")
	}
	if cfg.Channels.Outbound.BreakerThreshold < 1 {
		issues = append(issues, "channels.outbound.breaker_threshold must be >= 1")
	}
	if cfg.Channels.Outbound.BreakerCooldownS < 1 {
		issues = append(issues, "channels.outbound.breaker_cooldown_s must be >= 1")
	}
	if cfg.Memory.SemanticBackend != "sqlite_vec" {
		if strings.EqualFold(cfg.Memory.SemanticBackend, "mempalace") {
			issues = append(issues, `memory.semantic_backend "mempalace" is invalid — MemPalace is the separate Python project; integrate it via mcp.clients or memory.mempalace (auto_start / managed_venv). Use semantic_backend: sqlite_vec.`)
		} else {
			issues = append(issues, "memory.semantic_backend must be sqlite_vec")
		}
	}
	if cfg.Memory.MemPalace.InjectIntoMemorySearch && !cfg.Memory.MemPalace.Enabled {
		issues = append(issues, "memory.mempalace.inject_into_memory_search requires memory.mempalace.enabled: true")
	}
	if cfg.Memory.MemPalace.ManagedVenv && !cfg.Memory.MemPalace.AutoStart {
		issues = append(issues, "memory.mempalace.managed_venv requires memory.mempalace.auto_start: true")
	}
	if cfg.Memory.MemPalace.Enabled {
		found := false
		for _, c := range cfg.MCP.Clients {
			if c.Name == cfg.Memory.MemPalace.MCPClientName {
				found = true
				break
			}
		}
		if !found && !cfg.Memory.MemPalace.AutoStart {
			issues = append(issues, fmt.Sprintf("memory.mempalace.enabled requires either mcp.clients with name %q or memory.mempalace.auto_start: true", cfg.Memory.MemPalace.MCPClientName))
		}
	}
	if cfg.Memory.MemPalace.SearchLimit < 0 {
		issues = append(issues, "memory.mempalace.search_limit must be >= 0")
	}
	if cfg.Memory.Mining.Mode != "incremental" && cfg.Memory.Mining.Mode != "full" {
		issues = append(issues, "memory.mining.mode must be one of: incremental, full")
	}
	if cfg.Memory.Mining.ChunkSize < 64 {
		issues = append(issues, "memory.mining.chunk_size must be >= 64")
	}
	if cfg.Memory.Mining.ChunkOverlap < 0 {
		issues = append(issues, "memory.mining.chunk_overlap must be >= 0")
	}
	if cfg.Memory.Mining.MaxFilesPerRun < 1 {
		issues = append(issues, "memory.mining.max_files_per_run must be >= 1")
	}
	if cfg.Tracing.SampleRatio < 0 || cfg.Tracing.SampleRatio > 1 {
		issues = append(issues, "tracing.sample_ratio must be between 0 and 1")
	}

	if len(issues) > 0 {
		return fmt.Errorf("config validation errors:\n  - %s", strings.Join(issues, "\n  - "))
	}
	return nil
}

// DefaultConfigPath returns the default config file path (~/.opsintelligence/opsintelligence.yaml).
func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".opsintelligence", "opsintelligence.yaml")
}

// PublicGatewayBaseURL returns the origin (no trailing slash) for links the agent can give users
// to open local HTML dashboards served by the gateway under /workspace/.
func (c *Config) PublicGatewayBaseURL() string {
	if c == nil {
		return ""
	}
	h := c.Gateway.Host
	if h == "" {
		h = "127.0.0.1"
	}
	p := c.Gateway.Port
	if p == 0 {
		p = 18790
	}
	return fmt.Sprintf("http://%s:%d", h, p)
}

// applyTeamPromptFiles merges the active team's *.md rule files into
// Extensions.PromptFiles. The team directory is expanded via ExpandHome and
// resolved relative to StateDir when relative. Missing directories are a
// no-op: it is safe to configure a team before creating its directory.
//
// Loading order inside the directory is deterministic (lexicographic), so
// pr-review.md sorts before sonar.md and guidelines apply consistently.
func applyTeamPromptFiles(cfg *Config) {
	if cfg == nil || cfg.Teams.Active == "" {
		return
	}
	dir := cfg.Teams.Dir
	if strings.HasPrefix(dir, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			dir = filepath.Join(home, dir[2:])
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(cfg.StateDir, dir)
	}
	teamDir := filepath.Join(dir, cfg.Teams.Active)
	entries, err := os.ReadDir(teamDir)
	if err != nil {
		return
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		paths = append(paths, filepath.Join(teamDir, e.Name()))
	}
	if len(paths) == 0 {
		return
	}
	sort.Strings(paths)
	cfg.Extensions.Enabled = true
	cfg.Extensions.PromptFiles = append(cfg.Extensions.PromptFiles, paths...)
}

// resolveDevOpsTokens replaces empty Token fields with the value of TokenEnv (os.Getenv)
// when non-empty. Inline YAML values always win.
func resolveDevOpsTokens(d *DevOpsConfig) {
	if d == nil {
		return
	}
	if d.GitHub.Token == "" && d.GitHub.TokenEnv != "" {
		d.GitHub.Token = os.Getenv(d.GitHub.TokenEnv)
	}
	if d.GitLab.Token == "" && d.GitLab.TokenEnv != "" {
		d.GitLab.Token = os.Getenv(d.GitLab.TokenEnv)
	}
	if d.Jenkins.Token == "" && d.Jenkins.TokenEnv != "" {
		d.Jenkins.Token = os.Getenv(d.Jenkins.TokenEnv)
	}
	if d.Sonar.Token == "" && d.Sonar.TokenEnv != "" {
		d.Sonar.Token = os.Getenv(d.Sonar.TokenEnv)
	}
}
