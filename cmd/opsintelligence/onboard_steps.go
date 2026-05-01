package main

// onboard_steps.go — BuildOnboardSteps converts runOnboarding's sequential
// form + side-effect flow into []OnboardWizardStep so the entire wizard runs
// inside a single bubbletea alt-screen shell (RunOnboardWizard) with a
// persistent progress header.
//
// Design notes:
//   - MakeForm factories are called lazily (at step activation), so closures
//     can read state already collected by earlier steps.
//   - SideEffect functions run as goroutines inside the wizard; they must NOT
//     call huh forms or RunWithSpinner (those would create nested bubbletea
//     programs). Long operations (download, install) run directly; the wizard
//     shows its own spinner for the duration.
//   - WhatsApp QR linking cannot be done cleanly from inside alt-screen mode
//     (the QR text would be overdrawn). Users are directed to run
//     `opsintelligence start` after setup to complete pairing.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/huh"
	"github.com/opsintelligence/opsintelligence/cmd/opsintelligence/tui"
	"github.com/opsintelligence/opsintelligence/internal/config"
	"github.com/opsintelligence/opsintelligence/internal/localintel"
	"github.com/opsintelligence/opsintelligence/internal/provider/bedrock"
	"github.com/opsintelligence/opsintelligence/internal/skills"
)

// ── Shared state ─────────────────────────────────────────────────────────────

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

	selectedChannels []string
	tgBotToken       string
	tgDMMode         string
	tgAllowFromRaw   string
	dcBotToken       string
	dcDMMode         string
	dcAllowFromRaw   string
	dcRequireMention bool
	slBotToken       string
	slAppToken       string
	slDMMode         string
	slAllowFromRaw   string
	waSessionID      string
	waDMMode         string
	waAllowFromRaw   string
	teamsAppID       string
	teamsAppPassword string
	teamsListenAddr  string
	teamsDMMode      string
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

