package main

// onboard_steps.go — helper functions used by the onboarding wizard
// (BuildOnboardStepsWizard in onboard_wizard_steps.go). The legacy huh-based
// step builders that lived in this file were removed in Phase 5c chunk 4;
// only the state-population and YAML-emission helpers remain.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

// onboardState is the shared mutable state threaded through every wizard step.
// Pointer-bound fields are written by step OnSubmit callbacks and read by
// subsequent steps' Skip / Form closures.
type onboardState struct {
	configPath string
	existing   *config.Config

	primary   provEntry
	secondary provEntry
	embed     embedEntry

	secChoice string // "none" | "configure"

	gwMode  string
	gwPort  int
	gwHost  string
	gwToken string
	tsMode  string

	selectedChannels  []string
	tgBotToken        string
	tgDMMode          string
	tgAllowFromRaw    string
	dcBotToken        string
	dcDMMode          string
	dcAllowFromRaw    string
	dcRequireMention  bool
	slBotToken        string
	slAppToken        string
	slDMMode          string
	slAllowFromRaw    string
	waSessionID       string
	waDMMode          string
	waAllowFromRaw    string
	teamsAppID        string
	teamsAppPassword  string
	teamsListenAddr   string
	teamsDMMode       string
	teamsAllowFromRaw string

	selectedSkills     []string
	codingModel        string
	visionModel        string
	usePlano           bool
	planoEndpoint      string
	planoFastModel     string
	planoPowerfulModel string
	setupMP            bool
	localIntelEnabled  bool
	localIntelGGUF     string
	memPalaceEnabled   bool
	configureDevOps    bool
	ghWebhookEnabled   bool
	ghWebhookSecret    string
	githubToken        string
	githubTokenEnv     string
	githubBaseURL      string
	githubDefaultOrg   string
	gitlabURL          string
	gitlabToken        string
	gitlabTokenEnv     string
	jenkinsURL         string
	jenkinsUser        string
	jenkinsToken       string
	jenkinsTokenEnv    string
	sonarURL           string
	sonarToken         string
	sonarTokenEnv      string
	sonarProjectPrefix string
	activeTeam         string

	// Set true when the final save side-effect completes successfully.
	done bool
}

func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}


// ── Existing-config pre-population ───────────────────────────────────────────

