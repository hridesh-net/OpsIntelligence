// Package main — OpsIntelligence onboarding.
//
// This is a deliberately practical wizard for the DevOps fork. It collects:
//   - a default LLM provider from a broad provider list (OpenAI, Anthropic,
//     Groq, Mistral, Together, OpenRouter, NVIDIA, Cohere, DeepSeek,
//     Perplexity, xAI, HuggingFace, Ollama, vLLM, LM Studio, Azure OpenAI,
//     Bedrock, Vertex, Voyage)
//   - provider API key/base-url/model fields needed for the chosen provider
//   - Messaging channels (Telegram, Discord, Slack, WhatsApp) — optional, AssistClaw-style
//   - DevOps integration tokens (GitHub, GitLab, Jenkins, SonarQube) — optional
//   - an active team name
//
// It writes a starter ~/.opsintelligence/opsintelligence.yaml. Advanced users
// should edit the YAML directly (see .opsintelligence.yaml.example).
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

// onboardNeedsAPIKey mirrors AssistClaw: local providers have no cloud key; Vertex uses the dedicated Vertex step.
func onboardNeedsAPIKey(provider string) bool {
	switch provider {
	case "ollama", "vllm", "lm_studio", "vertex":
		return false
	default:
		return true
	}
}

func onboardBaseURLDefault(provider string) (string, bool) {
	switch provider {
	case "ollama":
		return "http://127.0.0.1:11434", true
	case "vllm":
		return "http://127.0.0.1:8000", true
	case "lm_studio":
		return "http://127.0.0.1:1234", true
	default:
		return "", false
	}
}

func onboardPickContains(picks []string, want string) bool {
	for _, p := range picks {
		if p == want {
			return true
		}
	}
	return false
}

