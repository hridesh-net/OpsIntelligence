// Package main — OpsIntelligence onboarding.
//
// This is a deliberately practical wizard for the DevOps fork. It collects:
//   - a default LLM provider from a broad provider list (OpenAI, Anthropic,
//     Groq, Mistral, Together, OpenRouter, NVIDIA, Cohere, DeepSeek,
//     Perplexity, xAI, HuggingFace, Ollama, vLLM, LM Studio)
//   - provider API key/base-url/model fields needed for the chosen provider
//   - Slack bot/app tokens (optional)
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

	"github.com/charmbracelet/huh"
	"github.com/spf13/cobra"

	"github.com/opsintelligence/opsintelligence/internal/config"
)

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
		Long: `Runs a minimal wizard that collects LLM provider credentials, Slack tokens (optional),
DevOps integration tokens (GitHub/GitLab/Jenkins/SonarQube — optional), and an active team name,
then writes ~/.opsintelligence/opsintelligence.yaml.

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
			)

			if !nonInteractive {
				form := huh.NewForm(
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
							).
							Value(&provider),
						huh.NewInput().
							Title("Provider API key (optional for local providers)").
							EchoMode(huh.EchoModePassword).
							Value(&apiKey),
						huh.NewInput().
							Title("Provider base URL (optional override)").
							Description("For OpenAI-compatible/local providers. Leave blank to use provider defaults.").
							Value(&providerURL),
						huh.NewInput().
							Title("Default model (optional)").
							Description("Examples: gpt-5, claude-sonnet-4-5, grok-4, llama-3.3-70b.").
							Value(&defaultModel),
						huh.NewInput().
							Title("OpenRouter app/site name (optional)").
							Description("Only used when provider=openrouter for better request attribution.").
							Value(&openrouterSite),
						huh.NewInput().
							Title("OpenRouter site URL (optional)").
							Description("Only used when provider=openrouter (e.g. https://ops.example.com).").
							Value(&openrouterURL),
					),
					huh.NewGroup(
						huh.NewNote().
							Title("Slack (optional)").
							Description("Leave blank to skip. Bot token (xoxb-…) and app-level token (xapp-…)."),
						huh.NewInput().Title("Slack bot token").Value(&slackBotToken),
						huh.NewInput().Title("Slack app token").Value(&slackAppToken),
					),
					huh.NewGroup(
						huh.NewNote().
							Title("GitHub (optional)").
							Description("Personal access token or App installation token with repo/read:org scope."),
						huh.NewInput().Title("GitHub token").EchoMode(huh.EchoModePassword).Value(&githubToken),
					),
					huh.NewGroup(
						huh.NewNote().
							Title("GitLab (optional)").
							Description("Base URL (e.g. https://gitlab.example.com) and a personal/project access token."),
						huh.NewInput().Title("GitLab base URL").Value(&gitlabURL),
						huh.NewInput().Title("GitLab token").EchoMode(huh.EchoModePassword).Value(&gitlabToken),
					),
					huh.NewGroup(
						huh.NewNote().
							Title("Jenkins (optional)").
							Description("Base URL plus user + API token for reading job status."),
						huh.NewInput().Title("Jenkins base URL").Value(&jenkinsURL),
						huh.NewInput().Title("Jenkins user").Value(&jenkinsUser),
						huh.NewInput().Title("Jenkins API token").EchoMode(huh.EchoModePassword).Value(&jenkinsToken),
					),
					huh.NewGroup(
						huh.NewNote().
							Title("SonarQube (optional)").
							Description("Base URL and user/project token for quality-gate + issues endpoints."),
						huh.NewInput().Title("Sonar base URL").Value(&sonarURL),
						huh.NewInput().Title("Sonar token").EchoMode(huh.EchoModePassword).Value(&sonarToken),
					),
					huh.NewGroup(
						huh.NewInput().
							Title("Active team name").
							Description("Directory under ~/.opsintelligence/teams/<name> whose *.md files the agent must follow.").
							Value(&activeTeam),
					),
				)
				if err := form.Run(); err != nil {
					return err
				}
			}

			path := config.DefaultConfigPath()
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Errorf("mkdir %s: %w", filepath.Dir(path), err)
			}
			content := renderOnboardYAML(onboardValues{
				Provider:      provider,
				APIKey:        apiKey,
				ProviderURL:   providerURL,
				DefaultModel:  defaultModel,
				OpenRouterURL: openrouterURL,
				OpenRouterApp: openrouterSite,
				SlackBot:      slackBotToken,
				SlackApp:      slackAppToken,
				GitHubToken:   githubToken,
				GitLabURL:     gitlabURL,
				GitLabToken:   gitlabToken,
				JenkinsURL:    jenkinsURL,
				JenkinsUser:   jenkinsUser,
				JenkinsToken:  jenkinsToken,
				SonarURL:      sonarURL,
				SonarToken:    sonarToken,
				ActiveTeam:    activeTeam,
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
	SlackBot, SlackApp                    string
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
	if v.SlackBot != "" || v.SlackApp != "" {
		b.WriteString("  slack:\n")
		b.WriteString("    bot_token: \"" + v.SlackBot + "\"\n")
		b.WriteString("    app_token: \"" + v.SlackApp + "\"\n")
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