func populateFromExisting(s *onboardState, c *config.Config) {
	// Primary provider
	if c.Routing.Default != "" {
		parts := strings.Split(c.Routing.Default, "/")
		if len(parts) > 0 {
			prov := parts[0]
			if prov == "azure_openai" {
				prov = "azure"
			}
			s.primary.provider = prov
		}
	}
	switch s.primary.provider {
	case "anthropic":
		if c.Providers.Anthropic != nil {
			s.primary.apiKey = c.Providers.Anthropic.APIKey
			s.primary.baseURL = c.Providers.Anthropic.BaseURL
			s.primary.model = c.Providers.Anthropic.DefaultModel
		}
	case "openai":
		if c.Providers.OpenAI != nil {
			s.primary.apiKey = c.Providers.OpenAI.APIKey
			s.primary.baseURL = c.Providers.OpenAI.BaseURL
			s.primary.model = c.Providers.OpenAI.DefaultModel
		}
	case "azure":
		if c.Providers.AzureOpenAI != nil {
			s.primary.apiKey = c.Providers.AzureOpenAI.APIKey
			s.primary.baseURL = c.Providers.AzureOpenAI.BaseURL
			s.primary.apiVersion = c.Providers.AzureOpenAI.APIVersion
			s.primary.model = c.Providers.AzureOpenAI.DefaultModel
		}
	case "ollama":
		if c.Providers.Ollama != nil {
			s.primary.baseURL = c.Providers.Ollama.BaseURL
			s.primary.model = c.Providers.Ollama.DefaultModel
		}
	case "bedrock":
		if c.Providers.Bedrock != nil {
			s.primary.awsRegion = c.Providers.Bedrock.Region
			s.primary.awsProfile = c.Providers.Bedrock.Profile
			s.primary.awsAccessKey = c.Providers.Bedrock.AccessKeyID
			s.primary.awsSecretKey = c.Providers.Bedrock.SecretAccessKey
			s.primary.apiKey = c.Providers.Bedrock.APIKey
			s.primary.model = c.Providers.Bedrock.DefaultModel
		}
	case "groq":
		if c.Providers.Groq != nil {
			s.primary.apiKey = c.Providers.Groq.APIKey
			s.primary.model = c.Providers.Groq.DefaultModel
		}
	case "mistral":
		if c.Providers.Mistral != nil {
			s.primary.apiKey = c.Providers.Mistral.APIKey
			s.primary.model = c.Providers.Mistral.DefaultModel
		}
	case "openrouter":
		if c.Providers.OpenRouter != nil {
			s.primary.apiKey = c.Providers.OpenRouter.APIKey
			s.primary.model = c.Providers.OpenRouter.DefaultModel
		}
	case "deepseek":
		if c.Providers.DeepSeek != nil {
			s.primary.apiKey = c.Providers.DeepSeek.APIKey
			s.primary.model = c.Providers.DeepSeek.DefaultModel
		}
	case "gemini":
		if c.Providers.Gemini != nil {
			s.primary.apiKey = c.Providers.Gemini.APIKey
			s.primary.baseURL = c.Providers.Gemini.BaseURL
			s.primary.model = c.Providers.Gemini.DefaultModel
		}
	case "vertex":
		if c.Providers.Vertex != nil {
			s.primary.vertexProj = c.Providers.Vertex.ProjectID
			s.primary.vertexLoc = c.Providers.Vertex.Location
			s.primary.vertexCreds = c.Providers.Vertex.Credentials
			s.primary.model = c.Providers.Vertex.DefaultModel
		}
	}

	// Secondary provider
	if c.Routing.Fallback != "" {
		parts := strings.Split(c.Routing.Fallback, "/")
		if len(parts) > 0 {
			sProv := parts[0]
			if sProv == "azure_openai" {
				sProv = "azure"
			}
			s.secondary.provider = sProv
			s.secChoice = "configure"
		}
	}
	switch s.secondary.provider {
	case "anthropic":
		if c.Providers.Anthropic != nil {
			s.secondary.apiKey = c.Providers.Anthropic.APIKey
			s.secondary.model = c.Providers.Anthropic.DefaultModel
		}
	case "openai":
		if c.Providers.OpenAI != nil {
			s.secondary.apiKey = c.Providers.OpenAI.APIKey
			s.secondary.model = c.Providers.OpenAI.DefaultModel
		}
	case "ollama":
		if c.Providers.Ollama != nil {
			s.secondary.baseURL = c.Providers.Ollama.BaseURL
			s.secondary.model = c.Providers.Ollama.DefaultModel
		}
	case "groq":
		if c.Providers.Groq != nil {
			s.secondary.apiKey = c.Providers.Groq.APIKey
			s.secondary.model = c.Providers.Groq.DefaultModel
		}
	}

	// Embeddings
	if len(c.Embeddings.Priority) > 0 {
		eName := c.Embeddings.Priority[0]
		if eName == "azure_openai" {
			eName = "azure"
		}
		s.embed.provider = eName
		switch eName {
		case "openai":
			if c.Embeddings.OpenAI != nil {
				s.embed.apiKey = c.Embeddings.OpenAI.APIKey
				s.embed.model = c.Embeddings.OpenAI.DefaultModel
			}
		case "ollama":
			if c.Embeddings.OllamaEmbed != nil {
				s.embed.baseURL = c.Embeddings.OllamaEmbed.BaseURL
				s.embed.model = c.Embeddings.OllamaEmbed.DefaultModel
			}
		case "cohere":
			if c.Embeddings.Cohere != nil {
				s.embed.apiKey = c.Embeddings.Cohere.APIKey
				s.embed.model = c.Embeddings.Cohere.DefaultModel
			}
		}
	}

	// Gateway
	s.gwMode = normalizeGatewayBind(c.Gateway.Bind)
	if c.Gateway.Host != "" {
		s.gwHost = c.Gateway.Host
	}
	if c.Gateway.Port != 0 {
		s.gwPort = c.Gateway.Port
	}
	s.gwToken = c.Gateway.Token
	s.tsMode = c.Gateway.Tailscale.Mode
	for _, r := range c.Routing.Rules {
		if r.Task == "coding" {
			s.codingModel = r.Model
		}
		if r.Task == "vision" {
			s.visionModel = r.Model
		}
	}

	// Plano
	if c.Plano.Enabled {
		s.usePlano = true
		if strings.TrimSpace(c.Plano.Endpoint) != "" {
			s.planoEndpoint = c.Plano.Endpoint
		}
		if len(c.Plano.Preferences) >= 2 {
			s.planoFastModel = c.Plano.Preferences[0].PreferModel
			s.planoPowerfulModel = c.Plano.Preferences[1].PreferModel
		}
	}

	// Channels
	if c.Channels.Telegram != nil {
		s.selectedChannels = append(s.selectedChannels, "telegram")
		s.tgBotToken = c.Channels.Telegram.BotToken
		s.tgDMMode = c.Channels.Telegram.DMMode
		if s.tgDMMode == "" {
			s.tgDMMode = "pairing"
		}
		s.tgAllowFromRaw = strings.Join(c.Channels.Telegram.AllowFrom, ", ")
	}
	if c.Channels.Discord != nil {
		s.selectedChannels = append(s.selectedChannels, "discord")
		s.dcBotToken = c.Channels.Discord.BotToken
		s.dcDMMode = c.Channels.Discord.DMMode
		if s.dcDMMode == "" {
			s.dcDMMode = "pairing"
		}
		s.dcAllowFromRaw = strings.Join(c.Channels.Discord.AllowFrom, ", ")
		if c.Channels.Discord.RequireMention != nil {
			s.dcRequireMention = *c.Channels.Discord.RequireMention
		}
	}
	if c.Channels.Slack != nil {
		s.selectedChannels = append(s.selectedChannels, "slack")
		s.slBotToken = c.Channels.Slack.BotToken
		s.slAppToken = c.Channels.Slack.AppToken
		s.slDMMode = c.Channels.Slack.DMMode
		if s.slDMMode == "" {
			s.slDMMode = "pairing"
		}
		s.slAllowFromRaw = strings.Join(c.Channels.Slack.AllowFrom, ", ")
	}
	if c.Channels.WhatsApp != nil {
		s.selectedChannels = append(s.selectedChannels, "whatsapp")
		s.waSessionID = c.Channels.WhatsApp.SessionID
		s.waDMMode = c.Channels.WhatsApp.DMMode
		if s.waDMMode == "" {
			s.waDMMode = "pairing"
		}
		s.waAllowFromRaw = strings.Join(c.Channels.WhatsApp.AllowFrom, ", ")
	}
	if c.Channels.Teams != nil {
		s.selectedChannels = append(s.selectedChannels, "msteams")
		s.teamsAppID = c.Channels.Teams.AppID
		s.teamsAppPassword = c.Channels.Teams.AppPassword
		s.teamsListenAddr = c.Channels.Teams.ListenAddr
		s.teamsDMMode = c.Channels.Teams.DMMode
		if s.teamsDMMode == "" {
			s.teamsDMMode = "allowlist"
		}
		s.teamsAllowFromRaw = strings.Join(c.Channels.Teams.AllowFrom, ", ")
	}

	// Skills + local intel
	s.selectedSkills = c.Agent.EnabledSkills
	s.localIntelEnabled = c.Agent.LocalIntel.Enabled
	s.localIntelGGUF = c.Agent.LocalIntel.GGUFPath

	// DevOps
	d := c.DevOps
	s.githubToken = d.GitHub.Token
	s.githubTokenEnv = strings.TrimSpace(d.GitHub.TokenEnv)
	s.githubBaseURL = strings.TrimSpace(d.GitHub.BaseURL)
	s.githubDefaultOrg = strings.TrimSpace(d.GitHub.DefaultOrg)
	s.gitlabURL = strings.TrimSpace(d.GitLab.BaseURL)
	s.gitlabToken = d.GitLab.Token
	s.gitlabTokenEnv = strings.TrimSpace(d.GitLab.TokenEnv)
	s.jenkinsURL = strings.TrimSpace(d.Jenkins.BaseURL)
	s.jenkinsUser = strings.TrimSpace(d.Jenkins.User)
	s.jenkinsToken = d.Jenkins.Token
	s.jenkinsTokenEnv = strings.TrimSpace(d.Jenkins.TokenEnv)
	s.sonarURL = strings.TrimSpace(d.Sonar.BaseURL)
	s.sonarToken = d.Sonar.Token
	s.sonarTokenEnv = strings.TrimSpace(d.Sonar.TokenEnv)
	s.sonarProjectPrefix = strings.TrimSpace(d.Sonar.ProjectKeyPrefix)

	if c.Webhooks.Enabled && c.Webhooks.Adapters.GitHub.Enabled &&
		strings.TrimSpace(c.Webhooks.Adapters.GitHub.Secret) != "" {
		s.ghWebhookEnabled = true
		s.ghWebhookSecret = c.Webhooks.Adapters.GitHub.Secret
	}
}