// BuildOnboardSteps creates the full []OnboardWizardStep for RunOnboardWizard
// and a pointer to the shared state struct.  After the wizard exits,
// state.done == true indicates normal completion (config saved).
func BuildOnboardSteps(configPath string, existing *config.Config) ([]tui.OnboardWizardStep, *onboardState) {
	s := &onboardState{
		configPath:        configPath,
		existing:          existing,
		gwMode:            "",
		gwPort:            18790,
		gwHost:            "127.0.0.1",
		tsMode:            "off",
		secChoice:         "none",
		tgDMMode:          "pairing",
		dcDMMode:          "pairing",
		dcRequireMention:  true,
		slDMMode:          "pairing",
		waDMMode:          "pairing",
		teamsDMMode:       "allowlist",
		teamsListenAddr:   ":3978",
	}
	if existing != nil {
		populateFromExisting(s, existing)
	}

	// Per-provider auth mode strings (local to each provider's step group).
	var bedrockAuthPrimary string
	var bedrockAuthSecondary string

	var steps []tui.OnboardWizardStep

	// ── 1. AI Provider ───────────────────────────────────────────────────────
	steps = append(steps, providerSteps(
		"🧠", "AI Provider", "Select the primary LLM that powers your agent",
		&s.primary, true, &bedrockAuthPrimary, nil,
	)...)

	// ── 2. Secondary Provider ────────────────────────────────────────────────
	steps = append(steps, tui.OnboardFormStep(
		"🔀", "Secondary Provider", "Optional fallback or high-availability provider",
		func() *huh.Form {
			return huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Secondary / Fallback Provider?").
					Description("Pick a second model for high availability or specific tasks.").
					Options(
						huh.NewOption("None", "none"),
						huh.NewOption("Choose a secondary provider", "configure"),
					).
					Value(&s.secChoice),
			))
		},
	))
	steps = append(steps, providerSteps(
		"🔀", "Secondary Provider", "",
		&s.secondary, false, &bedrockAuthSecondary,
		func() bool { return s.secChoice == "configure" },
	)...)

	// ── 3. Plano Smart Routing ───────────────────────────────────────────────
	steps = append(steps, tui.OnboardConditionalFormStep(
		"⚡", "Smart Routing", "Auto-route prompts by complexity to save 30–60% on LLM costs",
		func() bool {
			return s.secChoice == "configure" &&
				s.secondary.provider != "" && s.secondary.provider != "none"
		},
		func() *huh.Form {
			hint := ""
			if !openAICompatProviders[s.primary.provider] {
				hint = "\n\n⚠ Note: your primary provider (" + s.primary.provider + ") is not OpenAI-compatible.\n" +
					"Plano will route to your secondary for complex tasks."
			}
			return huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title("Enable Smart Routing with Plano?").
					Description("Requires Docker. Runs locally on port 12000." + hint).
					Value(&s.usePlano),
			))
		},
	))
	steps = append(steps, tui.OnboardConditionalFormStep(
		"⚡", "Smart Routing — Plano Config", "",
		func() bool { return s.secChoice == "configure" && s.usePlano },
		func() *huh.Form {
			if strings.TrimSpace(s.planoEndpoint) == "" {
				s.planoEndpoint = "http://localhost:12000/v1"
			}
			fastOpts := ensureSelectValue([]huh.Option[string]{
				huh.NewOption("GPT-4o mini", "openai/gpt-4o-mini"),
				huh.NewOption("Groq Llama3 8B", "groq/llama3-8b-8192"),
				huh.NewOption("Mistral 7B", "mistral/mistral-7b-instruct"),
				huh.NewOption("Ollama Llama3.2", "ollama/llama3.2"),
				huh.NewOption("DeepSeek V2 Lite", "deepseek/deepseek-chat"),
			}, s.planoFastModel, "Current — ")
			powerOpts := ensureSelectValue([]huh.Option[string]{
				huh.NewOption("GPT-4o", "openai/gpt-4o"),
				huh.NewOption("GPT-4.1", "openai/gpt-4.1"),
				huh.NewOption("Groq Llama3 70B", "groq/llama3-70b-8192"),
				huh.NewOption("Mistral Large", "mistral/mistral-large-latest"),
				huh.NewOption("Ollama Llama3.1 70B", "ollama/llama3.1:70b"),
				huh.NewOption("DeepSeek R1", "deepseek/deepseek-r1"),
			}, s.planoPowerfulModel, "Current — ")
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("Plano endpoint").
					Description("Leave as default if running Plano locally via Docker.").
					Value(&s.planoEndpoint),
				huh.NewSelect[string]().
					Title("Fast model — for simple queries").
					Options(fastOpts...).
					Value(&s.planoFastModel),
				huh.NewSelect[string]().
					Title("Powerful model — for complex tasks").
					Options(powerOpts...).
					Value(&s.planoPowerfulModel),
			))
		},
	))
	steps = append(steps, tui.OnboardConditionalSideStep(
		"Starting Plano via Docker",
		func() bool { return s.secChoice == "configure" && s.usePlano },
		func() error {
			if !setupPlanoDocker(s.planoEndpoint) {
				return fmt.Errorf("docker setup skipped — start manually: docker run -d -p 12000:12000 katanemo/plano:latest")
			}
			return nil
		},
	))

	// ── 4. Model Routing ─────────────────────────────────────────────────────
	steps = append(steps, tui.OnboardFormStep(
		"⚡", "Model Routing", "Assign specialized models for coding and vision tasks",
		func() *huh.Form {
			if s.codingModel == "" {
				s.codingModel = "default"
			}
			if s.visionModel == "" {
				s.visionModel = "default"
			}
			codingOpts := ensureSelectValue([]huh.Option[string]{
				huh.NewOption("Use Default", "default"),
				huh.NewOption("Claude 3.5 Sonnet", "anthropic/claude-3-5-sonnet-20241022"),
				huh.NewOption("GPT-4o", "openai/gpt-4o"),
				huh.NewOption("DeepSeek-R1 (Local)", "ollama/deepseek-r1"),
			}, s.codingModel, "Current — ")
			visionOpts := ensureSelectValue([]huh.Option[string]{
				huh.NewOption("Use Default", "default"),
				huh.NewOption("Claude 3.5 Sonnet", "anthropic/claude-3-5-sonnet-20241022"),
				huh.NewOption("GPT-4o", "openai/gpt-4o"),
			}, s.visionModel, "Current — ")
			return huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Advanced Routing: Coding").
					Options(codingOpts...).
					Value(&s.codingModel),
				huh.NewSelect[string]().
					Title("Advanced Routing: Vision").
					Options(visionOpts...).
					Value(&s.visionModel),
			))
		},
	))

	// ── 5. Embeddings ────────────────────────────────────────────────────────
	steps = append(steps, tui.OnboardFormStep(
		"🔍", "Embeddings", "Semantic memory and search require an embedding model",
		func() *huh.Form {
			return huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Embedding Provider").
					Description("Used for Semantic Memory (local learning).").
					Options(
						huh.NewOption("OpenAI (Recommended)", "openai"),
						huh.NewOption("Azure OpenAI", "azure"),
						huh.NewOption("Ollama (Local)", "ollama"),
						huh.NewOption("AWS Bedrock", "bedrock"),
						huh.NewOption("Cohere", "cohere"),
						huh.NewOption("Google Generative AI", "google"),
						huh.NewOption("Voyage AI", "voyage"),
						huh.NewOption("Mistral Native", "mistral"),
						huh.NewOption("Google Vertex AI", "vertex"),
					).
					Value(&s.embed.provider),
			))
		},
	))
	// Embedding credentials (conditional on provider)
	steps = append(steps, tui.OnboardConditionalFormStep(
		"🔍", "Embeddings — credentials", "",
		func() bool { return s.embed.provider != "" && s.embed.provider != "bedrock" },
		func() *huh.Form {
			var fields []huh.Field
			switch s.embed.provider {
			case "ollama":
				if strings.TrimSpace(s.embed.baseURL) == "" {
					s.embed.baseURL = "http://localhost:11434"
				}
				fields = append(fields, huh.NewInput().Title("Ollama Base URL").Value(&s.embed.baseURL))
			case "azure":
				fields = append(fields,
					huh.NewInput().Title("Azure Endpoint").Value(&s.embed.baseURL),
					huh.NewInput().Title("Azure API Key").Password(true).Value(&s.embed.apiKey),
				)
			default:
				fields = append(fields, huh.NewInput().
					Title(fmt.Sprintf("%s API Key (Embeddings)", s.embed.provider)).
					Password(true).
					Value(&s.embed.apiKey))
			}
			embedModels := map[string][]huh.Option[string]{
				"openai":  {huh.NewOption("text-embedding-3-small", "text-embedding-3-small"), huh.NewOption("text-embedding-3-large", "text-embedding-3-large")},
				"azure":   {huh.NewOption("text-embedding-3-small", "text-embedding-3-small"), huh.NewOption("text-embedding-3-large", "text-embedding-3-large")},
				"ollama":  {huh.NewOption("nomic-embed-text", "nomic-embed-text"), huh.NewOption("mxbai-embed-large", "mxbai-embed-large")},
				"cohere":  {huh.NewOption("embed-v4.0", "embed-v4.0")},
				"google":  {huh.NewOption("text-embedding-004", "text-embedding-004")},
				"voyage":  {huh.NewOption("voyage-3", "voyage-3"), huh.NewOption("voyage-3-lite", "voyage-3-lite")},
				"mistral": {huh.NewOption("mistral-embed", "mistral-embed")},
				"vertex":  {huh.NewOption("text-embedding-004", "text-embedding-004"), huh.NewOption("text-multilingual-embedding-002", "text-multilingual-embedding-002")},
			}
			if opts, ok := embedModels[s.embed.provider]; ok {
				modelOpts := ensureSelectValue(append([]huh.Option[string](nil), opts...), s.embed.model, "Current — ")
				fields = append(fields, huh.NewSelect[string]().
					Title("Embedding Model").
					Options(modelOpts...).
					Value(&s.embed.model))
			} else {
				fields = append(fields, huh.NewInput().Title("Embedding Model ID").Value(&s.embed.model))
			}
			return huh.NewForm(huh.NewGroup(fields...))
		},
	))

	// ── 6. Local Intelligence (side-effect only) ──────────────────────────────
	steps = append(steps, tui.OnboardConditionalSideStep(
		"Preparing Local Gemma model",
		func() bool { return strings.TrimSpace(s.localIntelGGUF) == "" },
		func() error {
			// Check env var first
			if p := strings.TrimSpace(os.Getenv("OPSINTELLIGENCE_LOCAL_GEMMA_GGUF")); p != "" {
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					s.localIntelGGUF = p
					s.localIntelEnabled = true
					return nil
				}
			}
			stateDir := filepath.Dir(s.configPath)
			dst := localintel.DefaultGGUFPath(stateDir)
			if src, ok := discoverBundledGGUF(dst); ok {
				if err := copyFileAtomic(src, dst); err != nil {
					return fmt.Errorf("copy bundled Gemma: %w", err)
				}
				s.localIntelGGUF = dst
				s.localIntelEnabled = true
				return nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Minute)
			defer cancel()
			res, err := localintel.BootstrapGGUF(ctx, localintel.BootstrapOptions{
				StateDir: stateDir,
				GGUFPath: dst,
			})
			if err != nil {
				return fmt.Errorf("Gemma download skipped: %w — run later: opsintelligence local-intel setup", err)
			}
			s.localIntelGGUF = res.Path
			s.localIntelEnabled = true
			return nil
		},
	))

	// ── 7. Memory ────────────────────────────────────────────────────────────
	steps = append(steps, tui.OnboardFormStep(
		"🧩", "Memory", "Structured hierarchical memory (requires Python 3.9+)",
		func() *huh.Form {
			return huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title("Set up MemPalace now?").
					Description("Creates a Python venv and installs the mempalace PyPI package.\nSkip safely — run `opsintelligence quickstart` later.").
					Value(&s.setupMP),
			))
		},
	))
	steps = append(steps, tui.OnboardConditionalSideStep(
		"Installing MemPalace",
		func() bool { return s.setupMP },
		func() error {
			stateDir := filepath.Dir(s.configPath)
			opts := tui.SetupOptions{StateDir: stateDir}
			if err := tui.RunMemPalaceSetup(context.Background(), opts); err != nil {
				return fmt.Errorf("MemPalace setup failed: %w — retry: opsintelligence quickstart", err)
			}
			s.memPalaceEnabled = true
			return nil
		},
	))

	// ── 8. Gateway & Access ──────────────────────────────────────────────────
	steps = append(steps, tui.OnboardFormStep(
		"🌐", "Gateway & Access", "Configure how the agent API is exposed on your network",
		func() *huh.Form {
			portStr := fmt.Sprint(s.gwPort)
			portField := huh.NewInput().Title("Gateway Port").Value(&portStr).Validate(func(v string) error {
				var p int
				if _, err := fmt.Sscan(v, &p); err != nil || p < 1 || p > 65535 {
					return fmt.Errorf("invalid port")
				}
				s.gwPort = p
				return nil
			})
			return huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Remote Access Mode").
					Options(
						huh.NewOption("Local Only (127.0.0.1)", "loopback"),
						huh.NewOption("Local Network (LAN - 0.0.0.0)", "lan"),
						huh.NewOption("Tailscale (Secure VPN)", "tailscale"),
					).
					Value(&s.gwMode),
				huh.NewInput().Title("Gateway Host").Value(&s.gwHost),
				portField,
				huh.NewInput().Title("Security Token").
					Description("Password to protect your Gateway API. Leave empty for auto-generate.").
					Value(&s.gwToken),
			))
		},
	))
	// Auto-generate token side-effect
	steps = append(steps, tui.OnboardConditionalSideStep(
		"Generating Gateway token",
		func() bool { return strings.TrimSpace(s.gwToken) == "" },
		func() error {
			s.gwToken = randomToken(24)
			return nil
		},
	))
	// Tailscale mode + hostname form (conditional on Tailscale bind being selected)
	steps = append(steps, tui.OnboardConditionalFormStep(
		"🌐", "Gateway — Tailscale", "",
		func() bool { return s.gwMode == "tailscale" },
		func() *huh.Form {
			// Auto-detect the machine's Tailscale FQDN so the user can confirm it.
			detectedHost := detectTailscaleHostname()
			if detectedHost != "" && placeholderGatewayHost(s.gwHost) {
				s.gwHost = detectedHost
			}
			return huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Tailscale Mode").
					Options(
						huh.NewOption("Off", "off"),
						huh.NewOption("Serve (Local Tailnet)", "serve"),
						huh.NewOption("Funnel (Public Internet via Tailscale)", "funnel"),
					).
					Value(&s.tsMode),
				huh.NewInput().
					Title("Tailscale Hostname").
					Description("Your machine's Tailscale FQDN (e.g. opsintelligence.tail1234.ts.net).\nAuto-detected when possible — required for Funnel public URLs.").
					Value(&s.gwHost).
					Validate(func(v string) error {
						if strings.EqualFold(strings.TrimSpace(s.tsMode), "funnel") && placeholderGatewayHost(v) {
							return fmt.Errorf("need a real *.ts.net hostname for Funnel (Tailscale menu → CLI integration, or set OPSINTELLIGENCE_TAILSCALE_BIN); not localhost")
						}
						return nil
					}),
			))
		},
	))

	// Host Tailscale Funnel step — shown for loopback/LAN bind so users can
	// expose the gateway via the host machine's existing Tailscale installation
	// without needing the embedded tsnet node and a separate auth key.
	steps = append(steps, tui.OnboardConditionalFormStep(
		"🌐", "Gateway — Expose via Tailscale Funnel", "",
		func() bool { return s.gwMode == "loopback" || s.gwMode == "lan" },
		func() *huh.Form {
			// Ensure tsMode starts at "off" unless already set to funnel.
			if !strings.EqualFold(strings.TrimSpace(s.tsMode), "funnel") {
				s.tsMode = "off"
			}
			// Auto-detect machine hostname so the webhook URL is accurate.
			if detectedHost := detectTailscaleHostname(); detectedHost != "" && placeholderGatewayHost(s.gwHost) {
				s.gwHost = detectedHost
			}
			return huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("Expose via Tailscale Funnel?").
					Description("Runs `tailscale funnel <port>` on startup so Microsoft's Bot\nFramework servers can reach your gateway for Teams webhooks.").
					Options(
						huh.NewOption("No — keep local only", "off"),
						huh.NewOption("Yes — use host Tailscale Funnel", "funnel"),
					).
					Value(&s.tsMode),
				huh.NewInput().
					Title("Machine Tailscale Hostname").
					Description("Your Tailscale FQDN (e.g. myhost.tail1234.ts.net).\nAuto-detected when possible — needed for the Teams webhook URL.").
					Value(&s.gwHost).
					Validate(func(v string) error {
						if strings.EqualFold(strings.TrimSpace(s.tsMode), "funnel") && placeholderGatewayHost(v) {
							return fmt.Errorf("need your machine's *.ts.net hostname (run: tailscale status)")
						}
						return nil
					}),
			))
		},
	))

	// ── 9. Messaging Channels ────────────────────────────────────────────────
	steps = append(steps, tui.OnboardFormStep(
		"💬", "Messaging Channels", "Connect Telegram, Slack, Discord, WhatsApp, or Teams",
		func() *huh.Form {
			tgLabel := "Telegram"
			dcLabel := "Discord"
			slLabel := "Slack"
			waLabel := "WhatsApp"
			msLabel := "Microsoft Teams"
			if s.existing != nil {
				if s.existing.Channels.Telegram != nil && s.existing.Channels.Telegram.BotToken != "" {
					tgLabel = "Telegram ✓"
				}
				if s.existing.Channels.Discord != nil && s.existing.Channels.Discord.BotToken != "" {
					dcLabel = "Discord ✓"
				}
				if s.existing.Channels.Slack != nil && s.existing.Channels.Slack.BotToken != "" {
					slLabel = "Slack ✓"
				}
				if s.existing.Channels.WhatsApp != nil {
					waLabel = "WhatsApp ✓"
				}
				if s.existing.Channels.Teams != nil && s.existing.Channels.Teams.AppID != "" {
					msLabel = "Microsoft Teams ✓"
				}
			}
			return huh.NewForm(huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title("Select messaging channels to enable").
					Description("Configure credentials for each selected channel next.").
					Options(
						huh.NewOption(tgLabel, "telegram"),
						huh.NewOption(dcLabel, "discord"),
						huh.NewOption(slLabel, "slack"),
						huh.NewOption(waLabel, "whatsapp"),
						huh.NewOption(msLabel, "msteams"),
					).
					Value(&s.selectedChannels),
			))
		},
	))
	// Per-channel credential forms
	steps = append(steps, tui.OnboardConditionalFormStep(
		"💬", "Telegram Setup", "",
		func() bool { return containsStr(s.selectedChannels, "telegram") },
		func() *huh.Form {
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().Title("Telegram Bot Token").Value(&s.tgBotToken),
				huh.NewSelect[string]().Title("Telegram Security Mode").
					Options(channelModeOptions()...).Value(&s.tgDMMode),
				huh.NewInput().Title("Whitelisted Telegram IDs").
					Description("Comma-separated IDs/usernames. Only for Allowlist mode.").
					Value(&s.tgAllowFromRaw),
			))
		},
	))
	steps = append(steps, tui.OnboardConditionalFormStep(
		"💬", "Discord Setup", "",
		func() bool { return containsStr(s.selectedChannels, "discord") },
		func() *huh.Form {
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().Title("Discord Bot Token").Value(&s.dcBotToken),
				huh.NewSelect[string]().Title("Discord Security Mode").
					Options(channelModeOptions()...).Value(&s.dcDMMode),
				huh.NewInput().Title("Whitelisted Discord IDs").
					Description("Comma-separated numeric IDs. Only for Allowlist mode.").
					Value(&s.dcAllowFromRaw),
				huh.NewConfirm().
					Title("Require @bot mention in server channels?").
					Description("Recommended: avoids noise in busy guild channels. DMs are unaffected.").
					Value(&s.dcRequireMention),
			))
		},
	))
	steps = append(steps, tui.OnboardConditionalFormStep(
		"💬", "Slack Setup", "",
		func() bool { return containsStr(s.selectedChannels, "slack") },
		func() *huh.Form {
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().Title("Slack Bot Token").Value(&s.slBotToken),
				huh.NewInput().Title("Slack App Token").Value(&s.slAppToken),
				huh.NewSelect[string]().Title("Slack Security Mode").
					Options(channelModeOptions()...).Value(&s.slDMMode),
				huh.NewInput().Title("Whitelisted Slack IDs").
					Description("Comma-separated numeric IDs. Only for Allowlist mode.").
					Value(&s.slAllowFromRaw),
			))
		},
	))
	steps = append(steps, tui.OnboardConditionalFormStep(
		"💬", "WhatsApp Setup", "",
		func() bool { return containsStr(s.selectedChannels, "whatsapp") },
		func() *huh.Form {
			if strings.TrimSpace(s.waSessionID) == "" {
				s.waSessionID = "personal"
			}
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("WhatsApp Session ID").
					Description("A name for this session (e.g. 'personal'). QR scan required after setup.").
					Value(&s.waSessionID),
				huh.NewSelect[string]().Title("WhatsApp Security Mode").
					Options(channelModeOptions()...).Value(&s.waDMMode),
				huh.NewInput().Title("Whitelisted Numbers").
					Description("Comma-separated (e.g. '1234567890, 9876543210'). Only for Allowlist mode.").
					Value(&s.waAllowFromRaw),
			))
		},
	))
	steps = append(steps, tui.OnboardConditionalFormStep(
		"💬", "Microsoft Teams Setup", "",
		func() bool { return containsStr(s.selectedChannels, "msteams") },
		func() *huh.Form {
			funnelMode := s.gwMode == "tailscale" && strings.EqualFold(strings.TrimSpace(s.tsMode), "funnel")
			appIDDesc := "Microsoft App ID from your Azure Bot registration."
			if funnelMode {
				appIDDesc = "Microsoft App ID from your Azure Bot registration.\nWebhook will be served via Tailscale Funnel at <your-funnel-url>/teams/api/messages"
			}
			fields := []huh.Field{
				huh.NewInput().Title("Azure Bot App ID").
					Description(appIDDesc).
					Value(&s.teamsAppID),
				huh.NewInput().Title("Azure Bot App Password").Password(true).
					Value(&s.teamsAppPassword),
			}
			// In Tailscale Funnel mode Teams mounts on the gateway — no separate listen addr needed.
			if !funnelMode {
				fields = append(fields,
					huh.NewInput().Title("Webhook Listen Address").
						Description("Port for Bot Framework webhook (default :3978).").
						Value(&s.teamsListenAddr),
				)
			}
			fields = append(fields,
				huh.NewSelect[string]().Title("Teams Security Mode").
					Options(
						huh.NewOption("Allowlist (Recommended)", "allowlist"),
						huh.NewOption("Open (any authenticated Teams user)", "open"),
						huh.NewOption("Disabled", "disabled"),
					).
					Value(&s.teamsDMMode),
				huh.NewInput().Title("Allowed Teams User IDs").
					Description("Comma-separated AAD object IDs. Leave empty for open mode.").
					Value(&s.teamsAllowFromRaw),
			)
			return huh.NewForm(huh.NewGroup(fields...))
		},
	))

	// ── 10. Skills ───────────────────────────────────────────────────────────
	steps = append(steps, tui.OnboardFormStep(
		"🛠", "Skills", "Install built-in and custom agent skills",
		func() *huh.Form {
			home, _ := os.UserHomeDir()
			bundledDir := filepath.Join(home, ".opsintelligence", "skills", "bundled")
			customDir := filepath.Join(home, ".opsintelligence", "skills", "custom")
			_ = os.MkdirAll(bundledDir, 0o755)
			_ = os.MkdirAll(customDir, 0o755)
			if src := resolveBundledSkillsSrc(); src != "" {
				_ = skills.CopyDir(src, bundledDir)
			}
			mp := skills.NewMarketplace(bundledDir, customDir)
			idx, err := mp.FetchIndex(context.Background())
			if err != nil {
				// Index unavailable — let the user continue without skill selection.
				return huh.NewForm(huh.NewGroup(
					huh.NewNote().
						Title("Skills").
						Description("Could not reach the skills index.\nYou can enable skills later by editing your config."),
				))
			}
			const customSentinel = "__custom__"
			var opts []huh.Option[string]
			inIndex := make(map[string]bool)
			for _, e := range idx.Skills {
				inIndex[e.Name] = true
				label := e.Name
				if e.Emoji != "" {
					label = e.Emoji + "  " + e.Name
				}
				if e.Description != "" {
					desc := e.Description
					if len(desc) > 72 {
						desc = desc[:71] + "…"
					}
					label += "\n     " + desc
				}
				opts = append(opts, huh.NewOption(label, e.Name))
			}
			for _, name := range s.selectedSkills {
				if name != "" && !inIndex[name] {
					opts = append(opts, huh.NewOption(name+" (from config)", name))
				}
			}
			opts = append(opts, huh.NewOption("＋  Add custom skill  (local path or URL)", customSentinel))
			return huh.NewForm(
				huh.NewGroup(
					huh.NewMultiSelect[string]().
						Title("Skills to enable").
						Description("Space toggles · Enter confirms. Bundled skills fetch automatically when missing.").
						Options(opts...).
						Value(&s.selectedSkills),
				).Title("Agent skills"),
			)
		},
	))
	// Custom skill path form (conditional on __custom__ being in selection)
	var customSkillPath string
	steps = append(steps, tui.OnboardConditionalFormStep(
		"🛠", "Custom Skill", "",
		func() bool { return containsStr(s.selectedSkills, "__custom__") },
		func() *huh.Form {
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("Custom skill path or URL").
					Placeholder("/path/to/skill  or  https://github.com/user/skill").
					Value(&customSkillPath),
			))
		},
	))
	// Skills install side-effect
	steps = append(steps, tui.OnboardSideStep(
		"Installing selected skills",
		func() error {
			home, _ := os.UserHomeDir()
			customDir := filepath.Join(home, ".opsintelligence", "skills", "custom")
			bundledDir := filepath.Join(home, ".opsintelligence", "skills", "bundled")
			mp := skills.NewMarketplace(bundledDir, customDir)
			var kept []string
			for _, n := range s.selectedSkills {
				if n == "" || n == "__custom__" {
					// Handle custom path install
					if n == "__custom__" && strings.TrimSpace(customSkillPath) != "" {
						dest, err := mp.InstallFromPath(customSkillPath)
						if err != nil {
							dest, err = mp.Install(context.Background(), customSkillPath)
						}
						if err == nil {
							kept = append(kept, filepath.Base(dest))
						}
					}
					continue
				}
				dest := filepath.Join(customDir, n)
				if _, err := os.Stat(dest); os.IsNotExist(err) {
					if _, err := mp.Install(context.Background(), n); err != nil {
						continue
					}
				}
				kept = append(kept, n)
			}
			s.selectedSkills = kept
			return nil
		},
	))

	// ── 11. DevOps Integrations ──────────────────────────────────────────────
	steps = append(steps, tui.OnboardFormStep(
		"⚙", "DevOps Integrations", "GitHub, GitLab, Jenkins, SonarQube (all optional)",
		func() *huh.Form {
			if s.existing != nil {
				d := s.existing.DevOps
				if s.githubToken != "" || strings.TrimSpace(s.githubTokenEnv) != "" ||
					s.gitlabToken != "" || strings.TrimSpace(s.gitlabTokenEnv) != "" ||
					(strings.TrimSpace(s.jenkinsURL) != "" && (s.jenkinsToken != "" || strings.TrimSpace(s.jenkinsTokenEnv) != "")) ||
					(strings.TrimSpace(s.sonarURL) != "" && (s.sonarToken != "" || strings.TrimSpace(s.sonarTokenEnv) != "")) {
					s.configureDevOps = true
				}
				_ = d
			}
			return huh.NewForm(
				huh.NewGroup(
					huh.NewConfirm().
						Title("Configure DevOps API integrations now?").
						Description("GitHub PAT, GitLab, Jenkins, Sonar — used for PR review and REST APIs.\nSkip to leave existing devops: block unchanged.").
						Value(&s.configureDevOps),
				).Title("DevOps integration"),
			)
		},
	))
	steps = append(steps, tui.OnboardConditionalFormStep(
		"⚙", "DevOps — Credentials", "",
		func() bool { return s.configureDevOps },
		func() *huh.Form {
			return huh.NewForm(
				huh.NewGroup(
					huh.NewInput().Title("GitHub API base URL (optional)").
						Description("Leave blank for https://api.github.com.").Value(&s.githubBaseURL),
					huh.NewInput().Title("GitHub default org (optional)").Value(&s.githubDefaultOrg),
					huh.NewInput().Title("GitHub personal access token (optional)").Password(true).Value(&s.githubToken),
					huh.NewInput().Title("Or: GitHub token env var (optional)").
						Description("e.g. GITHUB_TOKEN").Value(&s.githubTokenEnv),
					huh.NewInput().Title("GitLab base URL (optional)").Value(&s.gitlabURL),
					huh.NewInput().Title("GitLab token (optional)").Password(true).Value(&s.gitlabToken),
					huh.NewInput().Title("Or: GitLab token env var (optional)").Value(&s.gitlabTokenEnv),
					huh.NewInput().Title("Jenkins base URL (optional)").Value(&s.jenkinsURL),
					huh.NewInput().Title("Jenkins user (optional)").Value(&s.jenkinsUser),
					huh.NewInput().Title("Jenkins token (optional)").Password(true).Value(&s.jenkinsToken),
					huh.NewInput().Title("Or: Jenkins token env var (optional)").Value(&s.jenkinsTokenEnv),
					huh.NewInput().Title("SonarQube base URL (optional)").Value(&s.sonarURL),
					huh.NewInput().Title("SonarQube token (optional)").Password(true).Value(&s.sonarToken),
					huh.NewInput().Title("Or: Sonar token env var (optional)").Value(&s.sonarTokenEnv),
					huh.NewInput().Title("Sonar project key prefix (optional)").Value(&s.sonarProjectPrefix),
				).Title("DevOps credentials"),
			)
		},
	))
	steps = append(steps, tui.OnboardFormStep(
		"⚙", "GitHub Webhook", "Enable GitHub → gateway webhook adapter",
		func() *huh.Form {
			return huh.NewForm(huh.NewGroup(
				huh.NewConfirm().
					Title("Enable GitHub → gateway webhook adapter?").
					Description("When enabled, set the same signing secret in GitHub → Settings → Webhooks.\nSkip to leave existing webhooks: block unchanged.").
					Value(&s.ghWebhookEnabled),
			))
		},
	))
	steps = append(steps, tui.OnboardConditionalFormStep(
		"⚙", "GitHub Webhook — Secret", "",
		func() bool { return s.ghWebhookEnabled },
		func() *huh.Form {
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().
					Title("Webhook signing secret").
					Description("Repository/org webhook secret (X-Hub-Signature-256). Not your PAT.").
					Password(true).
					Value(&s.ghWebhookSecret),
			))
		},
	))

	// ── 12. Save configuration ───────────────────────────────────────────────
	steps = append(steps, tui.OnboardSideStep(
		"Merging and saving configuration",
		func() error {
			yaml := buildConfigYAML(s)
			merged, err := mergeOnboardYAML(s.configPath, []byte(yaml))
			if err != nil {
				return fmt.Errorf("merge: %w", err)
			}
			if err := os.MkdirAll(filepath.Dir(s.configPath), 0o755); err != nil {
				return err
			}
			if err := os.WriteFile(s.configPath, merged, 0o600); err != nil {
				return err
			}
			s.done = true
			return nil
		},
	))

	// ── 13. Login service install ────────────────────────────────────────────
	steps = append(steps, tui.OnboardConditionalSideStep(
		"Registering login service (auto-start)",
		func() bool { return runtime.GOOS == "darwin" || runtime.GOOS == "linux" },
		func() error {
			if root := filepath.Dir(s.configPath); root != "" && root != "." {
				_ = os.Setenv("OPSINTELLIGENCE_STATE_DIR", root)
			}
			return installService()
		},
	))

	return steps, s
}