func onboardSplitCSV(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// runOnboarding is invoked by `loadConfig` when no config file exists. It runs
// the non-interactive skeleton path so fresh installs get a usable YAML before
// the daemon starts. Users can re-run `opsintelligence onboard` later for the
// full wizard.
func runOnboarding(path string) (string, error) {
	if path == "" {
		path = config.DefaultConfigPath()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return path, fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
	}
	content := renderOnboardYAML(onboardValues{
		Provider:   "openai",
		ActiveTeam: "platform",
	})
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return path, fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

func onboardCmd(flags *globalFlags) *cobra.Command {
	_ = flags
	var nonInteractive bool
	cmd := &cobra.Command{
		Use:   "onboard",
		Short: "Interactive setup: write a starter opsintelligence.yaml",
		Long: `Runs a minimal wizard that collects LLM provider credentials, messaging channels
(Telegram, Discord, Slack, WhatsApp — optional, same flow as AssistClaw), DevOps tokens
(GitHub/GitLab/Jenkins/SonarQube — optional), and an active team name, then writes ~/.opsintelligence/opsintelligence.yaml.

Advanced configuration (memory tiers, MCP clients, cron, webhooks, extensions) is best edited
directly in YAML — see .opsintelligence.yaml.example in the repository.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			var (
				provider       = "openai"
				apiKey         string
				providerURL    string
				defaultModel   string
				openrouterSite string
				openrouterURL  string
				azureAPIVer    string
				bedrockRegion  string
				bedrockProfile string
				accessKeyID    string
				secretKey      string
				vertexProject  string
				vertexLocation string
				vertexCreds    string
				slackBotToken  string
				slackAppToken  string
				githubToken    string
				gitlabURL      string
				gitlabToken    string
				jenkinsURL     string
				jenkinsUser    string
				jenkinsToken   string
				sonarURL       string
				sonarToken     string
				activeTeam     = "platform"
				// AssistClaw-style opt-in: configure provider creds, messaging channels, DevOps integrations.
				configureProvider = true
				integrationPicks  []string
				channelPicks      []string
				tgBotToken        string
				tgDMMode          = "pairing"
				tgAllowFromRaw    string
				tgReqMention      = true
				dcBotToken        string
				dcDMMode          = "pairing"
				dcAllowFromRaw    string
				dcReqMention      = true
				slackDMMode       = "pairing"
				slackAllowFromRaw string
				waSessionID       string
				waDMMode          = "pairing"
				waAllowFromRaw    string
			)

			if !nonInteractive {
				runStep := func(group *huh.Group) error {
					form := huh.NewForm(group).WithShowHelp(true).WithShowErrors(true)
					form.WithProgramOptions(tea.WithAltScreen())
					return form.Run()
				}
				if err := runStep(
					huh.NewGroup(
						huh.NewSelect[string]().
							Title("Default LLM provider").
							Options(
								huh.NewOption("OpenAI", "openai"),
								huh.NewOption("Anthropic", "anthropic"),
								huh.NewOption("Groq", "groq"),
								huh.NewOption("Mistral", "mistral"),
								huh.NewOption("Together AI", "together"),
								huh.NewOption("OpenRouter", "openrouter"),
								huh.NewOption("NVIDIA NIM", "nvidia"),
								huh.NewOption("Cohere", "cohere"),
								huh.NewOption("DeepSeek", "deepseek"),
								huh.NewOption("Perplexity", "perplexity"),
								huh.NewOption("xAI (Grok)", "xai"),
								huh.NewOption("HuggingFace Inference", "huggingface"),
								huh.NewOption("Ollama (local)", "ollama"),
								huh.NewOption("vLLM (OpenAI-compatible)", "vllm"),
								huh.NewOption("LM Studio (local)", "lm_studio"),
								huh.NewOption("Azure OpenAI", "azure_openai"),
								huh.NewOption("AWS Bedrock", "bedrock"),
								huh.NewOption("Google Vertex AI", "vertex"),
								huh.NewOption("Voyage", "voyage"),
							).
							Value(&provider),
						huh.NewInput().
							Title("Default model (optional)").
							Description("Examples: gpt-5, claude-sonnet-4-5, grok-4, llama-3.3-70b.").
							Value(&defaultModel),
					).Title("Provider selection"),
				); err != nil {
					return err
				}
				if err := runStep(
					huh.NewGroup(
						huh.NewConfirm().
							Title("Configure provider API keys and connection details now?").
							Description("Say No to skip — you can edit ~/.opsintelligence/opsintelligence.yaml later (same idea as AssistClaw).").
							Value(&configureProvider),
					).Title("Provider setup"),
				); err != nil {
					return err
				}
				if configureProvider {
					var credFields []huh.Field
					if onboardNeedsAPIKey(provider) {
						credFields = append(credFields,
							huh.NewInput().
								Title("Provider API key").
								Description("Stored in your local config file.").
								EchoMode(huh.EchoModePassword).
								Value(&apiKey))
					}
					if def, ok := onboardBaseURLDefault(provider); ok {
						if strings.TrimSpace(providerURL) == "" {
							providerURL = def
						}
						credFields = append(credFields,
							huh.NewInput().
								Title("Base URL").
								Description("Override if your server listens elsewhere.").
								Value(&providerURL))
					} else if provider == "azure_openai" {
						credFields = append(credFields,
							huh.NewInput().
								Title("Azure OpenAI endpoint").
								Description("e.g. https://YOUR_RESOURCE.openai.azure.com").
								Value(&providerURL))
					} else if provider == "vertex" {
						// Project / location / service-account path are collected on the Vertex step.
					} else if provider == "bedrock" {
						// Region / IAM / keys are collected on the Bedrock step; optional bearer still uses api_key above.
					} else {
						credFields = append(credFields,
							huh.NewInput().
								Title("Base URL (optional override)").
								Description("For OpenAI-compatible proxies or custom endpoints. Leave blank for provider defaults.").
								Value(&providerURL))
					}
					if len(credFields) > 0 {
						if err := runStep(huh.NewGroup(credFields...).Title("Provider credentials")); err != nil {
							return err
						}
					}
					if provider == "openrouter" {
						if err := runStep(
							huh.NewGroup(
								huh.NewInput().Title("OpenRouter app/site name (optional)").Description("Used for request attribution.").Value(&openrouterSite),
								huh.NewInput().Title("OpenRouter site URL (optional)").Description("e.g. https://ops.example.com").Value(&openrouterURL),
							).Title("OpenRouter options"),
						); err != nil {
							return err
						}
					}
					if provider == "azure_openai" {
						if err := runStep(
							huh.NewGroup(
								huh.NewInput().Title("Azure API version (optional)").Description("e.g. 2024-06-01").Value(&azureAPIVer),
							).Title("Azure OpenAI options"),
						); err != nil {
							return err
						}
					}
					if provider == "bedrock" {
						if err := runStep(
							huh.NewGroup(
								huh.NewInput().Title("Bedrock region (optional)").Description("e.g. us-east-1").Value(&bedrockRegion),
								huh.NewInput().Title("Bedrock profile (optional)").Description("Named AWS profile.").Value(&bedrockProfile),
								huh.NewInput().Title("AWS access key ID (optional)").Description("Only if not using profile/role auth.").Value(&accessKeyID),
								huh.NewInput().Title("AWS secret access key (optional)").EchoMode(huh.EchoModePassword).Description("Only if not using profile/role auth.").Value(&secretKey),
							).Title("AWS Bedrock options"),
						); err != nil {
							return err
						}
					}
					if provider == "vertex" {
						if err := runStep(
							huh.NewGroup(
								huh.NewInput().Title("Vertex project ID (optional)").Value(&vertexProject),
								huh.NewInput().Title("Vertex location (optional)").Description("e.g. us-central1").Value(&vertexLocation),
								huh.NewInput().Title("Vertex credentials file path (optional)").Description("Service-account JSON path").Value(&vertexCreds),
							).Title("Vertex options"),
						); err != nil {
							return err
						}
					}
				}
				if err := runStep(
					huh.NewGroup(
						huh.NewMultiSelect[string]().
							Title("Messaging channels").
							Description("AssistClaw-style: pick chat apps to configure now. Space toggles; leave empty to skip.").
							Options(
								huh.NewOption("Telegram", "telegram"),
								huh.NewOption("Discord", "discord"),
								huh.NewOption("Slack (Socket Mode)", "slack"),
								huh.NewOption("WhatsApp", "whatsapp"),
							).
							Value(&channelPicks),
					).Title("Messaging channels"),
				); err != nil {
					return err
				}
				dmModeOpts := []huh.Option[string]{
					huh.NewOption("Pairing (recommended)", "pairing"),
					huh.NewOption("Allowlist only", "allowlist"),
					huh.NewOption("Open (public)", "open"),
					huh.NewOption("Disabled", "disabled"),
				}
				if onboardPickContains(channelPicks, "telegram") {
					if err := runStep(
						huh.NewGroup(
							huh.NewInput().Title("Telegram bot token").Value(&tgBotToken),
							huh.NewSelect[string]().Title("Telegram security mode").Options(dmModeOpts...).Value(&tgDMMode),
							huh.NewInput().Title("Whitelisted Telegram IDs").Description("Comma-separated numeric IDs or @usernames (allowlist mode).").Value(&tgAllowFromRaw),
							huh.NewConfirm().Title("Require @bot mention in groups?").Value(&tgReqMention),
						).Title("Telegram"),
					); err != nil {
						return err
					}
				}
				if onboardPickContains(channelPicks, "discord") {
					if err := runStep(
						huh.NewGroup(
							huh.NewInput().Title("Discord bot token").Value(&dcBotToken),
							huh.NewSelect[string]().Title("Discord security mode").Options(dmModeOpts...).Value(&dcDMMode),
							huh.NewInput().Title("Whitelisted Discord user IDs").Description("Comma-separated (allowlist mode).").Value(&dcAllowFromRaw),
							huh.NewConfirm().Title("Require @bot mention in server channels?").Description("Recommended for busy servers.").Value(&dcReqMention),
						).Title("Discord"),
					); err != nil {
						return err
					}
				}
				if onboardPickContains(channelPicks, "slack") {
					if err := runStep(
						huh.NewGroup(
							huh.NewInput().Title("Slack bot token").Description("xoxb-…").Value(&slackBotToken),
							huh.NewInput().Title("Slack app token").Description("xapp-… (Socket Mode).").Value(&slackAppToken),
							huh.NewSelect[string]().Title("Slack security mode").Options(dmModeOpts...).Value(&slackDMMode),
							huh.NewInput().Title("Whitelisted Slack user IDs").Description("Comma-separated (allowlist mode).").Value(&slackAllowFromRaw),
						).Title("Slack"),
					); err != nil {
						return err
					}
				}
				if onboardPickContains(channelPicks, "whatsapp") {
					if strings.TrimSpace(waSessionID) == "" {
						waSessionID = "personal"
					}
					if err := runStep(
						huh.NewGroup(
							huh.NewNote().Title("WhatsApp").Description("Links via QR on first start; session DB lives under your state directory."),
							huh.NewInput().Title("WhatsApp session ID").Description("e.g. personal").Value(&waSessionID),
							huh.NewSelect[string]().Title("WhatsApp security mode").Options(dmModeOpts...).Value(&waDMMode),
							huh.NewInput().Title("Whitelisted numbers").Description("Comma-separated E.164 (allowlist mode).").Value(&waAllowFromRaw),
						).Title("WhatsApp"),
					); err != nil {
						return err
					}
				}
				if err := runStep(
					huh.NewGroup(
						huh.NewMultiSelect[string]().
							Title("Which DevOps integrations should we configure now?").
							Description("Space to toggle · Enter to confirm · leave empty to skip all (configure later in YAML).").
							Options(
								huh.NewOption("GitHub (PAT or app token)", "github"),
								huh.NewOption("GitLab", "gitlab"),
								huh.NewOption("Jenkins", "jenkins"),
								huh.NewOption("SonarQube", "sonar"),
							).
							Value(&integrationPicks),
					).Title("DevOps integrations"),
				); err != nil {
					return err
				}
				if onboardPickContains(integrationPicks, "github") {
					if err := runStep(
						huh.NewGroup(
							huh.NewNote().Title("GitHub").Description("PAT or App installation token with repo/read:org scope."),
							huh.NewInput().Title("GitHub token").EchoMode(huh.EchoModePassword).Value(&githubToken),
						).Title("GitHub"),
					); err != nil {
						return err
					}
				}
				if onboardPickContains(integrationPicks, "gitlab") {
					if err := runStep(
						huh.NewGroup(
							huh.NewNote().Title("GitLab").Description("Base URL and personal/project access token."),
							huh.NewInput().Title("GitLab base URL").Value(&gitlabURL),
							huh.NewInput().Title("GitLab token").EchoMode(huh.EchoModePassword).Value(&gitlabToken),
						).Title("GitLab"),
					); err != nil {
						return err
					}
				}
				if onboardPickContains(integrationPicks, "jenkins") {
					if err := runStep(
						huh.NewGroup(
							huh.NewNote().Title("Jenkins").Description("Base URL plus user + API token for job status."),
							huh.NewInput().Title("Jenkins base URL").Value(&jenkinsURL),
							huh.NewInput().Title("Jenkins user").Value(&jenkinsUser),
							huh.NewInput().Title("Jenkins API token").EchoMode(huh.EchoModePassword).Value(&jenkinsToken),
						).Title("Jenkins"),
					); err != nil {
						return err
					}
				}
				if onboardPickContains(integrationPicks, "sonar") {
					if err := runStep(
						huh.NewGroup(
							huh.NewNote().Title("SonarQube").Description("Base URL and token for quality-gate + issues."),
							huh.NewInput().Title("Sonar base URL").Value(&sonarURL),
							huh.NewInput().Title("Sonar token").EchoMode(huh.EchoModePassword).Value(&sonarToken),
						).Title("SonarQube"),
					); err != nil {
						return err
					}
				}
				if err := runStep(
					huh.NewGroup(
						huh.NewInput().
							Title("Active team name").
							Description("Directory under ~/.opsintelligence/teams/<name> whose *.md files the agent must follow.").
							Value(&activeTeam),
					).Title("Team policy"),
				); err != nil {
					return err
				}
			}

			path := config.DefaultConfigPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
			}
			content := renderOnboardYAML(onboardValues{
				Provider:           provider,
				APIKey:             apiKey,
				ProviderURL:        providerURL,
				DefaultModel:       defaultModel,
				OpenRouterURL:      openrouterURL,
				OpenRouterApp:      openrouterSite,
				AzureAPIVer:        azureAPIVer,
				BedrockRegion:      bedrockRegion,
				BedrockProfile:     bedrockProfile,
				AccessKeyID:        accessKeyID,
				SecretKey:          secretKey,
				VertexProject:      vertexProject,
				VertexLocation:     vertexLocation,
				VertexCreds:        vertexCreds,
				TelegramBot:        tgBotToken,
				TelegramDMMode:     tgDMMode,
				TelegramAllow:      tgAllowFromRaw,
				TelegramReqMention: tgReqMention,
				DiscordBot:         dcBotToken,
				DiscordDMMode:      dcDMMode,
				DiscordAllow:       dcAllowFromRaw,
				DiscordReqMention:  dcReqMention,
				SlackBot:           slackBotToken,
				SlackApp:           slackAppToken,
				SlackDMMode:        slackDMMode,
				SlackAllow:         slackAllowFromRaw,
				WhatsAppEnabled:    onboardPickContains(channelPicks, "whatsapp"),
				WaSession:          waSessionID,
				WaDMMode:           waDMMode,
				WaAllow:            waAllowFromRaw,
				GitHubToken:        githubToken,
				GitLabURL:          gitlabURL,
				GitLabToken:        gitlabToken,
				JenkinsURL:         jenkinsURL,
				JenkinsUser:        jenkinsUser,
				JenkinsToken:       jenkinsToken,
				SonarURL:           sonarURL,
				SonarToken:         sonarToken,
				ActiveTeam:         activeTeam,
			})
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				return fmt.Errorf("write %s: %w", path, err)
			}
			fmt.Printf("Wrote %s\n", path)
			fmt.Println("Next: edit this YAML to tune memory/MCP/webhooks, then run `opsintelligence doctor`.")
			return nil
		},
	}
	cmd.Flags().BoolVar(&nonInteractive, "non-interactive", false, "Write a minimal YAML skeleton without prompts (for CI provisioning)")
	return cmd
}

type onboardValues struct {
	Provider                              string
	APIKey                                string
	ProviderURL                           string
	DefaultModel                          string
	OpenRouterURL, OpenRouterApp          string
	AzureAPIVer                           string
	BedrockRegion, BedrockProfile         string
	AccessKeyID, SecretKey                string
	VertexProject, VertexLocation         string
	VertexCreds                           string
	TelegramBot                           string
	TelegramDMMode                        string
	TelegramAllow                         string
	TelegramReqMention                    bool
	DiscordBot                            string
	DiscordDMMode                         string
	DiscordAllow                          string
	DiscordReqMention                     bool
	SlackBot, SlackApp                    string
	SlackDMMode                           string
	SlackAllow                            string
	WhatsAppEnabled                       bool
	WaSession                             string
	WaDMMode                              string
	WaAllow                               string
	GitHubToken                           string
	GitLabURL, GitLabToken                string
	JenkinsURL, JenkinsUser, JenkinsToken string
	SonarURL, SonarToken                  string
	ActiveTeam                            string
}

func renderOnboardYAML(v onboardValues) string {
	var b strings.Builder
	b.WriteString("version: 1\n\n")
	b.WriteString("state_dir: \"~/.opsintelligence\"\n\n")

	b.WriteString("gateway:\n")
	b.WriteString("  host: \"127.0.0.1\"\n")
	b.WriteString("  port: 18789\n")
	b.WriteString("  token: \"\"\n\n")

	b.WriteString("agent:\n")
	b.WriteString("  max_iterations: 64\n")
	b.WriteString("  system_prompt_ext: |\n")
	b.WriteString("    Always read and follow the team's files under teams/<active-team>/ before giving opinions\n")
	b.WriteString("    on PRs, Sonar, or CI. If a guideline conflicts with the user, ask for clarification.\n")
	b.WriteString("  enabled_skills:\n    - devops\n\n")

	b.WriteString("providers:\n")
	switch v.Provider {
	case "azure_openai":
		b.WriteString("  azure_openai:\n")
		if v.APIKey != "" {
			b.WriteString("    api_key: \"" + v.APIKey + "\"\n")
		}
		if v.ProviderURL != "" {
			b.WriteString("    base_url: \"" + v.ProviderURL + "\"\n")
		}
		if v.AzureAPIVer != "" {
			b.WriteString("    api_version: \"" + v.AzureAPIVer + "\"\n")
		}
		if v.DefaultModel != "" {
			b.WriteString("    default_model: \"" + v.DefaultModel + "\"\n")
		}
		b.WriteString("\n")
	case "bedrock":
		b.WriteString("  bedrock:\n")
		if v.BedrockRegion != "" {
			b.WriteString("    region: \"" + v.BedrockRegion + "\"\n")
		}
		if v.BedrockProfile != "" {
			b.WriteString("    profile: \"" + v.BedrockProfile + "\"\n")
		}
		if v.AccessKeyID != "" {
			b.WriteString("    access_key_id: \"" + v.AccessKeyID + "\"\n")
		}
		if v.SecretKey != "" {
			b.WriteString("    secret_access_key: \"" + v.SecretKey + "\"\n")
		}
		if v.APIKey != "" {
			b.WriteString("    api_key: \"" + v.APIKey + "\"\n")
		}
		if v.DefaultModel != "" {
			b.WriteString("    default_model: \"" + v.DefaultModel + "\"\n")
		}
		b.WriteString("\n")
	case "vertex":
		b.WriteString("  vertex:\n")
		if v.VertexProject != "" {
			b.WriteString("    project_id: \"" + v.VertexProject + "\"\n")
		}
		if v.VertexLocation != "" {
			b.WriteString("    location: \"" + v.VertexLocation + "\"\n")
		}
		if v.VertexCreds != "" {
			b.WriteString("    credentials: \"" + v.VertexCreds + "\"\n")
		}
		if v.DefaultModel != "" {
			b.WriteString("    default_model: \"" + v.DefaultModel + "\"\n")
		}
		b.WriteString("\n")
	case "voyage":
		b.WriteString("  voyage:\n")
		if v.APIKey != "" {
			b.WriteString("    api_key: \"" + v.APIKey + "\"\n")
		}
		if v.ProviderURL != "" {
			b.WriteString("    base_url: \"" + v.ProviderURL + "\"\n")
		}
		if v.DefaultModel != "" {
			b.WriteString("    default_model: \"" + v.DefaultModel + "\"\n")
		}
		b.WriteString("\n")
	case "anthropic":
		b.WriteString("  anthropic:\n")
		if v.APIKey != "" {
			b.WriteString("    api_key: \"" + v.APIKey + "\"\n")
		}
		if v.ProviderURL != "" {
			b.WriteString("    base_url: \"" + v.ProviderURL + "\"\n")
		}
		if v.DefaultModel != "" {
			b.WriteString("    default_model: \"" + v.DefaultModel + "\"\n")
		}
		b.WriteString("\n")
	case "groq", "mistral", "together", "nvidia", "cohere", "deepseek", "perplexity", "xai":
		b.WriteString("  " + v.Provider + ":\n")
		if v.APIKey != "" {
			b.WriteString("    api_key: \"" + v.APIKey + "\"\n")
		}
		if v.DefaultModel != "" {
			b.WriteString("    default_model: \"" + v.DefaultModel + "\"\n")
		}
		b.WriteString("\n")
	case "openrouter":
		b.WriteString("  openrouter:\n")
		if v.APIKey != "" {
			b.WriteString("    api_key: \"" + v.APIKey + "\"\n")
		}
		if v.DefaultModel != "" {
			b.WriteString("    default_model: \"" + v.DefaultModel + "\"\n")
		}
		if v.OpenRouterApp != "" {
			b.WriteString("    site_name: \"" + v.OpenRouterApp + "\"\n")
		}
		if v.OpenRouterURL != "" {
			b.WriteString("    site_url: \"" + v.OpenRouterURL + "\"\n")
		}
		b.WriteString("\n")
	case "huggingface":
		b.WriteString("  huggingface:\n")
		if v.APIKey != "" {
			b.WriteString("    api_key: \"" + v.APIKey + "\"\n")
		}
		if v.ProviderURL != "" {
			b.WriteString("    base_url: \"" + v.ProviderURL + "\"\n")
		}
		if v.DefaultModel != "" {
			b.WriteString("    default_model: \"" + v.DefaultModel + "\"\n")
			b.WriteString("    model: \"" + v.DefaultModel + "\"\n")
		}
		b.WriteString("\n")
	case "ollama", "vllm", "lm_studio":
		b.WriteString("  " + v.Provider + ":\n")
		if v.ProviderURL != "" {
			b.WriteString("    base_url: \"" + v.ProviderURL + "\"\n")
		} else if v.Provider == "ollama" {
			b.WriteString("    base_url: \"http://127.0.0.1:11434\"\n")
		} else {
			b.WriteString("    base_url: \"http://127.0.0.1:8000\"\n")
		}
		if v.APIKey != "" {
			b.WriteString("    api_key: \"" + v.APIKey + "\"\n")
		}
		if v.DefaultModel != "" {
			b.WriteString("    default_model: \"" + v.DefaultModel + "\"\n")
		}
		b.WriteString("\n")
	default:
		b.WriteString("  openai:\n")
		if v.APIKey != "" {
			b.WriteString("    api_key: \"" + v.APIKey + "\"\n")
		}
		if v.ProviderURL != "" {
			b.WriteString("    base_url: \"" + v.ProviderURL + "\"\n")
		}
		if v.DefaultModel != "" {
			b.WriteString("    default_model: \"" + v.DefaultModel + "\"\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("channels:\n")
	b.WriteString("  outbound:\n")
	b.WriteString("    max_attempts: 5\n    base_delay_ms: 250\n    max_delay_ms: 10000\n")
	b.WriteString("    jitter_percent: 0.2\n    breaker_threshold: 5\n    breaker_cooldown_s: 30\n")
	b.WriteString("    dlq_path: \"~/.opsintelligence/channels/dlq.ndjson\"\n")
	if strings.TrimSpace(v.TelegramBot) != "" {
		b.WriteString("  telegram:\n")
		b.WriteString("    bot_token: \"" + v.TelegramBot + "\"\n")
		if v.TelegramDMMode != "" {
			b.WriteString("    dm_mode: \"" + v.TelegramDMMode + "\"\n")
		}
		if v.TelegramReqMention {
			b.WriteString("    require_mention: true\n")
		} else {
			b.WriteString("    require_mention: false\n")
		}
		if ids := onboardSplitCSV(v.TelegramAllow); len(ids) > 0 {
			b.WriteString("    allow_from:\n")
			for _, id := range ids {
				b.WriteString("      - \"" + id + "\"\n")
			}
		}
	}
	if strings.TrimSpace(v.DiscordBot) != "" {
		b.WriteString("  discord:\n")
		b.WriteString("    bot_token: \"" + v.DiscordBot + "\"\n")
		if v.DiscordDMMode != "" {
			b.WriteString("    dm_mode: \"" + v.DiscordDMMode + "\"\n")
		}
		if v.DiscordReqMention {
			b.WriteString("    require_mention: true\n")
		} else {
			b.WriteString("    require_mention: false\n")
		}
		if ids := onboardSplitCSV(v.DiscordAllow); len(ids) > 0 {
			b.WriteString("    allow_from:\n")
			for _, id := range ids {
				b.WriteString("      - \"" + id + "\"\n")
			}
		}
	}
	if v.SlackBot != "" || v.SlackApp != "" {
		b.WriteString("  slack:\n")
		b.WriteString("    bot_token: \"" + v.SlackBot + "\"\n")
		b.WriteString("    app_token: \"" + v.SlackApp + "\"\n")
		if v.SlackDMMode != "" {
			b.WriteString("    dm_mode: \"" + v.SlackDMMode + "\"\n")
		}
		if ids := onboardSplitCSV(v.SlackAllow); len(ids) > 0 {
			b.WriteString("    allow_from:\n")
			for _, id := range ids {
				b.WriteString("      - \"" + id + "\"\n")
			}
		}
	}
	if v.WhatsAppEnabled {
		sid := strings.TrimSpace(v.WaSession)
		if sid == "" {
			sid = "personal"
		}
		b.WriteString("  whatsapp:\n")
		b.WriteString("    session_id: \"" + sid + "\"\n")
		if v.WaDMMode != "" {
			b.WriteString("    dm_mode: \"" + v.WaDMMode + "\"\n")
		}
		if ids := onboardSplitCSV(v.WaAllow); len(ids) > 0 {
			b.WriteString("    allow_from:\n")
			for _, id := range ids {
				b.WriteString("      - \"" + id + "\"\n")
			}
		}
	}
	b.WriteString("\n")

	b.WriteString("devops:\n")
	writeBool := func(key string, ok bool) {
		if ok {
			b.WriteString("    enabled: true\n")
		} else {
			b.WriteString("    enabled: false\n")
		}
		_ = key
	}
	b.WriteString("  github:\n")
	writeBool("github", v.GitHubToken != "")
	if v.GitHubToken != "" {
		b.WriteString("    token: \"" + v.GitHubToken + "\"\n")
	}
	b.WriteString("  gitlab:\n")
	writeBool("gitlab", v.GitLabToken != "" && v.GitLabURL != "")
	if v.GitLabURL != "" {
		b.WriteString("    base_url: \"" + v.GitLabURL + "\"\n")
	}
	if v.GitLabToken != "" {
		b.WriteString("    token: \"" + v.GitLabToken + "\"\n")
	}
	b.WriteString("  jenkins:\n")
	writeBool("jenkins", v.JenkinsURL != "" && v.JenkinsToken != "")
	if v.JenkinsURL != "" {
		b.WriteString("    base_url: \"" + v.JenkinsURL + "\"\n")
	}
	if v.JenkinsUser != "" {
		b.WriteString("    user: \"" + v.JenkinsUser + "\"\n")
	}
	if v.JenkinsToken != "" {
		b.WriteString("    token: \"" + v.JenkinsToken + "\"\n")
	}
	b.WriteString("  sonar:\n")
	writeBool("sonar", v.SonarURL != "" && v.SonarToken != "")
	if v.SonarURL != "" {
		b.WriteString("    base_url: \"" + v.SonarURL + "\"\n")
	}
	if v.SonarToken != "" {
		b.WriteString("    token: \"" + v.SonarToken + "\"\n")
	}
	b.WriteString("\n")

	b.WriteString("teams:\n")
	b.WriteString("  active: \"" + v.ActiveTeam + "\"\n")
	b.WriteString("  dir: \"~/.opsintelligence/teams\"\n\n")

	b.WriteString("security:\n  mode: enforce\n  pii_mask: true\n")
	return b.String()
}