// ── YAML generation ───────────────────────────────────────────────────────────

func buildConfigYAML(s *onboardState) string {
	var sb strings.Builder
	sb.WriteString("# OpsIntelligence Configuration\nversion: 1\n\n")

	sb.WriteString("gateway:\n")
	sb.WriteString(fmt.Sprintf("  host: \"%s\"\n  port: %d\n  bind: \"%s\"\n", s.gwHost, s.gwPort, s.gwMode))
	if s.gwToken != "" {
		sb.WriteString(fmt.Sprintf("  token: \"%s\"\n", s.gwToken))
	}
	if s.gwMode == "tailscale" || strings.EqualFold(strings.TrimSpace(s.tsMode), "funnel") {
		sb.WriteString(fmt.Sprintf("  tailscale:\n    mode: \"%s\"\n", s.tsMode))
	}
	sb.WriteString("\n")

	sb.WriteString("agent:\n  max_iterations: 64\n")
	sb.WriteString("  # run_trace_mode: off   # tracing defaults to on; uncomment to disable\n")
	if len(s.selectedSkills) > 0 {
		sb.WriteString("  enabled_skills:\n")
		for _, sk := range s.selectedSkills {
			sb.WriteString(fmt.Sprintf("    - \"%s\"\n", sk))
		}
	}
	if s.localIntelEnabled {
		sb.WriteString("  local_intel:\n    enabled: true\n")
		if g := strings.TrimSpace(s.localIntelGGUF); g != "" {
			sb.WriteString(fmt.Sprintf("    gguf_path: %q\n", g))
		}
		sb.WriteString("    max_tokens: 256\n    smart_routing: false\n")
	}
	sb.WriteString("\n")

	if s.memPalaceEnabled {
		stateDir := filepath.Dir(s.configPath)
		sb.WriteString(fmt.Sprintf("mempalace:\n  enabled: true\n  state_dir: %q\n\n", filepath.Join(stateDir, "mempalace")))
	}

	sb.WriteString("repo_intel:\n  enabled: true\n\n")

	sb.WriteString("providers:\n")
	writeProv := func(e provEntry) {
		name := e.provider
		if name == "azure" {
			name = "azure_openai"
		}
		sb.WriteString(fmt.Sprintf("  %s:\n", name))
		if e.apiKey != "" {
			sb.WriteString(fmt.Sprintf("    api_key: \"%s\"\n", e.apiKey))
		}
		if e.baseURL != "" {
			sb.WriteString(fmt.Sprintf("    base_url: \"%s\"\n", e.baseURL))
		}
		if e.awsRegion != "" {
			sb.WriteString(fmt.Sprintf("    region: \"%s\"\n", e.awsRegion))
		}
		if e.awsAccessKey != "" {
			sb.WriteString(fmt.Sprintf("    access_key_id: \"%s\"\n    secret_access_key: \"%s\"\n", e.awsAccessKey, e.awsSecretKey))
		}
		if e.awsProfile != "" {
			sb.WriteString(fmt.Sprintf("    profile: \"%s\"\n", e.awsProfile))
		}
		if e.vertexProj != "" {
			sb.WriteString(fmt.Sprintf("    project_id: \"%s\"\n    location: \"%s\"\n", e.vertexProj, e.vertexLoc))
		}
		if e.vertexCreds != "" {
			sb.WriteString(fmt.Sprintf("    credentials: \"%s\"\n", e.vertexCreds))
		}
		sb.WriteString(fmt.Sprintf("    default_model: \"%s\"\n", e.model))
	}
	writeProv(s.primary)
	if s.secondary.provider != "" && s.secondary.provider != "none" {
		writeProv(s.secondary)
	}
	sb.WriteString("\n")

	if s.embed.provider != "" {
		sb.WriteString("embeddings:\n")
		sb.WriteString(fmt.Sprintf("  priority:\n    - \"%s\"\n", s.embed.provider))
		eName := s.embed.provider
		if eName == "azure" {
			eName = "azure_openai"
		}
		sb.WriteString(fmt.Sprintf("  %s:\n", eName))
		if s.embed.apiKey != "" {
			sb.WriteString(fmt.Sprintf("    api_key: \"%s\"\n", s.embed.apiKey))
		}
		if s.embed.baseURL != "" {
			sb.WriteString(fmt.Sprintf("    base_url: \"%s\"\n", s.embed.baseURL))
		}
		if s.embed.model != "" {
			sb.WriteString(fmt.Sprintf("    default_model: \"%s\"\n", s.embed.model))
		}
		sb.WriteString("\n")
	}

	if len(s.selectedChannels) > 0 {
		sb.WriteString("channels:\n")
		for _, ch := range s.selectedChannels {
			sb.WriteString(fmt.Sprintf("  %s:\n", ch))
			switch ch {
			case "telegram":
				sb.WriteString(fmt.Sprintf("    bot_token: \"%s\"\n    dm_mode: \"%s\"\n", s.tgBotToken, s.tgDMMode))
				writeAllowFrom(&sb, s.tgAllowFromRaw)
			case "discord":
				sb.WriteString(fmt.Sprintf("    bot_token: \"%s\"\n    dm_mode: \"%s\"\n    require_mention: %t\n", s.dcBotToken, s.dcDMMode, s.dcRequireMention))
				writeAllowFrom(&sb, s.dcAllowFromRaw)
			case "slack":
				sb.WriteString(fmt.Sprintf("    bot_token: \"%s\"\n    app_token: \"%s\"\n    dm_mode: \"%s\"\n", s.slBotToken, s.slAppToken, s.slDMMode))
				writeAllowFrom(&sb, s.slAllowFromRaw)
			case "whatsapp":
				sb.WriteString(fmt.Sprintf("    session_id: \"%s\"\n    dm_mode: \"%s\"\n", s.waSessionID, s.waDMMode))
				writeAllowFrom(&sb, s.waAllowFromRaw)
			case "msteams":
				if s.teamsAppID != "" {
					sb.WriteString(fmt.Sprintf("    app_id: %q\n", s.teamsAppID))
				}
				if s.teamsAppPassword != "" {
					sb.WriteString(fmt.Sprintf("    app_password: %q\n", s.teamsAppPassword))
				}
				// Tailscale Funnel: mount Teams on the gateway so the shared HTTPS
				// Funnel endpoint serves /teams/api/messages. Standalone :3978 is not
				// reachable from Microsoft's servers in Funnel-only setups.
				if s.gwMode == "tailscale" && strings.EqualFold(strings.TrimSpace(s.tsMode), "funnel") {
					sb.WriteString("    expose_via: \"gateway\"\n")
				} else {
					if l := strings.TrimSpace(s.teamsListenAddr); l != "" && l != ":3978" {
						sb.WriteString(fmt.Sprintf("    listen_addr: %q\n", l))
					}
				}
				sb.WriteString(fmt.Sprintf("    dm_mode: %q\n", s.teamsDMMode))
				writeAllowFrom(&sb, s.teamsAllowFromRaw)
			}
		}
		sb.WriteString("\n")
	}

	sb.WriteString("routing:\n")
	pName := s.primary.provider
	if pName == "azure" {
		pName = "azure_openai"
	}
	sb.WriteString(fmt.Sprintf("  default: \"%s/%s\"\n", pName, s.primary.model))
	if s.secondary.provider != "" && s.secondary.provider != "none" {
		sName := s.secondary.provider
		if sName == "azure" {
			sName = "azure_openai"
		}
		sb.WriteString(fmt.Sprintf("  fallback: \"%s/%s\"\n", sName, s.secondary.model))
	}
	if s.codingModel != "default" && s.codingModel != "" {
		sb.WriteString(fmt.Sprintf("  rules:\n    - task: \"coding\"\n      model: \"%s\"\n", s.codingModel))
	}
	if s.visionModel != "default" && s.visionModel != "" {
		if s.codingModel == "default" || s.codingModel == "" {
			sb.WriteString("  rules:\n")
		}
		sb.WriteString(fmt.Sprintf("    - task: \"vision\"\n      model: \"%s\"\n", s.visionModel))
	}

	if s.usePlano {
		sb.WriteString(fmt.Sprintf("\nplano:\n  enabled: true\n  endpoint: \"%s\"\n  preferences:\n", s.planoEndpoint))
		sb.WriteString(fmt.Sprintf("    - description: \"Simple queries → fast model\"\n      prefer_model: \"%s\"\n", s.planoFastModel))
		sb.WriteString(fmt.Sprintf("    - description: \"Complex tasks → powerful model\"\n      prefer_model: \"%s\"\n", s.planoPowerfulModel))
	}

	if s.configureDevOps {
		sb.WriteString("\ndevops:\n")
		writeEn := func(on bool) {
			if on {
				sb.WriteString("    enabled: true\n")
			} else {
				sb.WriteString("    enabled: false\n")
			}
		}
		ghOn := s.githubToken != "" || strings.TrimSpace(s.githubTokenEnv) != ""
		sb.WriteString("  github:\n")
		writeEn(ghOn)
		if b := strings.TrimSpace(s.githubBaseURL); b != "" {
			sb.WriteString(fmt.Sprintf("    base_url: %q\n", b))
		}
		if o := strings.TrimSpace(s.githubDefaultOrg); o != "" {
			sb.WriteString(fmt.Sprintf("    default_org: %q\n", o))
		}
		if s.githubToken != "" {
			sb.WriteString(fmt.Sprintf("    token: %q\n", s.githubToken))
		}
		if e := strings.TrimSpace(s.githubTokenEnv); e != "" {
			sb.WriteString(fmt.Sprintf("    token_env: %q\n", e))
		}
		glOn := strings.TrimSpace(s.gitlabURL) != "" && (s.gitlabToken != "" || strings.TrimSpace(s.gitlabTokenEnv) != "")
		sb.WriteString("  gitlab:\n")
		writeEn(glOn)
		if u := strings.TrimSpace(s.gitlabURL); u != "" {
			sb.WriteString(fmt.Sprintf("    base_url: %q\n", u))
		}
		if s.gitlabToken != "" {
			sb.WriteString(fmt.Sprintf("    token: %q\n    token_env: %q\n", s.gitlabToken, s.gitlabTokenEnv))
		}
		jkOn := strings.TrimSpace(s.jenkinsURL) != "" && (s.jenkinsToken != "" || strings.TrimSpace(s.jenkinsTokenEnv) != "")
		sb.WriteString("  jenkins:\n")
		writeEn(jkOn)
		if u := strings.TrimSpace(s.jenkinsURL); u != "" {
			sb.WriteString(fmt.Sprintf("    base_url: %q\n", u))
		}
		if s.jenkinsToken != "" {
			sb.WriteString(fmt.Sprintf("    token: %q\n", s.jenkinsToken))
		}
		soOn := strings.TrimSpace(s.sonarURL) != "" && (s.sonarToken != "" || strings.TrimSpace(s.sonarTokenEnv) != "")
		sb.WriteString("  sonar:\n")
		writeEn(soOn)
		if u := strings.TrimSpace(s.sonarURL); u != "" {
			sb.WriteString(fmt.Sprintf("    base_url: %q\n", u))
		}
		if s.sonarToken != "" {
			sb.WriteString(fmt.Sprintf("    token: %q\n", s.sonarToken))
		}
		if p := strings.TrimSpace(s.sonarProjectPrefix); p != "" {
			sb.WriteString(fmt.Sprintf("    project_key_prefix: %q\n", p))
		}
	}

	if s.ghWebhookEnabled && strings.TrimSpace(s.ghWebhookSecret) != "" {
		sb.WriteString("\nwebhooks:\n  enabled: true\n  max_concurrent: 10\n  timeout: \"10m\"\n  adapters:\n    github:\n      enabled: true\n")
		sb.WriteString(fmt.Sprintf("      secret: %q\n      path: \"github\"\n", strings.TrimSpace(s.ghWebhookSecret)))
		sb.WriteString("      events:\n        pull_request: [opened, reopened, synchronize, ready_for_review]\n")
	}

	if s.activeTeam != "" {
		sb.WriteString(fmt.Sprintf("\nteams:\n  active: \"%s\"\n  dir: \"~/.opsintelligence/teams\"\n", s.activeTeam))
	}

	return sb.String()
}

func writeAllowFrom(sb *strings.Builder, raw string) {
	if raw == "" {
		return
	}
	sb.WriteString("    allow_from:\n")
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			sb.WriteString(fmt.Sprintf("      - \"%s\"\n", p))
		}
	}
}