// ── Provider step builder ─────────────────────────────────────────────────────

// providerSteps returns 4–5 conditional form steps for a single provider
// (select → bedrock auth mode → credentials → model → custom model).
// extraCond, when non-nil, is AND-ed with each step's own condition.
func providerSteps(
	icon, title, subtitle string,
	entry *provEntry,
	isPrimary bool,
	bedrockAuthMode *string,
	extraCond func() bool,
) []tui.OnboardWizardStep {
	cond := func(inner func() bool) func() bool {
		return func() bool {
			if extraCond != nil && !extraCond() {
				return false
			}
			return inner == nil || inner()
		}
	}

	allPrimary := []huh.Option[string]{
		huh.NewOption("Anthropic (Recommended)", "anthropic"),
		huh.NewOption("OpenAI", "openai"),
		huh.NewOption("Ollama (Local / Free)", "ollama"),
		huh.NewOption("AWS Bedrock", "bedrock"),
		huh.NewOption("Google Vertex AI", "vertex"),
		huh.NewOption("Groq", "groq"),
		huh.NewOption("Mistral", "mistral"),
		huh.NewOption("DeepSeek", "deepseek"),
		huh.NewOption("xAI (Grok)", "xai"),
		huh.NewOption("Perplexity", "perplexity"),
		huh.NewOption("OpenRouter", "openrouter"),
		huh.NewOption("NVIDIA NIM", "nvidia"),
		huh.NewOption("Together AI", "together"),
		huh.NewOption("HuggingFace (Inference API)", "huggingface"),
		huh.NewOption("Cohere", "cohere"),
		huh.NewOption("Azure OpenAI", "azure"),
		huh.NewOption("vLLM (Local / Custom)", "vllm"),
		huh.NewOption("LM Studio (Local)", "lmstudio"),
	}
	allSecondary := []huh.Option[string]{
		huh.NewOption("Ollama (Local / Free)", "ollama"),
		huh.NewOption("OpenAI", "openai"),
		huh.NewOption("Groq (Super Fast)", "groq"),
		huh.NewOption("Anthropic", "anthropic"),
		huh.NewOption("Mistral", "mistral"),
		huh.NewOption("OpenRouter", "openrouter"),
		huh.NewOption("Azure OpenAI", "azure"),
		huh.NewOption("DeepSeek", "deepseek"),
		huh.NewOption("xAI (Grok)", "xai"),
		huh.NewOption("Perplexity", "perplexity"),
		huh.NewOption("AWS Bedrock", "bedrock"),
		huh.NewOption("Google Vertex AI", "vertex"),
		huh.NewOption("NVIDIA NIM", "nvidia"),
		huh.NewOption("Together AI", "together"),
		huh.NewOption("HuggingFace (Inference API)", "huggingface"),
		huh.NewOption("Cohere", "cohere"),
		huh.NewOption("vLLM (Local / Custom)", "vllm"),
		huh.NewOption("LM Studio (Local)", "lmstudio"),
	}
	provOpts := allSecondary
	if isPrimary {
		provOpts = allPrimary
	}
	provTitle := "Which primary AI provider would you like to use?"
	if !isPrimary {
		provTitle = "Which secondary AI provider would you like to use?"
	}

	// Step 1: provider select
	s1 := tui.OnboardWizardStep{
		Icon: icon, Title: title, Subtitle: subtitle,
		Condition: cond(nil),
		MakeForm: func() *huh.Form {
			// Reset stale sub-fields when user changes provider
			prevProv := entry.provider
			_ = prevProv
			return huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title(provTitle).
					Options(provOpts...).
					Value(&entry.provider),
			))
		},
	}

	// Step 2: Bedrock auth mode
	s2 := tui.OnboardWizardStep{
		Icon: icon, Title: title,
		Condition: cond(func() bool { return entry.provider == "bedrock" }),
		MakeForm: func() *huh.Form {
			if *bedrockAuthMode == "" {
				*bedrockAuthMode = inferBedrockAuthMode(*entry)
			}
			if strings.TrimSpace(entry.awsRegion) == "" {
				entry.awsRegion = "us-east-1"
			}
			return huh.NewForm(huh.NewGroup(
				huh.NewSelect[string]().
					Title("AWS Bedrock Authentication").
					Description("Tip: Bearer Tokens are most stable in us-east-1.").
					Options(
						huh.NewOption("Direct IAM Keys (AccessKeyID/SecretKey)", "iam"),
						huh.NewOption("AWS Named Profile (~/.aws/credentials)", "profile"),
						huh.NewOption("Native Bedrock API Key (Bearer Token)", "api_key"),
					).
					Value(bedrockAuthMode),
			))
		},
	}

	// Step 3: credentials
	needsCreds := func() bool {
		needsKey := map[string]bool{
			"anthropic": true, "openai": true, "groq": true, "mistral": true,
			"openrouter": true, "azure": true, "deepseek": true, "perplexity": true,
			"xai": true, "together": true, "nvidia": true, "cohere": true,
			"huggingface": true, "voyage": true,
		}
		needsURL := map[string]bool{
			"ollama": true, "vllm": true, "lmstudio": true,
		}
		return needsKey[entry.provider] || needsURL[entry.provider] ||
			entry.provider == "bedrock" || entry.provider == "vertex" || entry.provider == "azure"
	}
	s3 := tui.OnboardWizardStep{
		Icon: icon, Title: title,
		Condition: cond(needsCreds),
		MakeForm: func() *huh.Form {
			var fields []huh.Field
			needsKey := map[string]bool{
				"anthropic": true, "openai": true, "groq": true, "mistral": true,
				"openrouter": true, "azure": true, "deepseek": true, "perplexity": true,
				"xai": true, "together": true, "nvidia": true, "cohere": true,
				"huggingface": true, "voyage": true,
			}
			defaultURL := map[string]string{
				"ollama": "http://localhost:11434", "vllm": "http://localhost:8000/v1",
				"lmstudio": "http://localhost:1234/v1", "azure": "https://YOUR_RESOURCE_NAME.openai.azure.com",
			}
			if needsKey[entry.provider] {
				fields = append(fields, huh.NewInput().Title("Enter API Key").
					Description("Stored safely in your local configuration.").Password(true).Value(&entry.apiKey))
			}
			if def, ok := defaultURL[entry.provider]; ok {
				if strings.TrimSpace(entry.baseURL) == "" {
					entry.baseURL = def
				}
				fields = append(fields, huh.NewInput().
					Title(fmt.Sprintf("Base URL (default: %s)", def)).Value(&entry.baseURL))
			}
			if entry.provider == "azure" {
				fields = append(fields, huh.NewInput().
					Title("Azure API Version (e.g., 2024-02-15-preview)").Value(&entry.apiVersion))
			}
			if entry.provider == "bedrock" {
				fields = append(fields, huh.NewInput().Title("AWS Region").Value(&entry.awsRegion))
				switch *bedrockAuthMode {
				case "iam":
					fields = append(fields,
						huh.NewInput().Title("AWS Access Key ID").Value(&entry.awsAccessKey),
						huh.NewInput().Title("AWS Secret Access Key").Password(true).Value(&entry.awsSecretKey),
					)
				case "profile":
					if entry.awsProfile == "" {
						entry.awsProfile = "default"
					}
					fields = append(fields, huh.NewInput().Title("AWS Profile").Value(&entry.awsProfile))
				case "api_key":
					fields = append(fields, huh.NewInput().Title("Bedrock API Key").Password(true).Value(&entry.apiKey))
				}
			}
			if entry.provider == "vertex" {
				fields = append(fields,
					huh.NewInput().Title("GCP Project ID").Value(&entry.vertexProj),
					huh.NewInput().Title("GCP Location (e.g. us-central1)").Value(&entry.vertexLoc),
					huh.NewInput().Title("Service Account JSON Path (Optional)").Value(&entry.vertexCreds),
				)
			}
			if len(fields) == 0 {
				fields = append(fields, huh.NewNote().Title(title).Description("No credentials needed for this provider."))
			}
			return huh.NewForm(huh.NewGroup(fields...))
		},
	}

	// Step 4: model select
	modelChoices := map[string][]huh.Option[string]{
		"anthropic": {
			huh.NewOption("Claude 3.7 Sonnet (Latest)", "claude-3-7-sonnet-20250219"),
			huh.NewOption("Claude 3.5 Sonnet (Classic)", "claude-3-5-sonnet-20241022"),
			huh.NewOption("Claude 3.5 Haiku (Fast)", "claude-3-5-haiku-20241022"),
			huh.NewOption("Other / Custom...", "custom"),
		},
		"openai": {
			huh.NewOption("GPT-4o (Smartest)", "gpt-4o"),
			huh.NewOption("GPT-4o-mini (Efficient)", "gpt-4o-mini"),
			huh.NewOption("o3-mini (Reasoning)", "o3-mini"),
			huh.NewOption("Other / Custom...", "custom"),
		},
		"ollama": {
			huh.NewOption("Llama 3.2 (3B)", "llama3.2"),
			huh.NewOption("Mistral (7B)", "mistral"),
			huh.NewOption("DeepSeek-R1 (70B Distill)", "deepseek-r1"),
			huh.NewOption("Other / Custom...", "custom"),
		},
		"groq":      {huh.NewOption("Llama 3.3 70B Versatile", "llama-3.3-70b-versatile"), huh.NewOption("Mixtral 8x7B", "mixtral-8x7b-32768"), huh.NewOption("Other / Custom...", "custom")},
		"deepseek":  {huh.NewOption("DeepSeek Chat", "deepseek-chat"), huh.NewOption("DeepSeek Reasoner (R1)", "deepseek-reasoner"), huh.NewOption("Other / Custom...", "custom")},
		"perplexity": {huh.NewOption("Sonar Reasoning Pro", "sonar-reasoning-pro"), huh.NewOption("Sonar Pro", "sonar-pro"), huh.NewOption("Other / Custom...", "custom")},
		"xai":       {huh.NewOption("Grok 4", "grok-4-latest"), huh.NewOption("Grok Beta", "grok-beta"), huh.NewOption("Other / Custom...", "custom")},
		"vertex":    {huh.NewOption("Gemini 1.5 Pro", "gemini-1.5-pro"), huh.NewOption("Gemini 1.5 Flash", "gemini-1.5-flash"), huh.NewOption("Gemini 2.0 Flash Exp", "gemini-2.0-flash-exp"), huh.NewOption("Other / Custom...", "custom")},
	}
	s4 := tui.OnboardWizardStep{
		Icon: icon, Title: title,
		Condition: cond(nil),
		MakeForm: func() *huh.Form {
			// Bedrock: fetch live model list (blocks briefly with spinner visible above)
			if entry.provider == "bedrock" {
				ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
				defer cancel()
				picks := bedrock.ListOnboardingTextModels(ctx, bedrock.Config{
					Region:          strings.TrimSpace(entry.awsRegion),
					Profile:         strings.TrimSpace(entry.awsProfile),
					AccessKeyID:     strings.TrimSpace(entry.awsAccessKey),
					SecretAccessKey: strings.TrimSpace(entry.awsSecretKey),
					APIKey:          strings.TrimSpace(entry.apiKey),
				})
				var opts []huh.Option[string]
				for _, p := range picks {
					opts = append(opts, huh.NewOption(p.Label, p.ID))
				}
				if len(opts) == 0 {
					opts = []huh.Option[string]{
						huh.NewOption("Claude 3.5 Sonnet v2", "anthropic.claude-3-5-sonnet-20241022-v2:0"),
						huh.NewOption("Claude 3 Haiku", "anthropic.claude-3-haiku-20240307-v1:0"),
						huh.NewOption("Llama 3.1 70B Instruct", "meta.llama3-1-70b-instruct-v1:0"),
					}
				}
				opts = append(opts, huh.NewOption("Other / Custom...", "custom"))
				opts = ensureSelectValue(opts, entry.model, "Current — ")
				return huh.NewForm(huh.NewGroup(
					huh.NewSelect[string]().Title("Select Bedrock Model").
						Description("Lists ON_DEMAND text models in your region.").
						Options(opts...).Value(&entry.model),
				))
			}
			if opts, ok := modelChoices[entry.provider]; ok {
				modelOpts := ensureSelectValue(append([]huh.Option[string](nil), opts...), entry.model, "Current — ")
				return huh.NewForm(huh.NewGroup(
					huh.NewSelect[string]().Title("Select Model").Options(modelOpts...).Value(&entry.model),
				))
			}
			// Providers without a curated list — free-text input
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().Title("Enter Model ID").Value(&entry.model),
			))
		},
	}

	// Step 5: custom model
	s5 := tui.OnboardWizardStep{
		Icon: icon, Title: title,
		Condition: cond(func() bool { return entry.model == "custom" }),
		MakeForm: func() *huh.Form {
			return huh.NewForm(huh.NewGroup(
				huh.NewInput().Title("Enter Custom Model ID").Value(&entry.model),
			))
		},
	}

	return []tui.OnboardWizardStep{s1, s2, s3, s4, s5}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func channelModeOptions() []huh.Option[string] {
	return []huh.Option[string]{
		huh.NewOption("Pairing (Recommended)", "pairing"),
		huh.NewOption("Allowlist only", "allowlist"),
		huh.NewOption("Open (Public)", "open"),
		huh.NewOption("Disabled", "disabled"),
	}
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
